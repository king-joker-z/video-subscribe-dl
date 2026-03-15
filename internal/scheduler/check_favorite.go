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

// checkFavorite 检查收藏夹源，翻页获取所有视频（支持增量）
func (s *Scheduler) checkFavorite(src db.Source) {
	client := s.clientForSource(src)

	mid, mediaID, err := bilibili.ExtractFavoriteInfo(src.URL)
	if err != nil {
		log.Printf("Extract favorite info failed: %v", err)
		return
	}

	// 如果 mediaID 为空但 URL 里有 fid，尝试解析
	if mediaID == 0 {
		folders, err := client.GetFavoriteList(mid)
		if err != nil {
			if bilibili.IsRiskControl(err) {
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("Get favorite list failed: %v", err)
			return
		}
		if len(folders) == 0 {
			log.Printf("No favorites found for mid %d", mid)
			return
		}
		mediaID = folders[0].ID
		log.Printf("Using default favorite: %s (id=%d)", folders[0].Title, mediaID)
	}

	// 获取 UP 主信息用于命名目录
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
		src.Name = upInfo.Name + " - 收藏夹"
		s.db.UpdateSource(&src)
	}

	if upInfo.Name != "" {
		s.db.UpsertPerson(fmt.Sprintf("%d", upInfo.MID), upInfo.Name, upInfo.Face)
	}

	// 增量基准
	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	firstScanPages := 0
	if val, err := s.db.GetSetting("first_scan_pages"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			firstScanPages = n
		}
	}

	if isFirstScan {
		log.Printf("[favorite][首次全量] %s: 开始全量扫描", src.Name)
	} else {
		log.Printf("[favorite][增量] %s: 基准时间 %s",
			src.Name, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"))
	}

	pageSize := 20
	page := 1
	totalFetched := 0
	totalNew := 0
	var maxPubDate int64
	stopped := false

	for {
		videos, hasMore, err := client.GetFavoriteVideos(mediaID, page, pageSize)
		if err != nil {
			if bilibili.IsRiskControl(err) {
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("Get favorite videos page %d failed: %v", page, err)
			break
		}
		if page == 1 {
			if isFirstScan {
				log.Printf("[favorite][首次全量] 翻页获取中 (page %d, got %d)...", page, len(videos))
			}
		}

		for _, v := range videos {
			if v.BvID == "" {
				continue
			}

			totalFetched++

			// 增量检查：收藏夹视频按收藏时间倒序，PubDate 为发布时间
			if !isFirstScan && v.PubDate <= latestVideoAt {
				stopped = true
				break
			}

			if v.PubDate > maxPubDate {
				maxPubDate = v.PubDate
			}

			totalNew++

			uploaderName := v.Owner.Name
			uploaderDir := bilibili.SanitizePath(uploaderName)
			ownerInfo := &bilibili.UPInfo{MID: v.Owner.MID, Name: v.Owner.Name, Face: v.Owner.Face}
			if ownerInfo.Name != "" {
				s.db.UpsertPerson(fmt.Sprintf("%d", ownerInfo.MID), ownerInfo.Name, ownerInfo.Face)
				s.ensurePeopleDir(ownerInfo)
			}
			s.processOneVideo(src, client, v.BvID, v.Title, v.Pic, uploaderName, uploaderDir, "", ownerInfo)
		}

		if stopped {
			break
		}
		if !hasMore || len(videos) < pageSize {
			break
		}

		// 首次扫描页数限制
		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[favorite][首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}

		page++
		time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)
	}

	// 更新 latest_video_at
	if maxPubDate > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxPubDate); err != nil {
			log.Printf("[favorite][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[favorite][首次全量] %s: %d 个视频 (翻页 %d)", src.Name, totalNew, page)
	} else if stopped {
		log.Printf("[favorite][增量] %s: %d 个新视频 (共检查 %d, 在第 %d 页停止)",
			src.Name, totalNew, totalFetched, page)
	} else {
		log.Printf("[favorite][增量] %s: %d 个新视频 (共检查 %d, 翻页 %d)",
			src.Name, totalNew, totalFetched, page)
	}
}
