package api

import (
	"net/http"
	"time"
)

// PHStatusHandler Pornhub 暂停状态 API
type PHStatusHandler struct {
	getStatus       func() (paused bool, reason string, pausedAt time.Time)
	resumeFunc      func()
	pauseFunc       func(reason string)
	getCookieStatus func() (bool, string)
}

func NewPHStatusHandler() *PHStatusHandler {
	return &PHStatusHandler{}
}

func (h *PHStatusHandler) SetStatusFunc(fn func() (bool, string, time.Time)) {
	h.getStatus = fn
}

func (h *PHStatusHandler) SetResumeFunc(fn func()) {
	h.resumeFunc = fn
}

func (h *PHStatusHandler) SetPauseFunc(fn func(reason string)) {
	h.pauseFunc = fn
}

// SetCookieStatusFunc 注入 Cookie 状态查询函数
func (h *PHStatusHandler) SetCookieStatusFunc(fn func() (bool, string)) {
	h.getCookieStatus = fn
}

// HandleStatus GET /api/ph/status — 返回 PH 暂停状态 + Cookie 有效性
func (h *PHStatusHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	var cookieValid bool
	var cookieMsg string
	if h.getCookieStatus != nil {
		cookieValid, cookieMsg = h.getCookieStatus()
	} else {
		cookieValid = true
	}

	if h.getStatus == nil {
		apiOK(w, map[string]interface{}{
			"paused":       false,
			"cookie_valid": cookieValid,
			"cookie_msg":   cookieMsg,
		})
		return
	}

	paused, reason, pausedAt := h.getStatus()
	resp := map[string]interface{}{
		"paused":       paused,
		"cookie_valid": cookieValid,
		"cookie_msg":   cookieMsg,
	}
	if paused {
		resp["reason"] = reason
		resp["paused_at"] = pausedAt.Format(time.RFC3339)
		resp["paused_duration"] = time.Since(pausedAt).Round(time.Second).String()
	}
	apiOK(w, resp)
}

// HandleResume POST /api/ph/resume — 手动恢复 PH 下载
func (h *PHStatusHandler) HandleResume(w http.ResponseWriter, r *http.Request) {
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
		"message": "Pornhub 下载已恢复",
	})
}

// HandlePause POST /api/ph/pause — 手动暂停 PH 下载
func (h *PHStatusHandler) HandlePause(w http.ResponseWriter, r *http.Request) {
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
		"message": "Pornhub 下载已暂停",
	})
}
