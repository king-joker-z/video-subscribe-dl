package api

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"strings"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/util"
)

// VideosHandler 视频/下载 API
type VideosHandler struct {
	db              *db.DB
	downloadDir     string
	onRetryDownload func(int64)
	onProcessPending func()
	onRedownload    func(int64)
}

func NewVideosHandler(database *db.DB, downloadDir string) *VideosHandler {
	return &VideosHandler{db: database, downloadDir: downloadDir}
}

func (h *VideosHandler) SetRetryDownloadFunc(fn func(int64)) {
	h.onRetryDownload = fn
}

func (h *VideosHandler) SetProcessPendingFunc(fn func()) {
	h.onProcessPending = fn
}

func (h *VideosHandler) SetRedownloadFunc(fn func(int64)) {
	h.onRedownload = fn
}

// GET /api/videos — 视频列表（分页 + 筛选 + 排序）
func (h *VideosHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	pg := ParsePagination(r)
	status := r.URL.Query().Get("status")
	sourceID := r.URL.Query().Get("source_id")
	uploader := r.URL.Query().Get("uploader")
	search := r.URL.Query().Get("search")

	// 构建 WHERE 子句
	where := "WHERE 1=1"
	args := []interface{}{}

	if status != "" && status != "all" {
		if status == "failed" {
			where += " AND d.status IN ('failed','permanent_failed')"
		} else if status == "completed" {
			where += " AND d.status IN ('completed','relocated')"
		} else {
			where += " AND d.status = ?"
			args = append(args, status)
		}
	}
	if sourceID != "" {
		sid, _ := strconv.ParseInt(sourceID, 10, 64)
		if sid > 0 {
			where += " AND d.source_id = ?"
			args = append(args, sid)
		}
	}
	if uploader != "" {
		where += " AND d.uploader = ?"
		args = append(args, uploader)
	}
	if search != "" {
		where += " AND (d.title LIKE ? OR d.uploader LIKE ?)"
		args = append(args, "%"+search+"%", "%"+search+"%")
	}

	// 总数
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	var total int
	h.db.QueryRow("SELECT COUNT(*) FROM downloads d "+where, countArgs...).Scan(&total)

	// 排序
	orderBy := "d.created_at DESC"
	if pg.Sort != "" {
		// 白名单验证排序字段
		allowedSorts := map[string]string{
			"created":  "d.created_at",
			"title":    "d.title",
			"status":   "d.status",
			"size":     "d.file_size",
			"uploader": "d.uploader",
		}
		if col, ok := allowedSorts[pg.Sort]; ok {
			orderBy = col + " " + strings.ToUpper(pg.Order)
		}
	}

	// 查询
	query := `
		SELECT d.id, d.source_id, d.video_id, COALESCE(d.title,''), COALESCE(d.filename,''), d.status,
		       COALESCE(d.file_path,''), d.file_size, COALESCE(d.uploader,''), COALESCE(d.description,''),
		       COALESCE(d.thumbnail,''), COALESCE(d.thumb_path,''), d.duration,
		       d.downloaded_at, COALESCE(d.error_message,''),
		       COALESCE(d.retry_count,0), COALESCE(d.last_error,''), d.created_at
		FROM downloads d ` + where + `
		ORDER BY ` + orderBy + `
		LIMIT ? OFFSET ?
	`
	args = append(args, pg.PageSize, pg.Offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		apiError(w, CodeInternal, "查询失败: "+err.Error())
		return
	}
	defer rows.Close()

	var videos []db.Download
	for rows.Next() {
		var dl db.Download
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename,
			&dl.Status, &dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description,
			&dl.Thumbnail, &dl.ThumbPath, &dl.Duration, &dl.DownloadedAt,
			&dl.ErrorMessage, &dl.RetryCount, &dl.LastError, &dl.CreatedAt); err != nil {
			apiError(w, CodeInternal, "解析数据失败")
			return
		}
		videos = append(videos, dl)
	}
	if videos == nil {
		videos = []db.Download{}
	}

	apiPaginated(w, videos, total, pg.Page, pg.PageSize)
}

// GET /api/videos/:id — 视频详情
func (h *VideosHandler) HandleGet(w http.ResponseWriter, r *http.Request, id int64) {
	dl, err := h.db.GetDownload(id)
	if err != nil {
		apiError(w, CodeVideoNotFound, "视频不存在")
		return
	}
	apiOK(w, dl)
}

// POST /api/videos/:id/retry — 重试下载
func (h *VideosHandler) HandleRetry(w http.ResponseWriter, r *http.Request, id int64) {
	if h.onRetryDownload != nil {
		h.onRetryDownload(id)
		log.Printf("[video] Retry download %d via API", id)
	} else {
		h.db.UpdateDownloadStatus(id, "pending", "", 0, "")
		h.db.ResetRetryCount(id)
	}
	apiOK(w, map[string]interface{}{"id": id, "message": "已提交重试"})
}

// POST /api/videos/:id/cancel — 取消下载
func (h *VideosHandler) HandleCancel(w http.ResponseWriter, r *http.Request, id int64) {
	h.db.UpdateDownloadStatus(id, "cancelled", "", 0, "手动取消")
	log.Printf("[video] Cancelled download %d", id)
	apiOK(w, map[string]interface{}{"id": id, "message": "已取消"})
}

// POST /api/videos/:id/redownload — 重新下载（删除旧文件，重置为 pending）
func (h *VideosHandler) HandleRedownload(w http.ResponseWriter, r *http.Request, id int64) {
	dl, err := h.db.GetDownload(id)
	if err != nil {
		apiError(w, CodeVideoNotFound, "视频不存在")
		return
	}
	if dl.Status != "completed" && dl.Status != "relocated" {
		apiError(w, CodeInvalidParam, "只能重新下载已完成的视频")
		return
	}

	// 删除旧文件所在的整个视频目录（包含 NFO、封面、弹幕等）
	if dl.FilePath != "" {
		util.RemoveVideoDir(dl.FilePath, h.downloadDir)
	}

	// 重置状态为 pending
	h.db.UpdateDownloadStatus(id, "pending", "", 0, "")
	h.db.ResetRetryCount(id)
	// 清空旧的 file_path 和 thumb_path
	h.db.Exec("UPDATE downloads SET file_path = '', file_size = 0, thumb_path = '', downloaded_at = NULL WHERE id = ?", id)

	// 直接提交到下载队列（不依赖 sync 增量拉取）
	if h.onRedownload != nil {
		go h.onRedownload(id)
	} else if h.onProcessPending != nil {
		go h.onProcessPending()
	}

	log.Printf("[video] Redownload %d (%s)", id, dl.Title)
	apiOK(w, map[string]interface{}{"id": id, "message": "已提交重新下载"})
}


// DELETE /api/videos/:id — 删除下载记录及文件
func (h *VideosHandler) HandleDeleteVideo(w http.ResponseWriter, r *http.Request, id int64) {
	dl, _ := h.db.GetDownload(id)
	if dl != nil && dl.FilePath != "" {
		util.RemoveVideoDir(dl.FilePath, h.downloadDir)
	}

	// 回退 source 的 latest_video_at，确保下次同步能扫到被删除的视频
	if dl != nil && dl.SourceID > 0 {
		h.rewindLatestVideoAt(dl.SourceID, dl.VideoID)
	}

	h.db.Exec("DELETE FROM downloads WHERE id = ?", id)
	log.Printf("[video] Deleted record %d", id)
	apiOK(w, map[string]interface{}{"id": id, "message": "已删除"})
}

// POST /api/videos/:id/delete-files — 只删除本地文件，不改数据库状态
func (h *VideosHandler) HandleDeleteFiles(w http.ResponseWriter, r *http.Request, id int64) {
	dl, err := h.db.GetDownload(id)
	if err != nil {
		apiError(w, CodeVideoNotFound, "视频不存在")
		return
	}

	if dl.FilePath == "" {
		apiError(w, CodeInvalidParam, "没有关联的本地文件")
		return
	}

	util.RemoveVideoDir(dl.FilePath, h.downloadDir)

	// 清空文件路径和大小，但不改状态
	h.db.Exec("UPDATE downloads SET file_path = '', file_size = 0, thumb_path = '' WHERE id = ?", id)

	log.Printf("[video] Deleted files for %d (%s): %s", id, dl.Title, dl.FilePath)
	apiOK(w, map[string]interface{}{"id": id, "message": "本地文件已删除"})
}

// POST /api/videos/batch — 批量操作
func (h *VideosHandler) HandleBatch(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	var req struct {
		Action string  `json:"action"` // retry | cancel | delete | redownload | delete_files
		IDs    []int64 `json:"ids"`
	}
	if err := parseJSON(r, &req); err != nil {
		apiError(w, CodeInvalidParam, "请求参数错误")
		return
	}

	if len(req.IDs) == 0 {
		apiError(w, CodeInvalidParam, "ids 不能为空")
		return
	}

	var affected int
	var redownloadIDs []int64
	for _, id := range req.IDs {
		switch req.Action {
		case "retry":
			if h.onRetryDownload != nil {
				h.onRetryDownload(id)
			} else {
				h.db.UpdateDownloadStatus(id, "pending", "", 0, "")
				h.db.ResetRetryCount(id)
			}
			affected++
		case "redownload":
			dl, _ := h.db.GetDownload(id)
			if dl != nil && (dl.Status == "completed" || dl.Status == "relocated") {
				if dl.FilePath != "" {
					util.RemoveVideoDir(dl.FilePath, h.downloadDir)
				}
				h.db.UpdateDownloadStatus(id, "pending", "", 0, "")
				h.db.ResetRetryCount(id)
				h.db.Exec("UPDATE downloads SET file_path = '', file_size = 0, thumb_path = '', downloaded_at = NULL WHERE id = ?", id)
				redownloadIDs = append(redownloadIDs, id)
				affected++
			}
		case "cancel":
			h.db.UpdateDownloadStatus(id, "cancelled", "", 0, "批量取消")
			affected++
		case "delete":
			dl, _ := h.db.GetDownload(id)
			if dl != nil && dl.FilePath != "" {
				util.RemoveVideoDir(dl.FilePath, h.downloadDir)
			}
			if dl != nil && dl.SourceID > 0 {
				h.rewindLatestVideoAt(dl.SourceID, dl.VideoID)
			}
			h.db.Exec("DELETE FROM downloads WHERE id = ?", id)
			affected++
		case "delete_files":
			dl, _ := h.db.GetDownload(id)
			if dl != nil && dl.FilePath != "" {
				util.RemoveVideoDir(dl.FilePath, h.downloadDir)
				h.db.Exec("UPDATE downloads SET file_path = '', file_size = 0, thumb_path = '' WHERE id = ?", id)
				affected++
			}
		default:
			apiError(w, CodeInvalidParam, "未知操作: "+req.Action)
			return
		}
	}

	// 批量重新下载后触发 pending 处理
	if req.Action == "redownload" {
		if h.onRedownload != nil {
			for _, id := range redownloadIDs {
				go h.onRedownload(id)
			}
		} else if h.onProcessPending != nil {
			go h.onProcessPending()
		}
	}

	log.Printf("[video] Batch %s: %d items", req.Action, affected)
	apiOK(w, map[string]interface{}{
		"action":   req.Action,
		"affected": affected,
	})
}

// HandleByID 路由分发 /api/videos/:id
func (h *VideosHandler) HandleByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/videos/")
	if path == "" || path == "batch" {
		if path == "batch" {
			h.HandleBatch(w, r)
			return
		}
		apiError(w, CodeInvalidParam, "缺少 ID")
		return
	}

	// /api/videos/:id/retry
	if strings.HasSuffix(path, "/retry") && r.Method == "POST" {
		idStr := strings.TrimSuffix(path, "/retry")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			apiError(w, CodeInvalidParam, "无效的 ID")
			return
		}
		h.HandleRetry(w, r, id)
		return
	}

	// /api/videos/:id/redownload
	if strings.HasSuffix(path, "/redownload") && r.Method == "POST" {
		idStr := strings.TrimSuffix(path, "/redownload")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			apiError(w, CodeInvalidParam, "无效的 ID")
			return
		}
		h.HandleRedownload(w, r, id)
		return
	}

	// /api/videos/:id/delete-files
	if strings.HasSuffix(path, "/delete-files") && r.Method == "POST" {
		idStr := strings.TrimSuffix(path, "/delete-files")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			apiError(w, CodeInvalidParam, "无效的 ID")
			return
		}
		h.HandleDeleteFiles(w, r, id)
		return
	}

	// /api/videos/:id/cancel
	if strings.HasSuffix(path, "/cancel") && r.Method == "POST" {
		idStr := strings.TrimSuffix(path, "/cancel")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			apiError(w, CodeInvalidParam, "无效的 ID")
			return
		}
		h.HandleCancel(w, r, id)
		return
	}

	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil {
		apiError(w, CodeInvalidParam, "无效的 ID")
		return
	}

	switch r.Method {
	case "GET":
		h.HandleGet(w, r, id)
	case "DELETE":
		h.HandleDeleteVideo(w, r, id)
	default:
		apiError(w, CodeMethodNotAllow, "method not allowed")
	}
}


// POST /api/videos/detect-charge — 手动触发全量充电检测
func (h *VideosHandler) HandleDetectCharge(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	// 查找所有 failed 和 permanent_failed 的视频
	rows, err := h.db.Query(`SELECT id, video_id, title FROM downloads WHERE status IN ('failed', 'permanent_failed')`)
	if err != nil {
		apiError(w, CodeInternal, "查询失败: "+err.Error())
		return
	}
	defer rows.Close()

	type chargeCheckItem struct {
		ID      int64
		VideoID string
		Title   string
	}
	var items []chargeCheckItem
	for rows.Next() {
		var v chargeCheckItem
		rows.Scan(&v.ID, &v.VideoID, &v.Title)
		items = append(items, v)
	}

	if len(items) == 0 {
		apiOK(w, map[string]interface{}{"detected": 0, "message": "没有失败的视频"})
		return
	}

	// 获取 bilibili client
	var client *bilibili.Client
	if credJSON, _ := h.db.GetSetting("credential_json"); credJSON != "" {
		if cred := bilibili.CredentialFromJSON(credJSON); cred != nil && !cred.IsEmpty() {
			client = bilibili.NewClientWithCredential(cred)
		}
	}
	if client == nil {
		var cookie string
		if cp, _ := h.db.GetSetting("cookie_path"); cp != "" {
			cookie = bilibili.ReadCookieFile(cp)
		}
		client = bilibili.NewClient(cookie)
	}

	// 异步检测，立即返回
	go func() {
		detected := 0
		for _, v := range items {
			bvid := v.VideoID
			if parts := strings.SplitN(v.VideoID, "_P", 2); len(parts) == 2 {
				bvid = parts[0]
			}
			detail, err := client.GetVideoDetail(bvid)
			if err != nil {
				continue
			}
			if detail.IsChargePlus() {
				h.db.UpdateDownloadStatus(v.ID, "charge_blocked", "", 0, "充电专属/付费视频")
				detected++
				log.Printf("[detect-charge] %s (%s) → charge_blocked", v.Title, v.VideoID)
			}
			time.Sleep(500 * time.Millisecond)
		}
		log.Printf("[detect-charge] 检测完成: %d/%d 为充电专属", detected, len(items))
	}()

	apiOK(w, map[string]interface{}{
		"total":   len(items),
		"message": fmt.Sprintf("已启动充电检测，共 %d 个失败视频", len(items)),
	})
}

// HandleThumb GET /api/thumb/:id — 封面图
func (h *VideosHandler) HandleThumb(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/thumb/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		apiError(w, CodeInvalidParam, "无效的 ID")
		return
	}

	dl, err := h.db.GetDownload(id)
	if err != nil {
		apiError(w, CodeVideoNotFound, "视频不存在")
		return
	}

	// 先尝试本地缩略图
	if dl.ThumbPath != "" {
		if _, err := os.Stat(dl.ThumbPath); err == nil {
			http.ServeFile(w, r, dl.ThumbPath)
			return
		}
	}
	// 从 file_path 推算
	if dl.FilePath != "" {
		ext := filepath.Ext(dl.FilePath)
		thumbPath := strings.TrimSuffix(dl.FilePath, ext) + "-thumb.jpg"
		if _, err := os.Stat(thumbPath); err == nil {
			http.ServeFile(w, r, thumbPath)
			return
		}
	}
	// 302 到 CDN
	if dl.Thumbnail != "" {
		http.Redirect(w, r, dl.Thumbnail, http.StatusFound)
		return
	}
	apiError(w, CodeNotFound, "无封面图")
}

// rewindLatestVideoAt 回退 source 的 latest_video_at，确保下次同步时能扫到被删除的视频
// 通过查询被删除视频的 created_at 来决定是否需要回退
func (h *VideosHandler) rewindLatestVideoAt(sourceID int64, videoID string) {
	// 获取被删除视频的 bilibili 发布时间
	// 由于 downloads 表没有存 bilibili 的原始发布时间戳，我们需要查 source 的 latest_video_at
	// 如果删了某条记录，直接把 latest_video_at 设为 0，让下次同步做一次全量扫描
	// 这是最安全的方案：虽然会多扫一次，但保证不遗漏

	var latestVideoAt int64
	err := h.db.QueryRow("SELECT COALESCE(latest_video_at, 0) FROM sources WHERE id = ?", sourceID).Scan(&latestVideoAt)
	if err != nil || latestVideoAt == 0 {
		return // source 不存在或已经是 0，无需回退
	}

	// 重置为 0，下次同步时做全量扫描
	h.db.Exec("UPDATE sources SET latest_video_at = 0 WHERE id = ?", sourceID)
	log.Printf("[video] Reset latest_video_at for source %d (was %d) due to video deletion (%s)", sourceID, latestVideoAt, videoID)
}
