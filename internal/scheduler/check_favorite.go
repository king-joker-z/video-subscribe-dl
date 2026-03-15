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

// checkFavorite 检查收藏夹源，翻页获取所有视频
func (s *Scheduler) checkFavorite(src db.Source) {
	client := s.clientForSource(src)

	mid, mediaID, err := bilibili.ExtractFavoriteInfo(src.URL)
	if err != nil {
		log.Printf("Extract favorite info failed: %v", err)
		return
	}

	// 如果 mediaID 为空但 URL 里有 fid，尝试解析
	if mediaID == 0 {
		// 可能只有 mid，获取所有收藏夹，取第一个（默认收藏夹）
		folders, err := client.GetFavoriteList(mid)
		if err != nil {
			if errors.Is(err, bilibili.ErrRateLimited) {
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
		if errors.Is(err, bilibili.ErrRateLimited) {
			s.triggerCooldown()
			s.dl.Pause()
			return
		}
		log.Printf("Get UP info failed (mid=%d): %v", mid, err)
		upInfo = &bilibili.UPInfo{MID: mid, Name: src.Name}
	}

	if (src.Name == "" || src.Name == "未命名") && upInfo.Name != "" {
		src.Name = upInfo.Name + " - 收藏夹"
		s.db.UpdateSource(&src)
	}

	if upInfo.Name != "" {
		s.db.UpsertPerson(fmt.Sprintf("%d", upInfo.MID), upInfo.Name, upInfo.Face)
	}

	// 翻页获取收藏夹内所有视频
	pageSize := 20
	page := 1
	totalFetched := 0

	for {
		videos, hasMore, err := client.GetFavoriteVideos(mediaID, page, pageSize)
		if err != nil {
			if errors.Is(err, bilibili.ErrRateLimited) {
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("Get favorite videos page %d failed: %v", page, err)
			break
		}
		if page == 1 {
			log.Printf("Favorite: fetching videos (page %d, got %d)...", page, len(videos))
		}

		for _, v := range videos {
			if v.BvID == "" {
				continue // 已失效的视频
			}
			uploaderName := v.Owner.Name
			uploaderDir := bilibili.SanitizePath(uploaderName)
			ownerInfo := &bilibili.UPInfo{MID: v.Owner.MID, Name: v.Owner.Name, Face: v.Owner.Face}
			if ownerInfo.Name != "" {
				s.db.UpsertPerson(fmt.Sprintf("%d", ownerInfo.MID), ownerInfo.Name, ownerInfo.Face)
				s.ensurePeopleDir(ownerInfo)
			}
			s.processOneVideo(src, client, v.BvID, v.Title, v.Pic, uploaderName, uploaderDir, "", ownerInfo)
		}

		totalFetched += len(videos)
		if !hasMore || len(videos) < pageSize {
			break
		}
		page++
		time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)
	}
	log.Printf("Favorite: fetched %d videos (pages: %d)", totalFetched, page)
}
