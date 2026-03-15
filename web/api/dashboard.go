package api

import (
	"net/http"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/util"
)

// DashboardHandler 仪表盘 API
type DashboardHandler struct {
	db          *db.DB
	downloader  *downloader.Downloader
	downloadDir string
}

func NewDashboardHandler(database *db.DB, dl *downloader.Downloader, downloadDir string) *DashboardHandler {
	return &DashboardHandler{db: database, downloader: dl, downloadDir: downloadDir}
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

	// 按月统计
	if byMonth, err := h.db.GetStatsByMonth(); err == nil {
		result["by_month"] = byMonth
	}

	apiOK(w, result)
}
