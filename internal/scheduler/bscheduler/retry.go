package bscheduler

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/nfo"
)

// retryOneDownload 执行单个 B站下载的重试
func (s *BiliScheduler) retryOneDownload(dl db.Download) {
	if s.dl != nil && s.dl.IsPaused() {
		if !s.IsInCooldown() {
			s.dl.Resume()
			log.Printf("[bscheduler] 风控冷却结束，恢复下载器")
		} else {
			s.rateLimitMu.Lock()
			until := s.cooldownUntil
			s.rateLimitMu.Unlock()
			log.Printf("[bscheduler] Downloader paused (cooldown until %s), skipping retry for %s",
				until.Format("15:04:05"), dl.VideoID)
			return
		}
	}

	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[bscheduler] Source %d not found for download %d, skipping", dl.SourceID, dl.ID)
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
	if isMultiPart {
		videoTitle := detail.Title
		multiPartBase := filepath.Join(outputDir, bilibili.SanitizeFilename(videoTitle)+" ["+actualBvID+"]")
		outputDir = filepath.Join(multiPartBase, "Season 1")
		flat = true
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

// RetryFailedDownloads 扫描失败下载并重试可重试的
func (s *BiliScheduler) RetryFailedDownloads() {
	const maxPerCycle = 5

	marked, err := s.db.MarkPermanentFailed(config.MaxRetryCount)
	if err != nil {
		log.Printf("[bscheduler] Mark permanent failed error: %v", err)
	} else if marked > 0 {
		log.Printf("[bscheduler] Marked %d downloads as permanent_failed", marked)
	}

	retryable, err := s.db.GetRetryableDownloads(config.MaxRetryCount, maxPerCycle)
	if err != nil {
		log.Printf("[bscheduler] Get retryable downloads error: %v", err)
		return
	}

	if len(retryable) == 0 {
		return
	}

	log.Printf("[bscheduler] Found %d retryable failed downloads", len(retryable))

	for _, dl := range retryable {
		if s.dl != nil && s.dl.IsPaused() {
			if !s.IsInCooldown() {
				s.dl.Resume()
				log.Printf("[bscheduler] 风控冷却结束，恢复下载器")
			} else {
				log.Printf("[bscheduler] Downloader paused (cooldown), stopping retry cycle")
				return
			}
		}
		s.retryOneDownload(dl)
		time.Sleep(2 * time.Second)
	}
}

// RetryByID 手动重试指定下载记录
func (s *BiliScheduler) RetryByID(dlID int64) {
	dl, err := s.db.GetDownload(dlID)
	if err != nil || dl == nil {
		log.Printf("[bscheduler] Download %d not found", dlID)
		return
	}
	s.db.ResetRetryCount(dlID)
	s.retryOneDownload(*dl)
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
		if !s.IsInCooldown() {
			s.dl.Resume()
			log.Printf("[bscheduler] 风控冷却结束，恢复下载器")
		} else {
			s.rateLimitMu.Lock()
			until := s.cooldownUntil
			s.rateLimitMu.Unlock()
			log.Printf("[bscheduler] Downloader paused (cooldown until %s), skipping", until.Format("15:04:05"))
			return
		}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.retryOneDownload(*dl)
	}()
}
