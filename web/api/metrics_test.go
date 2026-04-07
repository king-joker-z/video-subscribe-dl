package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/downloader"
)

// newTestDownloader creates a minimal Downloader suitable for unit tests.
// It uses MaxConcurrent=1 and a nil bilibili.Client (no real network needed
// for counter-only tests).
func newTestDownloader(t *testing.T) *downloader.Downloader {
	t.Helper()
	dl := downloader.New(downloader.Config{MaxConcurrent: 1}, (*bilibili.Client)(nil))
	t.Cleanup(func() { dl.Stop() })
	return dl
}

func TestHandlePrometheus_PlatformCounters(t *testing.T) {
	dl := newTestDownloader(t)

	// Increment bilibili completed counter once
	dl.AddPlatformCompleted("bilibili")

	h := NewMetricsHandler(dl)

	req := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
	w := httptest.NewRecorder()
	h.HandlePrometheus(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := w.Body.String()

	// Verify the new per-platform counter appears with correct label syntax
	want := `vsd_downloads_completed_total{platform="bilibili"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("response body missing %q\ngot:\n%s", want, body)
	}

	// Verify douyin and pornhub counters are present (value 0)
	for _, platform := range []string{"douyin", "pornhub"} {
		marker := `vsd_downloads_completed_total{platform="` + platform + `"}`
		if !strings.Contains(body, marker) {
			t.Errorf("response body missing metric for platform %q\ngot:\n%s", platform, body)
		}
	}

	// Verify HELP and TYPE lines for all three new metric families
	for _, metric := range []string{
		"vsd_downloads_completed_total",
		"vsd_downloads_failed_total",
		"vsd_scheduler_last_check_timestamp",
	} {
		if !strings.Contains(body, "# HELP "+metric) {
			t.Errorf("missing HELP line for %s", metric)
		}
		if !strings.Contains(body, "# TYPE "+metric) {
			t.Errorf("missing TYPE line for %s", metric)
		}
	}

	// Verify existing flat counters are preserved
	for _, flat := range []string{
		"vsd_downloader_completed_total",
		"vsd_downloader_failed_total",
	} {
		if !strings.Contains(body, flat) {
			t.Errorf("existing metric %q missing from response", flat)
		}
	}
}

func TestHandlePrometheus_SchedulerLastCheck_Zero(t *testing.T) {
	dl := newTestDownloader(t)
	h := NewMetricsHandler(dl)

	// Register a callback that returns zero time (scheduler not yet started)
	h.SetSchedulerLastCheckFunc("bilibili", func() time.Time { return time.Time{} })

	req := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
	w := httptest.NewRecorder()
	h.HandlePrometheus(w, req)

	body := w.Body.String()

	// Zero time must output 0, not -62135596800
	want := `vsd_scheduler_last_check_timestamp{platform="bilibili"} 0`
	if !strings.Contains(body, want) {
		t.Errorf("expected zero-time to render as 0, response body:\n%s", body)
	}
}

func TestHandlePrometheus_SchedulerLastCheck_NonZero(t *testing.T) {
	dl := newTestDownloader(t)
	h := NewMetricsHandler(dl)

	fixed := time.Unix(1700000000, 0)
	h.SetSchedulerLastCheckFunc("douyin", func() time.Time { return fixed })

	req := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
	w := httptest.NewRecorder()
	h.HandlePrometheus(w, req)

	body := w.Body.String()

	want := `vsd_scheduler_last_check_timestamp{platform="douyin"} 1700000000`
	if !strings.Contains(body, want) {
		t.Errorf("expected non-zero timestamp, response body:\n%s", body)
	}
}

func TestHandlePrometheus_MethodNotAllowed(t *testing.T) {
	dl := newTestDownloader(t)
	h := NewMetricsHandler(dl)

	req := httptest.NewRequest(http.MethodPost, "/api/metrics/prometheus", nil)
	w := httptest.NewRecorder()
	h.HandlePrometheus(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Result().StatusCode)
	}
}
