// Package bscheduler 封装 B站（Bilibili）专属的调度逻辑。
// BiliScheduler 只负责 B站平台任务，不包含抖音逻辑和 cron 编排。
package bscheduler

import (
	"sync"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/notify"
)

// upInfoCacheEntry 缓存 UP 主信息（包括负缓存）
type upInfoCacheEntry struct {
	info      *bilibili.UPInfo
	err       error
	fetchedAt time.Time
}

const (
	upInfoCacheTTL      = 6 * time.Hour
	upInfoErrorCacheTTL = 30 * time.Minute
)

// BiliScheduler 封装 B站专属的调度状态与逻辑。
// 实现 PlatformScheduler 接口（通过 CheckSource/RetryDownload/IsPaused/Stop）。
type BiliScheduler struct {
	db          *db.DB
	dl          *downloader.Downloader
	downloadDir string
	cookiePath  string
	notifier    *notify.Notifier

	// bilibili client 保护
	biliMu sync.RWMutex
	bili   *bilibili.Client

	// 风控退避
	rateLimitMu        sync.Mutex
	cooldownUntil      time.Time
	lastCooldownNotify time.Time

	// Cookie 定期检测
	lastCookieCheck time.Time

	// 并发控制信号量
	videoSema *bilibili.Semaphore
	pageSema  *bilibili.Semaphore

	// 下载频率限制器
	downloadLimiter *bilibili.RateLimiter

	// UP 主信息缓存
	upInfoCache   map[int64]*upInfoCacheEntry
	upInfoCacheMu sync.RWMutex

	// 热配置
	hotConfig *config.HotConfig

	// 全量扫描去重
	fullScanRunning   map[int64]bool
	fullScanRunningMu sync.Mutex

	// 待处理的 WaitGroup
	wg *sync.WaitGroup
}

// Config 创建 BiliScheduler 所需的配置
type Config struct {
	DB          *db.DB
	Downloader  *downloader.Downloader
	DownloadDir string
	CookiePath  string
	Notifier    *notify.Notifier
	HotConfig   *config.HotConfig
	WG          *sync.WaitGroup
}

// New 创建 BiliScheduler
func New(cfg Config) *BiliScheduler {
	cookie := bilibili.ReadCookieFile(cfg.CookiePath)
	s := &BiliScheduler{
		db:              cfg.DB,
		dl:              cfg.Downloader,
		downloadDir:     cfg.DownloadDir,
		cookiePath:      cfg.CookiePath,
		notifier:        cfg.Notifier,
		bili:            bilibili.NewClient(cookie),
		hotConfig:       cfg.HotConfig,
		upInfoCache:     make(map[int64]*upInfoCacheEntry),
		fullScanRunning: make(map[int64]bool),
		videoSema:       bilibili.NewSemaphore(3),
		pageSema:        bilibili.NewSemaphore(2),
		downloadLimiter: bilibili.NewRateLimiter(4, 1, 15*time.Second),
		wg:              cfg.WG,
	}
	if s.wg == nil {
		s.wg = &sync.WaitGroup{}
	}
	return s
}

// ─── PlatformScheduler 接口实现 ───────────────────────────────────────────────

// CheckSource 根据 source 类型分发到对应的检查方法
func (s *BiliScheduler) CheckSource(src db.Source) {
	switch src.Type {
	case "season":
		s.CheckSeason(src)
	case "series":
		s.CheckSeries(src)
	case "favorite":
		s.CheckFavorite(src)
	case "watchlater":
		s.CheckWatchLater(src)
	case "up", "channel", "":
		s.CheckUP(src)
	default:
		s.CheckUP(src)
	}
}

// RetryDownload 重试单个 B站下载记录
func (s *BiliScheduler) RetryDownload(dl db.Download) {
	s.retryOneDownload(dl)
}

// IsPaused 返回 B站下载器是否处于暂停状态
func (s *BiliScheduler) IsPaused() bool {
	if s.dl == nil {
		return false
	}
	return s.dl.IsPaused()
}

// Stop 清理资源
func (s *BiliScheduler) Stop() {
	if s.downloadLimiter != nil {
		s.downloadLimiter.Stop()
	}
}
