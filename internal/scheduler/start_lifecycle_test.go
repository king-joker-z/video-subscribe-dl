package scheduler

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerStartIsIdempotentWhileRunning(t *testing.T) {
	s, calls, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()

	var registrations atomic.Int32
	s.startRegisterHook = func() {
		registrations.Add(1)
	}

	s.Start()
	s.Start()
	if got := registrations.Load(); got != 1 {
		t.Fatalf("Start registered workers %d times, want 1", got)
	}

	s.Stop()
	assertNotifierStopped(t, s, calls)

	s.Start()
	if got := registrations.Load(); got != 1 {
		t.Fatalf("Start after Stop registered workers %d times, want 1", got)
	}
	assertNotifierStopped(t, s, calls)
}

func TestSchedulerConcurrentStartRegistersWorkersOnce(t *testing.T) {
	s, calls, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()

	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	var registrations atomic.Int32
	s.startRegisterHook = func() {
		registrations.Add(1)
		close(startEntered)
		<-releaseStart
	}

	const starters = 16
	var wg sync.WaitGroup
	for range starters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Start()
		}()
	}
	select {
	case <-startEntered:
	case <-time.After(time.Second):
		t.Fatal("no Start call entered worker registration")
	}

	close(releaseStart)
	returned := make(chan struct{})
	go func() {
		wg.Wait()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("concurrent Start calls did not all return")
	}
	if got := registrations.Load(); got != 1 {
		t.Fatalf("concurrent Start registered workers %d times, want 1", got)
	}

	s.Stop()
	assertNotifierStopped(t, s, calls)

	s.Start()
	if got := registrations.Load(); got != 1 {
		t.Fatalf("Start after Stop registered workers %d times, want 1", got)
	}
	assertNotifierStopped(t, s, calls)
}
