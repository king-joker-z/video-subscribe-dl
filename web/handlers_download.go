package web

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"video-subscribe-dl/internal/db"
)

// GET /api/thumb/{id} — 提供本地封面图（fallback: 302 到 bilibili CDN）
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/thumb/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}

	// 查找下载记录
	downloads, err := s.db.GetDownloads(10000)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}

	for _, dl := range downloads {
		if dl.ID == id {
			// 先尝试本地缩略图
			if dl.ThumbPath != "" {
				if _, err := os.Stat(dl.ThumbPath); err == nil {
					http.ServeFile(w, r, dl.ThumbPath)
					return
				}
			}
			// 本地没有，尝试根据 file_path 推算
			if dl.FilePath != "" {
				ext := filepath.Ext(dl.FilePath)
				thumbPath := strings.TrimSuffix(dl.FilePath, ext) + "-thumb.jpg"
				if _, err := os.Stat(thumbPath); err == nil {
					http.ServeFile(w, r, thumbPath)
					return
				}
			}
			// 都没有，302 到 CDN
			if dl.Thumbnail != "" {
				http.Redirect(w, r, dl.Thumbnail, http.StatusFound)
				return
			}
			jsonError(w, "no thumbnail", 404)
			return
		}
	}

	jsonError(w, "download not found", 404)
}

// GET /api/downloads
func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 10000 // 默认返回全部
	}
	status := r.URL.Query().Get("status")
	var downloads []db.Download
	var err error
	if status != "" {
		downloads, err = s.db.GetDownloadsByStatus(status, limit)
	} else {
		downloads, err = s.db.GetDownloads(limit)
	}
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if downloads == nil {
		downloads = []db.Download{}
	}
	jsonResponse(w, downloads)
}

// POST /api/downloads/{id}/retry - Manual retry a failed download
func (s *Server) handleDownloadByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/downloads/")

	// DELETE /api/downloads/{id} — 删除单条下载记录及对应文件
	if r.Method == "DELETE" && !strings.Contains(path, "/") {
		id, err := strconv.ParseInt(path, 10, 64)
		if err != nil {
			jsonError(w, "invalid id", 400)
			return
		}
		// 先查记录拿到文件路径
		dl, _ := s.db.GetDownload(id)
		if dl != nil && dl.FilePath != "" {
			dir := filepath.Dir(dl.FilePath)
			if dir != "" && dir != "." && dir != "/" {
				os.RemoveAll(dir)
				log.Printf("[delete] Removed files: %s", dir)
			}
		}
		_, err = s.db.Exec("DELETE FROM downloads WHERE id = ?", id)
		if err != nil {
			jsonError(w, "delete failed: "+err.Error(), 500)
			return
		}
		log.Printf("[delete] Removed download record %d", id)
		jsonResponse(w, map[string]bool{"ok": true})
		return
	}

	if r.Method == "POST" && strings.HasSuffix(path, "/redownload") {
		idStr := strings.TrimSuffix(path, "/redownload")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			jsonError(w, "invalid id", 400)
			return
		}
		// 重新下载：重置状态并提交到下载队列
		s.db.UpdateDownloadStatus(id, "pending", "", 0, "")
		s.db.ResetRetryCount(id)
		if s.onRetryDownload != nil {
			s.onRetryDownload(id)
			log.Printf("[redownload] Resubmitted download %d", id)
		}
		jsonResponse(w, map[string]bool{"ok": true})
		return
	}

	if r.Method == "POST" && strings.HasSuffix(path, "/retry") {
		idStr := strings.TrimSuffix(path, "/retry")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			jsonError(w, "invalid id", 400)
			return
		}
		// 通过 scheduler 回调真正提交到下载队列
		if s.onRetryDownload != nil {
			s.onRetryDownload(id)
			log.Printf("[manual-retry] Resubmitted download %d via scheduler", id)
			jsonResponse(w, map[string]bool{"ok": true})
		} else {
			// fallback: 仅重置状态
			s.db.UpdateDownloadStatus(id, "pending", "", 0, "")
			s.db.ResetRetryCount(id)
			log.Printf("[manual-retry] Reset download %d to pending (no scheduler)", id)
			jsonResponse(w, map[string]bool{"ok": true})
		}
		return
	}

	jsonError(w, "not found", 404)
}

// POST /api/downloads/batch/process-pending — 开始下载所有 pending
func (s *Server) handleBatchProcessPending(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.onProcessPending != nil {
		s.onProcessPending()
	}
	log.Printf("[batch] Process pending triggered")
	jsonResponse(w, map[string]bool{"ok": true})
}

// POST /api/downloads/batch/retry-failed — 批量重试所有失败的下载
func (s *Server) handleBatchRetryFailed(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	affected, err := s.db.RetryAllFailed()
	if err != nil {
		jsonError(w, "batch retry failed: "+err.Error(), 500)
		return
	}
	log.Printf("[batch] Retried all failed downloads: %d records reset to pending", affected)
	// 自动触发下载
	if s.onProcessPending != nil {
		s.onProcessPending()
	}
	jsonResponse(w, map[string]interface{}{"ok": true, "affected": affected})
}

// DELETE /api/downloads/batch/completed — 批量删除所有已完成的下载记录
func (s *Server) handleBatchDeleteCompleted(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		jsonError(w, "method not allowed", 405)
		return
	}
	affected, err := s.db.DeleteByStatus("completed")
	if err != nil {
		jsonError(w, "batch delete failed: "+err.Error(), 500)
		return
	}
	// 同时清理 relocated 状态的记录
	affected2, _ := s.db.DeleteByStatus("relocated")
	total := affected + affected2
	log.Printf("[batch] Deleted completed/relocated downloads: %d records", total)
	jsonResponse(w, map[string]interface{}{"ok": true, "affected": total})
}
