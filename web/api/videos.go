package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/util"
)

// VideosHandler 视频/下载 API
type VideosHandler struct {
	db               *db.DB
	downloadDir      string
	onRetryDownload  func(int64)
	onProcessPending func()
	onRedownload     func(int64)
	onRepairThumb    func(videoPath, thumbPath string) error
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

func (h *VideosHandler) SetRepairThumbFunc(fn func(string, string) error) {
	h.onRepairThumb = fn
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

	if status == "" {
		// 默认不显示已删除的视频
		where += " AND d.status != 'deleted'"
	} else if status != "all" {
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
			"created":    "d.created_at",
			"title":      "d.title",
			"status":     "d.status",
			"size":       "d.file_size",
			"uploader":   "d.uploader",
			"downloaded": "d.downloaded_at",
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
		       COALESCE(d.retry_count,0), COALESCE(d.last_error,''),
		       COALESCE(d.detail_status,0), d.created_at
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
		// Fix CR-001(infra): downloaded_at is nullable; scan into a *time.Time pointer so
		// the SQLite driver stores nil for NULL rows (pending/failed) instead of erroring.
		var downloadedAt *time.Time
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename,
			&dl.Status, &dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description,
			&dl.Thumbnail, &dl.ThumbPath, &dl.Duration, &downloadedAt,
			&dl.ErrorMessage, &dl.RetryCount, &dl.LastError,
			&dl.DetailStatus, &dl.CreatedAt); err != nil {
			apiError(w, CodeInternal, "解析数据失败")
			return
		}
		dl.DownloadedAt = downloadedAt
		videos = append(videos, dl)
	}
	// P0-10: check for iteration errors after the loop
	if err := rows.Err(); err != nil {
		apiError(w, CodeInternal, "读取数据失败: "+err.Error())
		return
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
		// fallback：直接改 DB 状态并触发 pending 处理
		h.db.UpdateDownloadStatus(id, "pending", "", 0, "")
		h.db.ResetRetryCount(id)
		if h.onProcessPending != nil {
			go h.onProcessPending()
		}
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
// 也支持 pending 状态的视频：直接触发下载，不删文件不重置
func (h *VideosHandler) HandleRedownload(w http.ResponseWriter, r *http.Request, id int64) {
	dl, err := h.db.GetDownload(id)
	if err != nil {
		apiError(w, CodeVideoNotFound, "视频不存在")
		return
	}

	// pending 状态：直接触发下载，无需删文件或重置
	if dl.Status == "pending" {
		if h.onRedownload != nil {
			go h.onRedownload(id)
		}
		log.Printf("[video] Trigger pending download %d (%s)", id, dl.Title)
		apiOK(w, map[string]interface{}{"id": id, "message": "已触发下载"})
		return
	}

	if dl.Status != "completed" && dl.Status != "relocated" &&
		dl.Status != "failed" && dl.Status != "permanent_failed" && dl.Status != "cancelled" {
		apiError(w, CodeInvalidParam, "只能重新下载已完成、失败或已取消的视频")
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


// DELETE /api/videos/:id — 软删除下载记录（标记为 deleted，删除本地文件）
func (h *VideosHandler) HandleDeleteVideo(w http.ResponseWriter, r *http.Request, id int64) {
	dl, _ := h.db.GetDownload(id)
	if dl != nil && dl.FilePath != "" {
		util.RemoveVideoDir(dl.FilePath, h.downloadDir)
	}

	// 软删除：标记状态为 deleted，清空文件信息
	h.db.Exec("UPDATE downloads SET status = 'deleted', file_path = '', file_size = 0, thumb_path = '' WHERE id = ?", id)
	log.Printf("[video] Soft-deleted record %d", id)
	apiOK(w, map[string]interface{}{"id": id, "message": "已删除"})
}

// POST /api/videos/:id/restore — 恢复已删除的视频（重新下载）
func (h *VideosHandler) HandleRestore(w http.ResponseWriter, r *http.Request, id int64) {
	dl, err := h.db.GetDownload(id)
	if err != nil {
		apiError(w, CodeVideoNotFound, "视频不存在")
		return
	}
	if dl.Status != "deleted" {
		apiError(w, CodeInvalidParam, "只能恢复已删除的视频")
		return
	}

	// 重置为 pending
	h.db.UpdateDownloadStatus(id, "pending", "", 0, "")
	h.db.ResetRetryCount(id)

	// 直接提交到下载队列
	if h.onRedownload != nil {
		go h.onRedownload(id)
	} else if h.onProcessPending != nil {
		go h.onProcessPending()
	}

	log.Printf("[video] Restored deleted video %d (%s)", id, dl.Title)
	apiOK(w, map[string]interface{}{"id": id, "message": "已恢复，开始重新下载"})
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
		Action   string  `json:"action"` // retry | cancel | delete | redownload | delete_files | restore | download_by_uploader | download_all_pending
		IDs      []int64 `json:"ids"`
		Uploader string  `json:"uploader"`
	}
	if err := parseJSON(r, &req); err != nil {
		apiError(w, CodeInvalidParam, "请求参数错误")
		return
	}

	// 按 UP 主批量下载 pending
	if req.Action == "download_by_uploader" {
		if req.Uploader == "" {
			apiError(w, CodeInvalidParam, "uploader 不能为空")
			return
		}
		downloads, err := h.db.GetPendingByUploader(req.Uploader)
		if err != nil {
			apiError(w, CodeInternal, "查询失败: "+err.Error())
			return
		}
		if len(downloads) == 0 {
			apiOK(w, map[string]interface{}{
				"action": req.Action, "affected": 0,
				"message": fmt.Sprintf("UP主 %s 没有待处理的下载", req.Uploader),
			})
			return
		}
		if h.onRedownload != nil {
			for _, dl := range downloads {
				go h.onRedownload(dl.ID)
			}
		} else if h.onProcessPending != nil {
			go h.onProcessPending()
		}
		log.Printf("[video] Batch download_by_uploader: %s, %d items", req.Uploader, len(downloads))
		apiOK(w, map[string]interface{}{
			"action": req.Action, "affected": len(downloads),
			"message": fmt.Sprintf("已提交 %s 的 %d 个待处理下载", req.Uploader, len(downloads)),
		})
		return
	}

	// 全部 pending 批量下载
	if req.Action == "download_all_pending" {
		if h.onProcessPending != nil {
			go h.onProcessPending()
		}
		log.Printf("[video] Batch download_all_pending triggered")
		apiOK(w, map[string]interface{}{
			"action": req.Action, "affected": 0,
			"message": "已触发全部待处理下载",
		})
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
				// P1-9: call asynchronously so a large batch doesn't block the HTTP handler
				go h.onRetryDownload(id)
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
			h.db.Exec("UPDATE downloads SET status = 'deleted', file_path = '', file_size = 0, thumb_path = '' WHERE id = ?", id)
			affected++
		case "restore":
			dl, _ := h.db.GetDownload(id)
			if dl != nil && dl.Status == "deleted" {
				h.db.UpdateDownloadStatus(id, "pending", "", 0, "")
				h.db.ResetRetryCount(id)
				redownloadIDs = append(redownloadIDs, id)
				affected++
			}
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

	// 批量重新下载/恢复后触发 pending 处理
	if req.Action == "redownload" || req.Action == "restore" {
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

	// /api/videos/:id/restore
	if strings.HasSuffix(path, "/restore") && r.Method == "POST" {
		idStr := strings.TrimSuffix(path, "/restore")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			apiError(w, CodeInvalidParam, "无效的 ID")
			return
		}
		h.HandleRestore(w, r, id)
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
		// P0-6: check Scan error; silently ignoring it would produce zero-value records
		if err := rows.Scan(&v.ID, &v.VideoID, &v.Title); err != nil {
			log.Printf("[detect-charge] rows.Scan error: %v", err)
			continue
		}
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
	// P0-11: 使用 context 限制整体超时，并在每次 API 调用前检查取消信号
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		detected := 0
		for _, v := range items {
			select {
			case <-ctx.Done():
				log.Printf("[detect-charge] 检测超时，已中止 (%d/%d 完成)", detected, len(items))
				return
			default:
			}
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

// POST /api/videos/fix-stale-failed — 修复历史脏数据：next_retry_at=0 的 PH failed 记录
// 无 SSH 时通过此 API 在线修复。幂等操作，可重复调用。
func (h *VideosHandler) HandleFixStaleFailed(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	now := time.Now().Unix()

	// Step 1：retry_count >= 3 的旧 failed 记录升级为 permanent_failed（不再自动重试）
	res1, err := h.db.Exec(`
		UPDATE downloads
		SET status = 'permanent_failed',
		    error_message = COALESCE(error_message, '') || ' [auto-upgraded by fix-stale-failed]'
		WHERE status = 'failed'
		  AND COALESCE(retry_count, 0) >= 3
		  AND source_id IN (SELECT id FROM sources WHERE type = 'pornhub')
	`)
	var upgraded int64
	if err == nil {
		upgraded, _ = res1.RowsAffected()
	} else {
		log.Printf("[fix-stale-failed] upgrade step error: %v", err)
	}

	// Step 2：retry_count < 3 且 next_retry_at=0 的记录设置 next_retry_at=now+15min
	// （让它们在 15 分钟后才被 retry-worker 捞到，而不是立即再次投递）
	nextRetry := now + 15*60
	res2, err2 := h.db.Exec(`
		UPDATE downloads
		SET next_retry_at = ?
		WHERE status = 'failed'
		  AND COALESCE(retry_count, 0) < 3
		  AND COALESCE(next_retry_at, 0) = 0
		  AND source_id IN (SELECT id FROM sources WHERE type = 'pornhub')
	`, nextRetry)
	var scheduled int64
	if err2 == nil {
		scheduled, _ = res2.RowsAffected()
	} else {
		log.Printf("[fix-stale-failed] schedule step error: %v", err2)
	}

	log.Printf("[fix-stale-failed] done: upgraded=%d permanent_failed, scheduled=%d for retry", upgraded, scheduled)
	apiOK(w, map[string]interface{}{
		"upgraded":  upgraded,
		"scheduled": scheduled,
		"message":   fmt.Sprintf("已修复：%d 条升级为 permanent_failed，%d 条设置了下次重试时间", upgraded, scheduled),
	})
}

// POST /api/videos/skip-video-disabled — 将 "Video Disabled" 的永久失败记录标记为 skipped
// 幂等操作，可重复调用。
func (h *VideosHandler) HandleSkipVideoDisabled(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	res, err := h.db.Exec(`
		UPDATE downloads
		SET status = 'skipped',
		    error_message = 'skipped: video permanently disabled on Pornhub'
		WHERE status IN ('failed', 'permanent_failed')
		  AND (
		    error_message LIKE '%Video Disabled%'
		    OR error_message LIKE '%video permanently unavailable%'
		    OR error_message LIKE '%Video Unavailable%'
		  )
	`)
	if err != nil {
		log.Printf("[skip-video-disabled] db error: %v", err)
		apiError(w, CodeInternal, "数据库操作失败: "+err.Error())
		return
	}
	affected, _ := res.RowsAffected()
	log.Printf("[skip-video-disabled] done: %d records marked as skipped", affected)
	apiOK(w, map[string]interface{}{
		"skipped": affected,
		"message": fmt.Sprintf("已将 %d 条 Video Disabled 记录标记为 skipped，不再重试", affected),
	})
}

// POST /api/videos/repair-thumbs — 历史封面补全
func (h *VideosHandler) HandleRepairThumbs(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	// 只补 PH（pornhub）平台的封面：JOIN sources 过滤 type='pornhub'
	// 范围：thumb_path 为空，或 thumb_path 文件不存在
	rows, err := h.db.Query(`
		SELECT d.id, d.file_path, d.title, COALESCE(d.thumb_path,'')
		FROM downloads d
		JOIN sources s ON s.id = d.source_id
		WHERE d.status='completed' AND d.file_path != '' AND s.type='pornhub'`)
	if err != nil {
		apiError(w, CodeInternal, "查询失败: "+err.Error())
		return
	}
	defer rows.Close()

	type repairItem struct {
		ID        int64
		FilePath  string
		Title     string
		ThumbPath string
	}
	var items []repairItem
	for rows.Next() {
		var v repairItem
		// P0-6: check Scan error; silently ignoring it would produce zero-value records
		if err := rows.Scan(&v.ID, &v.FilePath, &v.Title, &v.ThumbPath); err != nil {
			log.Printf("[repair-thumbs] rows.Scan error: %v", err)
			continue
		}
		// 只处理封面缺失的：thumb_path 为空，或 thumb_path 指向的文件不存在
		if v.ThumbPath != "" {
			if _, err := os.Stat(v.ThumbPath); err == nil {
				continue // 封面文件存在，跳过
			}
		}
		items = append(items, v)
	}

	total := len(items)
	var success, skipped, failed int

	for _, item := range items {
		// 解析实际视频文件路径：
		// file_path 可能是 mp4 文件，也可能是视频所在目录（历史数据）
		videoFile := item.FilePath
		fi, statErr := os.Stat(item.FilePath)
		if statErr != nil {
			log.Printf("[repair-thumbs] id=%d skipped: stat failed path=%q err=%v", item.ID, item.FilePath, statErr)
			skipped++
			continue
		}
		if fi.IsDir() {
			// 在目录里找第一个 .mp4 文件
			entries, readErr := os.ReadDir(item.FilePath)
			if readErr != nil {
				log.Printf("[repair-thumbs] id=%d skipped: readdir failed path=%q err=%v", item.ID, item.FilePath, readErr)
				skipped++
				continue
			}
			found := ""
			for _, e := range entries {
				if !e.IsDir() && strings.ToLower(filepath.Ext(e.Name())) == ".mp4" {
					found = filepath.Join(item.FilePath, e.Name())
					break
				}
			}
			if found == "" {
				log.Printf("[repair-thumbs] id=%d skipped: no .mp4 in dir=%q", item.ID, item.FilePath)
				skipped++
				continue
			}
			videoFile = found
		}

		// thumbPath = 视频文件同目录，文件名（不含扩展名）+ "-poster.jpg"
		ext := filepath.Ext(videoFile)
		base := videoFile[:len(videoFile)-len(ext)]
		thumbPath := base + "-poster.jpg"

		if h.onRepairThumb != nil {
			if repErr := h.onRepairThumb(videoFile, thumbPath); repErr != nil {
				log.Printf("[repair-thumbs] id=%d failed: %v", item.ID, repErr)
				failed++
				continue
			}
		} else {
			skipped++
			continue
		}

		// 写入 DB
		if dbErr := h.db.UpdateThumbPath(item.ID, thumbPath); dbErr != nil {
			log.Printf("[repair-thumbs] UpdateThumbPath id=%d failed: %v", item.ID, dbErr)
		}
		success++
	}

	log.Printf("[repair-thumbs] done: total=%d success=%d skipped=%d failed=%d", total, success, skipped, failed)
	apiOK(w, map[string]interface{}{
		"total":   total,
		"success": success,
		"skipped": skipped,
		"failed":  failed,
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
	// 从 file_path 推算（同时尝试 -poster.jpg 和 -thumb.jpg）
	if dl.FilePath != "" {
		ext := filepath.Ext(dl.FilePath)
		base := strings.TrimSuffix(dl.FilePath, ext)
		for _, suffix := range []string{"-poster.jpg", "-thumb.jpg"} {
			thumbPath := base + suffix
			if _, err := os.Stat(thumbPath); err == nil {
				http.ServeFile(w, r, thumbPath)
				return
			}
		}
	}
	// 302 到 CDN
	if dl.Thumbnail != "" {
		http.Redirect(w, r, dl.Thumbnail, http.StatusFound)
		return
	}
	apiError(w, CodeNotFound, "无封面图")
}




