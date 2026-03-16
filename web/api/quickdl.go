package api

import (
	"fmt"
	"math/rand"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
)

// douyinMaxConcurrent 限制抖音快速下载的最大并发数
const douyinMaxConcurrent = 3

// QuickDownloadHandler 单视频快速下载 API
type QuickDownloadHandler struct {
	db            *db.DB
	downloadDir   string
	downloader    *downloader.Downloader
	notifier      *notify.Notifier
	getBiliClient func() *bilibili.Client
	douyinSem    chan struct{} // 抖音下载并发信号量
}

func NewQuickDownloadHandler(database *db.DB, dl *downloader.Downloader, downloadDir string) *QuickDownloadHandler {
	return &QuickDownloadHandler{
		db:          database,
		downloadDir: downloadDir,
		downloader:  dl,
		douyinSem:   make(chan struct{}, douyinMaxConcurrent),
	}
}

func (h *QuickDownloadHandler) SetBiliClientFunc(fn func() *bilibili.Client) {
	h.getBiliClient = fn
}

func (h *QuickDownloadHandler) SetNotifier(n *notify.Notifier) {
	h.notifier = n
}

// POST /api/download — 快速下载单个视频
func (h *QuickDownloadHandler) HandleQuickDownload(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := parseJSON(r, &req); err != nil || req.URL == "" {
		apiError(w, CodeInvalidParam, "请提供视频 URL 或 BV/AV 号")
		return
	}

	// 抖音链接检测（优先）
	if douyin.IsDouyinURL(req.URL) {
		h.handleDouyinQuickDownload(w, req.URL)
		return
	}

	// 解析 BV/AV 号
	bvid, avid, err := bilibili.ExtractBVID(req.URL)
	if err != nil {
		apiError(w, CodeInvalidParam, "无法识别该链接，请输入 B站/抖音 视频链接")
		return
	}

	// 获取 bilibili client
	var client *bilibili.Client
	if h.getBiliClient != nil {
		client = h.getBiliClient()
	}
	if client == nil {
		apiError(w, CodeInternal, "bilibili 客户端未初始化")
		return
	}

	// AV 号转 BV 号
	if bvid == "" && avid > 0 {
		resolved, err := client.AV2BV(avid)
		if err != nil {
			apiError(w, CodeInternal, fmt.Sprintf("AV 号解析失败: %v", err))
			return
		}
		bvid = resolved
	}

	// 检查是否已存在
	var existingID int64
	err = h.db.QueryRow(
		"SELECT id FROM downloads WHERE video_id = ? OR video_id LIKE ? LIMIT 1",
		bvid, bvid+"_P%",
	).Scan(&existingID)
	if err == nil && existingID > 0 {
		dl, _ := h.db.GetDownload(existingID)
		if dl != nil && (dl.Status == "completed" || dl.Status == "relocated" || dl.Status == "downloading") {
			apiOK(w, map[string]interface{}{
				"exists":  true,
				"id":      existingID,
				"status":  dl.Status,
				"title":   dl.Title,
				"message": fmt.Sprintf("该视频已存在（%s）", dl.Status),
			})
			return
		}
	}

	// 获取视频详情
	detail, err := client.GetVideoDetail(bvid)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			apiError(w, CodeInternal, "触发 B 站风控，请稍后再试")
			return
		}
		apiError(w, CodeInternal, fmt.Sprintf("获取视频详情失败: %v", err))
		return
	}

	if detail.IsBangumi() {
		apiError(w, CodeInvalidParam, "该视频为番剧/影视内容，暂不支持下载")
		return
	}
	if detail.IsUnavailable() {
		apiError(w, CodeInvalidParam, "该视频不可用（可能已被删除或审核中）")
		return
	}

	tryUpower, _ := h.db.GetSetting("try_upower")
	if detail.IsChargePlus() && tryUpower != "true" {
		apiError(w, CodeInvalidParam, "该视频为充电专属/付费内容，无法下载")
		return
	}

	uploaderName := detail.Owner.Name
	uploaderDir := bilibili.SanitizePath(uploaderName)

	pages := bilibili.GetAllPages(detail)
	if len(pages) == 0 {
		apiError(w, CodeInternal, "无法获取视频分P信息")
		return
	}

	// 读取全局下载设置
	quality, _ := h.db.GetSetting("download_quality")
	if quality == "" {
		quality = "best"
	}
	codec, _ := h.db.GetSetting("download_codec")
	if codec == "" {
		codec = "all"
	}
	danmakuStr, _ := h.db.GetSetting("download_danmaku")
	danmaku := danmakuStr == "true"
	subtitleStr, _ := h.db.GetSetting("download_subtitle")
	subtitle := subtitleStr == "true"
	qualityMin, _ := h.db.GetSetting("download_quality_min")
	skipNFOStr, _ := h.db.GetSetting("skip_nfo")
	skipNFO := skipNFOStr == "true"
	skipPosterStr, _ := h.db.GetSetting("skip_poster")
	skipPoster := skipPosterStr == "true"
	filenameTemplate, _ := h.db.GetSetting("filename_template")

	cookiesFile := ""
	if cp, _ := h.db.GetSetting("cookie_path"); cp != "" {
		cookiesFile = cp
	}

	// source_id=0 表示快速下载（无订阅源关联）
	var downloadIDs []int64
	outputDir := filepath.Join(h.downloadDir, uploaderDir)

	if len(pages) == 1 {
		dl := &db.Download{
			SourceID:  0,
			VideoID:   bvid,
			Title:     detail.Title,
			Uploader:  uploaderName,
			Thumbnail: detail.Pic,
			Status:    "pending",
		}
		dlID, err := h.db.CreateDownload(dl)
		if err != nil {
			apiError(w, CodeInternal, fmt.Sprintf("创建下载记录失败: %v", err))
			return
		}
		downloadIDs = append(downloadIDs, dlID)

		resultCh := make(chan *downloader.Result, 1)
		capturedDlID := dlID
		go h.handleResult(capturedDlID, bvid, detail, uploaderName, resultCh, skipNFO, skipPoster, nil)

		if err := h.downloader.Submit(&downloader.Job{
			DownloadID:       capturedDlID,
			BvID:             bvid,
			CID:              pages[0].CID,
			Title:            detail.Title,
			OutputDir:        outputDir,
			Quality:          quality,
			Codec:            codec,
			Danmaku:          danmaku,
			Subtitle:         subtitle,
			QualityMin:       qualityMin,
			SkipNFO:          skipNFO,
			SkipPoster:       skipPoster,
			UploaderName:     uploaderName,
			FilenameTemplate: filenameTemplate,
			CookiesFile:      cookiesFile,
			ResultCh:         resultCh,
			OnStart:          func() { h.db.UpdateDownloadStatus(capturedDlID, "downloading", "", 0, "") },
		}); err != nil {
			close(resultCh)
			apiError(w, CodeInternal, "下载队列已满，请稍后再试")
			return
		}
	} else {
		multiPartBase := filepath.Join(outputDir, bilibili.SanitizeFilename(detail.Title)+" ["+bvid+"]")
		seasonDir := filepath.Join(multiPartBase, "Season 1")
		os.MkdirAll(seasonDir, 0755)

		if !skipNFO {
			premiered := ""
			if detail.PubDate > 0 {
				premiered = time.Unix(detail.PubDate, 0).Format("2006-01-02")
			}
			tags, _ := client.GetVideoTags(bvid)
			nfo.GenerateTVShowNFO(&nfo.TVShowMeta{
				Title:        detail.Title,
				Plot:         detail.Desc,
				UploaderName: uploaderName,
				UploaderFace: detail.Owner.Face,
				Premiered:    premiered,
				Poster:       detail.Pic,
				Tags:         tags,
			}, multiPartBase)
		}

		if !skipPoster && detail.Pic != "" {
			posterPath := filepath.Join(multiPartBase, "poster.jpg")
			if _, err := os.Stat(posterPath); os.IsNotExist(err) {
				bilibili.DownloadFile(detail.Pic, posterPath)
			}
			fanartPath := filepath.Join(multiPartBase, "fanart.jpg")
			if _, err := os.Stat(fanartPath); os.IsNotExist(err) {
				bilibili.DownloadFile(detail.Pic, fanartPath)
			}
		}

		for _, page := range pages {
			partVideoID := fmt.Sprintf("%s_P%d", bvid, page.Page)
			partTitle := fmt.Sprintf("S01E%02d - %s [%s]", page.Page, page.PartName, bvid)

			dl := &db.Download{
				SourceID:  0,
				VideoID:   partVideoID,
				Title:     partTitle,
				Uploader:  uploaderName,
				Thumbnail: detail.Pic,
				Status:    "pending",
			}
			dlID, err := h.db.CreateDownload(dl)
			if err != nil {
				continue
			}
			downloadIDs = append(downloadIDs, dlID)

			epMeta := &nfo.EpisodeMeta{
				Title:        page.PartName,
				Season:       1,
				Episode:      page.Page,
				BvID:         bvid,
				UploaderName: uploaderName,
			}
			if detail.PubDate > 0 {
				epMeta.UploadDate = time.Unix(detail.PubDate, 0)
			}

			resultCh := make(chan *downloader.Result, 1)
			capturedDlID := dlID
			go h.handleResult(capturedDlID, partVideoID, detail, uploaderName, resultCh, skipNFO, skipPoster, epMeta)

			if err := h.downloader.Submit(&downloader.Job{
				DownloadID:       capturedDlID,
				BvID:             bvid,
				CID:              page.CID,
				Title:            partTitle,
				OutputDir:        seasonDir,
				Quality:          quality,
				Codec:            codec,
				Danmaku:          danmaku,
				Subtitle:         subtitle,
				QualityMin:       qualityMin,
				SkipNFO:          skipNFO,
				SkipPoster:       skipPoster,
				Flat:             true,
				UploaderName:     uploaderName,
				FilenameTemplate: filenameTemplate,
				CookiesFile:      cookiesFile,
				ResultCh:         resultCh,
				OnStart:          func() { h.db.UpdateDownloadStatus(capturedDlID, "downloading", "", 0, "") },
			}); err != nil {
				close(resultCh)
				log.Printf("[quickdl] Queue full for %s", partVideoID)
			}
		}
	}

	log.Printf("[quickdl] Quick download: %s (%s) by %s, %d parts",
		detail.Title, bvid, uploaderName, len(pages))

	apiOK(w, map[string]interface{}{
		"bvid":     bvid,
		"title":    detail.Title,
		"uploader": uploaderName,
		"duration": detail.Duration,
		"pic":      detail.Pic,
		"pages":    len(pages),
		"ids":      downloadIDs,
		"message":  fmt.Sprintf("已提交下载: %s (%d 个分P)", detail.Title, len(pages)),
	})
}

// handleDouyinQuickDownload 处理抖音单视频快速下载
func (h *QuickDownloadHandler) handleDouyinQuickDownload(w http.ResponseWriter, rawURL string) {
	client := douyin.NewClient()

	// 解析分享链接，获取 aweme_id
	resolved, err := client.ResolveShareURL(rawURL)
	if err != nil {
		apiError(w, CodeInternal, fmt.Sprintf("解析抖音链接失败: %v", err))
		return
	}

	var awemeID string
	switch resolved.Type {
	case douyin.URLTypeVideo:
		awemeID = resolved.VideoID
	case douyin.URLTypeUser:
		apiError(w, CodeInvalidParam, "这是抖音用户主页链接，请使用订阅源添加。快速下载仅支持单个视频链接")
		return
	default:
		apiError(w, CodeInvalidParam, "无法识别该抖音链接，请提供单个视频的链接")
		return
	}

	if awemeID == "" {
		apiError(w, CodeInvalidParam, "无法从链接中提取视频 ID")
		return
	}

	// 检查是否已存在
	var existingID int64
	h.db.QueryRow(
		"SELECT id FROM downloads WHERE video_id = ? LIMIT 1", awemeID,
	).Scan(&existingID)
	if existingID > 0 {
		dl, _ := h.db.GetDownload(existingID)
		if dl != nil && (dl.Status == "completed" || dl.Status == "relocated" || dl.Status == "downloading") {
			apiOK(w, map[string]interface{}{
				"exists":   true,
				"id":       existingID,
				"status":   dl.Status,
				"title":    dl.Title,
				"message":  fmt.Sprintf("该视频已存在（%s）", dl.Status),
				"platform": "douyin",
			})
			return
		}
	}

	// 获取视频详情
	detail, err := client.GetVideoDetail(awemeID)
	if err != nil {
		apiError(w, CodeInternal, fmt.Sprintf("获取抖音视频详情失败: %v", err))
		return
	}

	// 图集也支持快速下载（Phase 2）
	if !detail.IsNote && detail.VideoURL == "" {
		apiError(w, CodeInvalidParam, "该内容无可下载的视频或图片")
		return
	}

	// 创建下载记录
	title := detail.Desc
	if title == "" {
		title = fmt.Sprintf("douyin_%s", awemeID)
	}
	uploaderName := detail.Author.Nickname
	if uploaderName == "" {
		uploaderName = "未知作者"
	}

	dl := &db.Download{
		SourceID:  0,
		VideoID:   awemeID,
		Title:     title,
		Uploader:  uploaderName,
		Thumbnail: detail.Cover,
		Status:    "pending",
		Duration:  detail.Duration / 1000,
	}
	dlID, err := h.db.CreateDownload(dl)
	if err != nil {
		apiError(w, CodeInternal, fmt.Sprintf("创建下载记录失败: %v", err))
		return
	}

	// 异步执行下载
	go h.executeDouyinDownload(dlID, awemeID, detail, client)

	log.Printf("[quickdl·douyin] Quick download: %s (%s) by %s", title, awemeID, uploaderName)

	apiOK(w, map[string]interface{}{
		"aweme_id": awemeID,
		"title":    title,
		"uploader": uploaderName,
		"duration": detail.Duration / 1000,
		"pic":      detail.Cover,
		"pages":    1,
		"ids":      []int64{dlID},
		"platform":    "douyin",
		"is_note":     detail.IsNote,
		"image_count": len(detail.Images),
		"message":     fmt.Sprintf("已提交下载: %s", title),
	})
}

// executeDouyinDownload 异步执行抖音视频下载
func (h *QuickDownloadHandler) executeDouyinDownload(dlID int64, awemeID string, detail *douyin.DouyinVideo, client *douyin.DouyinClient) {
	// 获取信号量（限制并发）
	h.douyinSem <- struct{}{}
	defer func() { <-h.douyinSem }()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] quickdl·douyin recovered: %v (awemeID=%s)", r, awemeID)
			h.db.UpdateDownloadStatus(dlID, "failed", "", 0, fmt.Sprintf("panic: %v", r))
			// panic 时也发 SSE 失败事件
			h.downloader.EmitEvent(downloader.DownloadEvent{
				Type:  "failed",
				BvID:  awemeID,
				Title: detail.Desc,
				Error: fmt.Sprintf("panic: %v", r),
			})
		}
	}()

	h.db.UpdateDownloadStatus(dlID, "downloading", "", 0, "")

	// 图集走独立下载路径
	if detail.IsNote && len(detail.Images) > 0 {
		h.executeDouyinNoteDownload(dlID, awemeID, detail)
		return
	}

	// 解析最终下载 URL
	videoURL, err := client.ResolveVideoURL(detail.VideoURL)
	if err != nil {
		log.Printf("[quickdl·douyin] ResolveVideoURL failed: %v", err)
		h.db.UpdateDownloadStatus(dlID, "failed", "", 0, err.Error())
		h.db.IncrementRetryCount(dlID, err.Error())
		h.downloader.EmitEvent(downloader.DownloadEvent{
			Type:  "failed",
			BvID:  awemeID,
			Title: detail.Desc,
			Error: err.Error(),
		})
		return
	}

	// 构建输出路径
	uploaderName := detail.Author.Nickname
	if uploaderName == "" {
		uploaderName = "未知作者"
	}
	uploaderDir := douyin.SanitizePath(uploaderName)
	outputDir := filepath.Join(h.downloadDir, uploaderDir)
	os.MkdirAll(outputDir, 0755)

	title := detail.Desc
	if title == "" {
		title = fmt.Sprintf("douyin_%s", awemeID)
	}
	safeTitle := douyin.SanitizePath(title)
	if len(safeTitle) > 100 {
		safeTitle = safeTitle[:100]
	}
	videoFilePath := filepath.Join(outputDir, safeTitle+" ["+awemeID+"].mp4")

	// 下载视频（带重试）
	var fileSize int64
	for attempt := 1; attempt <= 3; attempt++ {
		fileSize, err = quickDownloadDouyinFile(videoURL, videoFilePath)
		if err == nil {
			break
		}
		log.Printf("[quickdl·douyin] Download attempt %d failed: %v", attempt, err)
		if attempt < 3 {
			backoff := time.Duration(10*(1<<(attempt-1))) * time.Second
			time.Sleep(backoff)
		}
	}
	if err != nil {
		log.Printf("[quickdl·douyin] Download failed after retries: %v", err)
		h.db.UpdateDownloadStatus(dlID, "failed", "", 0, err.Error())
		h.db.IncrementRetryCount(dlID, err.Error())
		h.downloader.EmitEvent(downloader.DownloadEvent{
			Type:  "failed",
			BvID:  awemeID,
			Title: detail.Desc,
			Error: err.Error(),
		})
		if h.notifier != nil {
			h.notifier.Send(notify.EventDownloadFailed, "抖音视频下载失败: "+detail.Desc, err.Error())
		}
		return
	}

	log.Printf("[quickdl·douyin] Downloaded: %s -> %s (%.1f MB)", awemeID, videoFilePath, float64(fileSize)/(1024*1024))

	// 下载封面
	skipPosterStr, _ := h.db.GetSetting("skip_poster")
	skipPoster := skipPosterStr == "true"
	if !skipPoster && detail.Cover != "" {
		thumbPath := filepath.Join(outputDir, safeTitle+" ["+awemeID+"]-poster.jpg")
		if err := quickDownloadDouyinThumb(detail.Cover, thumbPath); err != nil {
			log.Printf("[quickdl·douyin] Download cover failed: %v", err)
		} else {
			h.db.UpdateThumbPath(dlID, thumbPath)
		}
	}

	// 生成 NFO
	skipNFOStr, _ := h.db.GetSetting("skip_nfo")
	skipNFO := skipNFOStr == "true"
	if !skipNFO {
		meta := &nfo.VideoMeta{
			BvID:         awemeID,
			Title:        title,
			Description:  detail.Desc,
			UploaderName: uploaderName,
			UploadDate:   detail.CreateTimeUnix(),
			Duration:     detail.Duration / 1000,
			Thumbnail:    detail.Cover,
			WebpageURL:   fmt.Sprintf("https://www.douyin.com/video/%s", awemeID),
			LikeCount:    detail.DiggCount,
			ShareCount:   detail.ShareCount,
			ReplyCount:   detail.CommentCount,
		}
		if err := nfo.GenerateVideoNFO(meta, videoFilePath); err != nil {
			log.Printf("[quickdl·douyin] Generate NFO failed: %v", err)
		}
	}

	// 更新 DB
	h.db.UpdateDownloadStatus(dlID, "completed", videoFilePath, fileSize, "")
	h.db.UpdateDownloadMeta(dlID, uploaderName, detail.Desc, detail.Cover, detail.Duration/1000)

	statusBits := db.StatusBitVideo
	if !skipNFO {
		statusBits |= db.StatusBitNFO
	}
	if !skipPoster && detail.Cover != "" {
		statusBits |= db.StatusBitThumb
	}
	h.db.UpdateDetailStatus(dlID, statusBits)

	if h.notifier != nil {
		h.notifier.Send(notify.EventDownloadComplete, "抖音视频下载完成: "+title,
			fmt.Sprintf("作者: %s\n大小: %.1f MB", uploaderName, float64(fileSize)/(1024*1024)))
	}

	// 发送 SSE 事件通知前端
	h.downloader.EmitEvent(downloader.DownloadEvent{
		Type:     "completed",
		BvID:     awemeID,
		Title:    title,
		FileSize: fileSize,
	})

	log.Printf("[quickdl·douyin] Completed: %s -> %s", awemeID, videoFilePath)
}

// executeDouyinNoteDownload 异步执行抖音图集下载
func (h *QuickDownloadHandler) executeDouyinNoteDownload(dlID int64, awemeID string, detail *douyin.DouyinVideo) {
	// 获取信号量（限制并发）— 注意：图集走这里时，信号量已在 executeDouyinDownload 中获取
	// 但 executeDouyinNoteDownload 从 executeDouyinDownload 内部调用，所以不需要再获取

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] quickdl·douyin-note recovered: %v (awemeID=%s)", r, awemeID)
			h.db.UpdateDownloadStatus(dlID, "failed", "", 0, fmt.Sprintf("panic: %v", r))
			h.downloader.EmitEvent(downloader.DownloadEvent{
				Type:  "failed",
				BvID:  awemeID,
				Title: detail.Desc,
				Error: fmt.Sprintf("panic: %v", r),
			})
		}
	}()

	uploaderName := detail.Author.Nickname
	if uploaderName == "" {
		uploaderName = "未知作者"
	}
	uploaderDir := douyin.SanitizePath(uploaderName)

	title := detail.Desc
	if title == "" {
		title = fmt.Sprintf("douyin_%s", awemeID)
	}
	safeTitle := douyin.SanitizePath(title)
	if len(safeTitle) > 100 {
		safeTitle = safeTitle[:100]
	}

	noteDir := filepath.Join(h.downloadDir, uploaderDir, safeTitle+" ["+awemeID+"]")
	os.MkdirAll(noteDir, 0755)

	log.Printf("[quickdl·douyin-note] Downloading %d images for %s", len(detail.Images), awemeID)

	var totalSize int64
	successCount := 0

	for i, imgURL := range detail.Images {
		if imgURL == "" {
			continue
		}
		ext := ".jpg"
		if strings.Contains(imgURL, ".png") {
			ext = ".png"
		} else if strings.Contains(imgURL, ".webp") {
			ext = ".webp"
		}
		imgPath := filepath.Join(noteDir, fmt.Sprintf("%02d%s", i+1, ext))

		var fileSize int64
		var err error
		for attempt := 1; attempt <= 3; attempt++ {
			fileSize, err = quickDownloadDouyinFile(imgURL, imgPath)
			if err == nil {
				break
			}
			if attempt < 3 {
				time.Sleep(time.Duration(3*(1<<(attempt-1))) * time.Second)
			}
		}
		if err != nil {
			log.Printf("[quickdl·douyin-note] Failed image %d: %v", i+1, err)
			continue
		}
		totalSize += fileSize
		successCount++
		if i < len(detail.Images)-1 {
			time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)
		}
	}

	if successCount == 0 {
		h.db.UpdateDownloadStatus(dlID, "failed", "", 0, "all images download failed")
		h.db.IncrementRetryCount(dlID, "all images download failed")
		h.downloader.EmitEvent(downloader.DownloadEvent{
			Type:  "failed",
			BvID:  awemeID,
			Title: detail.Desc,
			Error: "all images download failed",
		})
		return
	}

	// 下载封面
	skipPosterStr, _ := h.db.GetSetting("skip_poster")
	if skipPosterStr != "true" && detail.Cover != "" {
		coverPath := filepath.Join(noteDir, "cover.jpg")
		quickDownloadDouyinThumb(detail.Cover, coverPath)
	}

	// 生成 NFO
	skipNFOStr, _ := h.db.GetSetting("skip_nfo")
	if skipNFOStr != "true" {
		meta := &nfo.VideoMeta{
			BvID: awemeID, Title: title, Description: detail.Desc,
			UploaderName: uploaderName, UploadDate: detail.CreateTimeUnix(),
			Thumbnail: detail.Cover,
			WebpageURL: fmt.Sprintf("https://www.douyin.com/note/%s", awemeID),
			LikeCount: detail.DiggCount, ShareCount: detail.ShareCount, ReplyCount: detail.CommentCount,
		}
		nfoPath := filepath.Join(noteDir, safeTitle+" ["+awemeID+"].nfo")
		nfo.GenerateMovieNFO(meta, nfoPath)
	}

	h.db.UpdateDownloadStatus(dlID, "completed", noteDir, totalSize, "")
	h.db.UpdateDownloadMeta(dlID, uploaderName, detail.Desc, detail.Cover, 0)

	if h.notifier != nil {
		h.notifier.Send(notify.EventDownloadComplete,
			fmt.Sprintf("抖音图集下载完成: %s (%d张)", title, successCount),
			fmt.Sprintf("作者: %s\n大小: %.1f MB", uploaderName, float64(totalSize)/(1024*1024)))
	}

	// 发送 SSE 事件通知前端
	h.downloader.EmitEvent(downloader.DownloadEvent{
		Type:     "completed",
		BvID:     awemeID,
		Title:    title,
		FileSize: totalSize,
	})

	log.Printf("[quickdl·douyin-note] Completed: %s (%d/%d images, %.1f MB)", awemeID, successCount, len(detail.Images), float64(totalSize)/(1024*1024))
}

// POST /api/download/preview — 预览视频信息（不下载）
func (h *QuickDownloadHandler) HandlePreview(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := parseJSON(r, &req); err != nil || req.URL == "" {
		apiError(w, CodeInvalidParam, "请提供视频 URL 或 BV/AV 号")
		return
	}

	// 抖音链接检测（优先）
	if douyin.IsDouyinURL(req.URL) {
		h.handleDouyinPreview(w, req.URL)
		return
	}

	bvid, avid, err := bilibili.ExtractBVID(req.URL)
	if err != nil {
		apiError(w, CodeInvalidParam, "无法识别该链接")
		return
	}

	var client *bilibili.Client
	if h.getBiliClient != nil {
		client = h.getBiliClient()
	}
	if client == nil {
		apiError(w, CodeInternal, "bilibili 客户端未初始化")
		return
	}

	if bvid == "" && avid > 0 {
		resolved, err := client.AV2BV(avid)
		if err != nil {
			apiError(w, CodeInternal, fmt.Sprintf("AV 号解析失败: %v", err))
			return
		}
		bvid = resolved
	}

	detail, err := client.GetVideoDetail(bvid)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			apiError(w, CodeInternal, "触发风控，请稍后再试")
			return
		}
		apiError(w, CodeInternal, fmt.Sprintf("获取视频详情失败: %v", err))
		return
	}

	pages := bilibili.GetAllPages(detail)

	var existStatus string
	var existID int64
	h.db.QueryRow(
		"SELECT id, status FROM downloads WHERE video_id = ? LIMIT 1", bvid,
	).Scan(&existID, &existStatus)

	result := map[string]interface{}{
		"bvid":        bvid,
		"title":       detail.Title,
		"description": detail.Desc,
		"uploader":    detail.Owner.Name,
		"duration":    detail.Duration,
		"pic":         detail.Pic,
		"pages":       len(pages),
		"pub_date":    detail.PubDate,
		"tname":       detail.TName,
		"platform":    "bilibili",
		"stat": map[string]interface{}{
			"view":     detail.Stat.View,
			"like":     detail.Stat.Like,
			"coin":     detail.Stat.Coin,
			"favorite": detail.Stat.Favorite,
			"danmaku":  detail.Stat.Danmaku,
		},
		"is_charge_plus": detail.IsChargePlus(),
		"is_bangumi":     detail.IsBangumi(),
		"is_unavailable": detail.IsUnavailable(),
	}

	if existID > 0 {
		result["existing_id"] = existID
		result["existing_status"] = existStatus
	}

	apiOK(w, result)
}

// handleDouyinPreview 预览抖音视频信息
func (h *QuickDownloadHandler) handleDouyinPreview(w http.ResponseWriter, rawURL string) {
	client := douyin.NewClient()

	resolved, err := client.ResolveShareURL(rawURL)
	if err != nil {
		apiError(w, CodeInternal, fmt.Sprintf("解析抖音链接失败: %v", err))
		return
	}

	var awemeID string
	switch resolved.Type {
	case douyin.URLTypeVideo:
		awemeID = resolved.VideoID
	case douyin.URLTypeUser:
		apiError(w, CodeInvalidParam, "这是抖音用户主页链接，请使用订阅源添加")
		return
	default:
		apiError(w, CodeInvalidParam, "无法识别该抖音链接")
		return
	}

	if awemeID == "" {
		apiError(w, CodeInvalidParam, "无法从链接中提取视频 ID")
		return
	}

	detail, err := client.GetVideoDetail(awemeID)
	if err != nil {
		apiError(w, CodeInternal, fmt.Sprintf("获取抖音视频详情失败: %v", err))
		return
	}

	var existStatus string
	var existID int64
	h.db.QueryRow(
		"SELECT id, status FROM downloads WHERE video_id = ? LIMIT 1", awemeID,
	).Scan(&existID, &existStatus)

	result := map[string]interface{}{
		"aweme_id":    awemeID,
		"title":       detail.Desc,
		"description": detail.Desc,
		"uploader":    detail.Author.Nickname,
		"duration":    detail.Duration / 1000,
		"pic":         detail.Cover,
		"pages":       1,
		"platform":    "douyin",
		"is_note":     detail.IsNote,
		"image_count": len(detail.Images),
		"stat": map[string]interface{}{
			"like":    detail.DiggCount,
			"comment": detail.CommentCount,
			"share":   detail.ShareCount,
		},
	}

	if existID > 0 {
		result["existing_id"] = existID
		result["existing_status"] = existStatus
	}

	apiOK(w, result)
}

// handleResult 处理下载结果（B站）
func (h *QuickDownloadHandler) handleResult(dlID int64, videoID string, detail *bilibili.VideoDetail, uploaderName string, ch chan *downloader.Result, skipNFO, skipPoster bool, episodeMeta *nfo.EpisodeMeta) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] quickdl handleResult recovered: %v (videoID=%s)", r, videoID)
			h.db.UpdateDownloadStatus(dlID, "failed", "", 0, fmt.Sprintf("panic: %v", r))
		}
	}()

	var result *downloader.Result
	select {
	case result = <-ch:
	case <-time.After(1 * time.Hour):
		h.db.UpdateDownloadStatus(dlID, "failed", "", 0, "download timeout (1h)")
		return
	}

	if result == nil {
		return
	}

	if !result.Success {
		errMsg := ""
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		h.db.UpdateDownloadStatus(dlID, "failed", "", 0, errMsg)
		h.db.IncrementRetryCount(dlID, errMsg)
		if h.notifier != nil {
			h.notifier.Send(notify.EventDownloadFailed, "B站视频下载失败: "+videoID, errMsg)
		}
		log.Printf("[quickdl] Failed: %s - %s", videoID, errMsg)
		return
	}

	h.db.UpdateDownloadStatus(dlID, "completed", result.FilePath, result.FileSize, "")
	if detail != nil {
		h.db.UpdateDownloadMeta(dlID, uploaderName, detail.Desc, detail.Pic, detail.Duration)
	}

	actualBvID := videoID
	if parts := strings.SplitN(videoID, "_P", 2); len(parts) == 2 {
		actualBvID = parts[0]
	}

	if !skipNFO && detail != nil {
		if episodeMeta != nil {
			nfo.GenerateEpisodeNFOFromPath(episodeMeta, result.FilePath)
		} else {
			var tags []string
			if h.getBiliClient != nil {
				tags, _ = h.getBiliClient().GetVideoTags(actualBvID)
			}
			meta := &nfo.VideoMeta{
				BvID: actualBvID, Title: detail.Title, Description: detail.Desc,
				UploaderName: uploaderName, UploaderFace: detail.Owner.Face,
				UploadDate: time.Unix(detail.PubDate, 0), Duration: detail.Duration,
				Tags: tags, ViewCount: detail.Stat.View, LikeCount: detail.Stat.Like,
				CoinCount: detail.Stat.Coin, DanmakuCount: detail.Stat.Danmaku,
				ReplyCount: detail.Stat.Reply, FavoriteCount: detail.Stat.Favorite,
				ShareCount: detail.Stat.Share, Thumbnail: detail.Pic,
				WebpageURL: fmt.Sprintf("https://www.bilibili.com/video/%s", actualBvID),
				TName:      detail.TName,
			}
			nfo.GenerateVideoNFO(meta, result.FilePath)
		}
	}

	if !skipPoster && detail != nil && detail.Pic != "" && result.FilePath != "" {
		ext := filepath.Ext(result.FilePath)
		thumbPath := strings.TrimSuffix(result.FilePath, ext) + "-thumb.jpg"
		if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(detail.Pic, thumbPath); err == nil {
				h.db.UpdateThumbPath(dlID, thumbPath)
			}
		} else {
			h.db.UpdateThumbPath(dlID, thumbPath)
		}
	}

	statusBits := db.StatusBitVideo
	if !skipNFO && detail != nil {
		statusBits |= db.StatusBitNFO
	}
	if !skipPoster && detail != nil && detail.Pic != "" {
		statusBits |= db.StatusBitThumb
	}
	if result.DanmakuDone {
		statusBits |= db.StatusBitDanmaku
	}
	if result.SubtitleDone {
		statusBits |= db.StatusBitSubtitle
	}
	h.db.UpdateDetailStatus(dlID, statusBits)

	// 发送通知
	if h.notifier != nil && detail != nil {
		h.notifier.Send(notify.EventDownloadComplete, "B站视频下载完成: "+detail.Title,
			fmt.Sprintf("UP主: %s\n大小: %.1f MB", uploaderName, float64(result.FileSize)/(1024*1024)))
	}

	log.Printf("[quickdl] Completed: %s -> %s", videoID, result.FilePath)
}

// quickDownloadDouyinFile 下载抖音视频 MP4
func quickDownloadDouyinFile(videoURL, destPath string) (int64, error) {
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

	httpClient := &http.Client{Timeout: 10 * time.Minute}
	resp, err := httpClient.Do(req)
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

// quickDownloadDouyinThumb 下载封面图
func quickDownloadDouyinThumb(thumbURL, destPath string) error {
	if _, err := os.Stat(destPath); err == nil {
		return nil
	}

	req, err := http.NewRequest("GET", thumbURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15")

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
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
