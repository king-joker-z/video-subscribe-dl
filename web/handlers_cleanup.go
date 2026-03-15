package web

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// GET /api/cleanup/stats — cleanup statistics
func (s *Server) handleCleanupStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	totalCleaned, freedBytes, err := s.db.GetCleanupStats()
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}

	retentionDays, _ := s.db.GetSetting("retention_days")
	autoCleanup, _ := s.db.GetSetting("auto_cleanup_on_low_disk")

	jsonResponse(w, map[string]interface{}{
		"total_cleaned":          totalCleaned,
		"total_freed_bytes":      freedBytes,
		"retention_days":         retentionDays,
		"auto_cleanup_on_low_disk": autoCleanup,
	})
}

// PUT /api/cleanup/config — update cleanup configuration
func (s *Server) handleCleanupConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" && r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}

	var body struct {
		RetentionDays        *int  `json:"retention_days"`
		AutoCleanupOnLowDisk *bool `json:"auto_cleanup_on_low_disk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}

	if body.RetentionDays != nil {
		s.db.SetSetting("retention_days", strconv.Itoa(*body.RetentionDays))
	}
	if body.AutoCleanupOnLowDisk != nil {
		val := "false"
		if *body.AutoCleanupOnLowDisk {
			val = "true"
		}
		s.db.SetSetting("auto_cleanup_on_low_disk", val)
	}

	jsonResponse(w, map[string]bool{"ok": true})
}
