package scheduler

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/notify"
)

func TestSchedulerStopStopsNotifier(t *testing.T) {
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	defer database.Close()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	for key, value := range map[string]string{
		"notify_type":     string(notify.NotifyWebhook),
		"webhook_url":     server.URL,
		"notify_on_error": "true",
	} {
		if err := database.SetSetting(key, value); err != nil {
			t.Fatalf("SetSetting(%s): %v", key, err)
		}
	}

	dl := downloader.New(downloader.Config{MaxConcurrent: 0}, bilibili.NewClient(""))
	defer dl.Stop()
	s := New(database, dl, t.TempDir(), "")

	s.Stop()
	s.GetNotifier().Send(notify.EventDownloadFailed, "test", "after scheduler stop")
	if got := calls.Load(); got != 0 {
		t.Fatalf("Send after Scheduler.Stop dispatched %d network calls", got)
	}
}
