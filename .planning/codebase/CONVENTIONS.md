# Conventions

## Naming Conventions

**Files**
- Go source files use `snake_case` (`sources_crud.go`, `cooldown_pause_test.go`, `filecleanup_test.go`)
- Test files end in `_test.go` and match their source file's name (`credential.go` → `credential_test.go`)
- Sub-package directories use `snake_case` (`dscheduler/`)

**Functions & Methods**
- Exported handlers follow `Handle<Action>` (`HandleList`, `HandleCreate`, `HandleByID`)
- Constructor functions follow `New<Type>` (`NewRouter`, `NewSourcesHandler`, `NewClient`)
- Internal helpers follow Go convention (camelCase, unexported): `initMemoryDB`, `buildStats`, `extractURL`
- Test helpers follow `new<Type>` or `create<Type>` (`newTestDouyinScheduler`, `createTestSource`)

**Variables & Constants**
- Error codes as typed constants with `Code` prefix: `CodeOK`, `CodeBadRequest`, `CodeNotFound`
- Audio/video quality constants as descriptive names: `CodecAVC`, `CodecHEVC`, `Audio64K`, `AudioHiRes`
- Boolean fields in DB structs use plain names: `Enabled`, `SkipNFO`, `SkipPoster`

**Packages**
- All under module `video-subscribe-dl`; internal packages live in `internal/<domain>`
- The web layer lives in `web/api/`; each domain gets its own file (`sources_crud.go`, `videos.go`, etc.)

---

## Code Style

- Standard Go formatting (`gofmt`); no custom `.golangci.yml` or `.editorconfig` found in repo
- No third-party linter config — relies on the compiler and reviewer discipline
- Struct tags use `json` with `snake_case` keys (`json:"source_id"`, `json:"page_size"`)
- Inline type definitions used for anonymous response shapes (e.g., `SourceWithStats` inside `HandleList`)
- Long `HandleUpdate` methods use a `map[string]interface{}` partial-update pattern with explicit per-field type switches
- Chinese comments are used throughout for business logic explanations (mixed-language codebase)
- `[FIXED: P1-x]` / `[FIXED: P2-x]` inline annotations mark known bug fixes with priority labels

---

## Error Handling

**HTTP handlers**
- All errors go through the central `apiError(w, code, msg)` helper which maps business error codes to HTTP status codes
- Success responses use `apiOK(w, data)` — always `200 OK` with `{"code":0,"data":...,"message":"ok"}`
- Method guards use `MethodGuard(method, w, r)` to return `405` and stop execution
- Parse errors (`parseJSON`) return `CodeInvalidParam` (400)

**Internal packages**
- Errors are returned as `(value, error)` pairs; callers decide whether to log or propagate
- DB operations: errors from `Scan` / `QueryRow` are logged with `log.Printf("[source] ... error: %v", err)` but execution continues where safe (e.g., stats queries don't abort the whole response)
- `recover()` is used defensively in two places: `RecoveryMiddleware` (catches panics in any handler) and around `dyClient.Close()` in source creation

**Panic recovery**
- `RecoveryMiddleware` wraps the entire mux and returns `500` with `{"code":50000,...}` on panic
- Douyin client `Close()` is wrapped with `defer func() { defer func() { recover() }(); dyClient.Close() }()` to avoid propagation

---

## Logging

- **Library**: stdlib `log` package only — no structured logging framework
- **Custom ring buffer**: `internal/logger.RingLogger` wraps stdout writes into a 1000-entry ring buffer served over SSE (`/api/events`) and WebSocket (`/api/ws/logs`)
- **Log format**: `<timestamp> [LEVEL] <message>` — timestamp is `2006-01-02 15:04:05`
- **Levels**: `Info`, `Warn`, `Error` methods on `RingLogger`; level detection from `parseLine` by scanning for "error"/"fatal"/"warn" substrings
- **`log.Printf` usage**: handlers use stdlib `log.Printf` with `[component]` prefixes (e.g., `[source]`, `[api]`, `[PANIC]`) rather than the custom logger
- **Request logging** (`LogMiddleware`): only logs `4xx/5xx` responses and slow (>500ms) requests — not every request

---

## API Design

**URL structure**
- All API endpoints under `/api/` prefix
- RESTful resource paths: `/api/sources`, `/api/sources/:id`, `/api/videos`, `/api/videos/:id`
- Sub-actions as path suffixes: `/api/sources/:id/sync`, `/api/sources/:id/fullscan`
- Non-REST actions as noun endpoints: `/api/task/trigger`, `/api/sign/reload`, `/api/download/preview`

**Response format** (uniform envelope):
```json
{
  "code": 0,
  "data": <any>,
  "message": "ok"
}
```

**Error responses** — same envelope, `code ≠ 0`, HTTP status derived from code range:
- `40000–40099` → 400 Bad Request
- `40100–40399` → 401 Unauthorized
- `40400–40499` → 404 Not Found
- `40500–40899` → 405 Method Not Allowed
- `42900–42999` → 429 Too Many Requests
- `50000+` → 500 Internal Server Error

**Pagination**: paginated endpoints return `{"items": [...], "total": N, "page": P, "page_size": S}`

**Middleware stack** (applied in `web/server.go`):
1. `RecoveryMiddleware` — panic recovery
2. `CORSMiddleware` — CORS headers (only when `CORS_ORIGIN` env set)
3. `LogMiddleware` — request logging (errors + slow)
4. `AuthMiddleware` — bearer token / cookie auth (opt-in via settings)
5. `JSONMiddleware` — sets `Content-Type: application/json` (skipped for SSE/WebSocket/stream)

---

## Comments & Documentation

- `README.md` exists and covers setup, Docker usage, and feature overview
- `CHANGELOG-2026-03-19.md` and `TODO.md` document project history and roadmap
- `BUG-AUDIT.md` documents known bugs/fixes with priority labels (`P1`, `P2`)
- Inline comments are bilingual: English for exported API doc-style comments; Chinese for business logic
- No Go doc comments (`//` before exported types/funcs) on most internal packages — doc quality is informal
- Handler files have single-line `// GET /api/sources` route comments before each handler
- Fix annotations like `// [FIXED: P1-8]` are inline in the code near the fix
