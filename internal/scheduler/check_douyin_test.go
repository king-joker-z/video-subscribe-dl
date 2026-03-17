package scheduler

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/notify"
)

// --- Mock DouyinAPI ---

type mockDouyinAPI struct {
	mu              sync.Mutex
	closed          bool
	validateResult  bool
	validateMsg     string
	userVideoCalls  int
	userVideoPages  []mockVideoPage // 按调用顺序返回
	profileResult   *douyin.DouyinUserProfile
	profileErr      error
	resolveResult   *douyin.ResolveResult
	resolveErr      error
	// GetVideoDetail mock
	videoDetailResult *douyin.DouyinVideo
	videoDetailErr    error
	videoDetailCalls  int
	// ResolveVideoURL mock
	resolveVideoResult string
	resolveVideoErr    error
	// GetMixVideos mock
	mixVideoResult []douyin.DouyinVideo
	mixVideoErr    error
}

type mockVideoPage struct {
	result *douyin.UserVideosResult
	err    error
}

func (m *mockDouyinAPI) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

func (m *mockDouyinAPI) ValidateCookie() (bool, string) {
	return m.validateResult, m.validateMsg
}

func (m *mockDouyinAPI) GetUserVideos(secUID string, maxCursor int64, consecutiveErrors ...int) (*douyin.UserVideosResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.userVideoCalls
	m.userVideoCalls++
	if idx < len(m.userVideoPages) {
		p := m.userVideoPages[idx]
		return p.result, p.err
	}
	return &douyin.UserVideosResult{}, nil
}

func (m *mockDouyinAPI) GetUserProfile(secUID string) (*douyin.DouyinUserProfile, error) {
	if m.profileResult != nil || m.profileErr != nil {
		return m.profileResult, m.profileErr
	}
	return &douyin.DouyinUserProfile{Nickname: "TestUser"}, nil
}

func (m *mockDouyinAPI) ResolveShareURL(shareURL string) (*douyin.ResolveResult, error) {
	if m.resolveResult != nil || m.resolveErr != nil {
		return m.resolveResult, m.resolveErr
	}
	return &douyin.ResolveResult{Type: douyin.URLTypeUser, SecUID: "test_sec_uid"}, nil
}

func (m *mockDouyinAPI) GetVideoDetail(videoID string) (*douyin.DouyinVideo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videoDetailCalls++
	if m.videoDetailErr != nil {
		return nil, m.videoDetailErr
	}
	if m.videoDetailResult != nil {
		return m.videoDetailResult, nil
	}
	return &douyin.DouyinVideo{
		AwemeID:  videoID,
		Desc:     "Test Video",
		VideoURL: "https://example.com/video.mp4",
		Duration: 30000,
		Author:   douyin.DouyinUser{Nickname: "TestUser"},
	}, nil
}

func (m *mockDouyinAPI) ResolveVideoURL(videoURL string) (string, error) {
	if m.resolveVideoErr != nil {
		return "", m.resolveVideoErr
	}
	if m.resolveVideoResult != "" {
		return m.resolveVideoResult, nil
	}
	return videoURL, nil
}

func (m *mockDouyinAPI) GetMixVideos(mixID string) ([]douyin.DouyinVideo, error) {
	if m.mixVideoErr != nil {
		return nil, m.mixVideoErr
	}
	return m.mixVideoResult, nil
}

func (m *mockDouyinAPI) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *mockDouyinAPI) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userVideoCalls
}

// --- Test helpers ---

// newTestScheduler 创建使用临时 SQLite 的 Scheduler（仅用于 checkDouyin/fullScanDouyin 测试）
func newTestScheduler(t *testing.T, mock *mockDouyinAPI) *Scheduler {
	t.Helper()
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	s := &Scheduler{
		db:              database,
		downloadDir:     t.TempDir(),
		stopCh:          make(chan struct{}),
		fullScanRunning: make(map[int64]bool),
		newDouyinClient: func() DouyinAPI { return mock },
		sleepFn:         func(d time.Duration) {}, // 测试中跳过 sleep
		notifier:        notify.New(database),
		douyinDownloadLimiter: douyin.NewRateLimiter(100, 100, time.Millisecond), // 测试中不限流
	}
	t.Cleanup(func() { s.douyinDownloadLimiter.Stop() })
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
	id, err := database.CreateSource(src)
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	src.ID = id
	return *src
}

// countPendingDownloads 计算 source 下 pending 状态的下载数
func countPendingDownloads(t *testing.T, database *db.DB, sourceID int64) int {
	t.Helper()
	all, err := database.GetPendingDownloads()
	if err != nil {
		t.Fatalf("GetPendingDownloads: %v", err)
	}
	count := 0
	for _, d := range all {
		if d.SourceID == sourceID {
			count++
		}
	}
	return count
}

// getDownloadsForSource 获取 source 的所有下载记录
func getDownloadsForSource(t *testing.T, database *db.DB, sourceID int64) []db.Download {
	t.Helper()
	all, err := database.GetAllDownloads()
	if err != nil {
		t.Fatalf("GetAllDownloads: %v", err)
	}
	var result []db.Download
	for _, d := range all {
		if d.SourceID == sourceID {
			result = append(result, d)
		}
	}
	return result
}

// ============================
// checkDouyin 测试
// ============================

func TestCheckDouyin_SkipWhenPaused(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	// 暂停抖音
	s.douyinPaused = true

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// mock 应该完全没被调用
	if mock.getCallCount() > 0 {
		t.Errorf("expected 0 API calls when paused, got %d", mock.getCallCount())
	}
}

func TestCheckDouyin_URLResolveFail(t *testing.T) {
	mock := &mockDouyinAPI{
		resolveErr: fmt.Errorf("invalid URL"),
	}
	s := newTestScheduler(t, mock)

	// 使用一个无法直接提取 secUID 的短链接格式
	src := createTestSource(t, s.db, "TestUser", "https://v.douyin.com/invalid")
	s.checkDouyin(src)

	// 不应有任何视频拉取调用
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
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// 应创建 3 个 pending 下载
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 3 {
		t.Errorf("expected 3 pending downloads, got %d", pending)
	}

	// latestVideoAt 应被更新为最大 CreateTime
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
						// 这个视频时间 <= latestVideoAt，会触发 stopped
						{AwemeID: "vid_old", Desc: "旧视频", CreateTime: now - 500, Duration: 15000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   true, // 有更多，但因 stopped 不会翻页
					MaxCursor: 100,
				},
			},
			{
				// 第二页不应被调用
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "vid_should_not_see", Desc: "不应看到", CreateTime: now - 1000, Duration: 10000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	// 设置增量基准时间
	s.db.UpdateSourceLatestVideoAt(src.ID, now-100)

	s.checkDouyin(src)

	// 只应创建 2 个新视频的下载（vid_old 的 CreateTime <= latestVideoAt）
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 pending downloads, got %d", pending)
	}

	// 只应调用一次 GetUserVideos（stopped=true 不翻页）
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
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")

	// 预先插入一条已下载的记录
	s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "vid_dup",
		Title:    "已存在",
		Status:   "done",
	})

	s.checkDouyin(src)

	// 只应创建 1 个新的 pending（vid_dup 已存在被跳过）
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
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// 应翻两页，创建 2 个下载
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
					Videos: []douyin.DouyinVideo{
						{AwemeID: "p1_v1", Desc: "Page1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "p2_v1", Desc: "Page2", CreateTime: now - 20, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   true,
					MaxCursor: 200,
				},
			},
			{
				// 第3页不应被拉取
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "p3_v1", Desc: "Page3", CreateTime: now - 30, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	// 设置首次扫描页数限制为 2
	s.db.SetSetting("first_scan_pages", "2")

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// 只应拉取 2 页
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
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

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
	s := newTestScheduler(t, mock)

	// 创建一个未命名的 source
	src := createTestSource(t, s.db, "未命名", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// 验证 source name 被更新（GetUserProfile 返回 ProfileName）
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
	s.checkDouyin(src)

	// 验证 client.Close() 被调用（defer）
	if !mock.isClosed() {
		t.Error("expected DouyinAPI.Close() to be called")
	}
}

// ============================
// fullScanDouyin 测试
// ============================

func TestFullScanDouyin_SkipWhenPaused(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)
	s.douyinPaused = true

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	if mock.getCallCount() > 0 {
		t.Errorf("expected 0 API calls when paused, got %d", mock.getCallCount())
	}
}

func TestFullScanDouyin_URLResolveFail(t *testing.T) {
	mock := &mockDouyinAPI{
		resolveErr: fmt.Errorf("bad url"),
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://v.douyin.com/bad")
	s.fullScanDouyin(src)

	if mock.getCallCount() > 0 {
		t.Errorf("expected 0 video calls on URL resolve failure, got %d", mock.getCallCount())
	}
}

func TestFullScanDouyin_CreatesOnlyMissingDownloads(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_001", Desc: "视频1", CreateTime: now - 100, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "fs_002", Desc: "视频2", CreateTime: now - 200, Duration: 60000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "fs_003", Desc: "视频3", CreateTime: now - 300, Duration: 15000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")

	// 预先插入 fs_002 为已下载
	s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "fs_002",
		Title:    "视频2",
		Status:   "done",
	})

	s.fullScanDouyin(src)

	// 应只创建 fs_001 和 fs_003（fs_002 已存在）
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 pending downloads (missing only), got %d", pending)
	}
}

func TestFullScanDouyin_NoMissing_EarlyReturn(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_all", Desc: "已有", CreateTime: now - 100, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")

	// 预先插入所有视频为已下载
	s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "fs_all",
		Title:    "已有",
		Status:   "done",
	})

	s.fullScanDouyin(src)

	// 不应创建新的 pending
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 0 {
		t.Errorf("expected 0 pending downloads (all exist), got %d", pending)
	}
}

func TestFullScanDouyin_Pagination_DeduplicatesAcrossPages(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "dup_v1", Desc: "Video1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "dup_v2", Desc: "Video2", CreateTime: now - 20, Duration: 60000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						// 跨页重复 dup_v2
						{AwemeID: "dup_v2", Desc: "Video2", CreateTime: now - 20, Duration: 60000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "dup_v3", Desc: "Video3", CreateTime: now - 30, Duration: 15000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	// fullScan 内部用 seenIDs 去重，应只有 3 个唯一视频
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 3 {
		t.Errorf("expected 3 pending downloads (dedup across pages), got %d", pending)
	}
}

func TestFullScanDouyin_UpdatesLatestVideoAt(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "lat_v1", Desc: "Latest", CreateTime: now - 50, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "lat_v2", Desc: "Older", CreateTime: now - 200, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	// 设置初始的 latestVideoAt
	s.db.UpdateSourceLatestVideoAt(src.ID, now-300)

	s.fullScanDouyin(src)

	// latestVideoAt 应更新到最大的 CreateTime
	latestAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	if latestAt != now-50 {
		t.Errorf("expected latestVideoAt=%d, got %d", now-50, latestAt)
	}
}

// ============================
// 错误处理 & 退避 测试
// ============================

func TestCheckDouyin_RiskControlError_StopsAfter2(t *testing.T) {
	// 模拟连续风控 403 错误，应在 2 次后停止
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("HTTP 403 forbidden")},
			{result: nil, err: fmt.Errorf("HTTP 403 forbidden")},
			// 第三次不应被调用
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
	s.checkDouyin(src)

	// 连续 2 次风控拦截后应停止
	if mock.getCallCount() != 2 {
		t.Errorf("expected 2 GetUserVideos calls (risk control stop), got %d", mock.getCallCount())
	}
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 0 {
		t.Errorf("expected 0 pending downloads after risk control, got %d", pending)
	}
}

func TestCheckDouyin_NormalError_RetriesAndRecovers(t *testing.T) {
	now := time.Now().Unix()
	// 第一次普通网络错误，第二次成功
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("connection timeout")},
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "retry_v1", Desc: "成功", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// 第一次失败 + 重试成功 = 2 calls
	if mock.getCallCount() != 2 {
		t.Errorf("expected 2 GetUserVideos calls (retry), got %d", mock.getCallCount())
	}
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download after retry, got %d", pending)
	}
}

func TestCheckDouyin_NormalError_StopsAfter5(t *testing.T) {
	// 模拟连续 5 次普通网络错误
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("connection timeout 1")},
			{result: nil, err: fmt.Errorf("connection timeout 2")},
			{result: nil, err: fmt.Errorf("connection timeout 3")},
			{result: nil, err: fmt.Errorf("connection timeout 4")},
			{result: nil, err: fmt.Errorf("connection timeout 5")},
			// 第 6 次不应被调用
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
	s.checkDouyin(src)

	// 连续 5 次普通错误后应停止
	if mock.getCallCount() != 5 {
		t.Errorf("expected 5 GetUserVideos calls (normal error stop), got %d", mock.getCallCount())
	}
}

func TestCheckDouyin_EmptyFirstPage_NoVideos(t *testing.T) {
	// 空视频列表（可能私密账号）
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
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
	s.checkDouyin(src)

	// 只调用 1 次，0 个下载
	if mock.getCallCount() != 1 {
		t.Errorf("expected 1 GetUserVideos call, got %d", mock.getCallCount())
	}
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 0 {
		t.Errorf("expected 0 pending downloads (empty list), got %d", pending)
	}
}

func TestCheckDouyin_CookieValidationSkippedWhenRecent(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: false,
		validateMsg:    "expired",
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "cv_v1", Desc: "Video", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)
	// 设置最后检查时间为 30 分钟前（< 1 小时，不触发验证）
	s.lastDouyinCookieCheck = time.Now().Add(-30 * time.Minute)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	// 即使 cookie 验证设为失败，因为距上次检查 < 1h，不会验证，视频仍能正常拉取
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download (cookie check skipped), got %d", pending)
	}
}

func TestCheckDouyin_RiskControlError_SingleRetryThenRecovers(t *testing.T) {
	now := time.Now().Unix()
	// 1 次风控错误后恢复
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("HTTP 429 too many requests")},
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "rc_v1", Desc: "恢复", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	if mock.getCallCount() != 2 {
		t.Errorf("expected 2 calls (1 risk retry + 1 success), got %d", mock.getCallCount())
	}
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download after risk recovery, got %d", pending)
	}
}

// ============================
// resolveDouyinSecUID 边界测试
// ============================

func TestCheckDouyin_ResolveShareURL_NonUserType(t *testing.T) {
	// ResolveShareURL 返回 video 类型而非 user，应失败
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		resolveResult: &douyin.ResolveResult{
			Type:    douyin.URLTypeVideo,
			VideoID: "12345",
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://v.douyin.com/video_link")
	s.checkDouyin(src)

	if mock.getCallCount() > 0 {
		t.Errorf("expected 0 video calls (non-user URL type), got %d", mock.getCallCount())
	}
}

// ============================
// fullScanDouyin 错误处理测试
// ============================

func TestFullScanDouyin_RiskControl_ContinuesWithPartialData(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_p1", Desc: "Page1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			{result: nil, err: fmt.Errorf("HTTP 403 forbidden")},
			{result: nil, err: fmt.Errorf("HTTP 403 forbidden")},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 1 {
		t.Errorf("expected 1 pending download (partial data), got %d", pending)
	}
}

func TestFullScanDouyin_NormalError_ContinuesWithPartialAfter5(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_err_v1", Desc: "OK", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
						{AwemeID: "fs_err_v2", Desc: "OK2", CreateTime: now - 20, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			{result: nil, err: fmt.Errorf("timeout 1")},
			{result: nil, err: fmt.Errorf("timeout 2")},
			{result: nil, err: fmt.Errorf("timeout 3")},
			{result: nil, err: fmt.Errorf("timeout 4")},
			{result: nil, err: fmt.Errorf("timeout 5")},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 pending downloads (partial after errors), got %d", pending)
	}
}

func TestFullScanDouyin_EmptyDescFallback(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_notitle", Desc: "", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
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
	expected := "douyin_fs_notitle"
	if downloads[0].Title != expected {
		t.Errorf("expected fallback title %q, got %q", expected, downloads[0].Title)
	}
}

func TestFullScanDouyin_EmptyAuthor_FallbackToSourceName(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "fs_noauthor", Desc: "视频", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: ""}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "MyUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.fullScanDouyin(src)

	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 1 {
		t.Fatalf("expected 1 download, got %d", len(downloads))
	}
	if downloads[0].Uploader != "MyUser" {
		t.Errorf("expected uploader 'MyUser', got %q", downloads[0].Uploader)
	}
}

func TestCheckDouyin_DirectSecUID_NoResolveNeeded(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		resolveErr:     fmt.Errorf("should not be called"),
		userVideoPages: []mockVideoPage{
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "direct_v1", Desc: "直接URL", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
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
		t.Errorf("expected 1 pending download (direct secUID), got %d", pending)
	}
}

func TestCheckDouyin_MixedErrors_ResetsCountOnSuccess(t *testing.T) {
	now := time.Now().Unix()
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
		userVideoPages: []mockVideoPage{
			{result: nil, err: fmt.Errorf("timeout 1")},
			{result: nil, err: fmt.Errorf("timeout 2")},
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "mix_v1", Desc: "P1", CreateTime: now - 10, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore:   true,
					MaxCursor: 100,
				},
			},
			{result: nil, err: fmt.Errorf("timeout 3")},
			{result: nil, err: fmt.Errorf("timeout 4")},
			{
				result: &douyin.UserVideosResult{
					Videos: []douyin.DouyinVideo{
						{AwemeID: "mix_v2", Desc: "P2", CreateTime: now - 20, Duration: 30000, Author: douyin.DouyinUser{Nickname: "TestUser"}},
					},
					HasMore: false,
				},
			},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	s.checkDouyin(src)

	if mock.getCallCount() != 6 {
		t.Errorf("expected 6 GetUserVideos calls (mixed errors), got %d", mock.getCallCount())
	}
	pending := countPendingDownloads(t, s.db, src.ID)
	if pending != 2 {
		t.Errorf("expected 2 pending downloads (mixed errors recovery), got %d", pending)
	}
}

func TestCheckDouyin_LatestVideoAt_NotUpdatedWhenNoNewVideos(t *testing.T) {
	now := time.Now().Unix()
	originalLatest := now - 100
	mock := &mockDouyinAPI{
		validateResult: true,
		validateMsg:    "ok",
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
	s.db.UpdateSourceLatestVideoAt(src.ID, originalLatest)

	s.checkDouyin(src)

	latestAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	if latestAt != originalLatest {
		t.Errorf("expected latestVideoAt unchanged at %d, got %d", originalLatest, latestAt)
	}
}

// ============================
// checkDouyinMix 测试
// ============================

// TestCheckDouyinMix_SkipWhenPaused 暂停时跳过
func TestCheckDouyinMix_SkipWhenPaused(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	// 设置暂停状态
	s.douyinPaused = true

	src := createTestSourceWithType(t, s.db, "TestMix", "https://www.douyin.com/collection/mix12345", "douyin_mix")
	callsBefore := 0 // mixVideoResult 调用计数（通过检查 downloads 数量）
	s.checkDouyinMix(src)

	// 暂停时不应该创建任何下载记录
	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != callsBefore {
		t.Errorf("expected no downloads when paused, got %d", len(downloads))
	}
}

// TestCheckDouyinMix_ParseCollectionURL 从合集 URL 解析 mix_id
func TestCheckDouyinMix_ParseCollectionURL(t *testing.T) {
	videos := []douyin.DouyinVideo{
		{
			AwemeID:    "mix_v001",
			Desc:       "mix video 1",
			CreateTime: 1700000001,
			Duration:   30000,
			Author:     douyin.DouyinUser{UID: "u1", Nickname: "MixCreator"},
			VideoURL:   "https://play.com/video.mp4",
			Cover:      "https://cover.jpg",
		},
	}
	mock := &mockDouyinAPI{
		mixVideoResult: videos,
	}
	s := newTestScheduler(t, mock)

	src := createTestSourceWithType(t, s.db, "TestMix", "https://www.douyin.com/collection/mx9999", "douyin_mix")
	s.checkDouyinMix(src)

	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 1 {
		t.Fatalf("expected 1 download, got %d", len(downloads))
	}
	if downloads[0].VideoID != "mix_v001" {
		t.Errorf("VideoID = %q, want mix_v001", downloads[0].VideoID)
	}
	if downloads[0].Uploader != "MixCreator" {
		t.Errorf("Uploader = %q, want MixCreator", downloads[0].Uploader)
	}
}

// TestCheckDouyinMix_ParseRawMixID mix_id 直接作为 URL（无 /collection/ 前缀）
func TestCheckDouyinMix_ParseRawMixID(t *testing.T) {
	videos := []douyin.DouyinVideo{
		{
			AwemeID:    "mix_v002",
			Desc:       "raw mix video",
			CreateTime: 1700000002,
			Duration:   20000,
			Author:     douyin.DouyinUser{UID: "u2", Nickname: "RawMixCreator"},
		},
	}
	mock := &mockDouyinAPI{
		mixVideoResult: videos,
	}
	s := newTestScheduler(t, mock)

	// URL 就是纯 mix_id
	src := createTestSourceWithType(t, s.db, "RawMixTest", "rawmix123", "douyin_mix")
	s.checkDouyinMix(src)

	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 1 {
		t.Fatalf("expected 1 download, got %d", len(downloads))
	}
	if downloads[0].VideoID != "mix_v002" {
		t.Errorf("VideoID = %q, want mix_v002", downloads[0].VideoID)
	}
}

// TestCheckDouyinMix_IncrementalDedup 重复视频不创建两次
func TestCheckDouyinMix_IncrementalDedup(t *testing.T) {
	videos := []douyin.DouyinVideo{
		{
			AwemeID:    "mix_dup001",
			Desc:       "dup video",
			CreateTime: 1700000010,
			Duration:   25000,
			Author:     douyin.DouyinUser{Nickname: "DupCreator"},
		},
	}
	mock := &mockDouyinAPI{
		mixVideoResult: videos,
	}
	s := newTestScheduler(t, mock)

	src := createTestSourceWithType(t, s.db, "DupMix", "https://www.douyin.com/collection/dupMix", "douyin_mix")

	// 第一次检查
	s.checkDouyinMix(src)
	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 1 {
		t.Fatalf("after 1st check: expected 1 download, got %d", len(downloads))
	}

	// 第二次检查（同样的视频），不应再增加
	s.checkDouyinMix(src)
	downloads = getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 1 {
		t.Errorf("after 2nd check: expected 1 download (no dup), got %d", len(downloads))
	}
}

// TestCheckDouyinMix_EmptyMix 空合集不创建下载
func TestCheckDouyinMix_EmptyMix(t *testing.T) {
	mock := &mockDouyinAPI{
		mixVideoResult: []douyin.DouyinVideo{},
	}
	s := newTestScheduler(t, mock)

	src := createTestSourceWithType(t, s.db, "EmptyMix", "https://www.douyin.com/collection/emptyMix", "douyin_mix")
	s.checkDouyinMix(src)

	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 0 {
		t.Errorf("expected 0 downloads for empty mix, got %d", len(downloads))
	}
}

// TestCheckDouyinMix_GetMixVideosError API 错误时优雅退出
func TestCheckDouyinMix_GetMixVideosError(t *testing.T) {
	mock := &mockDouyinAPI{
		mixVideoErr: fmt.Errorf("API error: rate limited"),
	}
	s := newTestScheduler(t, mock)

	src := createTestSourceWithType(t, s.db, "ErrMix", "https://www.douyin.com/collection/errMix", "douyin_mix")

	// 不应 panic，应该优雅退出
	s.checkDouyinMix(src)

	downloads := getDownloadsForSource(t, s.db, src.ID)
	if len(downloads) != 0 {
		t.Errorf("expected 0 downloads on API error, got %d", len(downloads))
	}
}

// createTestSourceWithType 在 DB 中创建指定类型的 Source
func createTestSourceWithType(t *testing.T, database *db.DB, name, rawURL, srcType string) db.Source {
	t.Helper()
	src := &db.Source{
		Type:    srcType,
		URL:     rawURL,
		Name:    name,
		Enabled: true,
	}
	id, err := database.CreateSource(src)
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	src.ID = id
	return *src
}
