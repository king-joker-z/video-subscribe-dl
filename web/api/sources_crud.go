package api

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/pornhub"
)

// SourcesHandler 订阅源 API
type SourcesHandler struct {
	db               *db.DB
	onSyncSource     func(int64)
	onFullScanSource func(int64)
}

func NewSourcesHandler(database *db.DB) *SourcesHandler {
	return &SourcesHandler{db: database}
}

func (h *SourcesHandler) SetFullScanSourceFunc(fn func(int64)) {
	h.onFullScanSource = fn
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

	// 附加每个源的视频统计（复用逻辑）
	type SourceWithStats struct {
		db.Source
		VideoCount     int `json:"video_count"`
		CompletedCount int `json:"completed_count"`
		FailedCount    int `json:"failed_count"`
		PendingCount   int `json:"pending_count"`
	}
	// 预加载所有 source 的统计数据（单次 GROUP BY 查询，避免 N+1）
	// [FIXED: P1-9] 记录 GetSourcesStats 错误，而非静默丢弃（nil map 读取安全，但应有可见日志）
	statsMap, err := h.db.GetSourcesStats()
	if err != nil {
		log.Printf("[sources] GetSourcesStats error: %v", err)
	}

	buildStats := func(sources []db.Source) []SourceWithStats {
		result := make([]SourceWithStats, 0, len(sources))
		for _, s := range sources {
			stats := SourceWithStats{Source: s}
			if st, ok := statsMap[s.ID]; ok {
				stats.VideoCount = st.Total
				stats.CompletedCount = st.Completed
				stats.FailedCount = st.Failed
				stats.PendingCount = st.Pending
			}
			result = append(result, stats)
		}
		return result
	}

	// 分页模式
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		page, err := strconv.Atoi(pageStr)
		if err != nil || page < 1 {
			page = 1
		}
		pageSizeStr := r.URL.Query().Get("page_size")
		pageSize, err := strconv.Atoi(pageSizeStr)
		if err != nil || pageSize < 1 {
			pageSize = 20
		}
		sources, total, err := h.db.GetSourcesPaged(sourceType, page, pageSize)
		if err != nil {
			apiError(w, CodeInternal, "获取订阅源失败: "+err.Error())
			return
		}
		if sources == nil {
			sources = []db.Source{}
		}
		result := buildStats(sources)
		apiOK(w, map[string]interface{}{
			"sources":   result,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		})
		return
	}

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

	result := buildStats(sources)
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

	// 清洗 URL：提取纯 URL，去除抖音分享文本追加的社交内容（如 "9@2.com :1pm"）
	source.URL = extractURL(source.URL)

	// 新建源默认启用（JSON 未传 enabled 时 bool 零值为 false，需显式设为 true）
	source.Enabled = true

	// 默认 type
	if source.Type == "" {
		source.Type = "up"
	}

	// 自动识别 URL 类型: 先检测抖音，再检测 Pornhub（互斥，else if 保证不覆盖）
	if douyin.IsDouyinURL(source.URL) {
		source.Type = "douyin"
	} else if isPornhubURL(source.URL) {
		source.Type = "pornhub"
	}
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
	case "douyin":
		if source.URL != "" {
			dyClient := douyin.NewClient()
			// [FIXED: P1-8] 用 recover 包裹 Close()，确保 Close() 内部 panic 不会传播
			defer func() {
				defer func() { recover() }()
				dyClient.Close()
			}()
			// 抖音号输入（不含 "://"，不含 "."）→ 先通过 QueryUser 查 sec_uid
			if !strings.Contains(source.URL, "://") && !strings.Contains(source.URL, ".") {
				uniqueID := strings.TrimPrefix(source.URL, "@")
				uniqueID = strings.TrimSpace(uniqueID)
				if profile, err := dyClient.GetUserByUniqueID(uniqueID); err == nil && profile.SecUID != "" {
					if source.Name == "" {
						source.Name = profile.Nickname
					}
					source.URL = "https://www.douyin.com/user/" + profile.SecUID
				} else if err != nil {
					apiError(w, CodeInvalidParam, "抖音号查询失败，请确认抖音号正确或稍后重试: "+err.Error())
					return
				}
			} else {
				// 正常 URL 解析（包含 "://"）
				resolveResult, err := dyClient.ResolveShareURL(source.URL)
				if err == nil {
					switch resolveResult.Type {
					case douyin.URLTypeUser:
						if source.Name == "" {
							if profile, err := dyClient.GetUserProfile(resolveResult.SecUID); err == nil && profile.Nickname != "" {
								source.Name = profile.Nickname
							}
						}
						source.URL = "https://www.douyin.com/user/" + resolveResult.SecUID
					case douyin.URLTypeVideo:
						// 视频链接 → 提取作者 SecUID，转为用户订阅
						detail, err := dyClient.GetVideoDetail(resolveResult.VideoID)
						if err == nil && detail.Author.SecUID != "" {
							if source.Name == "" {
								source.Name = detail.Author.Nickname
							}
							source.URL = "https://www.douyin.com/user/" + detail.Author.SecUID
						}
					}
				}
			}
		}
	case "pornhub":
		// 从博主主页 URL 自动获取名称
		if source.Name == "" && source.URL != "" {
			phClient := pornhubNewClient()
			if info, err := phClient.GetModelInfo(source.URL); err == nil && info.Name != "" {
				source.Name = info.Name
			}
			phClient.Close()
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

	// 关联全局 Cookie（抖音和 Pornhub 不需要）
	if source.Type != "douyin" && source.Type != "pornhub" && source.CookiesFile == "" {
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
	// [FIXED: P2-9] 处理 Scan 错误，避免数据库临时不可用时静默返回 0
	result := map[string]interface{}{
		"source": source,
	}
	var videoCount, completedCount, failedCount, pendingCount int
	if err := h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ?", id).Scan(&videoCount); err != nil {
		log.Printf("[source] count query error (source_id=%d): %v", id, err)
	}
	if err := h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status IN ('completed','relocated')", id).Scan(&completedCount); err != nil {
		log.Printf("[source] completed count query error (source_id=%d): %v", id, err)
	}
	if err := h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status IN ('failed','permanent_failed')", id).Scan(&failedCount); err != nil {
		log.Printf("[source] failed count query error (source_id=%d): %v", id, err)
	}
	if err := h.db.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status = 'pending'", id).Scan(&pendingCount); err != nil {
		log.Printf("[source] pending count query error (source_id=%d): %v", id, err)
	}
	result["video_count"] = videoCount
	result["completed_count"] = completedCount
	result["failed_count"] = failedCount
	result["pending_count"] = pendingCount

	apiOK(w, result)
}

// PUT /api/sources/:id — 支持部分更新
func (h *SourcesHandler) HandleUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	// 先读取现有记录
	existing, err := h.db.GetSource(id)
	if err != nil {
		apiError(w, CodeSourceNotFound, "订阅源不存在")
		return
	}

	// 解析请求体为 map 以支持部分更新
	var body map[string]interface{}
	if err := parseJSON(r, &body); err != nil {
		apiError(w, CodeInvalidParam, "请求参数错误")
		return
	}

	// 逐字段合并
	if v, ok := body["name"]; ok {
		if s, ok := v.(string); ok {
			existing.Name = s
		}
	}
	if v, ok := body["enabled"]; ok {
		switch val := v.(type) {
		case bool:
			existing.Enabled = val
		case float64:
			existing.Enabled = val != 0
		}
	}
	if v, ok := body["download_quality"]; ok {
		if s, ok := v.(string); ok {
			existing.DownloadQuality = s
		}
	}
	if v, ok := body["download_codec"]; ok {
		if s, ok := v.(string); ok {
			existing.DownloadCodec = s
		}
	}
	if v, ok := body["download_danmaku"]; ok {
		switch val := v.(type) {
		case bool:
			existing.DownloadDanmaku = val
		case float64:
			existing.DownloadDanmaku = val != 0
		}
	}
	if v, ok := body["download_subtitle"]; ok {
		switch val := v.(type) {
		case bool:
			existing.DownloadSubtitle = val
		case float64:
			existing.DownloadSubtitle = val != 0
		}
	}
	if v, ok := body["download_filter"]; ok {
		if s, ok := v.(string); ok {
			existing.DownloadFilter = s
		}
	}
	if v, ok := body["download_quality_min"]; ok {
		if s, ok := v.(string); ok {
			existing.DownloadQualityMin = s
		}
	}
	if v, ok := body["skip_nfo"]; ok {
		switch val := v.(type) {
		case bool:
			existing.SkipNFO = val
		case float64:
			existing.SkipNFO = val != 0
		}
	}
	if v, ok := body["skip_poster"]; ok {
		switch val := v.(type) {
		case bool:
			existing.SkipPoster = val
		case float64:
			existing.SkipPoster = val != 0
		}
	}
	if v, ok := body["use_dynamic_api"]; ok {
		switch val := v.(type) {
		case bool:
			existing.UseDynamicAPI = val
		case float64:
			existing.UseDynamicAPI = val != 0
		}
	}
	if v, ok := body["check_interval"]; ok {
		if f, ok := v.(float64); ok {
			existing.CheckInterval = int(f)
		}
	}
	if v, ok := body["cookies_file"]; ok {
		if s, ok := v.(string); ok {
			existing.CookiesFile = s
		}
	}
	if v, ok := body["filter_rules"]; ok {
		if s, ok := v.(string); ok {
			existing.FilterRules = s
		}
	}

	if err := h.db.UpdateSource(existing); err != nil {
		apiError(w, CodeInternal, "更新失败: "+err.Error())
		return
	}
	log.Printf("[source] Updated: id=%d, name=%s", id, existing.Name)
	apiOK(w, existing)
}

// DELETE /api/sources/:id
func (h *SourcesHandler) HandleDelete(w http.ResponseWriter, r *http.Request, id int64) {
	deleteFiles := r.URL.Query().Get("deleteFiles") == "true"
	if deleteFiles {
		deleted, err := h.db.DeleteSourceWithFiles(id)
		if err != nil {
			apiError(w, CodeInternal, "删除失败: "+err.Error())
			return
		}
		log.Printf("[source] Deleted with files: id=%d, removedFiles=%d", id, deleted)
		apiOK(w, map[string]interface{}{"id": id, "deletedFiles": deleted})
	} else {
		if err := h.db.DeleteSource(id); err != nil {
			apiError(w, CodeInternal, "删除失败: "+err.Error())
			return
		}
		log.Printf("[source] Deleted: id=%d", id)
		apiOK(w, map[string]interface{}{"id": id})
	}
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

	// /api/sources/:id/fullscan — 全量补漏扫描
	if strings.HasSuffix(path, "/fullscan") && r.Method == "POST" {
		idStr := strings.TrimSuffix(path, "/fullscan")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			apiError(w, CodeInvalidParam, "无效的 ID")
			return
		}
		h.HandleFullScan(w, r, id)
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

// reExtractURL 从文本中提取第一个 http(s):// URL，兼容抖音分享文本（"https://v.douyin.com/xxx/ 9@2.com :1pm"）
var reExtractURL = regexp.MustCompile(`https?://[^\s]+`)

// extractURL 从输入文本中提取 URL，若无则返回去除空白后的原始输入（兼容纯抖音号输入）
func extractURL(input string) string {
	input = strings.TrimSpace(input)
	// 去掉前端为绕过极空间反代规则追加的 &_=1 后缀
	input = strings.TrimSuffix(input, "&_=1")
	input = strings.TrimSpace(input)
	if m := reExtractURL.FindString(input); m != "" {
		// 去掉末尾可能多余的中文标点（抖音分享文本有时会追加 "。、，" 等）
		return strings.TrimRight(m, "。、，")
	}
	return input
}

// isPornhubURL 判断 URL 是否为 Pornhub 链接
func isPornhubURL(rawURL string) bool {
	return strings.Contains(rawURL, "pornhub.com")
}

// pornhubNewClient 创建一个用于 parse 阶段的 Pornhub 客户端（匿名，无 Cookie）
func pornhubNewClient() *pornhub.Client {
	return pornhub.NewClient("")
}
