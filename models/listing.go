package models

import (
	"time"

	"gorm.io/gorm"
)

// Listing 交易发布模型
type Listing struct {
	ID            string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	BookID        string         `gorm:"type:varchar(36);index;not null" json:"book_id"`
	SellerID      string         `gorm:"type:varchar(36);index;not null" json:"seller_id"`
	BuyerID       string         `gorm:"type:varchar(36);index" json:"buyer_id,omitempty"`
	Price         float64        `gorm:"type:decimal(10,2);not null" json:"price"`
	Status        string         `gorm:"type:varchar(20);default:available;comment:available,reserved,sold,cancelled" json:"status"`
	Note          string         `gorm:"type:text" json:"note,omitempty"`
	FavoriteCount int64          `gorm:"default:0" json:"favorite_count"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`

	// 关联关系
	Book      Book       `gorm:"foreignKey:BookID" json:"book,omitempty"`
	Seller    User       `gorm:"foreignKey:SellerID" json:"seller,omitempty"`
	Buyer     *User      `gorm:"foreignKey:BuyerID" json:"buyer,omitempty"`
	Favorites []Favorite `gorm:"foreignKey:ListingID" json:"favorites,omitempty"`
}

// Favorite 收藏模型
type Favorite struct {
	ID        string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	UserID    string    `gorm:"type:varchar(36);index;not null" json:"user_id"`
	ListingID string    `gorm:"type:varchar(36);index;not null" json:"listing_id"`
	CreatedAt time.Time `json:"created_at"`

	// 关联关系
	User    User    `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Listing Listing `gorm:"foreignKey:ListingID" json:"listing,omitempty"`
}

// TableName 指定表名
func (Listing) TableName() string {
	return "listings"
}

func (Favorite) TableName() string {
	return "favorites"
}

// BeforeCreate 创建前钩子
func (l *Listing) BeforeCreate(tx *gorm.DB) error {
	if l.ID == "" {
		l.ID = generateUUID()
	}
	return nil
}

func (f *Favorite) BeforeCreate(tx *gorm.DB) error {
	if f.ID == "" {
		f.ID = generateUUID()
	}
	return nil
}
