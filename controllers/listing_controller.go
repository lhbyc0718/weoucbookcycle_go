package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
	"weoucbookcycle_go/config"
	"weoucbookcycle_go/models"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// ListingController 发布控制器
type ListingController struct {
	redisClient *redis.Client
}

// NewListingController 创建发布控制器实例
func NewListingController() *ListingController {
	return &ListingController{
		redisClient: initRedis(),
	}
}

// CreateListingRequest 创建发布请求结构
type CreateListingRequest struct {
	BookID string  `json:"book_id" binding:"required"`
	Price  float64 `json:"price" binding:"required,gt=0"`
	Note   string  `json:"note" binding:"max=500"`
}

// UpdateListingStatusRequest 更新发布状态请求结构
type UpdateListingStatusRequest struct {
	Status  string `json:"status" binding:"required,oneof=available reserved sold cancelled"`
	BuyerID string `json:"buyer_id,omitempty"`
}

// GetListings 获取发布列表
// @Summary 获取发布列表
// @Description 分页获取发布列表
// @Tags listings
// @Accept json
// @Produce json
// @Param page query int false "页码" default(1)
// @Param limit query int false "每页数量" default(20)
// @Param status query string false "状态筛选"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/listings [get]
func (lc *ListingController) GetListings(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit
	status := c.Query("status")

	// 构建查询
	query := config.DB.Model(&models.Listing{})

	if status != "" {
		query = query.Where("status = ?", status)
	}

	// 获取总数
	var total int64
	query.Count(&total)

	// 获取数据
	var listings []models.Listing
	if err := query.
		Preload("Book").
		Preload("Book.Seller").
		Preload("Seller").
		Preload("Buyer").
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&listings).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get listings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"listings": listings,
		"total":    total,
		"page":     page,
		"limit":    limit,
	})
}

// GetListing 获取发布详情
// @Summary 获取发布详情
// @Description 根据发布ID获取详细信息
// @Tags listings
// @Accept json
// @Produce json
// @Param id path string true "发布ID"
// @Success 200 {object} models.Listing
// @Router /api/v1/listings/{id} [get]
func (lc *ListingController) GetListing(c *gin.Context) {
	listingID := c.Param("id")

	// 先尝试从Redis缓存获取
	cacheKey := "listing:" + listingID
	cached, err := lc.redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		var listing models.Listing
		if json.Unmarshal([]byte(cached), &listing) == nil {
			c.JSON(http.StatusOK, listing)
			return
		}
	}

	// 从数据库查询
	var listing models.Listing
	if err := config.DB.
		Preload("Book").
		Preload("Book.Seller").
		Preload("Seller").
		Preload("Buyer").
		First(&listing, "id = ?", listingID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Listing not found"})
		return
	}

	// 异步缓存到Redis
	go func() {
		data, _ := json.Marshal(listing)
		lc.redisClient.Set(ctx, cacheKey, data, time.Minute*10)
	}()

	c.JSON(http.StatusOK, listing)
}

// CreateListing 创建发布
// @Summary 创建发布
// @Description 创建新的书籍发布
// @Tags listings
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body CreateListingRequest true "发布信息"
// @Success 201 {object} models.Listing
// @Router /api/v1/listings [post]
func (lc *ListingController) CreateListing(c *gin.Context) {
	userID := c.GetString("user_id")

	var req CreateListingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 检查书籍是否存在
	var book models.Book
	if err := config.DB.First(&book, "id = ?", req.BookID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}

	// 检查是否已有发布的listing
	var existingListing models.Listing
	if err := config.DB.Where("book_id = ? AND seller_id = ? AND status IN ?",
		req.BookID, userID, []string{"available", "reserved"}).First(&existingListing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "This book is already listed"})
		return
	}

	listing := models.Listing{
		BookID:   req.BookID,
		SellerID: userID,
		Price:    req.Price,
		Note:     req.Note,
		Status:   "available",
	}

	if err := config.DB.Create(&listing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create listing"})
		return
	}

	// 异步发送通知给关注者（使用goroutine）
	go func() {
		// 这里可以添加通知逻辑
		// notifyFollowers(userID, listing.ID)
	}()

	c.JSON(http.StatusCreated, listing)
}

// UpdateListingStatus 更新发布状态
// @Summary 更新发布状态
// @Description 更新发布的状态（available/reserved/sold/cancelled）
// @Tags listings
// @Accept json
// @Produce json
// @Security Bearer
// @Param id path string true "发布ID"
// @Param request body UpdateListingStatusRequest true "状态更新信息"
// @Success 200 {object} models.Listing
// @Router /api/v1/listings/{id}/status [put]
func (lc *ListingController) UpdateListingStatus(c *gin.Context) {
	userID := c.GetString("user_id")
	listingID := c.Param("id")

	var listing models.Listing
	if err := config.DB.First(&listing, "id = ?", listingID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Listing not found"})
		return
	}

	var req UpdateListingStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 检查权限：只有卖家可以修改状态
	if listing.SellerID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to update this listing"})
		return
	}

	// 更新状态
	updates := map[string]interface{}{
		"status": req.Status,
	}

	// 如果是sold状态，设置买家ID
	if req.Status == "sold" && req.BuyerID != "" {
		updates["buyer_id"] = req.BuyerID
	}

	if err := config.DB.Model(&listing).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update listing status"})
		return
	}

	// 如果是sold状态，更新书籍状态
	if req.Status == "sold" {
		go func() {
			config.DB.Model(&models.Book{}).Where("id = ?", listing.BookID).Update("status", 0)
		}()
	}

	// 删除缓存
	go func() {
		lc.redisClient.Del(ctx, "listing:"+listingID)
	}()

	c.JSON(http.StatusOK, listing)
}

// GetMyListings 获取我的发布列表
// @Summary 获取我的发布列表
// @Description 获取当前登录用户的发布列表
// @Tags listings
// @Accept json
// @Produce json
// @Security Bearer
// @Success 200 {array} models.Listing
// @Router /api/v1/listings/mine [get]
func (lc *ListingController) GetMyListings(c *gin.Context) {
	userID := c.GetString("user_id")

	var listings []models.Listing
	if err := config.DB.
		Preload("Book").
		Where("seller_id = ?", userID).
		Order("created_at DESC").
		Find(&listings).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get my listings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"listings": listings})
}

// FavoriteListing 收藏/取消收藏发布
// @Summary 收藏发布
// @Description 收藏或取消收藏发布
// @Tags listings
// @Accept json
// @Produce json
// @Security Bearer
// @Param id path string true "发布ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/listings/{id}/favorite [post]
func (lc *ListingController) FavoriteListing(c *gin.Context) {
	userID := c.GetString("user_id")
	listingID := c.Param("id")

	// 检查是否已收藏
	var favorite models.Favorite
	err := config.DB.Where("user_id = ? AND listing_id = ?", userID, listingID).First(&favorite).Error

	if err == nil {
		// 已收藏，取消收藏
		if err := config.DB.Delete(&favorite).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unfavorite"})
			return
		}

		// 减少收藏计数
		go func() {
			config.DB.Exec("UPDATE listings SET favorite_count = favorite_count - 1 WHERE id = ?", listingID)
		}()

		c.JSON(http.StatusOK, gin.H{"message": "Unfavorited successfully"})
		return
	}

	// 未收藏，添加收藏
	favorite = models.Favorite{
		UserID:    userID,
		ListingID: listingID,
	}

	if err := config.DB.Create(&favorite).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to favorite"})
		return
	}

	// 增加收藏计数
	go func() {
		config.DB.Exec("UPDATE listings SET favorite_count = favorite_count + 1 WHERE id = ?", listingID)
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Favorited successfully"})
}
