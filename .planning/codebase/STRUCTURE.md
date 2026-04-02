# Directory Structure

## Root Layout

```
video-subscribe-dl/
├── cmd/server/          # Application entry point
├── internal/            # Private Go packages (business logic)
├── web/                 # HTTP server, API handlers, embedded assets
├── frontend/            # Vite/React dev build (separate from embedded static)
├── tests/               # Integration tests
├── Dockerfile           # Multi-stage Docker build
├── docker-compose.yml   # Docker Compose config
├── go.mod / go.sum      # Go module definition
└── vsd                  # Compiled binary (local dev artifact)
```

## Key Directories

### `cmd/server/`
Single file `main.go` — the composition root. Parses flags (`--data-dir`, `--download-dir`, `--port`), initialises all components in dependency order, wires callbacks between `web.Server` and `scheduler.Scheduler`, and manages graceful shutdown.

### `internal/bilibili/`
Bilibili platform integration.
- `client.go` — HTTP client, cookie/credential management, WBI-signed requests
- `credential.go` — `Credential` struct, JSON serialisation, cookie-string conversion, RSA key operations for token refresh
- `refresh.go` — Access token refresh flow (uses `ac_time_value`)
- `qrcode.go` — QR code login generation and polling
- `dash.go` — DASH stream resolution (video+audio URL selection by quality/codec), ffmpeg merge
- `dash_progress.go` — Per-phase progress reporting during DASH download
- `chunked.go` — Parallel chunked HTTP range download for large files
- `cdn.go` — CDN URL fallback/retry logic
- `wbi.go` — WBI request signing (mixin key derivation + parameter encoding)
- `analyzer.go` — Video metadata parsing from API responses
- `subtitle.go` / `danmaku.go` (via `internal/danmaku`) — Subtitle and danmaku download
- `ratelimit.go` / `semaphore.go` — Token-bucket rate limiter and concurrency semaphore
- `verify.go` — Cookie/credential validity check
- `error.go` — Platform-specific error types

### `internal/douyin/`
Douyin (TikTok CN) platform integration.
- `client.go` — HTTP client, UA rotation, Client Hints header injection
- `types.go` — API response types (`DouyinVideo`, `UserVideosResult`, etc.)
- `endpoints.go` — API endpoint constants and URL construction
- `request_params.go` — Common request parameter assembly
- `sign_pool.go` / `sign.js` — goja JS VM pool for TT_WID token signing
- `abogus_pool.go` / `abogus.js` — goja JS VM pool for a-bogus anti-bot parameter
- `fingerprint.go` — Browser fingerprint header generation
- `sign_updater.go` — Hot-swap remote signing scripts; auto-update every 6 hours
- `download.go` — Video URL resolution and download to disk
- `cookie.go` / `cookie_validator.go` — Cookie parsing and validity checking
- `ratelimit.go` — Per-client request rate limiting
- `diag.go` — Connectivity diagnostics endpoint helper
- `stats.go` / `logger.go` — Request statistics and structured logging

### `internal/pornhub/`
Pornhub platform integration.
- `client.go` — HTTP client with cookie support, model video listing, video URL resolution
- `types.go` — API/scraping response types
- `sanitize.go` — Filename sanitisation for PH video titles
- `ratelimit.go` — Rate limiting
- `error.go` — Platform error types

### `internal/scheduler/`
Scheduling orchestration.
- `scheduler.go` — Top-level `Scheduler`: lifecycle (Start/Stop), `checkAll`, `checkSourceList`, `ProcessAllPending`, retry queue worker, platform dispatch
- `platform.go` — `PlatformScheduler` interface
- `retry.go` — Exponential backoff retry logic shared across platforms
- `startup_cleanup.go` — Pre-start stale record cleanup
- `cleanup.go` — Periodic orphan/stale download cleanup

  **`bscheduler/`** — Bilibili sub-scheduler
  - `scheduler.go` — `BiliScheduler` struct and state
  - `lifecycle.go` — Startup, Stop, config watcher management
  - `check_up.go` — UP主 video list scanning
  - `check_season.go` — 合集 (season) scanning
  - `check_series.go` — 系列 scanning
  - `check_favorite.go` — 收藏夹 scanning
  - `check_watchlater.go` — 稍后看 scanning
  - `process.go` — Download record creation, dedup, filter application
  - `retry.go` — Bilibili-specific retry submission
  - `cooldown.go` — Risk-control cooldown state management
  - `cookie.go` — Cookie/credential hot-update
  - `startup_cleanup.go` — Pre-start Bilibili cleanup tasks
  - `doc.go` — Package documentation

  **`dscheduler/`** — Douyin sub-scheduler
  - `scheduler.go` — `DouyinScheduler` struct and state
  - `check.go` — User video and mix scanning
  - `process.go` — Download record creation and dedup
  - `progress.go` — Progress tracking and SSE event emission
  - `pause.go` — Pause/resume control
  - `cooldown.go` — Risk-control cooldown
  - `cookie.go` — Cookie hot-update and validation
  - `api.go` — Public API surface

  **`phscheduler/`** — Pornhub sub-scheduler
  - `scheduler.go` — `PHScheduler` struct and state
  - `check.go` — Model video scanning
  - `process.go` — Download record creation
  - `progress.go` — Progress tracking
  - `pause.go` / `cooldown.go` / `cookie.go` — Same patterns as dscheduler

### `internal/db/`
SQLite persistence layer.
- `db.go` — Schema definition, `Init()`, `Source` and `Download` types, connection config
- `download.go` — Download CRUD, status transitions, retry queries
- `source.go` — Source CRUD, due-check queries
- `settings.go` — Key-value settings store
- `people.go` — UP主/creator info (mid, name, avatar)
- `stats.go` — Aggregate stats queries (for dashboard)
- `cleanup.go` — Stale record cleanup queries

### `internal/downloader/`
- `downloader.go` — Worker pool, job queue, pause/resume, rate limiting, chunked download, ffmpeg merge dispatch, progress map, SSE event emission
- `stats.go` — Download throughput statistics

### `internal/scanner/`
- `scanner.go` — Walks download dir, reads `.info.json` sidecars, back-fills DB
- `reconcile.go` — Compares DB records against filesystem, returns `ReconcileResult` (orphan files, missing files, stale downloading)

### `internal/filter/`
- `filter.go` — Simple (keyword/regex) and advanced (JSON rule) video filtering; `MatchesSimple()` and `MatchesRules()` functions

### `internal/config/`
- `constants.go` — All compile-time defaults (intervals, queue size, buffer sizes, worker counts)
- `watcher.go` — `HotConfig` struct (RW-mutex, `onChange` callbacks), `ConfigWatcher` polling loop
- `template.go` — Filename template rendering (title, uploader, date substitutions)

### `internal/notify/`
- `notify.go` — `Notifier` struct, event types, Webhook/Telegram/Bark dispatch, background job worker

### `internal/logger/`
- `logger.go` — Ring-buffer logger (capacity 5000). `io.Writer` interface for `log` and `slog` sinks; exposes buffered entries for SSE log streaming

### `internal/nfo/`
- `nfo.go` — NFO sidecar file generation (Kodi-compatible XML)

### `internal/util/`
- `disk.go` — Available/total disk space query
- `filecleanup.go` — Safe file deletion helpers
- `ratelimit.go` — Generic token-bucket rate limiter

### `web/`
- `server.go` — `Server` struct, middleware (rate limit, auth), `Start`/`Shutdown`, health endpoint, JSON helpers
- `router.go` — Calls `api.Router.Register()` and registers `/static/` and `/` routes
- `embed.go` — `//go:embed` declarations for `static/` and `templates/`

  **`web/api/`** — One file per handler domain:
  | File | Handler | Routes |
  |------|---------|--------|
  | `dashboard.go` | `DashboardHandler` | `GET /api/dashboard` |
  | `sources_crud.go` | `SourcesHandler` | `GET/POST /api/sources`, `GET/PUT/DELETE /api/sources/:id` |
  | `sources_parse.go` | — | `POST /api/sources/parse` |
  | `sources_sync.go` | — | trigger source sync |
  | `sources_export.go` | — | `GET /api/sources/export`, `POST /api/sources/import` |
  | `videos.go` | `VideosHandler` | `GET /api/videos`, `GET/DELETE /api/videos/:id`, `/api/thumb/` |
  | `uploaders.go` | `UploadersHandler` | `/api/uploaders`, `/api/avatar/` |
  | `task.go` | `TaskHandler` | `/api/task/status`, `trigger`, `pause`, `resume`, `version` |
  | `settings.go` | `SettingsHandler` | `GET/PUT /api/settings`, `preview-template`, token login |
  | `credential.go` | `CredentialHandler` | `/api/credential`, QR code login |
  | `events.go` | `EventsHandler` | `GET /api/events` (SSE), `/api/logs`, `/api/ws/logs` |
  | `me.go` | `MeHandler` | `/api/me/favorites`, `uppers`, `subscribe` |
  | `quickdl.go` | `QuickDownloadHandler` | `POST /api/download`, `/api/download/preview` |
  | `quickdl_douyin.go` | — | Douyin quick-download helpers |
  | `stream.go` | `StreamHandler` | `GET /api/stream/:id` (video playback) |
  | `search.go` | `SearchHandler` | `GET /api/search` |
  | `metrics.go` | `MetricsHandler` | `/api/metrics`, `/api/metrics/prometheus` |
  | `notify.go` | `NotifyHandler` | `/api/notify/test`, `/api/notify/status` |
  | `diag.go` | `DiagHandler` | `/api/diag/bili`, `/api/diag/douyin` |
  | `douyin_cookie.go` | `DouyinCookieHandler` | `/api/douyin/cookie/validate`, `status` |
  | `douyin_status.go` | `DouyinStatusHandler` | `/api/douyin/status`, `resume`, `pause` |
  | `ph_cookie.go` | `PHCookieHandler` | `POST/DELETE /api/ph/cookie` |
  | `ph_status.go` | `PHStatusHandler` | `/api/ph/status`, `resume`, `pause` |
  | `sign_reload.go` | `SignReloadHandler` | `POST /api/sign/reload` |
  | `middleware.go` | — | `apiOK`, `apiError`, response helpers |
  | `response.go` | — | Unified response envelope types |
  | `pagination.go` | — | Pagination parameter parsing |
  | `httpclient.go` | — | Shared HTTP client for API handlers |

### `web/static/`
Embedded static assets served at `/static/`.
- `js/app.js` — React SPA entry, global SSE singleton, router
- `js/api.js` — Typed API client (`fetch` wrappers for every endpoint)
- `js/pages/` — Page components: `dashboard.js`, `sources.js`, `videos.js`, `uploaders.js`, `settings.js`, `logs.js`
- `js/components/` — Shared UI: `quick-download.js`, `video-detail.js`, `command-palette.js`, `utils.js`
- `icon-192.png/svg`, `icon-512.png/svg` — PWA icons
- `manifest.json` — PWA web app manifest

### `web/templates/`
- `index.html` — Single Go template; shell page that loads the React SPA via importmap

### `frontend/`
Vite + React development workspace (separate from the embedded static). Used for hot-reload during development; built output must be copied to `web/static/` for embedding.

### `tests/`
- `integration_test.go` — End-to-end integration tests against a running server instance

## Important Files

| File | Purpose |
|------|---------|
| `cmd/server/main.go` | Application entry point; component wiring and lifecycle |
| `internal/db/db.go` | SQLite schema, `DB` type, migrations |
| `internal/scheduler/scheduler.go` | Top-level scheduler; platform dispatch and lifecycle |
| `internal/downloader/downloader.go` | Download worker pool; core download logic |
| `web/server.go` | HTTP server, middleware, callback registry |
| `web/api/router.go` | All API route registrations |
| `web/embed.go` | Embeds static assets into binary |
| `internal/config/constants.go` | All tunable default values |
| `internal/bilibili/wbi.go` | WBI signing (required for all Bilibili API calls) |
| `internal/douyin/sign_pool.go` | JS VM pool for Douyin request signing |
| `Dockerfile` | Multi-stage build: Go builder + Alpine runtime with ffmpeg |

## Module Organization

The module is `video-subscribe-dl` (Go 1.25). Code is organised into three top-level namespaces:

- **`cmd/`** — Executable entry points (only `server`)
- **`internal/`** — Private packages, each with a single responsibility:
  - Platform clients: `bilibili`, `douyin`, `pornhub`
  - Scheduling: `scheduler` (top-level) + `scheduler/bscheduler`, `scheduler/dscheduler`, `scheduler/phscheduler`
  - Data: `db`
  - Cross-cutting: `config`, `filter`, `logger`, `notify`, `nfo`, `scanner`, `downloader`, `util`
- **`web/`** — HTTP server and all API handlers (`web/api/`); frontend assets embedded at build time

Key dependency rules:
- `web` depends on `internal/*` but not vice versa
- `scheduler` sub-packages (`bscheduler`, `dscheduler`, `phscheduler`) depend on their platform client packages but not on each other
- `filter` is a leaf package shared by `bscheduler` and `dscheduler` without circular imports
- `downloader` depends only on `bilibili` (for DASH download) and `config`/`danmaku`; it is not aware of other platforms
