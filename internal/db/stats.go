package db

// MonthStat 按月统计
type MonthStat struct {
	Month string `json:"month"` // "2025-01"
	Count int    `json:"count"`
}

// UploaderStat 按 UP 主统计
type UploaderStat struct {
	Uploader  string `json:"uploader"`
	Count     int    `json:"count"`
	TotalSize int64  `json:"total_size"`
}

// DetailedStats 完整统计信息
type DetailedStats struct {
	Total         int   `json:"total"`
	Completed     int   `json:"completed"`
	Failed        int   `json:"failed"`
	Pending       int   `json:"pending"`
	ChargeBlocked int   `json:"charge_blocked"`
	TotalSize     int64 `json:"total_size"` // bytes
	Sources       int   `json:"sources"`
}

func (d *DB) GetStats() (map[string]int, error) {
	stats := map[string]int{
		"sources":        0,
		"pending":        0,
		"downloading":    0,
		"completed":      0,
		"failed":         0,
		"relocated":      0,
		"charge_blocked": 0,
		"total":          0,
	}

	var sources int
	d.QueryRow("SELECT COUNT(*) FROM sources WHERE enabled = 1").Scan(&sources)
	stats["sources"] = sources

	rows, err := d.Query(`
		SELECT status, COUNT(*) as cnt
		FROM downloads
		GROUP BY status
	`)
	if err != nil {
		return stats, nil
	}
	defer rows.Close()

	var total int
	for rows.Next() {
		var status string
		var cnt int
		if err := rows.Scan(&status, &cnt); err != nil {
			continue
		}
		total += cnt
		switch status {
		case "pending":
			stats["pending"] = cnt
		case "downloading":
			stats["downloading"] = cnt
		case "completed":
			stats["completed"] += cnt
		case "relocated":
			// relocated 同时计入 completed（Dashboard 展示用）和 relocated（独立统计）
			stats["completed"] += cnt
			stats["relocated"] = cnt
		case "failed", "permanent_failed":
			stats["failed"] += cnt
		case "charge_blocked":
			stats["charge_blocked"] = cnt
		}
	}
	stats["total"] = total
	return stats, nil
}

// GetStatsDetailed 返回完整统计（总下载数、成功数、失败数、总文件大小）
func (d *DB) GetStatsDetailed() (*DetailedStats, error) {
	s := &DetailedStats{}
	d.QueryRow("SELECT COUNT(*) FROM downloads").Scan(&s.Total)
	d.QueryRow("SELECT COUNT(*) FROM downloads WHERE status IN ('completed','relocated')").Scan(&s.Completed)
	d.QueryRow("SELECT COUNT(*) FROM downloads WHERE status IN ('failed','permanent_failed')").Scan(&s.Failed)
	d.QueryRow("SELECT COUNT(*) FROM downloads WHERE status = 'pending'").Scan(&s.Pending)
	d.QueryRow("SELECT COALESCE(SUM(file_size),0) FROM downloads WHERE status IN ('completed','relocated')").Scan(&s.TotalSize)
	d.QueryRow("SELECT COUNT(*) FROM downloads WHERE status = 'charge_blocked'").Scan(&s.ChargeBlocked)
	d.QueryRow("SELECT COUNT(*) FROM sources WHERE enabled = 1").Scan(&s.Sources)
	return s, nil
}

// GetStatsByMonth 按月统计下载量（最近12个月）
func (d *DB) GetStatsByMonth() ([]MonthStat, error) {
	rows, err := d.Query(`
		SELECT strftime('%Y-%m', COALESCE(downloaded_at, created_at)) AS month, COUNT(*) AS cnt
		FROM downloads
		WHERE status IN ('completed','relocated')
		  AND COALESCE(downloaded_at, created_at) >= date('now', '-12 months')
		GROUP BY month
		ORDER BY month ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []MonthStat
	for rows.Next() {
		var s MonthStat
		if err := rows.Scan(&s.Month, &s.Count); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return stats, nil
}

// GetStatsByUploader 按 UP 主统计下载量 top N
func (d *DB) GetStatsByUploader(limit int) ([]UploaderStat, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := d.Query(`
		SELECT COALESCE(s.name, dl.uploader, '未知') AS up, COUNT(*) AS cnt, COALESCE(SUM(dl.file_size),0) AS total_size
		FROM downloads dl
		LEFT JOIN sources s ON s.id = dl.source_id
		WHERE dl.status IN ('completed','relocated')
		GROUP BY up
		ORDER BY cnt DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []UploaderStat
	for rows.Next() {
		var s UploaderStat
		if err := rows.Scan(&s.Uploader, &s.Count, &s.TotalSize); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return stats, nil
}


// GetStats24h 最近 24 小时完成的下载数量
func (d *DB) GetStats24h() (int, error) {
	var count int
	err := d.QueryRow(`
		SELECT COUNT(*) FROM downloads
		WHERE status IN ('completed', 'relocated')
		  AND downloaded_at >= datetime('now', '-24 hours')
	`).Scan(&count)
	return count, err
}
