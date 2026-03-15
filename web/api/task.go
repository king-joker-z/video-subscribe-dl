package api

import (
	"log"
	"net/http"
	"runtime"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
)

// TaskHandler 任务控制 API
type TaskHandler struct {
	db          *db.DB
	downloader  *downloader.Downloader
	startTime   time.Time
	version     string
	buildTime   string
	onCheckNow  func()
}

func NewTaskHandler(database *db.DB, dl *downloader.Downloader) *TaskHandler {
	return &TaskHandler{db: database, downloader: dl}
}

func (h *TaskHandler) SetCheckNowFunc(fn func())       { h.onCheckNow = fn }
func (h *TaskHandler) SetVersion(v string)              { h.version = v }
func (h *TaskHandler) SetBuildTime(t string)            { h.buildTime = t }
func (h *TaskHandler) SetStartTime(t time.Time)         { h.startTime = t }

// GET /api/task/status — 任务状态
func (h *TaskHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	status := "idle"
	activeCount := 0
	queueLen := 0

	if h.downloader != nil {
		if h.downloader.IsPaused() {
			status = "paused"
		} else if h.downloader.IsBusy() {
			status = "running"
		}
		activeCount = h.downloader.ActiveCount()
		queueLen = h.downloader.QueueLen()
	}

	apiOK(w, map[string]interface{}{
		"status":           status,
		"active_downloads": activeCount,
		"queue_length":     queueLen,
		"uptime":           time.Since(h.startTime).String(),
		"uptime_seconds":   int(time.Since(h.startTime).Seconds()),
	})
}

// POST /api/task/trigger — 手动触发
func (h *TaskHandler) HandleTrigger(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}
	log.Println("[task] Manual check triggered via API")
	if h.onCheckNow != nil {
		go h.onCheckNow()
	}
	apiOK(w, map[string]interface{}{"message": "已触发"})
}

// POST /api/task/pause — 暂停
func (h *TaskHandler) HandlePause(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}
	if h.downloader != nil {
		h.downloader.Pause()
	}
	apiOK(w, map[string]interface{}{"message": "已暂停"})
}

// POST /api/task/resume — 恢复
func (h *TaskHandler) HandleResume(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}
	if h.downloader != nil {
		h.downloader.Resume()
	}
	apiOK(w, map[string]interface{}{"message": "已恢复"})
}

// GET /api/version — 版本信息
func (h *TaskHandler) HandleVersion(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}
	apiOK(w, map[string]interface{}{
		"version":    h.version,
		"build_time": h.buildTime,
		"go":         runtime.Version(),
		"uptime":     time.Since(h.startTime).String(),
	})
}
