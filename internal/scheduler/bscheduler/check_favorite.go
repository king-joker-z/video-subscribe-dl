package bscheduler

import (
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

// CheckFavorite 检查收藏夹源
func (s *BiliScheduler) CheckFavorite(src db.Source) {
	client := s.clientForSource(src)

	mid, mediaID, err := bilibili.ExtractFavoriteInfo(src.URL)
	if err != nil {
		log.Printf("[bscheduler] Extract favorite info failed: %v", err)
		return
	}

	if mediaID == 0 {
		folders, err := client.GetFavoriteList(mid)
		if err != nil {
			if bilibili.IsRiskControl(err) {
				log.Printf("[bscheduler] Get favorite list 风控: %v", err)
				return
			}
			log.Printf("[bscheduler] Get favorite list failed: %v", err)
			return
		}
		if len(folders) == 0 {
			log.Printf("[bscheduler] No favorites found for mid %d", mid)
			return
		}
		mediaID = folders[0].ID
		log.Printf("[bscheduler] Using default favorite: %s (id=%d)", folders[0].Title, mediaID)
	}

	upInfo, err := client.GetUPInfo(mid)
	if err != nil {
		log.Printf("[bscheduler] Get UP info failed (mid=%d): %v", mid, err)
		return
	}

	if (src.Name == "" || src.Name == "未命名") && upInfo.Name != "" {
		src.Name = upInfo.Name + " - 收藏夹"
		s.db.UpdateSource(&src)
	}

	if upInfo.Name != "" {
		s.db.UpsertPerson(fmt.Sprintf("%d", upInfo.MID), upInfo.Name, upInfo.Face)
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
		log.Printf("[bscheduler·favorite][首次全量] %s: 开始全量扫描", src.Name)
	} else {
		log.Printf("[bscheduler·favorite][增量] %s: 基准时间 %s",
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
			log.Printf("[bscheduler] Get favorite videos page %d failed: %v", page, err)
			break
		}

		for _, v := range videos {
			if v.BvID == "" {
				continue
			}
			totalFetched++

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
		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[bscheduler·favorite][首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}
		page++
		time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)
	}

	if maxPubDate > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxPubDate); err != nil {
			log.Printf("[bscheduler·favorite][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[bscheduler·favorite][首次全量] %s: %d 个视频 (翻页 %d)", src.Name, totalNew, page)
	} else if stopped {
		log.Printf("[bscheduler·favorite][增量] %s: %d 个新视频 (共检查 %d, 在第 %d 页停止)",
			src.Name, totalNew, totalFetched, page)
	} else {
		log.Printf("[bscheduler·favorite][增量] %s: %d 个新视频 (共检查 %d, 翻页 %d)",
			src.Name, totalNew, totalFetched, page)
	}
}
