package xchina

import "strings"

// ErrKind 错误类型分类
type ErrKind int

const (
	ErrKindTransient    ErrKind = iota // 临时错误，可重试
	ErrKindRateLimit                   // 限流 / 403
	ErrKindUnavailable                 // 视频不可用（已删除/私有）
	ErrKindParseFailed                 // 页面解析失败
)

// GetErrKind 根据错误消息判断错误类型
func GetErrKind(err error) ErrKind {
	if err == nil {
		return ErrKindTransient
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "403") || strings.Contains(msg, "410") || strings.Contains(msg, "token expired"):
		return ErrKindRateLimit
	case strings.Contains(msg, "404") || strings.Contains(msg, "unavailable") || strings.Contains(msg, "not found"):
		return ErrKindUnavailable
	case strings.Contains(msg, "parse") || strings.Contains(msg, "m3u8") || strings.Contains(msg, "no segment"):
		return ErrKindParseFailed
	default:
		return ErrKindTransient
	}
}

// IsRateLimit 判断是否为限流错误
func IsRateLimit(err error) bool {
	return GetErrKind(err) == ErrKindRateLimit
}
