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

// retryOneDouyinDownload 执行单个抖音下载记录的重试/下载
func (s *Scheduler) retryOneDouyinDownload(dl db.Download) {
	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		log.Printf("[douyin-dl] Source %d not found for download %d, skipping", dl.SourceID, dl.ID)
		return
	}

	client := douyin.NewClient()

	// 获取视频详情
	s.db.UpdateDownloadStatus(dl.ID, "downloading", "", 0, "")
	detail, err := client.GetVideoDetail(dl.VideoID)
	if err != nil {
		log.Printf("[douyin-dl] GetVideoDetail failed for %s: %v", dl.VideoID, err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	// 解析最终下载 URL
	if detail.VideoURL == "" {
		errMsg := "no video URL found"
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, errMsg)
		s.db.IncrementRetryCount(dl.ID, errMsg)
		return
	}

	videoURL, err := client.ResolveVideoURL(detail.VideoURL)
	if err != nil {
		log.Printf("[douyin-dl] ResolveVideoURL failed: %v", err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	// 构建输出目录
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

	// 文件名
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

	// 下载视频
	fileSize, err := downloadDouyinFile(videoURL, videoFilePath)
	if err != nil {
		log.Printf("[douyin-dl] Download video failed: %v", err)
		s.db.UpdateDownloadStatus(dl.ID, "failed", "", 0, err.Error())
		s.db.IncrementRetryCount(dl.ID, err.Error())
		return
	}

	log.Printf("[douyin-dl] Downloaded: %s → %s (%.1f MB)", dl.VideoID, videoFilePath, float64(fileSize)/(1024*1024))

	// 下载封面
	if !src.SkipPoster && detail.Cover != "" {
		thumbPath := filepath.Join(outputDir, safeTitle+" ["+dl.VideoID+"]-poster.jpg")
		if err := downloadDouyinThumb(detail.Cover, thumbPath); err != nil {
			log.Printf("[douyin-dl] Download cover failed for %s: %v", dl.VideoID, err)
		}
	}

	// 生成 NFO
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

	// 更新 DB
	s.db.UpdateDownloadStatus(dl.ID, "completed", videoFilePath, fileSize, "")
	// 更新 thumbnail path
	// Update metadata (thumbnail, duration, uploader)
	s.db.UpdateDownloadMeta(dl.ID, uploaderName, detail.Desc, detail.Cover, detail.Duration/1000)

	s.notifier.Send(notify.EventDownloadComplete, "抖音视频下载完成: "+title,
		fmt.Sprintf("作者: %s\n大小: %.1f MB", uploaderName, float64(fileSize)/(1024*1024)))
}

// downloadDouyinFile 下载抖音视频 MP4 文件
func downloadDouyinFile(videoURL, destPath string) (int64, error) {
	// 确保目标目录存在
	os.MkdirAll(filepath.Dir(destPath), 0755)

	// 如果文件已存在且大于 0，跳过
	if info, err := os.Stat(destPath); err == nil && info.Size() > 0 {
		return info.Size(), nil
	}

	req, err := http.NewRequest("GET", videoURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.douyin.com/")

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

	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, fmt.Errorf("rename tmp: %w", err)
	}

	return written, nil
}

// downloadDouyinThumb 下载抖音封面图
func downloadDouyinThumb(thumbURL, destPath string) error {
	if _, err := os.Stat(destPath); err == nil {
		return nil // 已存在
	}

	req, err := http.NewRequest("GET", thumbURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

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
