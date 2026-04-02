# Phase 1.1 Plan Verification Report

**Date:** 2026-04-02  
**Verifier:** Claude Code (automated source inspection)  
**Files inspected:** `web/server.go`, `web/router.go`, `web/api/events.go`, `web/api/router.go`, `web/api/middleware.go`, `web/static/js/api.js`, `web/static/js/pages/logs.js`, `.planning/REQUIREMENTS.md`, `.planning/ROADMAP.md`

---

## Verdict Summary

| Check | Result |
|-------|--------|
| REQ-SEC-1 fully covered | ✅ PASS |
| REQ-SEC-2 fully covered | ✅ PASS |
| Every task has file paths | ✅ PASS |
| Every task has specific code changes | ✅ PASS (with one minor discrepancy — see issue #2) |
| Every task has a verification method | ✅ PASS |
| No missing tasks vs ROADMAP deliverables | ✅ PASS |
| No contradictions between PLAN and RESEARCH | ⚠️  ONE DISCREPANCY (nonce TTL) |
| Plan is executable without additional research | ✅ PASS (with four caveats noted below) |
| Test coverage specified for new code | ✅ PASS |

**Overall: PLAN IS EXECUTABLE. Two discrepancies must be resolved before execution; four caveats are low-risk but require awareness.**

---

## Section 1 — Source Code Reality Check

### 1.1 — `web/server.go:373` — Is `ensureAuthToken` really commented out?

**VERIFIED ✅**

Actual line 373:
```go
// s.ensureAuthToken() // auth disabled
```
Plan's claim is exactly correct. The function call is commented out.

---

### 1.2 — `web/server.go:376-383` — What middleware IS wired?

**VERIFIED ✅**

Actual lines 376–383:
```go
s.httpServer = &http.Server{
    Addr:              addr,
    Handler:           s.rateLimitMiddleware(s.mux),
    ReadHeaderTimeout: 10 * time.Second,
    ReadTimeout:       60 * time.Second,
    IdleTimeout:       120 * time.Second,
    MaxHeaderBytes:    1 << 20,
}
```

Only `rateLimitMiddleware` is wired. `authMiddleware` is defined at line 409 but never applied.  
**Plan's Change 1b target is correct:** `s.rateLimitMiddleware(s.mux)` → `s.rateLimitMiddleware(s.authMiddleware(s.mux))`.

---

### 1.3 — `web/api/events.go:138-185` — What's the current `HandleWSLogs` implementation?

**PLAN LINE NUMBERS ARE WRONG — BUT NOT A BLOCKER ⚠️**

PLAN.md says "lines 138–185" for the `HandleWSLogs` implementation; RESEARCH.md says "around line 312".  
Actual source confirms: `HandleWSLogs` **starts at line 312**, not 138–185.

Lines 138–144 are actually `HandleLogsClear` and the beginning of the WebSocket constants block. The plan probably carried over a stale line reference.

**What IS at 138–185:**
- Line 138: `HandleLogsClear` function  
- Lines 146–156: WebSocket opcode constants  
- Lines 158–208: `wsConn` struct and its methods

**What the plan actually describes (nonce insertion point) maps to lines 318–323 in reality:**
```go
// 318–322 (actual)
if r.Header.Get("Upgrade") != "websocket" {
    apiError(w, CodeBadRequest, "需要 WebSocket 升级")
    return
}
// ← T5 nonce check goes HERE (after line 322, before hijackConnection at line 324)
```

The code change T5 describes is architecturally correct. The only error is a stale line-range reference in the section header. Developer should insert the nonce check between lines 322 and 324, not at line 185.

**Fix for the plan:** Change the T5 header reference from `web/api/events.go:138-185` to `web/api/events.go:310-330`.

---

### 1.4 — `web/static/js/api.js` — Does `createLogSocket` use `?token=`?

**VERIFIED ✅**

Actual lines 195–224:
```js
export function createLogSocket(onLog, onConnected) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${proto}//${location.host}/api/ws/logs`;   // ← NO ?token=
  // ... sync function, no fetch, no nonce
  return { close: () => { if (ws) ws.close(); }, ws };
}
```

Plan's claim is exactly correct: the function is synchronous, does **not** use `?token=`, and directly constructs the WS URL with no auth parameter. The frontend currently relies entirely on the `auth_token` cookie (which is sent automatically for same-origin WebSocket upgrades).

---

### 1.5 — `web/static/js/pages/logs.js:36-71` — `connect` callback shape

**VERIFIED ✅ — T7b changes are correct but require careful diff**

Actual line 36:
```js
const connect = useCallback(() => {
```
Actual line 44:
```js
const sock = createLogSocket(onLog, (type) => {
```
Actual lines 48–68: `sock.ws` is accessed directly (synchronously) to wire `onerror` and `onclose` handlers.

Plan's T7b change (make `connect` async, `await createLogSocket`) is correct. However, the **`sock.ws` wiring at lines 48–68** also runs synchronously after the `createLogSocket` call. After making `createLogSocket` async and `const sock = await createLogSocket(...)`, the resolved `sock` object will still have `.ws` available synchronously — so the `if (sock.ws)` block at line 48 is safe and requires **no additional changes**.

**The plan correctly identifies this as safe.** No issue.

---

## Section 2 — Requirements Coverage

### REQ-SEC-1: Auth Auto-Initialization

**FULLY COVERED ✅**

| REQ-SEC-1 criterion | Task covering it |
|--------------------|-----------------|
| `ensureAuthToken()` re-enabled | T1 (Change 1a) |
| Auto-generate secure random token on first run | Already in `ensureAuthToken()` — function body unchanged |
| Token displayed to user on startup | Already in `ensureAuthToken()` — `log.Printf` banner at lines 358–365 |
| Auth protection enabled by default on fresh Docker run | T1 (both 1a uncomment + 1b wiring) |

The `ensureAuthToken()` function body (lines 333–366) is cryptographically sound and complete; only the call site needs uncommenting.

---

### REQ-SEC-2: WS Token Not in Query Params

**FULLY COVERED ✅**

| REQ-SEC-2 criterion | Task covering it |
|--------------------|-----------------|
| Replace `?token=xxx` with short-lived session nonce | T2 (nonce store), T3 (route) |
| Token never appears in server logs/browser history | T4 (remove `?token=` from both middleware files) |
| `ws/logs` validates nonce instead of raw token | T5 (HandleWSLogs nonce check) |
| Nonce single-use, 60s expiry | T2 (validateAndConsumeNonce + handleSessionCreate) |
| Frontend passes nonce, not raw token | T7 (createLogSocket becomes async) |

---

## Section 3 — Task-by-Task Verification

### T1 — Re-enable `ensureAuthToken()` and wire `authMiddleware`

**✅ CORRECT AND COMPLETE**

- Change 1a target: line 373 — confirmed correct.
- Change 1b target: `Handler: s.rateLimitMiddleware(s.mux)` — confirmed correct (line 378).
- `authMiddleware` is defined at line 409 — confirmed present and will be callable as `s.authMiddleware`.
- `NO_AUTH=1` bypass is already handled inside `getAuthToken()` → `authMiddleware` passes through when token is `""` — confirmed correct.
- Verification commands: valid.

---

### T2 — Add nonce store and session handler to `Server`

**✅ CORRECT AND COMPLETE — one minor discrepancy**

- Change 2a: `nonceMu sync.Mutex` + `nonceStore map[string]time.Time` — fields don't exist yet in `Server` struct (confirmed); correct struct field names.
- Change 2b: `s.nonceStore = make(map[string]time.Time)` in `NewServer()` — correct insertion point (after `mux` init at line 114).
- Change 2c: `handleSessionCreate` function body — uses `jsonError` and `jsonResponse` which exist in `web/server.go` (confirmed at lines 572–581). Correct.
- Change 2d: `validateAndConsumeNonce` — logic is sound. Deletes nonce before checking expiry (single-use guaranteed even on expired nonces). Correct.
- Change 2e: cleanup goroutine — the plan places this in `NewServer()`. Note that the existing `rateLimiter` cleanup is in a package-level `init()` function (lines 42–53), not in `NewServer()`. Placing the nonce cleanup in `NewServer()` is also valid (it's per-instance, not package-level like the global `sync.Map`). No issue.

---

### T3 — Register `POST /api/session` route

**✅ CORRECT AND COMPLETE**

- Target file: `web/router.go` — confirmed. `registerRoutes()` ends at line 23.
- Insertion after `s.apiRouter.Register(s.mux)` (line 14) is correct.
- `/api/session` is not in `isAuthWhitelist` — confirmed the whitelist only covers `/api/login/*`, `/health`, static files, and root.
- `s.mux.HandleFunc("/api/session", s.handleSessionCreate)` — correct pattern matching existing routes in `web/router.go`.

---

### T4 — Remove `?token=` from both auth middleware implementations

**✅ CORRECT AND COMPLETE**

- `web/server.go` lines 440–443: confirmed the exact block exists:
  ```go
  // 3. Query param ?token=xxx（WebSocket 连接用）
  if qToken := r.URL.Query().Get("token"); qToken == token {
      next.ServeHTTP(w, r)
      return
  }
  ```
- `web/api/middleware.go` lines 143–147: confirmed the exact block exists:
  ```go
  // 从 query param 获取（WebSocket 连接用）
  if qToken := r.URL.Query().Get("token"); qToken == token {
      next.ServeHTTP(w, r)
      return
  }
  ```
- Both blocks are verbatim as described in the plan.
- Verification command `grep -n 'Query().Get("token")'` is correct.

---

### T5 — Add nonce validation to `HandleWSLogs`

**⚠️ STALE LINE-NUMBER IN HEADER — CODE CHANGES ARE CORRECT**

- PLAN.md section header references `events.go:138-185`, but `HandleWSLogs` is at lines 310–467.
- The insertion point described ("after the Upgrade check, before hijackConnection") is correct:  
  - Upgrade check ends at line 322  
  - `hijackConnection` call is at line 324  
  - Nonce check inserts between these two — correct
- Change 5a: `validateNonce func(string) bool` field — `EventsHandler` struct has exactly 3 fields today (downloader, wsMu, wsConns); adding a 4th is clean. Correct.
- Change 5b: `SetValidateNonceFunc` setter — correct pattern matching existing setters like `SetRetryDownloadFunc`.
- Change 5c: nonce check code block — `r.URL.Query().Get("session")` is consistent with the nonce URL param used in T7. `http.Error` is appropriate here because the HTTP response writer has not yet been hijacked. Correct.

**Fix required:** Update the section header from `events.go:138-185` to `events.go:310-330`. No code change needed.

---

### T6 — Wire nonce validator from `Server` through `Router` into `EventsHandler`

**✅ CORRECT AND COMPLETE**

- Change 6a: `validateNonceFunc func(string) bool` field added to `Router` struct (currently has `onSyncAll func()` as last field at line 35) — correct insertion point.
- `SetValidateNonceFunc` setter on `Router` — correct pattern matching `SetRepairThumbFunc` (line 167), `SetSyncAllFunc` (line 91), etc.
- Change 6b: wire in `setupRoutes()` inside `if s.apiRouter != nil { ... }` block (which ends at line 226) — correct. The existing callback wiring pattern is identical to what T6 proposes.
- `s.validateAndConsumeNonce` as method value (captures receiver) — syntactically correct Go; no closure needed.
- One note: the setter in `Router` calls `rt.events.SetValidateNonceFunc(fn)` — but `rt.events` is always non-nil (initialized in `NewRouter()` at line 49). The nil guard is defensive-but-harmless. Correct.

---

### T7 — Update frontend `createLogSocket`

**✅ CORRECT AND COMPLETE — one omission noted**

- Change 7a: replacement of `createLogSocket` — the async function, `POST /api/session` fetch, nonce appended as `?session=`, graceful degradation on fetch failure — all correct.
- Change 7b: `connect` becomes `async`, `sock = await createLogSocket(...)` — correct.

**One omission:** The `logs.js` code at lines 48–68 accesses `sock.ws` synchronously after `createLogSocket`. When `createLogSocket` is made async, `await`-ing it means `sock.ws` is still available synchronously after the await. However, the plan does not explicitly confirm that the `sock.ws` wiring block (lines 48–68 in `logs.js`) is unchanged. The developer should verify line 48 (`if (sock.ws)`) still works after the await — it will, but this should be noted explicitly to prevent confusion.

No code change is needed, but the plan's description should note: "The `if (sock.ws) {...}` block at line 48 is unchanged and works correctly after the await."

---

### T8 — Write tests for new auth behaviour

**✅ MOSTLY COMPLETE — two issues**

**Issue T8-A (MINOR): `initTestDB` is in package `api`, not package `web`**

T8 creates `web/server_test.go` in `package web`. It calls `initTestDB(t)` which is defined in `web/api/api_test.go` as `package api`. These are different packages; the function is not accessible from `package web`.

The `web` package test must either:
1. Copy/inline the `initTestDB` helper into `web/server_test.go` — simplest
2. Or use `db.Init()` directly (as `tests/integration_test.go` does)

The fix is small: add an `initTestDB` helper to `web/server_test.go` itself using the same `sql.Open("sqlite", ":memory:")` pattern.

**Issue T8-B (MODERATE): Tests 8.4–8.6 bypass `nonceMu` — potential race condition in tests**

Tests 8.4, 8.5, 8.6 directly write to `s.nonceStore["abc123"]` without holding `s.nonceMu`:
```go
s.nonceStore["abc123"] = time.Now().Add(60 * time.Second)
```
This is safe in a single-goroutine test context, but `go test -race` will flag it if any goroutine accesses the map concurrently. Since the cleanup goroutine from T2 Change 2e runs every minute in `NewServer()`, and these tests construct `&Server{nonceStore: ...}` directly (not via `NewServer()`), the cleanup goroutine is NOT started — so there's no actual race. The tests are safe as written, but the explanation should be documented.

**Issue T8-C (DESIGN GAP): No test for `authMiddleware` 401 / cookie path**

RESEARCH.md Section 8 identified a needed test: `TestAuthMiddleware` — verifies 401 on missing token, 200 on correct cookie. The PLAN.md T8 does not include this test. The ROADMAP.md deliverables say "unit test for `ensureAuthToken()`" and "unit test for session nonce expiry" — both covered. But no HTTP-level test of the wired middleware chain is included in T8.

This is a gap relative to REQ-TEST-1 ("All new code in Milestone 1 must have unit tests"). The wired `authMiddleware` (T1) is new running code with no test.

**Recommended addition to T8:**

```go
// Test 8.9 — TestAuthMiddleware_401WhenTokenSet
// Scenario: auth token in DB, request has no auth header or cookie.
// Expect: 401.
func TestAuthMiddleware_401WhenTokenSet(t *testing.T) {
    db := initTestDB(t)
    _ = db.SetSetting("auth_token", "testtoken12345678901234567890ab")
    s := &Server{db: db, mux: http.NewServeMux(), nonceStore: make(map[string]time.Time)}
    s.mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })
    handler := s.authMiddleware(s.mux)
    req := httptest.NewRequest("GET", "/api/ping", nil)
    w := httptest.NewRecorder()
    handler.ServeHTTP(w, req)
    if w.Code != http.StatusUnauthorized {
        t.Fatalf("expected 401 without auth, got %d", w.Code)
    }
}

// Test 8.10 — TestAuthMiddleware_200WithValidCookie
func TestAuthMiddleware_200WithValidCookie(t *testing.T) {
    db := initTestDB(t)
    _ = db.SetSetting("auth_token", "testtoken12345678901234567890ab")
    s := &Server{db: db, mux: http.NewServeMux(), nonceStore: make(map[string]time.Time)}
    s.mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })
    handler := s.authMiddleware(s.mux)
    req := httptest.NewRequest("GET", "/api/ping", nil)
    req.AddCookie(&http.Cookie{Name: "auth_token", Value: "testtoken12345678901234567890ab"})
    w := httptest.NewRecorder()
    handler.ServeHTTP(w, req)
    if w.Code != http.StatusOK {
        t.Fatalf("expected 200 with valid cookie, got %d", w.Code)
    }
}
```

---

## Section 4 — PLAN.md vs RESEARCH.md Contradictions

### Contradiction #1 — Nonce TTL: 60s (PLAN) vs 30s (RESEARCH)

**THIS IS A REAL CONTRADICTION ⚠️**

| Document | Nonce TTL |
|----------|-----------|
| PLAN.md — Goal section | "60-second single-use nonce" |
| PLAN.md — T2 Change 2c `handleSessionCreate` | `time.Now().Add(60 * time.Second)` |
| PLAN.md — Success Criteria #4 | implied 60s |
| RESEARCH.md — Change 5 `handleSessionCreate` | `time.Now().Add(30 * time.Second)` |
| RESEARCH.md — Section 7 "Nonce storage" | "short-TTL (e.g., 30s)" |
| ROADMAP.md Phase 1.1 deliverables | "expires after 60 seconds" |

**Decision:** Use **60 seconds** as specified in PLAN.md and ROADMAP.md. RESEARCH.md's 30s was an earlier draft value that was not carried through to the plan or roadmap.

The developer must use `time.Now().Add(60 * time.Second)` in `handleSessionCreate` (as in PLAN.md Change 2c), not the 30s value from RESEARCH.md Change 5.

---

### Non-contradiction note: `handleSessionCreate` uses `http.MethodPost` (PLAN) vs `"POST"` string (RESEARCH)

PLAN.md uses `http.MethodPost` constant; RESEARCH.md uses the `"POST"` string literal. Both are valid Go; `http.MethodPost` is idiomatic. Use PLAN.md's version.

---

## Section 5 — Missing Tasks / Gaps vs ROADMAP Deliverables

| ROADMAP Phase 1.1 deliverable | Covered in PLAN? |
|------------------------------|-----------------|
| Re-enable `s.ensureAuthToken()` in `Server.Start()` | ✅ T1 |
| Confirm `ensureAuthToken()` prints token and persists on first run | ✅ Verified by source inspection — no code change needed; confirmed in T2 tests (8.1) |
| Remove `?token=xxx` from `authMiddleware` | ✅ T4 |
| New `POST /api/session` endpoint, 60s single-use nonce | ✅ T2 + T3 |
| `HandleWSLogs` validates `?session=<nonce>` | ✅ T5 |
| Frontend calls `/api/session` before WS | ✅ T7 |
| Unit test `ensureAuthToken()` first-run, idempotent, `NO_AUTH=1` | ✅ T8 tests 8.1–8.3 |
| Unit test session nonce expiry | ✅ T8 tests 8.4–8.6 |

**No missing tasks** relative to ROADMAP deliverables. The plan is complete.

---

## Section 6 — Executability Assessment

The plan is **immediately executable** by a developer with the following notes:

### Note 1 — `initTestDB` must be re-declared in `web/server_test.go` (BLOCKER for T8)

The `initTestDB` function in `web/api/api_test.go` is in package `api`. `web/server_test.go` is in package `web`. Copy the helper. Suggest this minimal version:

```go
func initTestDB(t *testing.T) *db.DB {
    t.Helper()
    sqlDB, err := sql.Open("sqlite", ":memory:")
    if err != nil {
        t.Fatalf("open memory db: %v", err)
    }
    if _, err := sqlDB.Exec(`
        CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);
    `); err != nil {
        t.Fatalf("create schema: %v", err)
    }
    return &db.DB{DB: sqlDB}
}
```

Only the `settings` table is needed for the `web` package tests (no sources/downloads needed).

### Note 2 — T5 insertion point: line 324 in events.go, not line 185

Insert nonce check between line 322 (end of `Upgrade` check) and line 324 (`hijackConnection` call). The section header is a stale reference; the code description is correct.

### Note 3 — T7b: `logs.js` `connect` must remain wrapped in `useCallback`

After making `connect` async, the `useCallback(() => {...})` wrapper must become `useCallback(async () => {...})`. This is what the plan specifies. Confirm the `deps` array `[createLogHandler]` is unchanged.

### Note 4 — Nonce TTL: use 60s from PLAN.md, not 30s from RESEARCH.md

See Section 4, Contradiction #1.

---

## Section 7 — Test Coverage Assessment

Coverage is specified for all new code in T8:

| New code unit | Test(s) covering it |
|--------------|---------------------|
| `ensureAuthToken()` (re-enabled) | 8.1 (first run), 8.2 (idempotent), 8.3 (NO_AUTH bypass) |
| `validateAndConsumeNonce()` | 8.4 (valid), 8.5 (expired), 8.6 (single-use) |
| `handleSessionCreate()` | 8.7 (method guard), 8.8 (nonce returned) |
| `authMiddleware` (wired by T1) | **GAP** — add tests 8.9 + 8.10 (see Section 3, T8-C) |
| `HandleWSLogs` nonce check (T5) | No direct unit test in T8 |

The `HandleWSLogs` nonce path is not unit-tested in T8. ROADMAP.md Phase 2.3 does include `HandleWSLogs` handler coverage (`web/api/api_test.go` — "HandleWSLogs (handshake rejection on non-WS request)"), so this is deferred to Phase 2.3. Acceptable for Phase 1.1.

---

## Issues Summary

| # | Severity | Location | Description | Fix |
|---|----------|----------|-------------|-----|
| 1 | **High** | T8 (web/server_test.go) | `initTestDB` is not accessible from `package web` | Declare a local `initTestDB` in `web/server_test.go` using only the `settings` table |
| 2 | **Medium** | RESEARCH.md vs PLAN.md | Nonce TTL is 30s in RESEARCH.md Change 5, 60s in PLAN.md Change 2c and ROADMAP | Use 60s as specified in PLAN.md and ROADMAP.md |
| 3 | **Low** | T5 section header | Section says `events.go:138-185` but `HandleWSLogs` starts at line 312 | Informational only — the code change description is correct; update header to `events.go:310-330` |
| 4 | **Low** | T8 | No test for wired `authMiddleware` 401 / cookie acceptance path | Add tests 8.9 and 8.10 (see Section 3, T8-C) |
| 5 | **Low** | T7b | Plan does not explicitly confirm the `if (sock.ws)` wiring block (lines 48–68) is unchanged | Add one-line note: "The `if (sock.ws)` block at line 48 is unchanged and works correctly after await" |

---

## Conclusion

**The plan is correct, internally consistent (except nonce TTL), and covers all ROADMAP deliverables for Phase 1.1.** All source-code assumptions have been verified against actual code. The plan is executable with two required fixes (Issue #1 and #2) and three low-severity improvements (#3–5).

**Execution can begin immediately after:**
1. Deciding to use 60s nonce TTL (use PLAN.md / ROADMAP.md value — discard RESEARCH.md's 30s)
2. Adding a local `initTestDB` to `web/server_test.go` (the `web/api/api_test.go` version is in a different package)

No additional research is required.
