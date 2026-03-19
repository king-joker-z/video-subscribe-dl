package db

import (
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
	d.Exec("DELETE FROM downloads WHERE source_id = ?", id)
	_, err := d.Exec("DELETE FROM sources WHERE id = ?", id)
	return err
}

// DeleteSourceWithFiles 删除订阅并清除本地文件
func (d *DB) DeleteSourceWithFiles(id int64) (int, error) {
	// 1. 查询所有 file_path
	rows, err := d.Query("SELECT file_path FROM downloads WHERE source_id = ?", id)
	if err != nil {
		// 即使查询失败也继续删除 DB 记录
		log.Printf("[source] Warning: failed to query file paths for source %d: %v", id, err)
		err2 := d.DeleteSource(id)
		return 0, err2
	}
	var paths []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		if p != "" {
			paths = append(paths, p)
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

	// 3. 删 DB 记录
	d.Exec("DELETE FROM downloads WHERE source_id = ?", id)
	_, err = d.Exec("DELETE FROM sources WHERE id = ?", id)
	return deleted, err
}

func (d *DB) UpdateSourceLastCheck(id int64) error {
	_, err := d.Exec("UPDATE sources SET last_check = CURRENT_TIMESTAMP WHERE id = ?", id)
	return err
}

func (d *DB) CleanSource(id int64) (int, error) {
	rows, err := d.Query("SELECT file_path FROM downloads WHERE source_id = ? AND status = 'completed'", id)
	if err != nil {
		return 0, err
	}
	var count int
	for rows.Next() {
		var path string
		rows.Scan(&path)
		if path != "" {
			count++
		}
	}
	rows.Close()
	_, err = d.Exec("DELETE FROM downloads WHERE source_id = ?", id)
	return count, err
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

func (d *DB) GetSourcesDueForCheck(globalInterval int) ([]Source, error) {
	var query string
	if globalInterval > 0 {
		query = fmt.Sprintf("SELECT "+sourceColumns+` FROM sources 
			WHERE enabled = 1 
			  AND (last_check IS NULL OR datetime(last_check, '+%d seconds') <= datetime('now'))`, globalInterval)
	} else {
		query = "SELECT " + sourceColumns + ` FROM sources 
			WHERE enabled = 1 
			  AND (last_check IS NULL OR datetime(last_check, '+' || check_interval || ' seconds') <= datetime('now'))`
	}
	rows, err := d.Query(query)
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
