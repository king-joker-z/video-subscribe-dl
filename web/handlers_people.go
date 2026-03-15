package web

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"video-subscribe-dl/internal/db"
)

// GET /api/people - Emby actor 列表（含视频数量）
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}
	people, err := s.db.GetPeopleWithVideoCount()
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if people == nil {
		people = []db.PersonWithCount{}
	}
	jsonResponse(w, people)
}

// GET /api/people/{name}/videos - 获取指定 UP 主的视频列表（同时匹配 uploader 和 source name）
func (s *Server) handlePeopleByName(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/people/")
	// Expected: {name}/videos
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		jsonError(w, "name required", 400)
		return
	}

	rawName := parts[0]
	name, err := url.PathUnescape(rawName)
	if err != nil {
		name = rawName
	}

	if len(parts) == 2 && parts[1] == "videos" {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit == 0 {
			limit = 500
		}
		downloads, err := s.db.GetDownloadsBySourceName(name, limit)
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		if downloads == nil {
			downloads = []db.Download{}
		}
		jsonResponse(w, downloads)
		return
	}

	jsonError(w, "not found", 404)
}

// POST /api/clean/source/{id} — 清理某个订阅源的所有下载记录（可选删除文件）
func (s *Server) handleCleanSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/clean/source/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}

	var body struct {
		DeleteFiles bool `json:"delete_files"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	// 获取源信息
	source, err := s.db.GetSource(id)
	if err != nil {
		jsonError(w, "source not found", 404)
		return
	}

	// 如果要删除文件
	deletedFiles := 0
	if body.DeleteFiles {
		downloads, _ := s.db.GetDownloads(10000)
		for _, dl := range downloads {
			if dl.SourceID == id && dl.FilePath != "" {
				dir := filepath.Dir(dl.FilePath)
				if err := os.RemoveAll(dir); err == nil {
					deletedFiles++
					log.Printf("Deleted: %s", dir)
				}
			}
		}
	}

	// 清理数据库记录
	count, err := s.db.CleanSource(id)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}

	log.Printf("Cleaned source %s: %d records, %d files deleted", source.Name, count, deletedFiles)
	jsonResponse(w, map[string]interface{}{
		"ok":            true,
		"records":       count,
		"files_deleted": deletedFiles,
	})
}

// POST /api/clean/uploader/{name} — 清理某个UP主/订阅源的所有下载记录和文件
func (s *Server) handleCleanUploader(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}

	rawName := strings.TrimPrefix(r.URL.Path, "/api/clean/uploader/")
	name, err := url.PathUnescape(rawName)
	if err != nil {
		name = rawName
	}
	if name == "" {
		jsonError(w, "name required", 400)
		return
	}
	log.Printf("Clean request: %s (raw: %s)", name, rawName)

	var body struct {
		DeleteFiles bool `json:"delete_files"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	// 收集所有匹配的 source_id
	allSources, _ := s.db.GetSources()
	matchSourceIDs := map[int64]bool{}
	for _, src := range allSources {
		if src.Name == name {
			matchSourceIDs[src.ID] = true
		}
	}

	downloads, _ := s.db.GetDownloads(10000)
	deletedRecords := 0
	deletedDirs := map[string]bool{}

	for _, dl := range downloads {
		// 匹配条件：source_id 对应的 source name == name
		if !matchSourceIDs[dl.SourceID] {
			continue
		}
		// 删除文件（从 file_path 推算目录，不拼接 name）
		if body.DeleteFiles && dl.FilePath != "" {
			dir := filepath.Dir(dl.FilePath)
			if dir != "" && dir != "." && dir != "/" && !deletedDirs[dir] {
				if err := os.RemoveAll(dir); err == nil {
					deletedDirs[dir] = true
					log.Printf("Deleted dir: %s", dir)
				}
			}
		}
		s.db.Exec("DELETE FROM downloads WHERE id = ?", dl.ID)
		deletedRecords++
	}

	log.Printf("Cleaned %s: %d records, %d dirs deleted", name, deletedRecords, len(deletedDirs))
	jsonResponse(w, map[string]interface{}{
		"ok":            true,
		"records":       deletedRecords,
		"files_deleted": len(deletedDirs),
	})
}
