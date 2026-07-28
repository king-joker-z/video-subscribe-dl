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
    download_subtitle INTEGER DEFAULT 0,
    download_filter TEXT DEFAULT '',
    download_quality_min TEXT DEFAULT '',
    skip_nfo INTEGER DEFAULT 0,
    skip_poster INTEGER DEFAULT 0,
    filter_rules TEXT DEFAULT '',
    use_dynamic_api INTEGER DEFAULT 0,
    latest_video_at INTEGER DEFAULT 0,
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
    detail_status INTEGER DEFAULT 0,
    last_error TEXT,
    next_retry_at INTEGER NOT NULL DEFAULT 0,
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

`

var indexes = `
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
	ID                 int64      `json:"id"`
	Type               string     `json:"type"`
	URL                string     `json:"url"`
	Name               string     `json:"name"`
	CookiesFile        string     `json:"cookies_file"`
	CheckInterval      int        `json:"check_interval"`
	DownloadQuality    string     `json:"download_quality"`
	DownloadCodec      string     `json:"download_codec"`
	DownloadDanmaku    bool       `json:"download_danmaku"`
	DownloadSubtitle   bool       `json:"download_subtitle"`
	DownloadFilter     string     `json:"download_filter"`
	DownloadQualityMin string     `json:"download_quality_min"`
	SkipNFO            bool       `json:"skip_nfo"`
	SkipPoster         bool       `json:"skip_poster"`
	FilterRules        string     `json:"filter_rules"`
	UseDynamicAPI      bool       `json:"use_dynamic_api"`
	LatestVideoAt      int64      `json:"latest_video_at"`
	Enabled            bool       `json:"enabled"`
	LastCheck          *time.Time `json:"last_check"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
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
	DetailStatus int        `json:"detail_status"` // 位图: 1=封面 2=视频 4=NFO 8=弹幕 16=字幕
	LastError    string     `json:"last_error"`
	CreatedAt    time.Time  `json:"created_at"`
	NextRetryAt  int64      `json:"next_retry_at"`
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

	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	// 迁移与索引创建必须分阶段进行：旧库的表不会被 CREATE TABLE IF NOT
	// EXISTS 补列，若索引先引用新列会使迁移永远无法开始。
	migrations := []migration{
		{"sources", "type", "ALTER TABLE sources ADD COLUMN type TEXT DEFAULT 'channel'"},
		{"sources", "last_check", "ALTER TABLE sources ADD COLUMN last_check DATETIME"},
		{"downloads", "uploader", "ALTER TABLE downloads ADD COLUMN uploader TEXT"},
		{"downloads", "description", "ALTER TABLE downloads ADD COLUMN description TEXT"},
		{"downloads", "thumbnail", "ALTER TABLE downloads ADD COLUMN thumbnail TEXT"},
		{"downloads", "duration", "ALTER TABLE downloads ADD COLUMN duration INTEGER DEFAULT 0"},
		{"sources", "download_codec", "ALTER TABLE sources ADD COLUMN download_codec TEXT DEFAULT 'all'"},
		{"sources", "download_danmaku", "ALTER TABLE sources ADD COLUMN download_danmaku INTEGER DEFAULT 0"},
		{"downloads", "thumb_path", "ALTER TABLE downloads ADD COLUMN thumb_path TEXT"},
		{"downloads", "retry_count", "ALTER TABLE downloads ADD COLUMN retry_count INTEGER DEFAULT 0"},
		{"downloads", "last_error", "ALTER TABLE downloads ADD COLUMN last_error TEXT"},
		{"sources", "download_filter", "ALTER TABLE sources ADD COLUMN download_filter TEXT DEFAULT ''"},
		{"sources", "download_quality_min", "ALTER TABLE sources ADD COLUMN download_quality_min TEXT DEFAULT ''"},
		{"sources", "skip_nfo", "ALTER TABLE sources ADD COLUMN skip_nfo INTEGER DEFAULT 0"},
		{"sources", "skip_poster", "ALTER TABLE sources ADD COLUMN skip_poster INTEGER DEFAULT 0"},
		{"sources", "latest_video_at", "ALTER TABLE sources ADD COLUMN latest_video_at INTEGER DEFAULT 0"},
		{"sources", "use_dynamic_api", "ALTER TABLE sources ADD COLUMN use_dynamic_api INTEGER DEFAULT 0"},
		{"sources", "filter_rules", "ALTER TABLE sources ADD COLUMN filter_rules TEXT DEFAULT ''"},
		{"sources", "download_subtitle", "ALTER TABLE sources ADD COLUMN download_subtitle INTEGER DEFAULT 0"},
		{"downloads", "detail_status", "ALTER TABLE downloads ADD COLUMN detail_status INTEGER DEFAULT 0"},
		{"downloads", "next_retry_at", `ALTER TABLE downloads ADD COLUMN next_retry_at INTEGER NOT NULL DEFAULT 0`},
	}
	for _, m := range migrations {
		exists, err := columnExists(db, m.table, m.column)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("inspect database column %s.%s: %w", m.table, m.column, err)
		}
		if exists {
			continue
		}
		if _, err := db.Exec(m.sql); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate database %s.%s: %w", m.table, m.column, err)
		}
	}

	if _, err := db.Exec(indexes); err != nil {
		db.Close()
		return nil, fmt.Errorf("create indexes: %w", err)
	}
	if err := validateSchema(db, migrations); err != nil {
		db.Close()
		return nil, err
	}

	return &DB{db}, nil
}

type migration struct {
	table  string
	column string
	sql    string
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	if table != "sources" && table != "downloads" {
		return false, fmt.Errorf("unsupported schema table %q", table)
	}
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func validateSchema(db *sql.DB, migrations []migration) error {
	for _, m := range migrations {
		exists, err := columnExists(db, m.table, m.column)
		if err != nil {
			return fmt.Errorf("validate database column %s.%s: %w", m.table, m.column, err)
		}
		if !exists {
			return fmt.Errorf("validate database column %s.%s: missing after migration", m.table, m.column)
		}
	}
	return nil
}

// GetSourcesDueForCheck 返回到期需要检查的 enabled sources
// globalInterval 为全局覆盖间隔(秒)，0 表示不覆盖，使用各 source 自身的 check_interval
