package bscheduler

import (
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

// CheckSeries 处理 Series（视频列表）类型合集
func (s *BiliScheduler) CheckSeries(src db.Source) {
	mid, seriesID, err := bilibili.ExtractSeasonInfo(src.URL)
	if err != nil {
		info, err2 := bilibili.ExtractCollectionInfo(src.URL)
		if err2 != nil {
			log.Printf("[bscheduler·series] Cannot parse URL: %s, err: %v", src.URL, err)
			return
		}
		mid = info.MID
		seriesID = info.ID
	}

	client := s.clientForSource(src)

	upInfo, err := client.GetUPInfo(mid)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.TriggerCooldown()
			return
		}
		log.Printf("[bscheduler·series] Get UP info failed: %v", err)
		return
	}

	s.ensurePeopleDir(upInfo)

	uploaderName := upInfo.Name
	uploaderDir := bilibili.SanitizePath(uploaderName)

	seriesMeta, err := client.GetSeriesInfo(mid, seriesID)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.TriggerCooldown()
			return
		}
		log.Printf("[bscheduler·series] Get series info failed: %v", err)
		return
	}

	collectionName := bilibili.SanitizePath(seriesMeta.Name)
	collectionDir := filepath.Join(s.downloadDir, uploaderDir, collectionName)
	os.MkdirAll(collectionDir, 0755)

	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	firstScanPages := 0
	if val, err := s.db.GetSetting("first_scan_pages"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			firstScanPages = n
		}
	}

	sortOrder := "desc"
	if isFirstScan {
		sortOrder = "asc"
	}

	var allArchives []bilibili.SeriesArchive
	page := 1
	pageSize := 100
	totalChecked := 0
	var maxPubDate int64
	stopped := false

	for {
		archives, _, err := client.GetSeriesVideosSorted(mid, seriesID, page, pageSize, sortOrder)
		if err != nil {
			if bilibili.IsRiskControl(err) {
				s.TriggerCooldown()
				return
			}
			log.Printf("[bscheduler·series] Get series page %d failed: %v", page, err)
			break
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
			log.Printf("[bscheduler·series][首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}
		page++
		time.Sleep(time.Duration(300+rand.Intn(300)) * time.Millisecond)
	}

	if maxPubDate > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxPubDate); err != nil {
			log.Printf("[bscheduler·series][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[bscheduler·series][首次全量] %s: %d 个视频 (翻页 %d)", collectionName, len(allArchives), page)
	} else if stopped {
		log.Printf("[bscheduler·series][增量] %s: %d 个新视频 (共检查 %d, 在第 %d 页停止)",
			collectionName, len(allArchives), totalChecked, page)
	} else {
		log.Printf("[bscheduler·series][增量] %s: %d 个新视频 (共检查 %d, 翻页 %d)",
			collectionName, len(allArchives), totalChecked, page)
	}

	premiered := ""
	if len(allArchives) > 0 {
		premiered = time.Unix(allArchives[0].PubDate, 0).Format("2006-01-02")
	}
	nfo.GenerateTVShowNFO(&nfo.TVShowMeta{
		Title: seriesMeta.Name, Plot: seriesMeta.Description, UploaderName: uploaderName,
		UploaderFace: upInfo.Face, Premiered: premiered,
	}, collectionDir)

	if len(allArchives) > 0 && allArchives[0].Pic != "" {
		posterPath := filepath.Join(collectionDir, "poster.jpg")
		if _, err := os.Stat(posterPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(allArchives[0].Pic, posterPath); err != nil {
				log.Printf("[bscheduler·series] Poster download failed: %v", err)
			}
		}
	}

	for _, a := range allArchives {
		s.processOneVideo(src, client, a.BvID, a.Title, a.Pic, uploaderName, uploaderDir, collectionName, upInfo)
	}
}
