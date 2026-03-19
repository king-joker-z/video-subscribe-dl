package dscheduler

import (
	"fmt"
	"testing"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
)

// ============================
// CheckDouyin 测试
// ============================

func TestCheckDouyin_SkipWhenPaused(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)
	s.Pause("test pause")

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.CheckDouyin(src)

	if mock.getCallCount() > 0 {
		t.Errorf("expected 0 API calls when paused, got %d", mock.getCallCount())
	}
}

func TestCheckDouyin_URLResolveFail(t *testing.T) {
	mock := &mockDouyinAPI{
		resolveErr: fmt.Errorf("invalid URL"),
	}
	s := newTestDouyinScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://v.douyin.com/invalid")
	s.CheckDouyin(src)

	if mock.getCallCount() > 0 {
		t.Errorf("expected 0 video calls on URL resolve failure, got %d", mock.getCallCount())
	}
}

func TestCheckDouyin_FirstScan_CreatesDownloads(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "vid_001", Desc: "视频1", CreateTime: now - 100, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "vid_002", Desc: "视频2", CreateTime: now - 200, Duration: 60000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "vid_003", Desc: "视频3", CreateTime: now - 300, Duration: 15000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   false,
					MaxCursor: 0,
				},
			},
		},
	}
	s := newTestDouyinScheduler(t, mock)
	// 触发一次 Cookie 检测，避免 lastCookieCheck 影响逻辑
	s.lastCookieCheck = time.Now().Add(-2 * time.Hour)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.CheckDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 3 {
		t.Errorf("expected 3 pending downloads, got %d", pending)
	}

	latestAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	if latestAt != now-100 {
		t.Errorf("expected latestVideoAt=%d, got %d", now-100, latestAt)
	}
}

func TestCheckDouyin_IncrementalScan_StopsAtKnown(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "vid_new1", Desc: "新视频1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "vid_new2", Desc: "新视频2", CreateTime: now - 20, Duration: 60000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "vid_old", Desc: "旧视频", CreateTime: now - 500, Duration: 15000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "vid_should_not_see", Desc: "不应看到", CreateTime: now - 1000, Duration: 10000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestDouyinScheduler(t, mock)
	s.lastCookieCheck = time.Now() // 跳过 Cookie 检测

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.db.UpdateSourceLatestVideoAt(src.ID, now-100)

	s.CheckDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 pending downloads (stopped at known), got %d", pending)
	}

	if mock.getCallCount() != 1 {
		t.Errorf("expected 1 GetUserVideos call, got %d", mock.getCallCount())
	}
}

func TestCheckDouyin_DeduplicatesExistingVideos(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "vid_dup", Desc: "已存在", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "vid_new", Desc: "新视频", CreateTime: now - 20, Duration: 60000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestDouyinScheduler(t, mock)
	s.lastCookieCheck = time.Now()

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "vid_dup",
		Title:    "已存在",
		Status:   "done",
	})

	s.CheckDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download (dedup), got %d", pending)
	}
}

func TestCheckDouyin_Pagination(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "p1_v1", Desc: "Page1 Video1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "p2_v1", Desc: "Page2 Video1", CreateTime: now - 20, Duration: 60000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   false,
					MaxCursor: 0,
				},
			},
		},
	}
	s := newTestDouyinScheduler(t, mock)
	s.lastCookieCheck = time.Now()

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.CheckDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 pending downloads across pages, got %d", pending)
	}
	if mock.getCallCount() != 2 {
		t.Errorf("expected 2 GetUserVideos calls, got %d", mock.getCallCount())
	}
}

func TestCheckDouyin_FirstScanPageLimit(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos:    []douyin.DouyinVideo{{AwemeID: "p1_v1", Desc: "Page1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}}},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			{
				result: &douyin.UserVideosResult{
					Videos:    []douyin.DouyinVideo{{AwemeID: "p2_v1", Desc: "Page2", CreateTime: now - 20, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}}},
					HasMore:   true,
					MaxCursor: 200,
				},
			},
			{
				result: &douyin.UserVideosResult{
					Videos:  []douyin.DouyinVideo{{AwemeID: "p3_v1", Desc: "Page3", CreateTime: now - 30, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}}},
					HasMore: false,
				},
			},
		},
	}
	s := newTestDouyinScheduler(t, mock)
	s.lastCookieCheck = time.Now()
	s.db.SetSetting("first_scan_pages", "2")

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.CheckDouyin(src)

	if mock.getCallCount() != 2 {
		t.Errorf("expected 2 GetUserVideos calls (page limit), got %d", mock.getCallCount())
	}
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 pending downloads, got %d", pending)
	}
}

func TestCheckDouyin_EmptyDesc_FallbackTitle(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "vid_notitle", Desc: "", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestDouyinScheduler(t, mock)
	s.lastCookieCheck = time.Now()

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.CheckDouyin(src)

	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 1 {
		t.Fatalf("expected 1 download, got %d", len(downloads))
	}
	expected := "douyin_vid_notitle"
	if downloads[0].Title != expected {
		t.Errorf("expected fallback title %q, got %q", expected, downloads[0].Title)
	}
}

func TestCheckDouyin_UpdatesSourceName(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "vid_001", Desc: "视频", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "RealName"}},
					},
					HasMore: false,
				},
			},
		},
		profileResult: &douyin.DouyinUserProfile{Nickname: "ProfileName"},
	}
	s := newTestDouyinScheduler(t, mock)
	s.lastCookieCheck = time.Now()

	src := createTestSource(t, s.db, "未命名", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.CheckDouyin(src)

	updated, _ := s.db.GetSource(src.ID)
	if updated.Name != "ProfileName" {
		t.Errorf("expected source name 'ProfileName', got %q", updated.Name)
	}
}

func TestCheckDouyin_ClientIsClosed(t *testing.T) {
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: &douyin.UserVideosResult{Videos: []douyin.DouyinVideo{}, HasMore: false}},
		},
	}
	s := newTestDouyinScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.CheckDouyin(src)

	if !mock.isClosed() {
		t.Error("expected DouyinAPI.Close() to be called")
	}
}

func TestCheckDouyin_CookieValidation_Invalid(t *testing.T) {
	mock := &mockDouyinAPI{
		validateResult: false,
		validateMsg:    "Cookie 已过期",
		userVideoPages: []mockVideoPage{
			{result: &douyin.UserVideosResult{Videos: []douyin.DouyinVideo{}, HasMore: false}},
		},
	}
	s := newTestDouyinScheduler(t, mock)
	// 让 lastCookieCheck 足够旧，触发验证
	s.lastCookieCheck = time.Now().Add(-2 * time.Hour)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.CheckDouyin(src)

	st := s.GetDouyinCookieStatus()
	if st.Valid {
		t.Error("expected cookie marked invalid after failed validation")
	}
	if st.Msg != "Cookie 已过期" {
		t.Errorf("expected msg 'Cookie 已过期', got %q", st.Msg)
	}
}

func TestCheckDouyin_CookieValidation_Valid(t *testing.T) {
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: &douyin.UserVideosResult{Videos: []douyin.DouyinVideo{}, HasMore: false}},
		},
	}
	s := newTestDouyinScheduler(t, mock)
	// 先设为无效
	s.SetCookieInvalid("old reason")
	s.lastCookieCheck = time.Now().Add(-2 * time.Hour)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.CheckDouyin(src)

	st := s.GetDouyinCookieStatus()
	if !st.Valid {
		t.Error("expected cookie marked valid after successful validation")
	}
}

// ============================
// CheckDouyinMix 测试
// ============================

func TestCheckDouyinMix_SkipWhenPaused(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)
	s.Pause("test pause")

	src := db.Source{
		ID:   1,
		Type: "douyin_mix",
		URL:  "https://www.douyin.com/collection/123456",
		Name: "测试合集",
	}
	s.CheckDouyinMix(src)

	if mock.getCallCount() > 0 {
		t.Errorf("expected 0 calls when paused, got %d", mock.getCallCount())
	}
}

func TestCheckDouyinMix_FetchError(t *testing.T) {
	mock := &mockDouyinAPI{
		mixVideoErr: fmt.Errorf("fetch error"),
	}
	s := newTestDouyinScheduler(t, mock)

	src := createTestSource(t, s.db, "测试合集", "https://www.douyin.com/collection/123456")
	src.Type = "douyin_mix"
	s.db.UpdateSource(&src)

	s.CheckDouyinMix(src)
	// 不应 panic
}

func TestCheckDouyinMix_CreatesNewDownloads(t *testing.T) {
	mock := &mockDouyinAPI{
		mixVideoResult: []douyin.DouyinVideo{
			{AwemeID: "mix_v1", Desc: "合集视频1", Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
			{AwemeID: "mix_v2", Desc: "合集视频2", Duration: 60000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
		},
	}
	s := newTestDouyinScheduler(t, mock)

	src := createTestSource(t, s.db, "测试合集", "https://www.douyin.com/collection/123456")
	src.Type = "douyin_mix"
	s.db.UpdateSource(&src)

	s.CheckDouyinMix(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 pending downloads, got %d", pending)
	}
}

func TestCheckDouyinMix_Deduplicates(t *testing.T) {
	mock := &mockDouyinAPI{
		mixVideoResult: []douyin.DouyinVideo{
			{AwemeID: "mix_existing", Desc: "已存在", Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
			{AwemeID: "mix_new", Desc: "新视频", Duration: 60000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
		},
	}
	s := newTestDouyinScheduler(t, mock)

	src := createTestSource(t, s.db, "测试合集", "https://www.douyin.com/collection/123456")
	src.Type = "douyin_mix"
	s.db.UpdateSource(&src)

	s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "mix_existing",
		Title:    "已存在",
		Status:   "completed",
	})

	s.CheckDouyinMix(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download (dedup), got %d", pending)
	}
}

// ============================
// FullScanDouyin 测试
// ============================

func TestFullScanDouyin_SkipWhenPaused(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)
	s.Pause("test pause")

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.FullScanDouyin(src)

	if mock.getCallCount() > 0 {
		t.Errorf("expected 0 API calls when paused, got %d", mock.getCallCount())
	}
}

func TestFullScanDouyin_CreatesOnlyMissing(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "vid_a", Desc: "视频A", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "vid_b", Desc: "视频B", CreateTime: now - 20, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "vid_c", Desc: "视频C", CreateTime: now - 30, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestDouyinScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	// 预先标记 vid_b 为已下载
	s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "vid_b",
		Title:    "视频B",
		Status:   "completed",
	})

	s.FullScanDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 missing videos (vid_a, vid_c), got %d", pending)
	}
}

func TestFullScanDouyin_NothingMissing(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "vid_all", Desc: "全部已下载", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestDouyinScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "vid_all",
		Title:    "全部已下载",
		Status:   "completed",
	})

	s.FullScanDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 0 {
		t.Errorf("expected 0 pending downloads when nothing missing, got %d", pending)
	}
}

// ============================
// 辅助函数
// ============================

func countPendingDownloads(t *testing.T, database *db.DB, sourceID int64) int {
	t.Helper()
	dls := getDownloadsForSource(t, database, sourceID)
	count := 0
	for _, d := range dls {
		if d.Status == "pending" {
			count++
		}
	}
	return count
}

func getDownloadsForSource(t *testing.T, database *db.DB, sourceID int64) []db.Download {
	t.Helper()
	all, err := database.GetDownloadsByStatus("pending", 10000)
	if err != nil {
		t.Fatalf("getDownloadsForSource: %v", err)
	}
	var result []db.Download
	for _, d := range all {
		if d.SourceID == sourceID {
			result = append(result, d)
		}
	}
	return result
}
