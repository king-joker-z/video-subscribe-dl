package phscheduler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// phProgressCallback PH 下载进度回调类型
type phProgressCallback func(info ProgressInfo)

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

// downloadPHFileWithProgress 下载 Pornhub 视频文件到 destPath，带断点续传、进度回调和 context 取消支持
func downloadPHFileWithProgress(ctx context.Context, fileURL, destPath string, downloadID int64, title string, onProgress phProgressCallback) (int64, error) {
	os.MkdirAll(filepath.Dir(destPath), 0755)

	// 如果文件已存在且非空，视为已完成
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

	tmpPath := destPath + ".tmp"

	// 断点续传：检查 .tmp 文件是否存在
	var startByte int64
	if fi, err := os.Stat(tmpPath); err == nil {
		startByte = fi.Size()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.pornhub.com/")
	req.Header.Set("Accept", "*/*")

	if startByte > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startByte))
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	// 接受 200 (全量) 或 206 (断点续传)
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return 0, fmt.Errorf("video download returned %d", resp.StatusCode)
	}

	// 如果服务器不支持 Range 且我们期望续传，重置起始位置
	if resp.StatusCode == 200 && startByte > 0 {
		startByte = 0
	}

	totalSize := resp.ContentLength
	if totalSize > 0 && startByte > 0 {
		totalSize += startByte // 还原为文件总大小
	}

	// 以追加模式打开 tmp 文件（续传时追加，新建时创建）
	var f *os.File
	if startByte > 0 {
		f, err = os.OpenFile(tmpPath, os.O_APPEND|os.O_WRONLY, 0644)
	} else {
		f, err = os.Create(tmpPath)
	}
	if err != nil {
		return 0, fmt.Errorf("create/open tmp file: %w", err)
	}

	if onProgress != nil {
		onProgress(ProgressInfo{
			DownloadID: downloadID,
			Title:      title,
			Status:     "downloading",
			Percent:    calcDownloadPercent(startByte, totalSize),
			Downloaded: startByte,
			Total:      totalSize,
		})
	}

	buf := make([]byte, 256*1024)
	written := startByte // 从续传位置开始计数
	lastProgressTime := time.Now()
	lastProgressBytes := written

	for {
		select {
		case <-ctx.Done():
			f.Close()
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

	if written == 0 || (written == startByte) {
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
