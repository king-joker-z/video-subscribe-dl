package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

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

// POST /api/sources/:id/sync
func (h *SourcesHandler) HandleSync(w http.ResponseWriter, r *http.Request, id int64) {
	if h.onSyncSource != nil {
		h.onSyncSource(id)
	}
	apiOK(w, map[string]interface{}{"id": id, "message": "同步已触发"})
}

// POST /api/sources/:id/fullscan — 全量补漏扫描
func (h *SourcesHandler) HandleFullScan(w http.ResponseWriter, r *http.Request, id int64) {
	if h.onFullScanSource != nil {
		h.onFullScanSource(id)
	}
	apiOK(w, map[string]interface{}{"id": id, "message": "全量补漏扫描已触发"})
}

// POST /api/sources/parse — 解析 URL，返回类型和名称
func (h *SourcesHandler) HandleParse(w http.ResponseWriter, r *http.Request) {
	// 同时支持 GET（?url=...）和 POST（JSON body），兼容极空间等反代对 POST 的特殊处理
	var rawInputURL string
	if r.Method == "GET" {
		// 优先读 q 参数（base64 编码，绕过极空间对含域名 query string 的 redirect）
		if q := r.URL.Query().Get("q"); q != "" {
			decoded, err := base64.StdEncoding.DecodeString(q)
			if err != nil {
				// 兼容 URL-safe base64
				decoded, err = base64.RawURLEncoding.DecodeString(q)
			}
			if err != nil {
				apiError(w, CodeInvalidParam, "参数解码失败")
				return
			}
			rawInputURL = string(decoded)
		} else {
			rawInputURL = r.URL.Query().Get("url")
		}
		if rawInputURL == "" {
			apiError(w, CodeInvalidParam, "请提供 url 参数")
			return
		}
	} else if r.Method == "POST" {
		var req struct {
			URL string `json:"url"`
		}
		if err := parseJSON(r, &req); err != nil || req.URL == "" {
			apiError(w, CodeInvalidParam, "请提供 url 参数")
			return
		}
		rawInputURL = req.URL
	} else {
		apiError(w, CodeMethodNotAllow, "method not allowed")
		return
	}

	// 构建 client
	var client *bilibili.Client
	if credJSON, _ := h.db.GetSetting("credential_json"); credJSON != "" {
		if cred := bilibili.CredentialFromJSON(credJSON); cred != nil && !cred.IsEmpty() {
			client = bilibili.NewClientWithCredential(cred)
		}
	}
	if client == nil {
		cp, _ := h.db.GetSetting("cookie_path")
		cookie := bilibili.ReadCookieFile(cp)
		client = bilibili.NewClient(cookie)
	}

	// 从输入中提取 URL（兼容抖音 App 分享时追加的社交文本，如 "https://... 9@2.com :1pm"）
	rawURL := extractURL(rawInputURL)
	result := map[string]interface{}{}

	// 1. 收藏夹: space.bilibili.com/xxx/favlist?fid=yyy
	if strings.Contains(rawURL, "favlist") {
		mid, mediaID, err := bilibili.ExtractFavoriteInfo(rawURL)
		if err == nil && mid > 0 {
			result["type"] = "favorite"
			result["mid"] = mid
			result["media_id"] = mediaID
			if info, err := client.GetUPInfo(mid); err == nil && info.Name != "" {
				result["name"] = info.Name + " - 收藏夹"
				result["uploader"] = info.Name
			}
			apiOK(w, result)
			return
		}
	}

	// 2. 合集 Season: collectiondetail 或 lists/xxx?type=season
	if strings.Contains(rawURL, "collectiondetail") || (strings.Contains(rawURL, "/lists/") && strings.Contains(rawURL, "type=season")) {
		mid, seasonID, err := bilibili.ExtractSeasonInfo(rawURL)
		if err == nil && mid > 0 && seasonID > 0 {
			result["type"] = "season"
			result["mid"] = mid
			result["season_id"] = seasonID
			if info, err := client.GetUPInfo(mid); err == nil && info.Name != "" {
				result["uploader"] = info.Name
				archives, meta, err := client.GetSeasonVideos(mid, seasonID, 1, 1)
				_ = archives
				if err == nil && meta != nil && meta.Title != "" {
					result["name"] = meta.Title
				} else {
					result["name"] = info.Name + " - 合集"
				}
			}
			apiOK(w, result)
			return
		}
	}

	// 3. Series: seriesdetail 或 lists/xxx?type=series
	if strings.Contains(rawURL, "seriesdetail") || (strings.Contains(rawURL, "/lists/") && strings.Contains(rawURL, "type=series")) {
		info, err := bilibili.ExtractCollectionInfo(rawURL)
		if err == nil && info.Type == bilibili.CollectionSeries {
			result["type"] = "series"
			result["mid"] = info.MID
			result["series_id"] = info.ID
			if upInfo, err := client.GetUPInfo(info.MID); err == nil && upInfo.Name != "" {
				result["uploader"] = upInfo.Name
				if seriesMeta, err := client.GetSeriesInfo(info.MID, info.ID); err == nil && seriesMeta.Name != "" {
					result["name"] = seriesMeta.Name
				} else {
					result["name"] = upInfo.Name + " - 系列"
				}
			}
			apiOK(w, result)
			return
		}
	}

	// 3.5 抖音号（uniqueID）：纯文本，不含 "://"，形如 "xxx" 或 "@xxx"
	if !strings.Contains(rawURL, "://") && !strings.Contains(rawURL, ".") {
		// 去掉前导 @
		uniqueID := strings.TrimPrefix(rawURL, "@")
		uniqueID = strings.TrimSpace(uniqueID)
		if len(uniqueID) >= 4 && len(uniqueID) <= 30 {
			dyClient := douyin.NewClient()
			// [FIXED: P1-8] 用 recover 包裹 Close()，确保 Close() 内部 panic 不会传播
			defer func() {
				defer func() { recover() }()
				dyClient.Close()
			}()
			if profile, err := dyClient.GetUserByUniqueID(uniqueID); err == nil && profile.SecUID != "" {
				result["type"] = "douyin"
				result["sec_uid"] = profile.SecUID
				result["unique_id"] = profile.UniqueID
				result["name"] = profile.Nickname
				result["uploader"] = profile.Nickname
				result["followers"] = profile.FollowerCount
				apiOK(w, result)
				return
			} else if err != nil {
				apiError(w, CodeInvalidParam, "抖音号 @"+uniqueID+" 查询失败: "+err.Error())
				return
			}
		}
	}

	// 4. 抖音链接
	if douyin.IsDouyinURL(rawURL) {
		dyClient := douyin.NewClient()
		// [FIXED: P1-8] 用 recover 包裹 Close()，确保 Close() 内部 panic 不会传播
		defer func() {
			defer func() { recover() }()
			dyClient.Close()
		}()
		resolved, err := dyClient.ResolveShareURL(rawURL)
		if err == nil {
			switch resolved.Type {
			case douyin.URLTypeUser:
				result["type"] = "douyin"
				result["sec_uid"] = resolved.SecUID
				// 用 GetUserProfile 获取用户名（比 GetUserVideos 更可靠，无视频也能拿到名称）
				if profile, err := dyClient.GetUserProfile(resolved.SecUID); err == nil && profile.Nickname != "" {
					result["name"] = profile.Nickname
					result["uploader"] = profile.Nickname
					result["followers"] = profile.FollowerCount
				}
				apiOK(w, result)
				return
			case douyin.URLTypeVideo:
				// 视频链接 → 提取作者信息，作为用户订阅（用户预期是订阅该作者）
				detail, err := dyClient.GetVideoDetail(resolved.VideoID)
				if err != nil {
					apiError(w, CodeInvalidParam, "获取视频信息失败（可能是风控，请稍后重试）: "+err.Error())
					return
				}
				result["type"] = "douyin"
				// 优先用 Author.SecUID 转成用户订阅，退化为视频订阅
				if detail.Author.SecUID != "" {
					result["sec_uid"] = detail.Author.SecUID
					result["name"] = detail.Author.Nickname
					result["uploader"] = detail.Author.Nickname
					// 尝试获取完整 profile（粉丝数等），失败了也没关系
					if profile, err2 := dyClient.GetUserProfile(detail.Author.SecUID); err2 == nil && profile.Nickname != "" {
						result["name"] = profile.Nickname
						result["uploader"] = profile.Nickname
						result["followers"] = profile.FollowerCount
					}
				} else {
					result["video_id"] = resolved.VideoID
					result["name"] = detail.Author.Nickname
					result["uploader"] = detail.Author.Nickname
				}
				apiOK(w, result)
				return
			}
		}
	}

	// 5. Pornhub 博主主页（放在 B站 ExtractMID 之前，避免被误匹配）
	if isPornhubURL(rawURL) {
		result["type"] = "pornhub"
		result["url"] = rawURL
		phClient := pornhubNewClient()
		defer phClient.Close()
		if info, err := phClient.GetModelInfo(rawURL); err == nil && info.Name != "" {
			result["name"] = info.Name
			result["uploader"] = info.Name
			log.Printf("[source·parse] Pornhub model name: %s", info.Name)
		} else if err != nil {
			log.Printf("[source·parse] GetModelInfo failed for %s: %v", rawURL, err)
		}
		apiOK(w, result)
		return
	}

	// 6. UP 主主页: space.bilibili.com/xxx
	mid, err := bilibili.ExtractMID(rawURL)
	if err == nil && mid > 0 {
		result["type"] = "up"
		result["mid"] = mid
		if info, err := client.GetUPInfo(mid); err == nil && info.Name != "" {
			result["name"] = info.Name
			result["uploader"] = info.Name
		}
		apiOK(w, result)
		return
	}

	apiError(w, CodeInvalidParam, "无法解析该 URL，请输入有效的 B 站、抖音或 Pornhub 链接")
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

// --- Export / Import ---

// ExportSource 导出用的 source 结构（不含 ID、时间戳等内部字段）
type ExportSource struct {
	Type               string `json:"type"`
	URL                string `json:"url"`
	Name               string `json:"name"`
	CheckInterval      int    `json:"check_interval,omitempty"`
	DownloadQuality    string `json:"download_quality,omitempty"`
	DownloadCodec      string `json:"download_codec,omitempty"`
	DownloadDanmaku    bool   `json:"download_danmaku,omitempty"`
	DownloadSubtitle   bool   `json:"download_subtitle,omitempty"`
	DownloadFilter     string `json:"download_filter,omitempty"`
	DownloadQualityMin string `json:"download_quality_min,omitempty"`
	SkipNFO            bool   `json:"skip_nfo,omitempty"`
	SkipPoster         bool   `json:"skip_poster,omitempty"`
	FilterRules        string `json:"filter_rules,omitempty"`
	UseDynamicAPI      bool   `json:"use_dynamic_api,omitempty"`
	Enabled            bool   `json:"enabled"`
}

// ExportPayload 导出文件的顶层结构
type ExportPayload struct {
	Version   string         `json:"version"`
	ExportedAt string        `json:"exported_at"`
	Count     int            `json:"count"`
	Sources   []ExportSource `json:"sources"`
}

// GET /api/sources/export — 导出所有订阅源为 JSON 文件
func (h *SourcesHandler) HandleExport(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	sources, err := h.db.GetSources()
	if err != nil {
		apiError(w, CodeInternal, "获取订阅源失败: "+err.Error())
		return
	}

	exported := make([]ExportSource, 0, len(sources))
	for _, s := range sources {
		exported = append(exported, ExportSource{
			Type:               s.Type,
			URL:                s.URL,
			Name:               s.Name,
			CheckInterval:      s.CheckInterval,
			DownloadQuality:    s.DownloadQuality,
			DownloadCodec:      s.DownloadCodec,
			DownloadDanmaku:    s.DownloadDanmaku,
			DownloadSubtitle:   s.DownloadSubtitle,
			DownloadFilter:     s.DownloadFilter,
			DownloadQualityMin: s.DownloadQualityMin,
			SkipNFO:            s.SkipNFO,
			SkipPoster:         s.SkipPoster,
			FilterRules:        s.FilterRules,
			UseDynamicAPI:      s.UseDynamicAPI,
			Enabled:            s.Enabled,
		})
	}

	payload := ExportPayload{
		Version:    "1",
		ExportedAt: time.Now().Format(time.RFC3339),
		Count:      len(exported),
		Sources:    exported,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		apiError(w, CodeInternal, "JSON 序列化失败: "+err.Error())
		return
	}

	filename := fmt.Sprintf("vsd-sources-%s.json", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Write(data)
}

// ImportResult 导入结果
type ImportResult struct {
	Created  int      `json:"created"`
	Skipped  int      `json:"skipped"`
	Errors   int      `json:"errors"`
	Details  []string `json:"details"`
}

// POST /api/sources/import — 从 JSON 文件导入订阅源
func (h *SourcesHandler) HandleImport(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	var payload ExportPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		apiError(w, CodeInvalidParam, "JSON 解析失败: "+err.Error())
		return
	}

	if len(payload.Sources) == 0 {
		apiError(w, CodeInvalidParam, "导入文件中没有订阅源数据")
		return
	}

	// 获取当前所有源的 URL 用于去重
	existing, err := h.db.GetSources()
	if err != nil {
		apiError(w, CodeInternal, "读取现有订阅源失败: "+err.Error())
		return
	}
	urlSet := make(map[string]bool, len(existing))
	for _, s := range existing {
		urlSet[s.URL] = true
	}

	result := ImportResult{
		Details: make([]string, 0),
	}

	for _, es := range payload.Sources {
		if es.URL == "" {
			result.Errors++
			result.Details = append(result.Details, "跳过: 缺少 URL")
			continue
		}

		// 去重：相同 URL 不重复创建
		if urlSet[es.URL] {
			result.Skipped++
			name := es.Name
			if name == "" {
				name = es.URL
			}
			result.Details = append(result.Details, fmt.Sprintf("跳过(已存在): %s", name))
			continue
		}

		src := &db.Source{
			Type:               es.Type,
			URL:                es.URL,
			Name:               es.Name,
			CheckInterval:      es.CheckInterval,
			DownloadQuality:    es.DownloadQuality,
			DownloadCodec:      es.DownloadCodec,
			DownloadDanmaku:    es.DownloadDanmaku,
			DownloadSubtitle:   es.DownloadSubtitle,
			DownloadFilter:     es.DownloadFilter,
			DownloadQualityMin: es.DownloadQualityMin,
			SkipNFO:            es.SkipNFO,
			SkipPoster:         es.SkipPoster,
			FilterRules:        es.FilterRules,
			UseDynamicAPI:      es.UseDynamicAPI,
			Enabled:            es.Enabled,
		}

		if src.Type == "" {
			src.Type = "up"
		}
		if src.CheckInterval <= 0 {
			src.CheckInterval = 7200
		}
		if src.DownloadQuality == "" {
			src.DownloadQuality = "best"
		}
		if src.DownloadCodec == "" {
			src.DownloadCodec = "all"
		}

		id, err := h.db.CreateSource(src)
		if err != nil {
			result.Errors++
			result.Details = append(result.Details, fmt.Sprintf("失败(%s): %v", es.Name, err))
			continue
		}

		result.Created++
		urlSet[es.URL] = true
		result.Details = append(result.Details, fmt.Sprintf("创建: %s (id=%d)", es.Name, id))
		log.Printf("[source/import] Created: id=%d, name=%s, type=%s", id, es.Name, es.Type)
	}

	log.Printf("[source/import] Import complete: created=%d, skipped=%d, errors=%d",
		result.Created, result.Skipped, result.Errors)
	apiOK(w, result)
}

// ─── Pornhub 辅助函数 ──────────────────────────────────────────────────────────

// isPornhubURL 判断 URL 是否为 Pornhub 链接
func isPornhubURL(rawURL string) bool {
	return strings.Contains(rawURL, "pornhub.com")
}

// pornhubNewClient 创建一个用于 parse 阶段的 Pornhub 客户端（匿名，无 Cookie）
func pornhubNewClient() *pornhub.Client {
	return pornhub.NewClient("")
}
