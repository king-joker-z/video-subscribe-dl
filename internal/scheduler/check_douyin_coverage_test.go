package scheduler

import (
	"fmt"
	"testing"
	"time"

	"video-subscribe-dl/internal/douyin"
)

// ============================
// 补充 checkDouyin/fullScanDouyin 覆盖率测试
// 目标：覆盖 check_douyin.go 中剩余未覆盖的分支
// ============================

// --- checkDouyin 未覆盖分支 ---

// TestCheckDouyin_GetProfileError_DoesNotBlock 覆盖 line 53-55:
// 当 source name 为空且 GetUserProfile 返回错误时，不影响后续视频检查
func TestCheckDouyin_GetProfileError_DoesNotBlock(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		profileErr:     fmt.Errorf("profile API unavailable"),
		profileResult:  nil,
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "prof_err_v1", Desc: "视频1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "AuthorName"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	// 空 name 触发 GetUserProfile 调用
	src := createTestSource(t, s.db, "", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// profile 获取失败，但视频仍然正常拉取
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download despite profile error, got %d", pending)
	}
}

// TestCheckDouyin_SourceNameFromVideoAuthor 覆盖 line 147-155:
// 当 source name 为空，且从视频列表获取到 author nickname 时更新 source name
func TestCheckDouyin_SourceNameFromVideoAuthor(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		// GetUserProfile 返回空 nickname，不更新 name
		profileResult: &douyin.DouyinUserProfile{Nickname: ""},
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "author_v1", Desc: "视频", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "VideoAuthor"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	// 空 name — profile 也返回空 nickname
	src := createTestSource(t, s.db, "", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// source name 应该从视频的 Author.Nickname 更新
	updated, _ := s.db.GetSource(src.ID)
	if updated.Name != "VideoAuthor" {
		t.Errorf("expected source name 'VideoAuthor' from video author, got %q", updated.Name)
	}
}

// TestCheckDouyin_EmptyAuthor_UsesSourceName 覆盖 line 153-155:
// 视频的 author nickname 为空时，uploader 使用 source name
func TestCheckDouyin_EmptyAuthor_UsesSourceName(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "noauthor_v1", Desc: "视频", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: ""}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "SourceName", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 1 {
		t.Fatalf("expected 1 download, got %d", len(downloads))
	}
	if downloads[0].Uploader != "SourceName" {
		t.Errorf("expected uploader 'SourceName' (fallback), got %q", downloads[0].Uploader)
	}
}

// TestCheckDouyin_NormalError_BackoffProgression 覆盖 line 118-120:
// 连续普通错误的指数退避值验证
func TestCheckDouyin_NormalError_BackoffProgression(t *testing.T) {
	now := time.Now().Unix()
	var sleepDurations []time.Duration
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			// 4 次普通网络错误
			{result: nil, err: fmt.Errorf("timeout 1")},
			{result: nil, err: fmt.Errorf("timeout 2")},
			{result: nil, err: fmt.Errorf("timeout 3")},
			{result: nil, err: fmt.Errorf("timeout 4")},
			// 第 5 次成功恢复
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "backoff_v1", Desc: "恢复", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)
	// 记录 sleep 调用
	s.sleepFn = func(d time.Duration) {
		sleepDurations = append(sleepDurations, d)
	}

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// 应该有 4 次退避 + 成功
	if mock.getCallCount() != 5 {
		t.Errorf("expected 5 calls, got %d", mock.getCallCount())
	}
	if len(sleepDurations) != 4 {
		t.Fatalf("expected 4 sleep calls, got %d", len(sleepDurations))
	}
	// 退避序列: 5s, 10s, 20s, 40s
	expectedBackoffs := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}
	for i, expected := range expectedBackoffs {
		if sleepDurations[i] != expected {
			t.Errorf("backoff[%d]: expected %v, got %v", i, expected, sleepDurations[i])
		}
	}

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download after recovery, got %d", pending)
	}
}

// --- fullScanDouyin 未覆盖分支 ---

// TestFullScanDouyin_UnnamedSource_FallbackName 覆盖 line 239-241:
// 当 source name 为空或"未命名"时，使用 secUID 前 8 字符作为名称
func TestFullScanDouyin_UnnamedSource_FallbackName(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "unnamed_v1", Desc: "视频", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "Author"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	// 空 name 触发 fallback
	src := createTestSource(t, s.db, "", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	// 视频应正常入库
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download, got %d", pending)
	}
}

// TestFullScanDouyin_SourceNameUnnamed_UsesFallback 覆盖 line 239-241:
// source name 为 "未命名" 时使用 fallback
func TestFullScanDouyin_SourceNameUnnamed_UsesFallback(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "unnamed2_v1", Desc: "视频", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: ""}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "未命名", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	// 应该使用 fallback name，视频应入库
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download with unnamed source, got %d", pending)
	}

	// uploader 应为 fallback name（douyin_ + secUID 前 8 字符）
	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 1 {
		t.Fatalf("expected 1 download, got %d", len(downloads))
	}
	expected := "douyin_MS4wLjAB"
	if downloads[0].Uploader != expected {
		t.Errorf("expected uploader %q (fallback), got %q", expected, downloads[0].Uploader)
	}
}

// TestFullScanDouyin_NormalError_BackoffProgression 覆盖 line 289-291:
// fullScan 中连续普通错误的退避和恢复
func TestFullScanDouyin_NormalError_BackoffProgression(t *testing.T) {
	now := time.Now().Unix()
	var sleepDurations []time.Duration
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			// 第一页成功
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_bp_v1", Desc: "Page1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "User"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			// 4 次连续普通错误
			{result: nil, err: fmt.Errorf("timeout 1")},
			{result: nil, err: fmt.Errorf("timeout 2")},
			{result: nil, err: fmt.Errorf("timeout 3")},
			{result: nil, err: fmt.Errorf("timeout 4")},
			// 第 5 次恢复
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_bp_v2", Desc: "Page2", CreateTime: now - 20, Duration: 30000, Author: douyin.DouyinUser{Nickname: "User"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)
	s.sleepFn = func(d time.Duration) {
		sleepDurations = append(sleepDurations, d)
	}

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	// 6 calls total: 1 success + 4 errors + 1 success
	if mock.getCallCount() != 6 {
		t.Errorf("expected 6 calls, got %d", mock.getCallCount())
	}

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 pending downloads, got %d", pending)
	}

	// 验证退避序列: page jitter + 4 error backoffs + no more jitter (hasMore=false)
	// sleepDurations[0] = page jitter (翻页间隔)
	// sleepDurations[1..4] = error backoffs (5s, 10s, 20s, 40s)
	if len(sleepDurations) < 5 {
		t.Fatalf("expected at least 5 sleep calls (1 jitter + 4 backoffs), got %d", len(sleepDurations))
	}
	// 验证错误退避值（跳过第一个 jitter）
	expectedBackoffs := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}
	for i, expected := range expectedBackoffs {
		actual := sleepDurations[i+1] // skip first page jitter
		if actual != expected {
			t.Errorf("error backoff[%d]: expected %v, got %v", i, expected, actual)
		}
	}
}

// TestFullScanDouyin_NormalError_BackoffCapAt60s 覆盖 line 289-291 中的 cap 分支:
// 确保退避值不超过 60s
func TestFullScanDouyin_NormalError_BackoffCapAt60s(t *testing.T) {
	now := time.Now().Unix()
	var sleepDurations []time.Duration
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			// 第一页成功
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_cap_v1", Desc: "P1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "User"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			// 5 次错误 → break
			{result: nil, err: fmt.Errorf("timeout 1")},
			{result: nil, err: fmt.Errorf("timeout 2")},
			{result: nil, err: fmt.Errorf("timeout 3")},
			{result: nil, err: fmt.Errorf("timeout 4")},
			{result: nil, err: fmt.Errorf("timeout 5")},
		},
	}
	s := newTestScheduler(t, mock)
	s.sleepFn = func(d time.Duration) {
		sleepDurations = append(sleepDurations, d)
	}

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download (partial), got %d", pending)
	}

	// 验证退避中无超过 60s 的值
	for i, d := range sleepDurations {
		if d > 60*time.Second {
			t.Errorf("backoff[%d] = %v exceeds 60s cap", i, d)
		}
	}
}

// TestFullScanDouyin_CreatesDownloadsWithCorrectFields 验证字段正确传递
func TestFullScanDouyin_CreatesDownloadsWithCorrectFields(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{
							AwemeID:    "fields_v1",
							Desc:       "完整字段测试",
							CreateTime: now - 100,
							Duration:   45000,
							Cover:      "https://example.com/cover.jpg",
							Author:     douyin.DouyinUser{Nickname: "FieldAuthor"},
						},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 1 {
		t.Fatalf("expected 1 download, got %d", len(downloads))
	}
	dl := downloads[0]
	if dl.VideoID != "fields_v1" {
		t.Errorf("expected VideoID 'fields_v1', got %q", dl.VideoID)
	}
	if dl.Title != "完整字段测试" {
		t.Errorf("expected Title '完整字段测试', got %q", dl.Title)
	}
	if dl.Uploader != "FieldAuthor" {
		t.Errorf("expected Uploader 'FieldAuthor', got %q", dl.Uploader)
	}
	if dl.Thumbnail != "https://example.com/cover.jpg" {
		t.Errorf("expected Thumbnail URL, got %q", dl.Thumbnail)
	}
	if dl.Duration != 45 { // 45000ms / 1000
		t.Errorf("expected Duration 45, got %d", dl.Duration)
	}
	if dl.Status != "pending" {
		t.Errorf("expected Status 'pending', got %q", dl.Status)
	}
}

// TestCheckDouyin_ProfileSuccessOverridesVideoAuthor 确保 profile 成功时使用 profile name
func TestCheckDouyin_ProfileSuccessOverridesVideoAuthor(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		profileResult:  &douyin.DouyinUserProfile{Nickname: "ProfileNick", UniqueID: "profile_id", FollowerCount: 1000, AwemeCount: 50},
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "prof_v1", Desc: "视频", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "VideoAuthorName"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "未命名", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// Profile 优先
	updated, _ := s.db.GetSource(src.ID)
	if updated.Name != "ProfileNick" {
		t.Errorf("expected 'ProfileNick' from profile, got %q", updated.Name)
	}
}
