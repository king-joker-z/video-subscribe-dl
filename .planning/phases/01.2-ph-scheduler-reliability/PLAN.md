---
wave: 1
autonomous: true
objective: >
  Pornhub scheduler goroutine never hangs permanently; page scans are
  cancellable when the scheduler is stopped. Achieve by: (1) increasing the
  goja JS-eval timeout 10s→15s and adding a log warning; (2) threading
  context.Context through GetModelVideos so Stop() propagates into the page
  loop; (3) exposing ClientOptions for test overrides; (4) wiring s.rootCtx
  into both CheckPHModel and FullScanPHModel; (5) writing first-ever tests
  for internal/pornhub.
requirements:
  - REQ-REL-1
  - REQ-REL-2
files_modified:
  - internal/pornhub/client.go
  - internal/scheduler/phscheduler/check.go
  - internal/pornhub/client_test.go   # new file
---

# Phase 1.2 — PH Scheduler Reliability · PLAN

## must_haves

> Goal-backward verification — every item below must be true for the phase
> to be considered complete.

- [ ] `GetVideoURL` times out in ≤15 s (never hangs indefinitely) — REQ-REL-1
- [ ] A `log.Printf` warning is emitted when the goja eval timeout fires — REQ-REL-1
- [ ] `GetModelVideos` accepts `context.Context` as first parameter — REQ-REL-2
- [ ] Cancelling the context during a page scan causes `GetModelVideos` to
      return `ctx.Err()` promptly (within pageDelay + 30 s HTTP timeout window)
      — REQ-REL-2
- [ ] Both `CheckPHModel` and `FullScanPHModel` pass `s.rootCtx` to
      `GetModelVideos` so `Stop()` cancels an in-progress scan — REQ-REL-2
- [ ] `maxPageHardLimit` is a package-level const (not local to
      `GetModelVideos`) — REQ-REL-2
- [ ] `ClientOptions` exported struct + `NewClientWithOptions` constructor
      exist so callers can override `PageDelay`, `MaxPageHardLimit`, and
      `JSEvalTimeout` for testing — REQ-REL-2
- [ ] `internal/pornhub/client_test.go` exists with at least two tests:
      `TestGetVideoURL_JSEvalTimeout` and `TestGetModelVideos_ContextCancel`
      — REQ-REL-1, REQ-REL-2

---

## T1 — GetVideoURL: 10 s → 15 s timeout + log warning

**Requirements:** REQ-REL-1

<read_first>
- `internal/pornhub/client.go` lines 756–790 (goja eval select block)
</read_first>

### Action

**1a. Add `evalTimeout()` helper to `Client`**

Add a private helper method immediately after `NewClientWithOptions` (or after
the `Client` struct definition):

```go
// evalTimeout returns the effective goja JS eval timeout.
// Zero value (not set via ClientOptions) defaults to 15 s.
func (c *Client) evalTimeout() time.Duration {
    if c.jsEvalTimeout > 0 {
        return c.jsEvalTimeout
    }
    return 15 * time.Second
}
```

**1b. Replace the hardcoded `time.After(10 * time.Second)` at line 787**

Current (line 787–789):
```go
case <-time.After(10 * time.Second):
    vm.Interrupt("js eval timeout")
    return "", fmt.Errorf("%w: js eval timeout", ErrParseFailed)
```

Replace with:
```go
case <-time.After(c.evalTimeout()):
    vm.Interrupt("js eval timeout")
    log.Printf("[pornhub·client] WARN: goja JS eval timeout (>%v), url=%s", c.evalTimeout(), videoPageURL)
    return "", fmt.Errorf("%w: js eval timeout after %v", ErrParseFailed, c.evalTimeout())
```

> `videoPageURL` is in scope — it is the first parameter of `GetVideoURL`.

### Acceptance criteria

```
# grep-verifiable in internal/pornhub/client.go after the edit:
grep -n 'c\.evalTimeout()' internal/pornhub/client.go
# must show at least 2 matches (time.After call + log call)

grep -n 'WARN: goja JS eval timeout' internal/pornhub/client.go
# must show exactly 1 match

grep -n '10 \* time\.Second' internal/pornhub/client.go
# must show 0 matches in the select block (the old hardcoded value is gone)
```

---

## T2 — GetModelVideos: context parameter + ctx.Done() loop checks + http.NewRequestWithContext

**Requirements:** REQ-REL-2

<read_first>
- `internal/pornhub/client.go` lines 374–458 (full `GetModelVideos` body)
- `internal/pornhub/client.go` lines 182–210 (`get` method)
</read_first>

### Action

**2a. Add `"context"` to the import block**

The current import block (lines 3–18) does not include `"context"`. Add it
as the first entry in the standard-library group:

```go
import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "os"
    "regexp"
    "strings"
    "sync"
    "time"

    "github.com/dop251/goja"
    "golang.org/x/net/html"
)
```

**2b. Change `GetModelVideos` signature**

Current (line 375):
```go
func (c *Client) GetModelVideos(modelURL string) ([]Video, error) {
```

New:
```go
func (c *Client) GetModelVideos(ctx context.Context, modelURL string) ([]Video, error) {
```

**2c. Add pre-loop ctx.Done() check after page-1 fetch**

After the first-page parse (after the `validateModelPage` call, before the
`for page := 2` loop, approximately at current line 421):

```go
// context check before entering the pagination loop
select {
case <-ctx.Done():
    log.Printf("[pornhub·client] GetModelVideos: 上下文已取消，跳过翻页")
    return allVideos, ctx.Err()
default:
}
```

**2d. Add ctx.Done() check at top of every for-loop iteration**

At the very top of the `for page := 2; page <= c.effectiveMaxPage(); page++`
body (before `pageURL := fmt.Sprintf(...)`):

```go
select {
case <-ctx.Done():
    log.Printf("[pornhub·client] GetModelVideos: 上下文已取消，停止翻页 (page=%d)", page)
    return allVideos, ctx.Err()
default:
}
```

**2e. Replace `time.Sleep(pageDelay)` with ctx-aware select**

Current (line 454):
```go
time.Sleep(pageDelay)
```

Replace with:
```go
select {
case <-ctx.Done():
    log.Printf("[pornhub·client] GetModelVideos: 上下文已取消（延迟中）, 停止翻页 (page=%d)", page)
    return allVideos, ctx.Err()
case <-time.After(c.effectivePageDelay()):
}
```

**2f. Add `effectivePageDelay()` and `effectiveMaxPage()` helpers**

Add these two private helpers alongside `evalTimeout()`:

```go
// effectivePageDelay returns the inter-page delay, honouring ClientOptions.PageDelay.
func (c *Client) effectivePageDelay() time.Duration {
    if c.pageDelayCfg > 0 {
        return c.pageDelayCfg
    }
    return pageDelay
}

// effectiveMaxPage returns the hard page limit, honouring ClientOptions.MaxPageHardLimit.
func (c *Client) effectiveMaxPage() int {
    if c.maxPageCfg > 0 {
        return c.maxPageCfg
    }
    return maxPageHardLimit
}
```

**2g. Update `get()` to accept and propagate context**

Change `get` signature from:
```go
func (c *Client) get(rawURL string) ([]byte, int, error) {
```
to:
```go
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, int, error) {
```

Replace the `http.NewRequest` call inside `get` from:
```go
req, err := http.NewRequest("GET", rawURL, nil)
```
to:
```go
req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
```

Update every call site of `c.get(...)` to pass a context:

| Call site | Current | New |
|-----------|---------|-----|
| `GetVideoThumbnail` (line 149) | `c.get(videoPageURL)` | `c.get(context.Background(), videoPageURL)` |
| `GetModelInfo` (line 261) | `c.get(cleanURL)` | `c.get(context.Background(), cleanURL)` |
| `GetModelVideos` page-1 (line 384) | `c.get(videosURL)` | `c.get(ctx, videosURL)` |
| `GetModelVideos` loop (line 425) | `c.get(pageURL)` | `c.get(ctx, pageURL)` |
| `GetVideoURL` (line 649) | `c.get(videoPageURL)` | `c.get(context.Background(), videoPageURL)` |

> `getWithCookie` uses `http.NewRequest` too — leave it unchanged; it is not
> in the hot path for cancellation and is only called for the tv-mode fallback.
> It can be updated in a follow-on phase.
>
> Note: `getJSON()` also uses `http.NewRequest` internally, but it is
> intentionally left unchanged here. `getJSON` is only called in the
> `GetVideoURL` cookie path (tv-mode fallback), not anywhere in the pagination
> loop, so it is outside the cancellation scope of REQ-REL-2.

**2h. Change the for-loop header to use `c.effectiveMaxPage()`**

In `GetModelVideos`, locate the pagination for-loop header:
```go
for page := 2; page <= maxPageHardLimit; page++ {
```

Change it to:
```go
for page := 2; page <= c.effectiveMaxPage(); page++ {
```

> This is required so the `MaxPageHardLimit` field in `ClientOptions` actually
> takes effect. If the bare `maxPageHardLimit` const is left here, setting
> `ClientOptions.MaxPageHardLimit` in tests will silently have no effect.

### Acceptance criteria

```
grep -n 'func (c \*Client) GetModelVideos' internal/pornhub/client.go
# must show: func (c *Client) GetModelVideos(ctx context.Context, modelURL string)

grep -n 'ctx\.Done()' internal/pornhub/client.go
# must show ≥ 3 matches (pre-loop check + top-of-loop check + sleep select)

grep -n 'time\.Sleep(pageDelay)' internal/pornhub/client.go
# must show 0 matches (replaced by select)

grep -n 'http\.NewRequestWithContext' internal/pornhub/client.go
# must show ≥ 1 match (in get())

grep -n '"context"' internal/pornhub/client.go
# must show 1 match (the import line)

grep -n 'c\.effectiveMaxPage()' internal/pornhub/client.go
# must show ≥ 1 match (the for-loop header)
```

---

## T3 — Lift maxPageHardLimit + ClientOptions struct + NewClientWithOptions constructor

**Requirements:** REQ-REL-2

<read_first>
- `internal/pornhub/client.go` lines 20–100 (constants, Client struct, NewClient)
- `internal/pornhub/client.go` line 411 (local const maxPageHardLimit — to be removed)
</read_first>

### Action

**3a. Lift `maxPageHardLimit` to package-level const**

Add after the existing package-level constants (after line 24 `pageDelay`):

```go
// maxPageHardLimit GetModelVideos 翻页绝对上限，防死循环
const maxPageHardLimit = 1000
```

Remove the local `const maxPageHardLimit = 1000` from inside `GetModelVideos`
(currently at line 411):
```go
// DELETE this line:
const maxPageHardLimit = 1000 // 防止死循环的兜底上限
```

**3b. Add `ClientOptions` exported struct**

Insert after the package-level constants and before the `var` block:

```go
// ClientOptions configures optional Client behaviour.
// The zero value of each field means "use the package default".
type ClientOptions struct {
    // PageDelay overrides the inter-page delay (default: 2 s).
    // Set to a small value in tests to avoid slow scans.
    PageDelay time.Duration
    // MaxPageHardLimit overrides the maximum pages to scan (default: 1000).
    MaxPageHardLimit int
    // JSEvalTimeout overrides the goja JS eval timeout (default: 15 s).
    JSEvalTimeout time.Duration
}
```

**3c. Add override fields to `Client` struct**

Extend the `Client` struct (currently lines 63–67) with three private fields:

```go
// Client Pornhub HTTP 客户端
type Client struct {
    httpClient *http.Client
    mu         sync.RWMutex // [FIXED: PH-2] 保护 cookie 并发读写
    cookie     string

    // configurable overrides (zero = use package-level default)
    jsEvalTimeout time.Duration
    pageDelayCfg  time.Duration
    maxPageCfg    int
    sleepFn       func(time.Duration) // reserved for future test injection
}
```

**3d. Update `NewClient` to initialise `sleepFn`**

In `NewClient`, after `c := &Client{...}` and before `return c`:

```go
c.sleepFn = time.Sleep // default; tests may override via NewClientWithOptions
```

**3e. Add `NewClientWithOptions` constructor**

Add immediately after `NewClient`:

```go
// NewClientWithOptions creates a Client with custom options.
// Use this in tests to set short timeouts and delays:
//
//	client := pornhub.NewClientWithOptions(pornhub.ClientOptions{
//	    JSEvalTimeout:    50 * time.Millisecond,
//	    PageDelay:        0,
//	    MaxPageHardLimit: 5,
//	})
func NewClientWithOptions(opts ClientOptions, cookie ...string) *Client {
    c := NewClient(cookie...)
    if opts.JSEvalTimeout > 0 {
        c.jsEvalTimeout = opts.JSEvalTimeout
    }
    if opts.PageDelay > 0 {
        c.pageDelayCfg = opts.PageDelay
    }
    if opts.MaxPageHardLimit > 0 {
        c.maxPageCfg = opts.MaxPageHardLimit
    }
    return c
}
```

### Acceptance criteria

```
grep -n '^const maxPageHardLimit' internal/pornhub/client.go
# must show exactly 1 match at package level (not inside a function)

grep -n 'type ClientOptions struct' internal/pornhub/client.go
# must show exactly 1 match

grep -n 'func NewClientWithOptions' internal/pornhub/client.go
# must show exactly 1 match

grep -n 'jsEvalTimeout' internal/pornhub/client.go
# must show ≥ 3 matches (struct field, options assignment, evalTimeout helper)

# The local const must be gone:
grep -n '^const maxPageHardLimit = 1000' internal/pornhub/client.go
# must show exactly 1 match AND it must NOT be inside a function body
# (verify by checking that the match is at column 0, not indented with tabs)
```

---

## T4 — Thread s.rootCtx into CheckPHModel and FullScanPHModel

**Requirements:** REQ-REL-2

<read_first>
- `internal/scheduler/phscheduler/check.go` line 60 (`CheckPHModel` call site)
- `internal/scheduler/phscheduler/check.go` line 202 (`FullScanPHModel` call site)
- `internal/scheduler/phscheduler/scheduler.go` lines 87–88 (`rootCtx` field)
</read_first>

### Action

**4a. `CheckPHModel` — line ~60**

Current (inside the retry loop, `for attempt := 1; attempt <= 3; attempt++`):
```go
videos, fetchErr = client.GetModelVideos(src.URL)
```

Replace with:
```go
videos, fetchErr = client.GetModelVideos(s.rootCtx, src.URL)
```

> No new import is needed in `check.go`. `s.rootCtx` is already of type
> `context.Context` (declared in `scheduler.go` which imports `"context"`).
> Passing a value does not require the caller to import the package.

**4b. `FullScanPHModel` — line 202**

Current:
```go
videos, err := client.GetModelVideos(src.URL)
```

Replace with:
```go
videos, err := client.GetModelVideos(s.rootCtx, src.URL)
```

### Acceptance criteria

```
grep -n 'GetModelVideos(s\.rootCtx' internal/scheduler/phscheduler/check.go
# must show exactly 2 matches (one in CheckPHModel, one in FullScanPHModel)

grep -n 'GetModelVideos(src\.URL)' internal/scheduler/phscheduler/check.go
# must show 0 matches (old bare call completely replaced)
```

---

## T5 — New test file: client_test.go

**Requirements:** REQ-REL-1, REQ-REL-2

<read_first>
- `internal/scheduler/dscheduler/mock_test.go` (test helper pattern)
- `internal/scheduler/dscheduler/check_test.go` (test structure pattern)
- `internal/pornhub/client.go` (functions under test — after T1–T3 edits)
</read_first>

### Action

Create `internal/pornhub/client_test.go` with `package pornhub_test`
(black-box, exercises only exported API).

Full file content:

```go
package pornhub_test

import (
    "context"
    "fmt"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"

    "video-subscribe-dl/internal/pornhub"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestGetVideoURL_JSEvalTimeout
//
// Serves a minimal Pornhub-shaped HTML page whose embedded JS contains an
// infinite loop (while(true){}). With a very short JSEvalTimeout the call
// must return an error mentioning "timeout" well within the test deadline.
// ─────────────────────────────────────────────────────────────────────────────

func TestGetVideoURL_JSEvalTimeout(t *testing.T) {
    // HTML with a fake flashvars variable that spins forever in goja
    infiniteLoopHTML := `<html><head><title>Test Video | Pornhub.com</title></head><body>
<div id="player">
<script type="text/javascript">
var flashvars_123 = (function(){
    while(true){}
    return { mediaDefinitions: [] };
})();
</script>
</div>
</body></html>`

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        fmt.Fprint(w, infiniteLoopHTML)
    }))
    defer srv.Close()

    client := pornhub.NewClientWithOptions(pornhub.ClientOptions{
        JSEvalTimeout: 150 * time.Millisecond, // very short for test speed
    })
    defer client.Close()

    start := time.Now()
    _, err := client.GetVideoURL(srv.URL + "/view_video.php?viewkey=phtest123")

    elapsed := time.Since(start)

    if err == nil {
        t.Fatal("expected error from JS eval timeout, got nil")
    }
    if !strings.Contains(err.Error(), "timeout") {
        t.Errorf("expected error to contain 'timeout', got: %v", err)
    }
    // Must return well within 2× the configured timeout, not hang for 15 s
    if elapsed > 2*time.Second {
        t.Errorf("GetVideoURL took too long (%v); expected ≤ 2s with short JSEvalTimeout", elapsed)
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// TestGetModelVideos_ContextCancel
//
// Fake server:
//   page 1 → returns one valid video card immediately
//   page 2 → blocks until the test context is cancelled
//
// The test cancels the context shortly after page 1 completes and verifies
// that GetModelVideos returns ctx.Err() promptly (not after the 30-s HTTP
// timeout or the 2-s page delay).
// ─────────────────────────────────────────────────────────────────────────────

func TestGetModelVideos_ContextCancel(t *testing.T) {
    // Minimal page template: one video card that satisfies extractVideos
    page1HTML := `<html><head><title>TestModel Porn Videos | Pornhub.com</title></head><body>
<div id="videoInVideoList">
  <li class="pcVideoListItem">
    <div class="videoPreviewBg">
      <a href="/view_video.php?viewkey=ph111111111" title="Video One">
        <img src="https://example.com/thumb.jpg" alt="Video One"/>
      </a>
    </div>
  </li>
</div>
</body></html>`

    // page2 blocks until the request context or a done channel fires
    blockCh := make(chan struct{})

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Query().Get("page") == "2" {
            // Block until blockCh is closed or request context done
            select {
            case <-blockCh:
            case <-r.Context().Done():
            }
            w.WriteHeader(http.StatusServiceUnavailable)
            return
        }
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        fmt.Fprint(w, page1HTML)
    }))
    defer srv.Close()
    defer close(blockCh) // unblock server goroutine on test exit

    client := pornhub.NewClientWithOptions(pornhub.ClientOptions{
        PageDelay:        1 * time.Millisecond, // near-zero delay so select fires quickly
        MaxPageHardLimit: 10,
    })
    defer client.Close()

    ctx, cancel := context.WithCancel(context.Background())

    // Cancel after a short window — enough for page 1 to complete but before
    // page 2 can respond.
    time.AfterFunc(200*time.Millisecond, cancel)

    start := time.Now()
    videos, err := client.GetModelVideos(ctx, srv.URL+"/model/testmodel/videos")
    elapsed := time.Since(start)

    // Must return an error (context cancelled)
    if err == nil {
        t.Errorf("expected context cancellation error, got nil (videos=%d)", len(videos))
    }
    if err != nil && err != context.Canceled {
        // Acceptable: ctx.Err() or a wrapped context.Canceled
        if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "cancel") {
            t.Errorf("expected context-related error, got: %v", err)
        }
    }
    // Must return well within the 30-s HTTP timeout window
    if elapsed > 5*time.Second {
        t.Errorf("GetModelVideos took %v; expected cancellation within 5s", elapsed)
    }
    // page 1 videos should still be present (partial result)
    if len(videos) == 0 {
        t.Errorf("expected at least 1 video from page 1, got 0")
    }
}
```

### Acceptance criteria

```
# File exists:
ls internal/pornhub/client_test.go

# Package declaration is pornhub_test (black-box):
grep -n '^package pornhub_test' internal/pornhub/client_test.go
# must show 1 match

# Both test functions are present:
grep -n 'func TestGetVideoURL_JSEvalTimeout' internal/pornhub/client_test.go
# must show 1 match

grep -n 'func TestGetModelVideos_ContextCancel' internal/pornhub/client_test.go
# must show 1 match

# Uses NewClientWithOptions:
grep -n 'NewClientWithOptions' internal/pornhub/client_test.go
# must show ≥ 2 matches (one per test)
```

---

## Execution order

Tasks must be executed in this order because each task depends on the
previous:

1. **T3** first — lift `maxPageHardLimit`, add `ClientOptions`, add struct
   fields. This establishes the data model everything else builds on.
2. **T1** — add `evalTimeout()` helper and update `GetVideoURL`. Depends on
   the `jsEvalTimeout` field from T3.
3. **T2** — add `"context"` import, change `GetModelVideos` signature, add
   loop checks, update `get()`. Depends on `effectivePageDelay()` /
   `effectiveMaxPage()` helpers (also added in this task), which need the
   struct fields from T3.
4. **T4** — update `check.go` call sites. Depends on T2 (new signature).
5. **T5** — write tests. Depends on T1 (JSEvalTimeout field via T3) and T2
   (context param) and T3 (NewClientWithOptions).

---

## Gotchas checklist

| # | Gotcha | Guard |
|---|--------|-------|
| 1 | `maxPageHardLimit` is currently a **local** const inside `GetModelVideos` (line 411) — easy to miss | T3 explicitly removes the local decl |
| 2 | `"context"` is **not** in `client.go` imports — compile error if omitted | T2 adds it |
| 3 | `check.go` does **not** need a new `context` import — just passes `s.rootCtx` | T4 notes this explicitly |
| 4 | `get()` signature change cascades to **5 call sites** — all must be updated | T2 lists every call site |
| 5 | `getWithCookie()` left unchanged — it still uses `http.NewRequest` (acceptable; out of REQ-REL-2 scope) | noted in T2 |
| 6 | `time.Sleep(pageDelay)` is the **last** statement in the loop — without the select the exit lag is up to 2 s after ctx.Done(); select reduces it to ~0 | T2 replaces with select |
| 7 | Page-1 fetch happens **before** the loop — need pre-loop ctx check to avoid starting pagination after cancellation | T2 adds pre-loop select |
| 8 | goja `vm.Interrupt()` is safe to call from any goroutine — no change to interrupt logic needed | documented in RESEARCH.md §10 |
| 9 | Tests in `package pornhub_test` (black-box) — they can only set options via exported `ClientOptions` / `NewClientWithOptions`, not by touching unexported fields directly | T5 uses `NewClientWithOptions` exclusively |
| 10 | The `for page := 2` loop uses `page <= maxPageHardLimit` as the bound — after T3 this must change to `page <= c.effectiveMaxPage()` | T2 performs the loop-bound update (step 2h) |
