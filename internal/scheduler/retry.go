package scheduler

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

// retryOneDownload 执行单个失败下载的重试
func (s *Scheduler) retryOneDownload(dl db.Download) {
	// 暂停时检查冷却是否已过期
	if s.dl.IsPaused() {
		if !s.isInCooldown() {
			s.dl.Resume()
			log.Printf("[retry-scheduler] 风控冷却结束，恢复下载器")
		} else {
			s.rateLimitMu.Lock()
			until := s.rateLimitUntil
			s.rateLimitMu.Unlock()
			log.Printf("[retry-scheduler] Downloader paused (cooldown until %s), skipping retry for %s", until.Format("15:04:05"), dl.VideoID)
			return
		}
	}

	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[retry-scheduler] Source %d not found for download %d, skipping", dl.SourceID, dl.ID)
		return
	}

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
			log.Printf("[retry-scheduler] 风控触发，停止重试: %s", dl.VideoID)
			s.triggerCooldown()
			s.dl.Pause()
			return
		}
		log.Printf("[retry-scheduler] Get detail failed for %s: %v", dl.VideoID, err)
		s.db.IncrementRetryCount(dl.ID, "retry: get detail failed: "+err.Error())
		return
	}

	// 充电专属检测
	tryUpower, _ := s.db.GetSetting("try_upower")
	if detail.IsChargePlus() && tryUpower != "true" {
		log.Printf("[retry-scheduler] 视频 %s (%s) 为充电专属/付费内容，更新为 charge_blocked (try_upower=false)", dl.Title, dl.VideoID)
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
		log.Printf("[retry-scheduler] No CID for %s, skipping retry", dl.VideoID)
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

	// 从 upInfo 获取 UP主名（和正常下载流程一致），不用 dl.Uploader（可能被污染）
	uploaderName := dl.Uploader
	if upInfo != nil && upInfo.Name != "" {
		uploaderName = upInfo.Name
	}
	uploaderDir := bilibili.SanitizePath(uploaderName)
	outputDir := filepath.Join(s.downloadDir, uploaderDir)

	// 多P视频重试时还原完整路径: UP主/视频标题 [BVxxx]/Season 1/
	isMultiPart := targetPageNum > 0
	flat := false
	if isMultiPart {
		videoTitle := detail.Title
		multiPartBase := filepath.Join(outputDir, bilibili.SanitizeFilename(videoTitle)+" ["+actualBvID+"]")
		outputDir = filepath.Join(multiPartBase, "Season 1")
		flat = true
	}

	// 多P重试时构造 episodeMeta
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

	log.Printf("[retry-scheduler] Resubmitted %s (retry #%d)", dl.VideoID, dl.RetryCount+1)
}

// retryFailedDownloads scans failed downloads and resubmits retryable ones
func (s *Scheduler) retryFailedDownloads() {
	const maxPerCycle = 5

	marked, err := s.db.MarkPermanentFailed(config.MaxRetryCount)
	if err != nil {
		log.Printf("[retry-scheduler] Mark permanent failed error: %v", err)
	} else if marked > 0 {
		log.Printf("[retry-scheduler] Marked %d downloads as permanent_failed", marked)
	}

	retryable, err := s.db.GetRetryableDownloads(config.MaxRetryCount, maxPerCycle)
	if err != nil {
		log.Printf("[retry-scheduler] Get retryable downloads error: %v", err)
		return
	}

	if len(retryable) == 0 {
		return
	}

	log.Printf("[retry-scheduler] Found %d retryable failed downloads", len(retryable))

	for _, dl := range retryable {
		// 暂停时检查冷却是否已过期
		if s.dl.IsPaused() {
			if !s.isInCooldown() {
				s.dl.Resume()
				log.Printf("[retry-scheduler] 风控冷却结束，恢复下载器")
			} else {
				log.Printf("[retry-scheduler] Downloader paused (cooldown), stopping retry cycle")
				return
			}
		}
		s.retryOneDownload(dl)
		time.Sleep(2 * time.Second)
	}
}

// RetryByID 手动重试指定下载记录（由 Web API 调用）
func (s *Scheduler) RetryByID(dlID int64) {
	dl, err := s.db.GetDownload(dlID)
	if err != nil || dl == nil {
		log.Printf("[manual-retry] Download %d not found", dlID)
		return
	}
	// 重置状态和重试计数
	s.db.ResetRetryCount(dlID)
	s.retryOneDownload(*dl)
}

// RedownloadByID 重新下载指定记录（由 Web API redownload 调用）
// 和 RetryByID 类似，但不检查状态，直接根据 pending 记录重新获取视频信息并提交下载
func (s *Scheduler) RedownloadByID(dlID int64) {
	dl, err := s.db.GetDownload(dlID)
	if err != nil || dl == nil {
		log.Printf("[redownload] Download %d not found", dlID)
		return
	}
	if dl.Status != "pending" {
		log.Printf("[redownload] Download %d status is %s, expected pending", dlID, dl.Status)
		return
	}
	// 暂停时检查冷却是否已过期
	if s.dl.IsPaused() {
		if !s.isInCooldown() {
			s.dl.Resume()
			log.Printf("[redownload] 风控冷却结束，恢复下载器")
		} else {
			s.rateLimitMu.Lock()
			until := s.rateLimitUntil
			s.rateLimitMu.Unlock()
			log.Printf("[redownload] Downloader paused (cooldown until %s), skipping", until.Format("15:04:05"))
			return
		}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.retryOneDownload(*dl)
		log.Printf("[redownload] Submitted download %d (%s) for redownload", dlID, dl.VideoID)
	}()
}
