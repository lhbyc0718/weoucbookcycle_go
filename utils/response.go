package utils

import (
	"context"
	"fmt"
	"net/http"
	"time"
	"weoucbookcycle_go/config"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Response 统一响应结构
type Response struct {
	Code    int         `json:"code"`            // 业务状态码
	Message string      `json:"message"`         // 响应消息
	Data    interface{} `json:"data,omitempty"`  // 响应数据
	Error   string      `json:"error,omitempty"` // 错误信息
}

// PageResponse 分页响应结构
type PageResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
	Total   int64       `json:"total"` // 总数
	Page    int         `json:"page"`  // 当前页
	Limit   int         `json:"limit"` // 每页数量
}

// 业务状态码常量
const (
	CodeSuccess             = 20000 // 成功
	CodeError               = 40000 // 错误
	CodeUnauthorized        = 40100 // 未授权
	CodeForbidden           = 40300 // 禁止访问
	CodeNotFound            = 40400 // 资源不存在
	CodeValidationError     = 42200 // 验证错误
	CodeInternalServerError = 50000 // 内部错误
)

// 业务状态码对应的消息
var codeMessages = map[int]string{
	CodeSuccess:             "操作成功",
	CodeError:               "操作失败",
	CodeUnauthorized:        "未授权，请重新登录",
	CodeForbidden:           "禁止访问",
	CodeNotFound:            "资源不存在",
	CodeValidationError:     "参数验证失败",
	CodeInternalServerError: "服务器内部错误",
}

// GetCodeMessage 获取状态码对应的消息
func GetCodeMessage(code int) string {
	if msg, exists := codeMessages[code]; exists {
		return msg
	}
	return "未知错误"
}

// Success 成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    CodeSuccess,
		Message: GetCodeMessage(CodeSuccess),
		Data:    data,
	})
}

// SuccessWithMessage 带消息的成功响应
func SuccessWithMessage(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    CodeSuccess,
		Message: message,
		Data:    data,
	})
}

// Error 错误响应
func Error(c *gin.Context, code int, message string) {
	if message == "" {
		message = GetCodeMessage(code)
	}
	c.JSON(http.StatusOK, Response{
		Code:    code,
		Message: message,
	})
}

// ErrorWithData 带数据的错误响应
func ErrorWithData(c *gin.Context, code int, message string, data interface{}) {
	if message == "" {
		message = GetCodeMessage(code)
	}
	c.JSON(http.StatusOK, Response{
		Code:    code,
		Message: message,
		Data:    data,
	})
}

// ValidationError 验证错误响应

// formatValidationErrors 格式化验证错误

// Unauthorized 未授权响应
func Unauthorized(c *gin.Context, message string) {
	if message == "" {
		message = GetCodeMessage(CodeUnauthorized)
	}
	c.JSON(http.StatusUnauthorized, Response{
		Code:    CodeUnauthorized,
		Message: message,
	})
}

// Forbidden 禁止访问响应
func Forbidden(c *gin.Context, message string) {
	if message == "" {
		message = GetCodeMessage(CodeForbidden)
	}
	c.JSON(http.StatusForbidden, Response{
		Code:    CodeForbidden,
		Message: message,
	})
}

// NotFound 资源不存在响应
func NotFound(c *gin.Context, message string) {
	if message == "" {
		message = GetCodeMessage(CodeNotFound)
	}
	c.JSON(http.StatusNotFound, Response{
		Code:    CodeNotFound,
		Message: message,
	})
}

// InternalError 内部错误响应
func InternalError(c *gin.Context, message string) {
	if message == "" {
		message = GetCodeMessage(CodeInternalServerError)
	}
	c.JSON(http.StatusInternalServerError, Response{
		Code:    CodeInternalServerError,
		Message: message,
	})
}

// Paginate 分页响应
func Paginate(c *gin.Context, data interface{}, total int64, page, limit int) {
	c.JSON(http.StatusOK, PageResponse{
		Code:    CodeSuccess,
		Message: GetCodeMessage(CodeSuccess),
		Data:    data,
		Total:   total,
		Page:    page,
		Limit:   limit,
	})
}

// AsyncResponse 异步响应（使用goroutine处理）
// 适用于耗时操作，立即返回，实际处理在后台进行
func AsyncResponse(c *gin.Context, task func() error, successMsg string) {
	// 创建任务ID
	taskID := generateTaskID()

	// 立即返回任务ID
	c.JSON(http.StatusAccepted, Response{
		Code:    CodeSuccess,
		Message: "任务已提交，正在处理中",
		Data: gin.H{
			"task_id": taskID,
		},
	})

	// 异步执行任务
	go func() {
		startTime := time.Now()

		// 执行任务
		err := task()

		// 记录任务状态到Redis
		if config.RedisClient != nil {
			taskStatus := "completed"
			errorMsg := ""
			if err != nil {
				taskStatus = "failed"
				errorMsg = err.Error()
			}

			ctx := context.Background()
			taskKey := fmt.Sprintf("task:%s", taskID)

			taskData := map[string]interface{}{
				"status":       taskStatus,
				"error":        errorMsg,
				"completed_at": time.Now().Unix(),
				"duration_ms":  time.Since(startTime).Milliseconds(),
			}

			config.RedisClient.HSet(ctx, taskKey, taskData)
			config.RedisClient.Expire(ctx, taskKey, 24*time.Hour)
		}
	}()
}

// CheckTaskStatus 检查任务状态
func CheckTaskStatus(taskID string) (map[string]string, error) {
	if config.RedisClient == nil {
		return nil, fmt.Errorf("redis not available")
	}

	ctx := context.Background()
	taskKey := fmt.Sprintf("task:%s", taskID)

	status, err := config.RedisClient.HGetAll(ctx, taskKey).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("task not found")
	}
	if err != nil {
		return nil, err
	}

	return status, nil
}

// generateTaskID 生成任务ID
func generateTaskID() string {
	return fmt.Sprintf("task_%d_%s", time.Now().Unix(), randomString(8))
}

// randomString 生成随机字符串
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}

// APIRateLimit API限流（使用Redis）
func APIRateLimit(c *gin.Context, userID string, limit int, duration time.Duration) bool {
	if config.RedisClient == nil {
		return true // Redis不可用时，不限流
	}

	ctx := context.Background()
	key := fmt.Sprintf("ratelimit:api:%s", userID)

	// 使用Redis的INCR和EXPIRE实现限流
	count, err := config.RedisClient.Incr(ctx, key).Result()
	if err != nil {
		return true
	}

	// 如果是第一次请求，设置过期时间
	if count == 1 {
		config.RedisClient.Expire(ctx, key, duration)
	}

	return count <= int64(limit)
}
