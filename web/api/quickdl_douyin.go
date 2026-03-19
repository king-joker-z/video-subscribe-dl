package api

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
)


// handleDouyinQuickDownload 处理抖音单视频快速下载
func (h *QuickDownloadHandler) handleDouyinQuickDownload(w http.ResponseWriter, rawURL string) {
	client := douyin.NewClient()
	// 注意：client 会传给异步 goroutine，不能在此 defer Close，由 goroutine 负责关闭

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

	// client 由调用方（handleDouyinQuickDownload）创建并传入，goroutine 负责关闭
	defer client.Close()

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

	// page scrape 路径已 resolve（URLResolved=true），无需二次 resolve
	videoURL := detail.VideoURL
	var err error
	if !detail.URLResolved {
		videoURL, err = client.ResolveVideoURL(detail.VideoURL)
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
	if safeTitle == "unknown" {
		safeTitle = fmt.Sprintf("douyin_%s", awemeID)
	}
	if len([]rune(safeTitle)) > 80 {
		safeTitle = string([]rune(safeTitle)[:80])
	}
	videoFilePath := filepath.Join(outputDir, safeTitle+" ["+awemeID+"].mp4")

	// 下载视频（带重试）
	var fileSize int64
	for attempt := 1; attempt <= 3; attempt++ {
		fileSize, err = douyin.DownloadFile(videoURL, videoFilePath)
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
		if err := douyin.DownloadThumb(detail.Cover, thumbPath); err != nil {
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
			Platform:     "douyin",
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
		Type:         "completed",
		BvID:         awemeID,
		Title:        title,
		FileSize:     fileSize,
		DownloadedAt: time.Now().Format(time.RFC3339),
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
	if safeTitle == "unknown" {
		safeTitle = fmt.Sprintf("douyin_%s", awemeID)
	}
	if len([]rune(safeTitle)) > 80 {
		safeTitle = string([]rune(safeTitle)[:80])
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
			fileSize, err = douyin.DownloadFile(imgURL, imgPath)
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
		douyin.DownloadThumb(detail.Cover, coverPath)
	}

	// 生成 NFO
	skipNFOStr, _ := h.db.GetSetting("skip_nfo")
	if skipNFOStr != "true" {
		meta := &nfo.VideoMeta{
			Platform: "douyin",
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
		Type:         "completed",
		BvID:         awemeID,
		Title:        title,
		FileSize:     totalSize,
		DownloadedAt: time.Now().Format(time.RFC3339),
	})

	log.Printf("[quickdl·douyin-note] Completed: %s (%d/%d images, %.1f MB)", awemeID, successCount, len(detail.Images), float64(totalSize)/(1024*1024))
}


// handleDouyinPreview 预览抖音视频信息
func (h *QuickDownloadHandler) handleDouyinPreview(w http.ResponseWriter, rawURL string) {
	client := douyin.NewClient()
	defer client.Close()

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

