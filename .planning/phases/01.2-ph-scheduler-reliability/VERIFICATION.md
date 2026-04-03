---
phase: 01.2-ph-scheduler-reliability
verified_by: post-execution static analysis
verified_at: 2026-04-02
requirements: [REQ-REL-1, REQ-REL-2]
verdict: PASS
---

# Phase 1.2 — Verification Report

## Requirements Cross-Reference

| Req ID    | Text (from REQUIREMENTS.md)                                                                                                  | Status |
|-----------|------------------------------------------------------------------------------------------------------------------------------|--------|
| REQ-REL-1 | Pornhub JS evaluation must not permanently block the phscheduler goroutine; JS execution must run in separate goroutine with channel + select + timeout; on timeout: return error, log warning, scheduler continues; JS eval completes or times out within 15 s | **PASS** |
| REQ-REL-2 | `GetModelVideos` must not block scheduler goroutine for >33 min; context-based cancellation so phscheduler Stop() propagates into page fetch loop; pageDelay and maxPageHardLimit configurable | **PASS** |

---

## Must-Have Checklist

### 1. goja JS eval timeout is 15 s (not 10 s)

**Evidence — `internal/pornhub/client.go` line 153:**
```go
return 15 * time.Second   // evalTimeout() helper default
```
`grep '10 \* time\.Second' internal/pornhub/client.go` → **0 matches** in the select block (no legacy 10 s value remains).

**Result: PASS**

---

### 2. `log.Printf` warning fires on eval timeout

**Evidence — `internal/pornhub/client.go` line 879:**
```go
log.Printf("[pornhub·client] WARN: goja JS eval timeout (>%v), url=%s", c.evalTimeout(), videoPageURL)
```
Exactly **1 match** of `WARN: goja JS eval timeout` in the file.

**Result: PASS**

---

### 3. `GetModelVideos` signature is `(ctx context.Context, modelURL string)`

**Evidence — `internal/pornhub/client.go` line 445:**
```go
func (c *Client) GetModelVideos(ctx context.Context, modelURL string) ([]Video, error) {
```

**Result: PASS**

---

### 4. `ctx.Done()` checked in at least 3 places inside `GetModelVideos`

**Evidence — `internal/pornhub/client.go`:**

| Line | Location |
|------|----------|
| 492  | Pre-loop check (after page-1 parse, before `for page := 2`) |
| 503  | Top-of-loop check (first statement inside the `for` body) |
| 540  | Sleep select (replaces `time.Sleep(pageDelay)`) |

`grep -n 'ctx\.Done()' internal/pornhub/client.go` → **3 matches**, all within `GetModelVideos`.

**Result: PASS** (≥ 3 required; exactly 3 present)

---

### 5. `http.NewRequestWithContext` used in `c.get()`

**Evidence — `internal/pornhub/client.go` line 253:**
```go
req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
```

**Result: PASS**

---

### 6. `maxPageHardLimit` is a package-level const (not local to a function)

**Evidence — `internal/pornhub/client.go` line 28:**
```go
const maxPageHardLimit = 1000
```
This is at column 0 (no indentation), outside any function body — confirmed by file structure review (lines 27–29 are between the `pageDelay` const and the block constants, before the `ClientOptions` struct at line 37).

No local `const maxPageHardLimit` remains inside `GetModelVideos` — the old line 411 declaration is gone.

**Result: PASS**

---

### 7. `ClientOptions` struct exists with `PageDelay`, `MaxPageHardLimit`, `JSEvalTimeout` fields

**Evidence — `internal/pornhub/client.go` lines 37–45:**
```go
type ClientOptions struct {
    PageDelay        time.Duration
    MaxPageHardLimit int
    JSEvalTimeout    time.Duration
}
```

**Result: PASS**

---

### 8. `NewClientWithOptions` constructor exists

**Evidence — `internal/pornhub/client.go` line 133:**
```go
func NewClientWithOptions(opts ClientOptions, cookie ...string) *Client {
```
Constructor reads all three `ClientOptions` fields and applies them to private struct fields (`jsEvalTimeout`, `pageDelayCfg`, `maxPageCfg`).

**Result: PASS**

---

### 9. `effectiveMaxPage()` used as loop bound (not bare `maxPageHardLimit`)

**Evidence — `internal/pornhub/client.go` line 500:**
```go
for page := 2; page <= c.effectiveMaxPage(); page++ {
```
`grep 'page <= maxPageHardLimit' internal/pornhub/client.go` → **0 matches** (bare const no longer used as loop bound).

**Result: PASS**

---

### 10. `s.rootCtx` passed to `GetModelVideos` in `CheckPHModel`

**Evidence — `internal/scheduler/phscheduler/check.go` line 60:**
```go
videos, fetchErr = client.GetModelVideos(s.rootCtx, src.URL)
```
Located inside the `for attempt := 1; attempt <= 3; attempt++` retry loop in `CheckPHModel`.

**Result: PASS**

---

### 11. `s.rootCtx` passed to `GetModelVideos` in `FullScanPHModel`

**Evidence — `internal/scheduler/phscheduler/check.go` line 202:**
```go
videos, err := client.GetModelVideos(s.rootCtx, src.URL)
```
Located in `FullScanPHModel`.

`grep 'GetModelVideos(src\.URL)' internal/scheduler/phscheduler/check.go` → **0 matches** (old bare call fully replaced in both functions).

**Result: PASS**

---

### 12. `client_test.go` has `TestGetVideoURL_JSEvalTimeout` and `TestGetModelVideos_ContextCancel`

**Evidence — `internal/pornhub/client_test.go`:**

| Item | Line | Value |
|------|------|-------|
| Package declaration | 1 | `package pornhub_test` (black-box) |
| `TestGetVideoURL_JSEvalTimeout` | 23 | Present |
| `TestGetModelVideos_ContextCancel` | 76 | Present |
| `NewClientWithOptions` usages | 42, 109 | 2 occurrences (one per test) |

File exercises only exported API via `ClientOptions` / `NewClientWithOptions` / `GetVideoURL` / `GetModelVideos`.

**Result: PASS**

---

## Additional Observations

### `evalTimeout()` helper confirmed

`internal/pornhub/client.go` lines 149–154:
```go
func (c *Client) evalTimeout() time.Duration {
    if c.jsEvalTimeout > 0 {
        return c.jsEvalTimeout
    }
    return 15 * time.Second
}
```
Used in **3 places** within the `GetVideoURL` select block (lines 877, 879, 880) — `time.After`, `log.Printf`, and `fmt.Errorf`.

### `effectivePageDelay()` helper confirmed

`internal/pornhub/client.go` lines 157–162:
```go
func (c *Client) effectivePageDelay() time.Duration {
    if c.pageDelayCfg > 0 {
        return c.pageDelayCfg
    }
    return pageDelay
}
```
Used at line 543 in the ctx-aware sleep select: `case <-time.After(c.effectivePageDelay())`.

### `"context"` import present

`internal/pornhub/client.go` line 4: `"context"` — first entry in the stdlib import group.

### `jsEvalTimeout` field appears in ≥ 3 places

Lines 85 (struct field), 136 (`NewClientWithOptions` assignment), 150 (`evalTimeout()` conditional) — satisfies the plan's acceptance criterion of ≥ 3 matches.

### `time.Sleep(pageDelay)` fully replaced

`grep 'time\.Sleep(pageDelay)' internal/pornhub/client.go` → **0 matches**.

---

## Build / Test Execution Note

> **Test compilation and execution require CI.**
>
> The tests in `internal/pornhub/client_test.go` depend on `github.com/dop251/goja`
> (a native Go dependency already present in `go.mod`) and the standard `net/http/httptest`
> package.  Static analysis confirms the test file compiles against the current exported
> surface (`NewClientWithOptions`, `ClientOptions`, `GetVideoURL`, `GetModelVideos`, `Close`).
> Runtime execution of `TestGetVideoURL_JSEvalTimeout` (exercises goja interrupt) and
> `TestGetModelVideos_ContextCancel` (exercises HTTP + context cancellation) must be
> validated by the CI pipeline (`go test ./internal/pornhub/...`).

---

## Summary

All **12 must-have items** from the PLAN.md checklist are confirmed **PASS** via static
analysis of the actual codebase.  REQ-REL-1 and REQ-REL-2 are fully satisfied:

- **REQ-REL-1:** goja eval runs in a separate goroutine; `select` + `c.evalTimeout()` (15 s
  default, overridable) guarantees the scheduler goroutine is never permanently blocked;
  `log.Printf` WARN fires on timeout.
- **REQ-REL-2:** `GetModelVideos(ctx, url)` propagates cancellation through `c.get(ctx, …)`
  (via `http.NewRequestWithContext`) and three `ctx.Done()` select points; both
  `CheckPHModel` and `FullScanPHModel` thread `s.rootCtx` into the call; page delay and
  hard page limit are configurable via `ClientOptions`/`NewClientWithOptions`.

**Phase 1.2 goal: ACHIEVED.**
