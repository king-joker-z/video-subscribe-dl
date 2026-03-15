package bilibili

import (
	"fmt"
)

// BiliErrorKind B站 API 错误类型枚举
type BiliErrorKind int

const (
	// ErrKindInvalidResponse 无效响应（无法解析）
	ErrKindInvalidResponse BiliErrorKind = iota
	// ErrKindErrorResponse API 返回错误码
	ErrKindErrorResponse
	// ErrKindRiskControl 风控触发
	ErrKindRiskControl
	// ErrKindInvalidStatusCode HTTP 状态码异常
	ErrKindInvalidStatusCode
	// ErrKindVideoStreamsEmpty 视频流为空
	ErrKindVideoStreamsEmpty
)

// BiliError B站 API 统一错误类型
type BiliError struct {
	Kind    BiliErrorKind
	Code    int    // API 返回的 code（仅 ErrorResponse 时有意义）
	Message string // 错误描述
}

func (e *BiliError) Error() string {
	switch e.Kind {
	case ErrKindErrorResponse:
		return fmt.Sprintf("bilibili API error: code=%d, msg=%s", e.Code, e.Message)
	case ErrKindRiskControl:
		return fmt.Sprintf("bilibili risk control: %s", e.Message)
	case ErrKindInvalidStatusCode:
		return fmt.Sprintf("bilibili invalid HTTP status: %s", e.Message)
	case ErrKindVideoStreamsEmpty:
		return "bilibili: video streams empty"
	default:
		return fmt.Sprintf("bilibili error: %s", e.Message)
	}
}

// IsRiskControlRelated 判断是否为风控相关错误
// 风控信号：code -352, -412, HTTP 412, v_voucher
func (e *BiliError) IsRiskControlRelated() bool {
	if e.Kind == ErrKindRiskControl {
		return true
	}
	if e.Kind == ErrKindErrorResponse {
		switch e.Code {
		case -352, -412:
			return true
		}
	}
	if e.Kind == ErrKindInvalidStatusCode && e.Code == 412 {
		return true
	}
	return false
}

// NewRiskControlError 创建风控错误
func NewRiskControlError(msg string) *BiliError {
	return &BiliError{Kind: ErrKindRiskControl, Message: msg}
}

// NewErrorResponse 创建 API 错误响应
func NewErrorResponse(code int, msg string) *BiliError {
	return &BiliError{Kind: ErrKindErrorResponse, Code: code, Message: msg}
}

// NewInvalidStatusCode 创建 HTTP 状态码错误
func NewInvalidStatusCode(statusCode int, msg string) *BiliError {
	return &BiliError{Kind: ErrKindInvalidStatusCode, Code: statusCode, Message: msg}
}

// NewVideoStreamsEmpty 创建视频流为空错误
func NewVideoStreamsEmpty() *BiliError {
	return &BiliError{Kind: ErrKindVideoStreamsEmpty, Message: "video streams empty"}
}

// IsRiskControl 检查任意 error 是否为风控相关
func IsRiskControl(err error) bool {
	if err == nil {
		return false
	}
	if be, ok := err.(*BiliError); ok {
		return be.IsRiskControlRelated()
	}
	// 兼容旧的 ErrRateLimited
	return err == ErrRateLimited
}
