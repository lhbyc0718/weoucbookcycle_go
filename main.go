package main

import (
	"log"
	"os"

	"weoucbookcycle_go/config"
	"weoucbookcycle_go/middleware"
	"weoucbookcycle_go/routes"
	"weoucbookcycle_go/websocket"

	"github.com/joho/godotenv"
)

func main() {
	// 加载 .env 文件
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  No .env file found, using system environment variables")
	} else {
		log.Println("✅ .env file loaded successfully")
	}

	//设置环境
	env := os.Getenv("GIN_MODE")
	if env == "" {
		os.Setenv("GIN_MODE", "debug")
	}

	// 初始化日志系统
	if err := middleware.InitLogger(env); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer middleware.FlushLogger()

	// 初始化数据库
	if err := config.InitDatabase(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer config.CloseDatabase()

	// 初始化Redis
	if err := config.InitializeRedis(); err != nil {
		log.Fatalf("Failed to initialize Redis: %v", err)
	}
	defer config.CloseRedis()

	//初始化websocket
	if err := websocket.InitWebSocket(); err != nil {
		log.Fatalf("Failed to initialize WebSocket: %v", err)
	}
	defer websocket.CloseWebSocket()

	// 设置路由
	r := config.SetupRouter()

	// 注册自定义路由
	routes.SetupRoutes(r)

	if err := config.StartServer(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	r.Run(":8080")

}
