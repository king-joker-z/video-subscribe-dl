package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "github.com/glebarez/sqlite"
)

var schema = `
CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT DEFAULT 'channel',
    url TEXT NOT NULL,
    name TEXT,
    cookies_file TEXT,
    check_interval INTEGER DEFAULT 1800,
    download_quality TEXT DEFAULT 'best',
    download_codec TEXT DEFAULT 'all',
    download_danmaku INTEGER DEFAULT 0,
    enabled INTEGER DEFAULT 1,
    last_check DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS downloads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER,
    video_id TEXT NOT NULL,
    title TEXT,
    filename TEXT,
    status TEXT DEFAULT 'pending',
    file_path TEXT,
    file_size INTEGER DEFAULT 0,
    uploader TEXT,
    description TEXT,
    thumbnail TEXT,
    thumb_path TEXT,
    duration INTEGER DEFAULT 0,
    downloaded_at DATETIME,
    error_message TEXT,
    retry_count INTEGER DEFAULT 0,
    last_error TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE,
    UNIQUE(source_id, video_id)
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE IF NOT EXISTS people (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    mid TEXT UNIQUE,
    name TEXT,
    avatar TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_downloads_status ON downloads(status);
CREATE INDEX IF NOT EXISTS idx_downloads_source ON downloads(source_id);
CREATE INDEX IF NOT EXISTS idx_downloads_uploader ON downloads(uploader);
CREATE INDEX IF NOT EXISTS idx_downloads_video_id ON downloads(video_id);
CREATE INDEX IF NOT EXISTS idx_downloads_source_video ON downloads(source_id, video_id);
`

type DB struct {
	*sql.DB
}

type Source struct {
	ID              int64      `json:"id"`
	Type            string     `json:"type"`
	URL             string     `json:"url"`
	Name            string     `json:"name"`
	CookiesFile     string     `json:"cookies_file"`
	CheckInterval   int        `json:"check_interval"`
	DownloadQuality string     `json:"download_quality"`
	DownloadCodec   string     `json:"download_codec"`
	DownloadDanmaku bool       `json:"download_danmaku"`
	DownloadFilter  string     `json:"download_filter"`
	DownloadQualityMin string  `json:"download_quality_min"`
	SkipNFO         bool       `json:"skip_nfo"`
	SkipPoster      bool       `json:"skip_poster"`
	Enabled         bool       `json:"enabled"`
	LastCheck       *time.Time `json:"last_check"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type Download struct {
	ID           int64      `json:"id"`
	SourceID     int64      `json:"source_id"`
	VideoID      string     `json:"video_id"`
	Title        string     `json:"title"`
	Filename     string     `json:"filename"`
	Status       string     `json:"status"`
	FilePath     string     `json:"file_path"`
	FileSize     int64      `json:"file_size"`
	Uploader     string     `json:"uploader"`
	Description  string     `json:"description"`
	Thumbnail    string     `json:"thumbnail"`
	ThumbPath    string     `json:"thumb_path"`
	Duration     int        `json:"duration"`
	DownloadedAt *time.Time `json:"downloaded_at"`
	ErrorMessage string     `json:"error_message"`
	RetryCount   int        `json:"retry_count"`
	LastError    string     `json:"last_error"`
	CreatedAt    time.Time  `json:"created_at"`
}

type Person struct {
	ID        int64     `json:"id"`
	MID       string    `json:"mid"`
	Name      string    `json:"name"`
	Avatar    string    `json:"avatar"`
	CreatedAt time.Time `json:"created_at"`
}

func Init(dataDir string) (*DB, error) {
	dbPath := filepath.Join(dataDir, "video-subscribe-dl.db")

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-8192)&_pragma=temp_store(MEMORY)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// 连接池: 串行化写入，消除 SQLITE_BUSY 锁竞争
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	_, err = db.Exec(schema)
	if err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}

	// 迁移
	migrations := []string{
		"ALTER TABLE sources ADD COLUMN type TEXT DEFAULT 'channel'",
		"ALTER TABLE sources ADD COLUMN last_check DATETIME",
		"ALTER TABLE downloads ADD COLUMN uploader TEXT",
		"ALTER TABLE downloads ADD COLUMN description TEXT",
		"ALTER TABLE downloads ADD COLUMN thumbnail TEXT",
		"ALTER TABLE downloads ADD COLUMN duration INTEGER DEFAULT 0",
		"ALTER TABLE sources ADD COLUMN download_codec TEXT DEFAULT 'all'",
		"ALTER TABLE sources ADD COLUMN download_danmaku INTEGER DEFAULT 0",
		"ALTER TABLE downloads ADD COLUMN thumb_path TEXT",
		"ALTER TABLE downloads ADD COLUMN retry_count INTEGER DEFAULT 0",
		"ALTER TABLE downloads ADD COLUMN last_error TEXT",
		"ALTER TABLE sources ADD COLUMN download_filter TEXT DEFAULT ''",
		"ALTER TABLE sources ADD COLUMN download_quality_min TEXT DEFAULT ''",
		"ALTER TABLE sources ADD COLUMN skip_nfo INTEGER DEFAULT 0",
		"ALTER TABLE sources ADD COLUMN skip_poster INTEGER DEFAULT 0",
	}
	for _, m := range migrations {
		db.Exec(m)
	}

	return &DB{db}, nil
}

// GetSourcesDueForCheck 返回到期需要检查的 enabled sources
// globalInterval 为全局覆盖间隔(秒)，0 表示不覆盖，使用各 source 自身的 check_interval
func (d *DB) GetSourcesDueForCheck(globalInterval int) ([]Source, error) {
	var query string
	if globalInterval > 0 {
		// 使用全局间隔覆盖
		query = fmt.Sprintf(`
			SELECT id, COALESCE(type,'channel'), url, COALESCE(name,''), COALESCE(cookies_file,''), 
			       check_interval, COALESCE(download_quality,'best'), COALESCE(download_codec,'all'), 
			       COALESCE(download_danmaku,0), enabled, last_check, created_at, updated_at
			FROM sources 
			WHERE enabled = 1 
			  AND (last_check IS NULL OR datetime(last_check, '+%d seconds') <= datetime('now'))
		`, globalInterval)
	} else {
		query = `
			SELECT id, COALESCE(type,'channel'), url, COALESCE(name,''), COALESCE(cookies_file,''), 
			       check_interval, COALESCE(download_quality,'best'), COALESCE(download_codec,'all'), 
			       COALESCE(download_danmaku,0), enabled, last_check, created_at, updated_at
			FROM sources 
			WHERE enabled = 1 
			  AND (last_check IS NULL OR datetime(last_check, '+' || check_interval || ' seconds') <= datetime('now'))
		`
	}

	rows, err := d.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []Source
	for rows.Next() {
		var s Source
		var enabled, danmaku int
		if err := rows.Scan(&s.ID, &s.Type, &s.URL, &s.Name, &s.CookiesFile,
			&s.CheckInterval, &s.DownloadQuality, &s.DownloadCodec, &danmaku, &enabled,
			&s.LastCheck, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Enabled = enabled == 1
		s.DownloadDanmaku = danmaku == 1
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}
