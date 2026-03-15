package api

import (
	"net/http"

	"video-subscribe-dl/internal/notify"
)

// NotifyHandler 通知 API
type NotifyHandler struct {
	notifier *notify.Notifier
}

func NewNotifyHandler(notifier *notify.Notifier) *NotifyHandler {
	return &NotifyHandler{notifier: notifier}
}

// POST /api/notify/test — 发送测试通知
func (h *NotifyHandler) HandleTest(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	if h.notifier == nil {
		apiError(w, CodeInternal, "通知模块未初始化")
		return
	}

	configured, notifyType := h.notifier.IsConfigured()
	if !configured {
		apiError(w, CodeInvalidParam, "未配置通知通道，请先选择通知类型并填写配置")
		return
	}

	if err := h.notifier.SendTest(); err != nil {
		apiError(w, CodeInternal, "发送测试通知失败: "+err.Error())
		return
	}

	apiOK(w, map[string]interface{}{
		"message": "测试通知已发送",
		"type":    string(notifyType),
	})
}

// GET /api/notify/status — 通知配置状态
func (h *NotifyHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	if h.notifier == nil {
		apiOK(w, map[string]interface{}{
			"configured": false,
			"type":       "",
		})
		return
	}

	apiOK(w, h.notifier.GetStatusInfo())
}
