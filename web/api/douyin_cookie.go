package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
)

// DouyinCookieHandler 抖音 Cookie 管理 API
// 独立于 B 站凭证系统，不修改任何 B 站相关代码
type DouyinCookieHandler struct {
	db *db.DB
}

func NewDouyinCookieHandler(database *db.DB) *DouyinCookieHandler {
	return &DouyinCookieHandler{db: database}
}

type validateCookieReq struct {
	Cookie string `json:"cookie"`
}

// POST /api/douyin/cookie/validate — 验证用户填写的抖音 Cookie 是否有效
func (h *DouyinCookieHandler) HandleValidate(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	var req validateCookieReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, CodeInvalidParam, "请求解析失败: "+err.Error())
		return
	}

	cookie := strings.TrimSpace(req.Cookie)
	if cookie == "" {
		apiError(w, CodeInvalidParam, "Cookie 不能为空")
		return
	}

	// 基础格式检查
	required := []string{"msToken", "ttwid"}
	for _, field := range required {
		if !strings.Contains(cookie, field+"=") {
			apiOK(w, map[string]interface{}{
				"valid":   false,
				"message": fmt.Sprintf("缺少必要字段: %s", field),
			})
			return
		}
	}

	// 临时设置全局 Cookie 做验证，验证完毕后还原
	// 保存当前 Cookie，验证完恢复
	prevCookie := douyin.GetGlobalUserCookie()
	douyin.SetGlobalUserCookie(cookie)

	client := douyin.NewClient()
	valid, message := client.ValidateCookie()
	client.Close()

	// 还原之前的 Cookie
	douyin.SetGlobalUserCookie(prevCookie)

	if !valid {
		log.Printf("[douyin-cookie] Validate failed: %s", message)
	} else {
		log.Printf("[douyin-cookie] Validate success: %s", message)
	}

	apiOK(w, map[string]interface{}{
		"valid":   valid,
		"message": message,
	})
}

// GET /api/douyin/cookie/status — 查询当前抖音 Cookie 状态
func (h *DouyinCookieHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	savedCookie, _ := h.db.GetSetting("douyin_cookie")
	hasUserCookie := strings.TrimSpace(savedCookie) != ""
	activeCookie := douyin.GetGlobalUserCookie()

	mode := "auto"
	if activeCookie != "" {
		mode = "user"
	}

	apiOK(w, map[string]interface{}{
		"has_user_cookie": hasUserCookie,
		"is_active":       activeCookie != "",
		"mode":            mode,
	})
}
