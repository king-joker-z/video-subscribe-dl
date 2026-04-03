# Phase 1.2 PH Scheduler Reliability — Research

**Date:** 2026-04-02
**Working directory:** `video-subscribe-dl/`
**Goal:** Goroutine hang fix (REQ-REL-1) + context-cancellable page scan (REQ-REL-2)

---

## 1. Goja eval timeout in `GetVideoURL` — current state (REQ-REL-1)

**File:** `internal/pornhub/client.go`, lines 779–789

```go
select {
case res := <-jsCh:
    if res.err != nil {
        return "", fmt.Errorf("%w: goja eval failed: %v", ErrParseFailed, res.err)
    }
    if err := json.Unmarshal([]byte(res.jsonStr), &fv); err != nil {
        return "", fmt.Errorf("%w: unmarshal flashvars: %v", ErrParseFailed, err)
    }
case <-time.After(10 * time.Second):
    vm.Interrupt("js eval timeout")
    return "", fmt.Errorf("%w: js eval timeout", ErrParseFailed)
}
```

### Findings:
- **Timeout IS already implemented** — `select` + `time.After` + `vm.Interrupt("js eval timeout")`. This is correct: the goroutine spawns the goja VM, and the selector races it against a timer.
- **Current timeout is 10s.** REQ-REL-1 acceptance criterion is **15s**. Change is a one-liner: `10 * time.Second` → `15 * time.Second`.
- **No log warning on timeout.** The timeout branch only calls `vm.Interrupt` and returns an error — it does not call `log.Printf`. REQ-REL-1 says "log warning" is required. Need to add `log.Printf("[pornhub·client] WARN: JS eval timeout after 15s, url=...")` before the `return`.
- **goja VM is NOT thread-safe.** The code comments note this (line 762): all VM ops (`RunString`, `vm.Get`, `Interrupt`) must happen in the same goroutine. The current code correctly spawns one goroutine for RunString/Get and calls `vm.Interrupt` from the parent goroutine — this is safe because `Interrupt` is documented as safe to call from any goroutine.
- **`context` is not imported** in `client.go`. Current imports are: `encoding/json`, `fmt`, `io`, `log`, `net/http`, `net/url`, `os`, `regexp`, `strings`, `sync`, `time`, `github.com/dop251/goja`, `golang.org/x/net/html`. Adding `context` requires adding it to the import block.

### REQ-REL-1 change summary:
1. `10 * time.Second` → `15 * time.Second` (line 787)
2. Add `log.Printf(...)` warning in the timeout branch before returning

---

## 2. `GetModelVideos` loop structure — current state (REQ-REL-2)

**File:** `internal/pornhub/client.go`, lines 375–458

### Current signature:
```go
func (c *Client) GetModelVideos(modelURL string) ([]Video, error)
```

No `context.Context` parameter. **Context is not passed in at all.**

### Loop structure (lines 423–455):
```go
for page := 2; page <= maxPageHardLimit; page++ {
    pageURL := fmt.Sprintf("%s?page=%d", videosURL, page)
    pageBody, pageStatus, pageErr := c.get(pageURL)   // HTTP GET (30s timeout)
    if pageErr != nil { ... break }
    // ... parse page ...
    if len(pageVideos) == 0 { break }
    allVideos = append(allVideos, pageVideos...)
    time.Sleep(pageDelay)    // 2s sleep
}
```

### What is missing:
- No `ctx.Done()` check at the top of the loop (before the HTTP fetch).
- No `ctx.Done()` check after `time.Sleep(pageDelay)`.
- `time.Sleep(pageDelay)` is a blocking call — it does not respect cancellation.

### Constants:
- `pageDelay = 2 * time.Second` — **already a package-level const** (line 24). ✅
- `maxPageHardLimit = 1000` — **currently declared as a local `const` inside `GetModelVideos`** (line 411), NOT a package-level const. This needs to be lifted.

### Required changes:
1. Add `context.Context` as first parameter: `func (c *Client) GetModelVideos(ctx context.Context, modelURL string) ([]Video, error)`
2. At top of for-loop: check `ctx.Done()` before each HTTP fetch
3. Replace `time.Sleep(pageDelay)` with a `select` that races the sleep timer against `ctx.Done()`
4. Lift `maxPageHardLimit` from local const to package-level const

### Cancellation window math:
- After `ctx.Done()` check at top of loop → 0s lag
- During `c.get()` (HTTP request) → the HTTP client has a 30s timeout but does NOT use the context, so it won't be cancelled during HTTP. **This is a second-order problem.** The HTTP client in `NewClient` uses `c.httpClient.Do(req)` without a context — requests made during cancellation will still complete their 30s window before the loop can check `ctx.Done()` again.
- During `time.Sleep(pageDelay)` → with `select + time.After + ctx.Done()`, cancels within milliseconds.

**Decision point for HTTP context propagation:**
The ROADMAP says: "cancels any in-progress PH scan within 1 page fetch timeout (30s)". This means it's acceptable that an in-flight HTTP request completes — we just need the loop to not start the NEXT page. So threading context into `c.get()` is NOT required for REQ-REL-2. The `ctx.Done()` check at loop top is sufficient.

However, to be thorough, the `get()` method could accept a context to allow mid-request cancellation. That would require either:
  - Adding `context.Context` to `get()` and using `http.NewRequestWithContext()`
  - Or not (acceptable for REQ-REL-2 scope).

**Recommendation:** Thread context into `c.get()` as well using `http.NewRequestWithContext()` — this is a small change and makes cancellation truly prompt. But per scope, only the loop-level check is strictly required.

---

## 3. `maxPageHardLimit` — current state

```go
// client.go line 411 — INSIDE GetModelVideos function body:
const maxPageHardLimit = 1000 // 防止死循环的兜底上限
```

**Must be lifted to package level** per the deliverables. Currently it's a local constant — it works functionally but cannot be overridden for tests.

---

## 4. `ClientOptions` pattern — does it exist? (REQ-REL-2)

**Finding: No `ClientOptions` struct exists anywhere in the `pornhub` package or any other package in the codebase.**

The closest analogues found are:
- `phscheduler.Config` struct (lines 93–98 in `scheduler.go`) — used to inject dependencies into `PHScheduler`
- `dscheduler.Config` struct — same pattern
- `dscheduler` uses a `sleepFn func(time.Duration)` field on the scheduler struct, overridden in tests to `func(d time.Duration) {}` (no-op). This is the **test-override pattern** used for time-based operations.

### Design options for making `pageDelay` / `maxPageHardLimit` overridable:

**Option A: `ClientOptions` struct passed to `NewClient`**
```go
type ClientOptions struct {
    PageDelay        time.Duration
    MaxPageHardLimit int
}

func NewClientWithOptions(opts ClientOptions, cookie ...string) *Client { ... }
```
Requires changing `NewClient` or adding a new constructor. Callers in phscheduler (`s.newClient()`) would need to pass options. Slightly more complex.

**Option B: Fields on `Client` struct**
```go
type Client struct {
    httpClient       *http.Client
    mu               sync.RWMutex
    cookie           string
    pageDelay        time.Duration  // 0 = use default const
    maxPageHardLimit int            // 0 = use default const
}
```
Test code can create a `Client` and set these fields directly. Simpler for tests.

**Option C: `sleepFn` field (mirror dscheduler pattern)**
Add `sleepFn func(time.Duration)` to `Client`, defaulting to `time.Sleep`. Tests override to no-op. This is the same pattern used in `dscheduler`.

**Recommendation: Option C + lift `maxPageHardLimit` to package-level const.**
- The `sleepFn` field mirrors the established dscheduler pattern exactly.
- `maxPageHardLimit` as a package-level const is fine for tests (tests can use a fake HTTP server that returns 0 videos quickly, so the limit doesn't matter).
- For the `pageDelay` override in tests, the `sleepFn` field is sufficient.
- A full `ClientOptions` struct is more than needed for the current test requirements.

---

## 5. How phscheduler stop signal works — context threading

**File:** `internal/scheduler/phscheduler/scheduler.go`

### Stop mechanism (lines 170–183):
```go
func (s *PHScheduler) Stop() {
    if s.rootCancel != nil {
        s.rootCancel()        // cancels rootCtx
    }
    if s.downloadLimiter != nil {
        s.downloadLimiter.Stop()
    }
    if s.wg != nil {
        s.wg.Wait()           // waits for all goroutines
    }
    s.eventChOnce.Do(func() { close(s.eventCh) })
}
```

### Root context (lines 87–89):
```go
rootCtx    context.Context
rootCancel context.CancelFunc
```

Created in `New()` (lines 109–110):
```go
ctx, cancel := context.WithCancel(context.Background())
```

### Current context usage in `check.go`:

In `CheckPHModel` (lines 83–88) — context IS already used for the backoff wait between retry attempts:
```go
select {
case <-s.rootCtx.Done():
    return
case <-time.After(backoff):
}
```

But `client.GetModelVideos(src.URL)` (line 60) is called **WITHOUT the context**. The `rootCtx` is available on `s` but is not passed through.

In `FullScanPHModel` (line 202) — context is NOT used at all:
```go
videos, err := client.GetModelVideos(src.URL)
```

### Exact lines to change in `check.go`:

1. **`CheckPHModel`** — line 60:
   ```go
   // BEFORE:
   videos, fetchErr = client.GetModelVideos(src.URL)
   // AFTER:
   videos, fetchErr = client.GetModelVideos(s.rootCtx, src.URL)
   ```
   Also add `"context"` to imports (check.go currently only imports `fmt`, `log`, `math/rand`, `strings`, `time` + internal packages).

2. **`FullScanPHModel`** — line 202:
   ```go
   // BEFORE:
   videos, err := client.GetModelVideos(src.URL)
   // AFTER:
   videos, err := client.GetModelVideos(s.rootCtx, src.URL)
   ```

---

## 6. Imports — what needs to be added

### `internal/pornhub/client.go`
- Add `"context"` — currently NOT imported.

### `internal/scheduler/phscheduler/check.go`
- Add `"context"` — currently NOT imported. However since `s.rootCtx` is already `context.Context` on the struct (defined in scheduler.go which imports `"context"`), this is only needed if `check.go` itself uses `context.Context` as a type directly. Passing `s.rootCtx` to a function that takes `context.Context` does NOT require check.go to import context — the caller doesn't name the type, it just passes the value.
- **Conclusion:** `check.go` does NOT need to import `"context"` — it just passes `s.rootCtx` as an argument. No new import needed in check.go.

---

## 7. Test file landscape

### `internal/pornhub/` — NO test files exist
```
internal/pornhub/
├── client.go
├── error.go
├── ratelimit.go
├── sanitize.go
└── types.go
```
**Zero test files.** This phase creates the first tests for this package.

### `internal/scheduler/phscheduler/` — NO test files exist
```
internal/scheduler/phscheduler/
├── check.go
├── scheduler.go
└── (other files?)
```
**Zero test files.** This phase creates the first tests for this package.

### Test patterns from `internal/scheduler/dscheduler/` (canonical reference):

1. **Mock interface pattern** (`mock_test.go`) — `mockDouyinAPI` implements `DouyinAPI` interface; injected via `Config.NewClient`.
2. **In-memory DB** — `db.Init(t.TempDir())` for full schema.
3. **`sleepFn` override** — `s.sleepFn = func(d time.Duration) {}` for instant sleep.
4. **`t.Cleanup(func() { s.Stop() })`** — clean lifecycle management.
5. **Fast limiter** — `newFastLimiter()` replaces the production rate limiter.

### Test patterns from `web/server_test.go` (Phase 1.1 reference):
- Uses `httptest.NewRecorder` + `httptest.NewRequest`
- Struct literal initialization with only the fields under test
- `t.Setenv` for env var isolation

### What the new tests need to do:

**Test 1: `TestGetVideoURL_JSEvalTimeout` (in `internal/pornhub/client_test.go`)**
- Craft a minimal HTML page containing a `flashvars_123 = {...}` script that triggers the goja eval path but the JS hangs (infinite loop: `while(true){}`).
- Create a test HTTP server that serves this HTML.
- Set a very short timeout (override the const or use `ClientOptions`).
- Call `GetVideoURL` and assert:
  - Returns an error containing "timeout"
  - Returns promptly (within a short deadline)

**Challenge:** The timeout is hardcoded at line 787 as `time.After(10 * time.Second)` (soon to be 15s). A test using the actual 15s timeout would be too slow. Options:
  - Add a `jsEvalTimeout time.Duration` field to `Client` (zero value = use 15s default).
  - OR: craft a JS that completes quickly but still exercises the timeout CODE PATH — not actually timing out. But that doesn't test the timeout path.

**Recommended approach:** Add `jsEvalTimeout time.Duration` field to `Client` struct. Default to 0 = use `15 * time.Second`. Tests can set it to `50 * time.Millisecond` for fast execution.

**Test 2: `TestGetModelVideos_ContextCancellation` (in `internal/pornhub/client_test.go`)**
- Create a test HTTP server that serves a valid page 1, then blocks on page 2 (or sleeps before responding).
- Call `GetModelVideos(ctx, url)` with a context that is cancelled after page 1 is fetched.
- Assert that the function returns `ctx.Err()` promptly (within a short deadline).

**Challenge with `time.Sleep(pageDelay)` in tests:** If `pageDelay = 2s`, a test will be slow. The `sleepFn` field approach from dscheduler solves this cleanly.

---

## 8. `ClientOptions` struct vs. fields on `Client` — final design

Based on analysis, the cleanest approach that matches the codebase conventions is:

**Add fields to `Client` struct with zero-value defaults:**
```go
type Client struct {
    httpClient       *http.Client
    mu               sync.RWMutex
    cookie           string
    // Overridable for tests (zero = use package-level const default)
    jsEvalTimeout    time.Duration
    sleepFn          func(time.Duration)  // for pageDelay sleep
}
```

In `NewClient`, set:
```go
c.sleepFn = time.Sleep
```

In `GetModelVideos`, replace `time.Sleep(pageDelay)` with:
```go
// After extracting pageDelay select:
effectiveDelay := pageDelay
if c.sleepFn == nil {
    c.sleepFn = time.Sleep
}
```

Actually, the cleanest approach: keep `sleepFn` always set (never nil) from `NewClient`.

For the `jsEvalTimeout`:
```go
func (c *Client) evalTimeout() time.Duration {
    if c.jsEvalTimeout > 0 {
        return c.jsEvalTimeout
    }
    return 15 * time.Second
}
```

This pattern requires NO new exported `ClientOptions` struct — tests in the same package can set unexported fields directly (same package). But tests would need to be in package `pornhub` (not `pornhub_test`).

**Alternative for exported `ClientOptions`** that satisfies the ROADMAP wording:
```go
// ClientOptions configures optional Client behaviour.
type ClientOptions struct {
    // PageDelay overrides the inter-page delay (default: pageDelay = 2s).
    // Set to a small value in tests to avoid slow scans.
    PageDelay time.Duration
    // MaxPageHardLimit overrides the maximum number of pages to scan (default: 1000).
    MaxPageHardLimit int
    // JSEvalTimeout overrides the goja JS eval timeout (default: 15s).
    JSEvalTimeout time.Duration
}
```

And `NewClient` gains an optional parameter or a separate constructor:
```go
func NewClientWithOptions(opts ClientOptions, cookie ...string) *Client
```

Or apply via a method:
```go
func (c *Client) WithOptions(opts ClientOptions) *Client { ... ; return c }
```

**Final recommendation:** Use unexported fields + a separate `withTestOptions` helper kept in `client_test.go` (internal test package `pornhub`). This avoids polluting the public API with test-only fields.

Actually, re-reading ROADMAP: *"expose them via an optional `ClientOptions` struct so callers can override for tests"* — this suggests a public struct. Use an **exported `ClientOptions`** struct and a `NewClientWithOptions(opts ClientOptions, cookie ...string) *Client` constructor.

---

## 9. Complete file-change map

### `internal/pornhub/client.go`

1. **Add `"context"` import.**
2. **Lift `maxPageHardLimit` to package-level const:**
   ```go
   const maxPageHardLimit = 1000 // GetModelVideos 翻页绝对上限，防死循环
   ```
   Remove the local `const maxPageHardLimit = 1000` inside `GetModelVideos`.
3. **Add `ClientOptions` struct + `NewClientWithOptions` constructor:**
   ```go
   type ClientOptions struct {
       PageDelay        time.Duration
       MaxPageHardLimit int
       JSEvalTimeout    time.Duration
   }
   ```
4. **Add fields to `Client` struct** (for overrides from options):
   ```go
   pageDelay        time.Duration
   maxPageHardLimit int
   jsEvalTimeout    time.Duration
   sleepFn          func(time.Duration)
   ```
5. **Update `NewClient` to set defaults via helper.**
6. **`GetVideoURL`** — two changes:
   - `time.After(10 * time.Second)` → `time.After(c.evalTimeout())` where `evalTimeout()` returns 15s or `c.jsEvalTimeout`.
   - Add `log.Printf("[pornhub·client] WARN: JS eval timeout (>%v), url=...", ...)` before returning.
7. **`GetModelVideos`** — three changes:
   - Add `ctx context.Context` as first parameter.
   - At top of for-loop body, add `ctx.Done()` check.
   - Replace `time.Sleep(pageDelay)` with select:
     ```go
     select {
     case <-ctx.Done():
         log.Printf("[pornhub·client] GetModelVideos: 上下文已取消，停止翻页")
         return allVideos, ctx.Err()
     case <-time.After(c.effectivePageDelay()):
     }
     ```

### `internal/scheduler/phscheduler/check.go`

1. **`CheckPHModel`** — line ~60: `client.GetModelVideos(src.URL)` → `client.GetModelVideos(s.rootCtx, src.URL)`
2. **`FullScanPHModel`** — line 202: `client.GetModelVideos(src.URL)` → `client.GetModelVideos(s.rootCtx, src.URL)`
3. **No new imports needed** (see Section 6 above).

### New file: `internal/pornhub/client_test.go`

Tests in package `pornhub` (not `pornhub_test`) to access unexported helpers if needed.

```
TestGetVideoURL_JSEvalTimeout    — goja infinite-loop JS → returns error with "timeout" message
TestGetModelVideos_ContextCancel — context cancelled mid-scan → returns ctx.Err() promptly
```

---

## 10. Key gotchas and surprises

### Gotcha 1: `maxPageHardLimit` is a LOCAL const (line 411), not package-level
The ROADMAP says to extract it as a package-level const. It's currently declared inside `GetModelVideos`. Lifting it is safe (same value, same scope behavior) but it's easy to miss that it's NOT already at package level.

### Gotcha 2: No context import in `client.go`
`context` is not in the import list. Must be added when adding the `ctx context.Context` parameter to `GetModelVideos`.

### Gotcha 3: `check.go` does NOT need a new `context` import
`s.rootCtx` is typed `context.Context` but `check.go` only passes it to a function — it doesn't declare any `context.Context` variables or use context package functions. No import needed.

### Gotcha 4: `c.get()` does NOT use context
The HTTP client in `NewClient` is created with a 30s timeout but individual HTTP requests are created via `http.NewRequest` (no context). During cancellation, the in-flight HTTP request will run to completion (up to 30s). This is acceptable per REQ-REL-2: "within 1 page fetch timeout (30s)". But if we want truly prompt cancellation, pass `ctx` to `http.NewRequestWithContext` in `get()`. Decision: add it for correctness; it's a small change.

### Gotcha 5: `c.cookie` is accessed directly (not via `getCookie()`) in `GetVideoURL` line 815
```go
if c.cookie != "" {   // line 815 — DIRECT field access, not getCookie()!
```
This is a pre-existing race condition risk (not introduced by Phase 1.2). Don't fix it here; it's out of scope.

### Gotcha 6: `time.Sleep(pageDelay)` is the LAST statement in the loop body
The `time.Sleep` happens AFTER the page is successfully processed. The context check at the TOP of the next loop iteration will catch cancellation before the NEXT HTTP fetch. So even without replacing `time.Sleep` with a select, the scheduler will cancel at "next iteration start". But replacing with select ensures it exits within ~0s of cancellation rather than waiting up to 2s for the sleep. Since REQ-REL-2 says "within one pageDelay (2s) + HTTP timeout (30s)", the select is required.

### Gotcha 7: `GetModelVideos` also has a synchronous page 1 fetch (lines 383–388) before the loop
The first page fetch (`c.get(videosURL)`) at line 384 is not inside the loop. A `ctx.Done()` check should also be inserted between the first-page fetch and the loop start, to avoid starting the loop at all if the context was cancelled during page 1.

### Gotcha 8: goja VM interrupt is safe from different goroutine
From goja docs: `vm.Interrupt()` is safe to call from any goroutine. The current code (calling `vm.Interrupt("js eval timeout")` from the select branch) is correct. No changes needed to the interrupt logic, only the timeout duration and the log warning.

### Gotcha 9: No existing tests for the pornhub package
Both `internal/pornhub/` and `internal/scheduler/phscheduler/` have zero test files. This is the first test coverage for these packages. Test setup requires a fake HTTP server (use `httptest.NewServer`) — no DB or complex infrastructure needed for client-level tests.

### Gotcha 10: `sleepFn` vs. struct fields — test accessibility
If tests are in package `pornhub` (same package, file `client_test.go` with `package pornhub`), they can set unexported fields directly. If tests are in `pornhub_test` (black-box), they need exported `ClientOptions`. The ROADMAP specifies `ClientOptions`, so the exported struct approach is the right one.

---

## 11. Analogous pattern reference: `sign_pool.go`

The ROADMAP for Phase 1.3 references `sign_pool.go`'s context-with-timeout pattern. Worth noting here for context-cancellation inspiration:

```go
// internal/douyin/sign_pool.go lines 117-128
func (sp *signPool) sign(ctx context.Context, params map[string]string) (string, error) {
    var entry *signEntry
    select {
    case entry = <-sp.pool:
    case <-ctx.Done():
        return "", fmt.Errorf("sign pool: timed out waiting for available VM (pool may be exhausted)")
    }
    ...
}
```

This is the pattern to mirror for the `GetModelVideos` loop cancellation check.

---

## 12. Summary table — all changes

| File | Line(s) | Change | REQ |
|------|---------|--------|-----|
| `internal/pornhub/client.go` | imports | Add `"context"` | REL-2 |
| `internal/pornhub/client.go` | ~line 26 | Add `maxPageHardLimit = 1000` as package-level const | REL-2 |
| `internal/pornhub/client.go` | ~line 62 | Add `ClientOptions` struct | REL-2 |
| `internal/pornhub/client.go` | ~line 64 | Add `pageDelay`, `maxPageHardLimit`, `jsEvalTimeout`, `sleepFn` fields to `Client` | REL-1/2 |
| `internal/pornhub/client.go` | ~line 71 | Update `NewClient` to set `sleepFn = time.Sleep`; add `NewClientWithOptions` | REL-2 |
| `internal/pornhub/client.go` | line 787 | `time.After(10s)` → `time.After(c.evalTimeout())` → returns 15s default | REL-1 |
| `internal/pornhub/client.go` | line 788 | Add `log.Printf(...)` warning on timeout | REL-1 |
| `internal/pornhub/client.go` | line 375 | Add `ctx context.Context` first param to `GetModelVideos` | REL-2 |
| `internal/pornhub/client.go` | line 411 | Remove local `const maxPageHardLimit` | REL-2 |
| `internal/pornhub/client.go` | line 423 | Add `ctx.Done()` check at top of for-loop | REL-2 |
| `internal/pornhub/client.go` | line 454 | Replace `time.Sleep(pageDelay)` with select over `ctx.Done()` + timer | REL-2 |
| `internal/scheduler/phscheduler/check.go` | line ~60 | Pass `s.rootCtx` to `GetModelVideos` in `CheckPHModel` | REL-2 |
| `internal/scheduler/phscheduler/check.go` | line 202 | Pass `s.rootCtx` to `GetModelVideos` in `FullScanPHModel` | REL-2 |
| `internal/pornhub/client_test.go` | new file | `TestGetVideoURL_JSEvalTimeout` | REL-1 |
| `internal/pornhub/client_test.go` | new file | `TestGetModelVideos_ContextCancel` | REL-2 |
