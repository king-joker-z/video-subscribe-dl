package dscheduler

import (
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
)

// CheckDouyin 检查抖音用户的新视频
func (s *DouyinScheduler) CheckDouyin(src db.Source) {
	if s.IsPaused() {
		log.Printf("[dscheduler] 抖音已暂停，跳过检查 %s", src.Name)
		return
	}

	client := s.newClient()
	defer client.Close()

	// Cookie 验证（每小时最多一次）
	if time.Since(s.lastCookieCheck) > 1*time.Hour {
		s.lastCookieCheck = time.Now()
		if valid, msg := client.ValidateCookie(); !valid {
			log.Printf("[dscheduler] ⚠️ Cookie 验证失败: %s", msg)
			s.SetCookieInvalid(msg)
		} else {
			log.Printf("[dscheduler] Cookie 验证通过: %s", msg)
			s.SetCookieValid()
		}
	}

	secUID, err := s.resolveDouyinSecUID(client, src.URL)
	if err != nil {
		log.Printf("[dscheduler] 抖音链接解析失败: %v", err)
		return
	}

	// 首次扫描时获取用户详情
	if src.Name == "" || src.Name == "未命名" {
		if profile, err := client.GetUserProfile(secUID); err == nil {
			if profile.Nickname != "" {
				src.Name = profile.Nickname
				s.db.UpdateSource(&src)
				log.Printf("[dscheduler] 用户信息更新: %s", profile.Nickname)
			}
		} else {
			log.Printf("[dscheduler] 获取用户信息失败（不影响视频检查）: %v", err)
		}
	}

	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	firstScanPages := 0
	if val, err := s.db.GetSetting("first_scan_pages"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			firstScanPages = n
		}
	}

	if isFirstScan {
		log.Printf("[dscheduler·首次全量] %s: 开始全量扫描 secUID=%s", src.Name, secUID)
	} else {
		log.Printf("[dscheduler·增量] %s: 基准时间 %s secUID=%s",
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

			isRiskControl := strings.Contains(errMsg, "403") ||
				strings.Contains(errMsg, "429") ||
				strings.Contains(errMsg, "captcha") ||
				strings.Contains(errMsg, "verify") ||
				strings.Contains(errMsg, "blocked")

			if isRiskControl {
				log.Printf("[dscheduler] ⚠️ 风控拦截 (第%d次): %v", consecutiveErrors, err)
				if consecutiveErrors >= 2 {
					log.Printf("[dscheduler] 连续 %d 次风控，本轮停止", consecutiveErrors)
					break
				}
				backoff := time.Duration(30000+rand.Intn(30000)) * time.Millisecond
				log.Printf("[dscheduler] 风控退避 %v", backoff)
				s.sleepFn(backoff)
			} else {
				log.Printf("[dscheduler] 获取视频列表失败 (第%d次): %v", consecutiveErrors, err)
				if consecutiveErrors >= 5 {
					log.Printf("[dscheduler] 连续 %d 次失败，本轮停止", consecutiveErrors)
					break
				}
				backoff := time.Duration(5*(1<<(consecutiveErrors-1))) * time.Second
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
				log.Printf("[dscheduler] 退避等待 %v", backoff)
				s.sleepFn(backoff)
			}
			continue
		}
		consecutiveErrors = 0
		page++

		for _, v := range result.Videos {
			if !isFirstScan && v.CreateTime <= latestVideoAt {
				stopped = true
				break
			}

			if v.CreateTime > maxCreated {
				maxCreated = v.CreateTime
			}
			totalNew++

			exists, _ := s.db.IsVideoDownloaded(src.ID, v.AwemeID)
			if !exists {
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
					log.Printf("[dscheduler] 保存待下载记录失败 %s: %v", v.AwemeID, err)
				} else {
					pendingCreated++
				}
			}
		}

		if len(result.Videos) == 0 && page == 1 {
			log.Printf("[dscheduler] ⚠️ 未获取到视频列表，可能是账号私密或被风控")
		}
		if stopped || !result.HasMore || len(result.Videos) == 0 {
			break
		}

		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[dscheduler·首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}

		maxCursor = result.MaxCursor
		jitter := time.Duration(5000+rand.Intn(5000)) * time.Millisecond
		s.sleepFn(jitter)
	}

	if maxCreated > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[dscheduler][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[dscheduler·首次全量] %s: 获取 %d 个新视频，创建 %d 个 pending 记录 (翻页 %d)",
			src.Name, totalNew, pendingCreated, page)
	} else if stopped {
		log.Printf("[dscheduler·增量] %s: 获取 %d 个新视频 (在第 %d 页停止)", src.Name, totalNew, page)
	} else {
		log.Printf("[dscheduler·增量] %s: 获取 %d 个新视频 (翻页 %d)", src.Name, totalNew, page)
	}
}

// FullScanDouyin 全量补漏扫描抖音用户
func (s *DouyinScheduler) FullScanDouyin(src db.Source) {
	if s.IsPaused() {
		log.Printf("[dscheduler·full-scan] 抖音已暂停，跳过全量扫描 %s", src.Name)
		return
	}

	client := s.newClient()
	defer client.Close()

	secUID, err := s.resolveDouyinSecUID(client, src.URL)
	if err != nil {
		log.Printf("[dscheduler·full-scan] 抖音链接解析失败: %v", err)
		return
	}

	uploaderName := src.Name
	if uploaderName == "" || uploaderName == "未命名" {
		uploaderName = fmt.Sprintf("douyin_%s", secUID[:8])
	}

	log.Printf("[dscheduler·full-scan] %s: 开始全量补漏扫描 secUID=%s", uploaderName, secUID)

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
				log.Printf("[dscheduler·full-scan] ⚠️ 风控拦截 (第%d次): %v", consecutiveErrors, err)
				if consecutiveErrors >= 2 {
					log.Printf("[dscheduler·full-scan] 连续 %d 次风控，用已拉取的 %d 个视频继续处理", consecutiveErrors, len(allVideos))
					break
				}
				backoff := time.Duration(30000+rand.Intn(30000)) * time.Millisecond
				s.sleepFn(backoff)
			} else {
				log.Printf("[dscheduler·full-scan] 获取视频列表失败 (第%d次): %v", consecutiveErrors, err)
				if consecutiveErrors >= 5 {
					log.Printf("[dscheduler·full-scan] 连续 %d 次失败，用已拉取的 %d 个视频继续处理", consecutiveErrors, len(allVideos))
					break
				}
				backoff := time.Duration(5*(1<<(consecutiveErrors-1))) * time.Second
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
				s.sleepFn(backoff)
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
			log.Printf("[dscheduler·full-scan] %s: 第一页获取 %d 个视频", uploaderName, len(result.Videos))
		}

		if !result.HasMore || len(result.Videos) == 0 {
			break
		}

		maxCursor = result.MaxCursor
		jitter := time.Duration(5000+rand.Intn(5000)) * time.Millisecond
		s.sleepFn(jitter)
	}

	log.Printf("[dscheduler·full-scan] %s: 第一阶段完成，共拉取 %d 个视频 (翻页 %d)", uploaderName, len(allVideos), page)

	var missing []videoEntry
	for _, v := range allVideos {
		exists, _ := s.db.IsVideoDownloaded(src.ID, v.AwemeID)
		if !exists {
			missing = append(missing, v)
		}
	}

	log.Printf("[dscheduler·full-scan] %s: 列表 %d 个，已下载 %d 个，缺失 %d 个",
		uploaderName, len(allVideos), len(allVideos)-len(missing), len(missing))

	if len(missing) == 0 {
		log.Printf("[dscheduler·full-scan] %s: 无缺失视频，扫描完成", uploaderName)
		return
	}

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
			log.Printf("[dscheduler·full-scan] 创建下载记录失败 %s: %v", v.AwemeID, err)
			continue
		}
		created++
	}

	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	if maxCreated > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[dscheduler][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	log.Printf("[dscheduler·full-scan] %s: 扫描完成，创建 %d 个待下载任务", uploaderName, created)
}

// CheckDouyinMix 检查抖音合集的新视频
func (s *DouyinScheduler) CheckDouyinMix(src db.Source) {
	if s.IsPaused() {
		log.Printf("[dscheduler·合集] 抖音已暂停，跳过检查 %s", src.Name)
		return
	}

	client := s.newClient()
	defer client.Close()

	mixID := parseMixID(src.URL)
	if mixID == "" {
		log.Printf("[dscheduler·合集] mix_id 解析失败: %s", src.URL)
		return
	}

	log.Printf("[dscheduler·合集] %s: 开始检查 mix_id=%s", src.Name, mixID)

	videos, err := client.GetMixVideos(mixID)
	if err != nil {
		log.Printf("[dscheduler·合集] %s: 获取视频列表失败: %v", src.Name, err)
		return
	}

	newCount := 0
	for _, v := range videos {
		exists, _ := s.db.IsVideoDownloaded(src.ID, v.AwemeID)
		if exists {
			continue
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
			log.Printf("[dscheduler·合集] 保存待下载记录失败 %s: %v", v.AwemeID, err)
		} else {
			newCount++
		}
	}

	log.Printf("[dscheduler·合集] %s: 发现 %d 个新视频（合集共 %d 个）", src.Name, newCount, len(videos))
}
