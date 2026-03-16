package api

import (
	"net/http"
	"runtime"
	"time"

	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/douyin"
)

// MetricsHandler 运行时指标端点
type MetricsHandler struct {
	dl              *downloader.Downloader
	startTime       time.Time
	getCooldownInfo func() (bool, int)
}

// NewMetricsHandler 创建 MetricsHandler
func NewMetricsHandler(dl *downloader.Downloader) *MetricsHandler {
	return &MetricsHandler{
		dl:        dl,
		startTime: time.Now(),
	}
}

// SetStartTime 设置服务启动时间
func (h *MetricsHandler) SetStartTime(t time.Time) {
	h.startTime = t
}

// SetCooldownInfoFunc 设置风控冷却信息回调
func (h *MetricsHandler) SetCooldownInfoFunc(fn func() (bool, int)) {
	h.getCooldownInfo = fn
}

// HandleMetrics GET /api/metrics — 返回运行时指标 JSON
func (h *MetricsHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// 签名池统计
	signPoolData := make(map[string]interface{})
	if xbogus := douyin.GetSignPoolStats(); xbogus != nil {
		signPoolData["xbogus"] = xbogus
	}
	if abogus := douyin.GetABogusPoolStats(); abogus != nil {
		signPoolData["abogus"] = abogus
	}

	// 下载器统计
	dlStats := h.dl.Stats()

	// 风控冷却
	cooldown := map[string]bool{"bili": false, "douyin": false}
	if h.getCooldownInfo != nil {
		inCooldown, _ := h.getCooldownInfo()
		if inCooldown {
			cooldown["bili"] = true
		}
	}

	result := map[string]interface{}{
		"goroutines":     runtime.NumGoroutine(),
		"memory_mb":      float64(memStats.Alloc) / 1024 / 1024,
		"memory_sys_mb":  float64(memStats.Sys) / 1024 / 1024,
		"gc_cycles":      memStats.NumGC,
		"sign_pool":      signPoolData,
		"downloader":     dlStats,
		"cooldown":       cooldown,
		"uptime_seconds": int(time.Since(h.startTime).Seconds()),
	}

	apiOK(w, result)
}
