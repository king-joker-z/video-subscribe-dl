package xscheduler

import (
	"context"
	"sync"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/xchina"
)

// ProgressInfo xchina 下载进度信息
type ProgressInfo struct {
	DownloadID int64
	Title      string
	Status     string
	Percent    float64
	Speed      int64
	Downloaded int64
	Total      int64
}

// DownloadEvent xchina 下载完成/失败事件
type DownloadEvent struct {
	Type         string
	VideoID      string
	Title        string
	FileSize     int64
	Error        string
	DownloadedAt string
}

// XScheduler 封装 xchina 专属调度状态与逻辑
type XScheduler struct {
	db          *db.DB
	downloadDir string
	notifier    *notify.Notifier

	// 客户端工厂
	newClient func() *xchina.Client

	// 冷却
	cooldownUntil time.Time
	cooldownMu    sync.Mutex

	// 暂停控制
	pausedMu    sync.RWMutex
	paused      bool
	pauseReason string
	pausedAt    time.Time

	// 下载频率限制
	downloadLimiter *xchina.RateLimiter

	// 进度追踪
	progressMu  sync.RWMutex
	progressMap map[string]*ProgressInfo

	// 下载事件推送 channel
	eventCh     chan DownloadEvent
	eventChOnce sync.Once

	// 下载 goroutine 并发上限（2）
	workerSem chan struct{}

	// 全量扫描防重入
	fullScanRunning   map[int64]bool
	fullScanRunningMu sync.Mutex

	// 最近一次调度检查时间戳
	lastCheckMu sync.RWMutex
	lastCheckAt time.Time

	// 生命周期 context
	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         *sync.WaitGroup
}

// Config 创建 XScheduler 所需的配置
type Config struct {
	DB          *db.DB
	DownloadDir string
	Notifier    *notify.Notifier
	NewClient   func() *xchina.Client
}

// New 创建 XScheduler
func New(cfg Config) *XScheduler {
	newClient := cfg.NewClient
	if newClient == nil {
		newClient = func() *xchina.Client {
			return xchina.NewClient()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}

	return &XScheduler{
		db:              cfg.DB,
		downloadDir:     cfg.DownloadDir,
		notifier:        cfg.Notifier,
		newClient:       newClient,
		progressMap:     make(map[string]*ProgressInfo),
		eventCh:         make(chan DownloadEvent, 100),
		downloadLimiter: xchina.NewRateLimiter(1, 1, 5*time.Second),
		workerSem:       make(chan struct{}, 2),
		fullScanRunning: make(map[int64]bool),
		rootCtx:         ctx,
		rootCancel:      cancel,
		wg:              wg,
	}
}

// ─── lastCheckAt ──────────────────────────────────────────────────────────────

func (s *XScheduler) setLastCheckAt(t time.Time) {
	s.lastCheckMu.Lock()
	defer s.lastCheckMu.Unlock()
	s.lastCheckAt = t
}

// LastCheckAt 返回最近一次调度检查时间
func (s *XScheduler) LastCheckAt() time.Time {
	s.lastCheckMu.RLock()
	defer s.lastCheckMu.RUnlock()
	return s.lastCheckAt
}

// ─── PlatformScheduler 接口 ────────────────────────────────────────────────────

// CheckSource 根据 source 类型分发
func (s *XScheduler) CheckSource(src db.Source) {
	defer s.setLastCheckAt(time.Now())
	s.CheckXChinaModel(src)
}

// RetryDownload 重试单个下载记录（非阻塞）
func (s *XScheduler) RetryDownload(dl db.Download) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.workerSem <- struct{}{}
		defer func() { <-s.workerSem }()
		s.retryOneDownload(dl)
	}()
}

// DispatchDownload 异步提交单个下载任务（非阻塞）
func (s *XScheduler) DispatchDownload(dl db.Download) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.workerSem <- struct{}{}
		defer func() { <-s.workerSem }()
		s.retryOneDownload(dl)
	}()
}

// IsPaused 返回是否被暂停
func (s *XScheduler) IsPaused() bool {
	s.pausedMu.RLock()
	defer s.pausedMu.RUnlock()
	return s.paused
}

// Stop 清理资源
func (s *XScheduler) Stop() {
	if s.rootCancel != nil {
		s.rootCancel()
	}
	if s.downloadLimiter != nil {
		s.downloadLimiter.Stop()
	}
	if s.wg != nil {
		s.wg.Wait()
	}
	s.eventChOnce.Do(func() { close(s.eventCh) })
}

// ─── 进度推送 ──────────────────────────────────────────────────────────────────

// GetEventChan 返回下载事件 channel
func (s *XScheduler) GetEventChan() <-chan DownloadEvent {
	return s.eventCh
}

func (s *XScheduler) setProgress(key string, info *ProgressInfo) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.progressMap[key] = info
}

func (s *XScheduler) removeProgress(key string) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	delete(s.progressMap, key)
}

func (s *XScheduler) emitEvent(evt DownloadEvent) {
	defer func() { recover() }() //nolint:errcheck
	select {
	case s.eventCh <- evt:
	default:
	}
}
