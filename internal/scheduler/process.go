package scheduler

import (
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

// fetchAndProcessSeason 统一处理合集（Season 类型）的翻页、去重、目录创建、NFO 生成、封面下载和视频遍历。
// checkSeason（独立合集源）和 processCollection（UP 主空间内合集）均委托此方法。
// 支持增量拉取：根据 source 的 latest_video_at 判断是否需要全量翻页。
func (s *Scheduler) fetchAndProcessSeason(src db.Source, client *bilibili.Client, mid, seasonID int64, uploaderName, uploaderDir string, upInfo *bilibili.UPInfo) {
	// 获取增量基准时间
	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	// 首次扫描页数限制
	firstScanPages := 0
	if val, err := s.db.GetSetting("first_scan_pages"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			firstScanPages = n
		}
	}

	var allArchives []bilibili.SeasonArchive
	var meta *bilibili.SeasonMeta
	page := 1
	pageSize := 100
	totalChecked := 0
	var maxPubDate int64
	stopped := false

	for {
		archives, m, err := client.GetSeasonVideos(mid, seasonID, page, pageSize)
		if err != nil {
			if bilibili.IsRiskControl(err) {
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("Get season %d page %d failed: %v", seasonID, page, err)
			break
		}
		if meta == nil {
			meta = m
		}

		for _, a := range archives {
			totalChecked++

			// 增量检查: 合集视频按发布时间倒序排列，遇到旧视频就停止
			if !isFirstScan && a.PubDate <= latestVideoAt {
				stopped = true
				break
			}

			if a.PubDate > maxPubDate {
				maxPubDate = a.PubDate
			}

			allArchives = append(allArchives, a)
		}

		if stopped {
			break
		}
		if len(archives) < pageSize {
			break
		}

		// 首次扫描页数限制
		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[season][首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}

		page++
		time.Sleep(time.Duration(300+rand.Intn(300)) * time.Millisecond)
	}

	if meta == nil {
		log.Printf("Get season %d failed: no metadata", seasonID)
		return
	}

	// 更新 latest_video_at
	if maxPubDate > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxPubDate); err != nil {
			log.Printf("[season][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	// 自动更新源名称
	if (src.Name == "" || src.Name == "未命名") && meta.Title != "" {
		src.Name = meta.Title
		s.db.UpdateSource(&src)
	}

	collectionName := bilibili.SanitizePath(meta.Title)
	collectionDir := filepath.Join(s.downloadDir, uploaderDir, collectionName)
	os.MkdirAll(collectionDir, 0755)

	if isFirstScan {
		log.Printf("[season][首次全量] %s by %s: %d 个视频 (翻页 %d)",
			meta.Title, uploaderName, len(allArchives), page)
	} else if stopped {
		log.Printf("[season][增量] %s by %s: %d 个新视频 (共检查 %d, 在第 %d 页停止)",
			meta.Title, uploaderName, len(allArchives), totalChecked, page)
	} else {
		log.Printf("[season][增量] %s by %s: %d 个新视频 (共检查 %d, 翻页 %d)",
			meta.Title, uploaderName, len(allArchives), totalChecked, page)
	}

	premiered := ""
	if len(allArchives) > 0 {
		premiered = time.Unix(allArchives[0].PubDate, 0).Format("2006-01-02")
	}
	if !src.SkipNFO {
		uploaderFace := ""
		if upInfo != nil {
			uploaderFace = upInfo.Face
		}
		nfo.GenerateTVShowNFO(&nfo.TVShowMeta{
			Title: meta.Title, Plot: meta.Intro, UploaderName: uploaderName,
			UploaderFace: uploaderFace, Premiered: premiered, Poster: meta.Cover,
		}, collectionDir)
	}

	if !src.SkipPoster && meta.Cover != "" {
		posterPath := filepath.Join(collectionDir, "poster.jpg")
		if _, err := os.Stat(posterPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(meta.Cover, posterPath); err != nil {
				log.Printf("Collection poster download failed: %v", err)
			} else {
				log.Printf("Collection poster saved: %s", posterPath)
			}
		}
	}

	for _, a := range allArchives {
		s.processOneVideo(src, client, a.BvID, a.Title, a.Pic, uploaderName, uploaderDir, collectionName, upInfo)
	}
}

// processCollection UP 主空间内合集，委托给 fetchAndProcessSeason
func (s *Scheduler) processCollection(src db.Source, client *bilibili.Client, mid, seasonID int64, uploaderName, uploaderDir string, upInfo *bilibili.UPInfo) {
	s.fetchAndProcessSeason(src, client, mid, seasonID, uploaderName, uploaderDir, upInfo)
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
// submitDownloadFlat 和 submitDownload 类似，但使用 Flat 模式（多P视频的 Season 目录）
func (s *Scheduler) submitDownloadFlat(src db.Source, videoID string, cid int64, title, pic, uploaderName, outputDir, cookiesFile string, detail *bilibili.VideoDetail, upInfo *bilibili.UPInfo) {
	if s.dl.IsPaused() {
		log.Printf("[scheduler] Downloader paused, keeping %s as pending", videoID)
		return
	}

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

	resultCh := make(chan *downloader.Result, 1)
	s.wg.Add(1)
	skipNFO2 := src.SkipNFO
	skipPoster2 := src.SkipPoster
	go func() {
		defer s.wg.Done()
		s.handleDownloadResult(dlID, videoID, detail, upInfo, resultCh, skipNFO2, skipPoster2)
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
		QualityMin:  src.DownloadQualityMin,
		SkipNFO:     src.SkipNFO,
		SkipPoster:  src.SkipPoster,
		Flat:        true,
		CookiesFile: cookiesFile,
		ResultCh:    resultCh,
		OnStart:     func() { s.db.UpdateDownloadStatus(capturedDlID, "downloading", "", 0, "") },
	}); err != nil {
		log.Printf("[scheduler] Queue full for %s, keeping pending for next sync", videoID)
		close(resultCh)
		return
	}
}

func (s *Scheduler) submitDownload(src db.Source, videoID string, cid int64, title, pic, uploaderName, outputDir, cookiesFile string, detail *bilibili.VideoDetail, upInfo *bilibili.UPInfo) {
	skipNFO := src.SkipNFO
	skipPoster := src.SkipPoster

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
		s.handleDownloadResult(dlID, videoID, detail, upInfo, resultCh, skipNFO, skipPoster)
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
		QualityMin:  src.DownloadQualityMin,
		SkipNFO:     src.SkipNFO,
		SkipPoster:  src.SkipPoster,
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
	// video 级别信号量: 控制同时处理的视频数
	if s.videoSema != nil {
		s.videoSema.Acquire()
		defer s.videoSema.Release()
	}

	// 标题过滤: 在 API 调用前预过滤，降低风控风险
	if src.DownloadFilter != "" && !matchesFilter(title, src.DownloadFilter) {
		return
	}

	if !s.checkDiskSpace() {
		return
	}

	detail, err := client.GetVideoDetail(bvid)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.triggerCooldown()
			s.dl.Pause()
		} else {
			log.Printf("Get detail failed for %s: %v", bvid, err)
		}
		return
	}

	// 充电专属/付费视频检查
	if detail.IsChargePlus() {
		log.Printf("视频 %s (%s) 为充电专属/付费内容，跳过下载", title, bvid)
		// 创建 charge_blocked 记录（不算失败）
		exists, _ := s.db.IsVideoDownloaded(src.ID, bvid)
		if !exists {
			dl := &db.Download{
				SourceID: src.ID, VideoID: bvid, Title: title,
				Uploader: uploaderName, Thumbnail: pic, Status: "charge_blocked",
			}
			s.db.CreateDownload(dl)
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
		// 多P视频目录结构: UP主/视频标题 [BVxxx]/Season 1/S01Exx - 分P标题 [BVxxx].mkv
		multiPartBase := filepath.Join(outputDir, bilibili.SanitizeFilename(title)+" ["+bvid+"]")
		seasonDir := filepath.Join(multiPartBase, "Season 1")
		os.MkdirAll(seasonDir, 0755)
		for _, page := range pages {
			partVideoID := fmt.Sprintf("%s_P%d", bvid, page.Page)
			exists, _ := s.db.IsVideoDownloaded(src.ID, partVideoID)
			if exists {
				continue
			}
			partTitle := fmt.Sprintf("S01E%02d - %s [%s]", page.Page, page.PartName, bvid)
			log.Printf("  Part %d/%d: %s (cid=%d)", page.Page, len(pages), page.PartName, page.CID)
			s.submitDownloadFlat(src, partVideoID, page.CID, partTitle, pic, uploaderName, seasonDir, cookiesFile, detail, upInfo)
		}
	}
}

func (s *Scheduler) handleDownloadResult(dlID int64, videoID string, detail *bilibili.VideoDetail, upInfo *bilibili.UPInfo, ch chan *downloader.Result, skipNFO, skipPoster bool) {
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
	// nil guard: detail 和 upInfo 可能在 API 异常时为 nil
	uploaderName := ""
	if upInfo != nil {
		uploaderName = upInfo.Name
	}
	if uploaderName == "" && detail != nil {
		uploaderName = detail.Owner.Name
	}
	if detail != nil {
		s.db.UpdateDownloadMeta(dlID, uploaderName, detail.Desc, detail.Pic, detail.Duration)
	} else {
		s.db.UpdateDownloadMeta(dlID, uploaderName, "", "", 0)
	}

	// 从 videoID 中提取真实 BV 号（多P格式为 BVxxx_P2）
	actualBvID := videoID
	if parts := strings.SplitN(videoID, "_P", 2); len(parts) == 2 {
		actualBvID = parts[0]
	}

	// 获取标签
	tags, _ := s.getBili().GetVideoTags(actualBvID)

	// 生成 NFO (受 SkipOption 控制)
	if skipNFO || detail == nil {
		if detail == nil {
			log.Printf("  NFO skipped (detail is nil)")
		} else {
			log.Printf("  NFO skipped (skip_nfo=true)")
		}
	} else {
	uploaderFace := ""
	if upInfo != nil {
		uploaderFace = upInfo.Face
	}
	if uploaderFace == "" {
		uploaderFace = detail.Owner.Face
	}
	meta := &nfo.VideoMeta{
		BvID: actualBvID, Title: detail.Title, Description: detail.Desc,
		UploaderName: uploaderName, UploaderFace: uploaderFace,
		UploadDate: time.Unix(detail.PubDate, 0), Duration: detail.Duration,
		Tags: tags, ViewCount: detail.Stat.View, LikeCount: detail.Stat.Like,
		Thumbnail:  detail.Pic,
		WebpageURL: fmt.Sprintf("https://www.bilibili.com/video/%s", actualBvID),
	}
	if err := nfo.GenerateVideoNFO(meta, result.FilePath); err != nil {
		log.Printf("NFO failed: %v", err)
	}
	} // end skipNFO

	// 下载封面图并记录路径 (受 SkipOption 控制)
	detailPic := ""
	if detail != nil {
		detailPic = detail.Pic
	}
	if !skipPoster && detailPic != "" && result.FilePath != "" {
		ext := filepath.Ext(result.FilePath)
		thumbPath := strings.TrimSuffix(result.FilePath, ext) + "-thumb.jpg"
		if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(detailPic, thumbPath); err != nil {
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

	detailTitle := videoID
	if detail != nil {
		detailTitle = detail.Title
	}
	log.Printf("Completed: %s -> %s", videoID, result.FilePath)
	s.notifier.Send(notify.EventDownloadComplete, "下载完成: "+detailTitle, result.FilePath)
}
