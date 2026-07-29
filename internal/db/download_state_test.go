package db

import (
	"testing"
	"time"
)

func TestProcessingStateTransitionsAreCompareAndSet(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, err := d.CreateSource(&Source{URL: "https://example.test", Name: "source", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	dlID, err := d.CreateDownload(&Download{SourceID: srcID, VideoID: "BVstate", Title: "state", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}

	if applied, err := d.CompleteDownloadIfProcessing(dlID, "/tmp/video.mkv", 123); err != nil || applied {
		t.Fatalf("complete before claim = (%v, %v), want (false, nil)", applied, err)
	}
	claimed, err := d.ClaimDownloadForProcessing(dlID)
	if err != nil || !claimed {
		t.Fatalf("claim = (%v, %v), want (true, nil)", claimed, err)
	}
	if applied, err := d.CompleteDownloadIfProcessing(dlID, "/tmp/video.mkv", 123); err != nil || !applied {
		t.Fatalf("complete after claim = (%v, %v), want (true, nil)", applied, err)
	}
	if applied, err := d.MarkDownloadTerminalIfProcessing(dlID, "skipped", "late result"); err != nil || applied {
		t.Fatalf("late terminal write = (%v, %v), want (false, nil)", applied, err)
	}
}

func TestFailAndScheduleRetryIfProcessing(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()

	srcID, err := d.CreateSource(&Source{URL: "https://example.test", Name: "source", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	dlID, err := d.CreateDownload(&Download{SourceID: srcID, VideoID: "BVretry-state", Title: "retry", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}

	assertFailure := func(wantCount int, wantStatus string, minDelay, maxDelay time.Duration) {
		t.Helper()
		claimed, err := d.ClaimDownloadForProcessing(dlID)
		if err != nil || !claimed {
			t.Fatalf("claim before failure %d = (%v, %v)", wantCount, claimed, err)
		}
		before := time.Now()
		outcome, err := d.FailAndScheduleRetryIfProcessing(dlID, "network failed", 3)
		if err != nil || !outcome.Applied {
			t.Fatalf("failure %d = (%+v, %v)", wantCount, outcome, err)
		}
		if outcome.RetryCount != wantCount || outcome.Status != wantStatus {
			t.Fatalf("failure %d outcome = %+v", wantCount, outcome)
		}
		if wantStatus == "permanent_failed" {
			if outcome.NextRetryAt != 0 {
				t.Fatalf("permanent failure next_retry_at = %d, want 0", outcome.NextRetryAt)
			}
			return
		}
		delay := time.Unix(outcome.NextRetryAt, 0).Sub(before)
		if delay < minDelay || delay > maxDelay {
			t.Fatalf("failure %d delay %v outside [%v,%v]", wantCount, delay, minDelay, maxDelay)
		}
	}

	assertFailure(1, "failed", 14*time.Minute, 16*time.Minute)
	assertFailure(2, "failed", 29*time.Minute, 31*time.Minute)
	assertFailure(3, "permanent_failed", 0, 0)

	var status, errorMessage, lastError string
	var retryCount int
	var nextRetryAt int64
	if err := d.QueryRow(`SELECT status, retry_count, error_message, last_error, next_retry_at FROM downloads WHERE id=?`, dlID).Scan(&status, &retryCount, &errorMessage, &lastError, &nextRetryAt); err != nil {
		t.Fatal(err)
	}
	if status != "permanent_failed" || retryCount != 3 || nextRetryAt != 0 || errorMessage != "network failed" || lastError != "network failed" {
		t.Fatalf("final row = status=%s retry=%d next=%d error=%q last=%q", status, retryCount, nextRetryAt, errorMessage, lastError)
	}
}
