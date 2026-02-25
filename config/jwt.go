package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig JWT配置结构
type JWTConfig struct {
	SecretKey      string
	ExpirationTime time.Duration
	Issuer         string
}

// GetJWTConfig 获取JWT配置
func GetJWTConfig() *JWTConfig {
	return &JWTConfig{
		SecretKey:      GetEnv("JWT_SECRET", "your-secret-key-change-in-production"),
		ExpirationTime: time.Hour * 24 * 7, // 7天
		Issuer:         "weoucbookcycle",
	}
}

// Claims JWT声明结构
type Claims struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Roles    []string `json:"roles"`
	jwt.RegisteredClaims
}

// JWTService JWT服务
type JWTService struct {
	config *JWTConfig
}

// NewJWTService 创建JWT服务实例
func NewJWTService() *JWTService {
	return &JWTService{
		config: GetJWTConfig(),
	}
}

// GenerateToken 生成JWT token
func (s *JWTService) GenerateToken(userID, username, email string, roles []string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		Username: username,
		Email:    email,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.config.ExpirationTime)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    s.config.Issuer,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.config.SecretKey))
}

// ValidateToken 验证JWT token
func (s *JWTService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// 验证签名算法
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.config.SecretKey), nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}

// RefreshToken 刷新token
func (s *JWTService) RefreshToken(tokenString string) (string, error) {
	claims, err := s.ValidateToken(tokenString)
	if err != nil {
		return "", err
	}

	// 检查token是否即将过期（剩余时间小于1天）
	if time.Until(claims.ExpiresAt.Time) > time.Hour*24 {
		return "", errors.New("token is still valid, no need to refresh")
	}

	return s.GenerateToken(claims.UserID, claims.Username, claims.Email, claims.Roles)
}

// GetJWTService 获取JWT服务实例（全局单例）
var jwtService *JWTService

func GetJWTService() *JWTService {
	if jwtService == nil {
		jwtService = NewJWTService()
	}
	return jwtService
}
