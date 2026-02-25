package config

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9" // 使用最新的 go-redis/v9
)

// RedisClient 全局 Redis 客户端实例
var RedisClient *redis.Client

// InitializeRedis 初始化 Redis 客户端
func InitializeRedis() error {
	redisAddr := GetEnv("REDIS_ADDR", "localhost:6379")
	redisPassword := GetEnv("REDIS_PASSWORD", "")
	redisDB := GetEnv("REDIS_DB", "0")

	// 解析数据库编号
	db := 0
	if redisDB != "" {
		fmt.Sscanf(redisDB, "%d", &db)
	}

	// 创建Redis客户端
	RedisClient = redis.NewClient(&redis.Options{
		Addr:         redisAddr,
		Password:     redisPassword,
		DB:           db,
		PoolSize:     10,              // 连接池大小
		MinIdleConns: 5,               // 最小空闲连接
		MaxRetries:   3,               // 最大重试次数
		DialTimeout:  5 * time.Second, // 连接超时
		ReadTimeout:  3 * time.Second, // 读取超时
		WriteTimeout: 3 * time.Second, // 写入超时
		PoolTimeout:  4 * time.Second, // 从连接池获取连接的超时
	})

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := RedisClient.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("failed to connect to Redis: %w", err)
	}

	log.Println("✅ Redis client initialized successfully")
	return nil
}

// CloseRedis 关闭 Redis 连接
func CloseRedis() error {
	if RedisClient != nil {
		return RedisClient.Close()
	}
	return nil
}

// ServerConfig 服务器配置结构
type ServerConfig struct {
	Port         string
	Mode         string
	ReadTimeout  int
	WriteTimeout int
	RedisEnabled bool // Redis是否启用
}

// GetServerConfig 获取服务器配置
func GetServerConfig() *ServerConfig {
	redisEnabled := GetEnv("REDIS_ENABLED", "true") == "true"

	return &ServerConfig{
		Port:         GetEnv("SERVER_PORT", "8080"),
		Mode:         GetEnv("GIN_MODE", "debug"),
		ReadTimeout:  30,
		WriteTimeout: 30,
		RedisEnabled: redisEnabled,
	}
}

// SetupRouter 设置路由
func SetupRouter() *gin.Engine {
	serverConfig := GetServerConfig()

	// 根据环境设置Gin模式
	gin.SetMode(serverConfig.Mode)

	// 创建Gin实例
	r := gin.New()

	// 全局中间件
	r.Use(gin.Recovery()) // 恢复panic
	r.Use(gin.Logger())   // 日志记录

	// CORS配置
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000", "http://localhost:5173", "http://localhost:4173"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 健康检查端点（包括数据库和Redis状态）
	r.GET("/health", func(c *gin.Context) {
		health := gin.H{
			"status":  "ok",
			"message": "Server is running",
		}

		// 检查数据库状态
		if DB != nil {
			sqlDB, err := DB.DB()
			if err == nil {
				if err := sqlDB.Ping(); err == nil {
					health["database"] = "connected"
				} else {
					health["database"] = "disconnected"
				}
			} else {
				health["database"] = "error"
			}
		} else {
			health["database"] = "not initialized"
		}

		// 检查Redis状态
		if RedisClient != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := RedisClient.Ping(ctx).Err(); err == nil {
				health["redis"] = "connected"
			} else {
				health["redis"] = "disconnected"
			}
		} else {
			health["redis"] = "not initialized"
		}

		c.JSON(200, health)
	})

	// // API版本分组
	// v1 := r.Group("/api/v1")
	// {
	// 	// 路由注册在 routes/routes.go 中处理
	// 	// 这里留空，保持结构清晰
	// }

	return r
}

// StartServer 启动服务器
func StartServer() error {
	serverConfig := GetServerConfig()

	// 初始化数据库
	if err := InitDatabase(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer CloseDatabase()

	// 初始化Redis（如果启用）
	if serverConfig.RedisEnabled {
		if err := InitializeRedis(); err != nil {
			log.Printf("⚠️  Warning: Redis initialization failed: %v", err)
			log.Println("Continuing without Redis caching...")
			RedisClient = nil
		}
	} else {
		log.Println("ℹ️  Redis is disabled in configuration")
	}
	defer CloseRedis()

	// 设置路由
	r := SetupRouter()

	// 获取服务器配置
	config := GetServerConfig()

	// 启动服务器
	addr := fmt.Sprintf(":%s", config.Port)
	log.Printf("🚀 Server starting on port %s in %s mode", config.Port, config.Mode)
	log.Printf("📚 API documentation: http://localhost%s/api/v1", addr)
	log.Printf("❤️  Health check: http://localhost%s/health", addr)

	if err := r.Run(addr); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

// GetServer 获取Gin实例（用于测试）
func GetServer() *gin.Engine {
	return SetupRouter()
}

// GetRedisClient 获取Redis客户端实例（供其他包使用）
// 这个函数可以在控制器中调用，而不是每个controller都自己创建redis客户端
func GetRedisClient() *redis.Client {
	return RedisClient
}
