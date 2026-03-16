package scheduler

import (
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
)

// checkDouyin 检查抖音用户的新视频
// 抖音风控严格，翻页间隔 5-10s（宁慢勿快）
func (s *Scheduler) checkDouyin(src db.Source) {
	client := douyin.NewClient()
	defer client.Close() // 确保 RateLimiter goroutine 被清理

	// 抖音 Cookie 验证（每小时最多一次，和 B 站 Cookie 验证完全独立）
	if time.Since(s.lastDouyinCookieCheck) > 1*time.Hour {
		s.lastDouyinCookieCheck = time.Now()
		if valid, msg := client.ValidateCookie(); !valid {
			log.Printf("[douyin] ⚠️ Cookie 验证失败: %s", msg)
		} else {
			log.Printf("[douyin] Cookie 验证通过: %s", msg)
		}
	}

	// 解析 sec_user_id
	secUID, err := s.resolveDouyinSecUID(client, src.URL)
	if err != nil {
		log.Printf("[douyin] 抖音链接解析失败，请检查 URL 格式是否正确: %v", err)
		return
	}

	// 首次扫描时获取用户详情（头像、名称等）
	if src.Name == "" || src.Name == "未命名" {
		if profile, err := client.GetUserProfile(secUID); err == nil {
			if profile.Nickname != "" {
				src.Name = profile.Nickname
				s.db.UpdateSource(&src)
				log.Printf("[douyin] 用户信息更新: %s (@%s) 粉丝=%d 作品=%d",
					profile.Nickname, profile.UniqueID, profile.FollowerCount, profile.AwemeCount)
			}
		} else {
			log.Printf("[douyin] 获取用户信息失败（不影响视频检查）: %v", err)
		}
	}

	// 增量基准时间
	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	firstScanPages := 0
	if val, err := s.db.GetSetting("first_scan_pages"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			firstScanPages = n
		}
	}

	if isFirstScan {
		log.Printf("[douyin·首次全量] %s: 开始全量扫描 secUID=%s", src.Name, secUID)
		if firstScanPages > 0 {
			log.Printf("[douyin·首次全量] 页数限制: %d 页", firstScanPages)
		}
	} else {
		log.Printf("[douyin·增量] %s: 基准时间 %s secUID=%s",
			src.Name, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"), secUID)
	}

	var maxCursor int64
	totalNew := 0
	pendingCreated := 0
	var maxCreated int64
	stopped := false
	page := 0
	consecutiveErrors := 0

	for {
		result, err := client.GetUserVideos(secUID, maxCursor, consecutiveErrors)
		if err != nil {
			consecutiveErrors++
			errMsg := err.Error()

			// 区分风控错误和普通网络错误
			isRiskControl := strings.Contains(errMsg, "403") ||
				strings.Contains(errMsg, "429") ||
				strings.Contains(errMsg, "captcha") ||
				strings.Contains(errMsg, "verify") ||
				strings.Contains(errMsg, "blocked")

			if isRiskControl {
				log.Printf("[douyin] ⚠️ 抖音风控拦截 (第%d次)，可能是请求频率过高或 IP 被限制: %v", consecutiveErrors, err)
				if consecutiveErrors >= 2 {
					log.Printf("[douyin] 连续 %d 次风控拦截，暂停本轮检查，将在下个周期重试", consecutiveErrors)
					break
				}
				// 风控退避: 30s + 随机 0-30s
				backoff := time.Duration(30000+rand.Intn(30000)) * time.Millisecond
				log.Printf("[douyin] 风控退避 %v 后重试", backoff)
				time.Sleep(backoff)
			} else {
				log.Printf("[douyin] 获取视频列表失败 (第%d次)，可能是网络问题或账号设为私密: %v", consecutiveErrors, err)
				if consecutiveErrors >= 5 {
					log.Printf("[douyin] 连续 %d 次失败，暂停检查，将在下个周期重试", consecutiveErrors)
					break
				}
				// 普通错误指数退避: 5s, 10s, 20s, 40s
				backoff := time.Duration(5*(1<<(consecutiveErrors-1))) * time.Second
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
				log.Printf("[douyin] 退避等待 %v 后重试", backoff)
				time.Sleep(backoff)
			}
			continue
		}
		consecutiveErrors = 0 // 重置连续错误计数

		page++

		for _, v := range result.Videos {
			// 增量检查: 遇到已有视频时停止
			if !isFirstScan && v.CreateTime <= latestVideoAt {
				stopped = true
				break
			}

			if v.CreateTime > maxCreated {
				maxCreated = v.CreateTime
			}

			totalNew++

			// 查重
			exists, _ := s.db.IsVideoDownloaded(src.ID, v.AwemeID)
			if !exists {
				// 首次扫描时更新 source 名称
				if (src.Name == "" || src.Name == "未命名") && v.Author.Nickname != "" {
					src.Name = v.Author.Nickname
					s.db.UpdateSource(&src)
				}

				uploaderName := v.Author.Nickname
				if uploaderName == "" {
					uploaderName = src.Name
				}

				title := v.Desc
				if title == "" {
					title = fmt.Sprintf("douyin_%s", v.AwemeID)
				}

				dl := &db.Download{
					SourceID:  src.ID,
					VideoID:   v.AwemeID,
					Title:     title,
					Uploader:  uploaderName,
					Thumbnail: v.Cover,
					Status:    "pending",
					Duration:  v.Duration / 1000,
				}
				if _, err := s.db.CreateDownload(dl); err != nil {
					log.Printf("[douyin] 保存待下载记录失败 %s: %v", v.AwemeID, err)
				} else {
					pendingCreated++
				}
			}
		}

		if len(result.Videos) == 0 && page == 1 {
			log.Printf("[douyin] ⚠️ 未获取到视频列表，可能是账号设为私密或被风控限制")
		}
		if stopped || !result.HasMore || len(result.Videos) == 0 {
			break
		}

		// 首次扫描页数限制
		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[douyin·首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}

		maxCursor = result.MaxCursor

		// 翻页间隔 5-10s（抖音风控严格，宁慢勿快）
		jitter := time.Duration(5000+rand.Intn(5000)) * time.Millisecond
		time.Sleep(jitter)
	}

	// 更新 latest_video_at
	if maxCreated > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[douyin·首次全量] %s: 获取 %d 个新视频，创建 %d 个 pending 记录 (翻页 %d)",
			src.Name, totalNew, pendingCreated, page)
		if pendingCreated > 0 {
			go s.ProcessAllPending()
		}
	} else if stopped {
		log.Printf("[douyin·增量] %s: 获取 %d 个新视频 (在第 %d 页停止)",
			src.Name, totalNew, page)
	} else {
		log.Printf("[douyin·增量] %s: 获取 %d 个新视频 (翻页 %d)",
			src.Name, totalNew, page)
	}
}

// fullScanDouyin 全量补漏扫描抖音用户（忽略增量基准，扫描所有视频，跳过已下载的）
func (s *Scheduler) fullScanDouyin(src db.Source) {
	client := douyin.NewClient()
	defer client.Close()

	secUID, err := s.resolveDouyinSecUID(client, src.URL)
	if err != nil {
		log.Printf("[full-scan·douyin] 抖音链接解析失败: %v", err)
		return
	}

	uploaderName := src.Name
	if uploaderName == "" || uploaderName == "未命名" {
		uploaderName = fmt.Sprintf("douyin_%s", secUID[:8])
	}

	log.Printf("[full-scan·douyin] %s: 开始全量补漏扫描 secUID=%s", uploaderName, secUID)

	// 第一阶段：翻页拉取所有视频（从 cursor=0 开始，无视 latestVideoAt）
	type videoEntry struct {
		AwemeID    string
		Title      string
		Cover      string
		Author     string
		CreateTime int64
		Duration   int
	}
	var allVideos []videoEntry
	seenIDs := map[string]bool{}

	var maxCursor int64
	page := 0
	consecutiveErrors := 0

	for {
		result, err := client.GetUserVideos(secUID, maxCursor, consecutiveErrors)
		if err != nil {
			consecutiveErrors++
			errMsg := err.Error()

			isRiskControl := strings.Contains(errMsg, "403") ||
				strings.Contains(errMsg, "429") ||
				strings.Contains(errMsg, "captcha") ||
				strings.Contains(errMsg, "verify") ||
				strings.Contains(errMsg, "blocked")

			if isRiskControl {
				log.Printf("[full-scan·douyin] ⚠️ 风控拦截 (第%d次): %v", consecutiveErrors, err)
				if consecutiveErrors >= 2 {
					log.Printf("[full-scan·douyin] 连续 %d 次风控，用已拉取的 %d 个视频继续处理", consecutiveErrors, len(allVideos))
					break
				}
				backoff := time.Duration(30000+rand.Intn(30000)) * time.Millisecond
				log.Printf("[full-scan·douyin] 风控退避 %v 后重试", backoff)
				time.Sleep(backoff)
			} else {
				log.Printf("[full-scan·douyin] 获取视频列表失败 (第%d次): %v", consecutiveErrors, err)
				if consecutiveErrors >= 5 {
					log.Printf("[full-scan·douyin] 连续 %d 次失败，用已拉取的 %d 个视频继续处理", consecutiveErrors, len(allVideos))
					break
				}
				backoff := time.Duration(5*(1<<(consecutiveErrors-1))) * time.Second
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
				log.Printf("[full-scan·douyin] 退避等待 %v 后重试", backoff)
				time.Sleep(backoff)
			}
			continue
		}
		consecutiveErrors = 0
		page++

		for _, v := range result.Videos {
			if seenIDs[v.AwemeID] {
				continue
			}
			seenIDs[v.AwemeID] = true

			title := v.Desc
			if title == "" {
				title = fmt.Sprintf("douyin_%s", v.AwemeID)
			}
			authorName := v.Author.Nickname
			if authorName == "" {
				authorName = uploaderName
			}

			allVideos = append(allVideos, videoEntry{
				AwemeID:    v.AwemeID,
				Title:      title,
				Cover:      v.Cover,
				Author:     authorName,
				CreateTime: v.CreateTime,
				Duration:   v.Duration,
			})
		}

		if page == 1 {
			log.Printf("[full-scan·douyin] %s: 第一页获取 %d 个视频", uploaderName, len(result.Videos))
		}

		if !result.HasMore || len(result.Videos) == 0 {
			break
		}

		maxCursor = result.MaxCursor

		// 翻页间隔 5-10s（抖音风控严格，和 checkDouyin 一致）
		jitter := time.Duration(5000+rand.Intn(5000)) * time.Millisecond
		time.Sleep(jitter)
	}

	log.Printf("[full-scan·douyin] %s: 第一阶段完成，共拉取 %d 个视频 (翻页 %d)", uploaderName, len(allVideos), page)

	// 第二阶段：过滤已下载的，只处理缺失的视频
	var missing []videoEntry
	for _, v := range allVideos {
		exists, _ := s.db.IsVideoDownloaded(src.ID, v.AwemeID)
		if !exists {
			missing = append(missing, v)
		}
	}

	log.Printf("[full-scan·douyin] %s: 列表 %d 个，已下载 %d 个，缺失 %d 个",
		uploaderName, len(allVideos), len(allVideos)-len(missing), len(missing))

	if len(missing) == 0 {
		log.Printf("[full-scan·douyin] %s: 无缺失视频，扫描完成", uploaderName)
		return
	}

	// 第三阶段：创建 pending 下载记录
	created := 0
	var maxCreated int64
	for _, v := range missing {
		if v.CreateTime > maxCreated {
			maxCreated = v.CreateTime
		}

		dl := &db.Download{
			SourceID:  src.ID,
			VideoID:   v.AwemeID,
			Title:     v.Title,
			Uploader:  v.Author,
			Thumbnail: v.Cover,
			Status:    "pending",
			Duration:  v.Duration / 1000,
		}
		if _, err := s.db.CreateDownload(dl); err != nil {
			log.Printf("[full-scan·douyin] 创建下载记录失败 %s: %v", v.AwemeID, err)
			continue
		}
		created++
	}

	// 更新 latestVideoAt（全量扫描完成后也要更新）
	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	if maxCreated > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	log.Printf("[full-scan·douyin] %s: 扫描完成，创建 %d 个待下载任务", uploaderName, created)

	// 触发 pending 处理
	if created > 0 {
		go s.ProcessAllPending()
	}
}

// resolveDouyinSecUID 从 source URL 解析 sec_user_id
func (s *Scheduler) resolveDouyinSecUID(client *douyin.DouyinClient, rawURL string) (string, error) {
	// 先尝试直接提取
	secUID, err := douyin.ExtractSecUID(rawURL)
	if err == nil {
		return secUID, nil
	}

	// 解析分享链接
	result, err := client.ResolveShareURL(rawURL)
	if err != nil {
		return "", err
	}

	if result.Type == douyin.URLTypeUser && result.SecUID != "" {
		return result.SecUID, nil
	}

	return "", fmt.Errorf("URL is not a douyin user page: %s", rawURL)
}

// getDouyinSetting 获取抖音平台配置，优先使用 douyin_ 前缀的设置，fallback 到全局
func (s *Scheduler) getDouyinSetting(key string) string {
	// 优先平台特有配置
	if val, err := s.db.GetSetting("douyin_" + key); err == nil && val != "" {
		return val
	}
	// fallback 到全局配置
	if val, err := s.db.GetSetting(key); err == nil && val != "" {
		return val
	}
	return ""
}
