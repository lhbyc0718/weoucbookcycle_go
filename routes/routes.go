package routes

import (
	"weoucbookcycle_go/controllers"
	"weoucbookcycle_go/middleware"
	"weoucbookcycle_go/websocket"

	"github.com/gin-gonic/gin"
)

// SetupRoutes 设置路由
func SetupRoutes(r *gin.Engine) {
	// 应用全局中间件
	r.Use(middleware.CORS())
	r.Use(middleware.Logger())

	// API 路由组（弃用版本号或与前端环境变量保持一致）
	// 之前使用 /api/v1，如果前端直接请求 /api，可以在这里修改。
	api := r.Group("/api")
	{
		// ====== 认证路由 (无需认证) ======
		auth := api.Group("/auth")
		{
			auth.POST("/register", controllers.NewAuthController().Register)
			auth.POST("/login", controllers.NewAuthController().Login)
			auth.POST("/refresh", controllers.NewAuthController().RefreshToken)
			auth.POST("/logout", controllers.NewAuthController().Logout)
			auth.POST("/verify-email", controllers.NewAuthController().VerifyEmail)
			auth.POST("/resend-verification", controllers.NewAuthController().ResendVerificationCode)
			auth.POST("/send-password-reset", controllers.NewAuthController().SendPasswordResetToken)
			auth.POST("/reset-password", controllers.NewAuthController().ResetPassword)
		}

		// ====== 用户路由 ======
		users := api.Group("/users")
		{
			users.GET("/active", controllers.NewUserController().GetActiveUsers)
			users.GET("/online", controllers.NewUserController().GetOnlineUsers)
			users.GET("/:id", controllers.NewUserController().GetUserProfile)
			users.PUT("/profile", middleware.AuthMiddleware(), controllers.NewUserController().UpdateUserProfile)
		}

		// ====== 书籍路由 ======
		books := api.Group("/books")
		{
			books.GET("", controllers.NewBookController().GetBooks)
			books.GET("/hot", controllers.NewBookController().GetHotBooks)
			books.GET("/search", controllers.NewBookController().SearchBooks)
			books.GET("/recommendations", middleware.AuthMiddleware(), controllers.NewBookController().GetRecommendations)
			books.GET("/:id", controllers.NewBookController().GetBook)
			books.POST("", middleware.AuthMiddleware(), controllers.NewBookController().CreateBook)
			books.PUT("/:id", middleware.AuthMiddleware(), controllers.NewBookController().UpdateBook)
			books.DELETE("/:id", middleware.AuthMiddleware(), controllers.NewBookController().DeleteBook)
			books.POST("/:id/like", middleware.AuthMiddleware(), controllers.NewBookController().LikeBook)
		}

		// ====== 发布路由 ======
		listings := api.Group("/listings")
		{
			listings.GET("", controllers.NewListingController().GetListings)
			listings.GET("/mine", middleware.AuthMiddleware(), controllers.NewListingController().GetMyListings)
			listings.GET("/:id", controllers.NewListingController().GetListing)
			listings.POST("", middleware.AuthMiddleware(), controllers.NewListingController().CreateListing)
			listings.PUT("/:id/status", middleware.AuthMiddleware(), controllers.NewListingController().UpdateListingStatus)
			listings.POST("/:id/favorite", middleware.AuthMiddleware(), controllers.NewListingController().FavoriteListing)
		}

		// ====== 聊天路由 ======
		chats := api.Group("/chats")
		{
			chats.GET("", middleware.AuthMiddleware(), controllers.NewChatController().GetChats)
			chats.GET("/unread", middleware.AuthMiddleware(), controllers.NewChatController().GetUnreadCount)
			chats.GET("/online-users", middleware.AuthMiddleware(), controllers.NewChatController().GetOnlineUsers)
			chats.GET("/:id", middleware.AuthMiddleware(), controllers.NewChatController().GetChat)
			chats.GET("/:id/messages", middleware.AuthMiddleware(), controllers.NewChatController().GetMessages)
			chats.POST("", middleware.AuthMiddleware(), controllers.NewChatController().CreateChat)
			chats.POST("/:id/messages", middleware.AuthMiddleware(), controllers.NewChatController().SendMessage)
			chats.PUT("/:id/read", middleware.AuthMiddleware(), controllers.NewChatController().MarkAsRead)
			chats.DELETE("/:id", middleware.AuthMiddleware(), controllers.NewChatController().DeleteChat)
		}

		// ====== 搜索路由 ======
		search := api.Group("/search")
		{
			search.GET("", controllers.NewSearchController().GlobalSearch)
			search.GET("/users", controllers.NewSearchController().SearchUsers)
			search.GET("/books", controllers.NewSearchController().SearchBooks)
			search.GET("/hot", controllers.NewSearchController().GetHotSearchKeywords)
			search.GET("/suggestions", controllers.NewSearchController().GetSuggestions)
		}
	}

	// ====== WebSocket路由 ======
	r.GET("/ws", websocket.HandleConnection)
	r.GET("/ws/chat", websocket.HandleConnection)
}
