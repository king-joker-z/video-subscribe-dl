package douyin

import (
	"errors"
	"fmt"
	"strings"
)

// DouyinErrKind 错误分类
type DouyinErrKind int

const (
	DouyinErrKindTransient   DouyinErrKind = iota // 临时错误，可重试
	DouyinErrKindUnavailable                       // 内容不可用（下架/私有/地区限制）
	DouyinErrKindRiskControl                       // 风控触发（验证码/IP封锁/频率限制）
	DouyinErrKindParseFailed                       // 解析失败（结构变更/新格式）
)

// DouyinError 抖音平台专属错误
type DouyinError struct {
	Kind    DouyinErrKind
	Code    int
	Message string
}

func (e *DouyinError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("douyin error (HTTP %d): %s", e.Code, e.Message)
	}
	return fmt.Sprintf("douyin error: %s", e.Message)
}

// 哨兵错误（保持向后兼容，ErrDouyinRiskControl 原在 client.go 中定义）
var (
	ErrDouyinRiskControl = errors.New("douyin risk control detected")
	ErrDouyinUnavailable = errors.New("douyin: content unavailable")
	ErrDouyinParseFailed = errors.New("douyin: parse failed")
)

// NewDouyinError 创建带分类的抖音错误
func NewDouyinError(kind DouyinErrKind, code int, msg string) *DouyinError {
	return &DouyinError{Kind: kind, Code: code, Message: msg}
}

// GetDouyinErrKind 从 error 中提取 DouyinErrKind
// 优先从 DouyinError.Kind 读，fallback 到哨兵错误判断
func GetDouyinErrKind(err error) DouyinErrKind {
	if err == nil {
		return DouyinErrKindTransient
	}
	var de *DouyinError
	if errors.As(err, &de) {
		return de.Kind
	}
	if errors.Is(err, ErrDouyinRiskControl) {
		return DouyinErrKindRiskControl
	}
	if errors.Is(err, ErrDouyinUnavailable) {
		return DouyinErrKindUnavailable
	}
	if errors.Is(err, ErrDouyinParseFailed) {
		return DouyinErrKindParseFailed
	}
	msg := err.Error()
	if strings.Contains(msg, "risk control") || strings.Contains(msg, "风控") {
		return DouyinErrKindRiskControl
	}
	if strings.Contains(msg, "unavailable") || strings.Contains(msg, "下架") || strings.Contains(msg, "404") {
		return DouyinErrKindUnavailable
	}
	return DouyinErrKindTransient
}
