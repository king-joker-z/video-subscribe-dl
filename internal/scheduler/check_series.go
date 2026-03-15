package scheduler

import (
	"errors"
	"log"
	"math/rand"
	"os"
	"path/filepath"
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
		if errors.Is(err, bilibili.ErrRateLimited) {
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
		if errors.Is(err, bilibili.ErrRateLimited) {
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

	// 全量翻页获取所有视频
	var allArchives []bilibili.SeriesArchive
	page := 1
	pageSize := 100
	for {
		archives, _, err := client.GetSeriesVideos(mid, seriesID, page, pageSize)
		if err != nil {
			if errors.Is(err, bilibili.ErrRateLimited) {
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("[series] Get series page %d failed: %v", page, err)
			break
		}
		allArchives = append(allArchives, archives...)
		if len(archives) < pageSize {
			break
		}
		page++
		time.Sleep(time.Duration(300+rand.Intn(300)) * time.Millisecond)
	}

	log.Printf("[series] %s: %d videos", collectionName, len(allArchives))

	// 生成 tvshow.nfo
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
