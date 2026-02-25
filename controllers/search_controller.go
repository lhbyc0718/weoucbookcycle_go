package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
	"weoucbookcycle_go/config"
	"weoucbookcycle_go/models"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// SearchController 搜索控制器
type SearchController struct {
	redisClient *redis.Client
}

// NewSearchController 创建搜索控制器实例
func NewSearchController() *SearchController {
	return &SearchController{
		redisClient: initRedis(),
	}
}

// SearchResult 搜索结果结构
type SearchResult struct {
	Books    []models.Book    `json:"books,omitempty"`
	Users    []models.User    `json:"users,omitempty"`
	Listings []models.Listing `json:"listings,omitempty"`
	Total    int              `json:"total"`
	Query    string           `json:"query"`
}

// GlobalSearch 全局搜索
// @Summary 全局搜索
// @Description 跨多个模块进行搜索
// @Tags search
// @Accept json
// @Produce json
// @Param q query string true "搜索关键词"
// @Param page query int false "页码" default(1)
// @Param limit query int false "每页数量" default(20)
// @Success 200 {object} SearchResult
// @Router /api/v1/search [get]
func (sc *SearchController) GlobalSearch(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Search query is required"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	// 检查Redis缓存
	cacheKey := "search:global:" + query + ":" + strconv.Itoa(page)
	cached, err := sc.redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		var result SearchResult
		if json.Unmarshal([]byte(cached), &result) == nil {
			c.JSON(http.StatusOK, result)
			return
		}
	}

	// 记录搜索关键词（异步）
	go func() {
		sc.redisClient.ZIncrBy(ctx, "search:hot", 1, query)
		sc.redisClient.Expire(ctx, "search:hot", time.Hour*24)
	}()

	// 使用goroutine并发搜索多个数据源
	var wg sync.WaitGroup
	var mu sync.Mutex

	result := SearchResult{
		Query: query,
	}

	// 并发搜索书籍
	wg.Add(1)
	go func() {
		defer wg.Done()

		searchPattern := "%" + query + "%"
		var books []models.Book

		config.DB.
			Where("status = ?", 1).
			Where("title LIKE ? OR author LIKE ? OR description LIKE ?",
				searchPattern, searchPattern, searchPattern).
			Limit(limit).
			Find(&books)

		mu.Lock()
		result.Books = books
		result.Total += len(books)
		mu.Unlock()
	}()

	// 并发搜索用户
	wg.Add(1)
	go func() {
		defer wg.Done()

		searchPattern := "%" + query + "%"
		var users []models.User

		config.DB.
			Where("username LIKE ? OR email LIKE ? OR bio LIKE ?",
				searchPattern, searchPattern, searchPattern).
			Limit(limit).
			Find(&users)

		mu.Lock()
		result.Users = users
		result.Total += len(users)
		mu.Unlock()
	}()

	// 并发搜索发布
	wg.Add(1)
	go func() {
		defer wg.Done()

		searchPattern := "%" + query + "%"
		var listings []models.Listing

		config.DB.
			Preload("Book").
			Where("status = ?", "available").
			Joins("JOIN books ON listings.book_id = books.id").
			Where("books.title LIKE ? OR books.author LIKE ? OR listings.note LIKE ?",
				searchPattern, searchPattern, searchPattern).
			Limit(limit).
			Find(&listings)

		mu.Lock()
		result.Listings = listings
		result.Total += len(listings)
		mu.Unlock()
	}()

	wg.Wait()

	// 异步缓存搜索结果
	go func() {
		data, _ := json.Marshal(result)
		sc.redisClient.Set(ctx, cacheKey, data, time.Minute*5)
	}()

	c.JSON(http.StatusOK, result)
}

// SearchUsers 搜索用户
// @Summary 搜索用户
// @Description 搜索用户
// @Tags search
// @Accept json
// @Produce json
// @Param q query string true "搜索关键词"
// @Param page query int false "页码" default(1)
// @Param limit query int false "每页数量" default(20)
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/search/users [get]
func (sc *SearchController) SearchUsers(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Search query is required"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit

	// 检查缓存
	cacheKey := "search:users:" + query + ":" + strconv.Itoa(page)
	cached, err := sc.redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		var result map[string]interface{}
		if json.Unmarshal([]byte(cached), &result) == nil {
			c.JSON(http.StatusOK, result)
			return
		}
	}

	searchPattern := "%" + query + "%"
	var users []models.User
	var total int64

	config.DB.Model(&models.User{}).
		Where("username LIKE ? OR email LIKE ? OR bio LIKE ?",
			searchPattern, searchPattern, searchPattern).
		Count(&total)

	config.DB.Where("username LIKE ? OR email LIKE ? OR bio LIKE ?",
		searchPattern, searchPattern, searchPattern).
		Limit(limit).
		Offset(offset).
		Find(&users)

	result := gin.H{
		"users": users,
		"total": total,
		"page":  page,
		"limit": limit,
		"query": query,
	}

	// 异步缓存
	go func() {
		data, _ := json.Marshal(result)
		sc.redisClient.Set(ctx, cacheKey, data, time.Minute*5)
	}()

	c.JSON(http.StatusOK, result)
}

// SearchBooks 搜索书籍
// @Summary 搜索书籍
// @Description 搜索书籍
// @Tags search
// @Accept json
// @Produce json
// @Param q query string true "搜索关键词"
// @Param page query int false "页码" default(1)
// @Param limit query int false "每页数量" default(20)
// @Param category query string false "分类筛选"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/search/books [get]
func (sc *SearchController) SearchBooks(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Search query is required"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit
	category := c.Query("category")

	// 检查缓存
	cacheKey := "search:books:" + query + ":" + strconv.Itoa(page)
	if category != "" {
		cacheKey += ":" + category
	}

	cached, err := sc.redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		var result map[string]interface{}
		if json.Unmarshal([]byte(cached), &result) == nil {
			c.JSON(http.StatusOK, result)
			return
		}
	}

	// 记录搜索
	go func() {
		sc.redisClient.ZIncrBy(ctx, "search:hot", 1, query)
	}()

	searchPattern := "%" + query + "%"
	var books []models.Book
	var total int64

	baseQuery := config.DB.Model(&models.Book{}).Where("status = ?", 1).
		Where("title LIKE ? OR author LIKE ? OR description LIKE ? OR category LIKE ?",
			searchPattern, searchPattern, searchPattern, searchPattern)

	if category != "" {
		baseQuery = baseQuery.Where("category = ?", category)
	}

	baseQuery.Count(&total)

	config.DB.Where("status = ?", 1).
		Where("title LIKE ? OR author LIKE ? OR description LIKE ? OR category LIKE ?",
			searchPattern, searchPattern, searchPattern, searchPattern).
		Preload("Seller").
		Limit(limit).
		Offset(offset).
		Find(&books)

	result := gin.H{
		"books": books,
		"total": total,
		"page":  page,
		"limit": limit,
		"query": query,
	}

	// 异步缓存
	go func() {
		data, _ := json.Marshal(result)
		sc.redisClient.Set(ctx, cacheKey, data, time.Minute*5)
	}()

	c.JSON(http.StatusOK, result)
}

// GetHotSearchKeywords 获取热门搜索词
// @Summary 获取热门搜索词
// @Description 获取最近搜索的热门关键词
// @Tags search
// @Accept json
// @Produce json
// @Param limit query int false "数量" default(10)
// @Success 200 {array} string
// @Router /api/v1/search/hot [get]
func (sc *SearchController) GetHotSearchKeywords(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	// 从Redis获取热门搜索（使用sorted set）
	keywords, err := sc.redisClient.ZRevRange(ctx, "search:hot", 0, int64(limit-1)).Result()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get hot search keywords"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"keywords": keywords,
	})
}

// GetSuggestions 获取搜索建议
// @Summary 获取搜索建议
// @Description 根据输入获取搜索建议
// @Tags search
// @Accept json
// @Produce json
// @Param q query string true "输入关键词"
// @Success 200 {array} string
// @Router /api/v1/search/suggestions [get]
func (sc *SearchController) GetSuggestions(c *gin.Context) {
	query := c.Query("q")
	if query == "" || len(query) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Query must be at least 2 characters"})
		return
	}

	// 检查缓存
	cacheKey := "search:suggestions:" + query
	cached, err := sc.redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		var suggestions []string
		if json.Unmarshal([]byte(cached), &suggestions) == nil {
			c.JSON(http.StatusOK, gin.H{"suggestions": suggestions})
			return
		}
	}

	searchPattern := query + "%"

	// 并发获取书籍标题和作者名作为建议
	var wg sync.WaitGroup
	var mu sync.Mutex
	suggestions := []string{}

	// 书籍标题建议
	wg.Add(1)
	go func() {
		defer wg.Done()

		var titles []string
		config.DB.Model(&models.Book{}).
			Where("title LIKE ? AND status = ?", searchPattern, 1).
			Limit(5).
			Pluck("title", &titles)

		mu.Lock()
		suggestions = append(suggestions, titles...)
		mu.Unlock()
	}()

	// 作者建议
	wg.Add(1)
	go func() {
		defer wg.Done()

		var authors []string
		config.DB.Model(&models.Book{}).
			Where("author LIKE ? AND status = ?", searchPattern, 1).
			Group("author").
			Limit(5).
			Pluck("author", &authors)

		mu.Lock()
		suggestions = append(suggestions, authors...)
		mu.Unlock()
	}()

	wg.Wait()

	// 去重
	uniqueSuggestions := make(map[string]bool)
	var result []string
	for _, s := range suggestions {
		if !uniqueSuggestions[s] {
			uniqueSuggestions[s] = true
			result = append(result, s)
		}
	}

	// 异步缓存
	go func() {
		data, _ := json.Marshal(result)
		sc.redisClient.Set(ctx, cacheKey, data, time.Minute*30)
	}()

	c.JSON(http.StatusOK, gin.H{"suggestions": result})
}
