package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
	"weoucbookcycle_go/config"
	"weoucbookcycle_go/models"
	"weoucbookcycle_go/services"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

// ChatController 聊天控制器
type ChatController struct {
	redisClient *redis.Client
	upgrader    websocket.Upgrader
	// 在线用户连接管理
	clients   map[string]*websocket.Conn // userID -> connection
	clientsMu sync.RWMutex
	// 消息队列
	messageQueue chan MessageTask
}

// MessageTask 消息任务
type MessageTask struct {
	ChatID  string
	UserID  string
	Content string
}

// NewChatController 创建聊天控制器实例
func NewChatController() *ChatController {
	cc := &ChatController{
		redisClient:  initRedis(),
		upgrader:     websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		clients:      make(map[string]*websocket.Conn),
		messageQueue: make(chan MessageTask, 1000),
	}

	// 启动消息处理worker池
	cc.startMessageWorkers()

	// 启动心跳检测
	go cc.heartbeatCheck()

	return cc
}

// startMessageWorkers 启动消息处理worker池
// 使用goroutine和channel实现异步消息处理
func (cc *ChatController) startMessageWorkers() {
	workerCount := 3 // 启动3个worker处理消息

	for i := 0; i < workerCount; i++ {
		go cc.messageWorker(i)
	}
}

// messageWorker 消息处理worker
func (cc *ChatController) messageWorker(workerID int) {
	for task := range cc.messageQueue {
		// 处理消息逻辑
		cc.processMessage(task)
	}
}

// processMessage 处理消息
func (cc *ChatController) processMessage(task MessageTask) error {
	// 创建消息记录
	message := models.Message{
		ChatID:   task.ChatID,
		SenderID: task.UserID,
		Content:  task.Content,
		IsRead:   false,
	}

	if err := config.DB.Create(&message).Error; err != nil {
		return err
	}

	// 获取聊天参与者
	var chatUsers []models.ChatUser
	config.DB.Where("chat_id = ?", task.ChatID).Find(&chatUsers)

	// 推送消息给在线用户（使用goroutine并发推送）
	for _, chatUser := range chatUsers {
		if chatUser.UserID != task.UserID {
			go func(receiverID string) {
				cc.sendMessageToUser(receiverID, message)
			}(chatUser.UserID)
		}
	}

	return nil
}

// sendMessageToUser 发送消息给指定用户
func (cc *ChatController) sendMessageToUser(userID string, message models.Message) {
	cc.clientsMu.RLock()
	conn, exists := cc.clients[userID]
	cc.clientsMu.RUnlock()

	if exists {
		conn.WriteJSON(message)
	}

	// 增加未读计数
	cc.redisClient.Incr(ctx, "unread:"+userID+":"+message.ChatID)
	cc.redisClient.Expire(ctx, "unread:"+userID+":"+message.ChatID, time.Hour*24*7)
}

// heartbeatCheck 心跳检测
// 定期检查连接是否存活
func (cc *ChatController) heartbeatCheck() {
	ticker := time.NewTicker(time.Minute * 1)
	defer ticker.Stop()

	for range ticker.C {
		cc.clientsMu.Lock()
		for userID, conn := range cc.clients {
			// 发送ping消息检测连接
			if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
				// 连接已断开，移除
				delete(cc.clients, userID)
				cc.redisClient.Del(ctx, "online:"+userID)
			}
		}
		cc.clientsMu.Unlock()
	}
}

// GetChats 获取聊天列表
// @Summary 获取聊天列表
// @Description 获取当前用户的聊天列表，包含每个聊天的未读消息数
// @Tags chats
// @Accept json
// @Produce json
// @Security Bearer
// @Success 200 {array} ChatResponse
// @Router /api/v1/chats [get]
func (cc *ChatController) GetChats(c *gin.Context) {
	userID := c.GetString("user_id")

	// 获取用户参与的聊天关系（包含数据库中的未读数）
	var chatUsers []models.ChatUser
	if err := config.DB.Where("user_id = ?", userID).Find(&chatUsers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get chats"})
		return
	}

	// 如果没有聊天，返回空数组
	if len(chatUsers) == 0 {
		c.JSON(http.StatusOK, gin.H{"chats": []models.ChatResponse{}})
		return
	}

	// 提取聊天ID列表
	chatIDs := make([]string, len(chatUsers))
	for i, cu := range chatUsers {
		chatIDs[i] = cu.ChatID
	}

	// 并发获取聊天详情
	var chats []models.ChatResponse
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, chatID := range chatIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			var chat models.Chat
			if err := config.DB.
				Preload("Users").
				Preload("Users.User").
				Where("id = ?", id).
				First(&chat).Error; err == nil {

				// 从Redis获取最新的未读数（如果有）
				var unreadCount int64

				if config.RedisClient != nil {
					unreadKey := "unread:" + userID + ":" + id
					unread, err := config.RedisClient.Get(ctx, unreadKey).Int64()
					if err == nil {
						unreadCount = unread
					}
				}

				// 如果Redis中没有，使用数据库中的值（从ChatUser中获取）
				if unreadCount == 0 {
					// 从chatUsers中找到对应的ChatUser获取未读数
					for _, cu := range chatUsers {
						if cu.ChatID == id {
							unreadCount = int64(cu.UnreadCount)
							break
						}
					}
				}

				// 转换为响应结构
				chatResponse := chat.ToChatResponse(unreadCount)

				// 添加到结果
				mu.Lock()
				chats = append(chats, chatResponse)
				mu.Unlock()
			}
		}(chatID)
	}

	wg.Wait()

	c.JSON(http.StatusOK, gin.H{"chats": chats})
}

// GetChat 获取聊天详情
// @Summary 获取聊天详情
// @Description 根据聊天ID获取详细信息
// @Tags chats
// @Accept json
// @Produce json
// @Param id path string true "聊天ID"
// @Security Bearer
// @Success 200 {object} models.Chat
// @Router /api/v1/chats/{id} [get]
func (cc *ChatController) GetChat(c *gin.Context) {
	userID := c.GetString("user_id")
	chatID := c.Param("id")

	// 检查用户是否有权限访问该聊天
	var chatUser models.ChatUser
	if err := config.DB.Where("chat_id = ? AND user_id = ?", chatID, userID).First(&chatUser).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to access this chat"})
		return
	}

	// 先尝试从Redis缓存获取
	cacheKey := "chat:" + chatID
	cached, err := cc.redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		var chat models.Chat
		if json.Unmarshal([]byte(cached), &chat) == nil {
			c.JSON(http.StatusOK, chat)
			return
		}
	}

	// 从数据库查询
	var chat models.Chat
	if err := config.DB.
		Preload("Users").
		Preload("Users.User").
		Preload("Messages").
		Preload("Messages.Sender").
		First(&chat, "id = ?", chatID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chat not found"})
		return
	}

	// 异步缓存到Redis
	go func() {
		data, _ := json.Marshal(chat)
		cc.redisClient.Set(ctx, cacheKey, data, time.Minute*10)
	}()

	c.JSON(http.StatusOK, chat)
}

// CreateChat 创建新聊天
// @Summary 创建新聊天
// @Description 创建新的聊天会话
// @Tags chats
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body map[string]interface{} true "聊天信息" example='{"user_id":"target-user-id"}'
// @Success 201 {object} models.Chat
// @Router /api/v1/chats [post]
func (cc *ChatController) CreateChat(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		UserID string `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 检查目标用户是否存在
	var targetUser models.User
	if err := config.DB.First(&targetUser, "id = ?", req.UserID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Target user not found"})
		return
	}

	// 检查是否已经存在这两个用户的聊天
	var existingChat models.Chat
	var existingChatUser models.ChatUser

	err := config.DB.
		Joins("JOIN chat_users ON chat_users.chat_id = chats.id").
		Where("chat_users.user_id = ?", userID).
		First(&existingChat).Error

	if err == nil {
		// 检查是否也包含目标用户
		err = config.DB.
			Where("chat_id = ? AND user_id = ?", existingChat.ID, req.UserID).
			First(&existingChatUser).Error

		if err == nil {
			// 聊天已存在，返回现有聊天
			c.JSON(http.StatusOK, existingChat)
			return
		}
	}

	// 创建新聊天
	chat := models.Chat{}

	if err := config.DB.Create(&chat).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create chat"})
		return
	}

	// 添加聊天用户（使用goroutine并发插入）
	var wg sync.WaitGroup
	users := []string{userID, req.UserID}

	for _, uid := range users {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			chatUser := models.ChatUser{
				ChatID:      chat.ID,
				UserID:      id,
				UnreadCount: 0,
			}
			config.DB.Create(&chatUser)
		}(uid)
	}
	wg.Wait()

	c.JSON(http.StatusCreated, chat)
}

// GetMessages 获取聊天消息
// @Summary 获取聊天消息
// @Description 获取聊天的消息列表（分页）
// @Tags chats
// @Accept json
// @Produce json
// @Param id path string true "聊天ID"
// @Param page query int false "页码" default(1)
// @Param limit query int false "每页数量" default(50)
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/chats/{id}/messages [get]
func (cc *ChatController) GetMessages(c *gin.Context) {
	userID := c.GetString("user_id")
	chatID := c.Param("id")

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset := (page - 1) * limit

	// 检查权限
	var chatUser models.ChatUser
	if err := config.DB.Where("chat_id = ? AND user_id = ?", chatID, userID).First(&chatUser).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to access this chat"})
		return
	}

	// 从Redis获取缓存消息
	cacheKey := "chat:" + chatID + ":messages:page:" + strconv.Itoa(page)
	cached, err := cc.redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		var messages []models.Message
		if json.Unmarshal([]byte(cached), &messages) == nil {
			c.JSON(http.StatusOK, gin.H{
				"messages": messages,
				"page":     page,
				"limit":    limit,
			})
			return
		}
	}

	// 从数据库查询
	var messages []models.Message
	var total int64

	config.DB.Model(&models.Message{}).Where("chat_id = ?", chatID).Count(&total)

	if err := config.DB.
		Preload("Sender").
		Where("chat_id = ?", chatID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&messages).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get messages"})
		return
	}

	// 反转消息顺序（最新的在最前面）
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	// 标记消息为已读
	go func() {
		config.DB.Model(&models.Message{}).
			Where("chat_id = ? AND sender_id != ?", chatID, userID).
			Update("is_read", true)

		// 清除Redis中的未读计数
		cc.redisClient.Del(ctx, "unread:"+userID+":"+chatID)
	}()

	// 异步缓存消息
	go func() {
		data, _ := json.Marshal(messages)
		cc.redisClient.Set(ctx, cacheKey, data, time.Minute*5)
	}()

	c.JSON(http.StatusOK, gin.H{
		"messages": messages,
		"total":    total,
		"page":     page,
		"limit":    limit,
	})
}

// SendMessage 发送消息
// @Summary 发送消息
// @Description 在指定聊天中发送新消息
// @Tags chats
// @Accept json
// @Produce json
// @Param id path string true "聊天ID"
// @Param request body map[string]interface{} true "消息内容" example='{"content":"Hello"}'
// @Security Bearer
// @Success 201 {object} models.Message
// @Router /api/v1/chats/{id}/messages [post]
func (cc *ChatController) SendMessage(c *gin.Context) {
	userID := c.GetString("user_id")
	chatID := c.Param("id")

	var req struct {
		Content string `json:"content" binding:"required,max=1000"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 检查权限
	var chatUser models.ChatUser
	if err := config.DB.Where("chat_id = ? AND user_id = ?", chatID, userID).First(&chatUser).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to send messages in this chat"})
		return
	}

	// 将消息任务放入队列（异步处理）
	task := MessageTask{
		ChatID:  chatID,
		UserID:  userID,
		Content: req.Content,
	}

	select {
	case cc.messageQueue <- task:
		c.JSON(http.StatusAccepted, gin.H{"message": "Message queued for delivery"})
	default:
		// 队列满了，直接处理
		cc.processMessage(task)
		c.JSON(http.StatusCreated, gin.H{"message": "Message sent successfully"})
	}
}

// HandleWebSocket WebSocket连接处理
// @Summary WebSocket连接
// @Description 建立WebSocket连接进行实时通信
// @Tags chats
// @Param user_id query string true "用户ID"
// @Router /ws [get]
func (cc *ChatController) HandleWebSocket(c *gin.Context) {
	userID := c.Query("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User ID is required"})
		return
	}

	// 升级HTTP连接为WebSocket连接
	conn, err := cc.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 添加到在线用户
	cc.clientsMu.Lock()
	cc.clients[userID] = conn
	cc.clientsMu.Unlock()

	// 设置Redis在线状态
	cc.redisClient.Set(ctx, "online:"+userID, "1", time.Minute*5)

	// 发送未读消息
	go cc.sendUnreadMessages(conn, userID)

	// 监听消息
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		// 处理接收到的消息
		var msg map[string]interface{}
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		// 处理消息类型
		switch msg["type"] {
		case "message":
			if chatID, ok := msg["chat_id"].(string); ok {
				if content, ok := msg["content"].(string); ok {
					task := MessageTask{
						ChatID:  chatID,
						UserID:  userID,
						Content: content,
					}
					cc.messageQueue <- task
				}
			}
		case "ping":
			// 心跳响应
			conn.WriteMessage(messageType, []byte("pong"))
		}
	}

	// 连接断开，清理
	cc.clientsMu.Lock()
	delete(cc.clients, userID)
	cc.clientsMu.Unlock()

	cc.redisClient.Del(ctx, "online:"+userID)
}

// sendUnreadMessages 发送未读消息
func (cc *ChatController) sendUnreadMessages(conn *websocket.Conn, userID string) {
	// 获取所有未读消息的key
	pattern := "unread:" + userID + ":*"
	keys, _ := cc.redisClient.Keys(ctx, pattern).Result()

	for _, key := range keys {
		// 提取chat_id
		chatID := key[len("unread:"+userID+":"):]

		// 获取最后几条消息
		cacheKey := "chat:" + chatID + ":last_messages"
		cached, err := cc.redisClient.LRange(ctx, cacheKey, 0, -1).Result()
		if err == nil {
			for _, msgStr := range cached {
				conn.WriteMessage(websocket.TextMessage, []byte(msgStr))
			}
		}
	}
}

// GetUnreadCount 获取未读消息数
// @Summary 获取未读消息数
// @Description 获取当前用户的所有未读消息数量
// @Tags chats
// @Accept json
// @Produce json
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/chats/unread [get]
func (cc *ChatController) GetUnreadCount(c *gin.Context) {
	userID := c.GetString("user_id")

	// 获取所有未读key
	pattern := "unread:" + userID + ":*"
	keys, _ := cc.redisClient.Keys(ctx, pattern).Result()

	totalUnread := 0
	chatUnread := make(map[string]int64)

	for _, key := range keys {
		// 提取chat_id
		chatID := key[len("unread:"+userID+":"):]

		// 获取未读数
		count, _ := cc.redisClient.Get(ctx, key).Int64()
		totalUnread += int(count)
		chatUnread[chatID] = count
	}

	c.JSON(http.StatusOK, gin.H{
		"total_unread": totalUnread,
		"chat_unread":  chatUnread,
	})
}

// GetOnlineUsers 获取在线用户列表
// @Summary 获取在线用户列表
// @Description 获取当前在线的用户列表
// @Tags chats
// @Accept json
// @Produce json
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/chats/online-users [get]
func (cc *ChatController) GetOnlineUsers(c *gin.Context) {
	chatService := services.NewChatService()

	onlineUsers, err := chatService.GetOnlineUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 50000, "message": err.Error()})
		return
	}

	// 获取在线用户详细信息
	var users []models.User
	if len(onlineUsers) > 0 {
		config.DB.Where("id IN ?", onlineUsers).Find(&users)
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Success",
		"data": gin.H{
			"online_users": users,
			"count":        len(onlineUsers),
		},
	})
}

// MarkAsRead 标记消息为已读
// @Summary 标记消息为已读
// @Description 标记指定聊天的所有未读消息为已读
// @Tags chats
// @Accept json
// @Produce json
// @Security Bearer
// @Param id path string true "聊天ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/chats/:id/read [put]
func (cc *ChatController) MarkAsRead(c *gin.Context) {
	userID := c.GetString("user_id")
	chatID := c.Param("id")

	chatService := services.NewChatService()

	if err := chatService.MarkAsRead(chatID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 50000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Messages marked as read",
	})
}

// DeleteChat 删除聊天
// @Summary 删除聊天
// @Description 删除指定聊天
// @Tags chats
// @Accept json
// @Produce json
// @Security Bearer
// @Param id path string true "聊天ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/chats/:id [delete]
func (cc *ChatController) DeleteChat(c *gin.Context) {
	userID := c.GetString("user_id")
	chatID := c.Param("id")

	chatService := services.NewChatService()

	if err := chatService.DeleteChat(chatID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 50000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    20000,
		"message": "Chat deleted successfully",
	})
}
