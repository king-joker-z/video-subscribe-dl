package scheduler

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/util"
)

func (s *Scheduler) processCollection(src db.Source, client *bilibili.Client, mid, seasonID int64, uploaderName, uploaderDir string, upInfo *bilibili.UPInfo) {
	// 全量翻页获取合集所有视频
	var allArchives []bilibili.SeasonArchive
	var meta *bilibili.SeasonMeta
	seasonPage := 1
	seasonPageSize := 100
	for {
		archives, m, err := client.GetSeasonVideos(mid, seasonID, seasonPage, seasonPageSize)
		if err != nil {
			if errors.Is(err, bilibili.ErrRateLimited) {
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("Get season %d page %d failed: %v", seasonID, seasonPage, err)
			break
		}
		if meta == nil {
			meta = m
		}
		allArchives = append(allArchives, archives...)
		if len(archives) < seasonPageSize {
			break
		}
		seasonPage++
		time.Sleep(time.Duration(300+rand.Intn(300)) * time.Millisecond)
	}
	archives := allArchives
	if meta == nil {
		log.Printf("Get season %d failed: no metadata", seasonID)
		return
	}

	collectionName := bilibili.SanitizePath(meta.Title)
	collectionDir := filepath.Join(s.downloadDir, uploaderDir, collectionName)
	os.MkdirAll(collectionDir, 0755)
	log.Printf("Collection: %s (%d videos)", collectionName, len(archives))

	premiered := ""
	if len(archives) > 0 {
		premiered = time.Unix(archives[0].PubDate, 0).Format("2006-01-02")
	}
	nfo.GenerateTVShowNFO(&nfo.TVShowMeta{
		Title: meta.Title, Plot: meta.Intro, UploaderName: uploaderName,
		UploaderFace: upInfo.Face, Premiered: premiered, Poster: meta.Cover,
	}, collectionDir)

	if meta.Cover != "" {
		posterPath := filepath.Join(collectionDir, "poster.jpg")
		if _, err := os.Stat(posterPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(meta.Cover, posterPath); err != nil {
				log.Printf("Collection poster download failed: %v", err)
			} else {
				log.Printf("Collection poster saved: %s", posterPath)
			}
		}
	}

	for _, a := range archives {
		s.processOneVideo(src, client, a.BvID, a.Title, a.Pic, uploaderName, uploaderDir, collectionName, upInfo)
	}
}

// checkDiskSpace returns true if disk has enough free space
func (s *Scheduler) checkDiskSpace() bool {
	minFreeGB := 1.0
	if v, err := s.db.GetSetting("min_disk_free_gb"); err == nil && v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed > 0 {
			minFreeGB = parsed
		}
	}
	free, err := util.GetDiskFree(s.downloadDir)
	if err != nil {
		log.Printf("[WARN] Disk space check failed: %v", err)
		return true // don't block downloads on check failure
	}
	minFreeBytes := uint64(minFreeGB * 1024 * 1024 * 1024)
	if free < minFreeBytes {
		log.Printf("[WARN] Low disk space: %.2f GB free (threshold: %.1f GB). Downloads paused.", float64(free)/1024/1024/1024, minFreeGB)
		s.notifier.Send(notify.EventDiskLow, "磁盘空间不足",
			fmt.Sprintf("剩余 %.2f GB，阈值 %.1f GB，下载已暂停", float64(free)/1024/1024/1024, minFreeGB))
		return false
	}
	return true
}

// prepareVideoDir 构建视频输出目录
func (s *Scheduler) prepareVideoDir(uploaderDir, collectionName string) string {
	outputDir := filepath.Join(s.downloadDir, uploaderDir)
	if collectionName != "" {
		outputDir = filepath.Join(outputDir, collectionName)
	}
	return outputDir
}

// submitDownload 创建下载记录并提交到队列
func (s *Scheduler) submitDownload(src db.Source, videoID string, cid int64, title, pic, uploaderName, outputDir, cookiesFile string, detail *bilibili.VideoDetail, upInfo *bilibili.UPInfo) {
	// 检查是否已有 pending 记录（容器重启后保留的）
	existingID, _ := s.db.GetPendingDownloadID(src.ID, videoID)
	var dlID int64
	if existingID > 0 {
		dlID = existingID
	} else {
		dl := &db.Download{
			SourceID: src.ID, VideoID: videoID, Title: title,
			Uploader: uploaderName, Thumbnail: pic, Status: "pending",
		}
		dlID, _ = s.db.CreateDownload(dl)
	}

	// 暂停时不提交到下载队列，保持 pending 等下轮处理
	if s.dl.IsPaused() {
		log.Printf("[scheduler] Downloader paused, keeping %s as pending", videoID)
		return
	}

	resultCh := make(chan *downloader.Result, 1)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.handleDownloadResult(dlID, videoID, detail, upInfo, resultCh)
	}()

	capturedDlID := dlID
	if err := s.dl.Submit(&downloader.Job{
		BvID:        strings.SplitN(videoID, "_P", 2)[0],
		CID:         cid,
		Title:       title,
		OutputDir:   outputDir,
		Quality:     src.DownloadQuality,
		Codec:       src.DownloadCodec,
		Danmaku:     src.DownloadDanmaku,
		CookiesFile: cookiesFile,
		ResultCh:    resultCh,
		OnStart:     func() { s.db.UpdateDownloadStatus(capturedDlID, "downloading", "", 0, "") },
	}); err != nil {
		// 队列满时保持 pending，下次同步会重新提交
		log.Printf("[scheduler] Queue full for %s, keeping pending for next sync", videoID)
		close(resultCh)
		return
	}
}

func (s *Scheduler) processOneVideo(src db.Source, client *bilibili.Client, bvid, title, pic, uploaderName, uploaderDir, collectionName string, upInfo *bilibili.UPInfo) {
	if !s.checkDiskSpace() {
		return
	}

	detail, err := client.GetVideoDetail(bvid)
	if err != nil {
		if errors.Is(err, bilibili.ErrRateLimited) {
			s.triggerCooldown()
			s.dl.Pause()
		} else {
			log.Printf("Get detail failed for %s: %v", bvid, err)
		}
		return
	}

	pages := bilibili.GetAllPages(detail)
	if len(pages) == 0 {
		log.Printf("No pages for %s, skipping", bvid)
		return
	}

	outputDir := s.prepareVideoDir(uploaderDir, collectionName)
	cookiesFile := src.CookiesFile
	if cookiesFile == "" {
		cookiesFile = s.cookiePath
	}

	if len(pages) == 1 {
		exists, _ := s.db.IsVideoDownloaded(src.ID, bvid)
		if exists {
			return
		}
		log.Printf("New: %s (%s, cid=%d)", title, bvid, pages[0].CID)
		s.submitDownload(src, bvid, pages[0].CID, title, pic, uploaderName, outputDir, cookiesFile, detail, upInfo)
	} else {
		log.Printf("Multi-part video: %s (%s, %d parts)", title, bvid, len(pages))
		for _, page := range pages {
			partVideoID := fmt.Sprintf("%s_P%d", bvid, page.Page)
			exists, _ := s.db.IsVideoDownloaded(src.ID, partVideoID)
			if exists {
				continue
			}
			partTitle := fmt.Sprintf("P%d %s", page.Page, page.PartName)
			log.Printf("  Part %d/%d: %s (cid=%d)", page.Page, len(pages), page.PartName, page.CID)
			multiPartDir := filepath.Join(outputDir, bilibili.SanitizeFilename(title)+" ["+bvid+"]")
			s.submitDownload(src, partVideoID, page.CID, partTitle, pic, uploaderName, multiPartDir, cookiesFile, detail, upInfo)
		}
	}
}

func (s *Scheduler) handleDownloadResult(dlID int64, videoID string, detail *bilibili.VideoDetail, upInfo *bilibili.UPInfo, ch chan *downloader.Result) {
	// panic 保护：避免 goroutine panic 导致 WaitGroup 永远阻塞
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] handleDownloadResult recovered: %v (videoID=%s)", r, videoID)
			s.db.UpdateDownloadStatus(dlID, "failed", "", 0, fmt.Sprintf("panic: %v", r))
			s.notifier.Send(notify.EventDownloadFailed, "下载处理异常: "+videoID, fmt.Sprintf("panic: %v", r))
		}
	}()

	// 超时保护：动态计算（默认1小时，限速时根据码率调整）
	timeout := 1 * time.Hour
	rateLimitBps := s.dl.GetRateLimit()
	if rateLimitBps > 0 {
		// 给 handleDownloadResult 额外 5 分钟缓冲
		timeout = timeout + 5*time.Minute
	}

	var result *downloader.Result
	select {
	case result = <-ch:
		// 正常收到结果
	case <-time.After(timeout):
		// 暂停状态下超时不算失败，标记 pending 等恢复后重试
		if s.dl.IsPaused() {
			log.Printf("[TIMEOUT] handleDownloadResult 超时但 downloader 处于暂停状态 (videoID=%s, paused=true, activeWorkers=%d, queueLen=%d)",
				videoID, s.dl.ActiveCount(), s.dl.QueueLen())
			s.db.UpdateDownloadStatus(dlID, "pending", "", 0, "timeout during pause, will retry")
			return
		}
		log.Printf("[TIMEOUT] handleDownloadResult 等待超时 (videoID=%s, paused=%v, activeWorkers=%d, queueLen=%d)",
			videoID, s.dl.IsPaused(), s.dl.ActiveCount(), s.dl.QueueLen())
		s.db.UpdateDownloadStatus(dlID, "failed", "", 0, fmt.Sprintf("download timeout (%v)", timeout))
		s.notifier.Send(notify.EventDownloadFailed, "下载超时: "+videoID, fmt.Sprintf("等待下载结果超过%v", timeout))
		return
	}
	if result == nil {
		// channel was closed without sending a result (e.g. queue full)
		log.Printf("[scheduler] No result received for %s (channel closed)", videoID)
		return
	}
	if !result.Success {
		errMsg := ""
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		s.db.UpdateDownloadStatus(dlID, "failed", "", 0, errMsg)
		s.db.IncrementRetryCount(dlID, errMsg)
		log.Printf("Failed: %s - %s", videoID, errMsg)
		s.notifier.Send(notify.EventDownloadFailed, "下载失败: "+videoID, errMsg)
		return
	}

	s.db.UpdateDownloadStatus(dlID, "completed", result.FilePath, result.FileSize, "")

	// 优先使用订阅源的 UP 主名字，保持同一订阅源下 uploader 一致
	// detail.Owner.Name 可能是视频原作者（转载/合作视频时不同）
	uploaderName := upInfo.Name
	if uploaderName == "" {
		uploaderName = detail.Owner.Name
	}
	s.db.UpdateDownloadMeta(dlID, uploaderName, detail.Desc, detail.Pic, detail.Duration)

	// 从 videoID 中提取真实 BV 号（多P格式为 BVxxx_P2）
	actualBvID := videoID
	if parts := strings.SplitN(videoID, "_P", 2); len(parts) == 2 {
		actualBvID = parts[0]
	}

	// 获取标签
	tags, _ := s.getBili().GetVideoTags(actualBvID)

	// 生成 NFO
	meta := &nfo.VideoMeta{
		BvID: actualBvID, Title: detail.Title, Description: detail.Desc,
		UploaderName: uploaderName, UploaderFace: detail.Owner.Face,
		UploadDate: time.Unix(detail.PubDate, 0), Duration: detail.Duration,
		Tags: tags, ViewCount: detail.Stat.View, LikeCount: detail.Stat.Like,
		Thumbnail:  detail.Pic,
		WebpageURL: fmt.Sprintf("https://www.bilibili.com/video/%s", actualBvID),
	}
	if err := nfo.GenerateVideoNFO(meta, result.FilePath); err != nil {
		log.Printf("NFO failed: %v", err)
	}

	// 下载封面图并记录路径
	if detail.Pic != "" && result.FilePath != "" {
		ext := filepath.Ext(result.FilePath)
		thumbPath := strings.TrimSuffix(result.FilePath, ext) + "-thumb.jpg"
		if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(detail.Pic, thumbPath); err != nil {
				log.Printf("Thumbnail download failed for %s: %v", videoID, err)
			} else {
				s.db.UpdateThumbPath(dlID, thumbPath)
				log.Printf("Thumbnail saved: %s", thumbPath)
			}
		} else {
			// 已存在，也更新路径到数据库
			s.db.UpdateThumbPath(dlID, thumbPath)
		}
	}

	log.Printf("Completed: %s -> %s", videoID, result.FilePath)
	s.notifier.Send(notify.EventDownloadComplete, "下载完成: "+detail.Title, result.FilePath)
}
