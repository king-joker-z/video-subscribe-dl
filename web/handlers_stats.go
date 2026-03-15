package web

import (
	"fmt"
	"net/http"
	"strconv"

	"video-subscribe-dl/internal/logger"
	"video-subscribe-dl/internal/util"
)

// GET /api/stats - 统计信息（增强版）
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	result := map[string]interface{}{}

	// 基础统计
	if detailed, err := s.db.GetStatsDetailed(); err == nil {
		result["total"] = detailed.Total
		result["completed"] = detailed.Completed
		result["failed"] = detailed.Failed
		result["pending"] = detailed.Pending
		result["total_size"] = detailed.TotalSize
		result["sources"] = detailed.Sources
		if detailed.Total > 0 {
			result["success_rate"] = float64(detailed.Completed) / float64(detailed.Total) * 100
		} else {
			result["success_rate"] = 0.0
		}
	}

	// 按月统计
	if byMonth, err := s.db.GetStatsByMonth(); err == nil {
		result["by_month"] = byMonth
	}

	// 按 UP 主统计 top 10
	if byUploader, err := s.db.GetStatsByUploader(10); err == nil {
		result["by_uploader"] = byUploader
	}

	// 磁盘信息
	if diskInfo, err := util.GetDiskInfo(s.downloadDir); err == nil {
		result["disk_total"] = diskInfo.Total
		result["disk_used"] = diskInfo.Used
		result["disk_free"] = diskInfo.Free
		result["disk_available"] = diskInfo.Available
	}

	jsonResponse(w, result)
}

// GET /api/logs?limit=100&offset=0 — 返回最近日志
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	appLogger := logger.Default()
	if appLogger == nil {
		jsonResponse(w, []logger.LogEntry{})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	entries := appLogger.GetLogs(limit, offset)
	jsonResponse(w, entries)
}

// GET /api/logs/stream — SSE 实时推送新日志
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", 500)
		return
	}

	appLogger := logger.Default()
	if appLogger == nil {
		jsonError(w, "logger not initialized", 500)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if origin := getCORSOrigin(); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	// Send initial connected event
	fmt.Fprintf(w, "data: {\"type\":\"connected\"}\n\n")
	flusher.Flush()

	ch := appLogger.Subscribe()
	defer appLogger.Unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			data := logger.MarshalEntry(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
