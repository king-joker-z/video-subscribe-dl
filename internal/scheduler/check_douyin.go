package scheduler

import (
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
)

// checkDouyin 检查抖音用户的新视频
// 抖音风控严格，翻页间隔 5-10s（宁慢勿快）
func (s *Scheduler) checkDouyin(src db.Source) {
	client := douyin.NewClient()

	// 解析 sec_user_id
	secUID, err := s.resolveDouyinSecUID(client, src.URL)
	if err != nil {
		log.Printf("[douyin] 解析 sec_user_id 失败: %v", err)
		return
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
		result, err := client.GetUserVideos(secUID, maxCursor)
		if err != nil {
			consecutiveErrors++
			log.Printf("[douyin] GetUserVideos 失败 (连续第%d次): %v", consecutiveErrors, err)

			// 指数退避: 连续 3 次失败后放弃
			if consecutiveErrors >= 3 {
				log.Printf("[douyin] 连续失败 %d 次，停止检查（可能触发风控）", consecutiveErrors)
				break
			}

			// 退避等待: 10s * 2^(errors-1)
			backoff := time.Duration(10*(1<<(consecutiveErrors-1))) * time.Second
			log.Printf("[douyin] 退避等待 %v 后重试", backoff)
			time.Sleep(backoff)
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
					log.Printf("[douyin] 创建 pending 记录失败 %s: %v", v.AwemeID, err)
				} else {
					pendingCreated++
				}
			}
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
