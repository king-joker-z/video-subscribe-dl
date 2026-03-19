package api

import (
	"fmt"
	"net/http"
	"strings"

	"video-subscribe-dl/internal/db"
)

// SearchHandler 全局搜索 API
type SearchHandler struct {
	db *db.DB
}

func NewSearchHandler(database *db.DB) *SearchHandler {
	return &SearchHandler{db: database}
}

// SearchResult 搜索结果项
type SearchResult struct {
	Type     string `json:"type"`               // video | uploader | source
	ID       int64  `json:"id"`                 // 记录 ID（video/source），UP 主为 0
	Title    string `json:"title"`              // 显示标题
	Subtitle string `json:"subtitle,omitempty"` // 副标题
	Status   string `json:"status,omitempty"`   // 状态（仅 video）
	Route    string `json:"route"`              // 前端路由 hash
	Icon     string `json:"icon"`               // lucide 图标名
}

// GET /api/search?q=xxx — 全局搜索
func (h *SearchHandler) HandleSearch(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		apiOK(w, []SearchResult{})
		return
	}

	var results []SearchResult
	like := "%" + q + "%"

	// 搜索视频（最多 8 条）
	videoRows, err := h.db.Query(`
		SELECT id, COALESCE(title,''), COALESCE(uploader,''), status, video_id
		FROM downloads
		WHERE status != 'deleted'
		  AND (title LIKE ? OR uploader LIKE ? OR video_id LIKE ?)
		ORDER BY created_at DESC
		LIMIT 8
	`, like, like, like)
	if err == nil {
		defer videoRows.Close()
		for videoRows.Next() {
			var id int64
			var title, uploader, status, videoID string
			if err := videoRows.Scan(&id, &title, &uploader, &status, &videoID); err != nil {
				continue
			}
			displayTitle := title
			if displayTitle == "" {
				displayTitle = videoID
			}
			results = append(results, SearchResult{
				Type:     "video",
				ID:       id,
				Title:    displayTitle,
				Subtitle: uploader,
				Status:   status,
				Route:    "#/videos",
				Icon:     "video",
			})
		}
	}

	// 搜索 UP 主（最多 5 条）
	uploaderRows, err := h.db.Query(`
		SELECT uploader, COUNT(*) as cnt
		FROM downloads
		WHERE status != 'deleted' AND uploader != '' AND uploader LIKE ?
		GROUP BY uploader
		ORDER BY cnt DESC
		LIMIT 5
	`, like)
	if err == nil {
		defer uploaderRows.Close()
		for uploaderRows.Next() {
			var uploader string
			var cnt int
			if err := uploaderRows.Scan(&uploader, &cnt); err != nil {
				continue
			}
			results = append(results, SearchResult{
				Type:     "uploader",
				Title:    uploader,
				Subtitle: fmt.Sprintf("%d 个视频", cnt),
				Route:    "#/videos?uploader=" + uploader,
				Icon:     "user",
			})
		}
	}

	// 搜索订阅源（最多 5 条）
	sourceRows, err := h.db.Query(`
		SELECT id, COALESCE(name,''), type, url
		FROM sources
		WHERE (name LIKE ? OR url LIKE ?) AND enabled = 1
		ORDER BY id DESC
		LIMIT 5
	`, like, like)
	if err == nil {
		defer sourceRows.Close()
		for sourceRows.Next() {
			var id int64
			var name, srcType, url string
			if err := sourceRows.Scan(&id, &name, &srcType, &url); err != nil {
				continue
			}
			displayName := name
			if displayName == "" {
				displayName = url
			}
			typeLabels := map[string]string{
				"up": "UP 主", "season": "合集", "favorite": "收藏夹",
				"watchlater": "稍后再看", "series": "系列",
			}
			subtitle := typeLabels[srcType]
			if subtitle == "" {
				subtitle = srcType
			}
			results = append(results, SearchResult{
				Type:     "source",
				ID:       id,
				Title:    displayName,
				Subtitle: subtitle,
				Route:    fmt.Sprintf("#/videos?source_id=%d&source_name=%s", id, displayName),
				Icon:     "rss",
			})
		}
	}

	if results == nil {
		results = []SearchResult{}
	}

	apiOK(w, results)
}
