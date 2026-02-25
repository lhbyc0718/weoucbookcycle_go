package models

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/google/uuid"
)

// generateUUID 生成UUID
func generateUUID() string {
	return uuid.New().String()
}

// generateRandomString 生成随机字符串
func generateRandomString(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)[:length]
}
