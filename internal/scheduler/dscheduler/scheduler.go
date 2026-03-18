// Package dscheduler 封装抖音专属的调度逻辑。
// DouyinScheduler 只负责抖音平台任务，有独立的暂停/冷却/进度推送机制，
// 不依赖 B站 Downloader 的 SetExternalProgress/EmitEvent。
package dscheduler

import (
	"sync"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/notify"
)

// DouyinAPI 定义抖音客户端方法（便于测试替换）
type DouyinAPI interface {
	Close()
	ValidateCookie() (bool, string)
	GetUserVideos(secUID string, maxCursor int64, consecutiveErrors ...int) (*douyin.UserVideosResult, error)
	GetUserProfile(secUID string) (*douyin.DouyinUserProfile, error)
	ResolveShareURL(shareURL string) (*douyin.ResolveResult, error)
	GetVideoDetail(videoID string) (*douyin.DouyinVideo, error)
	ResolveVideoURL(videoURL string) (string, error)
	GetMixVideos(mixID string) ([]douyin.DouyinVideo, error)
}

// douyinClientAdapter 将 *douyin.DouyinClient 适配到 DouyinAPI 接口
type douyinClientAdapter struct {
	*douyin.DouyinClient
}

// ProgressInfo 抖音下载进度信息
type ProgressInfo struct {
	DownloadID int64
	Title      string
	Status     string
	Percent    float64
	Speed      int64
	Downloaded int64
	Total      int64
}

// DownloadEvent 抖音下载完成/失败事件
type DownloadEvent struct {
	Type     string
	VideoID  string
	Title    string
	FileSize int64
	Error    string
}

// DouyinCookieStatus 抖音 Cookie 有效性状态
type DouyinCookieStatus struct {
	Valid bool
	Msg  string
}

// DouyinScheduler 封装抖音专属的调度状态与逻辑
type DouyinScheduler struct {
	db          *db.DB
	downloadDir string
	notifier    *notify.Notifier

	// 客户端工厂
	newClient func() DouyinAPI

	// 冷却
	cooldownUntil time.Time
	cooldownMu    sync.Mutex

	// 暂停控制
	pausedMu    sync.RWMutex
	paused      bool
	pauseReason string
	pausedAt    time.Time

	// 下载频率限制
	downloadLimiter *douyin.RateLimiter

	// 自定义 sleep（便于测试）
	sleepFn func(time.Duration)

	// Cookie 检测时间戳
	lastCookieCheck time.Time

	// 独立进度追踪（不依赖 BiliDownloader）
	progressMu  sync.RWMutex
	progressMap map[string]*ProgressInfo

	// 下载事件推送 channel
	eventCh chan DownloadEvent

	// Cookie 状态（全局单例方式，同步访问）
	cookieStatusMu sync.RWMutex
	cookieValid    bool
	cookieMsg      string
}

// Config 创建 DouyinScheduler 所需的配置
type Config struct {
	DB          *db.DB
	DownloadDir string
	Notifier    *notify.Notifier
	NewClient   func() DouyinAPI
}

// New 创建 DouyinScheduler
func New(cfg Config) *DouyinScheduler {
	newClient := cfg.NewClient
	if newClient == nil {
		newClient = func() DouyinAPI {
			return &douyinClientAdapter{douyin.NewClient()}
		}
	}
	s := &DouyinScheduler{
		db:              cfg.DB,
		downloadDir:     cfg.DownloadDir,
		notifier:        cfg.Notifier,
		newClient:       newClient,
		sleepFn:         time.Sleep,
		progressMap:     make(map[string]*ProgressInfo),
		eventCh:         make(chan DownloadEvent, 100),
		cookieValid:     true,
		downloadLimiter: douyin.NewRateLimiter(2, 1, 30*time.Second),
	}
	return s
}

// ─── PlatformScheduler 接口实现 ───────────────────────────────────────────────

// CheckSource 根据 source 类型分发到检查方法
func (s *DouyinScheduler) CheckSource(src db.Source) {
	switch src.Type {
	case "douyin_mix":
		s.CheckDouyinMix(src)
	default:
		s.CheckDouyin(src)
	}
}

// RetryDownload 重试单个抖音下载记录
func (s *DouyinScheduler) RetryDownload(dl db.Download) {
	s.RetryOneDownload(dl)
}

// IsPaused 返回抖音是否被暂停
func (s *DouyinScheduler) IsPaused() bool {
	s.pausedMu.RLock()
	defer s.pausedMu.RUnlock()
	return s.paused
}

// Stop 清理资源
func (s *DouyinScheduler) Stop() {
	if s.downloadLimiter != nil {
		s.downloadLimiter.Stop()
	}
}

// ─── 进度推送 ──────────────────────────────────────────────────────────────────

// GetEventChan 返回下载事件 channel（供外部订阅）
func (s *DouyinScheduler) GetEventChan() <-chan DownloadEvent {
	return s.eventCh
}

// setProgress 设置进度
func (s *DouyinScheduler) setProgress(key string, info *ProgressInfo) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.progressMap[key] = info
}

// removeProgress 删除进度
func (s *DouyinScheduler) removeProgress(key string) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	delete(s.progressMap, key)
}

// emitEvent 推送下载事件（非阻塞）
func (s *DouyinScheduler) emitEvent(evt DownloadEvent) {
	select {
	case s.eventCh <- evt:
	default:
		// channel 满时不阻塞
	}
}

// ─── Cookie 状态 ──────────────────────────────────────────────────────────────

// SetCookieInvalid 标记 Cookie 失效
func (s *DouyinScheduler) SetCookieInvalid(reason string) {
	s.cookieStatusMu.Lock()
	defer s.cookieStatusMu.Unlock()
	s.cookieValid = false
	s.cookieMsg = reason
}

// SetCookieValid 标记 Cookie 有效
func (s *DouyinScheduler) SetCookieValid() {
	s.cookieStatusMu.Lock()
	defer s.cookieStatusMu.Unlock()
	s.cookieValid = true
	s.cookieMsg = ""
}

// GetDouyinCookieStatus 获取 Cookie 状态
func (s *DouyinScheduler) GetDouyinCookieStatus() DouyinCookieStatus {
	s.cookieStatusMu.RLock()
	defer s.cookieStatusMu.RUnlock()
	return DouyinCookieStatus{Valid: s.cookieValid, Msg: s.cookieMsg}
}
