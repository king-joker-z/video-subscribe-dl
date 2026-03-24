package pornhub

import (
	"errors"
	"fmt"
	"strings"
)

// PHError Pornhub 平台专属错误
type PHError struct {
	Code    int    // HTTP 状态码（0 表示非 HTTP 错误）
	Message string // 错误描述
}

func (e *PHError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("pornhub error (HTTP %d): %s", e.Code, e.Message)
	}
	return fmt.Sprintf("pornhub error: %s", e.Message)
}

// 哨兵错误，用于 errors.Is 判断
var (
	ErrRateLimit   = errors.New("pornhub: rate limited")
	ErrUnavailable = errors.New("pornhub: content unavailable")
	ErrNoVideoURL  = errors.New("pornhub: no video URL found")
	ErrParseFailed = errors.New("pornhub: page parse failed")
)

// NewPHError 创建 PHError
func NewPHError(code int, msg string) *PHError {
	return &PHError{Code: code, Message: msg}
}

// IsRateLimit 判断是否为限流错误
func IsRateLimit(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRateLimit) {
		return true
	}
	var phErr *PHError
	if errors.As(err, &phErr) {
		return phErr.Code == 429 || phErr.Code == 503
	}
	msg := err.Error()
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests")
}

// IsUnavailable 判断是否为内容不可用错误（删除/私有/地区限制）
func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUnavailable) {
		return true
	}
	var phErr *PHError
	if errors.As(err, &phErr) {
		return phErr.Code == 404 || phErr.Code == 403 || phErr.Code == 410
	}
	msg := err.Error()
	return strings.Contains(msg, "404") ||
		strings.Contains(msg, "410") ||
		strings.Contains(msg, "unavailable") ||
		strings.Contains(msg, "deleted") ||
		strings.Contains(msg, "private")
}
