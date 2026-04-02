package web

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/glebarez/sqlite" // pure-Go SQLite driver (matches api_test.go)
	"video-subscribe-dl/internal/db"
)

// initTestDB creates an in-memory SQLite database for server-level tests.
// Only the `settings` table is created (the minimum needed for ensureAuthToken).
// NOTE: this is intentionally separate from web/api/api_test.go's initTestDB
// (different package, different schema subset).
func initTestDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	return &db.DB{DB: sqlDB}
}

// TestEnsureAuthToken_FirstRun verifies that on a fresh DB (no auth_token, NO_AUTH not set),
// a random 32-char hex token is generated and persisted.
func TestEnsureAuthToken_FirstRun(t *testing.T) {
	database := initTestDB(t)
	s := &Server{db: database, nonceStore: make(map[string]time.Time)}
	s.ensureAuthToken()
	token, err := database.GetSetting("auth_token")
	if err != nil || token == "" {
		t.Fatalf("expected auth_token in DB, got %q err=%v", token, err)
	}
	if len(token) != 32 { // hex(16 bytes) = 32 chars
		t.Fatalf("expected 32-char token, got len=%d", len(token))
	}
}

// TestEnsureAuthToken_Idempotent verifies that a second call to ensureAuthToken
// does not overwrite an existing token.
func TestEnsureAuthToken_Idempotent(t *testing.T) {
	database := initTestDB(t)
	_ = database.SetSetting("auth_token", "existingtoken12345678901234567890")
	s := &Server{db: database, nonceStore: make(map[string]time.Time)}
	s.ensureAuthToken()
	token, _ := database.GetSetting("auth_token")
	if token != "existingtoken12345678901234567890" {
		t.Fatalf("expected token unchanged, got %q", token)
	}
}

// TestEnsureAuthToken_NoAuthBypass verifies that NO_AUTH=1 prevents token generation.
func TestEnsureAuthToken_NoAuthBypass(t *testing.T) {
	t.Setenv("NO_AUTH", "1")
	database := initTestDB(t)
	s := &Server{db: database, nonceStore: make(map[string]time.Time)}
	s.ensureAuthToken()
	token, _ := database.GetSetting("auth_token")
	if token != "" {
		t.Fatalf("expected no token when NO_AUTH=1, got %q", token)
	}
}

// TestValidateNonce_Valid verifies that a non-expired nonce is accepted and deleted.
func TestValidateNonce_Valid(t *testing.T) {
	s := &Server{nonceStore: make(map[string]time.Time)}
	s.nonceStore["abc123"] = time.Now().Add(60 * time.Second)
	if !s.validateAndConsumeNonce("abc123") {
		t.Fatal("expected nonce to be valid")
	}
	if _, exists := s.nonceStore["abc123"]; exists {
		t.Fatal("nonce should be deleted after use")
	}
}

// TestValidateNonce_Expired verifies that an expired nonce is rejected.
func TestValidateNonce_Expired(t *testing.T) {
	s := &Server{nonceStore: make(map[string]time.Time)}
	s.nonceStore["expired"] = time.Now().Add(-1 * time.Second) // past
	if s.validateAndConsumeNonce("expired") {
		t.Fatal("expected expired nonce to fail")
	}
}

// TestValidateNonce_SingleUse verifies that a nonce can only be used once.
func TestValidateNonce_SingleUse(t *testing.T) {
	s := &Server{nonceStore: make(map[string]time.Time)}
	s.nonceStore["onceonly"] = time.Now().Add(60 * time.Second)
	if !s.validateAndConsumeNonce("onceonly") {
		t.Fatal("first use should succeed")
	}
	if s.validateAndConsumeNonce("onceonly") {
		t.Fatal("second use should fail (single-use)")
	}
}

// TestHandleSessionCreate_RequiresPost verifies that GET /api/session returns 405.
func TestHandleSessionCreate_RequiresPost(t *testing.T) {
	s := &Server{nonceStore: make(map[string]time.Time)}
	req := httptest.NewRequest("GET", "/api/session", nil)
	w := httptest.NewRecorder()
	s.handleSessionCreate(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestHandleSessionCreate_ReturnsNonce verifies that POST /api/session returns a 32-char hex nonce.
func TestHandleSessionCreate_ReturnsNonce(t *testing.T) {
	s := &Server{nonceStore: make(map[string]time.Time)}
	req := httptest.NewRequest("POST", "/api/session", nil)
	w := httptest.NewRecorder()
	s.handleSessionCreate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	nonce := resp["nonce"]
	if len(nonce) != 32 {
		t.Fatalf("expected 32-char nonce, got %q", nonce)
	}
}
