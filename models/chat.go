package models

import (
	"time"

	"gorm.io/gorm"
)

// Chat 聊天模型
type Chat struct {
	ID          string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	LastMessage string         `gorm:"type:text" json:"last_message,omitempty"`
	CreatedAt   time.Time      `gorm:"comment:创建时间" json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`

	// 关联关系
	Users    []ChatUser `gorm:"foreignKey:ChatID" json:"users,omitempty"`
	Messages []Message  `gorm:"foreignKey:ChatID" json:"messages,omitempty"`
}

// ChatUser 聊天用户关联模型
type ChatUser struct {
	ID          string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	ChatID      string    `gorm:"type:varchar(36);index;not null" json:"chat_id"`
	UserID      string    `gorm:"type:varchar(36);index;not null" json:"user_id"`
	UnreadCount int       `gorm:"default:0" json:"unread_count"`
	CreatedAt   time.Time `json:"created_at"`

	// 关联关系
	Chat Chat `gorm:"foreignKey:ChatID" json:"chat,omitempty"`
	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// ChatResponse 聊天响应结构（包含未读数）
type ChatResponse struct {
	ID          string     `json:"id"`
	LastMessage string     `json:"last_message,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
	UnreadCount int64      `json:"unread_count"` // 添加未读数字段
	Users       []ChatUser `json:"users,omitempty"`
	Messages    []Message  `json:"messages,omitempty"`
}

// ToChatResponse 将Chat转换为ChatResponse
func (c *Chat) ToChatResponse(unreadCount int64) ChatResponse {
	return ChatResponse{
		ID:          c.ID,
		LastMessage: c.LastMessage,
		UpdatedAt:   c.UpdatedAt,
		UnreadCount: unreadCount,
		Users:       c.Users,
		Messages:    c.Messages,
	}
}

// TableName 指定表名
func (Chat) TableName() string {
	return "chats"
}

func (ChatUser) TableName() string {
	return "chat_users"
}

// BeforeCreate 创建前钩子
func (c *Chat) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = generateUUID()
	}
	return nil
}

func (cu *ChatUser) BeforeCreate(tx *gorm.DB) error {
	if cu.ID == "" {
		cu.ID = generateUUID()
	}
	return nil
}
