package dscheduler

import (
	"sync"
	"testing"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/notify"
)

// mockDouyinAPI 实现 DouyinAPI 接口，用于测试
type mockDouyinAPI struct {
	mu              sync.Mutex
	closed          bool
	validateResult  bool
	validateMsg     string
	userVideoCalls  int
	userVideoPages  []mockVideoPage
	profileResult   *douyin.DouyinUserProfile
	profileErr      error
	resolveResult   *douyin.ResolveResult
	resolveErr      error
	videoDetailResult *douyin.DouyinVideo
	videoDetailErr    error
	videoDetailCalls  int
	resolveVideoResult string
	resolveVideoErr    error
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

// newTestDouyinScheduler 创建使用临时 SQLite 的 DouyinScheduler（测试用）
func newTestDouyinScheduler(t *testing.T, mock *mockDouyinAPI) *DouyinScheduler {
	t.Helper()
	database, err := db.Init(t.TempDir())
	if err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	s := New(Config{
		DB:          database,
		DownloadDir: t.TempDir(),
		Notifier:    notify.New(database),
		NewClient:   func() DouyinAPI { return mock },
	})
	// 测试中跳过 sleep
	s.sleepFn = func(d time.Duration) {}
	// 测试中不限流
	s.downloadLimiter.Stop()
	s.downloadLimiter = newFastLimiter()

	t.Cleanup(func() { s.Stop() })
	return s
}

// newFastLimiter 创建一个几乎不限流的 RateLimiter，供测试使用
func newFastLimiter() *douyin.RateLimiter {
	return douyin.NewRateLimiter(100, 100, time.Millisecond)
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
	if err := database.CreateSource(src); err != nil {
		t.Fatalf("createTestSource: %v", err)
	}
	return *src
}
