package bscheduler

import (
	"testing"
	"time"

	"video-subscribe-dl/internal/db"
)

func TestRecordProcessingFailureSchedulesAndConverges(t *testing.T) {
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	srcID, err := database.CreateSource(&db.Source{URL: "https://space.bilibili.com/1", Name: "source", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	dlID, err := database.CreateDownload(&db.Download{SourceID: srcID, VideoID: "BVretry", Title: "retry", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}

	s := New(Config{DB: database, DownloadDir: t.TempDir()})
	for attempt := 1; attempt <= 3; attempt++ {
		claimed, err := database.ClaimDownloadForProcessing(dlID)
		if err != nil || !claimed {
			t.Fatalf("claim %d = (%v, %v)", attempt, claimed, err)
		}
		before := time.Now().Unix()
		s.recordProcessingFailure(dlID, "BVretry", "retry failure")

		var status, errorMessage, lastError string
		var retryCount int
		var nextRetryAt int64
		if err := database.QueryRow(`SELECT status, retry_count, error_message, last_error, next_retry_at FROM downloads WHERE id=?`, dlID).Scan(&status, &retryCount, &errorMessage, &lastError, &nextRetryAt); err != nil {
			t.Fatal(err)
		}
		if retryCount != attempt || lastError != "retry failure" || errorMessage != "retry failure" {
			t.Fatalf("attempt %d row = status=%s retry=%d error=%q last=%q next=%d", attempt, status, retryCount, errorMessage, lastError, nextRetryAt)
		}
		if attempt < 3 {
			if status != "failed" || nextRetryAt <= before {
				t.Fatalf("attempt %d expected scheduled failed row, got status=%s next=%d", attempt, status, nextRetryAt)
			}
		} else if status != "permanent_failed" || nextRetryAt != 0 {
			t.Fatalf("third attempt expected permanent_failed without retry, got status=%s next=%d", status, nextRetryAt)
		}
	}
}
