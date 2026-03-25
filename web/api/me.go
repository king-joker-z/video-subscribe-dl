package api

import (
	"fmt"
	"net/http"
	"strconv"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

// MeHandler /me 接口：关注列表 & 收藏夹
type MeHandler struct {
	db   *db.DB
	bili func() *bilibili.Client // 获取当前 bilibili client
}

func NewMeHandler(database *db.DB) *MeHandler {
	return &MeHandler{db: database}
}

func (h *MeHandler) SetBiliClientFunc(fn func() *bilibili.Client) {
	h.bili = fn
}

func (h *MeHandler) getClient() *bilibili.Client {
	if h.bili != nil {
		return h.bili()
	}
	return nil
}

// GET /api/me/favorites — 我的收藏夹列表
func (h *MeHandler) HandleFavorites(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}
	client := h.getClient()
	if client == nil {
		apiError(w, CodeCredentialEmpty, "未登录")
		return
	}
	favs, err := client.GetMyFavorites()
	if err != nil {
		apiError(w, CodeInternal, "获取收藏夹失败: "+err.Error())
		return
	}
	apiOK(w, favs)
}

// GET /api/me/uppers — 我关注的 UP 主（支持搜索和分页）
func (h *MeHandler) HandleUppers(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}
	client := h.getClient()
	if client == nil {
		apiError(w, CodeCredentialEmpty, "未登录")
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 20
	}
	name := r.URL.Query().Get("name")

	var result *bilibili.FollowedUppers
	var err error
	if name != "" {
		result, err = client.SearchFollowedUppers(name, page, pageSize)
	} else {
		result, err = client.GetFollowedUppers(page, pageSize)
	}
	if err != nil {
		apiError(w, CodeInternal, "获取关注列表失败: "+err.Error())
		return
	}

	// 标记已订阅的 UP 主
	sources, _ := h.db.GetSources()
	subscribedMIDs := map[string]bool{}
	for _, s := range sources {
		if s.Type == "up" {
			// 从 URL 提取 mid
			mid := bilibili.ExtractMIDFromURL(s.URL)
			if mid != "" {
				subscribedMIDs[mid] = true
			}
		}
	}

	type UpperWithSub struct {
		bilibili.FollowedUpper
		Subscribed bool `json:"subscribed"`
	}

	items := make([]UpperWithSub, 0, len(result.List))
	for _, u := range result.List {
		items = append(items, UpperWithSub{
			FollowedUpper: u,
			Subscribed:    subscribedMIDs[fmt.Sprintf("%d", u.MID)],
		})
	}

	apiPaginated(w, items, result.Total, page, pageSize)
}

// POST /api/me/subscribe — 一键批量订阅
func (h *MeHandler) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	var req struct {
		MIDs []int64  `json:"mids"` // UP 主 mid 列表
		FIDs []int64  `json:"fids"` // 收藏夹 id 列表
		Type string   `json:"type"` // "up" 或 "favorite"
	}
	if err := parseJSON(r, &req); err != nil {
		apiError(w, CodeInvalidParam, "参数错误")
		return
	}

	client := h.getClient()
	if client == nil {
		apiError(w, CodeCredentialEmpty, "未登录")
		return
	}

	var created int
	var errors []string

	if req.Type == "up" && len(req.MIDs) > 0 {
		for _, mid := range req.MIDs {
			// 检查是否已订阅
			url := fmt.Sprintf("https://space.bilibili.com/%d", mid)
			exists := false
			sources, _ := h.db.GetSources()
			for _, s := range sources {
				if s.URL == url {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			// 获取 UP 主信息作为名称
			name := fmt.Sprintf("UP主 %d", mid)
			if info, err := client.GetUPInfo(mid); err == nil && info != nil {
				name = info.Name
			}
			src := &db.Source{
				Type:            "up",
				URL:             url,
				Name:            name,
				Enabled:         true,
				CheckInterval:   7200,
				DownloadQuality: "best",
				DownloadCodec:   "all",
			}
			if _, err := h.db.CreateSource(src); err != nil {
				errors = append(errors, fmt.Sprintf("mid %d: %v", mid, err))
			} else {
				created++
			}
		}
	}

	if req.Type == "favorite" && len(req.FIDs) > 0 {
		for _, fid := range req.FIDs {
			url := fmt.Sprintf("https://space.bilibili.com/0/favlist?fid=%d", fid)
			exists := false
			sources, _ := h.db.GetSources()
			for _, s := range sources {
				if s.URL == url || s.URL == fmt.Sprintf("https://www.bilibili.com/medialist/detail/ml%d", fid) {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			src := &db.Source{
				Type:            "favorite",
				URL:             fmt.Sprintf("https://www.bilibili.com/medialist/detail/ml%d", fid),
				Name:            fmt.Sprintf("收藏夹 %d", fid),
				Enabled:         true,
				CheckInterval:   7200,
				DownloadQuality: "best",
				DownloadCodec:   "all",
			}
			if _, err := h.db.CreateSource(src); err != nil {
				errors = append(errors, fmt.Sprintf("fid %d: %v", fid, err))
			} else {
				created++
			}
		}
	}

	apiOK(w, map[string]interface{}{
		"created": created,
		"errors":  errors,
	})
}
