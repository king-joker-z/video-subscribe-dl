package dscheduler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// defaultDouyinDownloadClient 复用的 HTTP Client，避免每次下载新建 transport
// [FIXED: DS-2] 提升为包级变量初始化一次复用
var defaultDouyinDownloadClient = &http.Client{Timeout: 10 * time.Minute}

// progressCallback 抖音下载进度回调类型
type progressCallback func(info ProgressInfo)

// calcDownloadSpeed 计算当前下载速度 (bytes/sec)
func calcDownloadSpeed(deltaBytes int64, elapsedSecs float64) float64 {
	if elapsedSecs <= 0 {
		return 0
	}
	return float64(deltaBytes) / elapsedSecs
}

// calcDownloadPercent 计算下载百分比
func calcDownloadPercent(downloaded, total int64) float64 {
	if total <= 0 || downloaded <= 0 {
		return 0
	}
	pct := float64(downloaded) / float64(total) * 100
	if pct > 100 {
		return 100
	}
	return pct
}

// downloadFileWithProgress 下载抖音视频文件到 destPath，带进度回调和 context 取消支持
func downloadFileWithProgress(ctx context.Context, fileURL, destPath string, downloadID int64, title string, onProgress progressCallback) (int64, error) {
	os.MkdirAll(filepath.Dir(destPath), 0755)

	if info, err := os.Stat(destPath); err == nil && info.Size() > 0 {
		if onProgress != nil {
			onProgress(ProgressInfo{
				DownloadID: downloadID,
				Status:     "done",
				Percent:    100,
				Downloaded: info.Size(),
				Total:      info.Size(),
			})
		}
		return info.Size(), nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Referer", "https://www.douyin.com/")
	req.Header.Set("Accept", "*/*")

	resp, err := defaultDouyinDownloadClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("video download returned %d", resp.StatusCode)
	}

	totalSize := resp.ContentLength

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("create tmp file: %w", err)
	}

	if onProgress != nil {
		onProgress(ProgressInfo{
			DownloadID: downloadID,
			Title:      title,
			Status:     "downloading",
			Percent:    0,
			Downloaded: 0,
			Total:      totalSize,
		})
	}

	buf := make([]byte, 256*1024)
	var written int64
	lastProgressTime := time.Now()
	lastProgressBytes := int64(0)

	for {
		select {
		case <-ctx.Done():
			f.Close()
			os.Remove(tmpPath)
			return 0, ctx.Err()
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				f.Close()
				os.Remove(tmpPath)
				return 0, fmt.Errorf("write: %w", writeErr)
			}
			written += int64(n)

			now := time.Now()
			if onProgress != nil && now.Sub(lastProgressTime) >= 500*time.Millisecond {
				elapsed := now.Sub(lastProgressTime).Seconds()
				speed := calcDownloadSpeed(written-lastProgressBytes, elapsed)
				pct := calcDownloadPercent(written, totalSize)
				onProgress(ProgressInfo{
					DownloadID: downloadID,
					Title:      title,
					Status:     "downloading",
					Percent:    pct,
					Speed:      int64(speed),
					Downloaded: written,
					Total:      totalSize,
				})
				lastProgressTime = now
				lastProgressBytes = written
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			os.Remove(tmpPath)
			return 0, fmt.Errorf("read: %w", readErr)
		}
	}

	f.Close()

	if written == 0 {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("downloaded 0 bytes")
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, fmt.Errorf("rename tmp: %w", err)
	}

	if onProgress != nil {
		onProgress(ProgressInfo{
			DownloadID: downloadID,
			Title:      title,
			Status:     "done",
			Percent:    100,
			Speed:      0,
			Downloaded: written,
			Total:      written,
		})
	}

	return written, nil
}
