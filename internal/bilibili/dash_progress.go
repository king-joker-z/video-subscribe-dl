package bilibili

import (
	"context"
	"fmt"
	"sync"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"errors"

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
// 视频和音频流并行下载，完成后 ffmpeg 合并
func DownloadDashWithProgressChunked(ctx context.Context, video, audio *DashStream, outputDir, filename string, onProgress ProgressCallback, rateLimitBps int64, numChunks int) (string, error) {
	os.MkdirAll(outputDir, 0755)

	videoTmp := filepath.Join(outputDir, ".video.m4s")
	audioTmp := filepath.Join(outputDir, ".audio.m4s")
	outputPath := filepath.Join(outputDir, filename+".mkv")

	// 并行下载视频和音频流
	var videoErr, audioErr error
	var wg sync.WaitGroup

	// 限速平分给视频和音频
	videoRateLimit := rateLimitBps
	audioRateLimit := rateLimitBps
	if rateLimitBps > 0 {
		videoRateLimit = rateLimitBps * 3 / 4 // 视频分 75%
		audioRateLimit = rateLimitBps / 4      // 音频分 25%
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("Downloading video: %dx%d %s (%d kbps)", video.Width, video.Height, video.Codecs, video.Bandwidth/1000)
		videoErr = downloadStreamWithProgressChunked(ctx, video, videoTmp, "video", onProgress, videoRateLimit, numChunks)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("Downloading audio: %s (%d kbps)", audio.Codecs, audio.Bandwidth/1000)
		audioErr = downloadStreamWithProgressChunked(ctx, audio, audioTmp, "audio", onProgress, audioRateLimit, numChunks)
	}()

	wg.Wait()

	if videoErr != nil {
		// [FIXED: P2-2] Clean up both temp files when either download fails
		os.Remove(videoTmp)
		os.Remove(audioTmp)
		return "", fmt.Errorf("download video: %w", videoErr)
	}
	if audioErr != nil {
		// [FIXED: P2-2] Clean up both temp files when either download fails
		os.Remove(videoTmp)
		os.Remove(audioTmp)
		return "", fmt.Errorf("download audio: %w", audioErr)
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
	// 使用 CDN 优先级排序: upos > cn > mcdn > pcdn
	urls := StreamURLs(stream, true)

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
		log.Printf("  Large file (%.1f MB > 20 MB), using %d-chunk parallel download",
			float64(contentLength)/1024/1024, numChunks)
		err := downloadChunked(ctx, rawURL, dest, contentLength, numChunks, phase, onProgress, rateLimitBps)
		if err != nil && errors.Is(err, ErrRangeNotSatisfiable) {
			log.Printf("  HTTP 416 in chunked download, fallback to single-thread")
			os.Remove(dest) // 清理可能的部分文件
			return downloadWithResumeProgress(ctx, rawURL, dest, phase, onProgress, rateLimitBps)
		}
		return err
	}

	// 小文件：单线程
	return downloadWithResumeProgress(ctx, rawURL, dest, phase, onProgress, rateLimitBps)
}

// max416Retries 断点续传遇 416 后删文件重试的最大次数
const max416Retries = 2

func downloadWithResumeProgress(ctx context.Context, rawURL, dest, phase string, onProgress ProgressCallback, rateLimitBps int64) error {
	for attempt := 0; attempt <= max416Retries; attempt++ {
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

		resp, err := sharedLargeDownloadClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode == 416 {
			resp.Body.Close()
			// 文件可能已完成或损坏，删除后重试
			if startByte > 0 {
				if rmErr := os.Remove(dest); rmErr != nil {
					return fmt.Errorf("416 and cannot remove %s: %w", dest, rmErr)
				}
				log.Printf("  HTTP 416 at %s, removed partial file (%d bytes), retry %d/%d",
					phase, startByte, attempt+1, max416Retries)
				continue
			}
			return ErrRangeNotSatisfiable
		}
		if resp.StatusCode != 200 && resp.StatusCode != 206 {
			resp.Body.Close()
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		err = downloadStreamToFile(resp, dest, startByte, phase, onProgress, rateLimitBps)
		resp.Body.Close()
		return err
	}
	return ErrRangeNotSatisfiable
}

// downloadStreamToFile 将 HTTP 响应体写入文件（带进度回调和限速）
func downloadStreamToFile(resp *http.Response, dest string, startByte int64, phase string, onProgress ProgressCallback, rateLimitBps int64) error {
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

			if onProgress != nil && now.Sub(lastProgressTime) >= 500*time.Millisecond {
				elapsed := now.Sub(lastProgressTime).Seconds()
				speed := float64(written-lastProgressBytes) / elapsed
				onProgress(phase, written, totalSize, speed)
				lastProgressTime = now
				lastProgressBytes = written
			}

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

	if onProgress != nil {
		onProgress(phase, written, totalSize, 0)
	}

	return nil
}
