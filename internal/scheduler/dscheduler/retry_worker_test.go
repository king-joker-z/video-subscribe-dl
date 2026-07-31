package dscheduler

import (
	"testing"

	"video-subscribe-dl/internal/db"
)

func createRetryWorkerDownload(t *testing.T, s *DouyinScheduler, videoID string) db.Download {
	t.Helper()
	src := createTestSource(t, s.db, "retry-worker-source", "https://example.test/douyin")
	id, err := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  videoID,
		Title:    "retry worker test",
		Status:   "pending",
	})
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}
	dl, err := s.db.GetDownload(id)
	if err != nil || dl == nil {
		t.Fatalf("GetDownload: download=%v err=%v", dl, err)
	}
	return *dl
}

func TestRetryDownloadReturnsImmediatelyWhenWorkerSemFull(t *testing.T) {
	s := newTestDouyinScheduler(t, &mockDouyinAPI{})
	s.workerSem = make(chan struct{}, 1)
	s.workerSem <- struct{}{}
	dl := createRetryWorkerDownload(t, s, "retry-worker-full")

	claimed := make(chan struct{})
	s.SetTestClaimHooks(func(db.Download) { close(claimed) }, func(db.Download) bool { return false })
	s.RetryDownload(dl)
	select {
	case <-claimed:
		t.Fatal("RetryDownload claimed before a worker slot was released")
	default:
	}

	<-s.workerSem
	<-claimed
	s.wg.Wait()
}

func TestRetryDownloadWaitingForWorkerSemIsTrackedByStop(t *testing.T) {
	s := newTestDouyinScheduler(t, &mockDouyinAPI{})
	s.workerSem = make(chan struct{}, 1)
	s.workerSem <- struct{}{}
	dl := createRetryWorkerDownload(t, s, "retry-worker-stop")
	s.SetTestClaimHooks(nil, func(db.Download) bool { return false })

	s.RetryDownload(dl)
	stopDone := make(chan struct{})
	go func() {
		s.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		t.Fatal("Stop returned while RetryDownload was still waiting for workerSem")
	default:
	}

	<-s.workerSem
	<-stopDone
}

func TestRetryDownloadExecutesWithAvailableWorkerSlot(t *testing.T) {
	s := newTestDouyinScheduler(t, &mockDouyinAPI{})
	s.workerSem = make(chan struct{}, 1)
	dl := createRetryWorkerDownload(t, s, "retry-worker-available")

	claimed := make(chan struct{})
	s.SetTestClaimHooks(func(db.Download) { close(claimed) }, func(db.Download) bool { return false })
	s.RetryDownload(dl)
	<-claimed
	s.wg.Wait()

	var status string
	if err := s.db.QueryRow(`SELECT status FROM downloads WHERE id=?`, dl.ID).Scan(&status); err != nil {
		t.Fatalf("query claimed download: %v", err)
	}
	if status != "downloading" {
		t.Fatalf("RetryDownload status=%q, want downloading", status)
	}
}
