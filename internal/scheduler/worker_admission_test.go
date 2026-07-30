package scheduler

import (
	"sync"
	"testing"
	"time"
)

func TestSchedulerPublicEntrypointsDoNotDispatchAfterStop(t *testing.T) {
	s, calls, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()

	s.Stop()
	before := calls.Load()

	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.CheckNow()
			s.ProcessAllPending()
			s.CheckOneSource(1)
			s.FullScanSource(1)
			s.SyncAll()
			s.RetryByID(1)
			s.RedownloadByID(1)
		}()
	}
	wait := make(chan struct{})
	go func() {
		wg.Wait()
		close(wait)
	}()
	select {
	case <-wait:
	case <-time.After(time.Second):
		t.Fatal("public entrypoints blocked after Stop")
	}

	if got := calls.Load(); got != before {
		t.Fatalf("public entrypoints dispatched network after Stop: before=%d after=%d", before, got)
	}
	select {
	case <-s.stopDone:
	default:
		t.Fatal("Stop completion signal is not closed")
	}
}

func TestSchedulerWorkerAdmissionAndStopAreSerialized(t *testing.T) {
	s, _, cleanup := newSchedulerLifecycleTest(t)
	defer cleanup()

	admissionEntered := make(chan struct{})
	releaseAdmission := make(chan struct{})
	s.workerAdmissionHook = func() {
		close(admissionEntered)
		<-releaseAdmission
	}

	entryReturned := make(chan struct{})
	go func() {
		s.CheckNow()
		close(entryReturned)
	}()
	select {
	case <-admissionEntered:
	case <-time.After(time.Second):
		t.Fatal("CheckNow did not enter worker admission hook")
	}

	stopReturned := make(chan struct{})
	go func() {
		s.Stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
		t.Fatal("Stop returned while worker admission held lifecycle lock")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseAdmission)
	select {
	case <-entryReturned:
	case <-time.After(time.Second):
		t.Fatal("CheckNow did not return after admission was released")
	}
	select {
	case <-stopReturned:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after worker admission completed")
	}

	s.CheckNow()
	select {
	case <-s.stopDone:
	default:
		t.Fatal("Stop completion signal is not closed")
	}
}
