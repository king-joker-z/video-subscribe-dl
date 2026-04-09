package xscheduler

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/xchina"
)

// xcProgressCallback 下载进度回调类型
type xcProgressCallback func(info ProgressInfo)

// retryOneDownload 执行单个 xchina 下载（带重试 + NFO）
func (s *XScheduler) retryOneDownload(dl db.Download) {
	if s.IsPaused() {
		log.Printf("[xscheduler] 已暂停，跳过 %s", dl.VideoID)
		return
	}

	// 检查 context 是否已取消
	select {
	case <-s.rootCtx.Done():
		log.Printf("[xscheduler] context cancelled, skip download %s", dl.VideoID)
		return
	default:
	}

	// CAS：原子抢占任务，防止双路投递重复执行
	claimed, err := s.db.ClaimDownloadForProcessing(dl.ID)
	if err != nil {
		log.Printf("[xscheduler] ClaimDownloadForProcessing failed for %d: %v", dl.ID, err)
		return
	}
	if !claimed {
		log.Printf("[xscheduler] Download %d already claimed by another goroutine, skip", dl.ID)
		return
	}

	if !s.downloadLimiter.Acquire() {
		log.Printf("[xscheduler] rate limiter stopped, skip download %s", dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "pending", "", 0, "")
		return
	}

	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[xscheduler] Source %d not found for download %d, marking failed", dl.SourceID, dl.ID)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, "source not found")
		return
	}
	if !src.Enabled {
		log.Printf("[xscheduler] Source %d (%s) is disabled, marking download %s as skipped", src.ID, src.Name, dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "skipped", "", 0, "skipped: source disabled")
		return
	}

	// 广播 started 事件
	s.emitEvent(DownloadEvent{
		Type:    "started",
		VideoID: dl.VideoID,
		Title:   dl.Title,
	})

	// 视频页 URL 从 Description 字段取（CheckXChinaModel 存入的 PageURL）
	// 若 Description 为空，按 VideoID 构造
	videoPageURL := dl.Description
	if videoPageURL == "" {
		videoPageURL = fmt.Sprintf("https://en.xchina.co/video/id-%s.html", dl.VideoID)
	}

	// 获取 HLS m3u8 URL 及视频页标题（最多重试 3 次，按错误分类决策）
	var m3u8URL string
	var pageTitle string
	for attempt := 1; attempt <= 3; attempt++ {
		m3u8URL, pageTitle, err = fetchVideoPageInfo(s.rootCtx, videoPageURL)
		if err == nil {
			break
		}
		log.Printf("[xscheduler] getXChinaVideoURL attempt %d failed for %s: %v", attempt, dl.VideoID, err)

		switch xchina.GetErrKind(err) {
		case xchina.ErrKindUnavailable:
			log.Printf("[xscheduler] Video %s unavailable, skipping: %v", dl.VideoID, err)
			s.db.UpdateDownloadStatus(dl.ID, "skipped", "", 0, "skipped: "+err.Error())
			return
		case xchina.ErrKindRateLimit:
			reason := fmt.Sprintf("XChina 限流触发: %v", err)
			s.Pause(reason)
			s.TriggerCooldown()
			if s.notifier != nil {
				s.notifier.Send(notify.EventRateLimited, "XChina 限流触发",
					"XChina 下载已暂停，请在 Web UI 手动恢复\n错误: "+err.Error())
			}
			s.markFailed(dl, err.Error())
			return
		case xchina.ErrKindParseFailed:
			log.Printf("[xscheduler] Video %s parse failed, marking failed for manual review: %v", dl.VideoID, err)
			s.markFailed(dl, err.Error())
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
		log.Printf("[xscheduler] fetchVideoPageInfo failed after retries for %s: %v", dl.VideoID, err)
		s.markFailed(dl, err.Error())
		return
	}

	// 若列表页没拿到标题，用视频详情页解析出的标题更新
	if pageTitle != "" && (dl.Title == "" || strings.HasPrefix(dl.Title, "xchina_")) {
		dl.Title = pageTitle
		s.db.Exec("UPDATE downloads SET title = ? WHERE id = ?", pageTitle, dl.ID)
		log.Printf("[xscheduler] Recovered title for %s: %s", dl.VideoID, pageTitle)
	}

	// 构建目录结构
	srcName := src.Name
	if srcName == "" {
		srcName = dl.Uploader
	}
	if srcName == "" {
		srcName = "xchina"
	}

	title := dl.Title
	if title == "" {
		title = fmt.Sprintf("xchina_%s", dl.VideoID)
	}

	videoDir := xchina.BuildVideoDir(s.downloadDir, srcName, title, dl.VideoID)
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		log.Printf("[xscheduler] mkdir failed for %s: %v", videoDir, err)
		s.markFailed(dl, "mkdir failed: "+err.Error())
		return
	}

	safeTitle := xchina.SafePath(title, "xchina_"+dl.VideoID)
	videoFilePath := filepath.Join(videoDir, safeTitle+".mp4")

	// 进度推送
	progressKey := fmt.Sprintf("xchina:%d", dl.ID)
	pCb := func(info ProgressInfo) {
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

	// HLS 下载（最多重试 3 次，重试时重新获取 URL 防 CDN 签名过期）
	var fileSize int64
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			if newURL, _, urlErr := fetchVideoPageInfo(s.rootCtx, videoPageURL); urlErr == nil {
				m3u8URL = newURL
				log.Printf("[xscheduler] Re-fetched URL on attempt %d", attempt)
			} else {
				log.Printf("[xscheduler] Re-fetch URL failed on attempt %d: %v", attempt, urlErr)
			}
		}
		fileSize, err = downloadXChinaHLS(s.rootCtx, m3u8URL, videoFilePath, dl.ID, title, pCb)
		if err == nil {
			break
		}
		log.Printf("[xscheduler] Download attempt %d failed: %v", attempt, err)
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
		log.Printf("[xscheduler] Download failed after retries: %v", err)
		tmpPath := videoFilePath + ".tmp"
		if removeErr := os.Remove(tmpPath); removeErr == nil {
			log.Printf("[xscheduler] Cleaned up stale tmp file: %s", tmpPath)
		}
		s.markFailed(dl, err.Error())
		return
	}

	log.Printf("[xscheduler] Downloaded: %s → %s (%.1f MB)", dl.VideoID, videoFilePath, float64(fileSize)/(1024*1024))

	// 下载封面
	if !src.SkipPoster {
		thumbPath := filepath.Join(videoDir, safeTitle+"-poster.jpg")
		if dl.Thumbnail == "" {
			log.Printf("[xscheduler] Thumbnail URL is empty for %s, skipping thumb download", dl.VideoID)
		} else {
			if thumbErr := downloadXChinaThumb(dl.Thumbnail, thumbPath); thumbErr != nil {
				log.Printf("[xscheduler] Download thumb failed for %s: %v", dl.VideoID, thumbErr)
			} else {
				if dbErr := s.db.UpdateThumbPath(dl.ID, thumbPath); dbErr != nil {
					log.Printf("[xscheduler] UpdateThumbPath failed for %s: %v", dl.VideoID, dbErr)
				}
			}
		}
		// 兜底：若封面文件不存在，用 ffmpeg 截取视频第 70% 位置的帧
		if _, statErr := os.Stat(thumbPath); os.IsNotExist(statErr) {
			if capErr := xcCaptureThumbFromVideo(videoFilePath, thumbPath); capErr != nil {
				log.Printf("[xscheduler] xcCaptureThumbFromVideo failed for %s: %v", dl.VideoID, capErr)
			} else {
				if dbErr := s.db.UpdateThumbPath(dl.ID, thumbPath); dbErr != nil {
					log.Printf("[xscheduler] UpdateThumbPath (capture) failed for %s: %v", dl.VideoID, dbErr)
				}
			}
		}
	}

	// 生成 NFO
	if !src.SkipNFO {
		meta := &nfo.VideoMeta{
			Platform:     "xchina",
			BvID:         dl.VideoID,
			Title:        title,
			UploaderName: srcName,
			WebpageURL:   videoPageURL,
		}
		if err := nfo.GenerateVideoNFO(meta, videoFilePath); err != nil {
			log.Printf("[xscheduler] Generate NFO failed: %v", err)
		}
	}

	s.db.UpdateDownloadStatus(dl.ID, "completed", videoFilePath, fileSize, "")
	s.db.UpdateDownloadMeta(dl.ID, srcName, "", dl.Thumbnail, dl.Duration)

	if s.notifier != nil {
		s.notifier.Send(notify.EventDownloadComplete, "XChina 视频下载完成: "+title,
			fmt.Sprintf("模特: %s\n大小: %.1f MB", srcName, float64(fileSize)/(1024*1024)))
	}
}

// fetchVideoPageInfo 从视频详情页提取 HLS m3u8 URL 和视频标题
func fetchVideoPageInfo(ctx context.Context, videoPageURL string) (m3u8URL, title string, err error) {
	req, reqErr := http.NewRequestWithContext(ctx, "GET", videoPageURL, nil)
	if reqErr != nil {
		return "", "", reqErr
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://en.xchina.co/")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	hc := &http.Client{Timeout: 30 * time.Second}
	resp, doErr := hc.Do(req)
	if doErr != nil {
		return "", "", doErr
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 || resp.StatusCode == 410 {
		return "", "", fmt.Errorf("HTTP %d: token expired or forbidden (%s)", resp.StatusCode, videoPageURL)
	}
	if resp.StatusCode == 404 {
		return "", "", fmt.Errorf("HTTP 404: video not found (%s)", videoPageURL)
	}
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, videoPageURL)
	}

	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if readErr != nil {
		return "", "", fmt.Errorf("read body failed: %v", readErr)
	}
	body := string(bodyBytes)

	m3u8URL, err = xchina.ExtractM3U8URL(body, videoPageURL)
	if err != nil {
		return "", "", err
	}

	// 从视频详情页提取标题：优先 <h1>，其次 <title>
	title = extractVideoPageTitle(body)
	return m3u8URL, title, nil
}

// extractVideoPageTitle 从视频详情页 HTML 提取标题
var (
	h1Re    = regexp.MustCompile(`(?i)<h1[^>]*>\s*([^<]{2,200})\s*</h1>`)
	titleRe = regexp.MustCompile(`(?i)<title>\s*([^<]+?)\s*(?:\s*[-|–]\s*[^<]*)?\s*</title>`)
)

func extractVideoPageTitle(body string) string {
	if m := h1Re.FindStringSubmatch(body); len(m) > 1 {
		t := strings.TrimSpace(m[1])
		if len([]rune(t)) >= 2 {
			return t
		}
	}
	if m := titleRe.FindStringSubmatch(body); len(m) > 1 {
		t := strings.TrimSpace(m[1])
		// 去掉 " - xchina" 等站点后缀
		for _, suffix := range []string{" - xchina", " - XChina", " | xchina", " | XChina"} {
			t = strings.TrimSuffix(t, suffix)
		}
		t = strings.TrimSpace(t)
		if len([]rune(t)) >= 2 {
			return t
		}
	}
	return ""
}

// downloadXChinaHLS 使用 ffmpeg 下载 HLS m3u8 流并转存为 mp4
func downloadXChinaHLS(ctx context.Context, m3u8URL, destPath string, _ int64, _ string, cb xcProgressCallback) (int64, error) {
	tmpPath := destPath + ".tmp"
	_ = os.Remove(tmpPath)

	args := []string{
		"-y",
		"-user_agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"-headers", "Referer: https://en.xchina.co/\r\n",
		"-i", m3u8URL,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-f", "mp4",
		tmpPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cb(ProgressInfo{Status: "downloading", Downloaded: 0})

	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpPath)
		return 0, fmt.Errorf("ffmpeg failed: %v\n%s", err, string(out))
	}

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

// downloadXChinaThumb 下载封面图，最多重试 3 次（指数退避 2s/4s）
func downloadXChinaThumb(thumbURL, destPath string) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		lastErr = downloadXChinaThumbOnce(thumbURL, destPath)
		if lastErr == nil {
			return nil
		}
		log.Printf("[xscheduler] downloadXChinaThumb attempt %d failed: %v", attempt, lastErr)
		if attempt < 3 {
			time.Sleep(time.Duration(2*(1<<(attempt-1))) * time.Second)
		}
	}
	return lastErr
}

func downloadXChinaThumbOnce(thumbURL, destPath string) error {
	req, err := http.NewRequest("GET", thumbURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://en.xchina.co/")

	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("thumb returned %d", resp.StatusCode)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 20*1024*1024))
	f.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return copyErr
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// xcCaptureThumbFromVideo 用 ffmpeg 截取视频第 70% 位置的帧作为封面
func xcCaptureThumbFromVideo(videoPath, destPath string) error {
	ffprobeBin := xcLookupBin("ffprobe")
	ffmpegBin := xcLookupBin("ffmpeg")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	probeCmd := exec.CommandContext(ctx, ffprobeBin,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)
	probeOut, err := probeCmd.Output()
	if err != nil {
		return fmt.Errorf("ffprobe failed: %v", err)
	}

	var duration float64
	fmt.Sscanf(string(probeOut), "%f", &duration)
	if duration <= 0 {
		return fmt.Errorf("invalid duration %q", string(probeOut))
	}

	seekTime := duration * 0.7
	timeStr := fmt.Sprintf("%.3f", seekTime)

	out, err := exec.CommandContext(ctx, ffmpegBin,
		"-ss", timeStr,
		"-i", videoPath,
		"-vframes", "1",
		"-q:v", "2",
		"-y",
		destPath,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg capture failed: %v\n%s", err, string(out))
	}
	return nil
}

func xcLookupBin(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, candidate := range []string{"/usr/bin/" + name, "/usr/local/bin/" + name, "/bin/" + name} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return name
}

// markFailed 标记下载失败并设置退避重试时间
func (s *XScheduler) markFailed(dl db.Download, errMsg string) {
	current, err := s.db.GetDownload(dl.ID)
	if err != nil || current == nil {
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, errMsg)
		return
	}
	newCount := current.RetryCount + 1
	if newCount >= 3 {
		s.db.UpdateDownloadStatus(dl.ID, "permanent_failed", "", 0, errMsg)
		log.Printf("[xscheduler] Video %s marked permanent_failed after %d retries", dl.VideoID, newCount)
	} else {
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, errMsg)
		s.db.IncrementRetryCount(dl.ID, errMsg)
		s.db.SetNextRetryAt(dl.ID, current.RetryCount)
		delays := []int{15, 30, 60}
		delay := 60
		if current.RetryCount < len(delays) {
			delay = delays[current.RetryCount]
		}
		log.Printf("[xscheduler] Video %s failed, next retry in ~%dm (retry_count=%d)", dl.VideoID, delay, newCount)
	}
}
