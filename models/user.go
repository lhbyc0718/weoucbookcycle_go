package models

import (
	"time"

	"gorm.io/gorm"
)

// User 用户模型
type User struct {
	ID            string         `gorm:"type:varchar(36);primaryKey;comment:用户ID (UUID)" json:"id"`
	Username      string         `gorm:"type:varchar(50);uniqueIndex;not null;comment:用户名" json:"username"`
	Email         string         `gorm:"type:varchar(100);uniqueIndex;not null;comment:邮箱" json:"email"`
	Password      string         `gorm:"type:varchar(255);not null;comment:密码" json:"-"` // 不返回给前端
	Avatar        string         `gorm:"type:varchar(255);comment:头像" json:"avatar,omitempty"`
	Phone         string         `gorm:"type:varchar(20);comment:手机号" json:"phone,omitempty"`
	Bio           string         `gorm:"type:text;comment:个人简介" json:"bio,omitempty"`
	EmailVerified bool           `gorm:"default:false;comment:邮箱是否已验证" json:"email_verified"`
	VerifiedAt    *time.Time     `gorm:"comment:验证时间" json:"verified_at,omitempty"`
	Status        int            `gorm:"default:1;comment:状态: 1=正常, 0=禁用" json:"status"`
	LastLogin     *time.Time     `gorm:"comment:最后登录时间" json:"last_login,omitempty"`
	LoginCount    int            `gorm:"default:0;comment:登录次数" json:"login_count"`
	CreatedAt     time.Time      `gorm:"comment:创建时间" json:"created_at"`
	UpdatedAt     time.Time      `gorm:"comment:更新时间" json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"` // 软删除

	// 关联关系
	Books     []Book     `gorm:"foreignKey:SellerID" json:"books,omitempty"`
	Listings  []Listing  `gorm:"foreignKey:SellerID" json:"listings,omitempty"`
	ChatUsers []ChatUser `gorm:"foreignKey:UserID" json:"chat_users,omitempty"`
	Messages  []Message  `gorm:"foreignKey:SenderID" json:"messages,omitempty"`
}

// TableName 指定表名
func (User) TableName() string {
	return "users"
}

// BeforeCreate 创建前钩子
func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == "" {
		u.ID = generateUUID()
	}
	return nil
}
