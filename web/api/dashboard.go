package api

import (
	"log"
	"net/http"
	"sync"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/util"
)

// DashboardHandler 仪表盘 API
type DashboardHandler struct {
	db              *db.DB
	downloader      *downloader.Downloader
	downloadDir     string
	getCooldownInfo func() (bool, int) // 返回 (inCooldown, remainingSec)

	// [FIXED: P1-2] credential 缓存需要用锁保护，避免并发 HTTP 请求的 data race
	credCacheMu   sync.RWMutex
	credCache     map[string]interface{}
	credCacheTime time.Time
	credCacheTTL  time.Duration
}

func NewDashboardHandler(database *db.DB, dl *downloader.Downloader, downloadDir string) *DashboardHandler {
	return &DashboardHandler{
		db:           database,
		downloader:   dl,
		downloadDir:  downloadDir,
		credCacheTTL: 5 * time.Minute,
	}
}

func (h *DashboardHandler) SetCooldownInfoFunc(fn func() (bool, int)) {
	h.getCooldownInfo = fn
}

// getCredentialStatus 获取凭证状态（带缓存，5分钟TTL）
func (h *DashboardHandler) getCredentialStatus() map[string]interface{} {
	// [FIXED: P1-2] 先用 RLock 快速检查缓存
	h.credCacheMu.RLock()
	if h.credCache != nil && time.Since(h.credCacheTime) < h.credCacheTTL {
		cached := h.credCache
		h.credCacheMu.RUnlock()
		return cached
	}
	h.credCacheMu.RUnlock()

	// 缓存过期或为空，获取写锁更新
	h.credCacheMu.Lock()
	defer h.credCacheMu.Unlock()

	// double-check: 另一个 goroutine 可能已更新
	if h.credCache != nil && time.Since(h.credCacheTime) < h.credCacheTTL {
		return h.credCache
	}

	result := map[string]interface{}{
		"has_credential": false,
		"source":         "none",
		"status":         "none",
		"status_label":   "未登录",
	}

	credJSON, _ := h.db.GetSetting("credential_json")
	credSource, _ := h.db.GetSetting("credential_source")
	cred := bilibili.CredentialFromJSON(credJSON)

	if cred == nil || cred.IsEmpty() {
		h.credCache = result
		h.credCacheTime = time.Now()
		return result
	}

	result["has_credential"] = true
	result["source"] = credSource

	if cred.UpdatedAt > 0 {
		result["updated_at"] = time.Unix(cred.UpdatedAt, 0).Format("2006-01-02 15:04:05")
	}

	// 验证用户信息
	httpClient := sharedAPIClient10s
	verifyResult, err := bilibili.VerifyCredential(cred, httpClient)
	if err != nil {
		log.Printf("[dashboard] credential verify error: %v", err)
		result["status"] = "error"
		result["status_label"] = "验证失败"
	} else if verifyResult != nil {
		if !verifyResult.LoggedIn {
			result["status"] = "expired"
			result["status_label"] = "已过期"
			result["need_refresh"] = true
		} else {
			result["status"] = "ok"
			result["status_label"] = "正常"
			result["username"] = verifyResult.Username
			result["vip_label"] = verifyResult.VIPLabel
			result["max_quality"] = verifyResult.MaxQuality
			result["vip_active"] = verifyResult.VIPActive
		}
	}

	h.credCache = result
	h.credCacheTime = time.Now()
	return result
}

// InvalidateCredentialCache 使凭证缓存失效（登录/刷新后调用）
func (h *DashboardHandler) InvalidateCredentialCache() {
	h.credCacheMu.Lock()
	h.credCache = nil
	h.credCacheMu.Unlock()
}

// GET /api/dashboard
func (h *DashboardHandler) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	result := map[string]interface{}{}

	// 基础统计
	if detailed, err := h.db.GetStatsDetailed(); err == nil {
		result["sources"] = detailed.Sources
		result["total_videos"] = detailed.Total
		result["completed"] = detailed.Completed
		result["failed"] = detailed.Failed
		result["pending"] = detailed.Pending
		result["total_size"] = detailed.TotalSize
		result["charge_blocked"] = detailed.ChargeBlocked
		if detailed.Total > 0 {
			result["success_rate"] = float64(detailed.Completed) / float64(detailed.Total) * 100
		} else {
			result["success_rate"] = 0.0
		}
	}

	// 下载中数量
	stats, _ := h.db.GetStats()
	result["downloading"] = stats["downloading"]

	// 队列状态
	result["queue_paused"] = false
	if h.downloader != nil {
		result["queue_paused"] = h.downloader.IsPaused()
		progress := h.downloader.GetProgress()
		result["active_downloads"] = len(progress)
	}

	// 磁盘信息
	if diskInfo, err := util.GetDiskInfo(h.downloadDir); err == nil {
		result["disk"] = map[string]interface{}{
			"total":     diskInfo.Total,
			"used":      diskInfo.Used,
			"free":      diskInfo.Free,
			"available": diskInfo.Available,
		}
	}

	// 最近下载（前10条，去掉图片URL减少前端请求）
	if recent, err := h.db.GetDownloads(10); err == nil {
		for i := range recent {
			recent[i].Thumbnail = ""
			recent[i].ThumbPath = ""
		}
		result["recent_downloads"] = recent
	}

	// 最近 24 小时下载数
	if count24h, err := h.db.GetStats24h(); err == nil {
		result["downloads_24h"] = count24h
	}

	// 风控冷却状态
	if h.getCooldownInfo != nil {
		inCooldown, remainingSec := h.getCooldownInfo()
		result["cooldown"] = map[string]interface{}{
			"active":        inCooldown,
			"remaining_sec": remainingSec,
		}
	}

	// 按月统计
	if byMonth, err := h.db.GetStatsByMonth(); err == nil {
		result["by_month"] = byMonth
	}

	// 凭证状态（带缓存）
	result["credential"] = h.getCredentialStatus()

	apiOK(w, result)
}
