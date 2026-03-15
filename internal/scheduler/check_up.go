package scheduler

import (

	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

func (s *Scheduler) checkUP(src db.Source) {
	client := s.clientForSource(src)

	mid, err := bilibili.ExtractMID(src.URL)
	if err != nil {
		log.Printf("Extract MID failed: %v", err)
		return
	}

	// UP 主信息
	upInfo, err := client.GetUPInfo(mid)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.triggerCooldown()
			s.dl.Pause()
		} else {
			log.Printf("Get UP info failed (mid=%d): %v", mid, err)
		}
		return
	}

	if (src.Name == "" || src.Name == "未命名") && upInfo.Name != "" {
		src.Name = upInfo.Name
		s.db.UpdateSource(&src)
	}

	if upInfo.Name != "" {
		s.db.UpsertPerson(fmt.Sprintf("%d", upInfo.MID), upInfo.Name, upInfo.Face)
		s.ensurePeopleDir(upInfo)
	}

	uploaderName := upInfo.Name
	uploaderDir := bilibili.SanitizePath(uploaderName)

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
		log.Printf("[首次全量] %s (mid=%d): 开始全量扫描", uploaderName, mid)
		if firstScanPages > 0 {
			log.Printf("[首次全量] 页数限制: %d 页", firstScanPages)
		}
	} else {
		log.Printf("[增量] %s (mid=%d): 基准时间 %s",
			uploaderName, mid, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"))
	}

	pageSize := 30
	page := 1
	processedSeasons := map[int64]bool{}
	processedBVIDs := map[string]bool{}
	totalFetched := 0
	totalNew := 0
	var maxCreated int64
	stopped := false

	for {
		videos, total, err := client.GetUPVideos(mid, page, pageSize)
		if err != nil {
			if bilibili.IsRiskControl(err) {
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("Get videos page %d failed: %v", page, err)
			break
		}
		if page == 1 {
			if isFirstScan {
				log.Printf("[首次全量] %s: 共 %d 个视频", uploaderName, total)
			} else {
				log.Printf("[增量] %s: 共 %d 个视频，增量检查中...", uploaderName, total)
			}
		}

		for _, v := range videos {
			// 增量检查: 视频发布时间 <= latestVideoAt 则停止（后面都是旧视频）
			if !isFirstScan && v.Created <= latestVideoAt {
				stopped = true
				break
			}

			// 追踪本轮最大 created 时间
			if v.Created > maxCreated {
				maxCreated = v.Created
			}

			totalNew++

			if v.IsSeason && v.SeasonID > 0 && !processedSeasons[v.SeasonID] {
				processedSeasons[v.SeasonID] = true
				s.processCollection(src, client, mid, v.SeasonID, uploaderName, uploaderDir, upInfo)
			}
			if v.IsSeason {
				processedBVIDs[v.BvID] = true
				continue
			}
			if processedBVIDs[v.BvID] {
				continue
			}
			processedBVIDs[v.BvID] = true
			s.processOneVideo(src, client, v.BvID, v.Title, v.Pic, uploaderName, uploaderDir, "", upInfo)
		}

		totalFetched += len(videos)

		if stopped {
			break
		}

		if totalFetched >= total || len(videos) < pageSize {
			break
		}

		// 首次扫描页数限制
		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}

		page++
		time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)
	}

	// 更新 latest_video_at
	if maxCreated > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[首次全量] %s: 获取 %d 个新视频 (共检查 %d, 翻页 %d)",
			uploaderName, totalNew, totalFetched, page)
	} else if stopped {
		log.Printf("[增量] %s: 获取 %d 个新视频 (共检查 %d, 在第 %d 页停止)",
			uploaderName, totalNew, totalFetched, page)
	} else {
		log.Printf("[增量] %s: 获取 %d 个新视频 (共检查 %d, 翻页 %d)",
			uploaderName, totalNew, totalFetched, page)
	}
}
