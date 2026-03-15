package api

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"video-subscribe-dl/internal/db"
)

// UploadersHandler UP 主 API
type UploadersHandler struct {
	db               *db.DB
	onRedownload     func(int64)
	onProcessPending func()
}

func NewUploadersHandler(database *db.DB) *UploadersHandler {
	return &UploadersHandler{db: database}
}

func (h *UploadersHandler) SetRedownloadFunc(fn func(int64)) {
	h.onRedownload = fn
}

func (h *UploadersHandler) SetProcessPendingFunc(fn func()) {
	h.onProcessPending = fn
}

// GET /api/uploaders — UP 主列表（含统计）
func (h *UploadersHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	pg := ParsePagination(r)
	search := r.URL.Query().Get("search")

	uploaders, total, err := h.db.GetDownloadUploaders("", search, pg.Page, pg.PageSize)
	if err != nil {
		apiError(w, CodeInternal, "查询失败: "+err.Error())
		return
	}
	if uploaders == nil {
		uploaders = []db.UploaderStats{}
	}

	// 获取 people 表的头像信息
	type UploaderWithAvatar struct {
		db.UploaderStats
		Avatar string `json:"avatar"`
		MID    string `json:"mid"`
	}

	people, _ := h.db.GetPeople()
	avatarMap := map[string]string{}
	midMap := map[string]string{}
	for _, p := range people {
		avatarMap[p.Name] = p.Avatar
		midMap[p.Name] = p.MID
	}

	var result []UploaderWithAvatar
	for _, u := range uploaders {
		item := UploaderWithAvatar{UploaderStats: u}
		// 不返回 avatar URL，减少前端图片请求
		if mid, ok := midMap[u.Uploader]; ok {
			item.MID = mid
		}
		result = append(result, item)
	}
	if result == nil {
		result = []UploaderWithAvatar{}
	}

	apiPaginated(w, result, total, pg.Page, pg.PageSize)
}

// GET /api/uploaders/:id/videos — 某 UP 主的视频
func (h *UploadersHandler) HandleVideos(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	// 从 URL 提取 uploader 名称
	path := strings.TrimPrefix(r.URL.Path, "/api/uploaders/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[1] != "videos" {
		apiError(w, CodeInvalidParam, "无效的路径")
		return
	}

	uploaderName, err := url.PathUnescape(parts[0])
	if err != nil {
		uploaderName = parts[0]
	}

	pg := ParsePagination(r)
	status := r.URL.Query().Get("status")

	downloads, total, err := h.db.GetDownloadsByUploaderPaged(uploaderName, status, pg.Page, pg.PageSize)
	if err != nil {
		apiError(w, CodeInternal, "查询失败: "+err.Error())
		return
	}
	if downloads == nil {
		downloads = []db.Download{}
	}

	// 获取统计
	stats, _ := h.db.GetDownloadStatsByUploader(uploaderName)

	apiOK(w, map[string]interface{}{
		"items":     downloads,
		"stats":     stats,
		"total":     total,
		"page":      pg.Page,
		"page_size": pg.PageSize,
	})
}

// HandleByID 路由分发 /api/uploaders/:id/...
func (h *UploadersHandler) HandleByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/uploaders/")

	// /api/uploaders/:name/videos
	if strings.Contains(path, "/videos") {
		h.HandleVideos(w, r)
		return
	}

	// POST /api/uploaders/:name/download-pending
	if strings.HasSuffix(path, "/download-pending") && r.Method == "POST" {
		name := strings.TrimSuffix(path, "/download-pending")
		uploaderName, err := url.PathUnescape(name)
		if err != nil {
			uploaderName = name
		}
		h.HandleDownloadPending(w, r, uploaderName)
		return
	}

	// /api/uploaders/:name — 获取单个 UP 主统计
	name, err := url.PathUnescape(path)
	if err != nil {
		name = path
	}

	if r.Method != "GET" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	stats, err := h.db.GetDownloadStatsByUploader(name)
	if err != nil {
		apiError(w, CodeNotFound, "UP 主不存在")
		return
	}

	// 不返回头像 URL，减少图片请求
	mid := ""
	if p, _ := h.db.GetPeopleByName(name); p != nil {
		mid = p.MID
	}

	apiOK(w, map[string]interface{}{
		"stats": stats,
		"mid":   mid,
	})
}

// POST /api/uploaders/:name/download-pending — 批量下载该 UP 主的所有 pending 视频
func (h *UploadersHandler) HandleDownloadPending(w http.ResponseWriter, r *http.Request, uploader string) {
	downloads, err := h.db.GetPendingByUploader(uploader)
	if err != nil {
		apiError(w, CodeInternal, "查询失败: "+err.Error())
		return
	}
	if len(downloads) == 0 {
		apiOK(w, map[string]interface{}{
			"affected": 0,
			"message":  "没有待处理的下载",
		})
		return
	}
	if h.onRedownload != nil {
		for _, dl := range downloads {
			go h.onRedownload(dl.ID)
		}
	} else if h.onProcessPending != nil {
		go h.onProcessPending()
	}
	log.Printf("[uploader] Download pending for %s: %d items", uploader, len(downloads))
	apiOK(w, map[string]interface{}{
		"affected": len(downloads),
		"message":  fmt.Sprintf("已提交 %d 个待处理下载", len(downloads)),
	})
}

// handleUploaderSuggestions GET /api/uploaders — 搜索提示用（快速返回）
func (h *UploadersHandler) HandleSuggestions(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}

	uploaders, _, err := h.db.GetDownloadUploaders("", search, 1, limit)
	if err != nil {
		apiError(w, CodeInternal, "查询失败")
		return
	}

	var names []string
	for _, u := range uploaders {
		names = append(names, u.Uploader)
	}
	if names == nil {
		names = []string{}
	}
	apiOK(w, names)
}
