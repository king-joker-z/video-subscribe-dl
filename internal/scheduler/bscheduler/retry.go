package bscheduler

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/filter"
	"video-subscribe-dl/internal/nfo"
)

// retryOneDownload 执行单个 B站下载的重试
func (s *BiliScheduler) retryOneDownload(dl db.Download) {
	if s.dl != nil && s.dl.IsPaused() {
		log.Printf("[bscheduler] Downloader paused, skipping retry for %s", dl.VideoID)
		return
	}

	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[bscheduler] Source %d not found for download %d, skipping", dl.SourceID, dl.ID)
		return
	}
	if !src.Enabled {
		log.Printf("[bscheduler] Source %d (%s) is disabled, skipping download %s", src.ID, src.Name, dl.VideoID)
		return
	}

	s.downloadLimiter.Acquire()

	actualBvID := dl.VideoID
	var targetPageNum int
	if parts := strings.SplitN(dl.VideoID, "_P", 2); len(parts) == 2 {
		actualBvID = parts[0]
		fmt.Sscanf(parts[1], "%d", &targetPageNum)
	}

	client := s.clientForSource(*src)
	detail, err := client.GetVideoDetail(actualBvID)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			log.Printf("[bscheduler] 风控触发，停止重试: %s", dl.VideoID)
			s.TriggerCooldown()
			return
		}
		log.Printf("[bscheduler] Get detail failed for %s: %v", dl.VideoID, err)
		s.db.IncrementRetryCount(dl.ID, "retry: get detail failed: "+err.Error())
		return
	}

	tryUpower, _ := s.db.GetSetting("try_upower")
	if detail.IsChargePlus() && tryUpower != "true" {
		log.Printf("[bscheduler] 视频 %s (%s) 为充电专属/付费内容，更新为 charge_blocked", dl.Title, dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "charge_blocked", "", 0, "充电专属/付费视频")
		return
	}

	// 重新校验过滤规则（用户可能在失败后更新了订阅源的过滤条件）
	if src.DownloadFilter != "" && !filter.MatchesSimple(dl.Title, src.DownloadFilter) {
		log.Printf("[bscheduler] retry: %s 不匹配过滤规则 '%s'，跳过", dl.VideoID, src.DownloadFilter)
		s.db.UpdateDownloadStatus(dl.ID, "skipped", "", 0, "filter: not matching simple rule")
		return
	}
	if src.FilterRules != "" {
		advRules := filter.ParseRules(src.FilterRules)
		titleRules := make([]filter.Rule, 0, len(advRules))
		for _, r := range advRules {
			if r.Target == "title" {
				titleRules = append(titleRules, r)
			}
		}
		if len(titleRules) > 0 && !filter.MatchesRules(titleRules, filter.VideoInfo{Title: dl.Title}) {
			log.Printf("[bscheduler] retry: %s 不匹配高级过滤规则，跳过", dl.VideoID)
			s.db.UpdateDownloadStatus(dl.ID, "skipped", "", 0, "filter: not matching advanced rules")
			return
		}
	}

	var cid int64
	if targetPageNum > 0 {
		for _, p := range bilibili.GetAllPages(detail) {
			if p.Page == targetPageNum {
				cid = p.CID
				break
			}
		}
	} else {
		cid = bilibili.GetVideoCID(detail)
	}
	if cid == 0 {
		log.Printf("[bscheduler] No CID for %s, skipping retry", dl.VideoID)
		s.db.IncrementRetryCount(dl.ID, "retry: no CID available")
		return
	}

	s.db.UpdateDownloadStatus(dl.ID, "pending", "", 0, "")

	cookiesFile := src.CookiesFile
	if cookiesFile == "" {
		cookiesFile = s.cookiePath
	}

	mid, _ := bilibili.ExtractMID(src.URL)
	upInfo, _ := s.getUPInfoCached(client, mid)

	uploaderName := dl.Uploader
	if upInfo != nil && upInfo.Name != "" {
		uploaderName = upInfo.Name
	}
	uploaderDir := bilibili.SanitizePath(uploaderName)
	outputDir := filepath.Join(s.downloadDir, uploaderDir)

	isMultiPart := targetPageNum > 0
	flat := false
	var multiPartBase string
	if isMultiPart {
		videoTitle := detail.Title
		multiPartBase = filepath.Join(outputDir, bilibili.SanitizeFilename(videoTitle)+" ["+actualBvID+"]")
		outputDir = filepath.Join(multiPartBase, "Season 1")
		flat = true
		os.MkdirAll(outputDir, 0755)

		// tvshow.nfo + poster/fanart（幂等：文件已存在则跳过）
		if !src.SkipNFO {
			tvshowNFOPath := filepath.Join(multiPartBase, "tvshow.nfo")
			if _, err := os.Stat(tvshowNFOPath); os.IsNotExist(err) {
				uploaderFace := ""
				if upInfo != nil {
					uploaderFace = upInfo.Face
				} else {
					uploaderFace = detail.Owner.Face
				}
				premiered := ""
				if detail.PubDate > 0 {
					premiered = time.Unix(detail.PubDate, 0).Format("2006-01-02")
				}
				tags, _ := s.getBili().GetVideoTags(actualBvID)
				nfo.GenerateTVShowNFO(&nfo.TVShowMeta{
					Title:        detail.Title,
					Plot:         detail.Desc,
					UploaderName: uploaderName,
					UploaderFace: uploaderFace,
					Premiered:    premiered,
					Poster:       dl.Thumbnail,
					Tags:         tags,
				}, multiPartBase)
			}
		}
		if !src.SkipPoster && dl.Thumbnail != "" {
			posterPath := filepath.Join(multiPartBase, "poster.jpg")
			if _, err := os.Stat(posterPath); os.IsNotExist(err) {
				bilibili.DownloadFile(dl.Thumbnail, posterPath)
			}
			fanartPath := filepath.Join(multiPartBase, "fanart.jpg")
			if _, err := os.Stat(fanartPath); os.IsNotExist(err) {
				bilibili.DownloadFile(dl.Thumbnail, fanartPath)
			}
		}
	}

	var episodeMeta *nfo.EpisodeMeta
	if isMultiPart && targetPageNum > 0 {
		partName := ""
		for _, p := range bilibili.GetAllPages(detail) {
			if p.Page == targetPageNum {
				partName = p.PartName
				break
			}
		}
		episodeMeta = &nfo.EpisodeMeta{
			Title:        partName,
			Season:       1,
			Episode:      targetPageNum,
			BvID:         actualBvID,
			UploaderName: uploaderName,
		}
		if detail.PubDate > 0 {
			episodeMeta.UploadDate = time.Unix(detail.PubDate, 0)
		}
	}

	resultCh := make(chan *downloader.Result, 1)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.handleDownloadResult(dl.ID, dl.VideoID, detail, upInfo, resultCh, src.SkipNFO, src.SkipPoster, episodeMeta)
	}()

	capturedDlID := dl.ID
	s.dl.Submit(&downloader.Job{
		DownloadID:       capturedDlID,
		BvID:             actualBvID,
		CID:              cid,
		Title:            dl.Title,
		OutputDir:        outputDir,
		Quality:          src.DownloadQuality,
		QualityMin:       src.DownloadQualityMin,
		Codec:            src.DownloadCodec,
		Danmaku:          src.DownloadDanmaku,
		Subtitle:         src.DownloadSubtitle,
		SkipNFO:          src.SkipNFO,
		SkipPoster:       src.SkipPoster,
		Flat:             flat,
		UploaderName:     dl.Uploader,
		FilenameTemplate: s.getFilenameTemplate(),
		CookiesFile:      cookiesFile,
		ResultCh:         resultCh,
		OnStart:          func() { s.db.UpdateDownloadStatus(capturedDlID, "downloading", "", 0, "") },
	})

	log.Printf("[bscheduler] Resubmitted %s (retry #%d)", dl.VideoID, dl.RetryCount+1)
}

// RedownloadByID 重新下载指定记录
func (s *BiliScheduler) RedownloadByID(dlID int64) {
	dl, err := s.db.GetDownload(dlID)
	if err != nil || dl == nil {
		log.Printf("[bscheduler] Download %d not found", dlID)
		return
	}
	if dl.Status != "pending" {
		log.Printf("[bscheduler] Download %d status is %s, expected pending", dlID, dl.Status)
		return
	}
	if s.dl != nil && s.dl.IsPaused() {
		log.Printf("[bscheduler] Downloader paused, skipping redownload for %d", dlID)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.retryOneDownload(*dl)
	}()
}
