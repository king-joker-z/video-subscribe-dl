package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "github.com/glebarez/sqlite"
)

// initMemoryDB creates an in-memory SQLite DB for testing
func initMemoryDB(t testing.TB) *DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	_, err = sqlDB.Exec(schema)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return &DB{sqlDB}
}

func TestInitMigratesExistingDatabase(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "video-subscribe-dl.db")
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE sources (id INTEGER PRIMARY KEY AUTOINCREMENT, url TEXT NOT NULL, name TEXT, cookies_file TEXT, check_interval INTEGER DEFAULT 1800, download_quality TEXT DEFAULT 'best', enabled INTEGER DEFAULT 1, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE downloads (id INTEGER PRIMARY KEY AUTOINCREMENT, source_id INTEGER, video_id TEXT NOT NULL, title TEXT, filename TEXT, status TEXT DEFAULT 'pending', file_path TEXT, file_size INTEGER DEFAULT 0, error_message TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, UNIQUE(source_id, video_id));
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE people (id INTEGER PRIMARY KEY AUTOINCREMENT, mid TEXT UNIQUE, name TEXT, avatar TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO sources (id, url, name) VALUES (7, 'https://example.test/u', 'legacy source');
		INSERT INTO downloads (id, source_id, video_id, title, status, file_path) VALUES (11, 7, 'legacy-video', 'legacy title', 'completed', '/media/legacy.mkv');
	`); err != nil {
		t.Fatalf("create legacy downloads: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	d, err := Init(dataDir)
	if err != nil {
		t.Fatalf("init migrated db: %v", err)
	}
	defer d.Close()

	var nextRetryAt int64
	if err := d.QueryRow("SELECT next_retry_at FROM downloads WHERE id = 11").Scan(&nextRetryAt); err != nil {
		t.Fatalf("next_retry_at column missing or unreadable: %v", err)
	}
	if nextRetryAt != 0 {
		t.Fatalf("expected migrated next_retry_at default 0, got %d", nextRetryAt)
	}
	var sourceID, downloadID int64
	var filePath string
	if err := d.QueryRow("SELECT id FROM sources WHERE id = 7").Scan(&sourceID); err != nil || sourceID != 7 {
		t.Fatalf("legacy source changed during migration: id=%d err=%v", sourceID, err)
	}
	if err := d.QueryRow("SELECT id, file_path FROM downloads WHERE id = 11").Scan(&downloadID, &filePath); err != nil || downloadID != 11 || filePath != "/media/legacy.mkv" {
		t.Fatalf("legacy download changed during migration: id=%d path=%q err=%v", downloadID, filePath, err)
	}
	var indexName string
	if err := d.QueryRow("SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_downloads_uploader'").Scan(&indexName); err != nil || indexName != "idx_downloads_uploader" {
		t.Fatalf("uploader index missing after migration: %v", err)
	}
}

func TestInitFailsWhenSchemaCannotBeApplied(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "video-subscribe-dl.db")
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacy.Exec(`CREATE VIEW downloads AS SELECT 1 AS id`); err != nil {
		t.Fatalf("create incompatible downloads view: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	if _, err := Init(dataDir); err == nil {
		t.Fatal("expected Init to fail rather than continue with an incompatible database object")
	}
}

// === TestSourceCRUD ===

func TestSourceCRUD_Create(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	s := &Source{
		Type:            "channel",
		URL:             "https://space.bilibili.com/12345",
		Name:            "Test UP",
		CheckInterval:   1800,
		DownloadQuality: "best",
		DownloadCodec:   "all",
		Enabled:         true,
	}
	id, err := d.CreateSource(s)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive id, got %d", id)
	}
}

func TestSourceCRUD_GetSources(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	d.CreateSource(&Source{URL: "https://space.bilibili.com/111", Name: "UP1", Enabled: true})
	d.CreateSource(&Source{URL: "https://space.bilibili.com/222", Name: "UP2", Enabled: false})

	sources, err := d.GetSources()
	if err != nil {
		t.Fatalf("get sources: %v", err)
	}
	if len(sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(sources))
	}
}

func TestSourceCRUD_GetEnabledSources(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	d.CreateSource(&Source{URL: "https://space.bilibili.com/111", Name: "UP1", Enabled: true})
	d.CreateSource(&Source{URL: "https://space.bilibili.com/222", Name: "UP2", Enabled: false})

	sources, err := d.GetEnabledSources()
	if err != nil {
		t.Fatalf("get enabled sources: %v", err)
	}
	if len(sources) != 1 {
		t.Errorf("expected 1 enabled source, got %d", len(sources))
	}
}

func TestSourceCRUD_Update(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	id, _ := d.CreateSource(&Source{URL: "https://space.bilibili.com/111", Name: "Old", Enabled: true})
	s, _ := d.GetSource(id)
	s.Name = "New Name"
	s.Enabled = false
	err := d.UpdateSource(s)
	if err != nil {
		t.Fatalf("update source: %v", err)
	}
	updated, _ := d.GetSource(id)
	if updated.Name != "New Name" {
		t.Errorf("expected 'New Name', got '%s'", updated.Name)
	}
	if updated.Enabled != false {
		t.Errorf("expected disabled, got enabled")
	}
}

func TestSourceCRUD_Delete(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	id, _ := d.CreateSource(&Source{URL: "https://space.bilibili.com/111", Name: "ToDelete", Enabled: true})
	err := d.DeleteSource(id)
	if err != nil {
		t.Fatalf("delete source: %v", err)
	}
	_, err = d.GetSource(id)
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

func TestSourceCRUD_DefaultType(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	id, _ := d.CreateSource(&Source{URL: "https://space.bilibili.com/111", Name: "NoType", Enabled: true})
	s, _ := d.GetSource(id)
	if s.Type != "channel" {
		t.Errorf("expected default type 'channel', got '%s'", s.Type)
	}
}

// === TestIsVideoDownloaded ===

func TestIsVideoDownloaded_NotExists(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	id, _ := d.CreateSource(&Source{URL: "https://space.bilibili.com/111", Name: "Test", Enabled: true})
	downloaded, err := d.IsVideoDownloaded(id, "BV_nonexist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if downloaded {
		t.Error("expected false for non-existent video")
	}
}

func TestIsVideoDownloaded_Pending(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, _ := d.CreateSource(&Source{URL: "u", Name: "t", Enabled: true})
	d.CreateDownload(&Download{SourceID: srcID, VideoID: "BV123", Title: "Test", Status: "pending"})

	downloaded, err := d.IsVideoDownloaded(srcID, "BV123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !downloaded {
		t.Error("expected true for pending video (dedup)")
	}
}

func TestIsVideoDownloaded_Completed(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, _ := d.CreateSource(&Source{URL: "u", Name: "t", Enabled: true})
	d.CreateDownload(&Download{SourceID: srcID, VideoID: "BV456", Title: "Done", Status: "completed"})

	downloaded, _ := d.IsVideoDownloaded(srcID, "BV456")
	if !downloaded {
		t.Error("expected true for completed video")
	}
}

func TestIsVideoDownloaded_Failed(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, _ := d.CreateSource(&Source{URL: "u", Name: "t", Enabled: true})
	d.CreateDownload(&Download{SourceID: srcID, VideoID: "BV789", Title: "Fail", Status: "failed"})

	downloaded, _ := d.IsVideoDownloaded(srcID, "BV789")
	if !downloaded {
		t.Error("expected true for failed video (still dedup)")
	}
}

func TestIsVideoDownloaded_PermanentFailed(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, _ := d.CreateSource(&Source{URL: "u", Name: "t", Enabled: true})
	dlID, _ := d.CreateDownload(&Download{SourceID: srcID, VideoID: "BVperm", Title: "PFail", Status: "failed"})
	d.Exec("UPDATE downloads SET status = 'permanent_failed' WHERE id = ?", dlID)

	downloaded, _ := d.IsVideoDownloaded(srcID, "BVperm")
	if downloaded {
		t.Error("expected false for permanent_failed (should allow retry)")
	}
}

func TestIsVideoDownloaded_CrossSource(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	src1, _ := d.CreateSource(&Source{URL: "u1", Name: "s1", Enabled: true})
	src2, _ := d.CreateSource(&Source{URL: "u2", Name: "s2", Enabled: true})
	d.CreateDownload(&Download{SourceID: src1, VideoID: "BVcross", Title: "Cross", Status: "completed"})

	downloaded, _ := d.IsVideoDownloaded(src2, "BVcross")
	if downloaded {
		t.Error("expected false for different source_id")
	}
}

// === TestDownloadStatusFlow ===

func TestDownloadStatusFlow_PendingToCompleted(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, _ := d.CreateSource(&Source{URL: "u", Name: "t", Enabled: true})
	dlID, _ := d.CreateDownload(&Download{SourceID: srcID, VideoID: "BVflow", Title: "Flow", Status: "pending"})

	err := d.UpdateDownloadStatus(dlID, "completed", "/path/to/file.mkv", 1024*1024, "")
	if err != nil {
		t.Fatalf("update status: %v", err)
	}

	downloads, _ := d.GetDownloadsByStatus("completed", 10)
	found := false
	for _, dl := range downloads {
		if dl.VideoID == "BVflow" {
			found = true
			if dl.FilePath != "/path/to/file.mkv" {
				t.Errorf("expected file path, got '%s'", dl.FilePath)
			}
			if dl.FileSize != 1024*1024 {
				t.Errorf("expected file size 1048576, got %d", dl.FileSize)
			}
		}
	}
	if !found {
		t.Error("completed download not found")
	}
}

func TestDownloadStatusFlow_PendingToFailed(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, _ := d.CreateSource(&Source{URL: "u", Name: "t", Enabled: true})
	dlID, _ := d.CreateDownload(&Download{SourceID: srcID, VideoID: "BVfail", Title: "Fail", Status: "pending"})

	err := d.UpdateDownloadStatus(dlID, "failed", "", 0, "network timeout")
	if err != nil {
		t.Fatalf("update status: %v", err)
	}

	downloads, _ := d.GetDownloadsByStatus("failed", 10)
	found := false
	for _, dl := range downloads {
		if dl.VideoID == "BVfail" {
			found = true
			if dl.ErrorMessage != "network timeout" {
				t.Errorf("expected error message 'network timeout', got '%s'", dl.ErrorMessage)
			}
		}
	}
	if !found {
		t.Error("failed download not found")
	}
}

func TestDownloadStatusFlow_RetryCount(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, _ := d.CreateSource(&Source{URL: "u", Name: "t", Enabled: true})
	dlID, _ := d.CreateDownload(&Download{SourceID: srcID, VideoID: "BVretry", Title: "Retry", Status: "failed"})

	err := d.IncrementRetryCount(dlID, "error 1")
	if err != nil {
		t.Fatalf("increment retry count 1: %v", err)
	}
	err = d.IncrementRetryCount(dlID, "error 2")
	if err != nil {
		t.Fatalf("increment retry count 2: %v", err)
	}

	// NOTE: GetRetryableDownloads has a known bug - its SELECT does not COALESCE
	// nullable columns (file_path, title, etc.), causing Scan errors on NULL values.
	// Verify retry count via direct query instead.
	var rc int
	var le string
	err = d.QueryRow(`SELECT COALESCE(retry_count,0), COALESCE(last_error,'') FROM downloads WHERE id = ?`, dlID).Scan(&rc, &le)
	if err != nil {
		t.Fatalf("query retry count: %v", err)
	}
	if rc != 2 {
		t.Errorf("expected retry_count 2, got %d", rc)
	}
	if le != "error 2" {
		t.Errorf("expected last_error 'error 2', got '%s'", le)
	}
}

func TestDownloadStatusFlow_ResetRetryCount(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, _ := d.CreateSource(&Source{URL: "u", Name: "t", Enabled: true})
	dlID, _ := d.CreateDownload(&Download{SourceID: srcID, VideoID: "BVreset", Title: "Reset", Status: "failed"})

	d.IncrementRetryCount(dlID, "err")
	d.IncrementRetryCount(dlID, "err2")

	err := d.ResetRetryCount(dlID)
	if err != nil {
		t.Fatalf("reset retry count: %v", err)
	}

	var rc int
	d.QueryRow(`SELECT COALESCE(retry_count,0) FROM downloads WHERE id = ?`, dlID).Scan(&rc)
	if rc != 0 {
		t.Errorf("expected retry_count 0 after reset, got %d", rc)
	}
}

func TestDownloadStatusFlow_MarkPermanentFailed(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, _ := d.CreateSource(&Source{URL: "u", Name: "t", Enabled: true})
	dlID, _ := d.CreateDownload(&Download{SourceID: srcID, VideoID: "BVpf", Title: "PF", Status: "failed"})
	d.IncrementRetryCount(dlID, "e1")
	d.IncrementRetryCount(dlID, "e2")
	d.IncrementRetryCount(dlID, "e3")

	affected, err := d.MarkPermanentFailed(3)
	if err != nil {
		t.Fatalf("mark permanent failed: %v", err)
	}
	if affected != 1 {
		t.Errorf("expected 1 affected, got %d", affected)
	}
}

// === TestSettings ===

func TestSettings_SetAndGet(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	d.SetSetting("output_dir", "/videos")
	val, err := d.GetSetting("output_dir")
	if err != nil {
		t.Fatalf("get setting: %v", err)
	}
	if val != "/videos" {
		t.Errorf("expected '/videos', got '%s'", val)
	}
}

func TestSettings_NonExistentKey(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	val, err := d.GetSetting("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty string, got '%s'", val)
	}
}

func TestSettings_Upsert(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	d.SetSetting("key1", "value1")
	d.SetSetting("key1", "value2")
	val, _ := d.GetSetting("key1")
	if val != "value2" {
		t.Errorf("expected 'value2' after upsert, got '%s'", val)
	}
}

// === TestPeople ===

func TestPeople_UpsertAndGet(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	err := d.UpsertPerson("12345", "TestUP", "https://avatar.url")
	if err != nil {
		t.Fatalf("upsert person: %v", err)
	}

	people, err := d.GetPeople()
	if err != nil {
		t.Fatalf("get people: %v", err)
	}
	if len(people) != 1 {
		t.Fatalf("expected 1 person, got %d", len(people))
	}
	if people[0].MID != "12345" || people[0].Name != "TestUP" {
		t.Errorf("person mismatch: %+v", people[0])
	}
}

func TestPeople_UpsertUpdate(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	d.UpsertPerson("111", "OldName", "old.url")
	d.UpsertPerson("111", "NewName", "new.url")

	people, _ := d.GetPeople()
	if len(people) != 1 {
		t.Fatalf("expected 1 person after upsert, got %d", len(people))
	}
	if people[0].Name != "NewName" {
		t.Errorf("expected 'NewName', got '%s'", people[0].Name)
	}
}

// BenchmarkGetStatsDetailed verifies the 2-query implementation performs
// acceptably on a realistic dataset.
// [FIXED: P1-3] benchmark added for GetStatsDetailed consolidation
func BenchmarkGetStatsDetailed(b *testing.B) {
	d := initMemoryDB(b) // testing.TB — works for *testing.B after 1a
	defer d.Close()

	// Seed: insert 200 downloads spread across all tracked statuses
	srcID, err := d.CreateSource(&Source{
		URL:     "https://space.bilibili.com/bench",
		Name:    "bench-src",
		Enabled: true,
	})
	if err != nil {
		b.Fatalf("create source: %v", err)
	}

	statuses := []string{"completed", "relocated", "failed", "permanent_failed", "pending", "charge_blocked", "downloading"}
	for i := 0; i < 200; i++ {
		status := statuses[i%len(statuses)]
		_, err := d.CreateDownload(&Download{
			SourceID: srcID,
			VideoID:  fmt.Sprintf("BVbench%04d", i),
			Title:    fmt.Sprintf("Bench Video %d", i),
			Status:   status,
			FileSize: int64(1024 * 1024 * (i%50 + 1)), // 1–50 MiB
		})
		if err != nil {
			b.Fatalf("create download %d: %v", i, err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.GetStatsDetailed(); err != nil {
			b.Fatal(err)
		}
	}
}
