package scheduler

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/notify"
)

func newSchedulerLifecycleTest(t *testing.T) (*Scheduler, *atomic.Int32, func()) {
	t.Helper()
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatalf("db.Init: %v", err)
	}

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
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
	s := New(database, dl, t.TempDir(), "")
	return s, &calls, func() {
		server.Close()
		dl.Stop()
		database.Close()
	}
}

func assertNotifierStopped(t *testing.T, s *Scheduler, calls *atomic.Int32) {
	t.Helper()
	select {
	case <-s.stopDone:
	default:
		t.Fatal("Scheduler.Stop returned before complete shutdown")
	}
	// Notifier.Stop waits for its worker before returning; Scheduler.Stop invokes it
	// before closing stopDone, so a closed stopDone proves the notifier worker exited.
	before := calls.Load()
	s.GetNotifier().Send(notify.EventDownloadFailed, "test", "after scheduler stop")
	if got := calls.Load(); got != before {
		t.Fatalf("Send after Scheduler.Stop dispatched network calls: before=%d after=%d", before, got)
	}
}

func TestSchedulerStopIsIdempotent(t *testing.T) {
	s, calls, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()

	s.Stop()
	s.Stop()
	assertNotifierStopped(t, s, calls)
}

func TestSchedulerStopIsConcurrentSafe(t *testing.T) {
	s, calls, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s.Stop()
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent Scheduler.Stop calls did not all return")
	}
	assertNotifierStopped(t, s, calls)
}

func TestSchedulerStartRegistrationAndStopAreSerialized(t *testing.T) {
	s, calls, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()

	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	s.startRegisterHook = func() {
		close(startEntered)
		<-releaseStart
	}

	startReturned := make(chan struct{})
	go func() {
		s.Start()
		close(startReturned)
	}()
	select {
	case <-startEntered:
	case <-time.After(time.Second):
		t.Fatal("Start did not enter worker-registration hook")
	}

	stopReturned := make(chan struct{})
	go func() {
		s.Stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
		t.Fatal("Stop returned while Start was still registering workers")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseStart)
	select {
	case <-startReturned:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after registration was released")
	}
	select {
	case <-stopReturned:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after Start registration completed")
	}
	assertNotifierStopped(t, s, calls)

	s.Start()
	assertNotifierStopped(t, s, calls)
}
