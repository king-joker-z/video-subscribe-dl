package db

import (
	"time"
)

// GetOldCompletedDownloads returns completed downloads older than retentionDays
func (d *DB) GetOldCompletedDownloads(retentionDays int) ([]Download, error) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	rows, err := d.Query(`
		SELECT id, source_id, video_id, COALESCE(title,''), COALESCE(filename,''), status,
		       COALESCE(file_path,''), file_size, COALESCE(uploader,''), COALESCE(description,''),
		       duration, downloaded_at, COALESCE(error_message,''), created_at
		FROM downloads
		WHERE status = 'completed' AND downloaded_at < ? AND file_path != ''
		ORDER BY downloaded_at ASC
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var downloads []Download
	for rows.Next() {
		var dl Download
		if err := rows.Scan(&dl.ID, &dl.SourceID, &dl.VideoID, &dl.Title, &dl.Filename,
			&dl.Status, &dl.FilePath, &dl.FileSize, &dl.Uploader, &dl.Description,
			&dl.Duration, &dl.DownloadedAt, &dl.ErrorMessage, &dl.CreatedAt); err != nil {
			return nil, err
		}
		downloads = append(downloads, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return downloads, nil
}

// MarkDownloadCleaned marks a download as cleaned (file deleted, record preserved)
func (d *DB) MarkDownloadCleaned(id int64) error {
	_, err := d.Exec("UPDATE downloads SET status = 'cleaned', file_path = '' WHERE id = ?", id)
	return err
}

// GetCleanupStats returns stats about cleaned downloads
func (d *DB) GetCleanupStats() (totalCleaned int64, freedBytes int64, err error) {
	err = d.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(file_size), 0)
		FROM downloads WHERE status = 'cleaned'
	`).Scan(&totalCleaned, &freedBytes)
	return
}
