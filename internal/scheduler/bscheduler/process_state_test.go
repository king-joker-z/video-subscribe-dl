package bscheduler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/notify"
)

func newResultTestScheduler(t *testing.T) (*BiliScheduler, *db.DB, *downloader.Downloader, func()) {
	t.Helper()
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dl := downloader.New(downloader.Config{MaxConcurrent: 0}, bilibili.NewClient(""))
	n := notify.New(database)
	s := New(Config{DB: database, Downloader: dl, DownloadDir: t.TempDir(), Notifier: n})
	cleanup := func() {
		n.Stop()
		dl.Stop()
		database.Close()
	}
	return s, database, dl, cleanup
}

func createClaimedDownload(t *testing.T, database *db.DB, videoID string) int64 {
	t.Helper()
	sourceID, err := database.CreateSource(&db.Source{URL: "https://space.bilibili.com/1", Name: "source", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	dlID, err := database.CreateDownload(&db.Download{SourceID: sourceID, VideoID: videoID, Title: videoID, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := database.ClaimDownloadForProcessing(dlID)
	if err != nil || !claimed {
		t.Fatalf("claim = (%v, %v)", claimed, err)
	}
	return dlID
}

func waitForDownloadStatus(t *testing.T, database *db.DB, dlID int64, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := database.QueryRow(`SELECT status FROM downloads WHERE id=?`, dlID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	var status string
	_ = database.QueryRow(`SELECT status FROM downloads WHERE id=?`, dlID).Scan(&status)
	t.Fatalf("download %d status = %s, want %s", dlID, status, want)
}

func TestHandleDownloadResultPauseTimeoutKeepsProcessingUntilSuccess(t *testing.T) {
	s, database, dl, cleanup := newResultTestScheduler(t)
	defer cleanup()
	dl.Pause()
	dlID := createClaimedDownload(t, database, "BVpause-timeout")

	oldTimeout := downloadResultWaitTimeout
	downloadResultWaitTimeout = 10 * time.Millisecond
	defer func() { downloadResultWaitTimeout = oldTimeout }()

	results := make(chan *downloader.Result, 1)
	go s.handleDownloadResult(dlID, "BVpause-timeout", nil, nil, results, true, true, nil)
	time.Sleep(30 * time.Millisecond)

	var status string
	if err := database.QueryRow(`SELECT status FROM downloads WHERE id=?`, dlID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "downloading" {
		t.Fatalf("status after paused wait timeout = %s, want downloading", status)
	}

	results <- &downloader.Result{Success: true, FilePath: "/tmp/final.mp4", FileSize: 42}
	waitForDownloadStatus(t, database, dlID, "completed")
}

func TestLateFailureAfterTerminalStateDoesNotNotifyOrOverwrite(t *testing.T) {
	var notifications atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notifications.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	s, database, _, cleanup := newResultTestScheduler(t)
	defer cleanup()
	if err := database.SetSetting("notify_on_error", "true"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting("webhook_url", server.URL); err != nil {
		t.Fatal(err)
	}
	dlID := createClaimedDownload(t, database, "BVlate-failure")
	applied, err := database.MarkDownloadTerminalIfProcessing(dlID, "skipped", "pre-existing terminal state")
	if err != nil || !applied {
		t.Fatalf("terminal transition = (%v, %v)", applied, err)
	}

	results := make(chan *downloader.Result, 1)
	results <- &downloader.Result{Success: false, Error: errors.New("late worker failure")}
	s.handleDownloadResult(dlID, "BVlate-failure", nil, nil, results, true, true, nil)

	var status string
	if err := database.QueryRow(`SELECT status FROM downloads WHERE id=?`, dlID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" {
		t.Fatalf("late failure overwrote status: %s", status)
	}
	time.Sleep(50 * time.Millisecond)
	if got := notifications.Load(); got != 0 {
		t.Fatalf("late failure sent %d duplicate failure notifications, want 0", got)
	}
}

func TestSubmitDownloadFailureAtomicallySchedulesRetry(t *testing.T) {
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	stoppedDownloader := downloader.New(downloader.Config{MaxConcurrent: 0}, bilibili.NewClient(""))
	stoppedDownloader.Stop()
	s := New(Config{DB: database, Downloader: stoppedDownloader, DownloadDir: t.TempDir(), Notifier: notify.New(database)})
	defer s.notifier.Stop()

	sourceID, err := database.CreateSource(&db.Source{URL: "https://space.bilibili.com/1", Name: "source", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s.submitDownload(db.Source{ID: sourceID}, "BVsubmit-failure", 1, "title", "", "up", t.TempDir(), "", nil, nil)

	var status, errorMessage, lastError string
	var retryCount int
	var nextRetryAt int64
	if err := database.QueryRow(`SELECT status, retry_count, error_message, last_error, next_retry_at FROM downloads WHERE source_id=? AND video_id=?`, sourceID, "BVsubmit-failure").Scan(&status, &retryCount, &errorMessage, &lastError, &nextRetryAt); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || retryCount != 1 || nextRetryAt <= time.Now().Unix() || errorMessage == "" || lastError != errorMessage {
		t.Fatalf("submit failure row = status=%s retry=%d next=%d error=%q last=%q", status, retryCount, nextRetryAt, errorMessage, lastError)
	}
}
