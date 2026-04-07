// Package dscheduler 封装抖音专属的调度逻辑。
// DouyinScheduler 只负责抖音平台任务，有独立的暂停/冷却/进度推送机制，
// 不依赖 B站 Downloader 的 SetExternalProgress/EmitEvent。
package dscheduler

import (
	"context"
	"net/http"
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
	Type         string
	VideoID      string
	Title        string
	FileSize     int64
	Error        string
	DownloadedAt string // RFC3339，完成时间，失败时为空
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

	// Cookie 检测时间戳（cookieCheckMu 保护并发读写）
	cookieCheckMu   sync.Mutex
	lastCookieCheck time.Time

	// 独立进度追踪（不依赖 BiliDownloader）
	progressMu  sync.RWMutex
	progressMap map[string]*ProgressInfo

	// 下载事件推送 channel
	eventCh     chan DownloadEvent
	eventChOnce sync.Once // [FIXED: DS-8] 保证 eventCh 只关闭一次

	// 下载 goroutine 并发上限（防止批量提交时无限创建 goroutine）
	workerSem chan struct{}

	// Cookie 状态（全局单例方式，同步访问）
	cookieStatusMu sync.RWMutex
	cookieValid    bool
	cookieMsg      string

	// 生命周期 context（Stop 时取消，中断正在进行的下载）
	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         sync.WaitGroup

	// [FIXED: DS-2] 复用 http.Client，避免每次下载新建 transport
	httpClient *http.Client

	// 最近一次调度检查时间戳
	lastCheckMu sync.RWMutex
	lastCheckAt time.Time
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
	ctx, cancel := context.WithCancel(context.Background())
	s := &DouyinScheduler{
		db:              cfg.DB,
		downloadDir:     cfg.DownloadDir,
		notifier:        cfg.Notifier,
		newClient:       newClient,
		sleepFn:         time.Sleep,
		progressMap:     make(map[string]*ProgressInfo),
		eventCh:         make(chan DownloadEvent, 100),
		cookieValid:     true,
		downloadLimiter: douyin.NewRateLimiter(3, 1, 15*time.Second),
		workerSem:       make(chan struct{}, 8), // 最多 8 个并发下载 goroutine
		rootCtx:         ctx,
		rootCancel:      cancel,
		// [FIXED: DS-2] 初始化一次，复用 transport（与 defaultDouyinDownloadClient 共享同一实例）
		httpClient: defaultDouyinDownloadClient,
	}
	return s
}

// Start 在调度器启动时调用，重置崩溃遗留的僵死 "downloading" 记录为 "pending"
// [FIXED: DS-1] 防止进程崩溃后 downloading 状态记录永久卡住
func (s *DouyinScheduler) Start() {
	s.resetStaleDownloading()
}

// resetStaleDownloading 将抖音平台僵死的 "downloading" 状态记录重置为 "pending"
func (s *DouyinScheduler) resetStaleDownloading() {
	result, err := s.db.Exec(`
		UPDATE downloads SET status = 'pending'
		WHERE status = 'downloading'
		  AND source_id IN (
		    SELECT id FROM sources WHERE type IN ('douyin','douyin_mix')
		  )
	`)
	if err != nil {
		return
	}
	if n, _ := result.RowsAffected(); n > 0 {
		_ = n
		// 日志由调用方记录或忽略
	}
}

// ─── lastCheckAt 管理 ─────────────────────────────────────────────────────────

// setLastCheckAt 更新最近一次调度检查时间（私有，线程安全）
func (s *DouyinScheduler) setLastCheckAt(t time.Time) {
	s.lastCheckMu.Lock()
	defer s.lastCheckMu.Unlock()
	s.lastCheckAt = t
}

// LastCheckAt 返回最近一次调度检查时间（公共，线程安全）
func (s *DouyinScheduler) LastCheckAt() time.Time {
	s.lastCheckMu.RLock()
	defer s.lastCheckMu.RUnlock()
	return s.lastCheckAt
}

// ─── PlatformScheduler 接口实现 ───────────────────────────────────────────────

// CheckSource 根据 source 类型分发到检查方法
func (s *DouyinScheduler) CheckSource(src db.Source) {
	defer s.setLastCheckAt(time.Now())
	switch src.Type {
	case "douyin_mix":
		s.CheckDouyinMix(src)
	default:
		s.CheckDouyin(src)
	}
}

// RetryDownload 重试单个抖音下载记录（通过信号量限制并发，与 phscheduler 对齐）
func (s *DouyinScheduler) RetryDownload(dl db.Download) {
	s.workerSem <- struct{}{} // 获取 slot，满时阻塞
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { <-s.workerSem }()
		s.RetryOneDownload(dl)
	}()
}

// DispatchDownload 异步提交单个下载任务（非阻塞，workerSem 满时在 goroutine 内等待）
func (s *DouyinScheduler) DispatchDownload(dl db.Download) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.workerSem <- struct{}{} // goroutine 内等待 slot，不阻塞调用方
		defer func() { <-s.workerSem }()
		s.RetryOneDownload(dl)
	}()
}

// IsPaused 返回抖音是否被暂停
func (s *DouyinScheduler) IsPaused() bool {
	s.pausedMu.RLock()
	defer s.pausedMu.RUnlock()
	return s.paused
}

// Stop 清理资源，取消所有进行中的下载
func (s *DouyinScheduler) Stop() {
	if s.rootCancel != nil {
		s.rootCancel()
	}
	if s.downloadLimiter != nil {
		s.downloadLimiter.Stop()
	}
	s.wg.Wait()
	// 等所有 worker 退出后再关 eventCh
	// [FIXED: DS-8] 用 sync.Once 保证只关闭一次，避免重复 close panic
	s.eventChOnce.Do(func() { close(s.eventCh) })
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
// [FIXED: DS-8] 用 recover 捕获向已关闭 channel 写入时的 panic
func (s *DouyinScheduler) emitEvent(evt DownloadEvent) {
	defer func() { recover() }() //nolint:errcheck
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
