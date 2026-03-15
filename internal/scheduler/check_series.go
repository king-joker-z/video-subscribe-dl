package scheduler

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

// checkSeries 处理 Series（视频列表）类型合集
func (s *Scheduler) checkSeries(src db.Source) {
	mid, seriesID, err := bilibili.ExtractSeasonInfo(src.URL)
	if err != nil {
		// 尝试用 ExtractCollectionInfo
		info, err2 := bilibili.ExtractCollectionInfo(src.URL)
		if err2 != nil {
			log.Printf("[series] Cannot parse URL: %s, err: %v", src.URL, err)
			return
		}
		mid = info.MID
		seriesID = info.ID
	}

	client := s.clientForSource(src)

	upInfo, err := client.GetUPInfo(mid)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.triggerCooldown()
			s.dl.Pause()
			return
		}
		log.Printf("[series] Get UP info failed: %v", err)
		return
	}

	s.ensurePeopleDir(upInfo)

	uploaderName := upInfo.Name
	uploaderDir := bilibili.SanitizePath(uploaderName)

	// 获取 Series 信息
	seriesMeta, err := client.GetSeriesInfo(mid, seriesID)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.triggerCooldown()
			s.dl.Pause()
			return
		}
		log.Printf("[series] Get series info failed: %v", err)
		return
	}

	collectionName := bilibili.SanitizePath(seriesMeta.Name)
	collectionDir := filepath.Join(s.downloadDir, uploaderDir, collectionName)
	os.MkdirAll(collectionDir, 0755)

	// 增量基准时间
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
		log.Printf("[series][首次全量] %s: 开始全量扫描", collectionName)
	} else {
		log.Printf("[series][增量] %s: 基准时间 %s",
			collectionName, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"))
	}

	// 增量模式下按倒序（desc）翻页，首次全量按正序（asc）
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
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("[series] Get series page %d failed: %v", page, err)
			break
		}

		for _, a := range archives {
			totalChecked++

			// 增量检查
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

		// 首次扫描页数限制
		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[series][首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}

		page++
		time.Sleep(time.Duration(300+rand.Intn(300)) * time.Millisecond)
	}

	// 更新 latest_video_at
	if maxPubDate > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxPubDate); err != nil {
			log.Printf("[series][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[series][首次全量] %s: %d 个视频 (翻页 %d)", collectionName, len(allArchives), page)
	} else if stopped {
		log.Printf("[series][增量] %s: %d 个新视频 (共检查 %d, 在第 %d 页停止)",
			collectionName, len(allArchives), totalChecked, page)
	} else {
		log.Printf("[series][增量] %s: %d 个新视频 (共检查 %d, 翻页 %d)",
			collectionName, len(allArchives), totalChecked, page)
	}

	// 生成 tvshow.nfo（使用最新的完整列表信息）
	premiered := ""
	if len(allArchives) > 0 {
		premiered = time.Unix(allArchives[0].PubDate, 0).Format("2006-01-02")
	}
	nfo.GenerateTVShowNFO(&nfo.TVShowMeta{
		Title: seriesMeta.Name, Plot: seriesMeta.Description, UploaderName: uploaderName,
		UploaderFace: upInfo.Face, Premiered: premiered,
	}, collectionDir)

	// Series 封面: 使用第一个视频的封面
	if len(allArchives) > 0 && allArchives[0].Pic != "" {
		posterPath := filepath.Join(collectionDir, "poster.jpg")
		if _, err := os.Stat(posterPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(allArchives[0].Pic, posterPath); err != nil {
				log.Printf("[series] Poster download failed: %v", err)
			}
		}
	}

	for _, a := range allArchives {
		s.processOneVideo(src, client, a.BvID, a.Title, a.Pic, uploaderName, uploaderDir, collectionName, upInfo)
	}
}
