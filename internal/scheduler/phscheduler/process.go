package phscheduler

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/pornhub"
)

// retryOneDownload 执行单个 PH 下载（带进度 + 重试 + NFO）
func (s *PHScheduler) retryOneDownload(dl db.Download) {
	if s.IsPaused() {
		log.Printf("[phscheduler] PH 下载已暂停，跳过 %s", dl.VideoID)
		return
	}

	// 检查 context 是否已取消（Stop 被调用）
	select {
	case <-s.rootCtx.Done():
		log.Printf("[phscheduler] context cancelled, skip download %s", dl.VideoID)
		return
	default:
	}

	if !s.downloadLimiter.Acquire() {
		log.Printf("[phscheduler] rate limiter stopped, skip download %s", dl.VideoID)
		return
	}

	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[phscheduler] Source %d not found for download %d, skipping", dl.SourceID, dl.ID)
		return
	}
	if !src.Enabled {
		log.Printf("[phscheduler] Source %d (%s) is disabled, skipping download %s", src.ID, src.Name, dl.VideoID)
		return
	}

	client := s.newClient()
	defer client.Close()
	if s.getCookie() != "" {
		client.SetCookie(s.getCookie())
	}

	s.db.UpdateDownloadStatus(dl.ID, "downloading", "", 0, "")
	// 广播 started 事件
	s.emitEvent(DownloadEvent{
		Type:    "started",
		VideoID: dl.VideoID,
		Title:   dl.Title,
	})

	// 构造视频页面 URL
	videoPageURL := fmt.Sprintf("https://www.pornhub.com/view_video.php?viewkey=%s", dl.VideoID)

	// 获取 MP4 直链（最多重试 3 次）
	var mp4URL string
	for attempt := 1; attempt <= 3; attempt++ {
		mp4URL, err = client.GetVideoURL(videoPageURL)
		if err == nil {
			break
		}
		log.Printf("[phscheduler] GetVideoURL attempt %d failed for %s: %v", attempt, dl.VideoID, err)

		if pornhub.IsUnavailable(err) {
			// 内容不可用（删除/私有），标记为完成（跳过）
			log.Printf("[phscheduler] Video %s is unavailable, marking as completed (skipped)", dl.VideoID)
			s.db.UpdateDownloadStatus(dl.ID, "completed", "", 0, "skipped: content unavailable")
			return
		}
		if pornhub.IsRateLimit(err) {
			reason := fmt.Sprintf("PH 限流触发: %v", err)
			s.Pause(reason)
			s.TriggerCooldown()
			if s.notifier != nil {
				s.notifier.Send(notify.EventRateLimited, "Pornhub 限流触发",
					"Pornhub 下载已暂停，请在 Web UI 手动恢复\n错误: "+err.Error())
			}
			s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
			s.db.IncrementRetryCount(dl.ID, err.Error())
			return
		}

		if attempt < 3 {
			backoff := time.Duration(5*(1<<(attempt-1))) * time.Second
			select {
			case <-s.rootCtx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}
	if err != nil {
		log.Printf("[phscheduler] GetVideoURL failed after retries for %s: %v", dl.VideoID, err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	// 构建目录结构
	srcName := src.Name
	if srcName == "" {
		srcName = dl.Uploader
	}
	if srcName == "" {
		srcName = "pornhub"
	}

	title := dl.Title
	if title == "" {
		title = fmt.Sprintf("pornhub_%s", dl.VideoID)
	}

	// BuildVideoDir 已内含 "safeTitle [viewkey]" 子目录，视频文件直接放在该目录下
	videoDir := pornhub.BuildVideoDir(s.downloadDir, srcName, title, dl.VideoID)
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		log.Printf("[phscheduler] mkdir failed for %s: %v", videoDir, err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, "mkdir failed: "+err.Error())
		s.db.IncrementRetryCount(dl.ID, "mkdir failed: "+err.Error())
		return
	}

	safeTitle := pornhub.SafePath(title, "pornhub_"+dl.VideoID)
	// 文件名：safeTitle [viewkey].mp4，放在 videoDir 内
	videoFilePath := filepath.Join(videoDir, safeTitle+".mp4")

	// 进度推送
	progressKey := fmt.Sprintf("pornhub:%d", dl.ID)
	var pCb phProgressCallback
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

	// 下载（最多重试 3 次）
	// HLS m3u8：用 ffmpeg 转存；普通 HTTP：Range 断点续传
	var fileSize int64
	isHLS := strings.Contains(mp4URL, ".m3u8")
	for attempt := 1; attempt <= 3; attempt++ {
		ctx := s.rootCtx
		if isHLS {
			fileSize, err = downloadHLSWithFFmpeg(ctx, mp4URL, videoFilePath, dl.ID, title, pCb)
		} else {
			fileSize, err = downloadPHFileWithProgress(ctx, mp4URL, videoFilePath, dl.ID, title, pCb)
		}
		if err == nil {
			break
		}
		log.Printf("[phscheduler] Download attempt %d failed: %v", attempt, err)
		s.removeProgress(progressKey)
		if attempt < 3 {
			backoff := time.Duration(10*(1<<(attempt-1))) * time.Second
			select {
			case <-s.rootCtx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}
	if err != nil {
		log.Printf("[phscheduler] Download failed after retries: %v", err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	log.Printf("[phscheduler] Downloaded: %s → %s (%.1f MB)", dl.VideoID, videoFilePath, float64(fileSize)/(1024*1024))

	// 下载封面
	if !src.SkipPoster && dl.Thumbnail != "" {
		thumbPath := filepath.Join(videoDir, safeTitle+"-poster.jpg")
		if thumbErr := downloadThumb(dl.Thumbnail, thumbPath); thumbErr != nil {
			log.Printf("[phscheduler] Download thumb failed for %s: %v", dl.VideoID, thumbErr)
		}
	}

	// 生成 NFO
	if !src.SkipNFO {
		meta := &nfo.VideoMeta{
			Platform:     "pornhub",
			BvID:         dl.VideoID,
			Title:        title,
			UploaderName: srcName,
			WebpageURL:   videoPageURL,
		}
		if err := nfo.GenerateVideoNFO(meta, videoFilePath); err != nil {
			log.Printf("[phscheduler] Generate NFO failed: %v", err)
		}
	}

	s.db.UpdateDownloadStatus(dl.ID, "completed", videoFilePath, fileSize, "")
	// UpdateDownloadMeta(id, uploader, description, thumbnail, duration)
	// description 留空，title 已在 downloads.title 字段
	s.db.UpdateDownloadMeta(dl.ID, srcName, "", dl.Thumbnail, dl.Duration)

	if s.notifier != nil {
		s.notifier.Send(notify.EventDownloadComplete, "Pornhub 视频下载完成: "+title,
			fmt.Sprintf("博主: %s\n大小: %.1f MB", srcName, float64(fileSize)/(1024*1024)))
	}
}

// downloadThumb 下载封面图到指定路径
func downloadThumb(thumbURL, destPath string) error {
	req, err := http.NewRequest("GET", thumbURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.pornhub.com/")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("thumb returned %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, io.LimitReader(resp.Body, 20*1024*1024)) // 封面最大 20 MB
	return err
}

// downloadHLSWithFFmpeg 使用 ffmpeg 将 HLS m3u8 流转存为 mp4 文件
// ffmpeg 直接从 CDN 拉取 TS 分片并 mux 成 mp4，无需断点续传（分片本身保证完整性）
func downloadHLSWithFFmpeg(ctx context.Context, m3u8URL, destPath string, dlID int64, title string, cb phProgressCallback) (int64, error) {
	// 临时文件，避免写一半的文件被误用
	tmpPath := destPath + ".tmp"

	// 如果之前的 .tmp 存在先清理（HLS 不支持断点续传，重来）
	_ = os.Remove(tmpPath)

	args := []string{
		"-y",                  // 覆盖输出
		"-user_agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"-headers", "Referer: https://www.pornhub.com/\r\n",
		"-i", m3u8URL,
		"-c", "copy",          // 流复制，不转码
		"-bsf:a", "aac_adtstoasc", // ADTS → ASC（mp4 容器要求）
		"-movflags", "+faststart",
		"-f", "mp4",           // 显式指定 muxer，避免 .tmp 后缀导致格式识别失败
		tmpPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// 通知前端"开始下载"
	cb(ProgressInfo{Status: "downloading", Downloaded: 0})

	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpPath)
		return 0, fmt.Errorf("ffmpeg failed: %v\n%s", err, string(out))
	}

	// 重命名 tmp → 最终路径
	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, fmt.Errorf("rename tmp failed: %v", err)
	}

	fi, _ := os.Stat(destPath)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	cb(ProgressInfo{Status: "done", Downloaded: size})
	return size, nil
}
