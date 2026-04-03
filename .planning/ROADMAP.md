# Roadmap — VSD v3.0

> Source of truth for phased execution. Each phase is atomic and independently mergeable.
> Derived from: `REQUIREMENTS.md`, `BUG-AUDIT.md`, `CONCERNS.md`, `TODO.md`.

---

## Milestone 1: Stability & Security

**Goal:** Fix all Critical/High reliability and security issues. System runs without hanging or
unauthenticated exposure.

**Success criteria:** All REQ-SEC-* and REQ-REL-* requirements met; no known goroutine hangs;
`ensureAuthToken()` is active on every fresh deployment; all Milestone 1 code has unit tests.

---

### Phase 1.1 — Auth Hardening ✅ Complete (2026-04-02)

**Goal:** Ensure every VSD deployment starts with authentication enabled by default, and that the
long-lived auth token is never exposed in WebSocket URLs or server logs.

**Requirements:** REQ-SEC-1, REQ-SEC-2

**Deliverables:**

- `web/server.go` — re-enable the `s.ensureAuthToken()` call in `Server.Start()` (currently
  commented out as `// s.ensureAuthToken() // auth disabled`)
- `web/server.go · ensureAuthToken()` — function already auto-generates a cryptographically random
  32-hex-char token via `crypto/rand`; confirm it prints the token to stdout and persists it to the
  `settings` table on first run (logic is already correct; just needs to be called)
- `web/server.go · authMiddleware()` — remove the `?token=xxx` query-param auth branch (currently
  the third fallback in `authMiddleware`); replace WebSocket auth with a short-lived session token:
  - New endpoint `POST /api/session` — exchanges the long-lived token for a 60s session nonce
    stored in-process (`sync.Map`)
  - `web/api/events.go · HandleWSLogs()` — validate the `?session=<nonce>` param against the
    in-process map instead of comparing against the raw auth token
  - Nonce is single-use and expires after 60 seconds; never logged
- `web/static/js/` — update the WS connection setup in the frontend to call
  `/api/session` before opening `ws://…/api/ws/logs`, passing the returned nonce
- Tests: unit test for `ensureAuthToken()` (first-run token generation, idempotent re-run,
  `NO_AUTH=1` bypass); unit test for session nonce expiry

**Estimated effort:** 1 day

---

### Phase 1.2 — PH Scheduler Reliability ✅ Complete (2026-04-02)

**Goal:** Pornhub scheduler goroutine never hangs permanently; page scans are cancellable when
the scheduler is stopped.

**Requirements:** REQ-REL-1, REQ-REL-2

**Deliverables:**

- `internal/pornhub/client.go · GetVideoURL()` — the goja eval timeout (`select … case <-time.After(10 * time.Second)`) is already implemented; **increase timeout to 15s** to match REQ-REL-1 acceptance criterion and add a structured log warning on timeout (currently only returns an error)
- `internal/pornhub/client.go · GetModelVideos()` — add `context.Context` as first parameter;
  check `ctx.Done()` at the top of each loop iteration (between page fetches) and after
  `time.Sleep(pageDelay)` so that scheduler cancellation propagates into the page loop within one
  `pageDelay` (2s) + HTTP timeout (30s) window
- `internal/scheduler/phscheduler/check.go · CheckPHModel()` — thread the scheduler's root
  context through to `client.GetModelVideos(ctx, src.URL)` so stopping `PHScheduler` cancels an
  in-progress full scan
- `internal/scheduler/phscheduler/check.go · FullScanPHModel()` — same context threading at
  line 202 (`videos, err := client.GetModelVideos(src.URL)`)
- `internal/pornhub/client.go` — extract `pageDelay` and `maxPageHardLimit` as package-level
  `const` (already done for `pageDelay`); expose them via an optional `ClientOptions` struct so
  callers can override for tests
- Tests: unit test for JS eval timeout path in `GetVideoURL`; unit test that cancelling a context
  mid-scan causes `GetModelVideos` to return `ctx.Err()` promptly

**Estimated effort:** 1 day

---

### Phase 1.3 — Performance & Resilience ✅ Complete (2026-04-03)

**Goal:** Dashboard stats load in a single DB round-trip; Douyin `a_bogus` signing never deadlocks
under pool exhaustion.

**Requirements:** REQ-PERF-1, REQ-REL-3

**Deliverables:**

- `internal/db/stats.go · GetStatsDetailed()` — replace the 7 separate `QueryRow` calls with a
  single `SELECT … SUM(CASE WHEN status IN ('completed','relocated') THEN 1 ELSE 0 END) AS completed, …`
  query plus one `SELECT COUNT(*) FROM sources WHERE enabled = 1`; total DB queries for
  `HandleDashboard` drops from 8 to 2
- `internal/douyin/abogus_pool.go · abogusPool.sign()` — the current implementation does a bare
  channel receive `entry := <-ap.pool` with no timeout; mirror the pattern from
  `sign_pool.go · signPool.sign()` which already uses `context.WithTimeout` + `select`; add a
  10s timeout identical to `signPoolGetTimeout`
- Tests: `BenchmarkGetStatsDetailed` in `internal/db/db_test.go` comparing single-query vs.
  multi-query variants; unit test for `abogusPool.sign()` under a fully drained pool (all entries
  withheld) asserting an error is returned within 10s

**Estimated effort:** 0.5 day

---

## Milestone 2: Quality & UX

**Goal:** Polish the user experience, close observability gaps, raise test coverage to agreed
floors, and make the migration system production-safe.

**Success criteria:** All REQ-UI-*, REQ-OBS-*, REQ-TEST-*, REQ-MAINT-* requirements met;
Prometheus endpoint includes per-platform counters; no `window.confirm` calls remain; migration
errors surface at WARN level.

---

### Phase 2.1 — Frontend Bug Fixes ✅ Complete (2026-04-03)

**Goal:** Eliminate the three open P1 frontend bugs and ship two UX improvements from the TODO
backlog.

**Requirements:** REQ-UI-1, REQ-UI-2

**Deliverables:**

- `web/static/js/pages/videos.js:87` **(P1-8)** — on SSE `download_event` with `type=completed`,
  call `GET /api/videos/:id` to refresh the full record (file_path, file_size, downloaded_at,
  thumb_path) instead of only updating the status field in-place
- `web/static/js/pages/videos.js:453` **(P1-9)** — narrow the `detectPlatform` second regex
  (`/^[a-z0-9]{8,20}$/i`) so it only matches confirmed Pornhub viewkey patterns (e.g. require at
  least one digit and enforce the `ph` prefix or `viewkey=` URL form); add at least 5 negative
  test cases in a JS unit test or comment block
- `web/static/js/pages/sources.js:337` **(P1-10)** — add `setLoading(true)` at the top of the
  `load()` function body so the spinner appears immediately on manual refresh
- `web/static/js/pages/dashboard.js` **(REQ-UI-2)** — add an empty-state placeholder element
  (e.g. `<div class="empty-state">暂无下载记录</div>`) rendered when `recent_downloads.length === 0`
- `web/static/js/pages/uploaders.js:91` **(REQ-UI-2)** — replace `window.confirm(…)` delete
  confirmation with a custom Dialog component already in use elsewhere in the SPA; ensure the
  dialog is dismissible via keyboard (Esc) and accessible

**Estimated effort:** 1 day

---

### Phase 2.2 — Observability

**Goal:** Expose per-platform download counters and scheduler timestamps in the existing Prometheus
endpoint so operators can build dashboards and alerts without parsing application logs.

**Requirements:** REQ-OBS-1

**Deliverables:**

- `internal/downloader/stats.go` — add `SuccessCount`, `FailureCount` fields broken down by
  platform label (`bilibili`, `douyin`, `pornhub`) to `downloader.Stats`; increment atomically in
  `downloader.go` on job completion/failure using the `source_type` field already carried by `Job`
- `web/api/metrics.go · MetricsHandler` — inject a callback or direct ref to the per-platform
  counter map; surface the following new Prometheus metrics in `HandlePrometheus()`:
  - `vsd_downloads_completed_total{platform="..."}` (counter)
  - `vsd_downloads_failed_total{platform="..."}` (counter)
  - `vsd_scheduler_last_check_timestamp{platform="..."}` (gauge, Unix seconds)
- `web/api/router.go` — wire the scheduler last-check getters into `MetricsHandler` via new setter
  `SetSchedulerLastCheckFunc(platform string, fn func() time.Time)`
- `internal/scheduler/phscheduler/scheduler.go`, `bscheduler`, `dscheduler` — expose a
  `LastCheckAt() time.Time` method on each scheduler so the metrics handler can read them
- Tests: HTTP test for `GET /api/metrics/prometheus` asserting the new label-keyed metric lines
  are present in the response body

**Estimated effort:** 1 day

---

### Phase 2.3 — Test Coverage

**Goal:** Bring the three under-covered packages to their agreed floors; all Milestone 1 code
already covered.

**Requirements:** REQ-TEST-1

**Deliverables:**

- `internal/logger/logger_test.go` *(new file)* — cover `Init`, `Default`, `Writer.Write`,
  `Subscribe`/`Unsubscribe`, `GetLogs`, `Clear`, `MarshalEntry`, ring-buffer wrap-around; target
  ≥ 60% statement coverage
- `internal/downloader/downloader_test.go` — extend existing tests to cover: `Pause`/`Resume`
  lifecycle, job cancellation via `rootCtx`, `IsPaused`, per-platform counter increments added in
  Phase 2.2, worker error path; target ≥ 50% statement coverage
- `web/api/api_test.go` — extend existing handler tests to cover: `HandleDashboard` (stats
  present in JSON), `HandlePrometheus` (format and labels), `HandleWSLogs` (handshake rejection on
  non-WS request), `HandleEvents` SSE headers; target ≥ 60% statement coverage
- CI gate: add `go test -coverprofile=coverage.out ./...` step with `go tool cover -func` output;
  fail if any of the three packages drops below floor

**Estimated effort:** 1.5 days

---

### Phase 2.4 — Migration Hardening

**Goal:** Make the SQLite migration system safe to extend: track applied migrations, distinguish
ignorable from real errors, and prevent re-application of already-applied statements.

**Requirements:** REQ-MAINT-1

**Deliverables:**

- `internal/db/db.go` — add `schema_migrations` table creation to `schema`:
  ```sql
  CREATE TABLE IF NOT EXISTS schema_migrations (
      id      INTEGER PRIMARY KEY AUTOINCREMENT,
      hash    TEXT    NOT NULL UNIQUE,
      sql     TEXT    NOT NULL,
      applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
  );
  ```
- `internal/db/db.go · Init()` — replace the raw `migrations` slice loop with a helper
  `applyMigration(db *sql.DB, sql string)` that:
  1. Computes `SHA1(sql)` as the migration hash
  2. Checks `schema_migrations` — skips if hash already present (idempotent)
  3. Executes the SQL; on error: if the message contains `"duplicate column"` log nothing
     (expected on existing DBs); otherwise log at `WARN` level with the failing SQL
  4. Inserts the hash + SQL into `schema_migrations` on success
- `internal/db/db_test.go` — add tests: migration applied once (hash present after), migration
  skipped on re-run (no duplicate error), real error (non-duplicate-column) is returned/logged
- No changes to existing migration SQL strings — only the execution mechanism changes

**Estimated effort:** 0.5 day

---

## Backlog (Out of Scope for v3.0)

The following items are recorded here to prevent re-discussion; they are deliberately deferred.

| Item | Reason deferred |
|------|----------------|
| Multi-user / multi-host deployment | Requires auth model rewrite; not needed for single-user self-hosted use case |
| YouTube / Twitter(X) / Instagram support | New platform integration work; separate milestone |
| Horizontal scaling | SQLite single-writer model makes this non-trivial; deferred until usage demands it |
| Notification secret encryption at rest | Low threat model for local deployment; can be added if remote Bark/Telegram secrets are a concern |
| SSE progress ticker rate-limiting (suppress when no active downloads) | `🟡` concern from CONCERNS.md; reduces CPU but is a minor optimization; revisit in v3.1 |
| `handleImportFile` Firefox `input.click()` DOM-append bug (`sources.js:481`) | `🟢` low severity; Firefox-specific; deferred to v3.1 |
| `scheduler/retry.go · retryOneDownload` missing top-level `src.Enabled` guard | Defensive improvement; sub-schedulers already check; low risk |
| SSE `logCh` close silently terminates event stream | `🟡` concern; requires logger lifecycle changes; v3.1 |
