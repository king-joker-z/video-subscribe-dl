package scheduler

import (
	"context"
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
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
)

// retryOneDouyinDownload 执行单个抖音下载
// 与 B站 DASH 不同，抖音视频是直接 MP4 下载（更简单但风控更严）
func (s *Scheduler) retryOneDouyinDownload(dl db.Download) {
	// 检查抖音是否被暂停（风控触发后需手动恢复）
	if s.IsDouyinPaused() {
		log.Printf("[douyin-dl] 抖音下载已暂停，跳过 %s", dl.VideoID)
		return
	}

	// 下载频率限制: 每分钟最多 2 条
	s.douyinDownloadLimiter.Acquire()

	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[douyin-dl] Source %d not found for download %d, skipping", dl.SourceID, dl.ID)
		return
	}

	client := s.newDouyinClient()
	defer client.Close()

	// Step 1: 获取视频详情（带重试）
	s.db.UpdateDownloadStatus(dl.ID, "downloading", "", 0, "")

	var detail *douyin.DouyinVideo
	for attempt := 1; attempt <= 3; attempt++ {
		detail, err = client.GetVideoDetail(dl.VideoID)
		if err == nil {
			break
		}
		log.Printf("[douyin-dl] GetVideoDetail attempt %d failed for %s: %v", attempt, dl.VideoID, err)
		if attempt < 3 {
			backoff := time.Duration(5*(1<<(attempt-1))) * time.Second
			time.Sleep(backoff)
		}
	}
	if err != nil {
		// 风控检测: 如果是风控错误，暂停抖音下载
		if errors.Is(err, douyin.ErrDouyinRiskControl) {
			reason := fmt.Sprintf("风控触发: %v", err)
			s.PauseDouyin(reason)
			s.notifier.Send(notify.EventRateLimited, "抖音风控触发",
				"抖音下载已暂停，请在 Web UI 手动恢复\n错误: "+err.Error())
			s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
			s.db.IncrementRetryCount(dl.ID, err.Error())
			return
		}
		log.Printf("[douyin-dl] GetVideoDetail failed after retries for %s: %v", dl.VideoID, err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	// 图集下载（Phase 2）
	if detail.IsNote && len(detail.Images) > 0 {
		s.downloadDouyinNote(dl, *src, detail)
		return
	}

	// 既不是图集也没有视频 URL，跳过
	if detail.VideoURL == "" {
		log.Printf("[douyin-dl] Skipping post %s: no video URL and no images", dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "completed", "", 0, "skipped: no downloadable content")
		return
	}

	// Step 2: 解析最终下载 URL（跟随 302）
	videoURL, err := client.ResolveVideoURL(detail.VideoURL)
	if err != nil {
		log.Printf("[douyin-dl] ResolveVideoURL failed: %v", err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	// Step 3: 构建输出路径
	uploaderName := detail.Author.Nickname
	if uploaderName == "" {
		uploaderName = dl.Uploader
	}
	if uploaderName == "" {
		uploaderName = src.Name
	}
	uploaderDir := douyin.SanitizePath(uploaderName)
	outputDir := filepath.Join(s.downloadDir, uploaderDir)

	title := detail.Desc
	if title == "" {
		title = dl.Title
	}
	if title == "" {
		title = fmt.Sprintf("douyin_%s", dl.VideoID)
	}
	safeTitle := douyin.SanitizePath(title)
	if len([]rune(safeTitle)) > 80 {
		safeTitle = string([]rune(safeTitle)[:80])
	}
	// 每个视频一个独立文件夹（和 B 站保持一致）
	videoDir := filepath.Join(outputDir, safeTitle+" ["+dl.VideoID+"]")
	os.MkdirAll(videoDir, 0755)
	videoFilePath := filepath.Join(videoDir, safeTitle+" ["+dl.VideoID+"].mp4")

	// Step 4: 下载视频（带重试 + SSE 进度追踪）
	var fileSize int64
	progressKey := fmt.Sprintf("douyin:%d", dl.ID)
	var progressCb douyinProgressCallback
	if s.dl != nil {
		progressCb = func(info downloader.ProgressInfo) {
			if info.Status == "done" {
				s.dl.RemoveExternalProgress(progressKey)
				// 推送 download_event: completed
				s.dl.EmitEvent(downloader.DownloadEvent{
					Type:     "completed",
					BvID:     dl.VideoID,
					Title:    title,
					FileSize: info.Downloaded,
				})
			} else {
				s.dl.SetExternalProgress(progressKey, &info)
			}
		}
	}
	for attempt := 1; attempt <= 3; attempt++ {
		ctx := context.Background()
		fileSize, err = downloadDouyinFileWithProgress(ctx, videoURL, videoFilePath, int64(dl.ID), title, progressCb)
		if err == nil {
			break
		}
		log.Printf("[douyin-dl] Download attempt %d failed: %v", attempt, err)
		if s.dl != nil {
			s.dl.RemoveExternalProgress(progressKey)
		}
		if attempt < 3 {
			backoff := time.Duration(10*(1<<(attempt-1))) * time.Second
			time.Sleep(backoff)
		}
	}
	if err != nil {
		log.Printf("[douyin-dl] Download failed after retries: %v", err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	log.Printf("[douyin-dl] Downloaded: %s → %s (%.1f MB)", dl.VideoID, videoFilePath, float64(fileSize)/(1024*1024))

	// Step 5: 下载封面
	if !src.SkipPoster && detail.Cover != "" {
		thumbPath := filepath.Join(videoDir, safeTitle+" ["+dl.VideoID+"]-poster.jpg")
		if err := douyin.DownloadThumb(detail.Cover, thumbPath); err != nil {
			log.Printf("[douyin-dl] Download cover failed for %s: %v", dl.VideoID, err)
		}
	}

	// Step 6: 生成 NFO
	if !src.SkipNFO {
		meta := &nfo.VideoMeta{
			Platform:     "douyin",
			BvID:         dl.VideoID,
			Title:        title,
			Description:  detail.Desc,
			UploaderName: uploaderName,
			UploadDate:   detail.CreateTimeUnix(),
			Duration:     detail.Duration / 1000,
			Thumbnail:    detail.Cover,
			WebpageURL:   douyin.BuildVideoWebURL(dl.VideoID),
			LikeCount:    detail.DiggCount,
			ShareCount:   detail.ShareCount,
			ReplyCount:   detail.CommentCount,
		}
		if err := nfo.GenerateVideoNFO(meta, videoFilePath); err != nil {
			log.Printf("[douyin-dl] Generate NFO failed: %v", err)
		}
	}

	// Step 7: 更新 DB
	s.db.UpdateDownloadStatus(dl.ID, "completed", videoFilePath, fileSize, "")
	s.db.UpdateDownloadMeta(dl.ID, uploaderName, detail.Desc, detail.Cover, detail.Duration/1000)

	s.notifier.Send(notify.EventDownloadComplete, "抖音视频下载完成: "+title,
		fmt.Sprintf("作者: %s\n大小: %.1f MB", uploaderName, float64(fileSize)/(1024*1024)))
}

// downloadDouyinNote 下载抖音图集（笔记）
// 将所有图片下载到 {uploader}/{title} [aweme_id]/ 目录中
func (s *Scheduler) downloadDouyinNote(dl db.Download, src db.Source, detail *douyin.DouyinVideo) {
	uploaderName := detail.Author.Nickname
	if uploaderName == "" {
		uploaderName = dl.Uploader
	}
	if uploaderName == "" {
		uploaderName = src.Name
	}

	title := detail.Desc
	if title == "" {
		title = dl.Title
	}
	if title == "" {
		title = fmt.Sprintf("douyin_%s", dl.VideoID)
	}
	safeTitle := douyin.SanitizePath(title)
	if len([]rune(safeTitle)) > 80 {
		safeTitle = string([]rune(safeTitle)[:80])
	}

	uploaderDir := douyin.SanitizePath(uploaderName)
	noteDir := filepath.Join(s.downloadDir, uploaderDir, safeTitle+" ["+dl.VideoID+"]")
	os.MkdirAll(noteDir, 0755)

	log.Printf("[douyin-note] Downloading %d images for note %s → %s", len(detail.Images), dl.VideoID, noteDir)

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
			log.Printf("[douyin-note] Download image %d attempt %d failed: %v", i+1, attempt, err)
			if attempt < 3 {
				backoff := time.Duration(3*(1<<(attempt-1))) * time.Second
				time.Sleep(backoff)
			}
		}
		if err != nil {
			log.Printf("[douyin-note] Failed to download image %d for %s: %v", i+1, dl.VideoID, err)
			continue
		}

		totalSize += fileSize
		successCount++

		// 图片间短暂间隔，避免风控
		if i < len(detail.Images)-1 {
			time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)
		}
	}

	if successCount == 0 {
		log.Printf("[douyin-note] All images failed for %s", dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, "all images download failed")
		s.db.IncrementRetryCount(dl.ID, "all images download failed")
		return
	}

	log.Printf("[douyin-note] Downloaded %d/%d images for %s (%.1f MB total)",
		successCount, len(detail.Images), dl.VideoID, float64(totalSize)/(1024*1024))

	// 下载封面（使用第一张图作为封面）
	if !src.SkipPoster && detail.Cover != "" {
		coverPath := filepath.Join(noteDir, "cover.jpg")
		if err := douyin.DownloadThumb(detail.Cover, coverPath); err != nil {
			log.Printf("[douyin-note] Download cover failed: %v", err)
		}
	}

	// 生成 NFO
	if !src.SkipNFO {
		meta := &nfo.VideoMeta{
			Platform:     "douyin",
			BvID:         dl.VideoID,
			Title:        title,
			Description:  detail.Desc,
			UploaderName: uploaderName,
			UploadDate:   detail.CreateTimeUnix(),
			Thumbnail:    detail.Cover,
			WebpageURL:   douyin.BuildNoteWebURL(dl.VideoID),
			LikeCount:    detail.DiggCount,
			ShareCount:   detail.ShareCount,
			ReplyCount:   detail.CommentCount,
		}
		nfoPath := filepath.Join(noteDir, safeTitle+" ["+dl.VideoID+"].nfo")
		if err := nfo.GenerateMovieNFO(meta, nfoPath); err != nil {
			log.Printf("[douyin-note] Generate NFO failed: %v", err)
		}
	}

	// 更新 DB —— 存目录路径
	s.db.UpdateDownloadStatus(dl.ID, "completed", noteDir, totalSize, "")
	s.db.UpdateDownloadMeta(dl.ID, uploaderName, detail.Desc, detail.Cover, 0)

	s.notifier.Send(notify.EventDownloadComplete,
		fmt.Sprintf("抖音图集下载完成: %s (%d张)", title, successCount),
		fmt.Sprintf("作者: %s\n大小: %.1f MB", uploaderName, float64(totalSize)/(1024*1024)))
}
