package bscheduler

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/nfo"
)

// CheckSeason 检查合集源（独立合集 source）
func (s *BiliScheduler) CheckSeason(src db.Source) {
	client := s.clientForSource(src)

	mid, seasonID, err := bilibili.ExtractSeasonInfo(src.URL)
	if err != nil {
		log.Printf("[bscheduler] Extract season info failed: %v", err)
		return
	}

	upInfo, err := client.GetUPInfo(mid)
	if err != nil {
		log.Printf("[bscheduler] Get UP info failed (mid=%d): %v", mid, err)
		return
	}

	if upInfo.Name != "" {
		s.db.UpsertPerson(fmt.Sprintf("%d", upInfo.MID), upInfo.Name, upInfo.Face)
		s.ensurePeopleDir(upInfo)
	}

	uploaderName := upInfo.Name
	uploaderDir := bilibili.SanitizePath(uploaderName)

	s.fetchAndProcessSeason(src, client, mid, seasonID, uploaderName, uploaderDir, upInfo)
}

// fetchAndProcessSeason 统一处理合集的翻页、去重、目录创建、NFO、封面和视频遍历
func (s *BiliScheduler) fetchAndProcessSeason(src db.Source, client *bilibili.Client, mid, seasonID int64, uploaderName, uploaderDir string, upInfo *bilibili.UPInfo) {
	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	firstScanPages := 0
	if val, err := s.db.GetSetting("first_scan_pages"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			firstScanPages = n
		}
	}

	var allArchives []bilibili.SeasonArchive
	var meta *bilibili.SeasonMeta
	page := 1
	pageSize := 100
	totalChecked := 0
	var maxPubDate int64
	stopped := false

	for {
		archives, m, err := client.GetSeasonVideos(mid, seasonID, page, pageSize)
		if err != nil {
			log.Printf("[bscheduler] Get season %d page %d failed: %v", seasonID, page, err)
			break
		}
		if meta == nil {
			meta = m
		}

		for _, a := range archives {
			totalChecked++
			if !isFirstScan && a.PubDate <= latestVideoAt {
				stopped = true
				break
			}
			if a.PubDate > maxPubDate {
				maxPubDate = a.PubDate
			}
			allArchives = append(allArchives, a)
		}

		if stopped {
			break
		}
		if len(archives) < pageSize {
			break
		}
		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[bscheduler·season][首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}
		page++
		time.Sleep(time.Duration(300+rand.Intn(300)) * time.Millisecond)
	}

	if meta == nil {
		log.Printf("[bscheduler] Get season %d failed: no metadata", seasonID)
		return
	}

	if maxPubDate > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxPubDate); err != nil {
			log.Printf("[bscheduler·season][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if (src.Name == "" || src.Name == "未命名") && meta.Title != "" {
		src.Name = meta.Title
		s.db.UpdateSource(&src)
	}

	collectionName := bilibili.SanitizePath(meta.Title)
	collectionDir := filepath.Join(s.downloadDir, uploaderDir, collectionName)
	os.MkdirAll(collectionDir, 0755)

	if isFirstScan {
		log.Printf("[bscheduler·season][首次全量] %s by %s: %d 个视频 (翻页 %d)",
			meta.Title, uploaderName, len(allArchives), page)
	} else if stopped {
		log.Printf("[bscheduler·season][增量] %s by %s: %d 个新视频 (共检查 %d, 在第 %d 页停止)",
			meta.Title, uploaderName, len(allArchives), totalChecked, page)
	} else {
		log.Printf("[bscheduler·season][增量] %s by %s: %d 个新视频 (共检查 %d, 翻页 %d)",
			meta.Title, uploaderName, len(allArchives), totalChecked, page)
	}

	premiered := ""
	if len(allArchives) > 0 {
		premiered = time.Unix(allArchives[0].PubDate, 0).Format("2006-01-02")
	}
	if !src.SkipNFO {
		uploaderFace := ""
		if upInfo != nil {
			uploaderFace = upInfo.Face
		}
		nfo.GenerateTVShowNFO(&nfo.TVShowMeta{
			Title: meta.Title, Plot: meta.Intro, UploaderName: uploaderName,
			UploaderFace: uploaderFace, Premiered: premiered, Poster: meta.Cover,
		}, collectionDir)
	}

	if !src.SkipPoster && meta.Cover != "" {
		posterPath := filepath.Join(collectionDir, "poster.jpg")
		if _, err := os.Stat(posterPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(meta.Cover, posterPath); err != nil {
				log.Printf("[bscheduler] Collection poster download failed: %v", err)
			}
		}
	}

	for _, a := range allArchives {
		s.processOneVideo(src, client, a.BvID, a.Title, a.Pic, uploaderName, uploaderDir, collectionName, upInfo)
	}
}
