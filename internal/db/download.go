package db

import (
	"database/sql"
	"fmt"
)

// 下载状态位图常量
const (
	StatusBitThumb    = 1  // 封面已下载
	StatusBitVideo    = 2  // 视频已下载
	StatusBitNFO      = 4  // NFO 已生成
	StatusBitDanmaku  = 8  // 弹幕已下载
	StatusBitSubtitle = 16 // 字幕已下载
)

func (d *DB) CreateDownload(dl *Download) (int64, error) {
	result, err := d.Exec(`
		INSERT OR IGNORE INTO downloads (source_id, video_id, title, filename, status, uploader, description, thumbnail, duration)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, dl.SourceID, dl.VideoID, dl.Title, dl.Filename, dl.Status, dl.Uploader, dl.Description, dl.Thumbnail, dl.Duration)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (d *DB) GetDownloads(limit int) ([]Download, error) {
	rows, err := d.Query(`
		SELECT id, source_id, video_id, COALESCE(title,''), COALESCE(filename,''), status, 
		       COALESCE(file_path,''), file_size, COALESCE(uploader,''), COALESCE(description,''), 
		       COALESCE(thumbnail,''), COALESCE(thumb_path,''), duration, downloaded_at, COALESCE(error_message,''), created_at
		FROM downloads ORDER BY created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var downloads []Download
	for rows.Next() {
		var dl Download
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename,
			&dl.Status, &dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description, &dl.Thumbnail,
			&dl.ThumbPath, &dl.Duration, &dl.DownloadedAt, &dl.ErrorMessage, &dl.CreatedAt); err != nil {
			return nil, err
		}
		downloads = append(downloads, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return downloads, nil
}

func (d *DB) GetDownloadsByStatus(status string, limit int) ([]Download, error) {
	rows, err := d.Query(`
		SELECT id, source_id, video_id, COALESCE(title,''), COALESCE(filename,''), status, 
		       COALESCE(file_path,''), file_size, COALESCE(uploader,''), COALESCE(description,''), 
		       COALESCE(thumbnail,''), COALESCE(thumb_path,''), duration, downloaded_at, COALESCE(error_message,''), created_at
		FROM downloads WHERE status = ? ORDER BY created_at DESC LIMIT ?
	`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var downloads []Download
	for rows.Next() {
		var dl Download
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename,
			&dl.Status, &dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description, &dl.Thumbnail,
			&dl.ThumbPath, &dl.Duration, &dl.DownloadedAt, &dl.ErrorMessage, &dl.CreatedAt); err != nil {
			return nil, err
		}
		downloads = append(downloads, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return downloads, nil
}

func (d *DB) GetPendingDownloads() ([]Download, error) {
	return d.GetDownloadsByStatus("pending", 1000)
}

func (d *DB) UpdateDownloadStatus(id int64, status string, filePath string, fileSize int64, errMsg string) error {
	if status == "completed" {
		_, err := d.Exec(`
			UPDATE downloads SET status=?, file_path=?, file_size=?, downloaded_at=CURRENT_TIMESTAMP, error_message=? WHERE id = ?
		`, status, filePath, fileSize, errMsg, id)
		return err
	}
	_, err := d.Exec(`UPDATE downloads SET status=?, error_message=? WHERE id = ?`, status, errMsg, id)
	return err
}

// IncrementRetryCount increments retry count and records the error
func (d *DB) IncrementRetryCount(id int64, lastError string) error {
	_, err := d.Exec(`UPDATE downloads SET retry_count = retry_count + 1, last_error = ? WHERE id = ?`, lastError, id)
	return err
}

// ResetRetryCount resets retry count (for manual retry)
func (d *DB) ResetRetryCount(id int64) error {
	_, err := d.Exec(`UPDATE downloads SET retry_count = 0, last_error = '' WHERE id = ?`, id)
	return err
}

// GetRetryableDownloads returns failed downloads that can be retried (retry_count < maxRetries)
func (d *DB) GetRetryableDownloads(maxRetries int, limit int) ([]Download, error) {
	rows, err := d.Query(`
		SELECT id, source_id, video_id, COALESCE(title,''), COALESCE(filename,''), status,
		       COALESCE(file_path,''), file_size, COALESCE(uploader,''), COALESCE(description,''),
		       COALESCE(thumbnail,''), COALESCE(thumb_path,''),
		       duration, downloaded_at, COALESCE(error_message,''), COALESCE(retry_count,0), COALESCE(last_error,''), created_at
		FROM downloads
		WHERE status = 'failed' AND COALESCE(retry_count,0) < ?
		ORDER BY id ASC LIMIT ?
	`, maxRetries, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var downloads []Download
	for rows.Next() {
		var dl Download
		var downloadedAt sql.NullTime
		err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename, &dl.Status,
			&dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description, &dl.Thumbnail, &dl.ThumbPath,
			&dl.Duration, &downloadedAt, &dl.ErrorMessage, &dl.RetryCount, &dl.LastError, &dl.CreatedAt)
		if err != nil {
			return nil, err
		}
		if downloadedAt.Valid {
			dl.DownloadedAt = &downloadedAt.Time
		}
		downloads = append(downloads, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return downloads, nil
}

// MarkPermanentFailed marks downloads that exceeded max retries
func (d *DB) MarkPermanentFailed(maxRetries int) (int64, error) {
	result, err := d.Exec(`
		UPDATE downloads SET status = 'permanent_failed'
		WHERE status = 'failed' AND COALESCE(retry_count,0) >= ?
	`, maxRetries)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// UpdateDownloadMeta 更新下载记录的元数据（下载完成后从 info.json 补充）
func (d *DB) UpdateDownloadMeta(id int64, uploader, description, thumbnail string, duration int) error {
	_, err := d.Exec(`
		UPDATE downloads SET uploader=?, description=?, thumbnail=?, duration=? WHERE id = ?
	`, uploader, description, thumbnail, duration, id)
	return err
}

// UpdateThumbPath 更新封面图本地路径
func (d *DB) UpdateThumbPath(id int64, thumbPath string) error {
	_, err := d.Exec("UPDATE downloads SET thumb_path=? WHERE id=?", thumbPath, id)
	return err
}

func (d *DB) IsVideoDownloaded(sourceID int64, videoID string) (bool, error) {
	var exists int
	// 按 source 去重：同一订阅源内已有记录则不重复创建
	// 排除 permanent_failed 让用户可以通过清理后重新触发
	// charge_blocked 也保留（充电视频无法下载）
	err := d.QueryRow(`
		SELECT COUNT(*) FROM downloads WHERE source_id = ? AND video_id = ? AND status NOT IN ('permanent_failed', 'deleted')
	`, sourceID, videoID).Scan(&exists)
	return exists > 0, err
}

func (d *DB) IsVideoExists(videoID string) (bool, error) {
	var exists int
	err := d.QueryRow(`SELECT COUNT(*) FROM downloads WHERE video_id = ?`, videoID).Scan(&exists)
	return exists > 0, err
}

func (d *DB) UpsertDownloadFromScan(videoID, title, uploader, filePath string, fileSize int64) error {
	_, err := d.Exec(`
		INSERT INTO downloads (source_id, video_id, title, uploader, status, file_path, file_size, downloaded_at)
		VALUES (0, ?, ?, ?, 'completed', ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(source_id, video_id) DO UPDATE SET
			title = excluded.title,
			uploader = excluded.uploader,
			file_path = excluded.file_path,
			file_size = excluded.file_size,
			status = 'completed',
			downloaded_at = CURRENT_TIMESTAMP
	`, videoID, title, uploader, filePath, fileSize)
	return err
}

// GetDownloadsByUploader 获取指定 UP 主的视频列表
func (d *DB) GetDownloadsByUploader(uploader string, limit int) ([]Download, error) {
	rows, err := d.Query(`
		SELECT id, source_id, video_id, COALESCE(title,''), COALESCE(filename,''), status,
		       COALESCE(file_path,''), file_size, COALESCE(uploader,''), COALESCE(description,''),
		       COALESCE(thumbnail,''), COALESCE(thumb_path,''), duration, downloaded_at, COALESCE(error_message,''), created_at
		FROM downloads WHERE uploader = ? ORDER BY created_at DESC LIMIT ?
	`, uploader, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var downloads []Download
	for rows.Next() {
		var dl Download
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename,
			&dl.Status, &dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description, &dl.Thumbnail,
			&dl.ThumbPath, &dl.Duration, &dl.DownloadedAt, &dl.ErrorMessage, &dl.CreatedAt); err != nil {
			return nil, err
		}
		downloads = append(downloads, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return downloads, nil
}

// GetAllDownloads 获取所有下载记录（用于对账）
func (d *DB) GetAllDownloads() ([]Download, error) {
	rows, err := d.Query(`
		SELECT id, source_id, video_id, COALESCE(title,''), COALESCE(filename,''), status,
		       COALESCE(file_path,''), file_size, COALESCE(uploader,''), COALESCE(description,''),
		       COALESCE(thumbnail,''), COALESCE(thumb_path,''), duration, downloaded_at, COALESCE(error_message,''), created_at
		FROM downloads
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var downloads []Download
	for rows.Next() {
		var dl Download
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename,
			&dl.Status, &dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description, &dl.Thumbnail,
			&dl.ThumbPath, &dl.Duration, &dl.DownloadedAt, &dl.ErrorMessage, &dl.CreatedAt); err != nil {
			return nil, err
		}
		downloads = append(downloads, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return downloads, nil
}

// MarkVideoMissing 将指定 video_id 的 completed 记录标记为 missing（保留兼容）
func (d *DB) MarkVideoMissing(videoID string) error {
	return d.MarkVideoRelocated(videoID)
}

// MarkVideoRelocated 标记文件已被用户迁移（保留下载记录，不重复下载）
// 状态改为 relocated 而非删除，确保去重逻辑仍然生效
func (d *DB) MarkVideoRelocated(videoID string) error {
	_, err := d.Exec("UPDATE downloads SET status = 'relocated', file_path = '' WHERE video_id = ? AND status = 'completed'", videoID)
	return err
}

// ResetDownloadToPending 将指定 id 的 downloading 记录重置为 pending
func (d *DB) ResetDownloadToPending(id int64) error {
	_, err := d.Exec("UPDATE downloads SET status = 'pending', error_message = 'reset after restart' WHERE id = ? AND status = 'downloading'", id)
	return err
}

// ResetStaleDownloads 重置所有 pending 和 downloading 状态的记录
// 容器重启后内存队列已清空，这些记录需要重新参与调度
// pending -> 删除记录（让 IsVideoDownloaded 返回 false，下次同步时重新创建并提交）
// downloading -> pending（由对账模块处理）
func (d *DB) ResetStaleDownloads() (int64, error) {
	// downloading -> pending（进程重启后需要重新排队）
	result, err := d.Exec("UPDATE downloads SET status = 'pending' WHERE status = 'downloading'")
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return affected, nil
}

// GetDownload 按 ID 获取单条下载记录
func (d *DB) GetDownload(id int64) (*Download, error) {
	row := d.QueryRow(`
		SELECT id, source_id, video_id, COALESCE(title,''), COALESCE(filename,''), status,
		       COALESCE(file_path,''), file_size, COALESCE(uploader,''), COALESCE(description,''),
		       COALESCE(thumbnail,''), COALESCE(thumb_path,''),
		       duration, downloaded_at, COALESCE(error_message,''), COALESCE(retry_count,0), COALESCE(last_error,''),
		       COALESCE(detail_status,0), created_at
		FROM downloads WHERE id = ?
	`, id)
	var dl Download
	var downloadedAt sql.NullTime
	err := row.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename, &dl.Status,
		&dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description, &dl.Thumbnail, &dl.ThumbPath,
		&dl.Duration, &downloadedAt, &dl.ErrorMessage, &dl.RetryCount, &dl.LastError,
		&dl.DetailStatus, &dl.CreatedAt)
	if err != nil {
		return nil, err
	}
	if downloadedAt.Valid {
		dl.DownloadedAt = &downloadedAt.Time
	}
	return &dl, nil
}

// RetryAllFailed 重置所有 failed 状态的记录为 pending（批量重试）
func (d *DB) RetryAllFailed() (int64, error) {
	result, err := d.Exec(`
		UPDATE downloads SET status = 'pending', error_message = '', retry_count = 0, last_error = ''
		WHERE status = 'failed' OR status = 'permanent_failed'
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteByStatus 删除指定状态的所有记录
func (d *DB) DeleteByStatus(status string) (int64, error) {
	result, err := d.Exec("DELETE FROM downloads WHERE status = ?", status)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetPendingDownloadID 查找指定 source+video 的 pending 记录 ID
func (d *DB) GetPendingDownloadID(sourceID int64, videoID string) (int64, error) {
	var id int64
	err := d.QueryRow("SELECT id FROM downloads WHERE source_id = ? AND video_id = ? AND status = 'pending' LIMIT 1", sourceID, videoID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetDownloadsBySourceName 按 source name 获取下载记录
func (d *DB) GetDownloadsBySourceName(sourceName string, limit int) ([]Download, error) {
	rows, err := d.Query(`
		SELECT dl.id, dl.source_id, dl.video_id, COALESCE(dl.title,''), COALESCE(dl.filename,''), dl.status,
		       COALESCE(dl.file_path,''), dl.file_size, COALESCE(dl.uploader,''), COALESCE(dl.description,''),
		       COALESCE(dl.thumbnail,''), COALESCE(dl.thumb_path,''),
		       dl.duration, dl.downloaded_at, COALESCE(dl.error_message,''), dl.created_at
		FROM downloads dl
		JOIN sources s ON s.id = dl.source_id
		WHERE s.name = ?
		ORDER BY dl.created_at DESC LIMIT ?
	`, sourceName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var downloads []Download
	for rows.Next() {
		var dl Download
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename,
			&dl.Status, &dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description, &dl.Thumbnail,
			&dl.ThumbPath, &dl.Duration, &dl.DownloadedAt, &dl.ErrorMessage, &dl.CreatedAt); err != nil {
			return nil, err
		}
		downloads = append(downloads, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return downloads, nil
}


// UploaderStats UP主下载统计
type UploaderStats struct {
	Uploader       string `json:"uploader"`
	Total          int    `json:"total"`
	Completed      int    `json:"completed"`
	Downloading    int    `json:"downloading"`
	Pending        int    `json:"pending"`
	Failed         int    `json:"failed"`
	Skipped        int    `json:"skipped"`
	ChargeBlocked  int    `json:"charge_blocked"`
	LastDownloadAt string `json:"last_download_at"`
}

// GetDownloadUploaders 获取 UP 主列表（分页 + 筛选）
func (d *DB) GetDownloadUploaders(status, search string, page, pageSize int) ([]UploaderStats, int, error) {
	where := "WHERE 1=1"
	args := []interface{}{}

	if search != "" {
		where += " AND d.uploader LIKE ?"
		args = append(args, "%"+search+"%")
	}

	// 先统计总数
	countQuery := fmt.Sprintf("SELECT COUNT(DISTINCT d.uploader) FROM downloads d %s AND d.uploader != ''", where)
	var total int
	if err := d.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// 分页查询
	query := fmt.Sprintf(`
		SELECT d.uploader,
			COUNT(*) as total,
			SUM(CASE WHEN d.status IN ('completed','relocated') THEN 1 ELSE 0 END) as completed,
			SUM(CASE WHEN d.status = 'downloading' THEN 1 ELSE 0 END) as downloading,
			SUM(CASE WHEN d.status = 'pending' THEN 1 ELSE 0 END) as pending,
			SUM(CASE WHEN d.status IN ('failed','permanent_failed') THEN 1 ELSE 0 END) as failed,
			SUM(CASE WHEN d.status = 'skipped' THEN 1 ELSE 0 END) as skipped,
			SUM(CASE WHEN d.status = 'charge_blocked' THEN 1 ELSE 0 END) as charge_blocked,
			MAX(d.created_at) as last_download_at
		FROM downloads d
		%s AND d.uploader != ''
		GROUP BY d.uploader
		ORDER BY last_download_at DESC
		LIMIT ? OFFSET ?
	`, where)

	offset := (page - 1) * pageSize
	args = append(args, pageSize, offset)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var uploaders []UploaderStats
	for rows.Next() {
		var u UploaderStats
		if err := rows.Scan(&u.Uploader, &u.Total, &u.Completed, &u.Downloading, &u.Pending, &u.Failed, &u.Skipped, &u.ChargeBlocked, &u.LastDownloadAt); err != nil {
			return nil, 0, err
		}
		uploaders = append(uploaders, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return uploaders, total, nil
}

// GetDownloadsByUploader 获取单个 UP 主的视频列表（分页）
func (d *DB) GetDownloadsByUploaderPaged(uploader, status string, page, pageSize int) ([]Download, int, error) {
	where := "WHERE d.uploader = ?"
	args := []interface{}{uploader}

	if status != "" && status != "all" {
		where += " AND d.status = ?"
		args = append(args, status)
	}

	// 总数
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM downloads d %s", where)
	var total int
	if err := d.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// 分页（按状态优先级排序）
	query := fmt.Sprintf(`
		SELECT id, source_id, video_id, title, status, file_path,
			COALESCE(uploader,''), COALESCE(thumbnail,''), COALESCE(description,''),
			COALESCE(duration,0), COALESCE(thumb_path,''),
			COALESCE(retry_count,0), COALESCE(last_error,''), created_at
		FROM downloads d %s
		ORDER BY
			CASE status
				WHEN 'downloading' THEN 1
				WHEN 'pending' THEN 2
				WHEN 'failed' THEN 3
				WHEN 'completed' THEN 4
				WHEN 'relocated' THEN 5
				ELSE 6
			END,
			created_at DESC
		LIMIT ? OFFSET ?
	`, where)

	offset := (page - 1) * pageSize
	args = append(args, pageSize, offset)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var downloads []Download
	for rows.Next() {
		var dl Download
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title,
			&dl.Status, &dl.FilePath, &dl.Uploader, &dl.Thumbnail, &dl.Description,
			&dl.Duration, &dl.ThumbPath, &dl.RetryCount, &dl.LastError, &dl.CreatedAt); err != nil {
			return nil, 0, err
		}
		downloads = append(downloads, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return downloads, total, nil
}

// GetDownloadStatsByUploader 获取单个 UP 主的全量统计（不受筛选影响）
func (d *DB) GetDownloadStatsByUploader(uploader string) (*UploaderStats, error) {
	query := `
		SELECT uploader,
			COUNT(*) as total,
			SUM(CASE WHEN status IN ('completed','relocated') THEN 1 ELSE 0 END) as completed,
			SUM(CASE WHEN status = 'downloading' THEN 1 ELSE 0 END) as downloading,
			SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) as pending,
			SUM(CASE WHEN status IN ('failed','permanent_failed') THEN 1 ELSE 0 END) as failed,
			SUM(CASE WHEN status = 'skipped' THEN 1 ELSE 0 END) as skipped,
			SUM(CASE WHEN status = 'charge_blocked' THEN 1 ELSE 0 END) as charge_blocked,
			MAX(created_at) as last_download_at
		FROM downloads WHERE uploader = ?
	`
	var u UploaderStats
	err := d.QueryRow(query, uploader).Scan(&u.Uploader, &u.Total, &u.Completed, &u.Downloading, &u.Pending, &u.Failed, &u.Skipped, &u.ChargeBlocked, &u.LastDownloadAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// RetryFailedByUploader 重试指定 UP 主的所有失败下载
func (d *DB) RetryFailedByUploader(uploader string) (int64, error) {
	result, err := d.Exec("UPDATE downloads SET status = 'pending', retry_count = 0, last_error = '' WHERE uploader = ? AND status IN ('failed','permanent_failed')", uploader)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteCompletedByUploader 删除指定 UP 主的已完成记录
func (d *DB) DeleteCompletedByUploader(uploader string) (int64, error) {
	result, err := d.Exec("DELETE FROM downloads WHERE uploader = ? AND status IN ('completed','relocated')", uploader)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetPendingByUploader 获取指定 UP 主的 pending 下载
func (d *DB) GetPendingByUploader(uploader string) ([]Download, error) {
	rows, err := d.Query("SELECT id, source_id, video_id, title, status, file_path, COALESCE(uploader,''), COALESCE(retry_count,0), COALESCE(last_error,''), created_at FROM downloads WHERE uploader = ? AND status = 'pending'", uploader)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var downloads []Download
	for rows.Next() {
		var dl Download
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Status, &dl.FilePath, &dl.Uploader, &dl.RetryCount, &dl.LastError, &dl.CreatedAt); err != nil {
			return nil, err
		}
		downloads = append(downloads, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return downloads, nil
}

// DeleteAllCompleted 删除所有已完成记录
func (d *DB) DeleteAllCompleted() (int64, error) {
	result, err := d.Exec("DELETE FROM downloads WHERE status IN ('completed','relocated')")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// UpdateDetailStatus 更新下载记录的 detail_status 位图（OR 合并）
func (d *DB) UpdateDetailStatus(id int64, bits int) error {
	_, err := d.Exec("UPDATE downloads SET detail_status = detail_status | ? WHERE id = ?", bits, id)
	return err
}

// GetDetailStatus 获取 detail_status 位图
func (d *DB) GetDetailStatus(id int64) (int, error) {
	var status int
	err := d.QueryRow("SELECT COALESCE(detail_status, 0) FROM downloads WHERE id = ?", id).Scan(&status)
	return status, err
}
