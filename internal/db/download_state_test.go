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

func TestPrepareRedownloadOnlyAllowsCompletedOrRelocated(t *testing.T) {
	d := initMemoryDB(t)
	sourceID, err := d.CreateSource(&Source{Type: "channel", URL: "https://example.test/source", Name: "source", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	for _, tc := range []struct {
		status string
		allow  bool
	}{
		{"completed", true},
		{"relocated", true},
		{"pending", false},
		{"downloading", false},
		{"failed", false},
		{"permanent_failed", false},
		{"cancelled", false},
		{"deleted", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			id, err := d.CreateDownload(&Download{SourceID: sourceID, VideoID: "redownload-" + tc.status, Status: tc.status})
			if err != nil {
				t.Fatalf("CreateDownload: %v", err)
			}
			if _, err := d.Exec(`UPDATE downloads SET file_path='/tmp/video.mkv', file_size=123, thumb_path='/tmp/thumb.jpg', retry_count=2, last_error='old', error_message='old' WHERE id=?`, id); err != nil {
				t.Fatalf("seed download metadata: %v", err)
			}
			admitted, err := d.PrepareRedownload(id)
			if err != nil {
				t.Fatalf("PrepareRedownload: %v", err)
			}
			if admitted != tc.allow {
				t.Fatalf("PrepareRedownload(%s) = %v, want %v", tc.status, admitted, tc.allow)
			}
			var status, filePath, thumbPath, lastError, errorMessage string
			var fileSize int64
			var retryCount int
			if err := d.QueryRow(`SELECT status, file_path, file_size, thumb_path, retry_count, last_error, error_message FROM downloads WHERE id=?`, id).Scan(&status, &filePath, &fileSize, &thumbPath, &retryCount, &lastError, &errorMessage); err != nil {
				t.Fatalf("query download: %v", err)
			}
			if tc.allow {
				if status != "pending" || filePath != "" || fileSize != 0 || thumbPath != "" || retryCount != 0 || lastError != "" || errorMessage != "" {
					t.Fatalf("admitted redownload not fully reset: status=%q path=%q size=%d thumb=%q retry=%d last=%q err=%q", status, filePath, fileSize, thumbPath, retryCount, lastError, errorMessage)
				}
			} else if status != tc.status || filePath != "/tmp/video.mkv" || fileSize != 123 || retryCount != 2 {
				t.Fatalf("rejected %s changed: status=%q path=%q size=%d retry=%d", tc.status, status, filePath, fileSize, retryCount)
			}
		})
	}
}

func TestPrepareRestoreOnlyAllowsDeleted(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()
	sourceID, err := d.CreateSource(&Source{URL: "https://example.test", Name: "source", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status string
		allow  bool
	}{
		{"deleted", true}, {"pending", false}, {"downloading", false}, {"completed", false}, {"relocated", false}, {"failed", false}, {"permanent_failed", false}, {"cancelled", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			id, err := d.CreateDownload(&Download{SourceID: sourceID, VideoID: "restore-" + tc.status, Status: tc.status})
			if err != nil {
				t.Fatalf("CreateDownload: %v", err)
			}
			if _, err := d.Exec(`UPDATE downloads SET retry_count=2, last_error='old', error_message='old' WHERE id=?`, id); err != nil {
				t.Fatalf("seed errors: %v", err)
			}
			admitted, err := d.PrepareRestore(id)
			if err != nil || admitted != tc.allow {
				t.Fatalf("PrepareRestore(%s) = (%v, %v), want (%v, nil)", tc.status, admitted, err, tc.allow)
			}
			var status, lastError, errorMessage string
			var retryCount int
			if err := d.QueryRow(`SELECT status, retry_count, last_error, error_message FROM downloads WHERE id=?`, id).Scan(&status, &retryCount, &lastError, &errorMessage); err != nil {
				t.Fatalf("query state: %v", err)
			}
			if tc.allow {
				if status != "pending" || retryCount != 0 || lastError != "" || errorMessage != "" {
					t.Fatalf("restored state: status=%q retry=%d last=%q error=%q", status, retryCount, lastError, errorMessage)
				}
			} else if status != tc.status || retryCount != 2 || lastError != "old" || errorMessage != "old" {
				t.Fatalf("rejected state changed: status=%q retry=%d last=%q error=%q", status, retryCount, lastError, errorMessage)
			}
		})
	}
}

func TestPrepareDeleteOnlyAllowsExplicitSafeStates(t *testing.T) {
	d := initMemoryDB(t)
	defer d.Close()
	sourceID, err := d.CreateSource(&Source{URL: "https://example.test", Name: "source", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		status string
		allow  bool
	}{
		{"pending", true}, {"failed", true}, {"permanent_failed", true}, {"completed", true},
		{"relocated", true}, {"cancelled", true}, {"skipped", true}, {"charge_blocked", true},
		{"downloading", false}, {"deleted", false}, {"unknown_state", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			id, err := d.CreateDownload(&Download{SourceID: sourceID, VideoID: "delete-" + tc.status, Status: tc.status})
			if err != nil {
				t.Fatalf("CreateDownload: %v", err)
			}
			if _, err := d.Exec(`UPDATE downloads SET file_path='/tmp/video.mkv', file_size=123, thumb_path='/tmp/thumb.jpg' WHERE id=?`, id); err != nil {
				t.Fatalf("seed metadata: %v", err)
			}

			admitted, err := d.PrepareDelete(id)
			if err != nil || admitted != tc.allow {
				t.Fatalf("PrepareDelete(%s) = (%v, %v), want (%v, nil)", tc.status, admitted, err, tc.allow)
			}
			var status, filePath, thumbPath string
			var fileSize int64
			if err := d.QueryRow(`SELECT status, file_path, file_size, thumb_path FROM downloads WHERE id=?`, id).Scan(&status, &filePath, &fileSize, &thumbPath); err != nil {
				t.Fatalf("query state: %v", err)
			}
			if tc.allow {
				if status != "deleted" || filePath != "" || fileSize != 0 || thumbPath != "" {
					t.Fatalf("admitted delete state: status=%q path=%q size=%d thumb=%q", status, filePath, fileSize, thumbPath)
				}
			} else if status != tc.status || filePath != "/tmp/video.mkv" || fileSize != 123 || thumbPath != "/tmp/thumb.jpg" {
				t.Fatalf("rejected %s changed: status=%q path=%q size=%d thumb=%q", tc.status, status, filePath, fileSize, thumbPath)
			}
		})
	}
}
