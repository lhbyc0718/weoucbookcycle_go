package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"time"
	"weoucbookcycle_go/config"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logger           *zap.Logger
	accessLogChannel chan *AccessLog
)

// AccessLog 访问日志结构
type AccessLog struct {
	Time       time.Time `json:"time"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Query      string    `json:"query,omitempty"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent,omitempty"`
	StatusCode int       `json:"status_code"`
	Latency    int64     `json:"latency_ms"`
	UserID     string    `json:"user_id,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// InitLogger 初始化日志系统
func InitLogger(mode string) error {
	var err error
	var zapConfig zap.Config

	if mode == "debug" || mode == "" {
		// 开发环境 - 控制台输出
		zapConfig = zap.NewDevelopmentConfig()
		zapConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		// 生产环境 - JSON格式
		zapConfig = zap.NewProductionConfig()
		zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}

	logger, err = zapConfig.Build()
	if err != nil {
		return err
	}

	// 启动日志处理worker池
	accessLogChannel = make(chan *AccessLog, 1000)
	go startLogWorkers()

	return nil
}

// startLogWorkers 启动日志处理worker
// 使用goroutine并发处理日志写入
func startLogWorkers() {
	workerCount := 3 // 3个worker并发处理日志

	for i := 0; i < workerCount; i++ {
		go func(workerID int) {
			for accessLog := range accessLogChannel {
				processAccessLog(workerID, accessLog)
			}
		}(i)
	}
}

// processAccessLog 处理单条访问日志
func (al *AccessLog) processAccessLog() {
	// 使用zap记录结构化日志
	logger.Info("access_log",
		zap.String("time", al.Time.Format(time.RFC3339)),
		zap.String("method", al.Method),
		zap.String("path", al.Path),
		zap.String("query", al.Query),
		zap.String("ip", al.IP),
		zap.String("user_agent", al.UserAgent),
		zap.Int("status_code", al.StatusCode),
		zap.Int64("latency_ms", al.Latency),
		zap.String("user_id", al.UserID),
		zap.String("request_id", al.RequestID),
		zap.String("error", al.Error),
	)

	// 异步将日志写入Redis（用于日志分析和监控）
	go func() {
		if config.RedisClient != nil {
			ctx := context.Background()
			logData, _ := json.Marshal(al)

			// 使用Redis Stream存储日志（适合实时分析）
			config.RedisClient.XAdd(ctx, &redis.XAddArgs{
				Stream: "access_logs",
				Values: map[string]interface{}{
					"timestamp":   al.Time.Unix(),
					"method":      al.Method,
					"path":        al.Path,
					"status_code": al.StatusCode,
					"latency_ms":  al.Latency,
					"ip":          al.IP,
					"user_id":     al.UserID,
					"full_data":   string(logData),
				},
			})

			// 设置Stream最大长度（保留最近7天的日志）
			config.RedisClient.XTrimMaxLen(ctx, "access_logs", 100000)
		}
	}()
}

// processAccessLog 处理单条访问日志（独立函数）
func processAccessLog(workerID int, accessLog *AccessLog) {
	accessLog.processAccessLog()
}

// Logger 返回日志中间件
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 记录开始时间
		start := time.Now()

		// 生成请求ID
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}
		c.Set("request_id", requestID)

		// 读取请求体（用于记录POST/PUT请求）
		var requestBody []byte
		if c.Request.Body != nil {
			requestBody, _ = io.ReadAll(c.Request.Body)
			// 重新设置请求体，让后续处理器可以读取
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// 处理请求
		c.Next()

		// 记录响应日志
		duration := time.Since(start)

		// 构建访问日志
		accessLog := &AccessLog{
			Time:       start,
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
			Query:      c.Request.URL.RawQuery,
			IP:         c.ClientIP(),
			UserAgent:  c.Request.UserAgent(),
			StatusCode: c.Writer.Status(),
			Latency:    duration.Milliseconds(),
			UserID:     c.GetString("user_id"),
			RequestID:  requestID,
		}

		// 如果有错误，记录错误信息
		if len(c.Errors) > 0 {
			accessLog.Error = c.Errors.String()
		}

		// 将日志放入队列（异步处理）
		select {
		case accessLogChannel <- accessLog:
		default:
			// 队列满，直接丢弃（保证请求不被阻塞）
			log.Printf("Log channel is full, dropping log: %s %s", accessLog.Method, accessLog.Path)
		}

		// 在响应头中添加请求ID
		c.Header("X-Request-ID", requestID)
	}
}

// generateRequestID 生成请求ID
func generateRequestID() string {
	return time.Now().Format("20060102150405") + "-" + randomString(8)
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

// ErrorLogger 错误日志记录
func ErrorLogger(msg string, fields ...zap.Field) {
	if logger != nil {
		logger.Error(msg, fields...)
	}
}

// InfoLogger 信息日志记录
func InfoLogger(msg string, fields ...zap.Field) {
	if logger != nil {
		logger.Info(msg, fields...)
	}
}

// DebugLogger 调试日志记录
func DebugLogger(msg string, fields ...zap.Field) {
	if logger != nil {
		logger.Debug(msg, fields...)
	}
}

// FlushLogger 刷新日志缓冲区
func FlushLogger() {
	if logger != nil {
		_ = logger.Sync()
	}
}
