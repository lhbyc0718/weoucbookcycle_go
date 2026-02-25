package utils

import (
	"errors"
	"fmt"
	"regexp"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

var (
	validate = validator.New()
	// 自定义验证错误缓存
	validationErrorsCache = make(map[string]string)
)

// 初始化验证器
func init() {
	// 注册自定义验证规则
	validate.RegisterValidation("password", validatePassword)
	validate.RegisterValidation("username", validateUsername)
	validate.RegisterValidation("isbn", validateISBN)
}

// Validator 验证器结构
type Validator struct {
	validator *validator.Validate
}

// NewValidator 创建新的验证器实例
func NewValidator() *Validator {
	return &Validator{
		validator: validate,
	}
}

// Validate 验证结构体
func (v *Validator) Validate(obj interface{}) error {
	if err := v.validator.Struct(obj); err != nil {
		var validationErrors validator.ValidationErrors
		if errors.As(err, &validationErrors) {
			return formatValidationErrors(validationErrors)
		}
		return err
	}
	return nil
}

// formatValidationErrors 格式化验证错误信息
func formatValidationErrors(errors []validator.FieldError) error {
	errorMap := make(map[string]string)

	for _, err := range errors {
		field := err.Field()
		tag := err.Tag()
		param := err.Param()

		// 先尝试从缓存中获取错误信息
		cacheKey := fmt.Sprintf("%s_%s", field, tag)
		if msg, exists := validationErrorsCache[cacheKey]; exists {
			errorMap[field] = msg
			continue
		}

		// 生成自定义错误信息
		msg := getErrorMessage(field, tag, param)
		validationErrorsCache[cacheKey] = msg
		errorMap[field] = msg
	}

	return &ValidationError{Errors: errorMap}
}

// ValidationError 验证错误结构
type ValidationError struct {
	Errors map[string]string `json:"errors"`
}

func (ve *ValidationError) Error() string {
	return fmt.Sprintf("Validation failed: %v", ve.Errors)
}

// getErrorMessage 获取错误消息
func getErrorMessage(field, tag, param string) string {
	// 中文错误消息映射
	errorMessages := map[string]string{
		"required": "%s不能为空",
		"email":    "%s格式不正确",
		"min":      "%s长度不能小于%s",
		"max":      "%s长度不能大于%s",
		"gt":       "%s必须大于%s",
		"gte":      "%s必须大于或等于%s",
		"lt":       "%s必须小于%s",
		"lte":      "%s必须小于或等于%s",
		"oneof":    "%s必须是以下值之一: %s",
		"alpha":    "%s只能包含字母",
		"alphanum": "%s只能包含字母和数字",
		"numeric":  "%s必须是数字",
		"e164":     "%s必须是有效的手机号",
		"password": "%s格式不正确，必须包含大小写字母、数字和特殊字符",
		"username": "%s只能包含字母、数字和下划线，且以字母开头",
		"isbn":     "%s格式不正确",
	}

	fieldNames := map[string]string{
		"username": "用户名",
		"email":    "邮箱",
		"password": "密码",
		"phone":    "手机号",
		"title":    "标题",
		"price":    "价格",
		"content":  "内容",
	}

	fieldName, _ := fieldNames[field]
	if fieldName == "" {
		fieldName = field
	}

	template, exists := errorMessages[tag]
	if !exists {
		return fmt.Sprintf("%s验证失败", fieldName)
	}

	return fmt.Sprintf(template, fieldName, param)
}

// 自定义验证规则

// validatePassword 密码验证
func validatePassword(fl validator.FieldLevel) bool {
	password := fl.Field().String()

	if len(password) < 8 {
		return false
	}

	var (
		hasUpper   bool
		hasLower   bool
		hasNumber  bool
		hasSpecial bool
	)

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	return hasUpper && hasLower && hasNumber && hasSpecial
}

// validateUsername 用户名验证
func validateUsername(fl validator.FieldLevel) bool {
	username := fl.Field().String()

	if len(username) < 3 || len(username) > 20 {
		return false
	}

	// 只能包含字母、数字和下划线
	matched, _ := regexp.MatchString(`^[a-zA-Z][a-zA-Z0-9_]*$`, username)
	return matched
}

// validateISBN ISBN验证
func validateISBN(fl validator.FieldLevel) bool {
	isbn := fl.Field().String()

	if isbn == "" {
		return true // 允许为空
	}

	// ISBN-10 或 ISBN-13
	isbn10Regex := `^(?:\d[\d-]{8}[\dX])$`
	isbn13Regex := `^(?:\d[\d-]{12}[\dX])$`

	matched10, _ := regexp.MatchString(isbn10Regex, isbn)
	matched13, _ := regexp.MatchString(isbn13Regex, isbn)

	return matched10 || matched13
}

// BindAndValidate 绑定并验证请求
func BindAndValidate(c *gin.Context, obj interface{}) error {
	if err := c.ShouldBindJSON(obj); err != nil {
		return err
	}

	v := NewValidator()
	if err := v.Validate(obj); err != nil {
		return err
	}

	return nil
}

// ValidateEmail 验证邮箱格式
func ValidateEmail(email string) bool {
	if email == "" {
		return false
	}
	emailRegex := `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	matched, _ := regexp.MatchString(emailRegex, email)
	return matched
}

// ValidatePhone 验证手机号（中国大陆）
func ValidatePhone(phone string) bool {
	if phone == "" {
		return false
	}
	phoneRegex := `^1[3-9]\d{9}$`
	matched, _ := regexp.MatchString(phoneRegex, phone)
	return matched
}

// SanitizeString 清理字符串（防止XSS）
func SanitizeString(input string) string {
	// 移除HTML标签
	reg := regexp.MustCompile(`<[^>]*>`)
	cleaned := reg.ReplaceAllString(input, "")

	// 移除JavaScript代码
	jsRegex := regexp.MustCompile(`<script[^>]*>.*?</script>`)
	cleaned = jsRegex.ReplaceAllString(cleaned, "")

	return cleaned
}

// LimitStringLength 限制字符串长度
func LimitStringLength(input string, maxLength int) string {
	if len(input) <= maxLength {
		return input
	}
	return input[:maxLength]
}
