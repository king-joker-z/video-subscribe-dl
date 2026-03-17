package scheduler

import (
	"fmt"
	"testing"
	"time"

	"video-subscribe-dl/internal/douyin"
)

// ============================
// checkDouyin — 剩余未覆盖分支
// ============================

// TestCheckDouyin_FirstScan_TriggersProcessAllPending 覆盖首次扫描后 go s.ProcessAllPending() 路径
func TestCheckDouyin_FirstScan_TriggersProcessAllPending(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fst_v1", Desc: "首次视频", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending on first scan, got %d", pending)
	}
}

// TestCheckDouyin_IncrementalScan_NoStop_LogsBranch 覆盖增量扫描完成但未 stopped 的日志分支
func TestCheckDouyin_IncrementalScan_NoStop_LogsBranch(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "inc_v1", Desc: "增量视频", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.db.UpdateSourceLatestVideoAt(src.ID, now-100)

	s.checkDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending, got %d", pending)
	}
}

// TestCheckDouyin_RiskControlError_BackoffRange 验证风控退避 sleep 在 [30s, 60s] 范围内
func TestCheckDouyin_RiskControlError_BackoffRange(t *testing.T) {
	var sleepDurations []time.Duration
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("HTTP 403 forbidden")},
			{result: nil, err: fmt.Errorf("HTTP 403 forbidden")},
		},
	}
	s := newTestScheduler(t, mock)
	s.sleepFn = func(d time.Duration) {
		sleepDurations = append(sleepDurations, d)
	}

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// 第一次风控错误触发退避 sleep，第二次触发 break
	if len(sleepDurations) != 1 {
		t.Errorf("expected 1 risk-control sleep, got %d", len(sleepDurations))
	}
	if len(sleepDurations) > 0 {
		if sleepDurations[0] < 30*time.Second || sleepDurations[0] > 60*time.Second {
			t.Errorf("risk control backoff %v not in [30s, 60s]", sleepDurations[0])
		}
	}
}

// TestCheckDouyin_CookieValidation_TriggeredAfter1Hour 覆盖 cookie 验证成功路径
func TestCheckDouyin_CookieValidation_TriggeredAfter1Hour(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "cookie valid",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "cv_v2", Desc: "Video", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)
	s.lastDouyinCookieCheck = time.Now().Add(-2 * time.Hour)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending, got %d", pending)
	}
}

// TestCheckDouyin_CookieValidation_FailureDoesNotBlock 覆盖 cookie 验证失败但不阻止继续
func TestCheckDouyin_CookieValidation_FailureDoesNotBlock(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: false,
		validateMsg:    "expired cookie",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "cv_fail_v1", Desc: "Video", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)
	s.lastDouyinCookieCheck = time.Time{}

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending despite cookie failure, got %d", pending)
	}
}

// TestCheckDouyin_Captcha_IsTreatedAsRiskControl 验证 "captcha" 错误被识别为风控
func TestCheckDouyin_Captcha_IsTreatedAsRiskControl(t *testing.T) {
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("captcha verification required")},
			{result: nil, err: fmt.Errorf("captcha verification required")},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// 连续 2 次风控后停止
	if mock.getCallCount() != 2 {
		t.Errorf("expected 2 calls (captcha risk control), got %d", mock.getCallCount())
	}
}

// TestCheckDouyin_Blocked_IsTreatedAsRiskControl 验证 "blocked" 错误被识别为风控
func TestCheckDouyin_Blocked_IsTreatedAsRiskControl(t *testing.T) {
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("IP blocked by server")},
			{result: nil, err: fmt.Errorf("IP blocked by server")},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	if mock.getCallCount() != 2 {
		t.Errorf("expected 2 calls (blocked risk control), got %d", mock.getCallCount())
	}
}

// TestCheckDouyin_Verify_IsTreatedAsRiskControl 验证 "verify" 错误被识别为风控
func TestCheckDouyin_Verify_IsTreatedAsRiskControl(t *testing.T) {
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("verify token failed")},
			{result: nil, err: fmt.Errorf("verify token failed")},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	if mock.getCallCount() != 2 {
		t.Errorf("expected 2 calls (verify risk control), got %d", mock.getCallCount())
	}
}

// ============================
// fullScanDouyin — 剩余未覆盖分支
// ============================

// TestFullScanDouyin_RiskControlBackoff_SleepCalled 验证风控退避 sleep 在正确范围
func TestFullScanDouyin_RiskControlBackoff_SleepCalled(t *testing.T) {
	now := time.Now().Unix()
	var sleepDurations []time.Duration
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_rc_v1", Desc: "Page1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "User"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			{result: nil, err: fmt.Errorf("HTTP 429 too many requests")},
			{result: nil, err: fmt.Errorf("HTTP 429 too many requests")},
		},
	}
	s := newTestScheduler(t, mock)
	s.sleepFn = func(d time.Duration) {
		sleepDurations = append(sleepDurations, d)
	}

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	// 应有: 1 page jitter + 1 risk control backoff (第二次 break)
	if len(sleepDurations) < 2 {
		t.Errorf("expected at least 2 sleeps (jitter + risk backoff), got %d", len(sleepDurations))
	}
	if len(sleepDurations) >= 2 {
		rcBackoff := sleepDurations[1]
		if rcBackoff < 30*time.Second || rcBackoff > 60*time.Second {
			t.Errorf("risk control backoff %v not in [30s, 60s]", rcBackoff)
		}
	}

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending (partial), got %d", pending)
	}
}

// TestFullScanDouyin_EmptyVideoList 覆盖全量扫描空列表路径
func TestFullScanDouyin_EmptyVideoList(t *testing.T) {
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos:  []douyin.DouyinVideo{},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 0 {
		t.Errorf("expected 0 pending for empty list, got %d", pending)
	}
}

// TestFullScanDouyin_Captcha_IsTreatedAsRiskControl 验证 captcha 在 fullScan 中也被识别为风控
func TestFullScanDouyin_Captcha_IsTreatedAsRiskControl(t *testing.T) {
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("captcha required")},
			{result: nil, err: fmt.Errorf("captcha required")},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	if mock.getCallCount() != 2 {
		t.Errorf("expected 2 calls (captcha risk control in fullScan), got %d", mock.getCallCount())
	}
}

// TestFullScanDouyin_ProcessAllPendingTriggered 覆盖 fullScan 创建 pending 后 goroutine 触发
func TestFullScanDouyin_ProcessAllPendingTriggered(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "proc_v1", Desc: "触发处理", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "User"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending, got %d", pending)
	}
}
