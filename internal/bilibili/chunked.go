package bilibili

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"errors"

	"video-subscribe-dl/internal/util"
)

const (
	// ChunkThreshold 分块下载阈值：20MB（参考 bili-sync）
	ChunkThreshold = 20 * 1024 * 1024
	// DefaultChunks 默认分块数
	DefaultChunks = 4
	// MaxChunkRetries 单块最大重试次数
	MaxChunkRetries = 3
)

// ErrRangeNotSatisfiable HTTP 416 Range Not Satisfiable
var ErrRangeNotSatisfiable = errors.New("HTTP 416 Range Not Satisfiable")

// chunkRange 描述一个下载块的字节范围
type chunkRange struct {
	Index int
	Start int64
	End   int64 // inclusive
}

// getContentLength 通过 HEAD 请求获取文件总大小
// 如果 HEAD 失败，回退用 GET + Range: bytes=0-0 探测
// sharedLargeDownloadClient 分块下载复用 sharedLargeDownloadClient（定义在 client.go）

func getContentLength(rawURL string) (int64, error) {
	client := sharedLargeDownloadClient // 复用 client.go 中的共享大文件下载 Client

	// 尝试 HEAD
	req, err := http.NewRequest("HEAD", rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")

	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 && resp.ContentLength > 0 {
			return resp.ContentLength, nil
		}
	}

	// 回退：GET Range 探测
	req2, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return 0, err
	}
	req2.Header.Set("User-Agent", randUA())
	req2.Header.Set("Referer", "https://www.bilibili.com")
	req2.Header.Set("Origin", "https://www.bilibili.com")
	req2.Header.Set("Range", "bytes=0-0")

	resp2, err := client.Do(req2)
	if err != nil {
		return 0, err
	}
	defer resp2.Body.Close()
	io.Copy(io.Discard, resp2.Body)

	if resp2.StatusCode == 206 {
		// Content-Range: bytes 0-0/TOTAL
		cr := resp2.Header.Get("Content-Range")
		var total int64
		if _, err := fmt.Sscanf(cr, "bytes 0-0/%d", &total); err == nil && total > 0 {
			return total, nil
		}
	}

	return 0, fmt.Errorf("cannot determine content length")
}

// downloadChunked 分块并行下载
// numChunks: 并行块数
// rateLimitBps: 总限速（平分给每个块）
// phase: "video" 或 "audio"
func downloadChunked(ctx context.Context, rawURL, dest string, totalSize int64, numChunks int, phase string, onProgress ProgressCallback, rateLimitBps int64) error {
	if numChunks < 1 {
		numChunks = DefaultChunks
	}
	if numChunks > 16 {
		numChunks = 16
	}

	// 计算每块范围
	chunkSize := totalSize / int64(numChunks)
	chunks := make([]chunkRange, numChunks)
	for i := 0; i < numChunks; i++ {
		chunks[i] = chunkRange{
			Index: i,
			Start: int64(i) * chunkSize,
			End:   int64(i+1)*chunkSize - 1,
		}
	}
	// [FIXED: P2-1] 最后一块的 End 修正为 totalSize-1，补偿整除截断（totalSize % numChunks 的余量）
	chunks[numChunks-1].End = totalSize - 1 // 最后一块取到末尾

	log.Printf("  Chunked download: %s, %d chunks, %.1f MB total",
		phase, numChunks, float64(totalSize)/1024/1024)

	// 每块的临时文件
	chunkFiles := make([]string, numChunks)
	for i := range chunks {
		chunkFiles[i] = fmt.Sprintf("%s.chunk%d", dest, i)
	}

	// 进度追踪
	var totalDownloaded int64
	var progressMu sync.Mutex
	lastProgressTime := time.Now()
	lastProgressBytes := int64(0)

	updateProgress := func() {
		if onProgress == nil {
			return
		}
		progressMu.Lock()
		now := time.Now()
		downloaded := atomic.LoadInt64(&totalDownloaded)
		elapsed := now.Sub(lastProgressTime).Seconds()
		var speed float64
		if elapsed > 0.3 {
			speed = float64(downloaded-lastProgressBytes) / elapsed
			lastProgressTime = now
			lastProgressBytes = downloaded
		}
		progressMu.Unlock()
		onProgress(phase, downloaded, totalSize, speed)
	}

	// 并行下载所有块
	var wg sync.WaitGroup
	errs := make([]error, numChunks)

	// 每块的限速 = 总限速 / 块数
	perChunkRate := int64(0)
	if rateLimitBps > 0 {
		perChunkRate = rateLimitBps / int64(numChunks)
		if perChunkRate < 1024 {
			perChunkRate = 1024 // 最低 1KB/s
		}
	}

	for i := range chunks {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = downloadOneChunk(ctx, rawURL, chunkFiles[idx], chunks[idx], perChunkRate, &totalDownloaded, updateProgress)
		}(i)
	}
	wg.Wait()

	// 检查是否有块失败
	for i, err := range errs {
		if err != nil {
			// 清理所有块文件
			for _, f := range chunkFiles {
				if removeErr := os.Remove(f); removeErr != nil && !os.IsNotExist(removeErr) {
					log.Printf("[WARN] Failed to remove chunk file %s: %v", f, removeErr)
				}
			}
			return fmt.Errorf("chunk %d failed: %w", i, err)
		}
	}

	// 合并所有块
	// [FIXED: P0-3] 用 succeeded 标志位 + defer 确保合并失败时清理所有剩余块文件，避免泄露
	log.Printf("  Merging %d chunks...", numChunks)
	outFile, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer outFile.Close()

	succeeded := false
	defer func() {
		if !succeeded {
			// 合并失败时清理所有尚未删除的块文件
			for _, cf := range chunkFiles {
				if removeErr := os.Remove(cf); removeErr != nil && !os.IsNotExist(removeErr) {
					log.Printf("[WARN] Failed to cleanup chunk file %s: %v", cf, removeErr)
				}
			}
		}
	}()

	for i, cf := range chunkFiles {
		f, err := os.Open(cf)
		if err != nil {
			return fmt.Errorf("open chunk %d: %w", i, err)
		}
		if _, err := io.Copy(outFile, f); err != nil {
			f.Close()
			return fmt.Errorf("copy chunk %d: %w", i, err)
		}
		f.Close()
		if removeErr := os.Remove(cf); removeErr != nil && !os.IsNotExist(removeErr) {
			log.Printf("[WARN] Failed to remove chunk file after merge %s: %v", cf, removeErr)
		}
	}
	succeeded = true

	// 最终进度
	if onProgress != nil {
		onProgress(phase, totalSize, totalSize, 0)
	}

	log.Printf("  Chunked download complete: %s (%.1f MB)", phase, float64(totalSize)/1024/1024)
	return nil
}

// downloadOneChunk 下载单个块，失败自动重试
func downloadOneChunk(ctx context.Context, rawURL, dest string, chunk chunkRange, rateLimitBps int64, totalDownloaded *int64, updateProgress func()) error {
	var lastErr error
	for attempt := 0; attempt <= MaxChunkRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*2) * time.Second
			log.Printf("  Chunk %d retry %d/%d (wait %v)", chunk.Index, attempt, MaxChunkRetries, delay)
			// 重试前重置该块在 totalDownloaded 中已累加的字节数，避免进度虚报
			if fi, err := os.Stat(dest); err == nil {
				atomic.AddInt64(totalDownloaded, -fi.Size())
			}
			time.Sleep(delay)
		}
		lastErr = downloadOneChunkAttempt(ctx, rawURL, dest, chunk, rateLimitBps, totalDownloaded, updateProgress)
		if lastErr == nil {
			return nil
		}
		// HTTP 416: Range 参数不变重试无意义，立即返回
		if errors.Is(lastErr, ErrRangeNotSatisfiable) {
			return lastErr
		}
	}
	return fmt.Errorf("after %d retries: %w", MaxChunkRetries, lastErr)
}

// downloadOneChunkAttempt 单次下载尝试
func downloadOneChunkAttempt(ctx context.Context, rawURL, dest string, chunk chunkRange, rateLimitBps int64, totalDownloaded *int64, updateProgress func()) error {
	client := sharedLargeDownloadClient

	// 检查已下载的部分（支持块内断点续传）
	// [FIXED: P0-1] 只计入此次 attempt 新增的字节，避免每次 retry 都把历史字节重复加入 totalDownloaded
	var startByte int64 = chunk.Start
	if fi, err := os.Stat(dest); err == nil {
		downloaded := fi.Size()
		// 如果已完成这个块，直接返回，不再计数（外层已统计过）
		expected := chunk.End - chunk.Start + 1
		if downloaded >= expected {
			return nil
		}
		// 续传：只记录已有进度（首次进入此 attempt，前面没有计过）
		startByte = chunk.Start + downloaded
		atomic.AddInt64(totalDownloaded, downloaded)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", startByte, chunk.End))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 416 {
		return ErrRangeNotSatisfiable
	}
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if startByte > chunk.Start {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
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

	buf := make([]byte, 128*1024) // 128KB buffer
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			atomic.AddInt64(totalDownloaded, int64(n))
			updateProgress()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	return nil
}
