# Plan 02.2-01 Execution Summary

**Phase:** 02.2-observability  
**Plan:** 02.2-01 (single plan for this phase)  
**Status:** Complete  
**Date:** 2026-04-07  

## Tasks Completed

### T1 — Downloader: Platform field + per-platform counters
**Commit:** `0ef6b6d`

- Added `Platform string` field to `downloader.Job` struct
- Added `platformCompleted`/`platformFailed map[string]*int64` to `Downloader` struct
- Initialized both maps in `New()` for all 3 known platforms (bilibili, douyin, pornhub)
- Added `AddPlatformCompleted/AddPlatformFailed` public methods with nil guards
- Updated `processOneJob` to atomically increment per-platform counters with nil guard
- Updated `DownloaderStats`/`Stats()` to expose `PlatformCompleted`/`PlatformFailed`
- Added `Platform: "bilibili"` to all 3 Job constructions in bscheduler (process.go x2, retry.go x1)

### T2 — Sub-schedulers: lastCheckAt tracking
**Commit:** `0bfe920`

- Added `lastCheckMu sync.RWMutex` and `lastCheckAt time.Time` to BiliScheduler, DouyinScheduler, PHScheduler
- Added `setLastCheckAt()`/`LastCheckAt()` methods to all 3 schedulers
- Added `defer s.setLastCheckAt(time.Now())` at top of each `CheckSource()` (all paths including early-return)

### T3 — MetricsHandler: new Prometheus metrics
**Commit:** `7d5d8b0`

- Added `getSchedulerLastCheck map[string]func() time.Time` to MetricsHandler + init in `NewMetricsHandler()`
- Added `SetSchedulerLastCheckFunc(platform, fn)` setter
- `HandlePrometheus` emits 3 new metric groups (alphabetical: bilibili->douyin->pornhub):
  - `vsd_downloads_completed_total{platform="..."}` counter
  - `vsd_downloads_failed_total{platform="..."}` counter
  - `vsd_scheduler_last_check_timestamp{platform="..."}` gauge (uses IsZero() to output 0 not -62135596800)
- Added `SetSchedulerLastCheckFunc` proxy to `Router`

### T4 — Top-level scheduler: counter wiring + lastCheckAt proxies
**Commit:** `df79866`

- Douyin/ph event-forwarding goroutines call `AddPlatformCompleted/Failed` on completed/failed events
- Added `GetBiliLastCheckAt()`, `GetDouyinLastCheckAt()`, `GetPHLastCheckAt()` proxy methods on `Scheduler`

### T5 — Full wiring: server.go + main.go
**Commit:** `5ca707b`

- `web/server.go`: getSchedulerLastCheck map field, SetSchedulerLastCheckFunc setter, setupRoutes wiring
- `cmd/server/main.go`: 3 SetSchedulerLastCheckFunc calls for bilibili/douyin/pornhub

## Files Changed

- `internal/downloader/downloader.go`
- `internal/downloader/stats.go`
- `internal/scheduler/bscheduler/process.go`
- `internal/scheduler/bscheduler/retry.go`
- `internal/scheduler/bscheduler/scheduler.go`
- `internal/scheduler/dscheduler/scheduler.go`
- `internal/scheduler/phscheduler/scheduler.go`
- `internal/scheduler/scheduler.go`
- `web/api/metrics.go`
- `web/api/router.go`
- `web/server.go`
- `cmd/server/main.go`
