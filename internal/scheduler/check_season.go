package scheduler

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/nfo"
)

// checkSeason 检查合集源，获取合集所有视频
func (s *Scheduler) checkSeason(src db.Source) {
	client := s.clientForSource(src)

	mid, seasonID, err := bilibili.ExtractSeasonInfo(src.URL)
	if err != nil {
		log.Printf("Extract season info failed: %v", err)
		return
	}

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

	if upInfo.Name != "" {
		s.db.UpsertPerson(fmt.Sprintf("%d", upInfo.MID), upInfo.Name, upInfo.Face)
		s.ensurePeopleDir(upInfo)
	}

	uploaderName := upInfo.Name
	uploaderDir := bilibili.SanitizePath(uploaderName)

	var allArchives []bilibili.SeasonArchive
	var meta *bilibili.SeasonMeta
	page := 1
	pageSize := 100
	for {
		archives, m, err := client.GetSeasonVideos(mid, seasonID, page, pageSize)
		if err != nil {
			if errors.Is(err, bilibili.ErrRateLimited) {
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("Get season %d page %d failed: %v", seasonID, page, err)
			break
		}
		if meta == nil {
			meta = m
		}
		allArchives = append(allArchives, archives...)
		if len(archives) < pageSize {
			break
		}
		page++
		time.Sleep(time.Duration(300+rand.Intn(300)) * time.Millisecond)
	}

	if meta == nil {
		log.Printf("Get season %d failed: no metadata", seasonID)
		return
	}

	if (src.Name == "" || src.Name == "未命名") && meta.Title != "" {
		src.Name = meta.Title
		s.db.UpdateSource(&src)
	}

	collectionName := bilibili.SanitizePath(meta.Title)
	collectionDir := filepath.Join(s.downloadDir, uploaderDir, collectionName)
	os.MkdirAll(collectionDir, 0755)

	log.Printf("Season: %s by %s (%d videos)", meta.Title, uploaderName, len(allArchives))

	premiered := ""
	if len(allArchives) > 0 {
		premiered = time.Unix(allArchives[0].PubDate, 0).Format("2006-01-02")
	}
	nfo.GenerateTVShowNFO(&nfo.TVShowMeta{
		Title: meta.Title, Plot: meta.Intro, UploaderName: uploaderName,
		UploaderFace: upInfo.Face, Premiered: premiered, Poster: meta.Cover,
	}, collectionDir)

	if meta.Cover != "" {
		posterPath := filepath.Join(collectionDir, "poster.jpg")
		if _, err := os.Stat(posterPath); os.IsNotExist(err) {
			bilibili.DownloadFile(meta.Cover, posterPath)
		}
	}

	for _, a := range allArchives {
		s.processOneVideo(src, client, a.BvID, a.Title, a.Pic, uploaderName, uploaderDir, collectionName, upInfo)
	}

	log.Printf("Season: fetched %d videos (pages: %d)", len(allArchives), page)
}
