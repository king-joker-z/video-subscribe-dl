package api

import (
	"encoding/base64"
	"log"
	"net/http"
	"strings"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/douyin"
)

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
