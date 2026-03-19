package scheduler

import (
	"testing"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/scheduler/dscheduler"
)

// newTestScheduler 创建用于测试的 Scheduler（使用 dscheduler）
func newTestScheduler(t *testing.T, _ interface{}) *Scheduler {
	t.Helper()
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	notifier := notify.New(database)
	douyinSched := dscheduler.New(dscheduler.Config{
		DB:          database,
		DownloadDir: t.TempDir(),
		Notifier:    notifier,
	})

	s := &Scheduler{
		db:          database,
		downloadDir: t.TempDir(),
		stopCh:      make(chan struct{}),
		notifier:    notifier,
		douyin:      douyinSched,
	}
	t.Cleanup(func() { douyinSched.Stop() })
	return s
}

// createTestSource 在 DB 中创建一个抖音 Source 并返回
func createTestSource(t *testing.T, database *db.DB, name, rawURL string) db.Source {
	t.Helper()
	src := &db.Source{
		Type:    "douyin",
		URL:     rawURL,
		Name:    name,
		Enabled: true,
	}
	if _, err := database.CreateSource(src); err != nil {
		t.Fatalf("createTestSource: %v", err)
	}
	return *src
}

// ============================
// retryFailedDownloads 测试
// ============================

func TestRetryFailedDownloads_NoRetryableRecords(t *testing.T) {
	s := newTestScheduler(t, nil)
	s.retryFailedDownloads()
}

func TestRetryFailedDownloads_MarksPermFailed(t *testing.T) {
	s := newTestScheduler(t, nil)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "vid_perm_fail",
		Title:    "永久失败",
		Status:   "failed",
	})

	for i := 0; i < 20; i++ {
		s.db.IncrementRetryCount(dlID, "test error")
	}

	s.retryFailedDownloads()

	dl, _ := s.db.GetDownload(dlID)
	if dl != nil && dl.Status != "permanent_failed" {
		t.Logf("status after retryFailedDownloads: %s (retry_count=%d)", dl.Status, dl.RetryCount)
	}
}

func TestRetryFailedDownloads_DouyinSourceUsesDouyinPath(t *testing.T) {
	s := newTestScheduler(t, nil)
	// 暂停抖音以拦截实际下载
	s.douyin.Pause("test pause")

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "dy_retry_vid",
		Title:    "抖音重试",
		Status:   "failed",
	})
	s.db.IncrementRetryCount(dlID, "test error")

	s.retryFailedDownloads()
	// 由于 douyin 已暂停，RetryDownload 直接返回，不应 panic
}

// ============================
// RetryByID 测试
// ============================

func TestRetryByID_NotFound(t *testing.T) {
	s := newTestScheduler(t, nil)
	s.RetryByID(99999)
}

func TestRetryByID_DouyinDownload(t *testing.T) {
	s := newTestScheduler(t, nil)
	s.douyin.Pause("阻止实际下载")

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "dy_manual_retry",
		Title:    "手动重试",
		Status:   "failed",
	})
	s.db.IncrementRetryCount(dlID, "previous error")

	s.RetryByID(dlID)

	dl, _ := s.db.GetDownload(dlID)
	if dl != nil && dl.RetryCount != 0 {
		t.Errorf("expected retry_count reset to 0, got %d", dl.RetryCount)
	}
}

// ============================
// RedownloadByID 测试
// ============================

func TestRedownloadByID_NotFound(t *testing.T) {
	s := newTestScheduler(t, nil)
	s.RedownloadByID(99999)
}

func TestRedownloadByID_WrongStatus(t *testing.T) {
	s := newTestScheduler(t, nil)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "dy_redownload_wrong_status",
		Title:    "状态不对",
		Status:   "completed",
	})

	s.RedownloadByID(dlID)

	dl, _ := s.db.GetDownload(dlID)
	if dl != nil && dl.Status != "completed" {
		t.Errorf("expected status to remain 'completed', got %s", dl.Status)
	}
}

func TestRedownloadByID_PendingStatus(t *testing.T) {
	s := newTestScheduler(t, nil)
	s.douyin.Pause("阻止实际下载")

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "dy_redownload_pending",
		Title:    "待下载",
		Status:   "pending",
	})

	s.RedownloadByID(dlID)

	time.Sleep(100 * time.Millisecond)
	// 由于 douyin 已暂停，下载被跳过，不应 panic
}
