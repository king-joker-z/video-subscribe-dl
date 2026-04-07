package api

import (
	"fmt"
	"net/http"
	"runtime"
	"time"

	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/douyin"
)

// MetricsHandler 运行时指标端点
type MetricsHandler struct {
	dl                    *downloader.Downloader
	startTime             time.Time
	getCooldownInfo       func() (bool, int)
	getPHCooldownInfo     func() (bool, int) // PH 冷却状态
	getSchedulerLastCheck map[string]func() time.Time
}

// NewMetricsHandler 创建 MetricsHandler
func NewMetricsHandler(dl *downloader.Downloader) *MetricsHandler {
	return &MetricsHandler{
		dl:                    dl,
		startTime:             time.Now(),
		getSchedulerLastCheck: make(map[string]func() time.Time),
	}
}

// SetSchedulerLastCheckFunc 注册某平台调度器的最近检查时间回调
func (h *MetricsHandler) SetSchedulerLastCheckFunc(platform string, fn func() time.Time) {
	h.getSchedulerLastCheck[platform] = fn
}

// SetStartTime 设置服务启动时间
func (h *MetricsHandler) SetStartTime(t time.Time) {
	h.startTime = t
}

// SetCooldownInfoFunc 设置风控冷却信息回调（B站）
func (h *MetricsHandler) SetCooldownInfoFunc(fn func() (bool, int)) {
	h.getCooldownInfo = fn
}

// SetPHCooldownInfoFunc 设置 PH 冷却信息回调
func (h *MetricsHandler) SetPHCooldownInfoFunc(fn func() (bool, int)) {
	h.getPHCooldownInfo = fn
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
	cooldown := map[string]bool{"bili": false, "douyin": false, "ph": false}
	if h.getCooldownInfo != nil {
		inCooldown, _ := h.getCooldownInfo()
		if inCooldown {
			cooldown["bili"] = true
		}
	}
	if h.getPHCooldownInfo != nil {
		inCooldown, _ := h.getPHCooldownInfo()
		if inCooldown {
			cooldown["ph"] = true
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

// HandlePrometheus GET /api/metrics/prometheus — Prometheus text format
func (h *MetricsHandler) HandlePrometheus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	dlStats := h.dl.Stats()
	uptimeSeconds := int(time.Since(h.startTime).Seconds())

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP vsd_goroutines Current number of goroutines\n")
	fmt.Fprintf(w, "# TYPE vsd_goroutines gauge\n")
	fmt.Fprintf(w, "vsd_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(w, "# HELP vsd_memory_bytes Allocated heap memory in bytes\n")
	fmt.Fprintf(w, "# TYPE vsd_memory_bytes gauge\n")
	fmt.Fprintf(w, "vsd_memory_bytes %d\n", memStats.Alloc)
	fmt.Fprintf(w, "# HELP vsd_memory_sys_bytes Total memory obtained from OS in bytes\n")
	fmt.Fprintf(w, "# TYPE vsd_memory_sys_bytes gauge\n")
	fmt.Fprintf(w, "vsd_memory_sys_bytes %d\n", memStats.Sys)
	fmt.Fprintf(w, "# HELP vsd_gc_cycles_total Total number of completed GC cycles\n")
	fmt.Fprintf(w, "# TYPE vsd_gc_cycles_total counter\n")
	fmt.Fprintf(w, "vsd_gc_cycles_total %d\n", memStats.NumGC)
	fmt.Fprintf(w, "# HELP vsd_uptime_seconds Uptime in seconds\n")
	fmt.Fprintf(w, "# TYPE vsd_uptime_seconds gauge\n")
	fmt.Fprintf(w, "vsd_uptime_seconds %d\n", uptimeSeconds)
	fmt.Fprintf(w, "# HELP vsd_downloader_active Currently active downloads\n")
	fmt.Fprintf(w, "# TYPE vsd_downloader_active gauge\n")
	fmt.Fprintf(w, "vsd_downloader_active %d\n", dlStats.Active)
	fmt.Fprintf(w, "# HELP vsd_downloader_queued Queued downloads\n")
	fmt.Fprintf(w, "# TYPE vsd_downloader_queued gauge\n")
	fmt.Fprintf(w, "vsd_downloader_queued %d\n", dlStats.Queued)
	fmt.Fprintf(w, "# HELP vsd_downloader_completed_total Total completed downloads\n")
	fmt.Fprintf(w, "# TYPE vsd_downloader_completed_total counter\n")
	fmt.Fprintf(w, "vsd_downloader_completed_total %d\n", dlStats.Completed)
	fmt.Fprintf(w, "# HELP vsd_downloader_failed_total Total failed downloads\n")
	fmt.Fprintf(w, "# TYPE vsd_downloader_failed_total counter\n")
	fmt.Fprintf(w, "vsd_downloader_failed_total %d\n", dlStats.Failed)

	// per-platform download counters (alphabetical order)
	platforms := []string{"bilibili", "douyin", "pornhub"}
	fmt.Fprintf(w, "# HELP vsd_downloads_completed_total Total completed downloads per platform\n")
	fmt.Fprintf(w, "# TYPE vsd_downloads_completed_total counter\n")
	for _, p := range platforms {
		fmt.Fprintf(w, "vsd_downloads_completed_total{platform=%q} %d\n", p, dlStats.PlatformCompleted[p])
	}
	fmt.Fprintf(w, "# HELP vsd_downloads_failed_total Total failed downloads per platform\n")
	fmt.Fprintf(w, "# TYPE vsd_downloads_failed_total counter\n")
	for _, p := range platforms {
		fmt.Fprintf(w, "vsd_downloads_failed_total{platform=%q} %d\n", p, dlStats.PlatformFailed[p])
	}

	// scheduler last-check timestamps per platform
	fmt.Fprintf(w, "# HELP vsd_scheduler_last_check_timestamp Unix timestamp of the last scheduler check per platform\n")
	fmt.Fprintf(w, "# TYPE vsd_scheduler_last_check_timestamp gauge\n")
	for _, p := range platforms {
		ts := int64(0)
		if fn := h.getSchedulerLastCheck[p]; fn != nil {
			if t := fn(); !t.IsZero() {
				ts = t.Unix()
			}
		}
		fmt.Fprintf(w, "vsd_scheduler_last_check_timestamp{platform=%q} %d\n", p, ts)
	}
}
