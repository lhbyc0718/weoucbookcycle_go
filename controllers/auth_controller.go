package controllers

import (
	"net/http"
	"weoucbookcycle_go/services"

	"github.com/gin-gonic/gin"
)

// AuthController 认证控制器
type AuthController struct {
	authService *services.AuthService // 使用authService而不是jwtService
}

// NewAuthController 创建认证控制器实例
func NewAuthController() *AuthController {
	return &AuthController{
		authService: services.NewAuthService(),
	}
}

// RegisterRequest 注册请求结构
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

// LoginRequest 登录请求结构
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// VerifyEmailRequest 验证邮箱请求结构
type VerifyEmailRequest struct {
	Email string `json:"email" binding:"required,email"`
	Code  string `json:"code" binding:"required"`
}

// ResendVerificationRequest 重新发送验证码请求结构
type ResendVerificationRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// SendPasswordResetRequest 发送密码重置请求结构
type SendPasswordResetRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// ResetPasswordRequest 重置密码请求结构
type ResetPasswordRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// Register 用户注册
// @Summary 用户注册
// @Description 创建新用户账号
// @Tags auth
// @Accept json
// @Produce json
// @Param request body RegisterRequest true "注册信息"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/auth/register [post]
func (ac *AuthController) Register(c *gin.Context) {
	var req services.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	user, token, err := ac.authService.Register(&req, c.ClientIP())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Registration successful",
		"data": gin.H{
			"token": token,
			"user": gin.H{
				"id":             user.ID,
				"username":       user.Username,
				"email":          user.Email,
				"email_verified": user.EmailVerified,
			},
		},
	})
}

// Login 用户登录
// @Summary 用户登录
// @Description 用户登录获取JWT token
// @Tags auth
// @Accept json
// @Produce json
// @Param request body LoginRequest true "登录信息"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/auth/login [post]
func (ac *AuthController) Login(c *gin.Context) {
	var req services.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	user, token, err := ac.authService.Login(&req, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 40100, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Login successful",
		"data": gin.H{
			"token": token,
			"user": gin.H{
				"id":             user.ID,
				"username":       user.Username,
				"email":          user.Email,
				"avatar":         user.Avatar,
				"email_verified": user.EmailVerified,
			},
		},
	})
}

// RefreshToken 刷新token
// @Summary 刷新token
// @Description 刷新过期的JWT token
// @Tags auth
// @Accept json
// @Produce json
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/auth/refresh [post]
func (ac *AuthController) RefreshToken(c *gin.Context) {
	tokenString := c.GetHeader("Authorization")
	if tokenString == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 40100, "message": "Authorization header required"})
		return
	}

	// 移除 "Bearer " 前缀
	if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
		tokenString = tokenString[7:]
	}

	newToken, userInfo, err := ac.authService.RefreshToken(tokenString)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 40100, "message": "Failed to refresh token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Token refreshed successfully",
		"data": gin.H{
			"token": newToken,
			"user":  userInfo,
		},
	})
}

// Logout 用户登出
// @Summary 用户登出
// @Description 用户登出，将token加入黑名单
// @Tags auth
// @Accept json
// @Produce json
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/auth/logout [post]
func (ac *AuthController) Logout(c *gin.Context) {
	tokenString := c.GetHeader("Authorization")
	if tokenString == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 40100, "message": "Authorization header required"})
		return
	}

	// 移除 "Bearer " 前缀
	if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
		tokenString = tokenString[7:]
	}

	userID := c.GetString("user_id")

	if err := ac.authService.Logout(tokenString, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 50000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Logout successful",
	})
}

// VerifyEmail 验证邮箱
// @Summary 验证邮箱
// @Description 验证用户邮箱
// @Tags auth
// @Accept json
// @Produce json
// @Param request body VerifyEmailRequest true "验证信息"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/auth/verify-email [post]
func (ac *AuthController) VerifyEmail(c *gin.Context) {
	var req VerifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	if err := ac.authService.VerifyEmail(req.Email, req.Code); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Email verified successfully",
	})
}

// ResendVerificationCode 重新发送验证码
// @Summary 重新发送验证码
// @Description 重新发送邮箱验证码
// @Tags auth
// @Accept json
// @Produce json
// @Param request body ResendVerificationRequest true "邮箱地址"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/auth/resend-verification [post]
func (ac *AuthController) ResendVerificationCode(c *gin.Context) {
	var req ResendVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	if err := ac.authService.ResendVerificationCode(req.Email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Verification code sent successfully",
	})
}

// SendPasswordResetToken 发送密码重置令牌
// @Summary 发送密码重置令牌
// @Description 发送密码重置邮件
// @Tags auth
// @Accept json
// @Produce json
// @Param request body SendPasswordResetRequest true "邮箱地址"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/auth/send-password-reset [post]
func (ac *AuthController) SendPasswordResetToken(c *gin.Context) {
	var req SendPasswordResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	if err := ac.authService.SendPasswordResetToken(req.Email); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 50000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Password reset email sent",
	})
}

// ResetPassword 重置密码
// @Summary 重置密码
// @Description 重置用户密码
// @Tags auth
// @Accept json
// @Produce json
// @Param request body ResetPasswordRequest true "重置信息"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/auth/reset-password [post]
func (ac *AuthController) ResetPassword(c *gin.Context) {
	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	if err := ac.authService.ResetPassword(req.Email, req.Token, req.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Password reset successfully",
	})
}
