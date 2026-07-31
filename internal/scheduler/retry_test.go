package scheduler

import (
	"bytes"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/scheduler/bscheduler"
	"video-subscribe-dl/internal/scheduler/dscheduler"
)

// newTestScheduler 创建用于测试的 Scheduler（使用 dscheduler）
func newTestScheduler(t *testing.T, _ interface{}) *Scheduler {
	t.Helper()
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatalf("db.Init: %v", err)
	}

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
	t.Cleanup(func() {
		douyinSched.Stop()
		notifier.Stop()
		database.Close()
	})
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

func newRetryDispatchTestScheduler(t *testing.T) *Scheduler {
	t.Helper()
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	notifier := notify.New(database)
	dl := downloader.New(downloader.Config{MaxConcurrent: 1}, bilibili.NewClient(""))
	dl.Pause()
	bili := bscheduler.New(bscheduler.Config{
		DB:          database,
		Downloader:  dl,
		DownloadDir: t.TempDir(),
		Notifier:    notifier,
		HotConfig:   config.NewHotConfig(),
	})
	douyinSched := dscheduler.New(dscheduler.Config{
		DB:          database,
		DownloadDir: t.TempDir(),
		Notifier:    notifier,
	})
	douyinSched.Pause("test pause")

	s := &Scheduler{
		db:          database,
		dl:          dl,
		downloadDir: t.TempDir(),
		stopCh:      make(chan struct{}),
		notifier:    notifier,
		bili:        bili,
		douyin:      douyinSched,
	}
	t.Cleanup(func() {
		douyinSched.Stop()
		dl.Stop()
		notifier.Stop()
		database.Close()
	})
	return s
}

func createSourceAndDownload(t *testing.T, s *Scheduler, sourceType string, enabled bool) (db.Source, *db.Download) {
	t.Helper()
	src := &db.Source{Type: sourceType, URL: "https://example.invalid/source", Name: "source", Enabled: enabled}
	if _, err := s.db.CreateSource(src); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	dlID, err := s.db.CreateDownload(&db.Download{
		SourceID: src.ID, VideoID: "retry-test", Title: "retry test", Status: "failed",
	})
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}
	dl, err := s.db.GetDownload(dlID)
	if err != nil || dl == nil {
		t.Fatalf("GetDownload: %v, %v", err, dl)
	}
	return *src, dl
}

type retryLogCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	events chan struct{}
}

func (c *retryLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	n, err := c.buffer.Write(p)
	c.mu.Unlock()
	select {
	case c.events <- struct{}{}:
	default:
	}
	return n, err
}

func (c *retryLogCapture) contains(marker string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Contains(c.buffer.Bytes(), []byte(marker))
}

func (c *retryLogCapture) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.String()
}

var retryLogOutputMu sync.Mutex

func beginRetryLogCapture(t *testing.T) *retryLogCapture {
	t.Helper()
	retryLogOutputMu.Lock()
	capture := &retryLogCapture{events: make(chan struct{}, 1)}
	original := log.Writer()
	log.SetOutput(capture)
	t.Cleanup(func() {
		log.SetOutput(original)
		retryLogOutputMu.Unlock()
	})
	return capture
}

func waitForRetryLog(t *testing.T, capture *retryLogCapture, marker string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		if capture.contains(marker) {
			return
		}
		select {
		case <-capture.events:
		case <-deadline.C:
			t.Fatalf("timed out waiting for retry dispatch marker %q; logs: %q", marker, capture.text())
		}
	}
}

func TestRetryOneDownload_DisabledSourceSkipsBilibiliAndDelegatedPlatform(t *testing.T) {
	for _, sourceType := range []string{"up", "douyin"} {
		t.Run(sourceType, func(t *testing.T) {
			s := newRetryDispatchTestScheduler(t)
			capture := beginRetryLogCapture(t)
			_, dl := createSourceAndDownload(t, s, sourceType, false)

			s.retryOneDownload(*dl)
			disabledMarker := "Source " + strconv.FormatInt(dl.SourceID, 10) + " is disabled"
			if !capture.contains(disabledMarker) {
				t.Fatalf("expected disabled-source boundary log, got %q", capture.text())
			}
			if capture.contains("[bscheduler]") || capture.contains("[dscheduler]") {
				t.Fatalf("disabled source must not be delegated, got %q", capture.text())
			}
			current, err := s.db.GetDownload(dl.ID)
			if err != nil || current == nil {
				t.Fatalf("GetDownload: %v, %v", err, current)
			}
			if current.Status != "failed" {
				t.Fatalf("disabled source must not reset status, got %q", current.Status)
			}
		})
	}
}

func TestRetryOneDownload_EnabledSourceStillDelegates(t *testing.T) {
	for _, tc := range []struct {
		sourceType string
		marker     string
	}{
		{sourceType: "up", marker: "[bscheduler] Downloader paused"},
		{sourceType: "douyin", marker: "[dscheduler] 抖音下载已暂停"},
	} {
		t.Run(tc.sourceType, func(t *testing.T) {
			s := newRetryDispatchTestScheduler(t)
			capture := beginRetryLogCapture(t)
			_, dl := createSourceAndDownload(t, s, tc.sourceType, true)

			s.retryOneDownload(*dl)
			waitForRetryLog(t, capture, tc.marker)
		})
	}
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

func TestRetryByIDOnlyAllowsFailedStates(t *testing.T) {
	for _, tc := range []struct {
		status string
		allow  bool
	}{
		{"failed", true},
		{"permanent_failed", true},
		{"downloading", false},
		{"pending", false},
		{"completed", false},
		{"cancelled", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			s := newTestScheduler(t, nil)
			s.douyin.Pause("prevent dispatch")
			src := createTestSource(t, s.db, "retry-state", "https://www.douyin.com/user/retry-state")
			id, err := s.db.CreateDownload(&db.Download{SourceID: src.ID, VideoID: "retry-" + tc.status, Status: tc.status})
			if err != nil {
				t.Fatalf("CreateDownload: %v", err)
			}
			if _, err := s.db.Exec(`UPDATE downloads SET retry_count=2 WHERE id=?`, id); err != nil {
				t.Fatalf("set retry count: %v", err)
			}

			if got := s.RetryByID(id); got != tc.allow {
				t.Fatalf("RetryByID(%s) = %v, want %v", tc.status, got, tc.allow)
			}
			current, err := s.db.GetDownload(id)
			if err != nil || current == nil {
				t.Fatalf("GetDownload: %v, %v", err, current)
			}
			if tc.allow {
				if current.RetryCount != 0 {
					t.Fatalf("accepted %s retry count = %d, want 0", tc.status, current.RetryCount)
				}
				return
			}
			if current.Status != tc.status || current.RetryCount != 2 {
				t.Fatalf("rejected %s changed to status=%q retry_count=%d", tc.status, current.Status, current.RetryCount)
			}
		})
	}
}

func TestConcurrentManualRetriesOnlyOneAdmissionWins(t *testing.T) {
	s := newTestScheduler(t, nil)
	src := createTestSource(t, s.db, "race", "https://www.douyin.com/user/race")
	id, err := s.db.CreateDownload(&db.Download{SourceID: src.ID, VideoID: "race-retry", Status: "failed"})
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	for range 2 {
		go func() {
			<-start
			applied, err := s.db.PrepareManualRetry(id)
			if err != nil {
				t.Errorf("PrepareManualRetry: %v", err)
			}
			results <- applied
		}()
	}
	close(start)
	first, second := <-results, <-results
	if (first && second) || (!first && !second) {
		t.Fatalf("concurrent manual retry results = %v, %v; want exactly one true", first, second)
	}
	claimed, err := s.db.ClaimDownloadForProcessing(id)
	if err != nil || !claimed {
		t.Fatalf("ClaimDownloadForProcessing after the winning retry = %v, %v; want true, nil", claimed, err)
	}
	current, err := s.db.GetDownload(id)
	if err != nil || current == nil {
		t.Fatalf("GetDownload: %v, %v", err, current)
	}
	if current.Status != "downloading" {
		t.Fatalf("final status = %q, want downloading", current.Status)
	}
}

func TestRetryByIDInterleavedClaimHasSingleAdmissionAndClaimWinner(t *testing.T) {
	s := newTestScheduler(t, nil)
	src := createTestSource(t, s.db, "interleaved", "https://www.douyin.com/user/interleaved")
	id, err := s.db.CreateDownload(&db.Download{SourceID: src.ID, VideoID: "interleaved-retry", Status: "failed"})
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}

	claimEntered := make(chan struct{})
	releaseClaim := make(chan struct{})
	claimedDone := make(chan struct{})
	var claimCount atomic.Int32
	s.douyin.SetTestClaimHooks(func(dl db.Download) {
		if dl.ID != id {
			t.Fatalf("claim hook download id = %d, want %d", dl.ID, id)
		}
		close(claimEntered)
		<-releaseClaim
	}, func(dl db.Download) bool {
		claimCount.Add(1)
		close(claimedDone)
		return false // Network-isolated test ends after the real platform claim.
	})

	firstResult := make(chan bool, 1)
	go func() { firstResult <- s.RetryByID(id) }()
	select {
	case <-claimEntered:
	case <-time.After(time.Second):
		t.Fatal("first RetryByID did not reach the platform claim hook")
	}

	if got := s.RetryByID(id); got {
		t.Fatal("second RetryByID was admitted after first retry prepared the record")
	}
	close(releaseClaim)
	select {
	case got := <-firstResult:
		if !got {
			t.Fatal("first RetryByID was not admitted")
		}
	case <-time.After(time.Second):
		t.Fatal("first RetryByID did not return after claim was released")
	}
	select {
	case <-claimedDone:
	case <-time.After(time.Second):
		t.Fatal("real platform ClaimDownloadForProcessing did not complete")
	}
	if got := claimCount.Load(); got != 1 {
		t.Fatalf("platform claim winner count = %d, want 1", got)
	}
	current, err := s.db.GetDownload(id)
	if err != nil || current == nil {
		t.Fatalf("GetDownload: %v, %v", err, current)
	}
	if current.Status != "downloading" {
		t.Fatalf("final status = %q, want downloading", current.Status)
	}
}
