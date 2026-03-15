package db

import "database/sql"

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
	// 全局去重：只要任何订阅源下载过就不重复（同一视频可能出现在多个源：UP主空间+收藏夹）
	// 排除 permanent_failed 让用户可以通过清理后重新触发
	err := d.QueryRow(`
		SELECT COUNT(*) FROM downloads WHERE video_id = ? AND status NOT IN ('permanent_failed', 'pending')
	`, videoID).Scan(&exists)
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
		       duration, downloaded_at, COALESCE(error_message,''), COALESCE(retry_count,0), COALESCE(last_error,''), created_at
		FROM downloads WHERE id = ?
	`, id)
	var dl Download
	var downloadedAt sql.NullTime
	err := row.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename, &dl.Status,
		&dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description, &dl.Thumbnail, &dl.ThumbPath,
		&dl.Duration, &downloadedAt, &dl.ErrorMessage, &dl.RetryCount, &dl.LastError, &dl.CreatedAt)
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
	return downloads, nil
}
