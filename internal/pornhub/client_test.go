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
