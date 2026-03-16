package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
