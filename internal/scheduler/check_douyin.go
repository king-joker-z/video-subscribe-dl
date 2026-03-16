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
func (s *Scheduler) checkDouyin(src db.Source) {
	client := douyin.NewClient()

	// 解析 sec_user_id
	secUID, err := s.resolveDouyinSecUID(client, src.URL)
	if err != nil {
		log.Printf("[douyin] 解析 sec_user_id 失败: %v", err)
		return
	}

	// 获取增量基准时间
	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	// 首次扫描页数限制
	firstScanPages := 0
	if val, err := s.db.GetSetting("first_scan_pages"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			firstScanPages = n
		}
	}

	if isFirstScan {
		log.Printf("[douyin·首次全量] %s: 开始全量扫描", src.Name)
		if firstScanPages > 0 {
			log.Printf("[douyin·首次全量] 页数限制: %d 页", firstScanPages)
		}
	} else {
		log.Printf("[douyin·增量] %s: 基准时间 %s",
			src.Name, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"))
	}

	var maxCursor int64
	totalNew := 0
	pendingCreated := 0
	var maxCreated int64
	stopped := false
	page := 0

	for {
		result, err := client.GetUserVideos(secUID, maxCursor)
		if err != nil {
			log.Printf("[douyin] 获取用户视频列表失败: %v", err)
			break
		}

		page++

		for _, v := range result.Videos {
			// 增量检查
			if !isFirstScan && v.CreateTime <= latestVideoAt {
				stopped = true
				break
			}

			if v.CreateTime > maxCreated {
				maxCreated = v.CreateTime
			}

			totalNew++

			// 创建 pending 下载记录
			exists, _ := s.db.IsVideoDownloaded(src.ID, v.AwemeID)
			if !exists {
				// 更新 source 名称（首次扫描可能为空）
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
					Duration:  v.Duration / 1000, // 毫秒转秒
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

		// 翻页间隔 5-8s（抖音风控更严格）
		time.Sleep(time.Duration(5000+rand.Intn(3000)) * time.Millisecond)
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
