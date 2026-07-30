package scheduler

import (
	"sync/atomic"
	"testing"
	"time"

	"video-subscribe-dl/internal/db"
)

func addWaitTestSources(t *testing.T, database *db.DB, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		if _, err := database.CreateSource(&db.Source{
			Type:    "channel",
			URL:     "https://example.test/source",
			Name:    "source",
			Enabled: true,
		}); err != nil {
			t.Fatalf("CreateSource: %v", err)
		}
	}
}

func waitForSchedulerWait(t *testing.T, entered <-chan time.Duration) time.Duration {
	t.Helper()
	select {
	case d := <-entered:
		return d
	case <-time.After(time.Second):
		t.Fatal("scheduler did not enter wait hook")
		return 0
	}
}

func TestSchedulerStopInterruptsCredentialWarmup(t *testing.T) {
	s, _, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()

	entered := make(chan time.Duration, 1)
	s.waitHook = func(d time.Duration) bool {
		entered <- d
		<-s.stopCh
		return false
	}
	s.credentialRefreshHook = func() bool { return true }
	var verifyCalls, pendingCalls atomic.Int32
	s.verifyCookieHook = func(string) { verifyCalls.Add(1) }
	s.processPendingHook = func() { pendingCalls.Add(1) }

	done := make(chan struct{})
	go func() {
		s.checkAll()
		close(done)
	}()
	if got := waitForSchedulerWait(t, entered); got != 30*time.Second {
		t.Fatalf("warm-up wait = %v, want 30s", got)
	}

	s.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("checkAll did not return after Stop interrupted warm-up")
	}
	if verifyCalls.Load() != 0 || pendingCalls.Load() != 0 {
		t.Fatalf("warm-up Stop dispatched follow-up work: verify=%d pending=%d", verifyCalls.Load(), pendingCalls.Load())
	}
}

func TestSchedulerStopInterruptsScheduledSourceInterval(t *testing.T) {
	s, _, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()
	addWaitTestSources(t, s.db, 2)

	entered := make(chan time.Duration, 1)
	s.waitHook = func(d time.Duration) bool {
		entered <- d
		<-s.stopCh
		return false
	}
	var checked, pendingCalls atomic.Int32
	s.checkSourceHook = func(db.Source) { checked.Add(1) }
	s.processPendingHook = func() { pendingCalls.Add(1) }

	done := make(chan struct{})
	go func() {
		s.checkSourceList([]db.Source{{ID: 1}, {ID: 2}})
		close(done)
	}()
	if got := waitForSchedulerWait(t, entered); got != 5*time.Second {
		t.Fatalf("scheduled interval = %v, want 5s", got)
	}

	s.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("checkSourceList did not return after Stop interrupted interval")
	}
	if checked.Load() != 1 || pendingCalls.Load() != 0 {
		t.Fatalf("scheduled Stop dispatched unexpected work: checked=%d pending=%d", checked.Load(), pendingCalls.Load())
	}
}

func TestSchedulerStopInterruptsManualSyncSourceInterval(t *testing.T) {
	s, _, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()
	addWaitTestSources(t, s.db, 2)

	entered := make(chan time.Duration, 1)
	s.waitHook = func(d time.Duration) bool {
		entered <- d
		<-s.stopCh
		return false
	}
	var verifyCalls, checked, pendingCalls atomic.Int32
	s.verifyCookieHook = func(string) { verifyCalls.Add(1) }
	s.checkSourceHook = func(db.Source) { checked.Add(1) }
	s.processPendingHook = func() { pendingCalls.Add(1) }

	done := make(chan struct{})
	go func() {
		s.checkAllForce()
		close(done)
	}()
	if got := waitForSchedulerWait(t, entered); got != 5*time.Second {
		t.Fatalf("manual interval = %v, want 5s", got)
	}

	s.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("checkAllForce did not return after Stop interrupted interval")
	}
	if verifyCalls.Load() != 1 || checked.Load() != 1 || pendingCalls.Load() != 0 {
		t.Fatalf("manual Stop dispatched unexpected work: verify=%d checked=%d pending=%d", verifyCalls.Load(), checked.Load(), pendingCalls.Load())
	}
}

func TestSchedulerWaitOrStopCompletesNormally(t *testing.T) {
	s, _, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()
	addWaitTestSources(t, s.db, 2)

	var waits []time.Duration
	s.waitHook = func(d time.Duration) bool {
		waits = append(waits, d)
		return true
	}
	var checked, pendingCalls atomic.Int32
	s.verifyCookieHook = func(string) {}
	s.checkSourceHook = func(db.Source) { checked.Add(1) }
	s.processPendingHook = func() { pendingCalls.Add(1) }

	s.checkSourceList([]db.Source{{ID: 1}, {ID: 2}})
	if len(waits) != 1 || waits[0] != 5*time.Second {
		t.Fatalf("normal scheduled waits = %v, want [5s]", waits)
	}
	if checked.Load() != 2 {
		t.Fatalf("normal scheduled check count = %d, want 2", checked.Load())
	}

	waits = nil
	s.checkAllForce()
	if len(waits) != 1 || waits[0] != 5*time.Second {
		t.Fatalf("normal manual waits = %v, want [5s]", waits)
	}
	if pendingCalls.Load() != 1 {
		t.Fatalf("normal manual pending calls = %d, want 1", pendingCalls.Load())
	}
}
