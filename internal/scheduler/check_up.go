package scheduler

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
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
		if errors.Is(err, bilibili.ErrRateLimited) {
			s.triggerCooldown()
			s.dl.Pause()
			return
		}
		log.Printf("Get UP info failed (mid=%d): %v", mid, err)
		upInfo = &bilibili.UPInfo{MID: mid, Name: src.Name}
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

	// 全量翻页获取 UP 主所有视频
	pageSize := 30
	page := 1
	processedSeasons := map[int64]bool{}
	processedBVIDs := map[string]bool{}  // 防止合集视频和普通投稿重复处理
	totalFetched := 0

	for {
		videos, total, err := client.GetUPVideos(mid, page, pageSize)
		if err != nil {
			if errors.Is(err, bilibili.ErrRateLimited) {
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("Get videos page %d failed: %v", page, err)
			break
		}
		if page == 1 {
			log.Printf("Total %d videos for %s, fetching all pages...", total, uploaderName)
		}

		for _, v := range videos {
			if v.IsSeason && v.SeasonID > 0 && !processedSeasons[v.SeasonID] {
				processedSeasons[v.SeasonID] = true
				s.processCollection(src, client, mid, v.SeasonID, uploaderName, uploaderDir, upInfo)
			}
			// 标记属于合集的视频 BV 号，避免后续作为普通投稿重复处理
			if v.IsSeason {
				processedBVIDs[v.BvID] = true
				continue
			}
			// 普通投稿：跳过已在合集中处理过的
			if processedBVIDs[v.BvID] {
				continue
			}
			processedBVIDs[v.BvID] = true
			s.processOneVideo(src, client, v.BvID, v.Title, v.Pic, uploaderName, uploaderDir, "", upInfo)
		}

		totalFetched += len(videos)
		if totalFetched >= total || len(videos) < pageSize {
			break
		}
		page++

		// 防封：翻页间隔
		time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)
	}
	log.Printf("Fetched %d videos for %s (pages: %d)", totalFetched, uploaderName, page)
}
