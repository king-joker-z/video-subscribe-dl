# Requirements — VSD v3.0

> Derived from: BUG-AUDIT.md, CONCERNS.md, TODO.md, user priorities.
> Scope: Milestone 1 (Stability) + Milestone 2 (Quality & UX)

---

## Milestone 1: Stability & Security

### REQ-SEC-1: Auth Auto-Initialization
- `ensureAuthToken()` must be re-enabled or replaced
- On first run with no `AUTH_TOKEN` env var and no DB token, auto-generate a secure random token
- Token must be displayed to user on startup (log output)
- Acceptance: Fresh Docker container starts with auth protection enabled by default

### REQ-SEC-2: WS Token Not in Query Params
- WebSocket auth via `?token=xxx` must be replaced with a short-lived session token or cookie
- Token must not appear in server logs or browser history
- Acceptance: `ws/logs` connection does not expose long-lived token in URL

### REQ-REL-1: PH goja Goroutine Hang Fix
- Pornhub JS evaluation must not permanently block the phscheduler goroutine
- JS execution must run in separate goroutine with channel + select + timeout
- On timeout: return error, log warning, scheduler continues
- Acceptance: `internal/pornhub/client.go` JS eval completes or times out within 15s; goroutine never hangs indefinitely

### REQ-REL-2: PH Scan Non-Blocking
- `GetModelVideos` must not block the scheduler goroutine for >33 minutes
- Use context-based cancellation so phscheduler stop propagates into the page fetch loop
- Page delay and maxPageHardLimit should be configurable (or at minimum not hardcoded)
- Acceptance: Stopping phscheduler cancels any in-progress PH scan within 1 page fetch timeout (30s)

### REQ-PERF-1: Dashboard Stats Single Query
- `GetStatsDetailed` must use a single `SELECT SUM(CASE WHEN...)` query instead of 7 separate QueryRow calls
- Acceptance: Dashboard page load issues ≤ 2 DB queries for stats

### REQ-REL-3: abogus Pool Timeout
- `abogusPool.sign()` must not block indefinitely when pool is drained
- Add `select` with context/timeout (e.g., 10s) and return error on timeout
- Acceptance: Under pool exhaustion, callers receive error within 10s

---

## Milestone 2: Quality & UX

### REQ-UI-1: Open P1 Frontend Bugs Fixed
- P1-8: SSE `completed` event must refresh full record fields (file_path, file_size, etc.)
- P1-9: `detectPlatform` regex must not over-match pornhub IDs
- P1-10: `sources.js load()` must set `setLoading(true)` on refresh

### REQ-UI-2: TODO.md UX Items
- Dashboard empty state: add placeholder when `recent_downloads` is empty
- UP主删除弹窗: replace `window.confirm` with custom Dialog component

### REQ-OBS-1: Metrics & Observability
- Prometheus endpoint (`/api/metrics/prometheus`) surfaces key counters:
  - Download success/failure counts per platform
  - Active download count
  - Queue depth
  - Scheduler last-check timestamps per platform

### REQ-TEST-1: Coverage Uplift
- `internal/logger`: achieve ≥ 60% coverage (currently 0%)
- `internal/downloader`: achieve ≥ 50% coverage (currently ~20%)
- `web/api` handlers: achieve ≥ 60% coverage (currently ~40%)
- All new code in Milestone 1 must have unit tests

### REQ-MAINT-1: Migration Version Tracking
- Add a `schema_migrations` table tracking applied migration hashes
- Distinguish "column already exists" (ignorable) from real migration errors (must log as warning)
- Acceptance: Migration errors other than duplicate-column are logged at WARN level

---

## Out of Scope (v3.0)
- Multi-user / multi-host deployment
- New platform support (YouTube, Twitter/X, Instagram) — backlog
- Horizontal scaling
- Notification secret encryption at rest
