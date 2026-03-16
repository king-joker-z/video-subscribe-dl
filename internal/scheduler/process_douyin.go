package scheduler

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
)

// retryOneDouyinDownload 执行单个抖音下载
// 与 B站 DASH 不同，抖音视频是直接 MP4 下载（更简单但风控更严）
func (s *Scheduler) retryOneDouyinDownload(dl db.Download) {
	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[douyin-dl] Source %d not found for download %d, skipping", dl.SourceID, dl.ID)
		return
	}

	client := douyin.NewClient()

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
		log.Printf("[douyin-dl] GetVideoDetail failed after retries for %s: %v", dl.VideoID, err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	// 图集暂时跳过（Phase 1 只处理视频）
	if detail.IsNote || detail.VideoURL == "" {
		log.Printf("[douyin-dl] Skipping note/image post %s (no video)", dl.VideoID)
		s.db.UpdateDownloadStatus(dl.ID, "completed", "", 0, "skipped: image/note post")
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
	os.MkdirAll(outputDir, 0755)

	title := detail.Desc
	if title == "" {
		title = dl.Title
	}
	if title == "" {
		title = fmt.Sprintf("douyin_%s", dl.VideoID)
	}
	safeTitle := douyin.SanitizePath(title)
	if len(safeTitle) > 100 {
		safeTitle = safeTitle[:100]
	}
	videoFilePath := filepath.Join(outputDir, safeTitle+" ["+dl.VideoID+"].mp4")

	// Step 4: 下载视频（带重试）
	var fileSize int64
	for attempt := 1; attempt <= 3; attempt++ {
		fileSize, err = downloadDouyinFile(videoURL, videoFilePath)
		if err == nil {
			break
		}
		log.Printf("[douyin-dl] Download attempt %d failed: %v", attempt, err)
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
		thumbPath := filepath.Join(outputDir, safeTitle+" ["+dl.VideoID+"]-poster.jpg")
		if err := downloadDouyinThumb(detail.Cover, thumbPath); err != nil {
			log.Printf("[douyin-dl] Download cover failed for %s: %v", dl.VideoID, err)
		}
	}

	// Step 6: 生成 NFO
	if !src.SkipNFO {
		meta := &nfo.VideoMeta{
			BvID:         dl.VideoID,
			Title:        title,
			Description:  detail.Desc,
			UploaderName: uploaderName,
			UploadDate:   detail.CreateTimeUnix(),
			Duration:     detail.Duration / 1000,
			Thumbnail:    detail.Cover,
			WebpageURL:   fmt.Sprintf("https://www.douyin.com/video/%s", dl.VideoID),
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

// downloadDouyinFile 下载抖音视频 MP4
func downloadDouyinFile(videoURL, destPath string) (int64, error) {
	os.MkdirAll(filepath.Dir(destPath), 0755)

	// 已存在且非空则跳过
	if info, err := os.Stat(destPath); err == nil && info.Size() > 0 {
		return info.Size(), nil
	}

	req, err := http.NewRequest("GET", videoURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Referer", "https://www.douyin.com/")
	req.Header.Set("Accept", "*/*")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http get video: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("video download returned %d", resp.StatusCode)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("create tmp file: %w", err)
	}

	written, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("write video: %w", err)
	}

	if written == 0 {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("downloaded 0 bytes")
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, fmt.Errorf("rename tmp: %w", err)
	}

	return written, nil
}

// downloadDouyinThumb 下载封面图
func downloadDouyinThumb(thumbURL, destPath string) error {
	if _, err := os.Stat(destPath); err == nil {
		return nil
	}

	req, err := http.NewRequest("GET", thumbURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("thumb download returned %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}
