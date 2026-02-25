package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
	"weoucbookcycle_go/config"
	"weoucbookcycle_go/models"
	"weoucbookcycle_go/services"

	"github.com/gin-gonic/gin"
)

// UserController 用户控制器
type UserController struct{}

// NewUserController 创建用户控制器实例
func NewUserController() *UserController {
	return &UserController{}
}

// UpdateProfileRequest 更新用户资料请求结构
type UpdateProfileRequest struct {
	Username string `json:"username" binding:"omitempty,min=3,max=50"`
	Avatar   string `json:"avatar" binding:"omitempty"`
	Phone    string `json:"phone" binding:"omitempty"`
	Bio      string `json:"bio" binding:"omitempty,max=500"`
}

// GetUserProfile 获取用户资料
// @Summary 获取用户资料
// @Description 根据用户ID获取用户详细信息
// @Tags users
// @Accept json
// @Produce json
// @Param id path string true "用户ID"
// @Success 200 {object} models.User
// @Router /api/v1/users/{id} [get]
func (uc *UserController) GetUserProfile(c *gin.Context) {
	userID := c.Param("id")
	// 先尝试从Redis缓存获取
	cacheKey := "user:" + userID

	cachedData, err := config.RedisClient.Get(context.Background(), cacheKey).Result()
	if err == nil {
		var cachedUser models.User
		if err := json.Unmarshal([]byte(cachedData), &cachedUser); err == nil {
			c.JSON(http.StatusOK, cachedUser)
			return
		}
	}

	var user models.User
	if err := config.DB.Preload("Books").Preload("Listings").First(&user, "id = ?", userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	//异步缓存用户信息到Redis（使用goroutine）
	go func() {
		config.RedisClient.Set(context.Background(), cacheKey, user, time.Minute*30)
	}()

	c.JSON(http.StatusOK, user)
}

// UpdateUserProfile 更新用户资料
// @Summary 更新用户资料
// @Description 更新当前登录用户的资料信息
// @Tags users
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body UpdateProfileRequest true "用户资料"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/users/profile [put]
func (uc *UserController) UpdateUserProfile(c *gin.Context) {
	userID := c.GetString("user_id") // 从中间件获取

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 构建更新map
	updates := make(map[string]interface{})
	if req.Username != "" {
		updates["username"] = req.Username
	}
	if req.Avatar != "" {
		updates["avatar"] = req.Avatar
	}
	if req.Phone != "" {
		updates["phone"] = req.Phone
	}
	if req.Bio != "" {
		updates["bio"] = req.Bio
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	var user models.User
	if err := config.DB.Model(&user).Where("id = ?", userID).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update profile"})
		return
	}

	// 删除Redis缓存
	// go func() {
	//     config.Redis.Del(context.Background(), "user:"+userID)
	// }()

	c.JSON(http.StatusOK, gin.H{
		"message": "Profile updated successfully",
		"user":    user,
	})
}

// GetActiveUsers 获取活跃用户列表
// @Summary 获取活跃用户列表
// @Description 获取最近的活跃用户，用于消息页面
// @Tags users
// @Accept json
// @Produce json
// @Param page query int false "页码" default(1)
// @Param limit query int false "每页数量" default(20)
// @Success 200 {array} models.User
// @Router /api/v1/users/active [get]
func (uc *UserController) GetActiveUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit

	var users []models.User
	if err := config.DB.
		Order("last_login DESC").
		Limit(limit).
		Offset(offset).
		Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get users"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"users": users,
		"page":  page,
		"limit": limit,
	})
}

// GetOnlineUsers 获取在线用户列表
// @Summary 获取在线用户列表
// @Description 获取当前在线的用户列表
// @Tags users
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/users/online [get]
func (uc *UserController) GetOnlineUsers(c *gin.Context) {
	chatService := services.NewChatService()

	onlineUsers, err := chatService.GetOnlineUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 50000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Success",
		"data": gin.H{
			"online_users": onlineUsers,
			"count":        len(onlineUsers),
		},
	})
}
