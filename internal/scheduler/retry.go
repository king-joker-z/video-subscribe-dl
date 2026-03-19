package scheduler

import (
	"log"
	"time"

	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/db"
)

// retryOneDownload 执行单个失败下载的重试，按平台类型分发。
// 调用前会二次确认 download 状态，避免并发重复处理已不是 pending/failed 的记录。
func (s *Scheduler) retryOneDownload(dl db.Download) {
	// 二次确认状态（避免并发重复处理）
	current, err := s.db.GetDownload(dl.ID)
	if err != nil || current == nil {
		return
	}
	if current.Status != "pending" && current.Status != "failed" {
		log.Printf("[retry] Download %d status is %s, skip", dl.ID, current.Status)
		return
	}

	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[retry-scheduler] Source %d not found for download %d, skipping", dl.SourceID, dl.ID)
		return
	}

	// 抖音类型委托给 dscheduler
	if src.Type == "douyin" || src.Type == "douyin_mix" {
		s.douyin.RetryDownload(dl)
		return
	}

	// B 站类型委托给 bscheduler
	if s.bili != nil {
		s.bili.RetryDownload(dl)
	}
}

// retryFailedDownloads 扫描失败下载并重试可重试的
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
	// 重置状态（包括 permanent_failed）和重试计数，确保 retryOneDownload 状态检查可通过
	s.db.ResetRetryCount(dlID)
	s.db.UpdateDownloadStatus(dlID, "failed", "", 0, "")
	// 重新读取，保证 retryOneDownload 拿到最新状态
	dl, err = s.db.GetDownload(dlID)
	if err != nil || dl == nil {
		return
	}
	// 用 wg 包装，确保 Stop() 时能优雅等待完成
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.retryOneDownload(*dl)
	}()
}

// RedownloadByID 重新下载指定记录（由 Web API redownload 调用）
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
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.retryOneDownload(*dl)
		log.Printf("[redownload] Submitted download %d (%s) for redownload", dlID, dl.VideoID)
	}()
}

// PauseDouyin / ResumeDouyin / IsDouyinPaused are defined in scheduler.go
