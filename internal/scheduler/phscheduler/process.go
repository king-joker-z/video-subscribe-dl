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
	"strconv"
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
		log.Printf("[phscheduler] Source %d (%s) is disabled, marking download %s as skipped", src.ID, src.Name, dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "skipped", "", 0, "skipped: source disabled")
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

	// 获取 MP4 直链（最多重试 3 次，按错误分类决策）
	var mp4URL string
	for attempt := 1; attempt <= 3; attempt++ {
		mp4URL, err = client.GetVideoURL(videoPageURL)
		if err == nil {
			break
		}
		log.Printf("[phscheduler] GetVideoURL attempt %d failed for %s: %v", attempt, dl.VideoID, err)

		switch pornhub.GetErrKind(err) {
		case pornhub.ErrKindUnavailable:
			// 内容真不可用（删除/私有），直接 skip，不重试
			log.Printf("[phscheduler] Video %s unavailable, skipping: %v", dl.VideoID, err)
			s.db.UpdateDownloadStatus(dl.ID, "skipped", "", 0, "skipped: "+err.Error())
			return
		case pornhub.ErrKindParseFailed:
			// 页面解析失败（竖屏新格式/embed外链等），标 failed 留人工排查
			log.Printf("[phscheduler] Video %s parse failed, marking failed for manual review: %v", dl.VideoID, err)
			s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
			s.db.IncrementRetryCount(dl.ID, err.Error())
			return
		case pornhub.ErrKindRateLimit:
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
		default: // ErrKindTransient：临时错误，继续重试
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

	// thumbnail 为空时尝试从视频详情页补充（处理历史遗留空值）
	if dl.Thumbnail == "" {
		if thumb := client.GetVideoThumbnail(videoPageURL); thumb != "" {
			dl.Thumbnail = thumb
			s.db.Exec("UPDATE downloads SET thumbnail = ? WHERE id = ?", thumb, dl.ID)
			log.Printf("[phscheduler] Recovered thumbnail for %s from page", dl.VideoID)
		} else {
			log.Printf("[phscheduler] Could not recover thumbnail for %s", dl.VideoID)
		}
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
	// HLS URL 带 CDN 时效签名，重试时重新获取防止过期
	var fileSize int64
	isHLS := strings.Contains(mp4URL, ".m3u8")
	for attempt := 1; attempt <= 3; attempt++ {
		// 从第 2 次起，HLS 重新获取 URL（CDN 签名可能过期）
		if attempt > 1 && isHLS {
			if newURL, urlErr := client.GetVideoURL(videoPageURL); urlErr == nil {
				mp4URL = newURL
				isHLS = strings.Contains(mp4URL, ".m3u8")
				log.Printf("[phscheduler] Re-fetched URL on attempt %d (isHLS=%v)", attempt, isHLS)
			} else {
				log.Printf("[phscheduler] Re-fetch URL failed on attempt %d: %v", attempt, urlErr)
			}
		}
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
		// 清理可能遗留的 .tmp 文件
		tmpPath := videoFilePath + ".tmp"
		if removeErr := os.Remove(tmpPath); removeErr == nil {
			log.Printf("[phscheduler] Cleaned up stale tmp file: %s", tmpPath)
		}
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	log.Printf("[phscheduler] Downloaded: %s → %s (%.1f MB)", dl.VideoID, videoFilePath, float64(fileSize)/(1024*1024))

	// 下载封面
	if !src.SkipPoster {
		thumbPath := filepath.Join(videoDir, safeTitle+"-poster.jpg")
		if dl.Thumbnail == "" {
			log.Printf("[phscheduler] Thumbnail URL is empty for %s, skipping thumb download", dl.VideoID)
		} else {
			if thumbErr := downloadThumb(dl.Thumbnail, thumbPath, s.getCookie()); thumbErr != nil {
				log.Printf("[phscheduler] Download thumb failed for %s: %v", dl.VideoID, thumbErr)
			} else {
				if dbErr := s.db.UpdateThumbPath(dl.ID, thumbPath); dbErr != nil {
					log.Printf("[phscheduler] UpdateThumbPath failed for %s: %v", dl.VideoID, dbErr)
				}
			}
		}
		// 兜底：若封面文件不存在，用 ffmpeg 截取视频第 70% 位置的帧
		if _, statErr := os.Stat(thumbPath); os.IsNotExist(statErr) {
			if capErr := captureThumbFromVideo(videoFilePath, thumbPath); capErr != nil {
				log.Printf("[phscheduler] captureThumbFromVideo failed for %s: %v", dl.VideoID, capErr)
			} else {
				if dbErr := s.db.UpdateThumbPath(dl.ID, thumbPath); dbErr != nil {
					log.Printf("[phscheduler] UpdateThumbPath (capture) failed for %s: %v", dl.VideoID, dbErr)
				}
			}
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

// downloadThumb 下载封面图到指定路径，最多重试 3 次（指数退避 2s/4s）
func downloadThumb(thumbURL, destPath, cookie string) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		lastErr = downloadThumbOnce(thumbURL, destPath, cookie)
		if lastErr == nil {
			return nil
		}
		log.Printf("[phscheduler] downloadThumb attempt %d failed: %v", attempt, lastErr)
		if attempt < 3 {
			time.Sleep(time.Duration(2*(1<<(attempt-1))) * time.Second) // 2s, 4s
		}
	}
	return lastErr
}

// downloadThumbOnce 执行单次封面图下载
func downloadThumbOnce(thumbURL, destPath, cookie string) error {
	req, err := http.NewRequest("GET", thumbURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.pornhub.com/")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

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

// CaptureThumbFromVideo 用 ffmpeg 截取视频第 70% 位置的帧作为封面图（可供外部调用）
func CaptureThumbFromVideo(videoPath, destPath string) error {
	return captureThumbFromVideo(videoPath, destPath)
}

// lookupBin 查找可执行文件路径：优先 PATH lookup，失败则尝试已知固定路径
func lookupBin(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	// Alpine/Debian 容器常见固定路径
	for _, candidate := range []string{"/usr/bin/" + name, "/usr/local/bin/" + name, "/bin/" + name} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return name // 兜底：原样返回，让系统报错
}

// captureThumbFromVideo 用 ffmpeg 截取视频第 70% 位置的帧作为封面图
func captureThumbFromVideo(videoPath, destPath string) error {
	ffprobe := lookupBin("ffprobe")
	ffmpeg := lookupBin("ffmpeg")

	// 先用 ffprobe 获取视频时长（秒）
	probeOut, err := exec.Command(ffprobe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	).Output()
	if err != nil {
		return fmt.Errorf("ffprobe failed: %v", err)
	}
	durationStr := strings.TrimSpace(string(probeOut))
	duration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil || duration <= 0 {
		return fmt.Errorf("invalid duration %q: %v", durationStr, err)
	}

	seekTime := duration * 0.7
	timeStr := strconv.FormatFloat(seekTime, 'f', 3, 64)

	out, err := exec.Command(ffmpeg,
		"-ss", timeStr,
		"-i", videoPath,
		"-vframes", "1",
		"-q:v", "2",
		"-y",      // 覆盖已有文件
		destPath,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg capture failed: %v\n%s", err, string(out))
	}
	return nil
}

// markFailed 标记下载失败并设置退避重试时间
// 若 retry_count+1 >= 3，升级为 permanent_failed，不再自动重试
func (s *PHScheduler) markFailed(dl db.Download, errMsg string) {
	newCount := dl.RetryCount + 1
	if newCount >= 3 {
		s.db.UpdateDownloadStatus(dl.ID, "permanent_failed", "", 0, errMsg)
		log.Printf("[phscheduler] Video %s marked permanent_failed after %d retries", dl.VideoID, newCount)
	} else {
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, errMsg)
		s.db.IncrementRetryCount(dl.ID, errMsg)
		s.db.SetNextRetryAt(dl.ID, dl.RetryCount)
		log.Printf("[phscheduler] Video %s failed, next retry in ~%dm (retry_count=%d)", dl.VideoID, []int{15, 30, 60}[dl.RetryCount], newCount)
	}
}
