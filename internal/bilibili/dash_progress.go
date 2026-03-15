package bilibili

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"video-subscribe-dl/internal/util"
)

// ProgressCallback 下载进度回调
// phase: "video" 或 "audio"
// downloaded: 已下载字节数
// total: 总字节数（可能为 -1 表示未知）
// speed: 当前下载速度（bytes/sec）
type ProgressCallback func(phase string, downloaded, total int64, speed float64)

// DownloadDashWithProgress 下载 DASH 流并合并为视频文件（带进度回调）
// rateLimitBps: bytes per second limit (0 = unlimited)
func DownloadDashWithProgress(ctx context.Context, video, audio *DashStream, outputDir, filename string, onProgress ProgressCallback, rateLimitBps int64) (string, error) {
	return DownloadDashWithProgressChunked(ctx, video, audio, outputDir, filename, onProgress, rateLimitBps, DefaultChunks)
}

// DownloadDashWithProgressChunked 下载 DASH 流（支持分块并行下载）
// numChunks: 并行分块数（仅大文件生效）
func DownloadDashWithProgressChunked(ctx context.Context, video, audio *DashStream, outputDir, filename string, onProgress ProgressCallback, rateLimitBps int64, numChunks int) (string, error) {
	os.MkdirAll(outputDir, 0755)

	videoTmp := filepath.Join(outputDir, ".video.m4s")
	audioTmp := filepath.Join(outputDir, ".audio.m4s")
	outputPath := filepath.Join(outputDir, filename+".mkv")

	// 下载视频流
	log.Printf("Downloading video: %dx%d %s (%d kbps)", video.Width, video.Height, video.Codecs, video.Bandwidth/1000)
	if err := downloadStreamWithProgressChunked(ctx, video, videoTmp, "video", onProgress, rateLimitBps, numChunks); err != nil {
		return "", fmt.Errorf("download video: %w", err)
	}

	// 下载音频流
	log.Printf("Downloading audio: %s (%d kbps)", audio.Codecs, audio.Bandwidth/1000)
	if err := downloadStreamWithProgressChunked(ctx, audio, audioTmp, "audio", onProgress, rateLimitBps, numChunks); err != nil {
		// 保留 videoTmp 供断点续传，不删除
		return "", fmt.Errorf("download audio: %w", err)
	}

	// ffmpeg 合并 — 通知前端
	if onProgress != nil {
		onProgress("merge", 0, 0, 0)
	}
	log.Printf("Merging: %s", outputPath)
	err := mergeStreams(videoTmp, audioTmp, outputPath)

	// 清理临时文件
	os.Remove(videoTmp)
	os.Remove(audioTmp)

	if err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}

	return outputPath, nil
}

func downloadStreamWithProgressChunked(ctx context.Context, stream *DashStream, dest, phase string, onProgress ProgressCallback, rateLimitBps int64, numChunks int) error {
	urls := []string{stream.BaseURL}
	urls = append(urls, stream.BackupURL...)

	for _, u := range urls {
		err := downloadSmartProgress(ctx, u, dest, phase, onProgress, rateLimitBps, numChunks)
		if err == nil {
			return nil
		}
		truncURL := u
		if len(truncURL) > 80 {
			truncURL = truncURL[:80]
		}
		log.Printf("Download failed from %s: %v, trying backup...", truncURL, err)
	}
	return fmt.Errorf("all URLs failed")
}

// downloadSmartProgress 智能选择单线程或分块下载
func downloadSmartProgress(ctx context.Context, rawURL, dest, phase string, onProgress ProgressCallback, rateLimitBps int64, numChunks int) error {
	// 先探测文件大小
	contentLength, err := getContentLength(rawURL)
	if err != nil {
		// 无法获取大小，回退单线程
		log.Printf("  Cannot get content length, fallback to single-thread: %v", err)
		return downloadWithResumeProgress(ctx, rawURL, dest, phase, onProgress, rateLimitBps)
	}

	// 大文件且 numChunks > 1：分块并行
	if contentLength > ChunkThreshold && numChunks > 1 {
		log.Printf("  Large file (%.1f MB > 50 MB), using %d-chunk parallel download",
			float64(contentLength)/1024/1024, numChunks)
		return downloadChunked(ctx, rawURL, dest, contentLength, numChunks, phase, onProgress, rateLimitBps)
	}

	// 小文件：单线程
	return downloadWithResumeProgress(ctx, rawURL, dest, phase, onProgress, rateLimitBps)
}

func downloadWithResumeProgress(ctx context.Context, rawURL, dest, phase string, onProgress ProgressCallback, rateLimitBps int64) error {
	client := &http.Client{Timeout: 0}

	var startByte int64
	if fi, err := os.Stat(dest); err == nil {
		startByte = fi.Size()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")
	if startByte > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startByte))
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if startByte > 0 && resp.StatusCode == 206 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		startByte = 0
	}

	f, err := os.OpenFile(dest, flags, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Apply rate limiting if configured
	var body io.Reader = resp.Body
	if rateLimitBps > 0 {
		body = util.NewRateLimitedReader(resp.Body, rateLimitBps)
	}

	totalSize := resp.ContentLength
	if totalSize > 0 {
		totalSize += startByte
	}
	written := startByte
	buf := make([]byte, 256*1024)
	lastLog := time.Now()
	lastProgressTime := time.Now()
	lastProgressBytes := written

	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			written += int64(n)

			now := time.Now()

			// 每秒更新一次进度回调
			if onProgress != nil && now.Sub(lastProgressTime) >= 500*time.Millisecond {
				elapsed := now.Sub(lastProgressTime).Seconds()
				speed := float64(written-lastProgressBytes) / elapsed
				onProgress(phase, written, totalSize, speed)
				lastProgressTime = now
				lastProgressBytes = written
			}

			// 日志输出（每 3 秒）
			if now.Sub(lastLog) > 3*time.Second {
				if totalSize > 0 {
					pct := float64(written) / float64(totalSize) * 100
					log.Printf("  %.1f%% (%s / %s)", pct, fmtBytes(written), fmtBytes(totalSize))
				} else {
					log.Printf("  %s downloaded", fmtBytes(written))
				}
				lastLog = now
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	// 最终进度更新
	if onProgress != nil {
		onProgress(phase, written, totalSize, 0)
	}

	return nil
}
