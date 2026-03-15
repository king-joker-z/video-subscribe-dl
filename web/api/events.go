package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/logger"
)

// EventsHandler SSE 实时推送
type EventsHandler struct {
	downloader *downloader.Downloader
}

func NewEventsHandler(dl *downloader.Downloader) *EventsHandler {
	return &EventsHandler{downloader: dl}
}

// GET /api/events — 统一 SSE 端点（下载进度 + 日志）
func (h *EventsHandler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, CodeInternal, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if origin := os.Getenv("CORS_ORIGIN"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	// 连接建立事件
	fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	// 订阅日志
	appLogger := logger.Default()
	var logCh chan logger.LogEntry
	if appLogger != nil {
		logCh = appLogger.Subscribe()
		defer appLogger.Unsubscribe(logCh)
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			// 发送下载进度
			if h.downloader != nil {
				progress := h.downloader.GetProgress()
				data, _ := json.Marshal(progress)
				fmt.Fprintf(w, "event: progress\ndata: %s\n\n", data)
				flusher.Flush()
			}

		case entry, ok := <-logCh:
			if !ok {
				return
			}
			data := logger.MarshalEntry(entry)
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// GET /api/logs — 历史日志
func (h *EventsHandler) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	appLogger := logger.Default()
	if appLogger == nil {
		apiOK(w, []logger.LogEntry{})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	entries := appLogger.GetLogs(limit, offset)
	apiOK(w, entries)
}
