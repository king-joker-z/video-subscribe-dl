package phscheduler

import (
	"context"
	"sync"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/pornhub"
)

// ProgressInfo PH 下载进度信息
type ProgressInfo struct {
	DownloadID int64
	Title      string
	Status     string
	Percent    float64
	Speed      int64
	Downloaded int64
	Total      int64
}

// DownloadEvent PH 下载完成/失败事件
type DownloadEvent struct {
	Type         string
	VideoID      string
	Title        string
	FileSize     int64
	Error        string
	DownloadedAt string // RFC3339，完成时间，失败时为空
}

// PHCookieStatus Pornhub Cookie 有效性状态
type PHCookieStatus struct {
	Valid bool
	Msg  string
}

// PHScheduler 封装 Pornhub 专属的调度状态与逻辑
type PHScheduler struct {
	db          *db.DB
	downloadDir string
	notifier    *notify.Notifier

	// 当前 Cookie（内存），cookieMu 保护并发读写
	cookieMu sync.RWMutex
	cookie   string

	// 客户端工厂
	newClient func() *pornhub.Client

	// 冷却
	cooldownUntil time.Time
	cooldownMu    sync.Mutex

	// 暂停控制
	pausedMu    sync.RWMutex
	paused      bool
	pauseReason string
	pausedAt    time.Time

	// 下载频率限制
	downloadLimiter *pornhub.RateLimiter

	// 独立进度追踪
	progressMu  sync.RWMutex
	progressMap map[string]*ProgressInfo

	// 下载事件推送 channel
	eventCh     chan DownloadEvent
	eventChOnce sync.Once // [FIXED: PH-1] 保证 eventCh 只关闭一次

	// 下载 goroutine 并发上限（2）
	workerSem chan struct{}

	// Cookie 状态
	cookieStatusMu sync.RWMutex
	cookieValid    bool
	cookieMsg      string

	// 全量扫描防重入
	fullScanRunning   map[int64]bool
	fullScanRunningMu sync.Mutex

	// 生命周期 context
	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         *sync.WaitGroup
}

// Config 创建 PHScheduler 所需的配置
type Config struct {
	DB          *db.DB
	DownloadDir string
	Notifier    *notify.Notifier
	NewClient   func() *pornhub.Client
}

// New 创建 PHScheduler
func New(cfg Config) *PHScheduler {
	newClient := cfg.NewClient
	if newClient == nil {
		newClient = func() *pornhub.Client {
			return pornhub.NewClient()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}

	s := &PHScheduler{
		db:              cfg.DB,
		downloadDir:     cfg.DownloadDir,
		notifier:        cfg.Notifier,
		newClient:       newClient,
		progressMap:     make(map[string]*ProgressInfo),
		eventCh:         make(chan DownloadEvent, 100),
		cookieValid:     true,
		downloadLimiter: pornhub.NewRateLimiter(1, 1, 5*time.Second),
		workerSem:       make(chan struct{}, 2), // 最多 2 个并发下载 goroutine
		fullScanRunning: make(map[int64]bool),
		rootCtx:         ctx,
		rootCancel:      cancel,
		wg:              wg,
	}
	return s
}

// ─── PlatformScheduler 接口实现 ───────────────────────────────────────────────

// CheckSource 根据 source 类型分发
func (s *PHScheduler) CheckSource(src db.Source) {
	s.CheckPHModel(src)
}

// RetryDownload 重试单个 PH 下载记录（通过信号量限制并发）
func (s *PHScheduler) RetryDownload(dl db.Download) {
	s.workerSem <- struct{}{} // 获取 slot，满时阻塞
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { <-s.workerSem }()
		s.retryOneDownload(dl)
	}()
}

// DispatchDownload 异步提交单个下载任务（非阻塞，workerSem 满时在 goroutine 内等待）
// 用于扫描完成后直接投递新增任务，避免依赖 ProcessAllPending 的防重入锁
func (s *PHScheduler) DispatchDownload(dl db.Download) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.workerSem <- struct{}{} // goroutine 内等待 slot，不阻塞调用方
		defer func() { <-s.workerSem }()
		s.retryOneDownload(dl)
	}()
}

// IsPaused 返回 PH 是否被暂停
func (s *PHScheduler) IsPaused() bool {
	s.pausedMu.RLock()
	defer s.pausedMu.RUnlock()
	return s.paused
}

// Stop 清理资源，取消所有进行中的下载
func (s *PHScheduler) Stop() {
	if s.rootCancel != nil {
		s.rootCancel()
	}
	if s.downloadLimiter != nil {
		s.downloadLimiter.Stop()
	}
	if s.wg != nil {
		s.wg.Wait()
	}
	// 等所有 worker 退出后再关 eventCh，让上游事件转发 goroutine 正常退出
	// [FIXED: PH-1] 用 sync.Once 保证只关闭一次，避免重复 close panic
	s.eventChOnce.Do(func() { close(s.eventCh) })
}

// ─── 进度推送 ─────────────────────────────────────────────────────────────────

// GetEventChan 返回下载事件 channel（供外部订阅）
func (s *PHScheduler) GetEventChan() <-chan DownloadEvent {
	return s.eventCh
}

// setProgress 设置进度
func (s *PHScheduler) setProgress(key string, info *ProgressInfo) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.progressMap[key] = info
}

// removeProgress 删除进度
func (s *PHScheduler) removeProgress(key string) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	delete(s.progressMap, key)
}

// emitEvent 推送下载事件（非阻塞）
// [FIXED: PH-1] 用 recover 捕获向已关闭 channel 写入时的 panic
func (s *PHScheduler) emitEvent(evt DownloadEvent) {
	defer func() { recover() }() //nolint:errcheck
	select {
	case s.eventCh <- evt:
	default:
		// channel 满时不阻塞
	}
}

// ─── Cookie 状态 ─────────────────────────────────────────────────────────────

// SetCookieInvalid 标记 Cookie 失效
func (s *PHScheduler) SetCookieInvalid(reason string) {
	s.cookieStatusMu.Lock()
	defer s.cookieStatusMu.Unlock()
	s.cookieValid = false
	s.cookieMsg = reason
}

// SetCookieValid 标记 Cookie 有效
func (s *PHScheduler) SetCookieValid() {
	s.cookieStatusMu.Lock()
	defer s.cookieStatusMu.Unlock()
	s.cookieValid = true
	s.cookieMsg = ""
}

// GetPHCookieStatus 获取 Cookie 状态
func (s *PHScheduler) GetPHCookieStatus() PHCookieStatus {
	s.cookieStatusMu.RLock()
	defer s.cookieStatusMu.RUnlock()
	return PHCookieStatus{Valid: s.cookieValid, Msg: s.cookieMsg}
}
