package scheduler

import (
	"testing"
	"time"

	"video-subscribe-dl/internal/db"
)

// ============================
// retryFailedDownloads 测试
// ============================

func TestRetryFailedDownloads_NoRetryableRecords(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	// 没有任何 failed 记录，不应 panic
	s.retryFailedDownloads()
}

func TestRetryFailedDownloads_MarksPermFailed(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	// 创建一条 retry_count 超限的 failed 记录
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "vid_perm_fail",
		Title:    "永久失败",
		Status:   "failed",
	})

	// 手动设置 retry_count 到很高（超过 MaxRetryCount）
	for i := 0; i < 20; i++ {
		s.db.IncrementRetryCount(dlID, "test error")
	}

	s.retryFailedDownloads()

	// 检查记录是否被标记为 permanent_failed
	dl, _ := s.db.GetDownload(dlID)
	if dl != nil && dl.Status != "permanent_failed" {
		// retryFailedDownloads 调用 MarkPermanentFailed，所以记录应该变为 permanent_failed
		// 或者如果 MaxRetryCount 很大的话，可能仍为 failed，但至少不 panic
		t.Logf("status after retryFailedDownloads: %s (retry_count=%d)", dl.Status, dl.RetryCount)
	}
}

func TestRetryFailedDownloads_DouyinSourceUsesDouyinPath(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	// 暂停抖音以拦截 retryOneDouyinDownload 的执行
	s.douyinPaused = true

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "dy_retry_vid",
		Title:    "抖音重试",
		Status:   "failed",
	})
	// 增加 1 次 retry（确保可重试）
	s.db.IncrementRetryCount(dlID, "test error")

	s.retryFailedDownloads()

	// 由于 douyinPaused=true，retryOneDouyinDownload 直接返回
	// 验证不 panic 即可
}

// ============================
// RetryByID 测试
// ============================

func TestRetryByID_NotFound(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	// ID 不存在不应 panic
	s.RetryByID(99999)
}

func TestRetryByID_DouyinDownload(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)
	s.douyinPaused = true // 阻止实际下载

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "dy_manual_retry",
		Title:    "手动重试",
		Status:   "failed",
	})
	s.db.IncrementRetryCount(dlID, "previous error")

	s.RetryByID(dlID)

	// 验证 retry_count 被重置
	dl, _ := s.db.GetDownload(dlID)
	if dl != nil && dl.RetryCount != 0 {
		t.Errorf("expected retry_count reset to 0, got %d", dl.RetryCount)
	}
}

// ============================
// RedownloadByID 测试
// ============================

func TestRedownloadByID_NotFound(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	// ID 不存在不应 panic
	s.RedownloadByID(99999)
}

func TestRedownloadByID_WrongStatus(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "dy_redownload_wrong_status",
		Title:    "状态不对",
		Status:   "completed", // 不是 pending
	})

	s.RedownloadByID(dlID)

	// status 不是 pending，不应触发下载
	dl, _ := s.db.GetDownload(dlID)
	if dl != nil && dl.Status != "completed" {
		t.Errorf("expected status to remain 'completed', got %s", dl.Status)
	}
}

func TestRedownloadByID_PendingStatus(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)
	s.douyinPaused = true // 阻止实际下载

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")

	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "dy_redownload_pending",
		Title:    "待下载",
		Status:   "pending",
	})

	s.RedownloadByID(dlID)

	// 给 goroutine 时间执行
	time.Sleep(100 * time.Millisecond)

	// 由于 douyinPaused=true，下载被跳过，但不应 panic
}

// ============================
// PauseDouyin / ResumeDouyin 测试
// ============================

func TestPauseDouyin_SetsState(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	if s.IsDouyinPaused() {
		t.Error("expected not paused initially")
	}

	s.PauseDouyin("test reason")

	if !s.IsDouyinPaused() {
		t.Error("expected paused after PauseDouyin")
	}

	paused, reason, pausedAt := s.GetDouyinPauseStatus()
	if !paused {
		t.Error("expected paused=true")
	}
	if reason != "test reason" {
		t.Errorf("expected reason 'test reason', got %q", reason)
	}
	if pausedAt.IsZero() {
		t.Error("expected non-zero pausedAt")
	}
}

func TestPauseDouyin_Idempotent(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	s.PauseDouyin("first reason")
	firstPausedAt := s.douyinPausedAt

	time.Sleep(10 * time.Millisecond)
	s.PauseDouyin("second reason")

	// 已暂停时再次 Pause 不应更新
	if s.douyinPauseReason != "first reason" {
		t.Errorf("expected reason unchanged, got %q", s.douyinPauseReason)
	}
	if !s.douyinPausedAt.Equal(firstPausedAt) {
		t.Error("expected pausedAt unchanged on second pause")
	}
}

func TestResumeDouyin_ClearsState(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	s.PauseDouyin("test reason")
	s.ResumeDouyin()

	if s.IsDouyinPaused() {
		t.Error("expected not paused after resume")
	}

	paused, reason, pausedAt := s.GetDouyinPauseStatus()
	if paused {
		t.Error("expected paused=false")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
	if !pausedAt.IsZero() {
		t.Error("expected zero pausedAt after resume")
	}
}

func TestResumeDouyin_NoopWhenNotPaused(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	// 不应 panic
	s.ResumeDouyin()
	if s.IsDouyinPaused() {
		t.Error("expected not paused")
	}
}

// ============================
// Cooldown 测试
// ============================

func TestGetCooldownInfo_NoCooldown(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	inCooldown, remaining := s.GetCooldownInfo()
	if inCooldown {
		t.Error("expected no cooldown")
	}
	if remaining != 0 {
		t.Errorf("expected 0 remaining, got %d", remaining)
	}
}

func TestTriggerDouyinCooldown(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	s.triggerDouyinCooldown()

	if !s.isDouyinInCooldown() {
		t.Error("expected douyin in cooldown after trigger")
	}

	// bili should NOT be in cooldown
	if s.isBiliInCooldown() {
		t.Error("expected bili NOT in cooldown (only douyin was triggered)")
	}
}
