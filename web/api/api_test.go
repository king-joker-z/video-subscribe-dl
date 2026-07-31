package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/glebarez/sqlite"
	"video-subscribe-dl/internal/db"
)

// initTestDB creates an in-memory SQLite DB with the full schema for testing.
func initTestDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}

	// Create full schema (same as db.Init but in-memory)
	schema := `
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
	if _, err := sqlDB.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return &db.DB{DB: sqlDB}
}

// setupTestRouter creates a test mux with API routes registered using in-memory DB.
func setupTestRouter(t *testing.T) (*http.ServeMux, *db.DB) {
	t.Helper()
	database := initTestDB(t)
	mux := http.NewServeMux()

	// Create handlers directly (no downloader needed for most tests)
	sourcesH := NewSourcesHandler(database)
	searchH := NewSearchHandler(database)
	diagH := NewDiagHandler(database)

	// Register routes that don't need downloader
	mux.HandleFunc("/api/sources", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			sourcesH.HandleList(w, r)
		case "POST":
			sourcesH.HandleCreate(w, r)
		default:
			apiError(w, CodeMethodNotAllow, "method not allowed")
		}
	})
	mux.HandleFunc("/api/sources/", sourcesH.HandleByID)

	mux.HandleFunc("/api/search", searchH.HandleSearch)

	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		apiOK(w, map[string]string{"status": "pong"})
	})

	mux.HandleFunc("/api/diag/bili", diagH.HandleBili)
	mux.HandleFunc("/api/diag/douyin", diagH.HandleDouyin)

	return mux, database
}

// parseResponse decodes a JSON API response.
func parseResponse(t *testing.T, rec *httptest.ResponseRecorder) Response {
	t.Helper()
	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	return resp
}

// ---------- Tests ----------

// TestPingEndpoint verifies GET /api/ping returns 200 + code 0.
func TestPingEndpoint(t *testing.T) {
	mux, _ := setupTestRouter(t)

	req := httptest.NewRequest("GET", "/api/ping", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := parseResponse(t, rec)
	if resp.Code != 0 {
		t.Errorf("expected code 0, got %d", resp.Code)
	}
}

// TestSourcesCRUD tests the create → list → delete flow for sources.
func TestSourcesCRUD(t *testing.T) {
	mux, _ := setupTestRouter(t)

	// 1. Create a source via POST /api/sources
	body := `{"url": "https://space.bilibili.com/12345", "name": "Test UP", "type": "up", "check_interval": 3600}`
	req := httptest.NewRequest("POST", "/api/sources", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("create: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	resp := parseResponse(t, rec)
	if resp.Code != 0 {
		t.Fatalf("create: expected code 0, got %d; msg: %s", resp.Code, resp.Message)
	}

	// Extract created ID
	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("create: expected data to be a map, got %T", resp.Data)
	}
	createdID := int64(dataMap["id"].(float64))
	if createdID <= 0 {
		t.Fatalf("create: expected positive id, got %d", createdID)
	}

	// 2. List sources via GET /api/sources
	req = httptest.NewRequest("GET", "/api/sources", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rec.Code)
	}
	resp = parseResponse(t, rec)
	if resp.Code != 0 {
		t.Fatalf("list: expected code 0, got %d", resp.Code)
	}
	listData, ok := resp.Data.([]interface{})
	if !ok {
		t.Fatalf("list: expected data to be an array, got %T", resp.Data)
	}
	if len(listData) != 1 {
		t.Fatalf("list: expected 1 source, got %d", len(listData))
	}

	// 3. Delete via DELETE /api/sources/:id
	req = httptest.NewRequest("DELETE", "/api/sources/"+json_itoa(createdID), nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// 4. Verify deletion — list should be empty
	req = httptest.NewRequest("GET", "/api/sources", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	resp = parseResponse(t, rec)
	listData, ok = resp.Data.([]interface{})
	if !ok {
		t.Fatalf("list after delete: expected array, got %T", resp.Data)
	}
	if len(listData) != 0 {
		t.Errorf("list after delete: expected 0 sources, got %d", len(listData))
	}
}

// TestSourceTypeValidation tests that valid types are accepted and creation works.
// Source types: up, douyin, favorite, season, watchlater.
func TestSourceTypeValidation(t *testing.T) {
	mux, _ := setupTestRouter(t)

	validTypes := []string{"up", "douyin", "favorite", "season"}
	for _, st := range validTypes {
		body := `{"url": "https://example.com/test", "name": "Test", "type": "` + st + `"}`
		req := httptest.NewRequest("POST", "/api/sources", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("type=%s: expected 200, got %d", st, rec.Code)
			continue
		}
		resp := parseResponse(t, rec)
		if resp.Code != 0 {
			t.Errorf("type=%s: expected code 0, got %d; msg: %s", st, resp.Code, resp.Message)
		}
	}

	// Test with invalid JSON should return 400
	req := httptest.NewRequest("POST", "/api/sources", bytes.NewBufferString("{invalid json}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid json: expected 400, got %d", rec.Code)
	}

	// Test with empty body should return 400
	req = httptest.NewRequest("POST", "/api/sources", bytes.NewBufferString(""))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: expected 400, got %d", rec.Code)
	}
}

// TestHealthEndpoint tests that GET /health returns a proper structure.
// Note: /health is registered in the web package (server.go), not in web/api.
// So we test the /api/ping endpoint here as the API-layer health check.
func TestHealthEndpoint(t *testing.T) {
	mux, _ := setupTestRouter(t)

	req := httptest.NewRequest("GET", "/api/ping", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Verify structure: code, data, message
	if _, ok := raw["code"]; !ok {
		t.Error("missing 'code' field")
	}
	if _, ok := raw["data"]; !ok {
		t.Error("missing 'data' field")
	}
	if _, ok := raw["message"]; !ok {
		t.Error("missing 'message' field")
	}

	codeVal, _ := raw["code"].(float64)
	if int(codeVal) != 0 {
		t.Errorf("expected code 0, got %v", raw["code"])
	}
	msg, _ := raw["message"].(string)
	if msg != "ok" {
		t.Errorf("expected message 'ok', got %q", msg)
	}
}

// json_itoa is a helper to convert int64 to string.
func json_itoa(n int64) string {
	return string(json.Number(itoa(n)))
}

func itoa(n int64) string {
	buf := make([]byte, 0, 20)
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 20)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	for i := len(digits) - 1; i >= 0; i-- {
		buf = append(buf, digits[i])
	}
	return string(buf)
}

// TestVideoSearchByUploader verifies that search query matches uploader field.
func TestVideoSearchByUploader(t *testing.T) {
	mux, database := setupTestRouter(t)

	// Register videos handler for this test
	videosH := NewVideosHandler(database, "/tmp/test-downloads")
	mux.HandleFunc("/api/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			videosH.HandleList(w, r)
		} else {
			apiError(w, CodeMethodNotAllow, "method not allowed")
		}
	})

	// Insert a source first
	_, err := database.Exec(`INSERT INTO sources (type, url, name) VALUES ('up', 'https://space.bilibili.com/1', 'Test Source')`)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	var srcID int64
	database.QueryRow(`SELECT id FROM sources ORDER BY id DESC LIMIT 1`).Scan(&srcID)

	// Insert two download records with different uploaders
	_, err = database.Exec(`INSERT INTO downloads (source_id, video_id, title, uploader, status) VALUES (?, 'vid001', 'Some Video Title', 'NarumiUploader', 'completed')`, srcID)
	if err != nil {
		t.Fatalf("insert download 1: %v", err)
	}
	_, err = database.Exec(`INSERT INTO downloads (source_id, video_id, title, uploader, status) VALUES (?, 'vid002', 'Another Video', 'OtherUploader', 'completed')`, srcID)
	if err != nil {
		t.Fatalf("insert download 2: %v", err)
	}

	// Search by uploader name — should find only the first record
	req := httptest.NewRequest("GET", "/api/videos?search=NarumiUploader", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	resp := parseResponse(t, rec)
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d; msg: %s", resp.Code, resp.Message)
	}

	// The response data is paginated: { items: [...], total: N }
	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", resp.Data)
	}
	total, _ := dataMap["total"].(float64)
	if int(total) != 1 {
		t.Errorf("expected 1 result when searching by uploader 'NarumiUploader', got %v", total)
	}

	// Search by partial uploader name (case-insensitive LIKE)
	req = httptest.NewRequest("GET", "/api/videos?search=narumi", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	resp = parseResponse(t, rec)
	dataMap, _ = resp.Data.(map[string]interface{})
	total, _ = dataMap["total"].(float64)
	if int(total) != 1 {
		t.Errorf("expected 1 result for partial uploader search 'narumi', got %v", total)
	}

	// Search by title should still work
	req = httptest.NewRequest("GET", "/api/videos?search=Another", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	resp = parseResponse(t, rec)
	dataMap, _ = resp.Data.(map[string]interface{})
	total, _ = dataMap["total"].(float64)
	if int(total) != 1 {
		t.Errorf("expected 1 result for title search 'Another', got %v", total)
	}
}

func TestVideoCancelOnlyCancelsPendingTask(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())

	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status) VALUES (?, 'pending-video', 'pending')`, sourceID)
	if err != nil {
		t.Fatalf("insert pending download: %v", err)
	}
	pendingID, _ := result.LastInsertId()
	result, err = database.Exec(`INSERT INTO downloads (source_id, video_id, status) VALUES (?, 'downloading-video', 'downloading')`, sourceID)
	if err != nil {
		t.Fatalf("insert downloading download: %v", err)
	}
	downloadingID, _ := result.LastInsertId()

	req := httptest.NewRequest(http.MethodPost, "/api/videos/1/cancel", nil)
	rec := httptest.NewRecorder()
	h.HandleCancel(rec, req, pendingID)
	if rec.Code != http.StatusOK {
		t.Fatalf("pending cancel HTTP status = %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := parseResponse(t, rec)
	if resp.Code != CodeOK {
		t.Fatalf("pending cancel code = %d, want %d", resp.Code, CodeOK)
	}
	if !strings.Contains(resp.Message, "ok") {
		t.Fatalf("pending cancel response message = %q", resp.Message)
	}

	var status string
	if err := database.QueryRow("SELECT status FROM downloads WHERE id = ?", pendingID).Scan(&status); err != nil {
		t.Fatalf("query pending status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("pending status = %q, want cancelled", status)
	}

	rec = httptest.NewRecorder()
	h.HandleCancel(rec, req, downloadingID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("downloading cancel HTTP status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	resp = parseResponse(t, rec)
	if resp.Code != CodeTaskBusy {
		t.Fatalf("downloading cancel code = %d, want %d", resp.Code, CodeTaskBusy)
	}
	if !strings.Contains(resp.Message, "排队任务") {
		t.Fatalf("downloading cancel message = %q", resp.Message)
	}
	if err := database.QueryRow("SELECT status FROM downloads WHERE id = ?", downloadingID).Scan(&status); err != nil {
		t.Fatalf("query downloading status: %v", err)
	}
	if status != "downloading" {
		t.Fatalf("downloading status changed to %q", status)
	}
}

func TestVideoBatchCancelOnlyAffectsPendingTasks(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())

	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	insert := func(videoID, status string) int64 {
		t.Helper()
		result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status) VALUES (?, ?, ?)`, sourceID, videoID, status)
		if err != nil {
			t.Fatalf("insert %s: %v", status, err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	pendingID := insert("pending-video", "pending")
	downloadingID := insert("downloading-video", "downloading")
	failedID := insert("failed-video", "failed")

	body := bytes.NewBufferString(fmt.Sprintf(`{"action":"cancel","ids":[%d,%d,%d]}`, pendingID, downloadingID, failedID))
	req := httptest.NewRequest(http.MethodPost, "/api/videos/batch", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleBatch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch cancel HTTP status = %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := parseResponse(t, rec)
	if resp.Code != CodeOK {
		t.Fatalf("batch cancel code = %d, want %d", resp.Code, CodeOK)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("batch cancel response data = %T, want map", resp.Data)
	}
	if affected := int(data["affected"].(float64)); affected != 1 {
		t.Fatalf("batch cancel affected = %d, want 1", affected)
	}

	for _, tc := range []struct {
		id   int64
		want string
	}{
		{pendingID, "cancelled"},
		{downloadingID, "downloading"},
		{failedID, "failed"},
	} {
		var status string
		if err := database.QueryRow("SELECT status FROM downloads WHERE id = ?", tc.id).Scan(&status); err != nil {
			t.Fatalf("query status for %d: %v", tc.id, err)
		}
		if status != tc.want {
			t.Fatalf("status for %d = %q, want %q", tc.id, status, tc.want)
		}
	}
}

func TestVideoRetryOnlyAllowsFailedStates(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())

	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	insert := func(videoID, status string) int64 {
		t.Helper()
		result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status, retry_count, last_error) VALUES (?, ?, ?, 2, 'previous error')`, sourceID, videoID, status)
		if err != nil {
			t.Fatalf("insert %s: %v", status, err)
		}
		id, _ := result.LastInsertId()
		return id
	}

	for _, tc := range []struct {
		status string
		allow  bool
	}{
		{"failed", true},
		{"permanent_failed", true},
		{"downloading", false},
		{"pending", false},
		{"completed", false},
		{"cancelled", false},
		{"deleted", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			id := insert("retry-"+tc.status, tc.status)
			req := httptest.NewRequest(http.MethodPost, "/api/videos/1/retry", nil)
			rec := httptest.NewRecorder()
			h.HandleRetry(rec, req, id)
			resp := parseResponse(t, rec)
			if tc.allow {
				if rec.Code != http.StatusOK || resp.Code != CodeOK {
					t.Fatalf("retry %s = HTTP %d code %d, body=%s", tc.status, rec.Code, resp.Code, rec.Body.String())
				}
				var status string
				var retryCount int
				if err := database.QueryRow(`SELECT status, retry_count FROM downloads WHERE id=?`, id).Scan(&status, &retryCount); err != nil {
					t.Fatalf("query retried download: %v", err)
				}
				if status != "pending" || retryCount != 0 {
					t.Fatalf("retry %s produced status=%q retry_count=%d, want pending/0", tc.status, status, retryCount)
				}
				return
			}
			if rec.Code != http.StatusBadRequest || resp.Code != CodeTaskBusy {
				t.Fatalf("retry %s = HTTP %d code %d, want 400/%d; body=%s", tc.status, rec.Code, resp.Code, CodeTaskBusy, rec.Body.String())
			}
			var status string
			var retryCount int
			if err := database.QueryRow(`SELECT status, retry_count FROM downloads WHERE id=?`, id).Scan(&status, &retryCount); err != nil {
				t.Fatalf("query rejected download: %v", err)
			}
			if status != tc.status || retryCount != 2 {
				t.Fatalf("rejected %s changed to status=%q retry_count=%d", tc.status, status, retryCount)
			}
		})
	}
}

func TestVideoBatchRetryOnlyCountsAllowedStates(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())

	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	insert := func(videoID, status string) int64 {
		t.Helper()
		result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status, retry_count) VALUES (?, ?, ?, 2)`, sourceID, videoID, status)
		if err != nil {
			t.Fatalf("insert %s: %v", status, err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	failedID := insert("failed", "failed")
	permanentID := insert("permanent", "permanent_failed")
	downloadingID := insert("downloading", "downloading")
	completedID := insert("completed", "completed")

	body := bytes.NewBufferString(fmt.Sprintf(`{"action":"retry","ids":[%d,%d,%d,%d]}`, failedID, permanentID, downloadingID, completedID))
	req := httptest.NewRequest(http.MethodPost, "/api/videos/batch", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleBatch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch retry HTTP status = %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := parseResponse(t, rec)
	data := resp.Data.(map[string]interface{})
	if got := int(data["affected"].(float64)); got != 2 {
		t.Fatalf("batch retry affected = %d, want 2", got)
	}
	for _, tc := range []struct {
		id   int64
		want string
	}{
		{failedID, "pending"},
		{permanentID, "pending"},
		{downloadingID, "downloading"},
		{completedID, "completed"},
	} {
		var status string
		if err := database.QueryRow(`SELECT status FROM downloads WHERE id=?`, tc.id).Scan(&status); err != nil {
			t.Fatalf("query status: %v", err)
		}
		if status != tc.want {
			t.Fatalf("status for %d = %q, want %q", tc.id, status, tc.want)
		}
	}
}

func TestVideoRetryCallbackContract(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())

	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	insert := func(videoID string) int64 {
		t.Helper()
		result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status, retry_count) VALUES (?, ?, 'completed', 2)`, sourceID, videoID)
		if err != nil {
			t.Fatalf("insert download: %v", err)
		}
		id, _ := result.LastInsertId()
		return id
	}

	falseID := insert("callback-false")
	var calls int
	h.SetRetryDownloadFunc(func(id int64) bool {
		calls++
		return false
	})
	req := httptest.NewRequest(http.MethodPost, "/api/videos/1/retry", nil)
	rec := httptest.NewRecorder()
	h.HandleRetry(rec, req, falseID)
	resp := parseResponse(t, rec)
	if rec.Code != http.StatusBadRequest || resp.Code != CodeTaskBusy || calls != 1 {
		t.Fatalf("false callback = HTTP %d code %d calls %d, want 400/%d/1", rec.Code, resp.Code, calls, CodeTaskBusy)
	}
	var status string
	if err := database.QueryRow(`SELECT status FROM downloads WHERE id=?`, falseID).Scan(&status); err != nil {
		t.Fatalf("query false callback record: %v", err)
	}
	if status != "completed" {
		t.Fatalf("false callback fell through to DB fallback: status=%q", status)
	}

	trueID := insert("callback-true")
	h.SetRetryDownloadFunc(func(id int64) bool { return id == trueID })
	rec = httptest.NewRecorder()
	h.HandleRetry(rec, req, trueID)
	resp = parseResponse(t, rec)
	if rec.Code != http.StatusOK || resp.Code != CodeOK {
		t.Fatalf("true callback = HTTP %d code %d, want 200/0", rec.Code, resp.Code)
	}
	if err := database.QueryRow(`SELECT status FROM downloads WHERE id=?`, trueID).Scan(&status); err != nil {
		t.Fatalf("query true callback record: %v", err)
	}
	if status != "completed" {
		t.Fatalf("true callback unexpectedly changed DB status to %q", status)
	}
}

func TestVideoBatchRetryCallbackCountsOnlyTrue(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())

	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	insert := func(videoID string) int64 {
		t.Helper()
		result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status) VALUES (?, ?, 'completed')`, sourceID, videoID)
		if err != nil {
			t.Fatalf("insert download: %v", err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	trueID := insert("callback-true")
	falseID := insert("callback-false")
	h.SetRetryDownloadFunc(func(id int64) bool { return id == trueID })

	body := bytes.NewBufferString(fmt.Sprintf(`{"action":"retry","ids":[%d,%d]}`, trueID, falseID))
	req := httptest.NewRequest(http.MethodPost, "/api/videos/batch", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleBatch(rec, req)
	resp := parseResponse(t, rec)
	if rec.Code != http.StatusOK || resp.Code != CodeOK {
		t.Fatalf("batch callback retry = HTTP %d code %d", rec.Code, resp.Code)
	}
	data := resp.Data.(map[string]interface{})
	if got := int(data["affected"].(float64)); got != 1 {
		t.Fatalf("batch callback affected = %d, want 1", got)
	}
	for _, id := range []int64{trueID, falseID} {
		var status string
		if err := database.QueryRow(`SELECT status FROM downloads WHERE id=?`, id).Scan(&status); err != nil {
			t.Fatalf("query callback record: %v", err)
		}
		if status != "completed" {
			t.Fatalf("callback batch fell through to DB fallback: id=%d status=%q", id, status)
		}
	}
}

func TestVideoRedownloadOnlyAllowsCompletedOrRelocated(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())

	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	for _, tc := range []struct {
		status string
		allow  bool
	}{
		{"completed", true}, {"relocated", true}, {"pending", false}, {"downloading", false}, {"failed", false}, {"permanent_failed", false}, {"cancelled", false}, {"deleted", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status, file_path, file_size, retry_count) VALUES (?, ?, ?, '/tmp/video.mkv', 123, 2)`, sourceID, "redownload-"+tc.status, tc.status)
			if err != nil {
				t.Fatalf("insert download: %v", err)
			}
			id, _ := result.LastInsertId()
			var deletes atomic.Int32
			dispatched := make(chan struct{}, 1)
			h.removeVideoDir = func(string) { deletes.Add(1) }
			h.SetRedownloadFunc(func(int64) { dispatched <- struct{}{} })
			req := httptest.NewRequest(http.MethodPost, "/api/videos/1/redownload", nil)
			rec := httptest.NewRecorder()
			h.HandleRedownload(rec, req, id)
			resp := parseResponse(t, rec)
			if tc.allow {
				select {
				case <-dispatched:
				case <-time.After(time.Second):
					t.Fatalf("allowed %s did not dispatch", tc.status)
				}
				if rec.Code != http.StatusOK || resp.Code != CodeOK || deletes.Load() != 1 {
					t.Fatalf("allowed %s: HTTP=%d code=%d deletes=%d", tc.status, rec.Code, resp.Code, deletes.Load())
				}
			} else if rec.Code != http.StatusBadRequest || resp.Code != CodeTaskBusy || deletes.Load() != 0 {
				t.Fatalf("rejected %s: HTTP=%d code=%d deletes=%d", tc.status, rec.Code, resp.Code, deletes.Load())
			}
		})
	}
}

func TestVideoRedownloadConcurrentOnlyOneDeletesAndDispatches(t *testing.T) {
	database := initTestDB(t)
	database.SetMaxOpenConns(1)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())
	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status, file_path) VALUES (?, 'redownload-race', 'completed', '/tmp/video.mkv')`, sourceID)
	if err != nil {
		t.Fatalf("insert download: %v", err)
	}
	id, _ := result.LastInsertId()
	var deletes atomic.Int32
	dispatched := make(chan struct{}, 2)
	h.removeVideoDir = func(string) { deletes.Add(1) }
	h.SetRedownloadFunc(func(int64) { dispatched <- struct{}{} })

	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			h.HandleRedownload(rec, httptest.NewRequest(http.MethodPost, "/api/videos/1/redownload", nil), id)
			responses <- rec
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	var success, conflict int
	for rec := range responses {
		switch rec.Code {
		case http.StatusOK:
			success++
		case http.StatusBadRequest:
			conflict++
		default:
			t.Fatalf("concurrent redownload unexpected HTTP status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if success != 1 || conflict != 1 || deletes.Load() != 1 {
		t.Fatalf("concurrent redownload success=%d conflict=%d deletes=%d, want 1/1/1", success, conflict, deletes.Load())
	}
	var status, filePath, thumbPath string
	var fileSize int64
	var retryCount int
	if err := database.QueryRow(`SELECT status, file_path, file_size, thumb_path, retry_count FROM downloads WHERE id=?`, id).Scan(&status, &filePath, &fileSize, &thumbPath, &retryCount); err != nil {
		t.Fatalf("query concurrent redownload state: %v", err)
	}
	if status != "pending" || filePath != "" || fileSize != 0 || thumbPath != "" || retryCount != 0 {
		t.Fatalf("concurrent redownload final state: status=%q file_path=%q file_size=%d thumb_path=%q retry_count=%d, want pending/empty/0/empty/0", status, filePath, fileSize, thumbPath, retryCount)
	}
	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("concurrent redownload did not dispatch")
	}
	select {
	case <-dispatched:
		t.Fatal("concurrent redownload dispatched more than once")
	default:
	}
}

func TestVideoBatchRedownloadOnlyCountsCompletedAndRelocated(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())
	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	insert := func(videoID, status string) int64 {
		t.Helper()
		result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status, file_path) VALUES (?, ?, ?, '/tmp/video.mkv')`, sourceID, videoID, status)
		if err != nil {
			t.Fatalf("insert %s: %v", status, err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	completedID := insert("completed", "completed")
	relocatedID := insert("relocated", "relocated")
	failedID := insert("failed", "failed")
	cancelledID := insert("cancelled", "cancelled")
	dispatched := make(chan struct{}, 2)
	h.removeVideoDir = func(string) {}
	h.SetRedownloadFunc(func(int64) { dispatched <- struct{}{} })
	body := bytes.NewBufferString(fmt.Sprintf(`{"action":"redownload","ids":[%d,%d,%d,%d]}`, completedID, relocatedID, failedID, cancelledID))
	req := httptest.NewRequest(http.MethodPost, "/api/videos/batch", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleBatch(rec, req)
	resp := parseResponse(t, rec)
	data := resp.Data.(map[string]interface{})
	if rec.Code != http.StatusOK || resp.Code != CodeOK || int(data["affected"].(float64)) != 2 {
		t.Fatalf("batch redownload HTTP=%d code=%d affected=%v", rec.Code, resp.Code, data["affected"])
	}
	for range 2 {
		select {
		case <-dispatched:
		case <-time.After(time.Second):
			t.Fatal("batch redownload did not dispatch every admitted item")
		}
	}
	select {
	case <-dispatched:
		t.Fatal("batch redownload dispatched more than admitted items")
	default:
	}
	for _, tc := range []struct {
		id   int64
		want string
	}{{completedID, "pending"}, {relocatedID, "pending"}, {failedID, "failed"}, {cancelledID, "cancelled"}} {
		var status string
		if err := database.QueryRow(`SELECT status FROM downloads WHERE id=?`, tc.id).Scan(&status); err != nil {
			t.Fatalf("query status: %v", err)
		}
		if status != tc.want {
			t.Fatalf("status for %d = %q, want %q", tc.id, status, tc.want)
		}
	}
}

func TestVideoRestoreOnlyAllowsDeleted(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())
	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	for _, tc := range []struct {
		status string
		allow  bool
	}{
		{"deleted", true}, {"pending", false}, {"downloading", false}, {"completed", false}, {"failed", false}, {"cancelled", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status, retry_count, last_error, error_message) VALUES (?, ?, ?, 2, 'old', 'old')`, sourceID, "restore-"+tc.status, tc.status)
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
			id, _ := result.LastInsertId()
			dispatched := make(chan struct{}, 1)
			h.SetRedownloadFunc(func(int64) { dispatched <- struct{}{} })
			rec := httptest.NewRecorder()
			h.HandleRestore(rec, httptest.NewRequest(http.MethodPost, "/api/videos/1/restore", nil), id)
			resp := parseResponse(t, rec)
			if tc.allow {
				if rec.Code != http.StatusOK || resp.Code != CodeOK {
					t.Fatalf("restore %s: HTTP=%d code=%d", tc.status, rec.Code, resp.Code)
				}
				select {
				case <-dispatched:
				case <-time.After(time.Second):
					t.Fatal("deleted restore did not dispatch")
				}
			} else if rec.Code != http.StatusBadRequest || resp.Code != CodeTaskBusy {
				t.Fatalf("restore %s: HTTP=%d code=%d, want 400/%d", tc.status, rec.Code, resp.Code, CodeTaskBusy)
			}
		})
	}
}

func TestVideoRestoreConcurrentOnlyOneAdmissionAndDispatch(t *testing.T) {
	database := initTestDB(t)
	database.SetMaxOpenConns(1)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())
	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status, retry_count, last_error, error_message) VALUES (?, 'restore-race', 'deleted', 2, 'old', 'old')`, sourceID)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := result.LastInsertId()
	dispatched := make(chan struct{}, 2)
	h.SetRedownloadFunc(func(int64) { dispatched <- struct{}{} })

	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			h.HandleRestore(rec, httptest.NewRequest(http.MethodPost, "/api/videos/1/restore", nil), id)
			responses <- rec
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	var success, conflict int
	for rec := range responses {
		switch rec.Code {
		case http.StatusOK:
			success++
		case http.StatusBadRequest:
			conflict++
		default:
			t.Fatalf("restore unexpected HTTP status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("restore success=%d conflict=%d, want 1/1", success, conflict)
	}
	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("restore did not dispatch")
	}
	select {
	case <-dispatched:
		t.Fatal("restore dispatched more than once")
	default:
	}
	var status, lastError, errorMessage string
	var retryCount int
	if err := database.QueryRow(`SELECT status, retry_count, last_error, error_message FROM downloads WHERE id=?`, id).Scan(&status, &retryCount, &lastError, &errorMessage); err != nil {
		t.Fatalf("query restored state: %v", err)
	}
	if status != "pending" || retryCount != 0 || lastError != "" || errorMessage != "" {
		t.Fatalf("restore final state: status=%q retry=%d last=%q error=%q", status, retryCount, lastError, errorMessage)
	}
}

func TestVideoBatchRestoreOnlyCountsDeleted(t *testing.T) {
	database := initTestDB(t)
	defer database.Close()
	h := NewVideosHandler(database, t.TempDir())
	sourceID, err := database.CreateSource(&db.Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	insert := func(videoID, status string) int64 {
		t.Helper()
		result, err := database.Exec(`INSERT INTO downloads (source_id, video_id, status, retry_count, last_error, error_message) VALUES (?, ?, ?, 2, 'old', 'old')`, sourceID, videoID, status)
		if err != nil {
			t.Fatalf("insert %s: %v", status, err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	deletedID := insert("deleted", "deleted")
	completedID := insert("completed", "completed")
	failedID := insert("failed", "failed")
	dispatched := make(chan struct{}, 1)
	h.SetRedownloadFunc(func(int64) { dispatched <- struct{}{} })
	body := bytes.NewBufferString(fmt.Sprintf(`{"action":"restore","ids":[%d,%d,%d]}`, deletedID, completedID, failedID))
	req := httptest.NewRequest(http.MethodPost, "/api/videos/batch", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleBatch(rec, req)
	resp := parseResponse(t, rec)
	data := resp.Data.(map[string]interface{})
	if rec.Code != http.StatusOK || resp.Code != CodeOK || int(data["affected"].(float64)) != 1 {
		t.Fatalf("batch restore HTTP=%d code=%d affected=%v", rec.Code, resp.Code, data["affected"])
	}
	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("batch restore did not dispatch deleted item")
	}
	for _, tc := range []struct {
		id   int64
		want string
	}{{deletedID, "pending"}, {completedID, "completed"}, {failedID, "failed"}} {
		var status string
		if err := database.QueryRow(`SELECT status FROM downloads WHERE id=?`, tc.id).Scan(&status); err != nil {
			t.Fatalf("query status: %v", err)
		}
		if status != tc.want {
			t.Fatalf("status for %d=%q, want %q", tc.id, status, tc.want)
		}
	}
}
