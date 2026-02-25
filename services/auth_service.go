package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"net/smtp"
	"sync"
	"time"
	"weoucbookcycle_go/config"
	"weoucbookcycle_go/models"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

var (
	redisCtx = context.Background()
)

// EmailConfig 邮件配置
type EmailConfig struct {
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	FromEmail    string
	FromName     string
}

// AuthConfig 认证配置
type AuthConfig struct {
	MaxLoginAttempts     int           // 最大登录失败次数
	LoginBlockDuration   time.Duration // 登录封禁时长
	RegisterLimitPerHour int           // 每小时最大注册次数
}

// AuthService 认证服务
type AuthService struct {
	jwtService  *config.JWTService
	emailConfig *EmailConfig
	authConfig  *AuthConfig
	// 邮件发送队列（使用goroutine异步处理）
	emailQueue   chan *EmailTask
	emailWorkers int
	// 登录失败记录队列
	loginFailureQueue chan *LoginFailure
	// IP封禁检查缓存
	ipBlockCache sync.Map // IP -> BlockInfo
}

// EmailTask 邮件发送任务
type EmailTask struct {
	Type      string // "welcome", "verification", "password_reset", "password_changed"
	ToEmail   string
	Subject   string
	Body      string
	HTMLBody  string
	Timestamp time.Time
	Retries   int
}

// LoginFailure 登录失败记录
type LoginFailure struct {
	Email     string
	IP        string
	Timestamp time.Time
	UserAgent string
}

// BlockInfo IP封禁信息
type BlockInfo struct {
	UnblockTime time.Time
	Reason      string
}

// NewAuthService 创建认证服务实例
func NewAuthService() *AuthService {
	emailConfig := &EmailConfig{
		SMTPHost:     config.GetEnv("SMTP_HOST", "smtp.gmail.com"),
		SMTPPort:     587,
		SMTPUser:     config.GetEnv("SMTP_USER", ""),
		SMTPPassword: config.GetEnv("SMTP_PASSWORD", ""),
		FromEmail:    config.GetEnv("FROM_EMAIL", "noreply@weoucbookcycle.com"),
		FromName:     config.GetEnv("FROM_NAME", "WeOUC BookCycle"),
	}

	authConfig := &AuthConfig{
		MaxLoginAttempts:     5,
		LoginBlockDuration:   15 * time.Minute,
		RegisterLimitPerHour: 3,
	}

	authService := &AuthService{
		jwtService:        config.GetJWTService(),
		emailConfig:       emailConfig,
		authConfig:        authConfig,
		emailQueue:        make(chan *EmailTask, 1000),
		emailWorkers:      5,
		loginFailureQueue: make(chan *LoginFailure, 1000),
	}

	// 启动邮件发送worker池
	authService.startEmailWorkers()

	// 启动登录失败处理worker
	authService.startLoginFailureWorker()

	// 启动IP封禁检查清理goroutine
	go authService.cleanupIPBlocks()

	return authService
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,max=100"`
}

// LoginRequest 登录请求
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// ==================== 注册相关方法 ====================

// Register 用户注册
func (as *AuthService) Register(req *RegisterRequest, clientIP string) (*models.User, string, error) {
	// 1. 检查IP是否被封禁
	if as.isIPBlocked(clientIP) {
		return nil, "", errors.New("your IP has been blocked due to suspicious activity")
	}

	// 2. 检查用户名是否已存在
	var existingUser models.User
	if err := config.DB.Where("username = ?", req.Username).First(&existingUser).Error; err == nil {
		return nil, "", errors.New("username already exists")
	}

	// 3. 检查邮箱是否已存在
	if err := config.DB.Where("email = ?", req.Email).First(&existingUser).Error; err == nil {
		return nil, "", errors.New("email already exists")
	}

	// 4. 检查注册频率限制（使用Redis）
	if config.RedisClient != nil {
		registerLimitKey := fmt.Sprintf("register:limit:%s", clientIP)
		count, _ := config.RedisClient.Get(redisCtx, registerLimitKey).Int64()
		if count >= int64(as.authConfig.RegisterLimitPerHour) {
			// 记录可疑行为，可能封禁IP
			as.recordSuspiciousActivity(clientIP, "too many registration attempts")
			return nil, "", fmt.Errorf("too many registration attempts, please try again later")
		}
	}

	// 5. 密码加密
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("failed to hash password: %w", err)
	}

	// 6. 生成邮箱验证码
	verificationCode := as.generateVerificationCode()

	// 7. 创建用户
	user := models.User{
		Username: req.Username,
		Email:    req.Email,
		Password: string(hashedPassword),
		Status:   1,
	}

	if err := config.DB.Create(&user).Error; err != nil {
		return nil, "", fmt.Errorf("failed to create user: %w", err)
	}

	// 8. 存储验证码到Redis（30分钟有效）
	verificationKey := fmt.Sprintf("verify:email:%s", req.Email)
	config.RedisClient.Set(redisCtx, verificationKey, verificationCode, 30*time.Minute)

	// 9. 增加注册计数
	if config.RedisClient != nil {
		registerLimitKey := fmt.Sprintf("register:limit:%s", clientIP)
		config.RedisClient.Incr(redisCtx, registerLimitKey)
		config.RedisClient.Expire(redisCtx, registerLimitKey, time.Hour)
	}

	// 10. 生成JWT token
	token, err := as.jwtService.GenerateToken(user.ID, user.Username, user.Email, []string{"user"})
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate token: %w", err)
	}

	// 11. 异步发送欢迎邮件和验证邮件（使用goroutine）
	go func() {
		as.queueEmail(&EmailTask{
			Type:      "welcome",
			ToEmail:   req.Email,
			Subject:   "Welcome to WeOUC BookCycle",
			Body:      fmt.Sprintf("Welcome %s! Your account has been created successfully.", req.Username),
			Timestamp: time.Now(),
		})
	}()

	go func() {
		verificationLink := fmt.Sprintf("http://localhost:5173/verify-email?email=%s&code=%s", req.Email, verificationCode)
		as.queueEmail(&EmailTask{
			Type:    "verification",
			ToEmail: req.Email,
			Subject: "Verify Your Email Address",
			HTMLBody: fmt.Sprintf(`
				<h2>Email Verification</h2>
				<p>Hello %s,</p>
				<p>Please verify your email address by clicking the link below:</p>
				<p><a href="%s">Verify Email</a></p>
				<p>Or use this verification code: <strong>%s</strong></p>
				<p>This code will expire in 30 minutes.</p>
				<p>If you did not create an account, please ignore this email.</p>
			`, req.Username, verificationLink, verificationCode),
			Timestamp: time.Now(),
		})
	}()

	// 12. 记录注册到Redis（用于统计分析）
	go func() {
		if config.RedisClient != nil {
			config.RedisClient.Incr(redisCtx, "stats:register:total")
			config.RedisClient.Incr(redisCtx, fmt.Sprintf("stats:register:%s", time.Now().Format("2006-01-02")))
			// 记录到Stream
			config.RedisClient.XAdd(redisCtx, &redis.XAddArgs{
				Stream: "user_events",
				Values: map[string]interface{}{
					"event":     "register",
					"user_id":   user.ID,
					"email":     user.Email,
					"username":  user.Username,
					"ip":        clientIP,
					"timestamp": time.Now().Unix(),
				},
			})
		}
	}()

	return &user, token, nil
}

// ==================== 登录相关方法 ====================

// Login 用户登录
func (as *AuthService) Login(req *LoginRequest, clientIP, userAgent string) (*models.User, string, error) {
	// 1. 检查IP是否被封禁
	if as.isIPBlocked(clientIP) {
		// 记录登录失败
		as.loginFailureQueue <- &LoginFailure{
			Email:     req.Email,
			IP:        clientIP,
			Timestamp: time.Now(),
			UserAgent: userAgent,
		}
		return nil, "", errors.New("your IP has been blocked due to too many failed login attempts. Please try again later")
	}

	// 2. 检查登录频率限制（基于IP和邮箱）
	if config.RedisClient != nil {
		loginLimitKey := fmt.Sprintf("login:limit:%s:%s", req.Email, clientIP)
		attempts, _ := config.RedisClient.Get(redisCtx, loginLimitKey).Int64()

		if attempts >= int64(as.authConfig.MaxLoginAttempts) {
			// 封禁IP
			as.blockIP(clientIP, "too many failed login attempts")
			return nil, "", fmt.Errorf("too many login attempts. Your IP has been blocked for %v", as.authConfig.LoginBlockDuration)
		}
	}

	// 3. 查找用户
	var user models.User
	if err := config.DB.Where("email = ?", req.Email).First(&user).Error; err != nil {
		// 记录登录失败
		as.recordLoginFailure(req.Email, clientIP, userAgent, "user not found")
		return nil, "", errors.New("invalid email or password")
	}

	// 4. 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		// 记录登录失败
		as.recordLoginFailure(req.Email, clientIP, userAgent, "invalid password")
		return nil, "", errors.New("invalid email or password")
	}

	// 5. 检查用户状态
	if user.Status == 0 {
		return nil, "", errors.New("account is disabled. Please contact support")
	}

	// 6. 更新最后登录时间和登录次数
	now := time.Now()
	loginCount := 0

	// 从Redis获取登录次数
	loginCountKey := fmt.Sprintf("user:login_count:%s", user.ID)
	if config.RedisClient != nil {
		count, _ := config.RedisClient.Get(redisCtx, loginCountKey).Int64()
		loginCount = int(count)
		config.RedisClient.Incr(redisCtx, loginCountKey)
	}

	updates := map[string]interface{}{
		"last_login":  &now,
		"login_count": loginCount + 1,
	}

	if err := config.DB.Model(&user).Updates(updates).Error; err != nil {
		// 不影响登录流程，只记录错误
	}

	// 7. 清除登录失败记录
	if config.RedisClient != nil {
		loginLimitKey := fmt.Sprintf("login:limit:%s:%s", req.Email, clientIP)
		config.RedisClient.Del(redisCtx, loginLimitKey)

		// 从内存缓存中移除IP封禁
		as.ipBlockCache.Delete(clientIP)
	}

	// 8. 生成JWT token
	token, err := as.jwtService.GenerateToken(user.ID, user.Username, user.Email, []string{"user"})
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate token: %w", err)
	}

	// 9. 异步记录登录日志（使用goroutine）
	go func() {
		as.recordLoginLog(&user, clientIP, userAgent, true)
	}()

	// 10. 记录活跃用户到Redis（用于在线统计）
	go func() {
		if config.RedisClient != nil {
			config.RedisClient.ZAdd(redisCtx, "users:active", redis.Z{
				Score:  float64(time.Now().Unix()),
				Member: user.ID,
			})
			config.RedisClient.Expire(redisCtx, "users:active", 7*24*time.Hour)
		}
	}()

	return &user, token, nil
}

// ==================== Token相关方法 ====================

// RefreshToken 刷新token
func (as *AuthService) RefreshToken(tokenString string) (string, map[string]interface{}, error) {
	// 1. 检查token是否在黑名单中
	if config.RedisClient != nil {
		blacklistKey := fmt.Sprintf("token:blacklist:%s", tokenString)
		exists, _ := config.RedisClient.Exists(redisCtx, blacklistKey).Result()
		if exists > 0 {
			return "", nil, errors.New("token has been revoked")
		}
	}

	// 2. 验证token
	claims, err := as.jwtService.ValidateToken(tokenString)
	if err != nil {
		return "", nil, err
	}

	// 3. 将旧token加入黑名单
	if config.RedisClient != nil {
		blacklistKey := fmt.Sprintf("token:blacklist:%s", tokenString)

		// 计算token剩余有效期
		expiration := time.Until(claims.ExpiresAt.Time)
		if expiration > 0 {
			config.RedisClient.Set(redisCtx, blacklistKey, "1", expiration)
		}
	}

	// 4. 生成新token
	newToken, err := as.jwtService.GenerateToken(
		claims.UserID,
		claims.Username,
		claims.Email,
		claims.Roles,
	)
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate new token: %w", err)
	}

	// 5. 返回新token和用户信息
	userInfo := map[string]interface{}{
		"user_id":  claims.UserID,
		"username": claims.Username,
		"email":    claims.Email,
		"roles":    claims.Roles,
	}

	return newToken, userInfo, nil
}

// Logout 用户登出
func (as *AuthService) Logout(tokenString, userID string) error {
	// 1. 将token加入黑名单
	if config.RedisClient != nil {
		blacklistKey := fmt.Sprintf("token:blacklist:%s", tokenString)

		// 解析token获取过期时间
		claims, err := as.jwtService.ValidateToken(tokenString)
		if err != nil {
			return err
		}

		expiration := time.Until(claims.ExpiresAt.Time)
		if expiration > 0 {
			config.RedisClient.Set(redisCtx, blacklistKey, "1", expiration)
		}
	}

	// 2. 从在线用户列表移除
	go func() {
		if config.RedisClient != nil {
			config.RedisClient.ZRem(redisCtx, "users:active", userID)
		}
	}()

	return nil
}

// ==================== 邮箱验证方法 ====================

// VerifyEmail 验证邮箱
func (as *AuthService) VerifyEmail(email, code string) error {
	// 1. 从Redis获取验证码
	verifyKey := fmt.Sprintf("verify:email:%s", email)
	storedCode, err := config.RedisClient.Get(redisCtx, verifyKey).Result()
	if err == redis.Nil {
		return errors.New("verification code has expired")
	}
	if err != nil {
		return fmt.Errorf("failed to verify code: %w", err)
	}

	// 2. 验证验证码
	if storedCode != code {
		// 记录验证失败
		as.recordVerificationFailure(email, "invalid code")
		return errors.New("invalid verification code")
	}

	// 3. 删除验证码
	config.RedisClient.Del(redisCtx, verifyKey)

	// 4. 更新用户状态
	result := config.DB.Model(&models.User{}).Where("email = ?", email).
		Updates(map[string]interface{}{
			"email_verified": true,
			"verified_at":    time.Now(),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to verify email: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return errors.New("user not found")
	}

	return nil
}

// ResendVerificationCode 重新发送验证码
func (as *AuthService) ResendVerificationCode(email string) error {
	// 1. 检查用户是否存在
	var user models.User
	if err := config.DB.Where("email = ?", email).First(&user).Error; err != nil {
		return errors.New("user not found")
	}

	// 2. 检查是否已验证
	if user.EmailVerified {
		return errors.New("email has already been verified")
	}

	// 3. 检查发送频率
	if config.RedisClient != nil {
		rateLimitKey := fmt.Sprintf("verify:rate_limit:%s", email)
		count, _ := config.RedisClient.Get(redisCtx, rateLimitKey).Int64()
		if count > 0 {
			return errors.New("please wait before requesting another verification code")
		}
	}

	// 4. 生成新验证码
	verificationCode := as.generateVerificationCode()

	// 5. 存储到Redis
	verifyKey := fmt.Sprintf("verify:email:%s", email)
	config.RedisClient.Set(redisCtx, verifyKey, verificationCode, 30*time.Minute)

	// 6. 设置发送频率限制（1分钟内不能重复发送）
	if config.RedisClient != nil {
		rateLimitKey := fmt.Sprintf("verify:rate_limit:%s", email)
		config.RedisClient.Set(redisCtx, rateLimitKey, "1", time.Minute)
	}

	// 7. 异步发送邮件
	go func() {
		verificationLink := fmt.Sprintf("http://localhost:5173/verify-email?email=%s&code=%s", email, verificationCode)
		as.queueEmail(&EmailTask{
			Type:    "verification",
			ToEmail: email,
			Subject: "Verify Your Email Address",
			HTMLBody: fmt.Sprintf(`
				<h2>Email Verification</h2>
				<p>Hello,</p>
				<p>Your new verification code is: <strong>%s</strong></p>
				<p>Or click the link below to verify:</p>
				<p><a href="%s">Verify Email</a></p>
				<p>This code will expire in 30 minutes.</p>
			`, verificationCode, verificationLink),
			Timestamp: time.Now(),
		})
	}()

	return nil
}

// ==================== 密码重置方法 ====================

// SendPasswordResetToken 发送密码重置令牌
func (as *AuthService) SendPasswordResetToken(email string) error {
	// 1. 检查用户是否存在
	var user models.User
	if err := config.DB.Where("email = ?", email).First(&user).Error; err != nil {
		// 为了安全，即使用户不存在也返回成功
		return nil
	}

	// 2. 检查发送频率
	if config.RedisClient != nil {
		rateLimitKey := fmt.Sprintf("reset:rate_limit:%s", email)
		count, _ := config.RedisClient.Get(redisCtx, rateLimitKey).Int64()
		if count > 0 {
			return errors.New("please wait before requesting another password reset")
		}
	}

	// 3. 生成重置令牌
	resetToken := generateRandomToken(32)

	// 4. 存储到Redis（30分钟有效）
	resetKey := fmt.Sprintf("reset:password:%s:%s", email, resetToken)
	config.RedisClient.Set(redisCtx, resetKey, "1", 30*time.Minute)

	// 5. 设置发送频率限制（5分钟内不能重复发送）
	if config.RedisClient != nil {
		rateLimitKey := fmt.Sprintf("reset:rate_limit:%s", email)
		config.RedisClient.Set(redisCtx, rateLimitKey, "1", 5*time.Minute)
	}

	// 6. 异步发送邮件
	go func() {
		resetLink := fmt.Sprintf("http://localhost:5173/reset-password?email=%s&token=%s", email, resetToken)
		as.queueEmail(&EmailTask{
			Type:    "password_reset",
			ToEmail: email,
			Subject: "Reset Your Password",
			HTMLBody: fmt.Sprintf(`
				<h2>Password Reset Request</h2>
				<p>Hello,</p>
				<p>We received a request to reset your password.</p>
				<p>Click the link below to reset your password:</p>
				<p><a href="%s">Reset Password</a></p>
				<p>This link will expire in 30 minutes.</p>
				<p>If you did not request a password reset, please ignore this email.</p>
			`, resetLink),
			Timestamp: time.Now(),
		})
	}()

	return nil
}

// ResetPassword 重置密码
func (as *AuthService) ResetPassword(email, token, newPassword string) error {
	// 1. 验证重置令牌
	resetKey := fmt.Sprintf("reset:password:%s:%s", email, token)
	exists, _ := config.RedisClient.Exists(redisCtx, resetKey).Result()
	if exists == 0 {
		return errors.New("reset token has expired or is invalid")
	}

	// 2. 验证密码强度
	if len(newPassword) < 8 {
		return errors.New("password must be at least 8 characters long")
	}

	// 3. 查找用户
	var user models.User
	if err := config.DB.Where("email = ?", email).First(&user).Error; err != nil {
		return errors.New("user not found")
	}

	// 4. 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// 5. 更新密码
	if err := config.DB.Model(&user).Update("password", string(hashedPassword)).Error; err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	// 6. 删除重置令牌
	config.RedisClient.Del(redisCtx, resetKey)

	// 7. 删除所有该用户的活跃token（强制重新登录）
	go func() {
		if config.RedisClient != nil {
			pattern := fmt.Sprintf("token:blacklist:%s:*", user.ID)
			keys, _ := config.RedisClient.Keys(redisCtx, pattern).Result()
			for _, key := range keys {
				config.RedisClient.Del(redisCtx, key)
			}
		}
	}()

	// 8. 异步发送密码修改通知邮件
	go func() {
		as.queueEmail(&EmailTask{
			Type:      "password_changed",
			ToEmail:   email,
			Subject:   "Your Password Has Been Changed",
			Body:      fmt.Sprintf("Hello %s,\n\nYour password has been successfully changed. If you did not make this change, please contact support immediately.\n\nBest regards,\nWeOUC BookCycle Team", user.Username),
			Timestamp: time.Now(),
		})
	}()

	return nil
}

// ==================== IP封禁相关方法 ====================

// isIPBlocked 检查IP是否被封禁
func (as *AuthService) isIPBlocked(ip string) bool {
	// 1. 检查内存缓存
	if info, exists := as.ipBlockCache.Load(ip); exists {
		blockInfo := info.(*BlockInfo)
		if time.Now().Before(blockInfo.UnblockTime) {
			return true
		}
		// 已过期，删除缓存
		as.ipBlockCache.Delete(ip)
	}

	// 2. 检查Redis
	if config.RedisClient != nil {
		blockKey := fmt.Sprintf("ip:blocked:%s", ip)
		exists, _ := config.RedisClient.Exists(redisCtx, blockKey).Result()
		if exists > 0 {
			return true
		}
	}

	return false
}

// blockIP 封禁IP
func (as *AuthService) blockIP(ip, reason string) {
	unblockTime := time.Now().Add(as.authConfig.LoginBlockDuration)

	// 1. 存储到内存缓存（快速检查）
	as.ipBlockCache.Store(ip, &BlockInfo{
		UnblockTime: unblockTime,
		Reason:      reason,
	})

	// 2. 存储到Redis（持久化）
	if config.RedisClient != nil {
		blockKey := fmt.Sprintf("ip:blocked:%s", ip)
		blockData := map[string]interface{}{
			"blocked_at": time.Now().Unix(),
			"unblock_at": unblockTime.Unix(),
			"reason":     reason,
		}
		config.RedisClient.HMSet(redisCtx, blockKey, blockData)
		config.RedisClient.Expire(redisCtx, blockKey, as.authConfig.LoginBlockDuration)

		// 记录到日志
		config.RedisClient.XAdd(redisCtx, &redis.XAddArgs{
			Stream: "security_events",
			Values: map[string]interface{}{
				"event":      "ip_blocked",
				"ip":         ip,
				"reason":     reason,
				"unblock_at": unblockTime.Unix(),
				"timestamp":  time.Now().Unix(),
			},
		})
	}
}

// unblockIP 解封IP
func (as *AuthService) unblockIP(ip string) {
	// 1. 从内存缓存删除
	as.ipBlockCache.Delete(ip)

	// 2. 从Redis删除
	if config.RedisClient != nil {
		blockKey := fmt.Sprintf("ip:blocked:%s", ip)
		config.RedisClient.Del(redisCtx, blockKey)

		// 记录到日志
		config.RedisClient.XAdd(redisCtx, &redis.XAddArgs{
			Stream: "security_events",
			Values: map[string]interface{}{
				"event":     "ip_unblocked",
				"ip":        ip,
				"timestamp": time.Now().Unix(),
			},
		})
	}
}

// recordSuspiciousActivity 记录可疑行为
func (as *AuthService) recordSuspiciousActivity(ip, reason string) {
	suspiciousKey := fmt.Sprintf("suspicious:%s", ip)
	count, _ := config.RedisClient.Incr(redisCtx, suspiciousKey).Result()
	config.RedisClient.Expire(redisCtx, suspiciousKey, time.Hour)

	// 如果可疑行为次数超过阈值，自动封禁
	if count >= 3 {
		as.blockIP(ip, "suspicious activity detected: "+reason)
	}
}

// cleanupIPBlocks 定期清理过期的IP封禁
func (as *AuthService) cleanupIPBlocks() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		as.ipBlockCache.Range(func(key, value interface{}) bool {
			blockInfo := value.(*BlockInfo)
			if time.Now().After(blockInfo.UnblockTime) {
				as.ipBlockCache.Delete(key)
			}
			return true
		})
	}
}

// ==================== 邮件发送相关方法 ====================

// startEmailWorkers 启动邮件发送worker池
func (as *AuthService) startEmailWorkers() {
	for i := 0; i < as.emailWorkers; i++ {
		go as.emailWorker(i)
	}
}

// emailWorker 邮件发送worker
func (as *AuthService) emailWorker(workerID int) {
	for task := range as.emailQueue {
		err := as.sendEmail(task)
		if err != nil {
			// 重试逻辑
			task.Retries++
			if task.Retries < 3 {
				time.Sleep(time.Second * time.Duration(task.Retries))
				as.emailQueue <- task
			} else {
				// 记录失败日志
				as.logEmailFailure(task, err)
			}
		}
	}
}

// queueEmail 将邮件任务加入队列
func (as *AuthService) queueEmail(task *EmailTask) {
	select {
	case as.emailQueue <- task:
	default:
		// 队列满，记录日志但不阻塞
	}
}

// sendEmail 发送邮件（实际实现）
func (as *AuthService) sendEmail(task *EmailTask) error {
	// 如果没有配置SMTP，直接返回成功（测试环境）
	if as.emailConfig.SMTPHost == "" || as.emailConfig.SMTPUser == "" {
		return nil
	}

	// 构建邮件
	from := mail.Address{Name: as.emailConfig.FromName, Address: as.emailConfig.FromEmail}
	to := mail.Address{Name: "", Address: task.ToEmail}

	// 设置邮件头
	headers := map[string]string{
		"From":         from.String(),
		"To":           to.String(),
		"Subject":      task.Subject,
		"Content-Type": "text/html; charset=UTF-8",
	}

	// 构建邮件内容
	message := ""
	for k, v := range headers {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n"

	if task.HTMLBody != "" {
		message += task.HTMLBody
	} else {
		message += task.Body
	}

	// 连接SMTP服务器
	smtpServer := fmt.Sprintf("%s:%d", as.emailConfig.SMTPHost, as.emailConfig.SMTPPort)
	smtpAuth := smtp.PlainAuth("", as.emailConfig.SMTPUser, as.emailConfig.SMTPPassword, as.emailConfig.SMTPHost)

	// 发送邮件
	err := smtp.SendMail(smtpServer, smtpAuth, as.emailConfig.FromEmail, []string{task.ToEmail}, []byte(message))
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

// logEmailFailure 记录邮件发送失败
func (as *AuthService) logEmailFailure(task *EmailTask, err error) {
	// 记录到日志
}

// ==================== 登录失败处理方法 ====================

// startLoginFailureWorker 启动登录失败处理worker
func (as *AuthService) startLoginFailureWorker() {
	go func() {
		for failure := range as.loginFailureQueue {
			as.processLoginFailure(failure)
		}
	}()
}

// processLoginFailure 处理登录失败
func (as *AuthService) processLoginFailure(failure *LoginFailure) {
	// 1. 记录到Redis Stream
	if config.RedisClient != nil {
		config.RedisClient.XAdd(redisCtx, &redis.XAddArgs{
			Stream: "login_failures",
			Values: map[string]interface{}{
				"email":      failure.Email,
				"ip":         failure.IP,
				"user_agent": failure.UserAgent,
				"timestamp":  failure.Timestamp.Unix(),
			},
		})
	}

	// 2. 检查该IP在短时间内的失败次数
	if config.RedisClient != nil {
		ipFailureKey := fmt.Sprintf("login:failures:ip:%s", failure.IP)
		count, _ := config.RedisClient.Incr(redisCtx, ipFailureKey).Result()
		config.RedisClient.Expire(redisCtx, ipFailureKey, time.Hour)

		// 如果失败次数超过阈值，封禁IP
		if count >= 10 {
			as.blockIP(failure.IP, "multiple login failures")
		}
	}

	// 3. 记录到Redis用于告警
	if config.RedisClient != nil {
		alertKey := fmt.Sprintf("alert:login_failure:%s", failure.IP)
		config.RedisClient.Set(redisCtx, alertKey, failure.Timestamp.Unix(), time.Hour)
	}
}

// recordLoginFailure 记录登录失败
func (as *AuthService) recordLoginFailure(email, ip, userAgent, reason string) {
	failure := &LoginFailure{
		Email:     email,
		IP:        ip,
		UserAgent: userAgent,
		Timestamp: time.Now(),
	}

	as.loginFailureQueue <- failure

	// 增加失败计数
	if config.RedisClient != nil {
		loginLimitKey := fmt.Sprintf("login:limit:%s:%s", email, ip)
		config.RedisClient.Incr(redisCtx, loginLimitKey)
		config.RedisClient.Expire(redisCtx, loginLimitKey, as.authConfig.LoginBlockDuration)
	}
}

// recordLoginLog 记录登录日志
func (as *AuthService) recordLoginLog(user *models.User, ip, userAgent string, success bool) {
	// 记录到Redis Stream
	if config.RedisClient != nil {
		config.RedisClient.XAdd(redisCtx, &redis.XAddArgs{
			Stream: "login_logs",
			Values: map[string]interface{}{
				"user_id":    user.ID,
				"username":   user.Username,
				"email":      user.Email,
				"ip":         ip,
				"user_agent": userAgent,
				"success":    success,
				"timestamp":  time.Now().Unix(),
			},
		})
	}
}

// recordVerificationFailure 记录验证失败
func (as *AuthService) recordVerificationFailure(email, reason string) {
	if config.RedisClient != nil {
		failureKey := fmt.Sprintf("verify:failures:%s", email)
		config.RedisClient.Incr(redisCtx, failureKey)
		config.RedisClient.Expire(redisCtx, failureKey, time.Hour)
	}
}

// ==================== 工具方法 ====================

// generateVerificationCode 生成验证码
func (as *AuthService) generateVerificationCode() string {
	b := make([]byte, 3)
	rand.Read(b)
	return fmt.Sprintf("%06d", int(b[0])<<16|int(b[1])<<8|int(b[2]))
}

// generateRandomToken 生成随机令牌
func generateRandomToken(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)
}
