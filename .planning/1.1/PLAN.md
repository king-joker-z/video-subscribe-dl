# Phase 1.1 — Auth Hardening: Execution Plan

**Date:** 2026-04-02
**Phase:** 1.1 — Auth Hardening
**Requirements:** REQ-SEC-1, REQ-SEC-2
**Constraint:** Build and test via GitHub CI only. No local `go build` / `go test`.

---

## Goal

Every VSD deployment starts with authentication enabled by default. The long-lived
auth token is **never** exposed in WebSocket URLs or server logs. Short-lived session
nonces replace the dead `?token=xxx` query-param path.

---

## Current State (from code inspection)

```
HTTP request
  → rateLimitMiddleware(s.mux)      ← only middleware applied (server.go:378)
      → s.mux.ServeHTTP(r)          ← routes hit directly, NO auth check

s.ensureAuthToken()     ← COMMENTED OUT at server.go:373
s.authMiddleware        ← DEFINED at server.go:409 but NEVER wired
api.AuthMiddleware      ← DEFINED at api/middleware.go:109 but NEVER wired
?token= check           ← DEAD CODE in both middleware files (frontend never sends it)
createLogSocket()       ← sync function, does NOT call /api/session, no nonce
```

---

## Target State

```
HTTP request
  → rateLimitMiddleware
      → authMiddleware              ← wired in
          → s.mux.ServeHTTP(r)

s.ensureAuthToken()     ← UNCOMMENTED in Start()
POST /api/session       ← new route: issues 60-second single-use nonce
GET  /api/ws/logs       ← validates ?session=<nonce> before WebSocket upgrade
?token= check           ← REMOVED from both middleware files
createLogSocket()       ← async, fetches nonce from POST /api/session first
```

---

## Task Sequence

### T1 — Re-enable `ensureAuthToken()` and wire `authMiddleware`

**File:** `web/server.go`
**REQ:** REQ-SEC-1
**Risk:** Low — `ensureAuthToken()` already handles `NO_AUTH=1` and `AUTH_TOKEN` env vars.

#### Changes

**Change 1a** — Uncomment `ensureAuthToken()` in `Start()` (line 373):

```go
// BEFORE:
// s.ensureAuthToken() // auth disabled

// AFTER:
s.ensureAuthToken()
```

**Change 1b** — Wire `authMiddleware` into handler chain in `Start()` (line 378):

```go
// BEFORE:
Handler: s.rateLimitMiddleware(s.mux),

// AFTER:
Handler: s.rateLimitMiddleware(s.authMiddleware(s.mux)),
```

#### Verification

```bash
grep -n 's\.ensureAuthToken()' web/server.go
# Expected: one match, NOT commented out

grep -n 'authMiddleware' web/server.go
# Expected: Handler line includes authMiddleware(s.mux)
```

**Done when:** `Start()` body contains the two uncommented lines above.

---

### T2 — Add nonce store and session handler to `Server`

**File:** `web/server.go`
**REQ:** REQ-SEC-2

#### Changes

**Change 2a** — Add fields to `Server` struct (after `rateLimitMu sync.RWMutex`, before closing `}`):

```go
// Session nonce store for WebSocket auth (short-lived, single-use)
nonceMu    sync.Mutex
nonceStore map[string]time.Time // nonce -> expiry
```

**Change 2b** — Initialize `nonceStore` in `NewServer()` (after `s.mux = http.NewServeMux()`):

```go
s.nonceStore = make(map[string]time.Time)
```

**Change 2c** — Add `handleSessionCreate` function (after `handleHealth`, before `SetVersion`):

```go
// POST /api/session
// Issues a short-lived session nonce for WebSocket auth.
// Nonce is single-use and expires after 60 seconds.
// authMiddleware must pass first (nonce only issued to authenticated sessions).
func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    b := make([]byte, 16)
    if _, err := rand.Read(b); err != nil {
        jsonError(w, "nonce generation failed", http.StatusInternalServerError)
        return
    }
    nonce := hex.EncodeToString(b)
    s.nonceMu.Lock()
    s.nonceStore[nonce] = time.Now().Add(60 * time.Second)
    s.nonceMu.Unlock()
    jsonResponse(w, map[string]string{"nonce": nonce})
}
```

**Change 2d** — Add `validateAndConsumeNonce` helper (directly after `handleSessionCreate`):

```go
// validateAndConsumeNonce checks and single-use-consumes a session nonce.
// Returns true if the nonce exists and has not expired; deletes it in both cases.
func (s *Server) validateAndConsumeNonce(nonce string) bool {
    s.nonceMu.Lock()
    defer s.nonceMu.Unlock()
    exp, ok := s.nonceStore[nonce]
    if !ok {
        return false
    }
    delete(s.nonceStore, nonce) // single-use regardless of expiry
    return time.Now().Before(exp)
}
```

**Change 2e** — Add background nonce cleanup goroutine in `NewServer()` (after `nonceStore` init):

```go
// Periodically clean up expired nonces (same pattern as rateLimiter cleanup).
go func() {
    ticker := time.NewTicker(time.Minute)
    defer ticker.Stop()
    for range ticker.C {
        now := time.Now()
        s.nonceMu.Lock()
        for nonce, exp := range s.nonceStore {
            if now.After(exp) {
                delete(s.nonceStore, nonce)
            }
        }
        s.nonceMu.Unlock()
    }
}()
```

#### Verification

```bash
grep -n 'nonceStore\|nonceMu\|handleSessionCreate\|validateAndConsumeNonce' web/server.go
# Expected: 4 symbol names present across struct, NewServer, and new functions
```

**Done when:** All four symbols found; code compiles on CI.

---

### T3 — Register `POST /api/session` route

**File:** `web/router.go`
**REQ:** REQ-SEC-2

`/api/session` is a `Server`-level handler (needs access to `nonceStore`), not an
`api.Router` handler. Register it directly on `s.mux`.

#### Change

In `registerRoutes()`, after `s.apiRouter.Register(s.mux)` (line 14):

```go
// Session nonce endpoint (WebSocket auth, requires prior auth via cookie/header)
s.mux.HandleFunc("/api/session", s.handleSessionCreate)
```

`/api/session` is **not** added to `isAuthWhitelist` — callers must already be
authenticated to receive a nonce.

#### Verification

```bash
grep -n '/api/session' web/router.go
# Expected: one match, s.mux.HandleFunc line
```

**Done when:** Route appears in `registerRoutes()`.

---

### T4 — Remove `?token=` from both auth middleware implementations

**Files:** `web/server.go`, `web/api/middleware.go`
**REQ:** REQ-SEC-2
**Why:** `?token=` exposes the long-lived secret in URLs and access logs. The frontend
never sent it; this is dead code. Removing it prevents accidental future use.

#### Change 4a — `web/server.go` `authMiddleware` (lines 440–443)

Remove these 4 lines from `authMiddleware`:

```go
// 3. Query param ?token=xxx（WebSocket 连接用）
if qToken := r.URL.Query().Get("token"); qToken == token {
    next.ServeHTTP(w, r)
    return
}
```

The middleware keeps the `Authorization: Bearer` header check and the `auth_token`
cookie check.

#### Change 4b — `web/api/middleware.go` `AuthMiddleware` (lines 143–147)

Remove the same block:

```go
// 从 query param 获取（WebSocket 连接用）
if qToken := r.URL.Query().Get("token"); qToken == token {
    next.ServeHTTP(w, r)
    return
}
```

#### Verification

```bash
grep -n 'Query().Get("token")' web/server.go web/api/middleware.go
# Expected: zero matches
```

**Done when:** No `?token=` query-param check remains in either file.

---

### T5 — Add nonce validation to `HandleWSLogs`

**File:** `web/api/events.go`
**REQ:** REQ-SEC-2

The WebSocket endpoint must validate the session nonce **before** performing the
WebSocket upgrade handshake. Cookie auth still works for browser sessions that
have an `auth_token` cookie — those bypass `validateNonce` because `authMiddleware`
will already have passed them through. The nonce is an additional layer for
non-cookie (programmatic) WS connections.

#### Change 5a — Add `validateNonce` field to `EventsHandler` struct

After existing fields in `EventsHandler`:

```go
// validateNonce, if non-nil, gates WebSocket upgrades with a short-lived nonce.
// The nonce is passed as ?session=<nonce> in the WS URL.
// When nil, nonce checking is skipped (auth disabled mode).
validateNonce func(string) bool
```

#### Change 5b — Add setter method

After `NewEventsHandler`:

```go
// SetValidateNonceFunc wires the session-nonce validator into the WebSocket handler.
// Call this from Server.setupRoutes() after creating the api.Router.
func (h *EventsHandler) SetValidateNonceFunc(fn func(string) bool) {
    h.validateNonce = fn
}
```

#### Change 5c — Add nonce check inside `HandleWSLogs`

After the `r.Method != "GET"` guard and the `r.Header.Get("Upgrade")` check, but
**before** `hijackConnection`:

```go
// Validate session nonce if a validator is configured.
// If the client is authenticated via cookie (authMiddleware already passed),
// we still require a nonce for defense-in-depth.
// When validateNonce is nil (auth disabled), skip the check.
if h.validateNonce != nil {
    nonce := r.URL.Query().Get("session")
    if nonce == "" || !h.validateNonce(nonce) {
        http.Error(w, "invalid or expired session nonce", http.StatusUnauthorized)
        return
    }
}
```

Insert at approximately line 323, after:

```go
if r.Header.Get("Upgrade") != "websocket" {
    apiError(w, CodeBadRequest, "需要 WebSocket 升级")
    return
}
```

#### Verification

```bash
grep -n 'validateNonce\|session nonce\|SetValidateNonceFunc' web/api/events.go
# Expected: all three symbols present
```

**Done when:** `HandleWSLogs` checks `?session=` nonce before hijacking the connection.

---

### T6 — Wire nonce validator from `Server` through `Router` into `EventsHandler`

**Files:** `web/api/router.go`, `web/server.go`
**REQ:** REQ-SEC-2

The nonce validator is a closure over `Server.validateAndConsumeNonce`. It needs to
flow: `Server.setupRoutes()` → `api.Router` → `EventsHandler`.

#### Change 6a — Add field and setter to `api.Router` (`web/api/router.go`)

Add field to `Router` struct (after `onSyncAll func()`):

```go
validateNonceFunc func(string) bool
```

Add setter (after `SetRepairThumbFunc`):

```go
// SetValidateNonceFunc wires the session-nonce validator into the WebSocket log handler.
func (rt *Router) SetValidateNonceFunc(fn func(string) bool) {
    rt.validateNonceFunc = fn
    if rt.events != nil {
        rt.events.SetValidateNonceFunc(fn)
    }
}
```

#### Change 6b — Wire in `Server.setupRoutes()` (`web/server.go`)

In `setupRoutes()`, inside the `if s.apiRouter != nil { ... }` block, after all
existing callback setters (around line 226, before the closing `}`):

```go
// Wire session-nonce validator for WebSocket log auth
s.apiRouter.SetValidateNonceFunc(s.validateAndConsumeNonce)
```

Note: `s.validateAndConsumeNonce` is a method expression — pass it as a method value:
`s.validateAndConsumeNonce` (this is valid Go: `func(string) bool`).

#### Verification

```bash
grep -n 'SetValidateNonceFunc\|validateNonceFunc' web/api/router.go web/server.go
# Expected: setter in router.go, wiring call in server.go setupRoutes()
```

**Done when:** Nonce validator flows from `Server` → `Router` → `EventsHandler`.

---

### T7 — Update frontend `createLogSocket` to fetch and use session nonce

**Files:** `web/static/js/api.js`, `web/static/js/pages/logs.js`
**REQ:** REQ-SEC-2

#### Change 7a — Replace `createLogSocket` in `api.js` (lines 196–224)

Replace the entire `createLogSocket` function:

```js
// WebSocket 日志连接（带 SSE 降级）
// Fetches a short-lived session nonce from POST /api/session before connecting.
// Falls back gracefully if the session endpoint fails (e.g. auth is disabled).
export async function createLogSocket(onLog, onConnected) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';

  // Step 1: obtain a short-lived session nonce for WebSocket auth.
  let nonce = '';
  try {
    const res = await fetch('/api/session', { method: 'POST', credentials: 'include' });
    if (res.ok) {
      const data = await res.json();
      nonce = data.nonce || '';
    }
    // Non-ok response (e.g. 401 before login) → proceed without nonce;
    // the WS upgrade will fail and the caller falls back to SSE.
  } catch (e) {
    // Network failure → proceed without nonce
  }

  const wsUrl = nonce
    ? `${proto}//${location.host}/api/ws/logs?session=${encodeURIComponent(nonce)}`
    : `${proto}//${location.host}/api/ws/logs`;

  let ws;
  try {
    ws = new WebSocket(wsUrl);
    ws.onopen = () => { if (onConnected) onConnected('websocket'); };
    ws.onmessage = (e) => {
      try {
        const entry = JSON.parse(e.data);
        if (onLog) onLog(entry);
      } catch(err) {}
    };
    ws.onerror = () => {
      // WebSocket 失败，降级到 SSE
      console.log('WebSocket failed, falling back to SSE');
      ws.close();
    };
    ws.onclose = () => {};
  } catch(e) {
    // 不支持 WebSocket
  }

  return {
    close: () => { if (ws) ws.close(); },
    ws,
  };
}
```

Key changes vs. original:
- Function is now `async` (`export async function createLogSocket`)
- Calls `POST /api/session` before opening WebSocket
- Appends `?session=<nonce>` to WS URL when nonce is available
- Gracefully degrades if `/api/session` returns non-200 (auth disabled or not logged in)

#### Change 7b — Update `connect` callback in `pages/logs.js` (lines 36–71)

The `connect` callback must become `async` to `await` the now-async `createLogSocket`:

```js
// Change line 36:
// BEFORE: const connect = useCallback(() => {
// AFTER:
const connect = useCallback(async () => {
```

```js
// Change line 44:
// BEFORE: const sock = createLogSocket(onLog, (type) => {
// AFTER:
const sock = await createLogSocket(onLog, (type) => {
```

The reconnection `setTimeout` on line 65 does NOT need to change — `connect` being
async means calling it returns a Promise, which `setTimeout` ignores harmlessly:

```js
// Unchanged — this is fine:
setTimeout(() => connect(), 5000);
```

#### Verification

```bash
grep -n 'async function createLogSocket\|await createLogSocket\|api/session' \
  web/static/js/api.js web/static/js/pages/logs.js
# Expected:
# api.js: "export async function createLogSocket"
# api.js: fetch '/api/session' POST
# logs.js: "const connect = useCallback(async"
# logs.js: "await createLogSocket"
```

**Done when:** `createLogSocket` is `async`, fetches nonce, passes `?session=` to WS URL.

---

### T8 — Write tests for new auth behaviour

**File:** `web/server_test.go` (new file)
**REQ:** REQ-SEC-1, REQ-SEC-2

No auth tests exist anywhere. This task creates `web/server_test.go` in `package web`
using an in-memory SQLite DB helper defined **locally in this file**.

#### File header (package declaration + imports)

```go
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
```

> ⚠️ **Do NOT import or reference `initTestDB` from `web/api/api_test.go`.**
> That function lives in `package api` and is not importable from `package web`.
> Define a local `initTestDB` here that only creates the `settings` table —
> the only table `ensureAuthToken()` needs.

#### Local helper (add at top of file, before test functions)

```go
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
```

#### Test cases

> ⚠️ **Variable naming:** Use `database` (not `db`) for the `*db.DB` variable
> in all test functions below to avoid a name collision with the `db` package import.

**Test 8.1 — `TestEnsureAuthToken_FirstRun`**

```go
// Scenario: DB has no auth_token, NO_AUTH not set, AUTH_TOKEN env not set.
// Expect: token written to DB, log message printed.
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
```

**Test 8.2 — `TestEnsureAuthToken_Idempotent`**

```go
// Scenario: DB already has auth_token.
// Expect: token unchanged after second call.
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
```

**Test 8.3 — `TestEnsureAuthToken_NoAuthBypass`**

```go
// Scenario: NO_AUTH=1 env var set.
// Expect: no token written to DB.
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
```

**Test 8.4 — `TestValidateNonce_Valid`**

```go
// Scenario: nonce exists and not expired.
// Expect: validateAndConsumeNonce returns true, nonce deleted after.
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
```

**Test 8.5 — `TestValidateNonce_Expired`**

```go
// Scenario: nonce exists but already expired.
// Expect: returns false.
func TestValidateNonce_Expired(t *testing.T) {
    s := &Server{nonceStore: make(map[string]time.Time)}
    s.nonceStore["expired"] = time.Now().Add(-1 * time.Second) // past
    if s.validateAndConsumeNonce("expired") {
        t.Fatal("expected expired nonce to fail")
    }
}
```

**Test 8.6 — `TestValidateNonce_SingleUse`**

```go
// Scenario: nonce used twice.
// Expect: first use returns true, second use returns false.
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
```

**Test 8.7 — `TestHandleSessionCreate_RequiresPost`**

```go
// Scenario: GET /api/session.
// Expect: 405 Method Not Allowed.
func TestHandleSessionCreate_RequiresPost(t *testing.T) {
    s := &Server{nonceStore: make(map[string]time.Time)}
    req := httptest.NewRequest("GET", "/api/session", nil)
    w := httptest.NewRecorder()
    s.handleSessionCreate(w, req)
    if w.Code != http.StatusMethodNotAllowed {
        t.Fatalf("expected 405, got %d", w.Code)
    }
}
```

**Test 8.8 — `TestHandleSessionCreate_ReturnsNonce`**

```go
// Scenario: POST /api/session (auth disabled, no token set).
// Expect: 200, body contains {"nonce": "<32-char-hex>"}.
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
```

#### Verification

```bash
# After CI runs:
# Tests pass: TestEnsureAuthToken_*, TestValidateNonce_*, TestHandleSessionCreate_*
# No new test failures in existing test suite
```

**Done when:** All 8 tests pass in CI.

---

## Execution Order

| # | Task | Files | Depends on |
|---|------|-------|------------|
| T1 | Re-enable `ensureAuthToken()` + wire `authMiddleware` | `web/server.go` | — |
| T2 | Add nonce store + session handler | `web/server.go` | T1 |
| T3 | Register `/api/session` route | `web/router.go` | T2 |
| T4 | Remove `?token=` from middleware | `web/server.go`, `web/api/middleware.go` | T1 |
| T5 | Add nonce validation to `HandleWSLogs` | `web/api/events.go` | T2 |
| T6 | Wire nonce validator through Router | `web/api/router.go`, `web/server.go` | T5 |
| T7 | Frontend nonce fetch in `createLogSocket` | `web/static/js/api.js`, `web/static/js/pages/logs.js` | T3, T5 |
| T8 | Write auth tests | `web/server_test.go` (new) | T1–T6 |

T1–T4 can be coded in a single commit. T5–T8 should follow to keep diffs reviewable.

---

## Commit Strategy

**Commit 1 — backend auth hardening:**
- `web/server.go` (T1, T2, T4 server-side)
- `web/router.go` (T3)
- `web/api/middleware.go` (T4 api-side)
- `web/api/events.go` (T5)
- `web/api/router.go` (T6)

**Commit 2 — frontend nonce flow:**
- `web/static/js/api.js` (T7a)
- `web/static/js/pages/logs.js` (T7b)

**Commit 3 — tests:**
- `web/server_test.go` (T8, new file)

---

## Success Criteria

| # | Criterion |
|---|-----------|
| 1 | `Start()` line 373 contains `s.ensureAuthToken()` — not commented out |
| 2 | Handler chain is `s.rateLimitMiddleware(s.authMiddleware(s.mux))` |
| 3 | `POST /api/session` returns `{"nonce": "<32-char-hex>"}` with HTTP 200 |
| 4 | `GET /api/ws/logs?session=invalid` → HTTP 401 (before upgrade) |
| 5 | `GET /api/ws/logs?session=<valid>` → 101 Switching Protocols |
| 6 | `GET /api/ws/logs?session=<same-nonce-again>` → 401 (single-use) |
| 7 | `r.URL.Query().Get("token")` → zero matches in middleware files |
| 8 | `NO_AUTH=1` → all endpoints accessible without any token (CI bypass) |
| 9 | `AUTH_TOKEN=xxx` env var → that token accepted; no DB token generated |
| 10 | All 8 new tests pass; all existing tests pass |

---

## Gotchas

1. **`createLogSocket` return type changes.** It was `{close, ws}` (sync); it becomes
   `Promise<{close, ws}>` (async). Every call site must `await` it. Only one call
   site exists: `connect` in `logs.js`. That callback must become `async`.

2. **`/api/session` must NOT be whitelisted.** Only authenticated users should
   receive nonces. The endpoint is behind `authMiddleware`. `NO_AUTH=1` bypass works
   because `getAuthToken()` returns `""` → `authMiddleware` skips all checks.

3. **Cookie auth still works for same-origin browser WS.** The browser sends
   `auth_token` cookie automatically with same-origin WebSocket upgrades. After
   `authMiddleware` passes (cookie valid), `HandleWSLogs` still requires a nonce.
   This means: user must be logged in (cookie set) AND `createLogSocket` must have
   fetched a valid nonce. For `NO_AUTH=1`, `validateNonce` is nil → no nonce check.

4. **`validateAndConsumeNonce` is a method value, not a function literal.** When
   passing it as `func(string) bool`, use `s.validateAndConsumeNonce` directly —
   Go method values capture the receiver, so this is correct.

5. **Two duplicate `authMiddleware` implementations.** Only `server.go:authMiddleware`
   is wired (T1). `api/middleware.go:AuthMiddleware` is cleaned (T4) but not wired.
   A follow-up can consolidate to one, but that is out of scope for Phase 1.1.

6. **SSE endpoint (`/api/events`) needs no changes.** Same-origin SSE sends the
   `auth_token` cookie automatically. The nonce flow applies only to WS.
