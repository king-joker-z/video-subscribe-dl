package db

func (d *DB) CreateSource(s *Source) (int64, error) {
	if s.Type == "" {
		s.Type = "channel"
	}
	result, err := d.Exec(`
		INSERT INTO sources (type, url, name, cookies_file, check_interval, download_quality, download_codec, download_danmaku, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, s.Type, s.URL, s.Name, s.CookiesFile, s.CheckInterval, s.DownloadQuality, s.DownloadCodec, s.DownloadDanmaku, s.Enabled)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (d *DB) GetSources() ([]Source, error) {
	rows, err := d.Query(`
		SELECT id, COALESCE(type,'channel'), url, COALESCE(name,''), COALESCE(cookies_file,''), 
		       check_interval, COALESCE(download_quality,'best'), COALESCE(download_codec,'all'), 
		       COALESCE(download_danmaku,0), enabled, last_check, created_at, updated_at,
		       COALESCE(download_filter,''), COALESCE(download_quality_min,''),
		       COALESCE(skip_nfo,0), COALESCE(skip_poster,0)
		FROM sources ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []Source
	for rows.Next() {
		var s Source
		var enabled, danmaku, skipNFO, skipPoster int
		if err := rows.Scan(&s.ID, &s.Type, &s.URL, &s.Name, &s.CookiesFile,
			&s.CheckInterval, &s.DownloadQuality, &s.DownloadCodec, &danmaku, &enabled,
			&s.LastCheck, &s.CreatedAt, &s.UpdatedAt,
			&s.DownloadFilter, &s.DownloadQualityMin, &skipNFO, &skipPoster); err != nil {
			return nil, err
		}
		s.Enabled = enabled == 1
		s.DownloadDanmaku = danmaku == 1
		s.SkipNFO = skipNFO == 1
		s.SkipPoster = skipPoster == 1
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

func (d *DB) GetEnabledSources() ([]Source, error) {
	rows, err := d.Query(`
		SELECT id, COALESCE(type,'channel'), url, COALESCE(name,''), COALESCE(cookies_file,''), 
		       check_interval, COALESCE(download_quality,'best'), COALESCE(download_codec,'all'), 
		       COALESCE(download_danmaku,0), enabled, last_check, created_at, updated_at,
		       COALESCE(download_filter,''), COALESCE(download_quality_min,''),
		       COALESCE(skip_nfo,0), COALESCE(skip_poster,0)
		FROM sources WHERE enabled = 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []Source
	for rows.Next() {
		var s Source
		var enabled, danmaku, skipNFO, skipPoster int
		if err := rows.Scan(&s.ID, &s.Type, &s.URL, &s.Name, &s.CookiesFile,
			&s.CheckInterval, &s.DownloadQuality, &s.DownloadCodec, &danmaku, &enabled,
			&s.LastCheck, &s.CreatedAt, &s.UpdatedAt,
			&s.DownloadFilter, &s.DownloadQualityMin, &skipNFO, &skipPoster); err != nil {
			return nil, err
		}
		s.Enabled = enabled == 1
		s.DownloadDanmaku = danmaku == 1
		s.SkipNFO = skipNFO == 1
		s.SkipPoster = skipPoster == 1
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

func (d *DB) GetSource(id int64) (*Source, error) {
	var s Source
	var enabled, danmaku, skipNFO, skipPoster int
	err := d.QueryRow(`
		SELECT id, COALESCE(type,'channel'), url, COALESCE(name,''), COALESCE(cookies_file,''), 
		       check_interval, COALESCE(download_quality,'best'), COALESCE(download_codec,'all'), 
		       COALESCE(download_danmaku,0), enabled, last_check, created_at, updated_at,
		       COALESCE(download_filter,''), COALESCE(download_quality_min,''),
		       COALESCE(skip_nfo,0), COALESCE(skip_poster,0)
		FROM sources WHERE id = ?
	`, id).Scan(&s.ID, &s.Type, &s.URL, &s.Name, &s.CookiesFile,
		&s.CheckInterval, &s.DownloadQuality, &s.DownloadCodec, &danmaku, &enabled,
		&s.LastCheck, &s.CreatedAt, &s.UpdatedAt,
		&s.DownloadFilter, &s.DownloadQualityMin, &skipNFO, &skipPoster)
	if err != nil {
		return nil, err
	}
	s.Enabled = enabled == 1
	s.DownloadDanmaku = danmaku == 1
	s.SkipNFO = skipNFO == 1
	s.SkipPoster = skipPoster == 1
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
	skipNFO := 0
	if s.SkipNFO {
		skipNFO = 1
	}
	skipPoster := 0
	if s.SkipPoster {
		skipPoster = 1
	}
	_, err := d.Exec(`
		UPDATE sources SET type=?, url=?, name=?, cookies_file=?, check_interval=?, 
		download_quality=?, download_codec=?, download_danmaku=?, enabled=?,
		download_filter=?, download_quality_min=?, skip_nfo=?, skip_poster=?,
		updated_at=CURRENT_TIMESTAMP
		WHERE id = ?
	`, s.Type, s.URL, s.Name, s.CookiesFile, s.CheckInterval,
		s.DownloadQuality, s.DownloadCodec, danmaku, enabled,
		s.DownloadFilter, s.DownloadQualityMin, skipNFO, skipPoster, s.ID)
	return err
}

func (d *DB) DeleteSource(id int64) error {
	// 级联删除下载记录
	d.Exec("DELETE FROM downloads WHERE source_id = ?", id)
	_, err := d.Exec("DELETE FROM sources WHERE id = ?", id)
	return err
}

func (d *DB) UpdateSourceLastCheck(id int64) error {
	_, err := d.Exec("UPDATE sources SET last_check = CURRENT_TIMESTAMP WHERE id = ?", id)
	return err
}

// 清理源的所有下载记录和文件
func (d *DB) CleanSource(id int64) (int, error) {
	// 获取该源的所有下载
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

	// 删除下载记录
	_, err = d.Exec("DELETE FROM downloads WHERE source_id = ?", id)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// UpdateSourceLatestVideoAt 更新源的最新视频时间戳
func (d *DB) UpdateSourceLatestVideoAt(id int64, ts int64) error {
	_, err := d.Exec("UPDATE sources SET latest_video_at = ? WHERE id = ?", ts, id)
	return err
}

// GetSourceLatestVideoAt 获取源的最新视频时间戳
func (d *DB) GetSourceLatestVideoAt(id int64) (int64, error) {
	var ts int64
	err := d.QueryRow("SELECT COALESCE(latest_video_at, 0) FROM sources WHERE id = ?", id).Scan(&ts)
	return ts, err
}
