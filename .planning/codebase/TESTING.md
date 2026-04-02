# Testing

## Test Framework

- **Language**: Go standard `testing` package only — no third-party test framework (no testify, gomock, etc.)
- **HTTP testing**: `net/http/httptest` (`httptest.NewRequest`, `httptest.NewRecorder`, `httptest.NewServer`)
- **Database**: `github.com/glebarez/sqlite` in-memory SQLite (`:memory:`) for all DB tests — no mocking layer for the DB itself
- **JavaScript engine**: `github.com/dop251/goja` (used in production for a_bogus/X-Bogus signing) — tested directly via pool abstraction
- **CI**: GitHub Actions (`.github/workflows/test.yml` + `docker.yml`) runs `go test ./... -v -count=1 -timeout 120s` on every push/PR; coverage report generated with `-coverprofile` on the Docker build workflow

---

## Test Organization

**Location**
- Unit/package tests: `*_test.go` files co-located with their source package (white-box, same `package`)
- Integration tests: `tests/integration_test.go` in a top-level `tests/` package (black-box, separate package)
- API handler tests: `web/api/api_test.go` — white-box tests within the `api` package

**Naming**
- Test functions: `Test<Subject>_<Scenario>` — e.g., `TestCheckDouyin_FirstScan_CreatesDownloads`, `TestSourceCRUD_Update`
- Table-driven tests use slice-of-struct with `name` / `want` fields, iterated with `t.Run(tt.name, ...)`
- Helper functions: `initMemoryDB`, `setupTestRouter`, `newTestDouyinScheduler`, `createTestSource`
- Mock types: `mock<Interface>` — e.g., `mockDouyinAPI`

---

## Test Types

**Unit tests** — the majority of test files:
| Package | File | What's tested |
|---|---|---|
| `internal/bilibili` | `credential_test.go` | Cookie string parsing/serialization, JSON marshal, RSA crypto path |
| `internal/bilibili` | `dash_test.go` | DASH stream selection (best video/audio by codec, quality, bandwidth) |
| `internal/bilibili` | `client_test.go` | Client construction, cookie update |
| `internal/bilibili` | `qrcode_test.go` | QR code generation helpers |
| `internal/danmaku` | `danmaku_test.go` | XML parsing, ASS conversion, collision detection (lane managers), color/time helpers |
| `internal/db` | `db_test.go` | Full CRUD for sources/downloads/settings/people, status flow, retry count, permanent-failed logic |
| `internal/douyin` | `abogus_test.go` | JS VM pool init, concurrent signing, URL signing, string escaping |
| `internal/douyin` | `parse_test.go` | `parseAwemeDetail` (video/note/images/cover fallback), `parseRouterDataForVideo`, URL building helpers |
| `internal/douyin` | `cookie_test.go` | msToken/verify_fp generation, cookie sanitization, global cookie manager |
| `internal/douyin` | `ratelimit_test.go` | Rate limiter behavior |
| `internal/douyin` | `fingerprint_test.go` | Browser fingerprint generation |
| `internal/douyin` | `sign_updater_test.go` | Sign updater logic |
| `internal/douyin` | `client_http_test.go`, `client_http2_test.go`, `client_test.go` | HTTP/HTTP2 client internals |
| `internal/downloader` | `downloader_test.go` | Timeout calculation, Job struct fields |
| `internal/scheduler` | `filter_test.go` | `MatchesSimple`, `ParseRules`, `MatchesRules` (all conditions + edge cases) |
| `internal/scheduler` | `retry_test.go` | `retryFailedDownloads`, `RetryByID`, `RedownloadByID` |
| `internal/scheduler` | `cleanup_test.go` | Invalid character detection, empty dir removal |
| `internal/scheduler/dscheduler` | `check_test.go` | `CheckDouyin` full flow (paused, URL resolve, first scan, incremental, dedup, pagination, page limit, fallback title, source name update, client close, cookie validation); `CheckDouyinMix`; `FullScanDouyin` |
| `internal/scheduler/dscheduler` | `cooldown_pause_test.go` | Pause/Resume state, cooldown timer, cookie status |
| `internal/scheduler/dscheduler` | `progress_test.go` | Download speed/percent calculation, `downloadFileWithProgress` (progress events, context cancel) |
| `internal/util` | `ratelimit_test.go` | `RateLimitedReader` — no limit, negative limit, data integrity, timing |
| `internal/util` | `filecleanup_test.go` | `RemoveAssociatedFiles`, `RemoveEmptyDirs` |

**API handler tests** (`web/api/api_test.go`):
- Ping endpoint, Sources CRUD flow, source type validation, video search by uploader (partial match, case-insensitive)
- Uses an in-memory SQLite DB with inline schema; no scheduler needed

**Integration tests** (`tests/integration_test.go`):
- Full `db.Init` + `downloader.New` + `api.NewRouter` stack in a temp directory
- Tests: Bilibili/Douyin source check flow, quick download parameter validation, metrics endpoint fields, Prometheus metrics format, sign reload endpoint, graceful shutdown, goroutine leak check (`DouyinClientClose`), full source CRUD, Prometheus output content
- Mock HTTP servers (`httptest.NewServer`) used to test JSON parsing of Bilibili/Douyin API responses

**Mock infrastructure** (`internal/scheduler/dscheduler/mock_test.go`):
- `mockDouyinAPI` implements the `DouyinAPI` interface with configurable per-call responses (`userVideoPages []mockVideoPage`)
- Mutex-protected call counters (`userVideoCalls`, `videoDetailCalls`) for verifying API call counts
- `newTestDouyinScheduler` injects the mock via `Config.NewClient`, replaces `sleepFn` with a no-op, and uses a fast rate limiter

---

## Coverage

**Estimated coverage by package** (no formal report in repo; based on test breadth):

| Package | Estimated Coverage | Notes |
|---|---|---|
| `internal/db` | ~80% | All major CRUD paths, retry/permanent-failed logic; some complex queries untested |
| `internal/bilibili` | ~60% | Credential, DASH selection, client construction tested; network-dependent methods (GetUPInfo, etc.) not mocked |
| `internal/danmaku` | ~85% | Parser, converter, lane managers, helpers all tested |
| `internal/douyin` | ~65% | Core signing, parsing, cookie, rate limiting tested; network I/O methods (`GetUserVideos`, etc.) rely on mock at scheduler layer |
| `internal/scheduler/dscheduler` | ~75% | Full check/scan/pause/cooldown flows via mock; download execution paths partially covered |
| `internal/scheduler` | ~50% | Retry/redownload logic tested; bili scheduler paths not tested |
| `internal/util` | ~90% | Rate-limited reader and file cleanup fully covered |
| `internal/downloader` | ~20% | Only timeout calculation and struct field tests; actual download execution untested |
| `web/api` | ~40% | Ping, CRUD, search tested; many handlers (credential, events, stream, quickdl) not covered |
| `internal/logger` | 0% | No tests for the ring logger |
| `internal/filter` | ~90% | All conditions tested via scheduler/filter_test.go |

**Notable gaps**:
- `internal/logger` has no tests despite non-trivial ring buffer and SSE subscription logic
- `internal/downloader` core download execution is untested
- `web/api` credential/login, SSE events, video streaming, and quick-download handlers are untested
- Bilibili scheduler (`internal/scheduler`) bili-specific paths have no mock coverage

---

## Running Tests

```bash
# Run all tests
go test ./...

# Run with verbose output
go test ./... -v -count=1 -timeout 120s

# Run a specific package
go test ./internal/scheduler/dscheduler/... -v

# Run with coverage report
go test -short -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Run a single test
go test ./internal/bilibili/... -run TestCredentialFromCookieString -v

# Run integration tests only
go test ./tests/... -v
```

CI runs `go test ./... -v -count=1 -timeout 120s` (see `.github/workflows/test.yml`).

---

## Test Data

**In-memory SQLite** — all DB tests open `:memory:` SQLite via `sql.Open("sqlite", ":memory:")`. The schema is either:
- Applied via exported `db.Init(tmpDir)` (integration tests, dscheduler tests)
- Inlined as a SQL string in the test helper `initTestDB` (api_test.go)
- Applied via the internal `schema` constant (db_test.go, white-box)

**Temp directories** — file system tests use `t.TempDir()` (auto-cleaned) for downloads, data directories, and file cleanup tests.

**Mock HTTP servers** — `httptest.NewServer` is used for:
- Bilibili API response parsing (hardcoded JSON payloads in integration tests)
- Douyin API response parsing (hardcoded JSON)
- `downloadFileWithProgress` tests (controllable payload size and streaming speed)

**Mock interfaces** — `mockDouyinAPI` in `dscheduler/mock_test.go` is the only interface mock. It covers the full `DouyinAPI` interface with configurable page-by-page responses for pagination testing.

**No fixtures directory** — there are no static fixture files; all test data is defined inline in the test functions.

**Global state management** — douyin cookie tests save/restore `globalCookieMgr` state using `defer` to avoid test pollution:
```go
origCookie := GetGlobalUserCookie()
defer SetGlobalUserCookie(origCookie)
```
Similarly, a_bogus pool tests call `resetABogusPool()` before and after via `defer` to ensure a clean JS VM pool state.
