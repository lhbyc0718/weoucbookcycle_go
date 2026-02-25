package models

import (
	"time"

	"gorm.io/gorm"
)

// Message 消息模型
type Message struct {
	ID        string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	ChatID    string         `gorm:"type:varchar(36);index;not null" json:"chat_id"`
	SenderID  string         `gorm:"type:varchar(36);index;not null" json:"sender_id"`
	Content   string         `gorm:"type:text;not null" json:"content"`
	IsRead    bool           `gorm:"default:false" json:"is_read"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 关联关系
	Chat   Chat `gorm:"foreignKey:ChatID" json:"chat,omitempty"`
	Sender User `gorm:"foreignKey:SenderID" json:"sender,omitempty"`
}

// TableName 指定表名
func (Message) TableName() string {
	return "messages"
}

// BeforeCreate 创建前钩子
func (m *Message) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = generateUUID()
	}
	return nil
}

// BeforeUpdate 更新前钩子 - 更新聊天最后消息时间
func (m *Message) AfterCreate(tx *gorm.DB) error {
	// 更新聊天的最后消息和时间
	return tx.Model(&Chat{}).Where("id = ?", m.ChatID).Updates(map[string]interface{}{
		"last_message": m.Content,
		"updated_at":   time.Now(),
	}).Error
}
