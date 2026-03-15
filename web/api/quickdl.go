package api

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/nfo"
)

// suppress unused import

// QuickDownloadHandler 单视频快速下载 API
type QuickDownloadHandler struct {
	db            *db.DB
	downloadDir   string
	downloader    *downloader.Downloader
	getBiliClient func() *bilibili.Client
}

func NewQuickDownloadHandler(database *db.DB, dl *downloader.Downloader, downloadDir string) *QuickDownloadHandler {
	return &QuickDownloadHandler{
		db:          database,
		downloadDir: downloadDir,
		downloader:  dl,
	}
}

func (h *QuickDownloadHandler) SetBiliClientFunc(fn func() *bilibili.Client) {
	h.getBiliClient = fn
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

	// 解析 BV/AV 号
	bvid, avid, err := bilibili.ExtractBVID(req.URL)
	if err != nil {
		apiError(w, CodeInvalidParam, "无法识别该链接，请输入 B 站视频 URL、BV 号或 AV 号")
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

// handleResult 处理下载结果
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

	log.Printf("[quickdl] Completed: %s -> %s", videoID, result.FilePath)
}
