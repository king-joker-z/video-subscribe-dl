package bscheduler

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/filter"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/util"
)

// ApplyConcurrencySettings 从 DB 读取并发配置并应用
func (s *BiliScheduler) ApplyConcurrencySettings() {
	if v, err := s.db.GetSetting("concurrent_video"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.videoSema = bilibili.NewSemaphore(n)
			log.Printf("[bscheduler] video 并发数: %d", n)
		}
	}
	if v, err := s.db.GetSetting("concurrent_page"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.pageSema = bilibili.NewSemaphore(n)
			log.Printf("[bscheduler] page 并发数: %d", n)
		}
	}
}

// getFilenameTemplate 获取文件名模板（从热配置或 DB）
func (s *BiliScheduler) getFilenameTemplate() string {
	if s.hotConfig != nil {
		snap := s.hotConfig.Get()
		if snap.FilenameTemplate != "" {
			return snap.FilenameTemplate
		}
	}
	if tmpl, err := s.db.GetSetting("filename_template"); err == nil && tmpl != "" {
		return tmpl
	}
	return "{{.Title}} [{{.BvID}}]"
}

func (s *BiliScheduler) clientForSource(src db.Source) *bilibili.Client {
	if src.CookiesFile != "" {
		cookie := bilibili.ReadCookieFile(src.CookiesFile)
		if cookie != "" {
			return bilibili.NewClient(cookie)
		}
	}
	return s.getBili()
}

func (s *BiliScheduler) checkDiskSpace() bool {
	minFreeGB := 1.0
	if v, err := s.db.GetSetting("min_disk_free_gb"); err == nil && v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed > 0 {
			minFreeGB = parsed
		}
	}
	free, err := util.GetDiskFree(s.downloadDir)
	if err != nil {
		log.Printf("[bscheduler][WARN] Disk space check failed: %v", err)
		return true
	}
	minFreeBytes := uint64(minFreeGB * 1024 * 1024 * 1024)
	if free < minFreeBytes {
		log.Printf("[bscheduler][WARN] Low disk space: %.2f GB free (threshold: %.1f GB)",
			float64(free)/1024/1024/1024, minFreeGB)
		s.notifier.Send(notify.EventDiskLow, "磁盘空间不足",
			fmt.Sprintf("剩余 %.2f GB，阈值 %.1f GB，下载已暂停",
				float64(free)/1024/1024/1024, minFreeGB))
		return false
	}
	return true
}

func (s *BiliScheduler) ensurePeopleDir(upInfo *bilibili.UPInfo) {
	if upInfo == nil || upInfo.Name == "" {
		return
	}
	dir := filepath.Join(s.downloadDir, "metadata", "people", bilibili.SanitizePath(upInfo.Name))
	os.MkdirAll(dir, 0755)
	nfo.GeneratePersonNFO(&nfo.PersonMeta{
		Name:  upInfo.Name,
		Thumb: upInfo.Face,
		MID:   upInfo.MID,
		Sign:  upInfo.Sign,
		Level: upInfo.Level,
		Sex:   upInfo.Sex,
	}, dir)
	if upInfo.Face != "" {
		avatarPath := filepath.Join(dir, "folder.jpg")
		if _, err := os.Stat(avatarPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(upInfo.Face, avatarPath); err != nil {
				log.Printf("[bscheduler] Avatar download failed for %s: %v", upInfo.Name, err)
			}
		}
	}
}

func (s *BiliScheduler) prepareVideoDir(uploaderDir, collectionName string) string {
	outputDir := filepath.Join(s.downloadDir, uploaderDir)
	if collectionName != "" {
		outputDir = filepath.Join(outputDir, collectionName)
	}
	return outputDir
}

// getUPInfoCached 带缓存的 UP 主信息获取
func (s *BiliScheduler) getUPInfoCached(client *bilibili.Client, mid int64) (*bilibili.UPInfo, error) {
	s.upInfoCacheMu.RLock()
	entry, ok := s.upInfoCache[mid]
	s.upInfoCacheMu.RUnlock()

	if ok {
		age := time.Since(entry.fetchedAt)
		if entry.info != nil && age < upInfoCacheTTL {
			return entry.info, nil
		}
		if entry.info == nil && entry.err != nil && age < upInfoErrorCacheTTL {
			return nil, entry.err
		}
	}

	info, err := client.GetUPInfo(mid)

	s.upInfoCacheMu.Lock()
	if err != nil {
		s.upInfoCache[mid] = &upInfoCacheEntry{info: nil, err: err, fetchedAt: time.Now()}
	} else {
		s.upInfoCache[mid] = &upInfoCacheEntry{info: info, fetchedAt: time.Now()}
	}
	s.upInfoCacheMu.Unlock()

	return info, err
}

// submitDownload 创建下载记录并提交到队列
func (s *BiliScheduler) submitDownload(src db.Source, videoID string, cid int64, title, pic, uploaderName, outputDir, cookiesFile string, detail *bilibili.VideoDetail, upInfo *bilibili.UPInfo) {
	skipNFO := src.SkipNFO
	skipPoster := src.SkipPoster

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

	if s.dl.IsPaused() {
		log.Printf("[bscheduler] Downloader paused, keeping %s as pending", videoID)
		return
	}

	resultCh := make(chan *downloader.Result, 1)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.handleDownloadResult(dlID, videoID, detail, upInfo, resultCh, skipNFO, skipPoster, nil)
	}()

	capturedDlID := dlID
	if err := s.dl.Submit(&downloader.Job{
		DownloadID:       capturedDlID,
		BvID:             strings.SplitN(videoID, "_P", 2)[0],
		CID:              cid,
		Title:            title,
		OutputDir:        outputDir,
		Quality:          src.DownloadQuality,
		Codec:            src.DownloadCodec,
		Danmaku:          src.DownloadDanmaku,
		Subtitle:         src.DownloadSubtitle,
		QualityMin:       src.DownloadQualityMin,
		SkipNFO:          src.SkipNFO,
		SkipPoster:       src.SkipPoster,
		UploaderName:     uploaderName,
		FilenameTemplate: s.getFilenameTemplate(),
		CookiesFile:      cookiesFile,
		ResultCh:         resultCh,
		OnStart:          func() { s.db.UpdateDownloadStatus(capturedDlID, "downloading", "", 0, "") },
	}); err != nil {
		log.Printf("[bscheduler] Queue full for %s, keeping pending for next sync", videoID)
		close(resultCh)
		return
	}
}

// submitDownloadFlat 用于多P视频的 Season 目录模式
func (s *BiliScheduler) submitDownloadFlat(src db.Source, videoID string, cid int64, title, pic, uploaderName, outputDir, cookiesFile string, detail *bilibili.VideoDetail, upInfo *bilibili.UPInfo, episodeMeta *nfo.EpisodeMeta) {
	if s.dl.IsPaused() {
		log.Printf("[bscheduler] Downloader paused, keeping %s as pending", videoID)
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
	capturedEpisodeMeta := episodeMeta
	go func() {
		defer s.wg.Done()
		s.handleDownloadResult(dlID, videoID, detail, upInfo, resultCh, skipNFO2, skipPoster2, capturedEpisodeMeta)
	}()

	capturedDlID := dlID
	if err := s.dl.Submit(&downloader.Job{
		DownloadID:       capturedDlID,
		BvID:             strings.SplitN(videoID, "_P", 2)[0],
		CID:              cid,
		Title:            title,
		OutputDir:        outputDir,
		Quality:          src.DownloadQuality,
		Codec:            src.DownloadCodec,
		Danmaku:          src.DownloadDanmaku,
		Subtitle:         src.DownloadSubtitle,
		QualityMin:       src.DownloadQualityMin,
		SkipNFO:          src.SkipNFO,
		SkipPoster:       src.SkipPoster,
		Flat:             true,
		UploaderName:     uploaderName,
		FilenameTemplate: s.getFilenameTemplate(),
		CookiesFile:      cookiesFile,
		ResultCh:         resultCh,
		OnStart:          func() { s.db.UpdateDownloadStatus(capturedDlID, "downloading", "", 0, "") },
	}); err != nil {
		log.Printf("[bscheduler] Queue full for %s, keeping pending for next sync", videoID)
		close(resultCh)
		return
	}
}

// processOneVideo 处理单个视频（API 调用 + 提交下载）
func (s *BiliScheduler) processOneVideo(src db.Source, client *bilibili.Client, bvid, title, pic, uploaderName, uploaderDir, collectionName string, upInfo *bilibili.UPInfo) {
	if s.videoSema != nil {
		s.videoSema.Acquire()
		defer s.videoSema.Release()
	}

	if src.DownloadFilter != "" && !filter.MatchesSimple(title, src.DownloadFilter) {
		return
	}

	advRules := filter.ParseRules(src.FilterRules)
	if len(advRules) > 0 {
		preInfo := filter.VideoInfo{Title: title}
		titleRules := make([]filter.Rule, 0)
		for _, r := range advRules {
			if r.Target == "title" {
				titleRules = append(titleRules, r)
			}
		}
		if !filter.MatchesRules(titleRules, preInfo) {
			return
		}
	}

	if !s.checkDiskSpace() {
		return
	}

	detail, err := client.GetVideoDetail(bvid)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.TriggerCooldown()
		} else {
			log.Printf("[bscheduler] Get detail failed for %s: %v", bvid, err)
		}
		return
	}

	tryUpower, _ := s.db.GetSetting("try_upower")
	if detail.IsChargePlus() && tryUpower != "true" {
		log.Printf("[bscheduler] 视频 %s (%s) 为充电专属/付费内容，跳过 (try_upower=false)", title, bvid)
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
		log.Printf("[bscheduler] No pages for %s, skipping", bvid)
		return
	}

	if len(advRules) > 0 {
		fullInfo := filter.VideoInfo{
			Title:    title,
			Duration: detail.Duration,
			Pages:    len(pages),
		}
		for _, r := range advRules {
			if r.Target == "tags" {
				if tags, err := client.GetVideoTags(bvid); err == nil {
					fullInfo.Tags = strings.Join(tags, ",")
				}
				break
			}
		}
		if !filter.MatchesRules(advRules, fullInfo) {
			log.Printf("[bscheduler] 视频 %s (%s) 未通过高级过滤规则，跳过", title, bvid)
			return
		}
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
		log.Printf("[bscheduler] New: %s (%s, cid=%d)", title, bvid, pages[0].CID)
		s.submitDownload(src, bvid, pages[0].CID, title, pic, uploaderName, outputDir, cookiesFile, detail, upInfo)
	} else {
		log.Printf("[bscheduler] Multi-part video: %s (%s, %d parts)", title, bvid, len(pages))
		multiPartBase := filepath.Join(outputDir, bilibili.SanitizeFilename(title)+" ["+bvid+"]")
		seasonDir := filepath.Join(multiPartBase, "Season 1")
		os.MkdirAll(seasonDir, 0755)

		if !src.SkipNFO {
			uploaderFace := ""
			if upInfo != nil {
				uploaderFace = upInfo.Face
			} else if detail != nil {
				uploaderFace = detail.Owner.Face
			}
			premiered := ""
			if detail != nil && detail.PubDate > 0 {
				premiered = time.Unix(detail.PubDate, 0).Format("2006-01-02")
			}
			tags, _ := s.getBili().GetVideoTags(bvid)
			nfo.GenerateTVShowNFO(&nfo.TVShowMeta{
				Title:        detail.Title,
				Plot:         detail.Desc,
				UploaderName: uploaderName,
				UploaderFace: uploaderFace,
				Premiered:    premiered,
				Poster:       pic,
				Tags:         tags,
			}, multiPartBase)
		}

		if !src.SkipPoster && pic != "" {
			posterPath := filepath.Join(multiPartBase, "poster.jpg")
			if _, err := os.Stat(posterPath); os.IsNotExist(err) {
				bilibili.DownloadFile(pic, posterPath)
			}
			fanartPath := filepath.Join(multiPartBase, "fanart.jpg")
			if _, err := os.Stat(fanartPath); os.IsNotExist(err) {
				bilibili.DownloadFile(pic, fanartPath)
			}
		}

		for _, page := range pages {
			partVideoID := fmt.Sprintf("%s_P%d", bvid, page.Page)
			exists, _ := s.db.IsVideoDownloaded(src.ID, partVideoID)
			if exists {
				continue
			}
			partTitle := fmt.Sprintf("S01E%02d - %s [%s]", page.Page, page.PartName, bvid)
			log.Printf("[bscheduler]   Part %d/%d: %s (cid=%d)", page.Page, len(pages), page.PartName, page.CID)

			epMeta := &nfo.EpisodeMeta{
				Title:        page.PartName,
				Season:       1,
				Episode:      page.Page,
				BvID:         bvid,
				UploaderName: uploaderName,
			}
			if detail != nil && detail.PubDate > 0 {
				epMeta.UploadDate = time.Unix(detail.PubDate, 0)
			}

			s.submitDownloadFlat(src, partVideoID, page.CID, partTitle, pic, uploaderName, seasonDir, cookiesFile, detail, upInfo, epMeta)
		}
	}
}

// handleDownloadResult 处理下载结果，生成 NFO 和缩略图
func (s *BiliScheduler) handleDownloadResult(dlID int64, videoID string, detail *bilibili.VideoDetail, upInfo *bilibili.UPInfo, ch chan *downloader.Result, skipNFO, skipPoster bool, episodeMeta *nfo.EpisodeMeta) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[bscheduler][PANIC] handleDownloadResult recovered: %v (videoID=%s)", r, videoID)
			s.db.UpdateDownloadStatus(dlID, "failed", "", 0, fmt.Sprintf("panic: %v", r))
			s.notifier.Send(notify.EventDownloadFailed, "下载处理异常: "+videoID, fmt.Sprintf("panic: %v", r))
		}
	}()

	timeout := 1 * time.Hour
	rateLimitBps := s.dl.GetRateLimit()
	if rateLimitBps > 0 {
		timeout = timeout + 5*time.Minute
	}

	var result *downloader.Result
	select {
	case result = <-ch:
	case <-time.After(timeout):
		if s.dl.IsPaused() {
			log.Printf("[bscheduler][TIMEOUT] 超时但 downloader 处于暂停状态 (videoID=%s)", videoID)
			s.db.UpdateDownloadStatus(dlID, "pending", "", 0, "timeout during pause, will retry")
			return
		}
		log.Printf("[bscheduler][TIMEOUT] 等待超时 (videoID=%s)", videoID)
		s.db.UpdateDownloadStatus(dlID, "failed", "", 0, fmt.Sprintf("download timeout (%v)", timeout))
		s.notifier.Send(notify.EventDownloadFailed, "下载超时: "+videoID, fmt.Sprintf("等待下载结果超过%v", timeout))
		return
	}
	if result == nil {
		log.Printf("[bscheduler] No result received for %s (channel closed)", videoID)
		return
	}
	if !result.Success {
		errMsg := ""
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		if strings.Contains(errMsg, "no video streams available") || strings.Contains(errMsg, "no suitable video stream") {
			log.Printf("[bscheduler] 充电专属/付费视频（无流）: %s - %s", videoID, errMsg)
			s.db.UpdateDownloadStatus(dlID, "charge_blocked", "", 0, "充电专属/付费视频（无可用流）")
			return
		}
		s.db.UpdateDownloadStatus(dlID, "failed", "", 0, errMsg)
		s.db.IncrementRetryCount(dlID, errMsg)
		log.Printf("[bscheduler] Failed: %s - %s", videoID, errMsg)
		s.notifier.Send(notify.EventDownloadFailed, "下载失败: "+videoID, errMsg)
		return
	}

	s.db.UpdateDownloadStatus(dlID, "completed", result.FilePath, result.FileSize, "")

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

	actualBvID := videoID
	if parts := strings.SplitN(videoID, "_P", 2); len(parts) == 2 {
		actualBvID = parts[0]
	}

	tags, _ := s.getBili().GetVideoTags(actualBvID)

	if skipNFO || detail == nil {
		if detail == nil {
			log.Printf("[bscheduler]   NFO skipped (detail is nil)")
		} else {
			log.Printf("[bscheduler]   NFO skipped (skip_nfo=true)")
		}
	} else if episodeMeta != nil {
		if err := nfo.GenerateEpisodeNFOFromPath(episodeMeta, result.FilePath); err != nil {
			log.Printf("[bscheduler] Episode NFO failed: %v", err)
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
			Platform:      "bilibili",
			BvID:          actualBvID,
			Title:         detail.Title,
			Description:   detail.Desc,
			UploaderName:  uploaderName,
			UploaderFace:  uploaderFace,
			UploadDate:    time.Unix(detail.PubDate, 0),
			Duration:      detail.Duration,
			Tags:          tags,
			ViewCount:     detail.Stat.View,
			LikeCount:     detail.Stat.Like,
			CoinCount:     detail.Stat.Coin,
			DanmakuCount:  detail.Stat.Danmaku,
			ReplyCount:    detail.Stat.Reply,
			FavoriteCount: detail.Stat.Favorite,
			ShareCount:    detail.Stat.Share,
			Thumbnail:     detail.Pic,
			WebpageURL:    fmt.Sprintf("https://www.bilibili.com/video/%s", actualBvID),
			TName:         detail.TName,
		}
		if err := nfo.GenerateVideoNFO(meta, result.FilePath); err != nil {
			log.Printf("[bscheduler] NFO failed: %v", err)
		}
	}

	detailPic := ""
	if detail != nil {
		detailPic = detail.Pic
	}
	if !skipPoster && detailPic != "" && result.FilePath != "" {
		ext := filepath.Ext(result.FilePath)
		thumbPath := strings.TrimSuffix(result.FilePath, ext) + "-thumb.jpg"
		if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(detailPic, thumbPath); err != nil {
				log.Printf("[bscheduler] Thumbnail download failed for %s: %v", videoID, err)
			} else {
				s.db.UpdateThumbPath(dlID, thumbPath)
				log.Printf("[bscheduler] Thumbnail saved: %s", thumbPath)
			}
		} else {
			s.db.UpdateThumbPath(dlID, thumbPath)
		}
	}

	statusBits := db.StatusBitVideo
	if !skipNFO && detail != nil {
		statusBits |= db.StatusBitNFO
	}
	if !skipPoster && detailPic != "" {
		statusBits |= db.StatusBitThumb
	}
	if result.DanmakuDone {
		statusBits |= db.StatusBitDanmaku
	}
	if result.SubtitleDone {
		statusBits |= db.StatusBitSubtitle
	}
	s.db.UpdateDetailStatus(dlID, statusBits)

	detailTitle := videoID
	if detail != nil {
		detailTitle = detail.Title
	}
	log.Printf("[bscheduler] Completed: %s -> %s", videoID, result.FilePath)
	s.notifier.Send(notify.EventDownloadComplete, "下载完成: "+detailTitle, result.FilePath)
}
