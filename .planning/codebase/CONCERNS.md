# Technical Concerns

> Based on full codebase audit, BUG-AUDIT.md (2026-03-25), and TODO.md.
> Severity: 🔴 Critical · 🟠 High · 🟡 Medium · 🟢 Low

---

## Security Concerns

### 🔴 Auth Is Effectively Disabled
**File:** `web/server.go:373`
`s.ensureAuthToken()` is commented out with `// auth disabled`. The `authMiddleware` still exists and works, but the token is never auto-generated on first run. A deployment that relies on the auto-generate path will start with **no authentication**. Users must manually set `AUTH_TOKEN` env var or the DB key.

### 🟠 Token Exposed in Query Parameter
**File:** `web/server.go:441`, `web/api/middleware.go:144`
`?token=xxx` is accepted for WebSocket authentication. Query params appear in server logs, proxy access logs, and browser history — this leaks the auth token to any intermediate system.

### 🟡 Rate Limiter Trusts X-Forwarded-For Without Validation
**File:** `web/server.go:495-498`
```go
if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
    ip = strings.Split(forwarded, ",")[0]
}
```
Any client can spoof this header to bypass per-IP rate limiting. Acceptable for a personal LAN tool, but a risk if exposed publicly or behind a misconfigured reverse proxy.

### 🟡 CORS Disabled by Default (No Wildcard Protection)
**File:** `web/api/middleware.go:30`
`CORS_ORIGIN` env var is empty by default, meaning no `Access-Control-Allow-Origin` header is set. For a single-origin SPA served by the same server this is fine. However, there is no enforcement that prevents setting `CORS_ORIGIN=*` accidentally, which would open all API routes.

### 🟡 Notification Secrets Stored in Plaintext SQLite
Telegram bot token, Bark device key, and Webhook URLs are stored in the `settings` table in plaintext. If the SQLite file is exfiltrated (e.g., via a path-traversal in a future endpoint), all notification credentials are exposed. No encryption at rest.

### 🟢 abogus Pool Uses JS String Interpolation (Mitigated)
**File:** `internal/douyin/abogus_pool.go:140`
```go
code := fmt.Sprintf("generate_a_bogus('%s', '%s')", safeQuery, safeUA)
```
`escapeJSString` only escapes `\`, `'`, `\n`, `\r`. Unicode control characters or null bytes in user-controlled input could still cause unexpected JS behavior. Low risk since the inputs come from internal URL construction, not user input.

---

## Performance Concerns

### 🟠 `GetStatsDetailed` Issues 7 Serial Queries
**File:** `internal/db/stats.go:83-92`
Seven individual `QueryRow` calls execute sequentially against SQLite. With `MaxOpenConns=1` they are serialized through a single connection. A single `SELECT` with `SUM(CASE …)` would do all of this in one round-trip and perform significantly better on large datasets.

### 🟠 PH Model Full Scan Blocks Up to ~33 Minutes
**File:** `internal/pornhub/client.go:411-455`
`maxPageHardLimit = 1000` pages × `pageDelay = 2s` = **up to 2000 seconds (~33 min)** of synchronous blocking inside the scheduler goroutine for a single Pornhub source sync. This stalls all other check logic in that goroutine.

### 🟡 `GetAllDownloads` Hard-Capped at 50 000 Rows
**File:** `internal/db/download.go:278`
The reconcile scanner loads up to 50 000 download records into a Go slice. On a large install this materializes 50k structs in memory at once. A cursor/batch approach would be more memory-efficient.

### 🟡 N+1 Pattern in `GetStatsDetailed` (Dashboard)
**File:** `internal/db/stats.go:83-92`
Same pattern as above: 7 separate `QueryRow` calls per dashboard page load, each acquiring the single SQLite connection in turn.

### 🟡 SSE Progress Ticker Fires Every Second Unconditionally
**File:** `web/api/events.go:76`
`time.NewTicker(1 * time.Second)` sends a `progress` SSE event every second even when there are no active downloads. With many concurrent browser tabs open, this creates unnecessary CPU and I/O load.

### 🟢 Double JSON Unmarshal in Bilibili `get()`
**File:** `internal/bilibili/client.go:526-530`
`body` is unmarshaled twice per API call (once into `result`, once inside `checkRateLimitCode`). Since `body` is already in memory this is a minor CPU cost, not a correctness issue.

---

## Reliability Concerns

### 🔴 `downloadOneChunkAttempt` Progress Double-Count on Retry
**File:** `internal/bilibili/chunked.go:261-271`
*(Status: listed as FIXED in BUG-AUDIT but the fix logic needs careful review)*
On retry, `atomic.AddInt64(totalDownloaded, -fi.Size())` subtracts the previous byte count. However, if the OS reclaims the partial file between the `os.Stat` and the `atomic.Add`, the subtraction is skipped but the value was already added in a previous attempt, leaving `totalDownloaded` overstated. Edge case, but can still cause progress >100% and potential downstream NaN.

### 🔴 Pornhub `goja` VM Timeout Only Interrupts JS, Not the Goroutine
**File:** `internal/pornhub/client.go` (P1-3 from BUG-AUDIT)
`time.AfterFunc(10s, vm.Interrupt)` sends an interrupt signal to goja but `RunString` remains synchronous. If the JS engine is executing a native Go bridge callback at interrupt time, the goroutine can block indefinitely, hanging the phscheduler worker permanently.

### 🟠 `abogusPool.sign` Blocks on an Empty Pool with No Timeout
**File:** `internal/douyin/abogus_pool.go:133`
```go
entry := <-ap.pool
```
If all 4 VMs are in use (e.g., concurrent sign requests), this blocks forever. If `replaceEntry` keeps failing, pool capacity degrades to 0 and the caller deadlocks. No `select` + timeout or context cancellation.

### 🟠 `ResetStaleDownloads` Requeues All `pending` on Restart
**File:** `internal/db/download.go:335-343`
After a restart, any pre-existing `pending` records (e.g., ones that were scheduled but never picked up) get requeued. If the queue had 1000 pending items before a crash, all 1000 are replayed at once, potentially causing a flood of API requests to Bilibili/Douyin and triggering rate limiting.

### 🟠 `DeleteSourceWithFiles` Has a File-Deleted-But-DB-Not-Deleted Window
**File:** `internal/db/source.go:196-218`
Files are deleted first (step 2), then DB records are deleted in a transaction (step 3). If the process crashes between step 2 and 3, the `source` record still exists in DB but files are gone. The `disabled` flag set in step 0 prevents re-scheduling, but the stale record is never cleaned up automatically.

### 🟡 Single SQLite Connection (`MaxOpenConns=1`) is a SPOF Under Load
**File:** `internal/db/db.go:151`
All DB operations (writes, reads, stats queries) serialize through one connection. Long-running read queries (e.g., `GetAllDownloads` with 50k rows) block all concurrent writes. While WAL mode helps for reads-during-writes, a large SELECT still holds a shared lock that can starve writers waiting on `busy_timeout(5000ms)`.

### 🟡 SSE `logCh` Close Terminates the Entire Event Stream
**File:** `web/api/events.go:96-99`
```go
case entry, ok := <-logCh:
    if !ok { return }
```
If the logger's broadcast channel is closed (e.g., on shutdown), all open SSE connections silently close. The client browser will auto-reconnect but users see a disconnection with no explanation.

### 🟡 No Context Cancellation on Pornhub Page Fetch
**File:** `internal/pornhub/client.go`
`http.Client` has a 30s timeout, but Pornhub's `GetModelVideos` loop across 1000 pages has no external context for cancellation. If `phscheduler` is stopped mid-loop, the loop cannot be interrupted until the current page fetch times out.

### 🟢 Notification Worker Silently Drops Events When Queue Full
**File:** `internal/notify/notify.go:151-154`
```go
select {
case n.jobCh <- job:
default:
    log.Printf("[notify] job queue full, dropping event=%s", event)
}
```
Queue size is 64. Under burst conditions (many downloads completing at once), notifications can be silently dropped. A missed `EventCookieExpired` or `EventDiskLow` could go unnoticed.

---

## Maintainability Concerns

### 🟠 `web/server.go` is 584 Lines — Overgrown God Object
`Server` has 20+ callback fields, duplicated `authMiddleware` / `isAuthWhitelist` functions that mirror identical logic in `web/api/middleware.go`. The two middleware implementations can drift. The callback-injection pattern (SetXxxFunc) makes the wiring in `setupRoutes()` extremely verbose and hard to audit.

### 🟠 `videos.go` HandleList Builds Raw SQL Strings
**File:** `web/api/videos.go:63-130`
Dynamic `WHERE` clause is built by string concatenation, which is correct now (all user values use `?`) but the pattern is fragile — one future maintainer adding a non-parameterized condition will introduce SQL injection. A query-builder abstraction or a comment warning would reduce this risk.

### 🟡 Migration System Has No Version Tracking
**File:** `internal/db/db.go:160-190`
Migrations are a flat list of `ALTER TABLE` statements run idempotently via "ignore duplicate column" errors. There is no migration version table, no rollback, and no way to know which migrations have actually run. Adding a migration out of order silently succeeds if the column already exists, masking schema drift.

### 🟡 `GetDownloadUploaders` Uses `fmt.Sprintf` to Embed `orderClause`
**File:** `internal/db/download.go:495-510`
`orderClause` is selected from a whitelist, so there is no injection risk today. But the mixed `fmt.Sprintf` + `?` parameterization style is inconsistent and a maintenance hazard.

### 🟡 Douyin `sign.js` and `abogus.js` Are Embedded Binaries With No Tests
**Files:** `internal/douyin/sign.js`, `internal/douyin/abogus.js`
These are the core anti-detection JS files embedded via `//go:embed`. There are no unit tests for the JS logic itself, and updating them requires manual verification. If TikTok/Douyin changes its signing algorithm, failures are silent until downloads start failing.

### 🟡 `sources.js` `load()` Missing `setLoading(true)`
**File:** `web/static/js/pages/sources.js:337-352`
Subsequent refreshes (after initial load) do not show a loading indicator. Users see stale data with no visual feedback during fetch. Listed in BUG-AUDIT P1-12.

### 🟢 `detectPlatform` Regex Over-Matches Pornhub
**File:** `web/static/js/pages/videos.js:453`
The second regex `/^[a-z0-9]{8,20}$/i` is too broad and can misidentify non-Pornhub IDs. Listed in BUG-AUDIT P1-8.

### 🟢 `sources.js` `handleImportFile` May Not Trigger in Firefox
**File:** `web/static/js/pages/sources.js:481-499`
`input.click()` without appending to the DOM first fails in some Firefox versions. Listed in BUG-AUDIT P2-12.

### 🟢 `videos.js` SSE `completed` Event Doesn't Refresh Full Record
**File:** `web/static/js/pages/videos.js:87`
After a download completes, `file_path`, `file_size`, and other fields are not updated until the user manually refreshes. Listed in BUG-AUDIT P1-7.

---

## Scalability Concerns

### 🟠 Single-Process, Single-DB Architecture
The application is designed as a single Go process with one SQLite database file. There is no horizontal scaling path — no shared DB, no message queue, no stateless worker model. Acceptable for personal use; becomes a hard constraint if multi-user or multi-host deployment is ever needed.

### 🟡 Download Queue is an In-Memory Buffered Channel (2000 cap)
**File:** `internal/config/constants.go:35`
If the process restarts, all in-flight queue entries are lost and must be rebuilt from the DB `pending` state. On restart with a large pending backlog, `ProcessAllPending` can enqueue up to 10 000 items at once (`GetDownloadsByStatus("pending", 10000)`), potentially overwhelming the queue buffer.

### 🟡 Log Ring Buffer is 5000 Entries with No Persistence
**File:** `internal/config/constants.go:38`
Logs are stored in a ring buffer in memory. On restart all recent log history is lost. For debugging production issues after a crash, this is a significant gap.

### 🟢 Rate Limiter State is In-Memory and Resets on Restart
**File:** `web/server.go:37-53`
The per-IP rate limiter uses `sync.Map` with no persistence. A restart clears all rate-limit windows. This is a minor concern for a personal tool, but worth noting.

---

## Known Bugs / TODOs

### From BUG-AUDIT.md (2026-03-25)

**P0 — Critical (may cause data loss or crash):**
| ID | File | Issue | Status |
|----|------|-------|--------|
| P0-1 | `bilibili/chunked.go:241-251` | Progress double-count on retry (overcounts `totalDownloaded`) | Fixed per audit |
| P0-2 | `db/source.go:139-145` | Non-transactional dual DELETE in `DeleteSource` | Fixed per audit |
| P0-3 | `bilibili/chunked.go:185-203` | Chunk file leak on merge failure | Fixed per audit |
| P0-4 | `db/download.go:395-404` | `GetDownloadsBySourceName` scan NULL `downloaded_at` → panic | Fixed per audit |

**P1 — Important (visible functional bugs):**
| ID | File | Issue | Status |
|----|------|-------|--------|
| P1-1 | `bilibili/client.go:482-512` | Rate-limit error not wrapped as `ErrRateLimited` | Fixed per audit |
| P1-2 | `bilibili/client.go:487` | `http.NewRequest` error ignored (potential nil-ptr panic on invalid URL) | Fixed per audit |
| P1-3 | `pornhub/client.go:596-604` | goja timeout only interrupts JS, goroutine may still block | **Open** |
| P1-4 | `pornhub/client.go:348-350` | Page delay and `maxPageHardLimit` are hardcoded | **Open** |
| P1-5 | `web/api/sources.go:233-273` | `dyClient.Close()` panic-safety uncertain | **Open** |
| P1-6 | `web/api/sources.go:56` | `GetSourcesStats` error silently dropped | Fixed per audit |
| P1-7 | `db/download.go:309-316` | `ResetStaleDownloads` comment/code mismatch | Fixed per audit |
| P1-8 | `web/static/js/pages/videos.js:87` | SSE `completed` event does not refresh full record fields | **Open** |
| P1-9 | `web/static/js/pages/videos.js:453` | `detectPlatform` regex over-matches pornhub | **Open** |
| P1-10| `web/static/js/pages/sources.js:337` | `load()` missing `setLoading(true)` on refresh | **Open** |

**P2 — Minor (code quality, edge cases):**
- `pornhub/client.go:151-154`: `GetModelInfo` URL trimming may over-strip names ending in "videos"
- `db/db.go:179-181`: Migration loop swallows errors (now logged but still continues)
- `web/api/sources.go:328-331`: 4 separate `QueryRow` calls in `HandleGet` with ignored scan errors
- `web/static/js/pages/sources.js:481-499`: `handleImportFile` `input.click()` without DOM append (Firefox bug)

### From TODO.md (current)
- API interaction issues (未列举具体接口 — vague, needs triage)
- UP主删除弹窗 uses `window.confirm` (should be custom Dialog)
- Dashboard empty state: no placeholder when `recent_downloads` is empty
- `retryOneDownload` at top-level `scheduler/retry.go` should add `src.Enabled` defensive check (lower priority — sub-schedulers already check)

---

## Dependencies Risk

### Go Dependencies

| Dependency | Version | Risk |
|---|---|---|
| `github.com/dop251/goja` | `v0.0.0-20260311135729` | 🟡 Pre-release / date-versioned. Used for JS signing (a_bogus, sign.js, PH JS eval). Breaking changes are possible in any update. Tightly coupled to embedded JS files. |
| `github.com/glebarez/sqlite` | `v1.10.0` | 🟢 Pure-Go SQLite wrapper. Stable. No CGO dependency is intentional. |
| `modernc.org/sqlite` | `v1.23.1` | 🟡 Underlying pure-Go SQLite. Lags behind upstream SQLite (currently 3.44 era); security patches to SQLite may take time to appear here. |
| `golang.org/x/net` | `v0.52.0` | 🟢 Current. Used for HTML parsing (Pornhub scraper). |
| `github.com/robfig/cron/v3` | `v3.0.1` | 🟢 Stable, widely used. |
| `gorm.io/gorm` | `v1.25.5` | 🟡 Listed as indirect — likely a transitive dep from glebarez/sqlite. The app uses raw `database/sql` (not GORM), so GORM is dead weight adding ~2 MB to the binary. |

### Runtime Dependencies

| Dependency | Risk |
|---|---|
| `ffmpeg` (Docker: alpine apk) | 🟠 Alpine's ffmpeg package version is not pinned in the Dockerfile. A future Alpine 3.19 package update could bring a different ffmpeg version, potentially changing muxing behavior for DASH segments. |
| `golang:1.25` (builder image) | 🟡 `go 1.25.0` in `go.mod` — Go 1.25 was not GA as of audit date (2026-03-25). Using a pre-release Go may expose build-time bugs or be unavailable in some CI environments. README says "Go 1.22" which contradicts go.mod. |

### External API / Scraper Risk

| Platform | Risk |
|---|---|
| Bilibili | 🟠 WBI signature (`internal/bilibili/wbi.go`) and dynamic API formats are undocumented and change without notice. Any Bilibili app update can break video list fetching silently. |
| Douyin (TikTok CN) | 🔴 **Highest fragility.** X-Bogus and a_bogus signing rely on reverse-engineered JS embedded in the binary. TikTok/Douyin rotates these algorithms regularly. When broken, all Douyin subscriptions stop fetching silently. No automated detection of algorithm breakage. |
| Pornhub | 🟠 HTML scraping of `flashvars_*` JS variables for video URLs. PH changes their player structure periodically. Multiple fallback patterns (`flashvarsVarPatterns`) help, but page structure changes require manual code updates. |
