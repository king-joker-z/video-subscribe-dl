package api

import (
	"net/http"
	"time"
)

// DouyinStatusHandler 抖音暂停状态 API
type DouyinStatusHandler struct {
	getStatus  func() (paused bool, reason string, pausedAt time.Time)
	resumeFunc func()
}

func NewDouyinStatusHandler() *DouyinStatusHandler {
	return &DouyinStatusHandler{}
}

func (h *DouyinStatusHandler) SetStatusFunc(fn func() (bool, string, time.Time)) {
	h.getStatus = fn
}

func (h *DouyinStatusHandler) SetResumeFunc(fn func()) {
	h.resumeFunc = fn
}

// HandleStatus GET /api/douyin/status — 返回抖音暂停状态
func (h *DouyinStatusHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	if h.getStatus == nil {
		apiOK(w, map[string]interface{}{
			"paused": false,
		})
		return
	}

	paused, reason, pausedAt := h.getStatus()
	resp := map[string]interface{}{
		"paused": paused,
	}
	if paused {
		resp["reason"] = reason
		resp["paused_at"] = pausedAt.Format(time.RFC3339)
		resp["paused_duration"] = time.Since(pausedAt).Round(time.Second).String()
	}
	apiOK(w, resp)
}

// HandleResume POST /api/douyin/resume — 手动恢复抖音下载
func (h *DouyinStatusHandler) HandleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	if h.resumeFunc == nil {
		apiError(w, CodeInternal, "resume function not configured")
		return
	}

	h.resumeFunc()
	apiOK(w, map[string]interface{}{
		"message": "抖音下载已恢复",
	})
}
