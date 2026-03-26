package pornhub

import (
	"errors"
	"fmt"
	"strings"
)

// ErrKind 错误分类
type ErrKind int

const (
	ErrKindTransient   ErrKind = iota // 临时错误，可重试
	ErrKindUnavailable                // 内容真不可用，直接 skip
	ErrKindParseFailed                // 结构解析失败，标 failed 留人工
	ErrKindRateLimit                  // 限流，触发 cooldown
)

// PHError Pornhub 平台专属错误
type PHError struct {
	Kind    ErrKind
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

// NewPHError 创建 PHError（显式指定 Kind）
func NewPHError(kind ErrKind, code int, msg string) *PHError {
	return &PHError{Kind: kind, Code: code, Message: msg}
}

// NewPHErrorAuto 创建 PHError，自动根据 HTTP 状态码推断 Kind
// 429/503 → RateLimit，404/403/410 → Unavailable，其他 → Transient
func NewPHErrorAuto(code int, msg string) *PHError {
	var kind ErrKind
	switch code {
	case 429, 503:
		kind = ErrKindRateLimit
	case 404, 403, 410:
		kind = ErrKindUnavailable
	default:
		kind = ErrKindTransient
	}
	return &PHError{Kind: kind, Code: code, Message: msg}
}

// GetErrKind 从 error 中提取 ErrKind
// 优先从 PHError.Kind 读，fallback 到原有 IsRateLimit/IsUnavailable 逻辑
func GetErrKind(err error) ErrKind {
	if err == nil {
		return ErrKindTransient
	}
	var phErr *PHError
	if errors.As(err, &phErr) {
		return phErr.Kind
	}
	if IsRateLimit(err) {
		return ErrKindRateLimit
	}
	if IsUnavailable(err) {
		return ErrKindUnavailable
	}
	if errors.Is(err, ErrParseFailed) {
		return ErrKindParseFailed
	}
	return ErrKindTransient
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
