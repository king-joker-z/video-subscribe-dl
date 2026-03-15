package api

import (
	"net/http"

	"video-subscribe-dl/internal/db"
)

// SettingsHandler 设置 API
type SettingsHandler struct {
	db              *db.DB
	onRefreshRate   func()
}

func NewSettingsHandler(database *db.DB) *SettingsHandler {
	return &SettingsHandler{db: database}
}

func (h *SettingsHandler) SetRefreshRateFunc(fn func()) {
	h.onRefreshRate = fn
}

// 公开的设置 key 列表
var settingsKeys = []string{
	"download_quality", "max_concurrent", "request_interval", "cookie_path",
	"nfo_type", "download_danmaku", "check_interval_minutes",
	"notify_type", "webhook_url", "telegram_bot_token", "telegram_chat_id",
	"bark_server", "bark_key",
	"notify_on_complete", "notify_on_error", "notify_on_cookie_expire", "notify_on_sync",
	"download_chunks", "max_download_speed_mb", "min_disk_free_gb",
	"rate_limit_per_minute", "retention_days", "auto_cleanup_on_low_disk",
	"auth_token",
}

// 敏感字段，不返回明文
var sensitiveKeys = map[string]bool{
	"auth_token":         true,
	"telegram_bot_token": true,
	"bark_key":           true,
}

// GET /api/settings
func (h *SettingsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	settings := map[string]string{}
	for _, key := range settingsKeys {
		val, _ := h.db.GetSetting(key)
		if sensitiveKeys[key] && val != "" {
			settings[key] = "***"
		} else {
			settings[key] = val
		}
	}
	apiOK(w, settings)
}

// PUT /api/settings
func (h *SettingsHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" && r.Method != "POST" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	var settings map[string]string
	if err := parseJSON(r, &settings); err != nil {
		apiError(w, CodeInvalidParam, "请求参数错误")
		return
	}

	for key, value := range settings {
		if err := h.db.SetSetting(key, value); err != nil {
			apiError(w, CodeInternal, "保存设置失败: "+err.Error())
			return
		}
	}

	// 刷新 rate limit 缓存
	if _, ok := settings["rate_limit_per_minute"]; ok && h.onRefreshRate != nil {
		h.onRefreshRate()
	}

	apiOK(w, map[string]interface{}{"message": "设置已更新"})
}

// POST /api/login/token — Token 认证
func (h *SettingsHandler) HandleTokenLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	if err := parseJSON(r, &req); err != nil {
		apiError(w, CodeInvalidParam, "参数错误")
		return
	}

	stored, _ := h.db.GetSetting("auth_token")
	if stored == "" || req.Token != stored {
		apiError(w, CodeUnauthorized, "Token 无效")
		return
	}

	// 设置 cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    req.Token,
		Path:     "/",
		MaxAge:   86400 * 30, // 30 天
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	apiOK(w, map[string]string{"message": "登录成功"})
}
