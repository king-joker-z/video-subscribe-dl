package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

// SourcesHandler 订阅源 API
type SourcesHandler struct {
	db                 *db.DB
	onSyncSource       func(int64)
}

func NewSourcesHandler(database *db.DB) *SourcesHandler {
	return &SourcesHandler{db: database}
}

func (h *SourcesHandler) SetSyncSourceFunc(fn func(int64)) {
	h.onSyncSource = fn
}

// GET /api/sources
func (h *SourcesHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	sourceType := r.URL.Query().Get("type")

	sources, err := h.db.GetSources()
	if err != nil {
		apiError(w, CodeInternal, "获取订阅源失败: "+err.Error())
		return
	}
	if sources == nil {
		sources = []db.Source{}
	}

	// 按类型筛选
	if sourceType != "" {
		var filtered []db.Source
		for _, s := range sources {
			if s.Type == sourceType {
				filtered = append(filtered, s)
			}
		}
		sources = filtered
		if sources == nil {
			sources = []db.Source{}
		}
	}

	// 附加每个源的视频统计
	type SourceWithStats struct {
		db.Source
		VideoCount     int `json:"video_count"`
		CompletedCount int `json:"completed_count"`
		FailedCount    int `json:"failed_count"`
		PendingCount   int `json:"pending_count"`
	}

	var result []SourceWithStats
	for _, s := range sources {
		stats := SourceWithStats{Source: s}
		// 统计该源的视频数
		h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ?", s.ID).Scan(&stats.VideoCount)
		h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status IN ('completed','relocated')", s.ID).Scan(&stats.CompletedCount)
		h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status IN ('failed','permanent_failed')", s.ID).Scan(&stats.FailedCount)
		h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status = 'pending'", s.ID).Scan(&stats.PendingCount)
		result = append(result, stats)
	}
	if result == nil {
		result = []SourceWithStats{}
	}

	apiOK(w, result)
}

// POST /api/sources
func (h *SourcesHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	var source db.Source
	if err := parseJSON(r, &source); err != nil {
		apiError(w, CodeInvalidParam, "请求参数错误: "+err.Error())
		return
	}

	// 默认 type
	if source.Type == "" {
		source.Type = "up"
	}

	// 自动识别 URL 类型
	if source.Type == "up" && source.URL != "" {
		if strings.Contains(source.URL, "favlist") {
			source.Type = "favorite"
		} else if strings.Contains(source.URL, "/lists/") && strings.Contains(source.URL, "type=season") {
			source.Type = "season"
		} else if strings.Contains(source.URL, "collectiondetail") {
			source.Type = "season"
		}
	}

	// 构建 client
	var client *bilibili.Client
	if source.CookiesFile != "" {
		cookie := bilibili.ReadCookieFile(source.CookiesFile)
		client = bilibili.NewClient(cookie)
	} else if credJSON, _ := h.db.GetSetting("credential_json"); credJSON != "" {
		if cred := bilibili.CredentialFromJSON(credJSON); cred != nil && !cred.IsEmpty() {
			client = bilibili.NewClientWithCredential(cred)
		}
	}
	if client == nil {
		cp, _ := h.db.GetSetting("cookie_path")
		cookie := bilibili.ReadCookieFile(cp)
		client = bilibili.NewClient(cookie)
	}

	// 自动获取名称
	switch source.Type {
	case "season":
		mid, seasonID, _ := bilibili.ExtractSeasonInfo(source.URL)
		if mid > 0 && seasonID > 0 && source.Name == "" {
			if info, err := client.GetUPInfo(mid); err == nil && info.Name != "" {
				archives, meta, err := client.GetSeasonVideos(mid, seasonID, 1, 1)
				_ = archives
				if err == nil && meta != nil && meta.Title != "" {
					source.Name = meta.Title
				} else {
					source.Name = info.Name + " - 合集"
				}
			}
		}
	case "favorite":
		mid, mediaID, _ := bilibili.ExtractFavoriteInfo(source.URL)
		if mid > 0 && source.Name == "" {
			if info, err := client.GetUPInfo(mid); err == nil && info.Name != "" {
				if mediaID > 0 {
					folders, err := client.GetFavoriteList(mid)
					if err == nil {
						for _, f := range folders {
							if f.ID == mediaID {
								source.Name = info.Name + " - " + f.Title
								break
							}
						}
					}
				}
				if source.Name == "" {
					source.Name = info.Name + " - 收藏夹"
				}
			}
		}
	case "watchlater":
		if source.URL == "" {
			source.URL = "watchlater://0"
		}
		if source.Name == "" {
			result, err := client.VerifyCookie()
			if err == nil && result.LoggedIn {
				source.Name = result.Username + " - 稍后再看"
				source.URL = fmt.Sprintf("watchlater://%d", result.MID)
			} else {
				source.Name = "稍后再看"
			}
		}
	default:
		if source.Name == "" && source.URL != "" {
			if mid, err := bilibili.ExtractMID(source.URL); err == nil {
				if info, err := client.GetUPInfo(mid); err == nil && info.Name != "" {
					source.Name = info.Name
				}
			}
		}
	}

	// 关联全局 Cookie
	if source.CookiesFile == "" {
		if cookiePath, err := h.db.GetSetting("cookie_path"); err == nil && cookiePath != "" {
			source.CookiesFile = cookiePath
		}
	}

	id, err := h.db.CreateSource(&source)
	if err != nil {
		apiError(w, CodeInternal, "创建订阅源失败: "+err.Error())
		return
	}

	log.Printf("[source] Created: id=%d, name=%s, type=%s", id, source.Name, source.Type)
	apiOK(w, map[string]interface{}{
		"id":   id,
		"name": source.Name,
		"type": source.Type,
	})
}

// GET /api/sources/:id
func (h *SourcesHandler) HandleGet(w http.ResponseWriter, r *http.Request, id int64) {
	source, err := h.db.GetSource(id)
	if err != nil {
		apiError(w, CodeSourceNotFound, "订阅源不存在")
		return
	}

	// 附加视频统计
	result := map[string]interface{}{
		"source": source,
	}
	var videoCount, completedCount, failedCount, pendingCount int
	h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ?", id).Scan(&videoCount)
	h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status IN ('completed','relocated')", id).Scan(&completedCount)
	h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status IN ('failed','permanent_failed')", id).Scan(&failedCount)
	h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status = 'pending'", id).Scan(&pendingCount)
	result["video_count"] = videoCount
	result["completed_count"] = completedCount
	result["failed_count"] = failedCount
	result["pending_count"] = pendingCount

	apiOK(w, result)
}

// PUT /api/sources/:id
func (h *SourcesHandler) HandleUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	var source db.Source
	if err := parseJSON(r, &source); err != nil {
		apiError(w, CodeInvalidParam, "请求参数错误")
		return
	}
	source.ID = id
	if err := h.db.UpdateSource(&source); err != nil {
		apiError(w, CodeInternal, "更新失败: "+err.Error())
		return
	}
	apiOK(w, map[string]interface{}{"id": id})
}

// DELETE /api/sources/:id
func (h *SourcesHandler) HandleDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := h.db.DeleteSource(id); err != nil {
		apiError(w, CodeInternal, "删除失败: "+err.Error())
		return
	}
	log.Printf("[source] Deleted: id=%d", id)
	apiOK(w, map[string]interface{}{"id": id})
}

// POST /api/sources/:id/sync
func (h *SourcesHandler) HandleSync(w http.ResponseWriter, r *http.Request, id int64) {
	if h.onSyncSource != nil {
		h.onSyncSource(id)
	}
	apiOK(w, map[string]interface{}{"id": id, "message": "同步已触发"})
}

// HandleByID 路由分发 /api/sources/:id
func (h *SourcesHandler) HandleByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sources/")
	if path == "" {
		apiError(w, CodeInvalidParam, "缺少 ID")
		return
	}

	// /api/sources/:id/sync
	if strings.HasSuffix(path, "/sync") && r.Method == "POST" {
		idStr := strings.TrimSuffix(path, "/sync")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			apiError(w, CodeInvalidParam, "无效的 ID")
			return
		}
		h.HandleSync(w, r, id)
		return
	}

	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil {
		apiError(w, CodeInvalidParam, "无效的 ID")
		return
	}

	switch r.Method {
	case "GET":
		h.HandleGet(w, r, id)
	case "PUT":
		h.HandleUpdate(w, r, id)
	case "DELETE":
		h.HandleDelete(w, r, id)
	default:
		apiError(w, CodeMethodNotAllow, "method not allowed")
	}
}
