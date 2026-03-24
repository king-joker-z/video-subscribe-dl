package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"video-subscribe-dl/internal/db"
)

// PHCookieHandler Pornhub Cookie 管理 API
type PHCookieHandler struct {
	db              *db.DB
	onCookieRefresh func(string)
}

func NewPHCookieHandler(database *db.DB) *PHCookieHandler {
	return &PHCookieHandler{db: database}
}

// SetCookieRefreshFunc 设置 Cookie 热更新回调（已废弃，使用 SetCookieUpdateFunc）
func (h *PHCookieHandler) SetCookieRefreshFunc(fn func(string)) {
	h.onCookieRefresh = fn
}

// SetCookieUpdateFunc 设置 Cookie 热更新回调
func (h *PHCookieHandler) SetCookieUpdateFunc(fn func(string)) {
	h.onCookieRefresh = fn
}

// HandleSave POST /api/ph/cookie — 保存 PH Cookie（公开方法，供 router 调用）
func (h *PHCookieHandler) HandleSave(w http.ResponseWriter, r *http.Request) {
	h.handleSetCookie(w, r)
}

// HandleDelete DELETE /api/ph/cookie — 删除 PH Cookie（公开方法，供 router 调用）
func (h *PHCookieHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	h.handleDeleteCookie(w, r)
}

type phSetCookieReq struct {
	Cookie string `json:"cookie"`
}

// handleSetCookie 保存用户提供的 PH Cookie
func (h *PHCookieHandler) handleSetCookie(w http.ResponseWriter, r *http.Request) {
	var req phSetCookieReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, CodeInvalidParam, "请求解析失败: "+err.Error())
		return
	}

	// 清洗 Cookie
	cookie := req.Cookie
	cookie = strings.ReplaceAll(cookie, "\r\n", "")
	cookie = strings.ReplaceAll(cookie, "\r", "")
	cookie = strings.ReplaceAll(cookie, "\n", "")
	cookie = strings.TrimSpace(cookie)

	if cookie == "" {
		apiError(w, CodeInvalidParam, "Cookie 不能为空")
		return
	}

	// 保存到 DB
	if err := h.db.SetSetting("ph_cookie", cookie); err != nil {
		apiError(w, CodeInternal, "保存 Cookie 失败: "+err.Error())
		return
	}

	// 热更新
	if h.onCookieRefresh != nil {
		h.onCookieRefresh(cookie)
	}

	log.Printf("[ph-cookie] Cookie 已保存并热更新（长度: %d）", len(cookie))

	apiOK(w, map[string]interface{}{
		"message": "PH Cookie 已保存",
	})
}

// handleDeleteCookie 删除 PH Cookie
func (h *PHCookieHandler) handleDeleteCookie(w http.ResponseWriter, r *http.Request) {
	if err := h.db.SetSetting("ph_cookie", ""); err != nil {
		apiError(w, CodeInternal, "删除 Cookie 失败: "+err.Error())
		return
	}

	// 热更新（传空字符串）
	if h.onCookieRefresh != nil {
		h.onCookieRefresh("")
	}

	log.Printf("[ph-cookie] Cookie 已删除")

	apiOK(w, map[string]interface{}{
		"message": "PH Cookie 已删除",
	})
}


