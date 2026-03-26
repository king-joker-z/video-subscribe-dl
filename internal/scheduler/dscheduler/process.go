package dscheduler

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
)

// RetryOneDownload 执行单个抖音下载
func (s *DouyinScheduler) RetryOneDownload(dl db.Download) {
	if s.IsPaused() {
		log.Printf("[dscheduler] 抖音下载已暂停，跳过 %s", dl.VideoID)
		return
	}

	s.downloadLimiter.Acquire()

	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[dscheduler] Source %d not found for download %d, skipping", dl.SourceID, dl.ID)
		return
	}
	if !src.Enabled {
		log.Printf("[dscheduler] Source %d (%s) is disabled, marking download %s as skipped", src.ID, src.Name, dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "skipped", "", 0, "skipped: source disabled")
		return
	}

	client := s.newClient()
	defer client.Close()

	s.db.UpdateDownloadStatus(dl.ID, "downloading", "", 0, "")
	// 广播 started 事件，让前端立即将状态从 pending 更新为 downloading
	s.emitEvent(DownloadEvent{
		Type:    "started",
		VideoID: dl.VideoID,
		Title:   dl.Title,
	})

	var detail *douyin.DouyinVideo
	for attempt := 1; attempt <= 3; attempt++ {
		detail, err = client.GetVideoDetail(dl.VideoID)
		if err == nil {
			break
		}
		log.Printf("[dscheduler] GetVideoDetail attempt %d failed for %s: %v", attempt, dl.VideoID, err)
		if attempt < 3 {
			backoff := time.Duration(5*(1<<(attempt-1))) * time.Second
			time.Sleep(backoff)
		}
	}
	if err != nil {
		if errors.Is(err, douyin.ErrDouyinRiskControl) {
			reason := fmt.Sprintf("风控触发: %v", err)
			s.Pause(reason)
			s.notifier.Send(notify.EventRateLimited, "抖音风控触发",
				"抖音下载已暂停，请在 Web UI 手动恢复\n错误: "+err.Error())
			s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
			s.db.IncrementRetryCount(dl.ID, err.Error())
			return
		}
		log.Printf("[dscheduler] GetVideoDetail failed after retries for %s: %v", dl.VideoID, err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	// 图集下载
	if detail.IsNote && len(detail.Images) > 0 {
		s.downloadDouyinNote(dl, *src, detail)
		return
	}

	if detail.VideoURL == "" {
		log.Printf("[dscheduler] Skipping post %s: no video URL and no images", dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "skipped", "", 0, "skipped: no downloadable content")
		return
	}

	// page scrape 路径已在 getVideoDetailPage 内部跟过 302（URLResolved=true），无需再次 resolve
	// API 路径返回的 VideoURL 是 CDN 间接地址，需要跟一次 302 获取直链
	videoURL := detail.VideoURL
	if !detail.URLResolved {
		resolved, err := client.ResolveVideoURL(detail.VideoURL)
		if err != nil {
			log.Printf("[dscheduler] ResolveVideoURL failed: %v", err)
			s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
			s.db.IncrementRetryCount(dl.ID, err.Error())
			return
		}
		videoURL = resolved
	}

	// srcName：订阅源名称，用于目录结构和 NFO studio/actor，与 B站行为一致
	srcName := src.Name
	if srcName == "" {
		srcName = dl.Uploader
	}
	uploaderDir := douyin.SanitizePath(srcName)
	outputDir := filepath.Join(s.downloadDir, uploaderDir)

	// uploaderName 仅用于通知文案，记录视频的实际作者
	uploaderName := detail.Author.Nickname
	if uploaderName == "" {
		uploaderName = srcName
	}

	title := detail.Desc
	if title == "" {
		title = dl.Title
	}
	if title == "" {
		title = fmt.Sprintf("douyin_%s", dl.VideoID)
	}
	safeTitle := douyin.SanitizePath(title)
	// SanitizePath 遇到全 emoji/特殊字符标题会返回 "unknown"，改为用 video ID 兜底
	if safeTitle == "unknown" {
		safeTitle = fmt.Sprintf("douyin_%s", dl.VideoID)
	}
	if len([]rune(safeTitle)) > 80 {
		safeTitle = string([]rune(safeTitle)[:80])
	}
	videoDir := filepath.Join(outputDir, safeTitle+" ["+dl.VideoID+"]")
	os.MkdirAll(videoDir, 0755)
	videoFilePath := filepath.Join(videoDir, safeTitle+" ["+dl.VideoID+"].mp4")

	var fileSize int64
	progressKey := fmt.Sprintf("douyin:%d", dl.ID)
	var pCb progressCallback
	pCb = func(info ProgressInfo) {
		if info.Status == "done" {
			s.removeProgress(progressKey)
			s.emitEvent(DownloadEvent{
				Type:         "completed",
				VideoID:      dl.VideoID,
				Title:        title,
				FileSize:     info.Downloaded,
				DownloadedAt: time.Now().Format(time.RFC3339),
			})
		} else {
			s.setProgress(progressKey, &info)
		}
	}

	for attempt := 1; attempt <= 3; attempt++ {
		// 从第 2 次起重新获取 URL（CDN 直链带时效签名，可能已过期）
		if attempt > 1 {
			if newDetail, urlErr := client.GetVideoDetail(dl.VideoID); urlErr == nil && newDetail.VideoURL != "" {
				newURL := newDetail.VideoURL
				if !newDetail.URLResolved {
					if resolved, resolveErr := client.ResolveVideoURL(newDetail.VideoURL); resolveErr == nil {
						newURL = resolved
					}
				}
				videoURL = newURL
				log.Printf("[dscheduler] Re-fetched video URL on attempt %d", attempt)
			} else {
				log.Printf("[dscheduler] Re-fetch video URL failed on attempt %d: %v", attempt, urlErr)
			}
		}
		ctx := s.rootCtx
		fileSize, err = downloadFileWithProgress(ctx, videoURL, videoFilePath, dl.ID, title, pCb)
		if err == nil {
			break
		}
		log.Printf("[dscheduler] Download attempt %d failed: %v", attempt, err)
		s.removeProgress(progressKey)
		if attempt < 3 {
			backoff := time.Duration(10*(1<<(attempt-1))) * time.Second
			time.Sleep(backoff)
		}
	}
	if err != nil {
		log.Printf("[dscheduler] Download failed after retries: %v", err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	log.Printf("[dscheduler] Downloaded: %s → %s (%.1f MB)", dl.VideoID, videoFilePath, float64(fileSize)/(1024*1024))

	if !src.SkipPoster && detail.Cover != "" {
		thumbPath := filepath.Join(videoDir, safeTitle+" ["+dl.VideoID+"]-poster.jpg")
		if err := douyin.DownloadThumb(detail.Cover, thumbPath); err != nil {
			log.Printf("[dscheduler] Download cover failed for %s: %v", dl.VideoID, err)
		}
	}

	if !src.SkipNFO {
		meta := &nfo.VideoMeta{
			Platform:     "douyin",
			BvID:         dl.VideoID,
			Title:        title,
			Description:  detail.Desc,
			UploaderName: srcName, // 固定用订阅源名，保持目录/studio/actor 一致
			UploadDate:   detail.CreateTimeUnix(),
			Duration:     detail.Duration / 1000,
			Thumbnail:    detail.Cover,
			WebpageURL:   douyin.BuildVideoWebURL(dl.VideoID),
			LikeCount:    detail.DiggCount,
			ShareCount:   detail.ShareCount,
			ReplyCount:   detail.CommentCount,
		}
		if err := nfo.GenerateVideoNFO(meta, videoFilePath); err != nil {
			log.Printf("[dscheduler] Generate NFO failed: %v", err)
		}
	}

	s.db.UpdateDownloadStatus(dl.ID, "completed", videoFilePath, fileSize, "")
	s.db.UpdateDownloadMeta(dl.ID, srcName, detail.Desc, detail.Cover, detail.Duration/1000)

	s.notifier.Send(notify.EventDownloadComplete, "抖音视频下载完成: "+title,
		fmt.Sprintf("作者: %s\n大小: %.1f MB", uploaderName, float64(fileSize)/(1024*1024)))
}

// downloadDouyinNote 下载抖音图集
func (s *DouyinScheduler) downloadDouyinNote(dl db.Download, src db.Source, detail *douyin.DouyinVideo) {
	// srcName：订阅源名称，用于目录结构和 NFO studio/actor
	srcName := src.Name
	if srcName == "" {
		srcName = dl.Uploader
	}
	uploaderDir := douyin.SanitizePath(srcName)

	// uploaderName 仅用于通知文案
	uploaderName := detail.Author.Nickname
	if uploaderName == "" {
		uploaderName = srcName
	}

	title := detail.Desc
	if title == "" {
		title = dl.Title
	}
	if title == "" {
		title = fmt.Sprintf("douyin_%s", dl.VideoID)
	}
	safeTitle := douyin.SanitizePath(title)
	// SanitizePath 遇到全 emoji/特殊字符标题会返回 "unknown"，改为用 video ID 兜底
	if safeTitle == "unknown" {
		safeTitle = fmt.Sprintf("douyin_%s", dl.VideoID)
	}
	if len([]rune(safeTitle)) > 80 {
		safeTitle = string([]rune(safeTitle)[:80])
	}

	noteDir := filepath.Join(s.downloadDir, uploaderDir, safeTitle+" ["+dl.VideoID+"]")
	os.MkdirAll(noteDir, 0755)

	log.Printf("[dscheduler·note] Downloading %d images for note %s → %s", len(detail.Images), dl.VideoID, noteDir)

	var totalSize int64
	successCount := 0

	for i, imgURL := range detail.Images {
		if imgURL == "" {
			continue
		}

		ext := ".jpg"
		if strings.Contains(imgURL, ".png") {
			ext = ".png"
		} else if strings.Contains(imgURL, ".webp") {
			ext = ".webp"
		}

		imgPath := filepath.Join(noteDir, fmt.Sprintf("%02d%s", i+1, ext))

		var fileSize int64
		var err error
		for attempt := 1; attempt <= 3; attempt++ {
			fileSize, err = douyin.DownloadFile(imgURL, imgPath)
			if err == nil {
				break
			}
			log.Printf("[dscheduler·note] Download image %d attempt %d failed: %v", i+1, attempt, err)
			if attempt < 3 {
				backoff := time.Duration(3*(1<<(attempt-1))) * time.Second
				time.Sleep(backoff)
			}
		}
		if err != nil {
			log.Printf("[dscheduler·note] Failed to download image %d for %s: %v", i+1, dl.VideoID, err)
			continue
		}

		totalSize += fileSize
		successCount++

		if i < len(detail.Images)-1 {
			time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)
		}
	}

	if successCount == 0 {
		log.Printf("[dscheduler·note] All images failed for %s", dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, "all images download failed")
		s.db.IncrementRetryCount(dl.ID, "all images download failed")
		return
	}

	log.Printf("[dscheduler·note] Downloaded %d/%d images for %s (%.1f MB total)",
		successCount, len(detail.Images), dl.VideoID, float64(totalSize)/(1024*1024))

	if !src.SkipPoster && detail.Cover != "" {
		coverPath := filepath.Join(noteDir, "cover.jpg")
		if err := douyin.DownloadThumb(detail.Cover, coverPath); err != nil {
			log.Printf("[dscheduler·note] Download cover failed: %v", err)
		}
	}

	if !src.SkipNFO {
		meta := &nfo.VideoMeta{
			Platform:     "douyin",
			BvID:         dl.VideoID,
			Title:        title,
			Description:  detail.Desc,
			UploaderName: srcName, // 固定用订阅源名
			UploadDate:   detail.CreateTimeUnix(),
			Thumbnail:    detail.Cover,
			WebpageURL:   douyin.BuildNoteWebURL(dl.VideoID),
			LikeCount:    detail.DiggCount,
			ShareCount:   detail.ShareCount,
			ReplyCount:   detail.CommentCount,
		}
		nfoPath := filepath.Join(noteDir, safeTitle+" ["+dl.VideoID+"].nfo")
		if err := nfo.GenerateMovieNFO(meta, nfoPath); err != nil {
			log.Printf("[dscheduler·note] Generate NFO failed: %v", err)
		}
	}

	s.db.UpdateDownloadStatus(dl.ID, "completed", noteDir, totalSize, "")
	s.db.UpdateDownloadMeta(dl.ID, srcName, detail.Desc, detail.Cover, 0)

	s.notifier.Send(notify.EventDownloadComplete,
		fmt.Sprintf("抖音图集下载完成: %s (%d张)", title, successCount),
		fmt.Sprintf("作者: %s\n大小: %.1f MB", uploaderName, float64(totalSize)/(1024*1024)))
}

// markFailed 标记下载失败并设置退避重试时间
// 若 retry_count+1 >= 3，升级为 permanent_failed，不再自动重试
func (s *DouyinScheduler) markFailed(dl db.Download, errMsg string) {
	newCount := dl.RetryCount + 1
	if newCount >= 3 {
		s.db.UpdateDownloadStatus(dl.ID, "permanent_failed", "", 0, errMsg)
		log.Printf("[dscheduler] Video %s marked permanent_failed after %d retries", dl.VideoID, newCount)
	} else {
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, errMsg)
		s.db.IncrementRetryCount(dl.ID, errMsg)
		s.db.SetNextRetryAt(dl.ID, dl.RetryCount)
		log.Printf("[dscheduler] Video %s failed, next retry in ~%dm (retry_count=%d)", dl.VideoID, []int{15, 30, 60}[dl.RetryCount], newCount)
	}
}
