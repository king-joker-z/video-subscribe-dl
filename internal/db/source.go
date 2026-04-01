package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
)

// sourceColumns 统一的 source SELECT 列（修改字段时只改这一处）
const sourceColumns = `id, COALESCE(type,'channel'), url, COALESCE(name,''), COALESCE(cookies_file,''), 
       check_interval, COALESCE(download_quality,'best'), COALESCE(download_codec,'all'), 
       COALESCE(download_danmaku,0), COALESCE(download_subtitle,0), enabled, last_check, created_at, updated_at,
       COALESCE(download_filter,''), COALESCE(download_quality_min,''),
       COALESCE(skip_nfo,0), COALESCE(skip_poster,0), COALESCE(use_dynamic_api,0), COALESCE(filter_rules,'')`

// scanSource 统一的 source 行扫描（修改字段时只改这一处）
func scanSource(scanner interface {
	Scan(dest ...interface{}) error
}) (Source, error) {
	var s Source
	var enabled, danmaku, subtitle, skipNFO, skipPoster, useDynamic int
	err := scanner.Scan(&s.ID, &s.Type, &s.URL, &s.Name, &s.CookiesFile,
		&s.CheckInterval, &s.DownloadQuality, &s.DownloadCodec, &danmaku, &subtitle, &enabled,
		&s.LastCheck, &s.CreatedAt, &s.UpdatedAt,
		&s.DownloadFilter, &s.DownloadQualityMin, &skipNFO, &skipPoster, &useDynamic, &s.FilterRules)
	if err != nil {
		return s, err
	}
	s.Enabled = enabled == 1
	s.DownloadDanmaku = danmaku == 1
	s.DownloadSubtitle = subtitle == 1
	s.SkipNFO = skipNFO == 1
	s.SkipPoster = skipPoster == 1
	s.UseDynamicAPI = useDynamic == 1
	return s, nil
}

func (d *DB) CreateSource(s *Source) (int64, error) {
	if s.Type == "" {
		s.Type = "channel"
	}
	result, err := d.Exec(`
		INSERT INTO sources (type, url, name, cookies_file, check_interval, download_quality, download_codec, download_danmaku, download_subtitle, enabled, filter_rules)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, s.Type, s.URL, s.Name, s.CookiesFile, s.CheckInterval, s.DownloadQuality, s.DownloadCodec, s.DownloadDanmaku, s.DownloadSubtitle, s.Enabled, s.FilterRules)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	s.ID = id
	return id, nil
}

func (d *DB) GetSources() ([]Source, error) {
	rows, err := d.Query("SELECT " + sourceColumns + " FROM sources ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

func (d *DB) GetEnabledSources() ([]Source, error) {
	rows, err := d.Query("SELECT " + sourceColumns + " FROM sources WHERE enabled = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

func (d *DB) GetSource(id int64) (*Source, error) {
	row := d.QueryRow("SELECT "+sourceColumns+" FROM sources WHERE id = ?", id)
	s, err := scanSource(row)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (d *DB) UpdateSource(s *Source) error {
	enabled := 0
	if s.Enabled {
		enabled = 1
	}
	danmaku := 0
	if s.DownloadDanmaku {
		danmaku = 1
	}
	subtitle := 0
	if s.DownloadSubtitle {
		subtitle = 1
	}
	skipNFO := 0
	if s.SkipNFO {
		skipNFO = 1
	}
	skipPoster := 0
	if s.SkipPoster {
		skipPoster = 1
	}
	useDynamic := 0
	if s.UseDynamicAPI {
		useDynamic = 1
	}
	_, err := d.Exec(`
		UPDATE sources SET type=?, url=?, name=?, cookies_file=?, check_interval=?, 
		download_quality=?, download_codec=?, download_danmaku=?, download_subtitle=?, enabled=?,
		download_filter=?, download_quality_min=?, skip_nfo=?, skip_poster=?,
		use_dynamic_api=?, filter_rules=?, updated_at=CURRENT_TIMESTAMP
		WHERE id = ?
	`, s.Type, s.URL, s.Name, s.CookiesFile, s.CheckInterval,
		s.DownloadQuality, s.DownloadCodec, danmaku, subtitle, enabled,
		s.DownloadFilter, s.DownloadQualityMin, skipNFO, skipPoster, useDynamic, s.FilterRules, s.ID)
	return err
}

func (d *DB) DeleteSource(id int64) error {
	// 拒绝删除有活跃下载任务的订阅源，防止文件与 DB 进入不一致状态
	var activeCount int
	d.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status = 'downloading'", id).Scan(&activeCount)
	if activeCount > 0 {
		return fmt.Errorf("该订阅源有 %d 个正在进行的下载任务，请等待完成后再删除", activeCount)
	}
	// [FIXED: P0-2] 将两条 DELETE 包在同一事务中，确保原子性，防止进程崩溃导致数据不一致
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM downloads WHERE source_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM sources WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSourceWithFiles 删除订阅并清除本地文件（含缩略图）
func (d *DB) DeleteSourceWithFiles(id int64) (int, error) {
	// 拒绝删除有活跃下载任务的订阅源（与 DeleteSource 保持一致）
	var activeCount int
	d.QueryRow("SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status = 'downloading'", id).Scan(&activeCount)
	if activeCount > 0 {
		return 0, fmt.Errorf("该订阅源有 %d 个正在进行的下载任务，请等待完成后再删除", activeCount)
	}

	// P0-5: 先将 source 标记为 disabled，防止崩溃后被重新调度
	// 若进程在删文件后、删 DB 前崩溃，重启时 disabled source 不会被调度，避免幽灵记录
	if _, err := d.Exec("UPDATE sources SET enabled=0 WHERE id=?", id); err != nil {
		log.Printf("[source] Warning: failed to disable source %d before deletion: %v", id, err)
	}

	// 1. 查询所有 file_path + thumb_path
	rows, err := d.Query("SELECT COALESCE(file_path,''), COALESCE(thumb_path,'') FROM downloads WHERE source_id = ?", id)
	if err != nil {
		// 即使查询失败也继续删除 DB 记录
		log.Printf("[source] Warning: failed to query file paths for source %d: %v", id, err)
		err2 := d.DeleteSource(id)
		return 0, err2
	}
	var paths []string
	for rows.Next() {
		var fp, tp string
		rows.Scan(&fp, &tp)
		if fp != "" {
			paths = append(paths, fp)
		}
		if tp != "" {
			paths = append(paths, tp)
		}
	}
	rows.Close()

	// 2. 删除文件（用 os.RemoveAll，可以删目录）
	deleted := 0
	for _, p := range paths {
		if err2 := os.RemoveAll(p); err2 != nil {
			log.Printf("[source] Warning: failed to remove %s: %v", p, err2)
		} else {
			deleted++
		}
	}

	// 3. 删 DB 记录（事务包裹，确保原子性）
	tx, err := d.Begin()
	if err != nil {
		return deleted, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM downloads WHERE source_id = ?", id); err != nil {
		return deleted, err
	}
	if _, err = tx.Exec("DELETE FROM sources WHERE id = ?", id); err != nil {
		return deleted, err
	}
	return deleted, tx.Commit()
}

func (d *DB) UpdateSourceLastCheck(id int64) error {
	_, err := d.Exec("UPDATE sources SET last_check = CURRENT_TIMESTAMP WHERE id = ?", id)
	return err
}

// ClearDownloadRecords 清理指定订阅源的下载记录（仅删除 DB 记录，不删除磁盘文件）。
// 此函数只操作数据库，如需同时清理磁盘文件请使用 DeleteSourceWithFiles。
// 返回值为原 completed 且 file_path 非空的记录数量（可用于日志统计）。
// P1-6: wrap SELECT+DELETE in a single transaction so the count and the delete
// are always consistent; a crash between the two steps no longer leaves a
// ghost count.
func (d *DB) ClearDownloadRecords(id int64) (int, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Count records that are completed AND have a local file (non-empty file_path)
	var count int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM downloads WHERE source_id = ? AND status = 'completed' AND COALESCE(file_path,'') != ''",
		id,
	).Scan(&count); err != nil {
		return 0, err
	}

	if _, err := tx.Exec("DELETE FROM downloads WHERE source_id = ?", id); err != nil {
		return 0, err
	}

	return count, tx.Commit()
}

func (d *DB) UpdateSourceLatestVideoAt(id int64, ts int64) error {
	_, err := d.Exec("UPDATE sources SET latest_video_at = ? WHERE id = ?", ts, id)
	return err
}

func (d *DB) GetSourceLatestVideoAt(id int64) (int64, error) {
	var ts int64
	err := d.QueryRow("SELECT COALESCE(latest_video_at, 0) FROM sources WHERE id = ?", id).Scan(&ts)
	return ts, err
}

// GetSourcesDueForCheck 返回到期需要检查的 enabled sources
// GetSourcesPaged 分页获取订阅源列表
func (d *DB) GetSourcesPaged(sourceType string, page, pageSize int) ([]Source, int, error) {
	countSQL := "SELECT COUNT(*) FROM sources"
	var args []interface{}
	if sourceType != "" {
		countSQL += " WHERE type = ?"
		args = append(args, sourceType)
	}
	var total int
	d.QueryRow(countSQL, args...).Scan(&total)

	offset := (page - 1) * pageSize
	dataSQL := "SELECT " + sourceColumns + " FROM sources"
	if sourceType != "" {
		dataSQL += " WHERE type = ?"
	}
	dataSQL += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	dataArgs := append(append([]interface{}{}, args...), pageSize, offset)

	rows, err := d.Query(dataSQL, dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var sources []Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, 0, err
		}
		sources = append(sources, s)
	}
	return sources, total, rows.Err()
}

// SourceExistsByURL 检查是否已存在 url 完全匹配的订阅源（精确匹配）
func (d *DB) SourceExistsByURL(url string) (bool, error) {
	var count int
	err := d.QueryRow("SELECT COUNT(*) FROM sources WHERE url = ?", url).Scan(&count)
	return count > 0, err
}

func (d *DB) GetSourcesDueForCheck(globalInterval int) ([]Source, error) {
	// P2-5: use a parameterized query instead of fmt.Sprintf to embed the integer,
	// avoiding any (theoretical) injection risk and keeping the SQL driver's
	// prepared-statement path.
	var (
		rows *sql.Rows
		err  error
	)
	if globalInterval > 0 {
		rows, err = d.Query(
			"SELECT "+sourceColumns+` FROM sources
			WHERE enabled = 1
			  AND (last_check IS NULL OR datetime(last_check, '+'||?||' seconds') <= datetime('now'))`,
			globalInterval,
		)
	} else {
		rows, err = d.Query(
			"SELECT "+sourceColumns+` FROM sources
			WHERE enabled = 1
			  AND (last_check IS NULL OR datetime(last_check, '+' || check_interval || ' seconds') <= datetime('now'))`,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}
