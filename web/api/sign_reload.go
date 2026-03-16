package api

import (
	"net/http"

	"video-subscribe-dl/internal/douyin"
)

// SignReloadHandler 签名算法热更新端点
type SignReloadHandler struct{}

// HandleReload POST /api/sign/reload — 手动触发签名脚本重新加载
func (h *SignReloadHandler) HandleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	updater := douyin.GetSignUpdater()
	if err := updater.ReloadFromRemote(); err != nil {
		apiError(w, CodeInternal, "reload failed: "+err.Error())
		return
	}

	apiOK(w, map[string]interface{}{
		"message": "sign scripts reload triggered",
	})
}
