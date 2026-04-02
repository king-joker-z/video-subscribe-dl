# Phase 01.1 — Auth Hardening: Post-Execution Verification

**Date:** 2026-04-02
**Type:** POST-EXECUTION (replaces planning-phase VERIFICATION.md)
**Verifier:** Claude Code — automated source inspection
**Phase goal:** Ensure every VSD deployment starts with authentication enabled by default, and that the long-lived auth token is never exposed in WebSocket URLs or server logs.
**Requirements in scope:** REQ-SEC-1, REQ-SEC-2

---

## Overall Verdict

| Requirement | Status |
|-------------|--------|
| REQ-SEC-1: Auth Auto-Initialization | ✅ PASS |
| REQ-SEC-2: WS Token Not in Query Params | ✅ PASS |

**Phase 01.1 goal: ACHIEVED. All 8 must-haves confirmed present in the codebase.**

---

## Must-Have Checklist

### MH-1 — `s.ensureAuthToken()` is NOT commented out in `Start()`

**✅ PASS**

`web/server.go` line 397:
```go
// 自动生成 auth_token（如果未设置且未禁用）
s.ensureAuthToken()
```
The call is active (uncommented). It executes on every `Start()` invocation before the HTTP server binds.

---

### MH-2 — Handler chain is `rateLimitMiddleware(authMiddleware(mux))`

**✅ PASS**

`web/server.go` line 402:
```go
Handler: s.rateLimitMiddleware(s.authMiddleware(s.mux)),
```
Both middleware layers are wired. `rateLimitMiddleware` is the outer layer (enforced first); `authMiddleware` is the inner layer (enforced after rate-limit passes).

---

### MH-3 — `POST /api/session` handler exists and returns 32-char hex nonce

**✅ PASS**

Route registration — `web/router.go` line 17:
```go
s.mux.HandleFunc("/api/session", s.handleSessionCreate)
```

Handler — `web/server.go` lines 590–605:
```go
func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost { ... 405 ... }
    b := make([]byte, 16)
    rand.Read(b)
    nonce := hex.EncodeToString(b)          // hex(16 bytes) = 32 chars
    s.nonceStore[nonce] = time.Now().Add(60 * time.Second)
    jsonResponse(w, map[string]string{"nonce": nonce})
}
```
`hex.EncodeToString(16 bytes)` = 32 hex characters. TTL = 60 seconds as specified by PLAN.md / ROADMAP.md. Single-use enforced via `validateAndConsumeNonce`.

---

### MH-4 — `GET /api/ws/logs` nonce validation exists before WebSocket upgrade

**✅ PASS**

`web/api/events.go` lines 338–344 (inside `HandleWSLogs`, after the `Upgrade` header check, before `hijackConnection`):
```go
if h.validateNonce != nil {
    nonce := r.URL.Query().Get("session")
    if nonce == "" || !h.validateNonce(nonce) {
        http.Error(w, "invalid or expired session nonce", http.StatusUnauthorized)
        return
    }
}
```
When auth is enabled, `h.validateNonce` is non-nil (wired via `SetValidateNonceFunc`). The nonce is consumed from `?session=` — not `?token=`. When `NO_AUTH=1`, `validateNonce` remains nil and the check is skipped.

The wiring chain is complete:
- `web/server.go:249` — `s.apiRouter.SetValidateNonceFunc(s.validateAndConsumeNonce)`
- `web/api/router.go:173–178` — `Router.SetValidateNonceFunc` → `rt.events.SetValidateNonceFunc(fn)`
- `web/api/events.go:43–45` — `EventsHandler.SetValidateNonceFunc` sets `h.validateNonce`

---

### MH-5 — `?token=` query-param block removed from both middleware files

**✅ PASS**

Search result for `Query().Get("token")` across `web/server.go` and `web/api/middleware.go`:
```
NOT FOUND
```

Neither `web/server.go`'s `authMiddleware` nor `web/api/middleware.go`'s `AuthMiddleware` contains a `?token=` query-parameter fallback. The pre-execution VERIFICATION.md confirmed both blocks existed; they have been removed.

---

### MH-6 — `NO_AUTH=1` bypass still intact in `authMiddleware`

**✅ PASS**

`web/server.go` — `getAuthToken()` (called inside `authMiddleware`), lines 419–431:
```go
func (s *Server) getAuthToken() string {
    noAuth := os.Getenv("NO_AUTH")
    if noAuth == "1" || noAuth == "true" {
        return "" // 禁用认证
    }
    ...
}
```
When `NO_AUTH=1`, `getAuthToken()` returns `""`. In `authMiddleware` (line 443–446):
```go
token := s.getAuthToken()
if token == "" {
    next.ServeHTTP(w, r)
    return
}
```
All requests pass through without authentication checks.

Additionally, `ensureAuthToken()` also short-circuits when `NO_AUTH=1` (line 364), so no token is generated or stored in the DB.

---

### MH-7 — `createLogSocket` is `async` and fetches `/api/session` before WS open

**✅ PASS**

`web/static/js/api.js` line 198:
```js
export async function createLogSocket(onLog, onConnected) {
```

The function:
1. `await fetch('/api/session', { method: 'POST', credentials: 'include' })` to obtain a nonce
2. On success, builds WS URL as `?session=${encodeURIComponent(nonce)}`
3. On failure (network error or non-ok response), proceeds without nonce — WS upgrade will return 401 and the caller falls back to SSE
4. Returns the `ws` object (or null) — never the raw auth token

The long-lived token never appears in the WebSocket URL or browser history.

`web/static/js/pages/logs.js` — `connect` callback:
- Line 36: `const connect = useCallback(async () => {`
- Line 44: `const sock = await createLogSocket(onLog, ...)`

Both the definition and the call-site are correctly async.

---

### MH-8 — 8 test functions exist in `web/server_test.go`

**✅ PASS**

Exact count: **8** test functions, confirmed by `grep -c "^func Test"`.

| # | Function name | Coverage |
|---|--------------|----------|
| 1 | `TestEnsureAuthToken_FirstRun` | Fresh DB → 32-char token generated & persisted |
| 2 | `TestEnsureAuthToken_Idempotent` | Existing token unchanged on re-call |
| 3 | `TestEnsureAuthToken_NoAuthBypass` | `NO_AUTH=1` → no token generated |
| 4 | `TestValidateNonce_Valid` | Non-expired nonce accepted, deleted after use |
| 5 | `TestValidateNonce_Expired` | Expired nonce rejected |
| 6 | `TestValidateNonce_SingleUse` | Second use of same nonce fails |
| 7 | `TestHandleSessionCreate_RequiresPost` | GET returns 405 |
| 8 | `TestHandleSessionCreate_ReturnsNonce` | POST returns 200 with 32-char nonce |

Package: `package web`. Uses local `initTestDB` helper (in-memory SQLite, `settings` table only) — correctly separate from `web/api/api_test.go`'s same-named function in `package api`.

---

## Requirements Cross-Reference

### REQ-SEC-1: Auth Auto-Initialization

> *On first run with no `AUTH_TOKEN` env var and no DB token, auto-generate a secure random token. Token must be displayed to user on startup. Fresh Docker container starts with auth protection enabled by default.*

| Criterion | Evidence | Status |
|-----------|----------|--------|
| `ensureAuthToken()` re-enabled | `server.go:397` — uncommented call in `Start()` | ✅ |
| Auto-generate secure random token on first run | `server.go:374–380` — `crypto/rand`, `hex.EncodeToString(16 bytes)` | ✅ |
| Token displayed to user on startup | `server.go:384–390` — `log.Printf` banner block with token | ✅ |
| Token persisted to DB | `server.go:380` — `s.db.SetSetting("auth_token", token)` | ✅ |
| Auth protection enabled by default | `server.go:402` — `authMiddleware` wired in handler chain | ✅ |

**REQ-SEC-1: FULLY MET ✅**

---

### REQ-SEC-2: WS Token Not in Query Params

> *WebSocket auth via `?token=xxx` must be replaced with a short-lived session token or cookie. Token must not appear in server logs or browser history. `ws/logs` connection does not expose long-lived token in URL.*

| Criterion | Evidence | Status |
|-----------|----------|--------|
| `?token=xxx` removed from middleware | `Query().Get("token")` not found in `server.go` or `api/middleware.go` | ✅ |
| Short-lived nonce issued via `POST /api/session` | `router.go:17`, `server.go:590–605` | ✅ |
| Nonce is single-use | `validateAndConsumeNonce` deletes nonce before return | ✅ |
| Nonce expires after 60s | `server.go:602` — `time.Now().Add(60 * time.Second)` | ✅ |
| `ws/logs` validates nonce (`?session=`) | `events.go:338–344` — nonce check before hijack | ✅ |
| Long-lived token never in WS URL | `api.js:215–216` — WS URL uses `?session=<nonce>`, not `?token=` | ✅ |
| Auth-disabled mode (`NO_AUTH=1`) skips nonce check | `events.go:338` — `if h.validateNonce != nil` guard | ✅ |

**REQ-SEC-2: FULLY MET ✅**

---

## Build / Test Verification

### Source inspection (automated — completed above)
All 8 must-haves confirmed by direct file inspection. No issues found.

### Compilation and test execution

> **HUMAN VERIFICATION REQUIRED**
>
> Test compilation and execution cannot be confirmed by automated source inspection alone. The following commands must be run by CI or a developer with a working Go toolchain before this phase is declared fully closed:
>
> ```bash
> # Compile the web package (no test run needed to check syntax)
> go build ./web/...
>
> # Run the new server-level tests
> go test -v -race ./web/ -run "TestEnsureAuthToken|TestValidateNonce|TestHandleSessionCreate"
>
> # Run the full test suite to check for regressions
> go test ./...
> ```
>
> Expected outcomes:
> - All 8 test functions in `web/server_test.go` pass
> - `go test -race` reports no data races
> - No regressions in `web/api/api_test.go` or `tests/integration_test.go`

---

## Observations / Notes

1. **`web/api/middleware.go` `AuthMiddleware` is defined but not used in the main handler chain.** The active auth middleware is `web/server.go`'s `s.authMiddleware`, wired at `server.go:402`. The `api.AuthMiddleware` factory in `web/api/middleware.go` remains dead code from a prior refactor. This is benign but worth a future cleanup. It does not affect correctness.

2. **Nonce TTL is 60s** — consistent with PLAN.md and ROADMAP.md. The planning-phase VERIFICATION.md identified a contradiction (RESEARCH.md said 30s); the executed code uses the correct 60s value.

3. **`createLogSocket` falls back gracefully.** When `POST /api/session` returns non-ok (e.g., 401 before the user logs in), the WS URL is built without `?session=`. The WS upgrade will then receive a 401 from the nonce guard, triggering the `onerror` handler in `logs.js` which falls back to the SSE path. This is the correct UX behavior.

4. **No test for `authMiddleware` 401/cookie path (flagged in planning VERIFICATION.md as T8-C gap).** This remains an open gap deferred to Phase 2.3 per the planning decision. The gap does not block phase completion for REQ-SEC-1 / REQ-SEC-2.

---

## Phase Completion Status

| Item | Status |
|------|--------|
| All must-haves confirmed in codebase | ✅ DONE |
| REQ-SEC-1 fully met | ✅ DONE |
| REQ-SEC-2 fully met | ✅ DONE |
| Test compilation / `go test -race` | ⏳ PENDING CI |
| Regression test run | ⏳ PENDING CI |

**Phase 01.1 is complete pending CI green on `go test ./...`.**
