# Phase 1.1 Auth Hardening — Research

**Date:** 2026-04-02
**Working directory:** `video-subscribe-dl/`
**Goal:** Re-enable `ensureAuthToken()` and replace `?token=xxx` WebSocket auth with a short-lived session nonce.

---

## 1. What does `ensureAuthToken()` do? (REQ-SEC-1)

**File:** `web/server.go`, lines 333–366

```go
// web/server.go:333-366
func (s *Server) ensureAuthToken() {
    // 环境变量优先
    if os.Getenv("AUTH_TOKEN") != "" {
        log.Printf("[auth] 使用环境变量 AUTH_TOKEN")
        return
    }
    // 如果环境变量 NO_AUTH=1 则禁用
    if noAuth := os.Getenv("NO_AUTH"); noAuth == "1" || noAuth == "true" {
        log.Printf("[auth] 认证已禁用 (NO_AUTH=%s)", noAuth)
        return
    }
    // 检查 DB 是否已有 token
    if token, err := s.db.GetSetting("auth_token"); err == nil && token != "" {
        log.Printf("[auth] Web UI 认证已启用，token: %s", token)
        return
    }
    // 自动生成随机 token
    b := make([]byte, 16)
    if _, err := rand.Read(b); err != nil {
        log.Printf("[auth] 生成 token 失败: %v，认证未启用", err)
        return
    }
    token := hex.EncodeToString(b)
    if err := s.db.SetSetting("auth_token", token); err != nil {
        log.Printf("[auth] 保存 token 失败: %v", err)
        return
    }
    log.Printf("============================================")
    log.Printf("[auth] Web UI 认证 Token（首次生成）: %s", token)
    log.Printf("[auth] 请妥善保存此 Token，用于登录 Web 界面")
    log.Printf("[auth] 可通过设置页面修改或设置环境变量 AUTH_TOKEN")
    log.Printf("[auth] 设置 NO_AUTH=1 可禁用认证")
    log.Printf("============================================")
}
```

### Answers:
- **Does it already print to stdout?** YES — it uses `log.Printf` which writes to stderr (Go's default log target), but is treated as stdout in most container/daemon setups. The banner is already there with `============================================` delimiters. No changes needed to the function body.
- **Does it already generate a random token?** YES — uses `crypto/rand` (16 bytes → 32-char hex string). This is cryptographically secure.
- **What is the problem?** It is **disabled** in `Start()`:

```go
// web/server.go:372-373
// 自动生成 auth_token（如果未设置且未禁用）
// s.ensureAuthToken() // auth disabled   ← COMMENTED OUT
```

**REQ-SEC-1 fix is trivially one line:** uncomment `s.ensureAuthToken()`.

---

## 2. The `NO_AUTH` / `AUTH_TOKEN` env var bypass

**File:** `web/server.go`, lines 395–407

```go
// web/server.go:395-407
func (s *Server) getAuthToken() string {
    noAuth := os.Getenv("NO_AUTH")
    if noAuth == "1" || noAuth == "true" {
        return "" // 禁用认证
    }
    if t := os.Getenv("AUTH_TOKEN"); t != "" {
        return t
    }
    if t, err := s.db.GetSetting("auth_token"); err == nil && t != "" {
        return t
    }
    return ""
}
```

**`NO_AUTH` behavior:** Setting `NO_AUTH=1` or `NO_AUTH=true` causes `getAuthToken()` to return `""`, which makes `authMiddleware` skip all token checks (line 420–422: `if token == "" { next.ServeHTTP(...); return }`). This is the intentional test/dev bypass. No changes needed.

**Note:** There is a **duplicate** `authMiddleware` implementation. The one in `web/server.go` (lines 409–454) is the one actually wired up (via `Start()`), but it is **not currently wired** either! `Start()` only wraps with `rateLimitMiddleware`, not `authMiddleware`:

```go
// web/server.go:377-386
s.httpServer = &http.Server{
    Addr:    addr,
    Handler: s.rateLimitMiddleware(s.mux),  // ← auth middleware NOT applied
    ...
}
```

There is also `web/api/middleware.go:109-155` which has an identical `AuthMiddleware` function but it is also **never registered** in `registerRoutes()` or `router.Register()`.

**⚠️ GOTCHA:** Both `authMiddleware` implementations exist but **neither is applied** to the handler chain. REQ-SEC-1 requires two fixes:
1. Uncomment `s.ensureAuthToken()` in `Start()`
2. Wire `s.authMiddleware` into the handler chain in `Start()`

---

## 3. The exact `?token=xxx` WebSocket auth code path (REQ-SEC-2)

### Server side — `web/server.go:440-443`

```go
// web/server.go:440-443
// 3. Query param ?token=xxx（WebSocket 连接用）
if qToken := r.URL.Query().Get("token"); qToken == token {
    next.ServeHTTP(w, r)
    return
}
```

This is inside `authMiddleware` in `web/server.go` (lines 409–454). The same pattern is duplicated in `web/api/middleware.go:143-147`:

```go
// web/api/middleware.go:143-147
// 从 query param 获取（WebSocket 连接用）
if qToken := r.URL.Query().Get("token"); qToken == token {
    next.ServeHTTP(w, r)
    return
}
```

### Why it is a security problem:
- The long-lived auth token (same one used for login) appears verbatim in `?token=<hex>` in the WebSocket URL.
- WebSocket URLs appear in server access logs, browser history, proxy logs — the long-lived secret leaks into logs.
- There is currently **no `?token=`** being constructed or sent by the frontend (see Section 4 below).

---

## 4. How the frontend currently connects to WebSocket

**File:** `web/static/js/api.js`, lines 196–224

```js
// web/static/js/api.js:196-224
export function createLogSocket(onLog, onConnected) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${proto}//${location.host}/api/ws/logs`;  // ← NO ?token= here

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
      console.log('WebSocket failed, falling back to SSE');
      ws.close();
    };
    ws.onclose = () => {};
  } catch(e) {
    // 不支持 WebSocket
  }
  return { close: () => { if (ws) ws.close(); }, ws };
}
```

**Finding:** The frontend currently does NOT append `?token=` to the WebSocket URL. The WS connection at `/api/ws/logs` is authenticated by the `auth_token` cookie (set by `POST /api/login/token`), which the browser sends automatically with same-origin WebSocket upgrades.

**So the current state is:**
- Auth is disabled → WS works with no auth
- If auth were enabled → WS would be blocked (401) because no `?token=` is sent by frontend
- The `?token=` path in `authMiddleware` is dead code that was never used by the frontend

This means REQ-SEC-2 implementation must ensure the WS endpoint stays accessible after login. The cookie-based approach already works — once the user logs in and gets the `auth_token` cookie, it is sent with WS upgrade requests automatically.

---

## 5. Are there other places where `?token=` is constructed or used?

**Searched:** All `web/` `.go` and `.js` files.

**Result:** No frontend code constructs a `?token=xxx` URL. The only `?token=` references are:
- `web/server.go:441` — server-side check (dead code from frontend's perspective)
- `web/api/middleware.go:144` — duplicate server-side check (dead code)

**Conclusion:** The `?token=` query param support is defensive server-side code that was written in anticipation of WS use but was never exercised by the client.

---

## 6. Existing test infrastructure

### `web/api/api_test.go` (package `api`)
- Uses `httptest.NewRecorder` + `httptest.NewRequest`
- Has `initTestDB()` (in-memory SQLite), `setupTestRouter()`, `parseResponse()`
- Tests: `TestPingEndpoint`, `TestSourcesCRUD`, `TestSourceTypeValidation`, `TestHealthEndpoint`, `TestVideoSearchByUploader`
- **No auth tests** — all tests bypass auth (auth middleware is never wired in the test router)

### `tests/integration_test.go` (package `tests`)
- Uses `db.Init()` + `newapi.NewRouter()` → full integration tests
- Has `apiCall()` helper
- Tests: source CRUD, douyin flow, metrics, prometheus, sign reload, etc.
- **No auth tests** either

### Key insight for new tests:
The `setupTestRouter` in `api_test.go` creates a raw `http.ServeMux` without `authMiddleware`. For session nonce tests, we'll need a test helper that wraps the mux with the new `SessionMiddleware`. The in-memory DB pattern (`initTestDB`) is ideal for storing nonces in the `settings` table (or a dedicated map).

---

## 7. Session nonce compatibility (Cookie domain, CORS, etc.)

### Cookie behavior
- `POST /api/login/token` sets `auth_token` cookie with:
  - `Path=/`, `MaxAge=86400*30`, `HttpOnly=true`, `SameSite=Lax`
  - **No `Secure` flag** (would need HTTPS), **No `Domain`** (defaults to request host)
- The nonce cookie for WS must be **short-lived** and different from the auth cookie. It should NOT be `HttpOnly` if set by JS (but since `POST /api/session` is server-side, it can be `HttpOnly`). Actually — for the nonce flow, the nonce is passed as a URL query param (`?session=<nonce>`), not as a cookie. So cookie domain/SameSite are irrelevant for the nonce itself.

### CORS
- CORS is handled by `CORSMiddleware` (in `web/api/middleware.go`) via `CORS_ORIGIN` env var, but this middleware is **never wired** into the handler chain either (same issue as `AuthMiddleware`).
- In practice, VSD serves both frontend and backend from the same origin (port), so there's no CORS issue for `POST /api/session`. The frontend uses `fetch('/api/session', ...)` with same-origin credentials.

### WebSocket upgrade & cookies
- The browser sends cookies automatically for same-origin WS upgrades (`new WebSocket('ws://same-host/...')`).
- For the nonce approach, the browser JS calls `POST /api/session` (with the `auth_token` cookie), gets back a short-lived nonce, then connects `new WebSocket('ws://host/api/ws/logs?session=<nonce>')`.
- The nonce is single-use + short-TTL (e.g., 30s). Even if it appears in a log, it expires before it can be replayed.

### Nonce storage
- Must be server-side. Options:
  1. In-memory `sync.Map` on `Server` struct (simplest, works for single-process)
  2. In the SQLite DB `settings` table (overkill, adds latency)

  **Recommend option 1** (in-memory map) since VSD is single-process and nonces are ephemeral. The map entry TTL can be enforced by storing `(nonce → expiresAt)` and checking at validation time, with a periodic cleanup goroutine (similar to the existing `rateLimiter` cleanup).

---

## 8. Complete map of files to change

### Backend

#### `web/server.go`

**Change 1** — Re-enable `ensureAuthToken()` (line 373):
```go
// BEFORE (line 373):
// s.ensureAuthToken() // auth disabled

// AFTER:
s.ensureAuthToken()
```

**Change 2** — Wire `authMiddleware` into handler chain (line 378):
```go
// BEFORE:
Handler: s.rateLimitMiddleware(s.mux),

// AFTER:
Handler: s.rateLimitMiddleware(s.authMiddleware(s.mux)),
```

**Change 3** — Remove `?token=xxx` check from `authMiddleware` (lines 440–443):
```go
// REMOVE these 4 lines from authMiddleware:
// 3. Query param ?token=xxx（WebSocket 连接用）
if qToken := r.URL.Query().Get("token"); qToken == token {
    next.ServeHTTP(w, r)
    return
}
```

**Change 4** — Add nonce store fields to `Server` struct (after line 103):
```go
// Add to Server struct:
nonceMu    sync.Mutex
nonceStore map[string]time.Time // nonce → expiresAt
```

**Change 5** — Add `handleSessionCreate` handler (new function):
```go
// POST /api/session → returns a short-lived nonce
func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
    if r.Method != "POST" {
        jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    // Must be authenticated (authMiddleware already enforced)
    b := make([]byte, 16)
    if _, err := rand.Read(b); err != nil {
        jsonError(w, "nonce generation failed", http.StatusInternalServerError)
        return
    }
    nonce := hex.EncodeToString(b)
    s.nonceMu.Lock()
    s.nonceStore[nonce] = time.Now().Add(30 * time.Second)
    s.nonceMu.Unlock()
    jsonResponse(w, map[string]string{"nonce": nonce})
}
```

**Change 6** — Add nonce validation helper (new function):
```go
func (s *Server) validateAndConsumeNonce(nonce string) bool {
    s.nonceMu.Lock()
    defer s.nonceMu.Unlock()
    exp, ok := s.nonceStore[nonce]
    if !ok {
        return false
    }
    delete(s.nonceStore, nonce) // single-use
    return time.Now().Before(exp)
}
```

**Change 7** — Add nonce cleanup goroutine in `NewServer` or `Start()`:
```go
// Add to Start() or as a background goroutine in NewServer:
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

**Change 8** — Initialize `nonceStore` in `NewServer` (around line 107):
```go
s.nonceStore = make(map[string]time.Time)
```

#### `web/router.go`

**Change 9** — Register `POST /api/session` route (in `registerRoutes()`):
```go
// Add after the existing route registrations:
s.mux.HandleFunc("/api/session", s.handleSessionCreate)
```

#### `web/server.go` — `isAuthWhitelist`

No change needed — `/api/session` must be behind auth (only authenticated users should get a nonce). It should NOT be whitelisted.

#### `web/api/events.go` — `HandleWSLogs`

**Change 10** — Add nonce validation for WS connection. The handler currently does no auth itself (relies on middleware). We need to pass a nonce validator into `EventsHandler` or handle it at the route level.

**Recommended approach:** Inject a `validateNonce func(string) bool` into `EventsHandler`:

```go
// In EventsHandler struct (web/api/events.go):
type EventsHandler struct {
    downloader    *downloader.Downloader
    validateNonce func(string) bool // NEW: nil means auth disabled / cookie-only
    wsMu          sync.Mutex
    wsConns       map[*wsConn]struct{}
}
```

In `HandleWSLogs` (around line 312), add nonce check BEFORE the WebSocket upgrade:
```go
// After method check, before hijack:
if h.validateNonce != nil {
    nonce := r.URL.Query().Get("session")
    if nonce == "" || !h.validateNonce(nonce) {
        http.Error(w, "invalid or expired session nonce", http.StatusUnauthorized)
        return
    }
}
```

This check happens after `authMiddleware` already passed the request through (for cookie auth), but for WS we additionally require a short-lived nonce.

**Alternative simpler approach:** Exempt `/api/ws/logs` from `authMiddleware` (whitelist it) and do the nonce check entirely inside `HandleWSLogs`. This avoids adding a callback and keeps the WS handler self-contained.

#### `web/api/middleware.go` — `AuthMiddleware`

**Change 11** — Remove `?token=xxx` query param check (lines 143–147):
```go
// REMOVE:
// 从 query param 获取（WebSocket 连接用）
if qToken := r.URL.Query().Get("token"); qToken == token {
    next.ServeHTTP(w, r)
    return
}
```

Note: `web/api/middleware.go:AuthMiddleware` is currently not wired into any handler chain. It may be intended for future use, or was superseded by the `authMiddleware` in `server.go`. Cleaning it up too is good hygiene.

#### `web/api/router.go`

**Change 12** — No route changes needed. `/api/session` is registered directly on `s.mux` in `registerRoutes()` (it's a `Server`-level handler, not an `api.Router` handler, because it needs access to the nonce store).

Alternatively, the session endpoint could be added to `api.Router` by injecting the nonce store/validator — but since the nonce store lives on `*Server`, keeping it in `web/server.go` + `web/router.go` is cleaner.

### Frontend

#### `web/static/js/api.js` — `createLogSocket`

**Change 13** — Add `POST /api/session` call before creating the WebSocket:

```js
// web/static/js/api.js — replace createLogSocket (lines 196-224)
export async function createLogSocket(onLog, onConnected) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';

  // Obtain a short-lived session nonce for WebSocket auth
  let nonce = '';
  try {
    const res = await fetch('/api/session', { method: 'POST', credentials: 'include' });
    if (res.ok) {
      const data = await res.json();
      nonce = data.nonce || '';
    }
  } catch (e) {
    // If session endpoint fails (e.g. auth disabled), proceed without nonce
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
      console.log('WebSocket failed, falling back to SSE');
      ws.close();
    };
    ws.onclose = () => {};
  } catch(e) {
    // 不支持 WebSocket
  }
  return { close: () => { if (ws) ws.close(); }, ws };
}
```

**Note:** `createLogSocket` becomes `async`. The caller in `pages/logs.js` must be updated accordingly.

#### `web/static/js/pages/logs.js` — `connect` callback

**Change 14** — Update `connect` to await the now-async `createLogSocket`:

```js
// web/static/js/pages/logs.js:36-71 — connect becomes async
const connect = useCallback(async () => {
  if (connectionRef.current) {
    connectionRef.current.close();
    connectionRef.current = null;
  }
  const onLog = createLogHandler();
  const sock = await createLogSocket(onLog, (type) => {  // await here
    setConnType('ws');
  });
  // ... rest unchanged
}, [createLogHandler]);
```

---

## 9. Auth middleware wiring — detailed current state

**Critical finding:** Auth middleware is defined but never applied to the HTTP server.

```
HTTP request
    → rateLimitMiddleware(s.mux)    ← only middleware applied (server.go:378)
        → s.mux.ServeHTTP(r)        ← routes hit directly

s.authMiddleware  ← DEFINED (server.go:409) but never called
api.AuthMiddleware ← DEFINED (api/middleware.go:109) but never called
```

After the fix:
```
HTTP request
    → rateLimitMiddleware
        → authMiddleware             ← add this
            → s.mux.ServeHTTP(r)
```

---

## 10. Recommended implementation approach

### Phase order

1. **REQ-SEC-1 first (2 changes):**
   - Uncomment `s.ensureAuthToken()` in `Start()`
   - Wire `s.authMiddleware(s.mux)` in `Start()`
   - This is zero-risk if `NO_AUTH=1` is set during development, so existing infra is not broken.

2. **REQ-SEC-2 second (nonce plumbing):**
   - Add nonce store to `Server` struct + `NewServer`
   - Add `handleSessionCreate` handler + register route
   - Add nonce validator to `EventsHandler`
   - Update `HandleWSLogs` to validate nonce
   - Remove `?token=` from both `authMiddleware` implementations
   - Update frontend `createLogSocket` to be async

### Why nonce over cookie-for-WS

The WS endpoint is currently protected by cookie (if auth were enabled). The cookie approach already works for same-origin — so why add nonces?

**Reason:** Token in URL leaks into logs. Cookie in WS upgrade is invisible in access logs. **So strictly speaking, the cookie approach is already sufficient and safe**. The nonce adds defense-in-depth: a nonce is single-use + 30s TTL, so even if a log entry or browser history entry is captured, it cannot be replayed.

**However:** Since the frontend currently does NOT send `?token=` in the WS URL, the primary attack surface (long-lived token in URL/logs) doesn't actually exist today. The real risk is: if someone adds `?token=` support to the frontend in the future, it would be a security regression.

**Pragmatic recommendation:**
- Implement REQ-SEC-1 (uncomment + wire auth) — this is the critical change.
- For REQ-SEC-2, the minimum safe implementation is: remove `?token=` from `authMiddleware` (so it can never be used), keep WS auth cookie-based. The full nonce session flow is a nice-to-have that provides defense-in-depth.
- If the full nonce flow is desired, implement it as described above.

### Nonce store: in-memory vs DB

| Criterion | In-memory `sync.Map` | SQLite `settings` table |
|-----------|---------------------|------------------------|
| Simplicity | ✅ Simple | ❌ Extra DB ops |
| Persistence across restart | ❌ Lost on restart | ✅ Survives restart |
| Appropriate for 30s TTL nonces | ✅ Yes (they expire anyway) | ❌ Overkill |
| Multi-process safe | ❌ No (VSD is single-process) | ✅ Yes |

**Decision: in-memory map with `sync.Mutex`.** VSD is a single Go binary, single process. Nonces surviving a restart have no value. Reuse the same cleanup goroutine pattern as `rateLimiter` (package-level in `server.go`).

---

## 11. Gotchas and surprises

1. **Auth was never wired at all.** The `authMiddleware` function exists but the server never applies it. Enabling auth requires both uncommenting `ensureAuthToken()` AND adding the middleware to the handler chain. Missing either one leaves auth non-functional.

2. **Two auth middleware implementations.** `web/server.go:authMiddleware` and `web/api/middleware.go:AuthMiddleware` are near-identical. The `api` package version takes a `getToken func() string` argument (cleaner design). Consider consolidating to the api package version in a follow-up, but for Phase 1.1 just wire the `server.go` version (it already has access to `s.getAuthToken()`).

3. **`createLogSocket` is not async today.** Making it `async` is a breaking API change for callers. Only `pages/logs.js` calls it, so this is safe, but the return type changes from `{close, ws}` to a `Promise<{close, ws}>`. The caller's `connect` callback in `logs.js` must also become `async`.

4. **SSE endpoint (`/api/events`) has no `?token=` mechanism.** The global SSE singleton in `app.js` (`ensureGlobalSSE`) connects to `/api/events` with no auth params. It relies on cookie auth. This is fine — same-origin SSE also sends cookies automatically. No changes needed for SSE.

5. **`/api/session` must be POST, not GET.** GET requests are cached, logged by proxies, and appear in browser history with parameters. POST avoids all of this.

6. **SameSite=Lax on the auth cookie is compatible with `POST /api/session`.** Lax allows cookies to be sent with same-site cross-page navigations and same-origin `fetch` with `credentials: 'include'`. Since `POST /api/session` is same-origin, the cookie is sent.

7. **The `auth_token` setting appears in the public settings keys list** (`web/api/settings.go:44`). It's also in `sensitiveKeys` (line 52) so it returns `"***"` masked. This is fine — it means the frontend can confirm that a token is set without seeing its value.

8. **Test coverage gap:** Neither `api_test.go` nor `integration_test.go` has any auth-related tests. Phase 1.1 should add:
   - `TestEnsureAuthToken` — verifies token is generated on first run and persisted
   - `TestAuthMiddleware` — verifies 401 on missing token, 200 on correct cookie
   - `TestSessionNonce` — verifies `POST /api/session` returns a nonce, nonce works once, second use fails
   - `TestNonceExpiry` — verifies nonce fails after TTL
