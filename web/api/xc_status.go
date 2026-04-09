package api

import (
	"net/http"
	"time"
)

// XCStatusHandler XChina 暂停状态 API
type XCStatusHandler struct {
	getStatus  func() (paused bool, reason string, pausedAt time.Time)
	resumeFunc func()
	pauseFunc  func(reason string)
}

func NewXCStatusHandler() *XCStatusHandler {
	return &XCStatusHandler{}
}

func (h *XCStatusHandler) SetStatusFunc(fn func() (bool, string, time.Time)) {
	h.getStatus = fn
}

func (h *XCStatusHandler) SetResumeFunc(fn func()) {
	h.resumeFunc = fn
}

func (h *XCStatusHandler) SetPauseFunc(fn func(reason string)) {
	h.pauseFunc = fn
}

// HandleStatus GET /api/xc/status — 返回 XChina 暂停状态
func (h *XCStatusHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
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

// HandleResume POST /api/xc/resume — 手动恢复 XChina 下载
func (h *XCStatusHandler) HandleResume(w http.ResponseWriter, r *http.Request) {
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
		"message": "XChina 下载已恢复",
	})
}

// HandlePause POST /api/xc/pause — 手动暂停 XChina 下载
func (h *XCStatusHandler) HandlePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	if h.pauseFunc == nil {
		apiError(w, CodeInternal, "pause function not configured")
		return
	}

	h.pauseFunc("手动暂停")
	apiOK(w, map[string]interface{}{
		"message": "XChina 下载已暂停",
	})
}
