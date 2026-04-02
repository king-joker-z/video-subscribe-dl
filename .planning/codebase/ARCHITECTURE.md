# Architecture

## System Overview

Video Subscribe DL (VSD) is a self-hosted video subscription and download manager. It monitors user-defined sources (Bilibili UP主, 收藏夹, 合集; Douyin users/合集; Pornhub models) and automatically downloads new videos. The system runs as a single Go binary with an embedded web UI, backed by a local SQLite database and the local filesystem for video storage.

The design is a monolith with clear internal layering: an HTTP API server (web tier), a platform-aware scheduler (orchestration tier), per-platform API clients (integration tier), a shared downloader worker pool (download tier), and a SQLite-backed persistence layer (data tier). Real-time progress is pushed to the browser via SSE and WebSocket.

## Components

### `cmd/server` — Entry Point
Bootstraps all components in order: logger → DB → platform clients → downloader → scanner → scheduler → web server. Wires cross-component callbacks (e.g., scheduler exposing `CheckNow`, `UpdateCredential`) into the web server before `Start()` is called. Handles graceful shutdown (scheduler → downloader → HTTP → DB).

### `web` — HTTP Server & Static Assets
- **`Server`** (`web/server.go`): `net/http` mux with per-IP sliding-window rate limiting (default 200 req/min) and optional Bearer/Cookie/query-param auth. Serves embedded static files and the single HTML template (`index.html`).
- **`api.Router`** (`web/api/router.go`): Registers ~40 REST endpoints grouped by domain (dashboard, sources, videos, uploaders, task, settings, credential, events, search, metrics, diagnostics, douyin/bili/ph controls). Each domain has a dedicated handler struct.
- **SSE/WebSocket** (`web/api/events.go`): `GET /api/events` streams download progress, completion events, and log lines. `GET /api/ws/logs` provides a raw WebSocket log feed.

### `internal/scheduler` — Scheduling Orchestrator
- **`Scheduler`** (top-level): Manages lifecycle, cron/interval ticking, cross-platform pending processing, and retry queue. Dispatches `checkSource()` calls to the correct sub-scheduler by source type.
- **`bscheduler.BiliScheduler`**: All Bilibili-specific logic — cookie verification, credential refresh, UP主/合集/收藏夹/稍后看 fetching, cooldown/backoff, WBI signing, config hot-reload.
- **`dscheduler.DouyinScheduler`**: Douyin-specific logic — user video pagination, 合集 (mix) scanning, cookie management, pause/resume, independent progress event channel.
- **`phscheduler.PHScheduler`**: Pornhub model scanning, cookie management, thumbnail repair, pause/resume, independent event channel.

### `internal/downloader` — Download Worker Pool
Concurrent worker pool (`MaxConcurrent` goroutines, default 1). Accepts `Job` structs from the scheduler; executes DASH video+audio download + ffmpeg merge for Bilibili, or direct MP4 download for Douyin/PH. Supports pause/resume, per-job progress tracking, configurable rate limiting (bytes/sec), chunked parallel download for large files (>50MB), and SSE event emission on completion/failure.

### `internal/bilibili` — Bilibili API Client
Handles WBI-signed API requests, DASH stream resolution, credential management (SESSDATA/bili_jct/buvid3 + `ac_time_value` refresh token), QR-code login, CDN chunk download, danmaku/subtitle fetching, rate-limited HTTP with exponential backoff on `-352`/`-412` risk-control errors.

### `internal/douyin` — Douyin API Client
Manages anti-bot signing: embeds `sign.js` (TT_WID token generation) and `abogus.js` (a-bogus parameter), runs both in goja JavaScript VM pools, generates browser-fingerprint headers (sec-ch-ua, Client Hints). Supports hot-swappable remote script updates via `SignUpdater`.

### `internal/pornhub` — Pornhub API Client
HTTP client with cookie injection, model video listing, direct video URL resolution, thumbnail download, rate limiting.

### `internal/db` — Persistence Layer
Thin wrapper over `database/sql` + pure-Go SQLite (`glebarez/sqlite`). Schema: `sources`, `downloads`, `settings`, `people`. WAL mode, single connection (serialised writes). Exposes typed query methods; migrations are inline idempotent `ALTER TABLE` statements.

### `internal/scanner` — Filesystem Reconciler
Walks `downloadDir` for `.info.json` files to back-fill missing DB records, and detects orphan files vs. missing DB entries. Used at startup and via `POST /api/scan/fix`.

### `internal/filter` — Video Filter Engine
Parses simple keyword/regex filters and advanced JSON rule sets (`title`, `duration`, `pubdate`, `pages`, `tags` conditions). Used by schedulers to skip unwanted videos before creating download records.

### `internal/notify` — Notification Dispatcher
Sends webhook/Telegram/Bark notifications for events: download_complete, download_failed, cookie_expired, disk_low, sync_complete, rate_limited. Runs a single background worker goroutine with a buffered job channel.

### `internal/config` — Hot Configuration
`HotConfig` (RW-mutex guarded struct) holds runtime-tunable values (workers, quality, intervals, filename template). `ConfigWatcher` polls the DB every 5 minutes and applies changes; `onChange` callbacks propagate updates to the downloader and scheduler.

### `web/static` — Frontend SPA
React (loaded from CDN via importmap, no bundler in production). Pages: Dashboard, Sources, Videos, Uploaders, Settings, Logs. Uses a global SSE singleton for real-time updates. Also ships a pre-built Vite bundle (`frontend/` directory) for development.

## Data Flow

**Subscription sync cycle:**
1. `Scheduler.checkAll()` fires on cron/interval tick.
2. DB query returns `sources` due for check (`last_check + check_interval < now`).
3. Each source is dispatched to the matching sub-scheduler (`bscheduler` / `dscheduler` / `phscheduler`).
4. Sub-scheduler calls the platform API client to fetch the latest video list, applies filter rules, and writes new `downloads` rows with `status=pending`.
5. `ProcessAllPending()` reads all `pending` rows and submits `Job`s to the `Downloader` queue.
6. Workers dequeue jobs, stream video bytes to disk (chunked + rate-limited), then call ffmpeg to merge DASH streams (Bilibili).
7. On completion, DB row is updated (`status=completed`, `file_path`, `file_size`), an SSE `download_event` is emitted, and the `Notifier` sends a push notification.

**Real-time UI updates:**
- `GET /api/events` (SSE) streams `progress`, `download_event`, and `log` event types.
- Frontend JS `ensureGlobalSSE()` maintains a single reconnecting `EventSource`; events are re-dispatched as `CustomEvent` on `window` so any component can subscribe.

**API request lifecycle:**
`rateLimitMiddleware` → `authMiddleware` → `http.ServeMux` route → handler → DB/downloader/scheduler callback → JSON response.

## Key Design Patterns

- **Callback injection**: `web.Server` exposes typed `Set*Func` setters; `main.go` wires scheduler methods in after constructing both. This decouples the web layer from the scheduler without interfaces.
- **Platform sub-scheduler pattern**: Each platform (`bscheduler`, `dscheduler`, `phscheduler`) is a self-contained struct with its own pause/cooldown/cookie/event logic, composed into the top-level `Scheduler`.
- **Atomic guard for re-entrant tasks**: `processPendingRunning int32` with `atomic.CompareAndSwap` prevents concurrent `ProcessAllPending` goroutines.
- **JavaScript VM pool**: Douyin's signing functions run in a pool of `goja` runtimes to amortize JS parse overhead while staying goroutine-safe.
- **Embedded assets**: `//go:embed` bundles `web/static/` and `web/templates/` directly into the binary — no external file serving required.
- **Idempotent schema migrations**: Inline `ALTER TABLE` statements, errors for existing columns silently ignored, ensure forward compatibility across upgrades.

## State Management

- **Persistent state**: SQLite database (`video-subscribe-dl.db`) in `--data-dir`. Holds all sources, download records, settings, and people (UP主) info.
- **In-memory state**: Downloader queue (buffered channel, capacity 2000), active job progress map, rate limiter windows (`sync.Map`), Bilibili UP主 info cache (TTL 6h), hot config snapshot, scheduler cooldown timestamps, Douyin/PH pause state.
- **Filesystem state**: Downloaded video files and thumbnails in `--download-dir`; `.info.json` sidecar metadata files; NFO/danmaku/subtitle files alongside videos.
- **Runtime config**: Settings stored in the `settings` DB table; `HotConfig` provides an in-memory cache refreshed by `ConfigWatcher` every 5 minutes.
