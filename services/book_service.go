package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
	"weoucbookcycle_go/config"
	"weoucbookcycle_go/models"

	"github.com/redis/go-redis/v9"
)

// BookService 书籍服务
type BookService struct {
	// 浏览统计队列
	viewStatsQueue chan *BookViewStat
	// 点赞统计队列
	likeStatsQueue chan *BookLikeStat
	// 搜索索引队列
	indexQueue chan *BookIndexTask
}

// BookViewStat 书籍浏览统计
type BookViewStat struct {
	BookID    string
	UserID    string
	Timestamp time.Time
	IP        string
}

// BookLikeStat 书籍点赞统计
type BookLikeStat struct {
	BookID    string
	UserID    string
	Type      string // "like" or "unlike"
	Timestamp time.Time
}

// BookIndexTask 书籍索引任务
type BookIndexTask struct {
	BookID string
	Action string // "index", "remove"
}

// NewBookService 创建书籍服务实例
func NewBookService() *BookService {
	bs := &BookService{
		viewStatsQueue: make(chan *BookViewStat, 2000),
		likeStatsQueue: make(chan *BookLikeStat, 2000),
		indexQueue:     make(chan *BookIndexTask, 1000),
	}

	// 启动统计worker池
	bs.startStatsWorkers()

	// 启动索引worker
	bs.startIndexWorker()

	return bs
}

// ==================== CRUD操作 ====================

// CreateBookRequest 创建书籍请求
type CreateBookRequest struct {
	Title       string   `json:"title" binding:"required,max=200"`
	Author      string   `json:"author" binding:"required,max=100"`
	ISBN        string   `json:"isbn" binding:"omitempty"`
	Category    string   `json:"category" binding:"required"`
	Price       float64  `json:"price" binding:"required,gt=0"`
	Description string   `json:"description"`
	Images      []string `json:"images"`
	Condition   string   `json:"condition" binding:"required,oneof=全新 九成新 八成新 七成新 其他"`
}

// UpdateBookRequest 更新书籍请求
type UpdateBookRequest struct {
	Title       string   `json:"title" binding:"omitempty,max=200"`
	Author      string   `json:"author" binding:"omitempty,max=100"`
	ISBN        string   `json:"isbn" binding:"omitempty"`
	Category    string   `json:"category" binding:"omitempty"`
	Price       float64  `json:"price" binding:"omitempty,gt=0"`
	Description string   `json:"description"`
	Images      []string `json:"images"`
	Condition   string   `json:"condition" binding:"omitempty,oneof=全新 九成新 八成新 七成新 其他"`
	Status      int      `json:"status" binding:"omitempty,oneof=0 1 2"`
}

// CreateBook 创建书籍
func (bs *BookService) CreateBook(userID string, req *CreateBookRequest) (*models.Book, error) {
	// 1. 验证ISBN格式（如果提供）
	if req.ISBN != "" {
		if !isValidISBN(req.ISBN) {
			return nil, errors.New("invalid ISBN format")
		}

		// 检查ISBN是否已存在
		var existingBook models.Book
		if err := config.DB.Where("isbn = ?", req.ISBN).First(&existingBook).Error; err == nil {
			return nil, errors.New("ISBN already exists")
		}
	}

	// 2. 转换图片数组为JSON
	imagesJSON, _ := json.Marshal(req.Images)

	// 3. 创建书籍
	book := models.Book{
		Title:       req.Title,
		Author:      req.Author,
		ISBN:        req.ISBN,
		Category:    req.Category,
		Price:       req.Price,
		Description: req.Description,
		Images:      string(imagesJSON),
		Condition:   req.Condition,
		SellerID:    userID,
		Status:      1,
		ViewCount:   0,
		LikeCount:   0,
	}

	if err := config.DB.Create(&book).Error; err != nil {
		return nil, fmt.Errorf("failed to create book: %w", err)
	}

	// 4. 异步清除缓存
	go bs.clearBookCaches(book.ID)

	// 5. 异步添加到搜索索引
	go func() {
		bs.indexQueue <- &BookIndexTask{
			BookID: book.ID,
			Action: "index",
		}
	}()

	// 6. 记录创建事件
	go func() {
		if config.RedisClient != nil {
			config.RedisClient.XAdd(redisCtx, &redis.XAddArgs{
				Stream: "book_events",
				Values: map[string]interface{}{
					"event":     "book_created",
					"book_id":   book.ID,
					"title":     book.Title,
					"seller_id": userID,
					"timestamp": time.Now().Unix(),
				},
			})
		}
	}()

	return &book, nil
}

// UpdateBook 更新书籍
func (bs *BookService) UpdateBook(userID, bookID string, req *UpdateBookRequest) (*models.Book, error) {
	// 1. 查找书籍
	var book models.Book
	if err := config.DB.First(&book, "id = ?", bookID).Error; err != nil {
		return nil, errors.New("book not found")
	}

	// 2. 检查权限
	if book.SellerID != userID {
		return nil, errors.New("you don't have permission to update this book")
	}

	// 3. 如果修改ISBN，检查是否重复
	if req.ISBN != "" && req.ISBN != book.ISBN {
		if !isValidISBN(req.ISBN) {
			return nil, errors.New("invalid ISBN format")
		}

		var existingBook models.Book
		if err := config.DB.Where("isbn = ? AND id != ?", req.ISBN, bookID).First(&existingBook).Error; err == nil {
			return nil, errors.New("ISBN already exists")
		}
	}

	// 4. 构建更新map
	updates := make(map[string]interface{})
	if req.Title != "" {
		updates["title"] = req.Title
	}
	if req.Author != "" {
		updates["author"] = req.Author
	}
	if req.ISBN != "" {
		updates["isbn"] = req.ISBN
	}
	if req.Category != "" {
		updates["category"] = req.Category
	}
	if req.Price > 0 {
		updates["price"] = req.Price
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if len(req.Images) > 0 {
		imagesJSON, _ := json.Marshal(req.Images)
		updates["images"] = string(imagesJSON)
	}
	if req.Condition != "" {
		updates["condition"] = req.Condition
	}
	if req.Status >= 0 {
		updates["status"] = req.Status
	}

	// 5. 更新数据库
	if err := config.DB.Model(&book).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("failed to update book: %w", err)
	}

	// 6. 重新查询更新后的数据
	if err := config.DB.First(&book, "id = ?", bookID).Error; err != nil {
		return nil, err
	}

	// 7. 异步清除缓存
	go bs.clearBookCaches(bookID)

	// 8. 异步更新搜索索引
	go func() {
		bs.indexQueue <- &BookIndexTask{
			BookID: book.ID,
			Action: "index",
		}
	}()

	return &book, nil
}

// DeleteBook 删除书籍
func (bs *BookService) DeleteBook(userID, bookID string) error {
	// 1. 查找书籍
	var book models.Book
	if err := config.DB.First(&book, "id = ?", bookID).Error; err != nil {
		return errors.New("book not found")
	}

	// 2. 检查权限
	if book.SellerID != userID {
		return errors.New("you don't have permission to delete this book")
	}

	// 3. 软删除
	if err := config.DB.Delete(&book).Error; err != nil {
		return fmt.Errorf("failed to delete book: %w", err)
	}

	// 4. 异步清除所有相关缓存
	go bs.clearBookCaches(bookID)

	// 5. 异步从搜索索引移除
	go func() {
		bs.indexQueue <- &BookIndexTask{
			BookID: bookID,
			Action: "remove",
		}
	}()

	return nil
}

// ==================== 查询方法 ====================

// GetBook 获取书籍详情
func (bs *BookService) GetBook(bookID, userID string) (*models.Book, error) {
	// 1. 尝试从Redis缓存获取
	cacheKey := fmt.Sprintf("book:%s", bookID)
	if config.RedisClient != nil {
		cached, err := config.RedisClient.Get(redisCtx, cacheKey).Result()
		if err == nil {
			var book models.Book
			if json.Unmarshal([]byte(cached), &book) == nil {
				// 异步记录浏览统计
				bs.viewStatsQueue <- &BookViewStat{
					BookID:    bookID,
					UserID:    userID,
					Timestamp: time.Now(),
				}
				return &book, nil
			}
		}
	}

	// 2. 从数据库查询
	var book models.Book
	if err := config.DB.Preload("Seller").First(&book, "id = ?", bookID).Error; err != nil {
		return nil, errors.New("book not found")
	}

	// 3. 异步记录浏览统计
	bs.viewStatsQueue <- &BookViewStat{
		BookID:    bookID,
		UserID:    userID,
		Timestamp: time.Now(),
	}

	// 4. 异步缓存到Redis
	go func() {
		data, _ := json.Marshal(book)
		config.RedisClient.Set(redisCtx, cacheKey, data, 10*time.Minute)
	}()

	return &book, nil
}

// GetBooks 获取书籍列表
func (bs *BookService) GetBooks(page, limit int, filters map[string]interface{}, sort string) ([]models.Book, int64, error) {
	offset := (page - 1) * limit

	// 1. 构建缓存key
	cacheKey := bs.buildBooksCacheKey(page, limit, filters, sort)

	// 2. 尝试从Redis获取
	if config.RedisClient != nil {
		cached, err := config.RedisClient.Get(redisCtx, cacheKey).Result()
		if err == nil {
			var result struct {
				Books []models.Book `json:"books"`
				Total int64         `json:"total"`
			}
			if json.Unmarshal([]byte(cached), &result) == nil {
				return result.Books, result.Total, nil
			}
		}
	}

	// 3. 构建查询
	query := config.DB.Model(&models.Book{}).Where("status = ?", 1)

	// 应用筛选条件
	if category, ok := filters["category"].(string); ok && category != "" {
		query = query.Where("category = ?", category)
	}
	if author, ok := filters["author"].(string); ok && author != "" {
		query = query.Where("author LIKE ?", "%"+author+"%")
	}
	if condition, ok := filters["condition"].(string); ok && condition != "" {
		query = query.Where("condition = ?", condition)
	}
	if minPrice, ok := filters["min_price"].(float64); ok && minPrice > 0 {
		query = query.Where("price >= ?", minPrice)
	}
	if maxPrice, ok := filters["max_price"].(float64); ok && maxPrice > 0 {
		query = query.Where("price <= ?", maxPrice)
	}
	if sellerID, ok := filters["seller_id"].(string); ok && sellerID != "" {
		query = query.Where("seller_id = ?", sellerID)
	}

	// 4. 获取总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count books: %w", err)
	}

	// 5. 获取数据
	var books []models.Book
	if err := query.
		Preload("Seller").
		Order(sort + " DESC").
		Limit(limit).
		Offset(offset).
		Find(&books).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get books: %w", err)
	}

	// 6. 异步缓存结果
	go func() {
		if config.RedisClient != nil {
			result := struct {
				Books []models.Book `json:"books"`
				Total int64         `json:"total"`
			}{books, total}
			data, _ := json.Marshal(result)
			config.RedisClient.Set(redisCtx, cacheKey, data, 5*time.Minute)
		}
	}()

	return books, total, nil
}

// GetHotBooks 获取热门书籍
func (bs *BookService) GetHotBooks(limit int) ([]models.Book, error) {
	cacheKey := "hot:books"

	// 1. 尝试从Redis获取
	if config.RedisClient != nil {
		cached, err := config.RedisClient.Get(redisCtx, cacheKey).Result()
		if err == nil {
			var books []models.Book
			if json.Unmarshal([]byte(cached), &books) == nil {
				return books, nil
			}
		}
	}

	// 2. 从数据库获取（根据浏览数和点赞数排序）
	var books []models.Book
	if err := config.DB.
		Where("status = ?", 1).
		Order("view_count DESC, like_count DESC, created_at DESC").
		Limit(limit).
		Find(&books).Error; err != nil {
		return nil, fmt.Errorf("failed to get hot books: %w", err)
	}

	// 3. 异步缓存
	go func() {
		if config.RedisClient != nil {
			data, _ := json.Marshal(books)
			config.RedisClient.Set(redisCtx, cacheKey, data, 10*time.Minute)
		}
	}()

	return books, nil
}

// ==================== 搜索方法 ====================

// SearchBooks 搜索书籍
func (bs *BookService) SearchBooks(query string, page, limit int) ([]models.Book, int64, error) {
	// 1. 构建缓存key
	cacheKey := fmt.Sprintf("search:books:%s:%d", query, page)

	// 2. 尝试从Redis获取
	if config.RedisClient != nil {
		cached, err := config.RedisClient.Get(redisCtx, cacheKey).Result()
		if err == nil {
			var result struct {
				Books []models.Book `json:"books"`
				Total int64         `json:"total"`
			}
			if json.Unmarshal([]byte(cached), &result) == nil {
				// 记录搜索关键词
				go bs.recordSearchKeyword(query)
				return result.Books, result.Total, nil
			}
		}
	}

	// 3. 记录搜索关键词
	go bs.recordSearchKeyword(query)

	// 4. 数据库搜索
	searchPattern := "%" + query + "%"
	var books []models.Book
	var total int64

	baseQuery := config.DB.Model(&models.Book{}).Where("status = ?", 1).
		Where("title LIKE ? OR author LIKE ? OR description LIKE ? OR category LIKE ?",
			searchPattern, searchPattern, searchPattern, searchPattern)

	baseQuery.Count(&total)

	if err := baseQuery.
		Preload("Seller").
		Limit(limit).
		Offset((page - 1) * limit).
		Find(&books).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to search books: %w", err)
	}

	// 5. 异步缓存结果
	go func() {
		if config.RedisClient != nil {
			result := struct {
				Books []models.Book `json:"books"`
				Total int64         `json:"total"`
			}{books, total}
			data, _ := json.Marshal(result)
			config.RedisClient.Set(redisCtx, cacheKey, data, 5*time.Minute)
		}
	}()

	return books, total, nil
}

// ==================== 点赞方法 ====================

// LikeBook 点赞书籍
func (bs *BookService) LikeBook(userID, bookID string) (bool, error) {
	// 1. 检查是否已点赞
	likeKey := fmt.Sprintf("like:%s:%s", userID, bookID)
	if config.RedisClient != nil {
		exists, _ := config.RedisClient.Exists(redisCtx, likeKey).Result()
		if exists > 0 {
			// 取消点赞
			config.RedisClient.Del(redisCtx, likeKey)
			bs.likeStatsQueue <- &BookLikeStat{
				BookID:    bookID,
				UserID:    userID,
				Type:      "unlike",
				Timestamp: time.Now(),
			}
			return false, nil
		}
	}

	// 2. 添加点赞
	if config.RedisClient != nil {
		config.RedisClient.Set(redisCtx, likeKey, "1", 30*24*time.Hour)
	}

	bs.likeStatsQueue <- &BookLikeStat{
		BookID:    bookID,
		UserID:    userID,
		Type:      "like",
		Timestamp: time.Now(),
	}

	return true, nil
}

// ==================== 推荐方法 ====================

// GetRecommendations 获取推荐书籍
func (bs *BookService) GetRecommendations(userID string, limit int) ([]models.Book, error) {
	cacheKey := fmt.Sprintf("recommendations:%s", userID)

	// 1. 尝试从Redis获取
	if config.RedisClient != nil {
		cached, err := config.RedisClient.Get(redisCtx, cacheKey).Result()
		if err == nil {
			var books []models.Book
			if json.Unmarshal([]byte(cached), &books) == nil {
				return books, nil
			}
		}
	}

	// 2. 基于用户浏览历史推荐
	var books []models.Book

	// 获取用户浏览历史
	historyKey := fmt.Sprintf("history:view:%s", userID)
	viewedBooks, _ := config.RedisClient.LRange(redisCtx, historyKey, 0, 9).Result()

	if len(viewedBooks) > 0 {
		// 基于浏览过的书籍的类别推荐
		var categories []string
		for _, bookID := range viewedBooks {
			var book models.Book
			if err := config.DB.Select("category").First(&book, "id = ?", bookID).Error; err == nil {
				categories = append(categories, book.Category)
			}
		}

		// 获取同类别的热门书籍
		if len(categories) > 0 {
			if err := config.DB.
				Where("status = ?", 1).
				Where("category IN ?", categories).
				Not("id", viewedBooks).
				Order("like_count DESC, view_count DESC").
				Limit(limit).
				Find(&books).Error; err != nil {
			} else {
				// 有推荐结果，缓存并返回
				go func() {
					data, _ := json.Marshal(books)
					config.RedisClient.Set(redisCtx, cacheKey, data, time.Hour)
				}()
				return books, nil
			}
		}
	}

	// 如果没有历史记录，返回热门书籍
	return bs.GetHotBooks(limit)
}

// ==================== Worker相关方法 ====================

// startStatsWorkers 启动统计worker池
func (bs *BookService) startStatsWorkers() {
	// 浏览统计worker
	for i := 0; i < 5; i++ {
		go bs.processViewStats(i)
	}

	// 点赞统计worker
	for i := 0; i < 5; i++ {
		go bs.processLikeStats(i)
	}
}

// startIndexWorker 启动索引worker
func (bs *BookService) startIndexWorker() {
	go func() {
		for task := range bs.indexQueue {
			bs.processIndexTask(task)
		}
	}()
}

// processViewStats 处理浏览统计
func (bs *BookService) processViewStats(workerID int) {
	for stat := range bs.viewStatsQueue {
		// 更新数据库（使用原子操作）
		config.DB.Exec("UPDATE books SET view_count = view_count + 1 WHERE id = ?", stat.BookID)

		// 更新Redis排行榜
		if config.RedisClient != nil {
			config.RedisClient.ZIncrBy(redisCtx, "rank:book:views", 1, stat.BookID)
			config.RedisClient.Expire(redisCtx, "rank:book:views", 7*24*time.Hour)
		}

		// 记录用户浏览历史
		if config.RedisClient != nil && stat.UserID != "" {
			historyKey := fmt.Sprintf("history:view:%s", stat.UserID)
			config.RedisClient.LPush(redisCtx, historyKey, stat.BookID)
			config.RedisClient.LTrim(redisCtx, historyKey, 0, 99) // 保留最近100条
			config.RedisClient.Expire(redisCtx, historyKey, 30*24*time.Hour)
		}
	}
}

// processLikeStats 处理点赞统计
func (bs *BookService) processLikeStats(workerID int) {
	for stat := range bs.likeStatsQueue {
		switch stat.Type {
		case "like":
			config.DB.Exec("UPDATE books SET like_count = like_count + 1 WHERE id = ?", stat.BookID)
			if config.RedisClient != nil {
				config.RedisClient.ZIncrBy(redisCtx, "rank:book:likes", 1, stat.BookID)
			}
		case "unlike":
			config.DB.Exec("UPDATE books SET like_count = like_count - 1 WHERE id = ?", stat.BookID)
			if config.RedisClient != nil {
				config.RedisClient.ZIncrBy(redisCtx, "rank:book:likes", -1, stat.BookID)
			}
		}
	}
}

// processIndexTask 处理索引任务
func (bs *BookService) processIndexTask(task *BookIndexTask) {
	if task.Action == "remove" {
		bs.removeFromSearchIndex(task.BookID)
	} else {
		var book models.Book
		if err := config.DB.First(&book, "id = ?", task.BookID).Error; err == nil {
			bs.indexBookForSearch(&book)
		}
	}
}

// ==================== 辅助方法 ====================

// clearBookCaches 清除书籍相关缓存
func (bs *BookService) clearBookCaches(bookID string) {
	if config.RedisClient == nil {
		return
	}

	// 使用goroutine并发清除多个缓存
	var wg sync.WaitGroup
	cacheKeys := []string{
		fmt.Sprintf("book:%s", bookID),
		"hot:books",
	}

	wg.Add(len(cacheKeys))
	for _, key := range cacheKeys {
		go func(k string) {
			defer wg.Done()
			config.RedisClient.Del(redisCtx, k)
		}(key)
	}
	wg.Wait()

	// 清除搜索缓存（模糊匹配）
	keys, _ := config.RedisClient.Keys(redisCtx, "search:books:*").Result()
	for _, key := range keys {
		config.RedisClient.Del(redisCtx, key)
	}

	// 清除推荐缓存
	pattern := "recommendations:*"
	recKeys, _ := config.RedisClient.Keys(redisCtx, pattern).Result()
	for _, key := range recKeys {
		config.RedisClient.Del(redisCtx, key)
	}
}

// indexBookForSearch 索引书籍用于搜索
func (bs *BookService) indexBookForSearch(book *models.Book) {
	if config.RedisClient == nil {
		return
	}

	// 将书籍信息存入Redis Hash
	indexKey := fmt.Sprintf("book:index:%s", book.ID)
	bookData := map[string]interface{}{
		"id":         book.ID,
		"title":      book.Title,
		"author":     book.Author,
		"category":   book.Category,
		"price":      book.Price,
		"condition":  book.Condition,
		"seller_id":  book.SellerID,
		"status":     book.Status,
		"created_at": book.CreatedAt.Unix(),
		"updated_at": book.UpdatedAt.Unix(),
	}

	config.RedisClient.HMSet(redisCtx, indexKey, bookData)
	config.RedisClient.Expire(redisCtx, indexKey, 24*time.Hour)
}

// removeFromSearchIndex 从搜索索引中移除
func (bs *BookService) removeFromSearchIndex(bookID string) {
	if config.RedisClient == nil {
		return
	}

	indexKey := fmt.Sprintf("book:index:%s", bookID)
	config.RedisClient.Del(redisCtx, indexKey)
}

// recordSearchKeyword 记录搜索关键词
func (bs *BookService) recordSearchKeyword(query string) {
	if config.RedisClient == nil {
		return
	}

	config.RedisClient.ZIncrBy(redisCtx, "search:hot", 1, query)
	config.RedisClient.Expire(redisCtx, "search:hot", 24*time.Hour)
}

// buildBooksCacheKey 构建书籍列表缓存key
func (bs *BookService) buildBooksCacheKey(page, limit int, filters map[string]interface{}, sort string) string {
	return fmt.Sprintf("books:page:%d:limit:%d:sort:%s", page, limit, sort)
}

// isValidISBN 验证ISBN格式
func isValidISBN(isbn string) bool {
	// 简单验证：ISBN-10 或 ISBN-13
	if len(isbn) == 10 || len(isbn) == 13 {
		return true
	}
	return false
}
