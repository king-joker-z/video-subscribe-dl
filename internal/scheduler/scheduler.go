package scheduler

import (
	"fmt"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/scheduler/bscheduler"
	"video-subscribe-dl/internal/scheduler/dscheduler"
)

// Scheduler 顶层编排器：持有子平台 scheduler，统一管理生命周期和任务分发。
// 所有 B 站平台相关逻辑委托给 bili (*bscheduler.BiliScheduler)。
// 所有抖音平台相关逻辑委托给 douyin (*dscheduler.DouyinScheduler)。
type Scheduler struct {
	db          *db.DB
	dl          *downloader.Downloader
	downloadDir string
	notifier    *notify.Notifier
	stopCh      chan struct{}
	wg          sync.WaitGroup

	// B 站子调度器（负责所有 B 站平台逻辑）
	bili *bscheduler.BiliScheduler

	// 抖音子调度器（负责所有抖音平台逻辑）
	douyin *dscheduler.DouyinScheduler

	// cron 调度器
	cronScheduler *cron.Cron

	// 防重入：同时只允许一个 ProcessAllPending goroutine 运行
	processPendingRunning int32
}

// New 创建顶层 Scheduler，同时初始化 BiliScheduler 和 DouyinScheduler 子调度器。
func New(database *db.DB, dl *downloader.Downloader, downloadDir, cookiePath string) *Scheduler {
	notifier := notify.New(database)
	hotConfig := config.NewHotConfig()
	wg := &sync.WaitGroup{}

	bili := bscheduler.New(bscheduler.Config{
		DB:          database,
		Downloader:  dl,
		DownloadDir: downloadDir,
		CookiePath:  cookiePath,
		Notifier:    notifier,
		HotConfig:   hotConfig,
		WG:          wg,
	})

	douyinSched := dscheduler.New(dscheduler.Config{
		DB:          database,
		DownloadDir: downloadDir,
		Notifier:    notifier,
	})

	return &Scheduler{
		db:          database,
		dl:          dl,
		downloadDir: downloadDir,
		notifier:    notifier,
		stopCh:      make(chan struct{}),
		bili:        bili,
		douyin:      douyinSched,
	}
}

// ─── 生命周期 ─────────────────────────────────────────────────────────────────

func (s *Scheduler) Start() {
	// 初始化热配置监视器（通过 bscheduler 的启动流程）
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		// 启动时重置 stale pending/downloading 状态
		if reset, err := s.db.ResetStaleDownloads(); err == nil && reset > 0 {
			log.Printf("[startup] Reset %d stale pending/downloading records (will be requeued)", reset)
		}

		// 委托给 BiliScheduler 做 B 站初始化工作
		s.bili.Startup()

		s.douyin.LoadUserCookie()
		s.ProcessAllPending()
		s.checkAll()

		// Cron 或固定间隔调度
		cronExpr, _ := s.db.GetSetting("schedule_cron")
		if cronExpr != "" {
			s.cronScheduler = cron.New(cron.WithSeconds())
			_, err := s.cronScheduler.AddFunc(cronExpr, func() {
				s.bili.PeriodicCookieCheck()
				s.checkAll()
			})
			if err != nil {
				log.Printf("[scheduler] Cron 表达式无效 (%s): %v，降级为固定间隔", cronExpr, err)
			} else {
				log.Printf("[scheduler] 使用 Cron 调度: %s", cronExpr)
				s.cronScheduler.Start()
				<-s.stopCh
				s.cronScheduler.Stop()
				return
			}
		}

		// 降级：固定间隔调度
		interval := config.DefaultSchedulerTick
		if v, err := s.db.GetSetting("check_interval_minutes"); err == nil && v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				interval = time.Duration(n) * time.Minute
			}
		}
		log.Printf("[scheduler] 使用固定间隔调度: %v", interval)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.bili.PeriodicCookieCheck()
				s.checkAll()
			case <-s.stopCh:
				return
			}
		}
	}()
	// 转发 dscheduler 抖音事件到 downloader SSE 通道，使前端能收到 vsd:download-event
	if s.dl != nil && s.douyin != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			evtCh := s.douyin.GetEventChan()
			for {
				select {
				case <-s.stopCh:
					return
				case evt, ok := <-evtCh:
					if !ok {
						return
					}
					// 二次确认 stop，避免 stopCh 关闭时与 evtCh 竞争导致多发一次事件
					select {
					case <-s.stopCh:
						return
					default:
					}
					s.dl.EmitEvent(downloader.DownloadEvent{
						Type:     evt.Type,
						BvID:     evt.VideoID,
						Title:    evt.Title,
						FileSize: evt.FileSize,
						Error:    evt.Error,
					})
				}
			}
		}()
	}

	log.Println("Scheduler started (interval: 5min)")
}

func (s *Scheduler) Stop() {
	s.bili.Stop()
	if s.douyin != nil {
		s.douyin.Stop()
	}
	close(s.stopCh)
	s.wg.Wait()
}

// ─── 检查逻辑 ──────────────────────────────────────────────────────────────────

func (s *Scheduler) checkAll() {
	// 先检查 Credential 是否需要刷新（委托给 bscheduler）
	s.bili.CheckAndRefreshCredential()
	s.bili.VerifyCookie("scheduled sync")

	// Retry failed downloads
	s.retryFailedDownloads()

	globalInterval := 0
	if val, err := s.db.GetSetting("check_interval_minutes"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			globalInterval = n * 60
		}
	}

	sources, err := s.db.GetSourcesDueForCheck(globalInterval)
	if err != nil {
		log.Printf("Get due sources failed: %v", err)
		return
	}
	s.checkSourceList(sources)
	s.ProcessAllPending()
}

// checkSourceList 检查一组 source，按平台级冷却跳过对应源
func (s *Scheduler) checkSourceList(sources []db.Source) {
	for i, src := range sources {
		switch src.Type {
		case "douyin", "douyin_mix":
			if s.isDouyinInCooldown() {
				continue
			}
		default:
			if s.isBiliInCooldown() {
				continue
			}
		}

		s.safeCheckSource(src)
		s.db.UpdateSourceLastCheck(src.ID)

		if i < len(sources)-1 {
			time.Sleep(5 * time.Second)
		}
	}
}

// safeCheckSource 带 panic 保护的 checkSource 调用
func (s *Scheduler) safeCheckSource(src db.Source) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] checkSource panic for %s (id=%d): %v", src.Name, src.ID, r)
		}
	}()
	s.checkSource(src)
}

func (s *Scheduler) checkAllForce() {
	log.Println("Manual sync triggered")
	s.bili.VerifyCookie("manual sync")
	sources, err := s.db.GetEnabledSources()
	if err != nil {
		log.Printf("Get sources failed: %v", err)
		return
	}
	for i, src := range sources {
		switch src.Type {
		case "douyin", "douyin_mix":
			if s.isDouyinInCooldown() {
				continue
			}
		default:
			if s.isBiliInCooldown() {
				continue
			}
		}

		s.safeCheckSource(src)
		s.db.UpdateSourceLastCheck(src.ID)

		if i < len(sources)-1 {
			time.Sleep(5 * time.Second)
		}
	}
	s.ProcessAllPending()
	log.Println("Manual sync completed")
}

// checkSource 按 source 类型分发到对应平台 scheduler
func (s *Scheduler) checkSource(src db.Source) {
	log.Printf("Checking: %s (%s) [type=%s]", src.Name, src.URL, src.Type)

	switch src.Type {
	case "douyin", "douyin_mix":
		s.douyin.CheckSource(src)
		return
	default:
		// 所有 B 站类型委托给 bscheduler
		s.bili.CheckSource(src)
	}
}

// ─── 公开 API 方法 ─────────────────────────────────────────────────────────────

// CheckNow 触发一次立即全量检查
func (s *Scheduler) CheckNow() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.checkAllForce()
	}()
}

// ProcessAllPending 把所有 pending 记录提交到下载队列。
// 使用 atomic 防重入，避免多个 goroutine 同时运行导致重复提交。
func (s *Scheduler) ProcessAllPending() {
	if s.dl == nil {
		return
	}
	// 防重入：同时只允许一个 goroutine 运行
	if !atomic.CompareAndSwapInt32(&s.processPendingRunning, 0, 1) {
		log.Printf("[process-pending] Already running, skip")
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer atomic.StoreInt32(&s.processPendingRunning, 0)

		downloads, err := s.db.GetDownloadsByStatus("pending", 10000)
		if err != nil {
			log.Printf("[process-pending] Error: %v", err)
			return
		}
		if len(downloads) == 0 {
			return
		}
		log.Printf("[process-pending] Submitting %d pending downloads to queue", len(downloads))
		submitted := 0
		for _, dl := range downloads {
			if err := s.submitDownload(dl); err != nil {
				log.Printf("[process-pending] Submit failed for %d: %v", dl.ID, err)
			} else {
				submitted++
			}
		}
		log.Printf("[process-pending] Submitted %d/%d downloads", submitted, len(downloads))
	}()
}

// submitDownload 将单个 download 记录提交到对应平台下载器，不重复查 DB。
func (s *Scheduler) submitDownload(dl db.Download) error {
	src, err := s.db.GetSource(dl.SourceID)
	if err != nil || src == nil {
		return fmt.Errorf("source %d not found", dl.SourceID)
	}
	if src.Type == "douyin" || src.Type == "douyin_mix" {
		s.douyin.RetryDownload(dl)
		return nil
	}
	if s.bili != nil {
		s.bili.RetryDownload(dl)
		return nil
	}
	return fmt.Errorf("no scheduler for type %s", src.Type)
}

// CheckOneSource 只同步指定 source
func (s *Scheduler) CheckOneSource(sourceID int64) {
	src, err := s.db.GetSource(sourceID)
	if err != nil || src == nil {
		log.Printf("[scheduler] Source %d not found", sourceID)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.checkSource(*src)
		log.Printf("Manual sync completed for source %d: %s", src.ID, src.Name)
	}()
}

// FullScanSource 触发单个 source 的全量补漏扫描
func (s *Scheduler) FullScanSource(sourceID int64) {
	src, err := s.db.GetSource(sourceID)
	if err != nil || src == nil {
		log.Printf("[scheduler] FullScanSource: source %d not found", sourceID)
		return
	}
	switch src.Type {
	case "douyin", "douyin_mix":
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.douyin.FullScanDouyin(*src)
		}()
	default:
		s.bili.FullScanSource(sourceID)
	}
}

// SyncAll 触发全部源检查（供 API 调用）
func (s *Scheduler) SyncAll() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.checkAllForce()
	}()
}

// StartupCleanup 一次性启动清理（扫描非法字符目录 + 重置全量扫描）
func (s *Scheduler) StartupCleanup() {
	s.bili.StartupCleanup()
}

// ─── 代理方法：B 站平台 ────────────────────────────────────────────────────────

// GetBiliClient 获取 bilibili client（供 web API 使用）
func (s *Scheduler) GetBiliClient() *bilibili.Client {
	return s.bili.GetBiliClient()
}

// UpdateCredential 更新 B 站 Credential
func (s *Scheduler) UpdateCredential(cred *bilibili.Credential) {
	s.bili.UpdateCredential(cred)
}

// UpdateCookie 更新 B 站 Cookie 文件路径
func (s *Scheduler) UpdateCookie(cookiePath string) {
	s.bili.UpdateCookie(cookiePath)
}

// GetBiliCooldownInfo 返回 B 站风控冷却状态
func (s *Scheduler) GetBiliCooldownInfo() (bool, int) {
	return s.bili.GetCooldownInfo()
}

// ReloadConfig 手动触发配置重载
func (s *Scheduler) ReloadConfig() {
	s.bili.ReloadConfig()
}

// GetHotConfig 获取当前热配置快照
func (s *Scheduler) GetHotConfig() config.HotConfigSnapshot {
	return s.bili.GetHotConfig()
}

// ─── 代理方法：通知器 ──────────────────────────────────────────────────────────

// GetNotifier 返回通知器实例
func (s *Scheduler) GetNotifier() *notify.Notifier {
	return s.notifier
}

// ─── 风控冷却（顶层汇总）──────────────────────────────────────────────────────

// isBiliInCooldown 检查 B 站是否在风控冷却期内
func (s *Scheduler) isBiliInCooldown() bool {
	if s.bili == nil {
		return false
	}
	return s.bili.IsInCooldown()
}

// isDouyinInCooldown 检查抖音是否在风控冷却期内
func (s *Scheduler) isDouyinInCooldown() bool {
	return s.douyin.IsInCooldown()
}

// GetCooldownInfo 返回风控冷却状态（供 API 使用，合并两个平台）
func (s *Scheduler) GetCooldownInfo() (inCooldown bool, remainingSec int) {
	_, douyinSec := s.douyin.GetCooldownInfo()

	var biliSec int
	if s.bili != nil {
		biliIn, sec := s.bili.GetCooldownInfo()
		if biliIn {
			biliSec = sec
		}
	}

	if douyinSec > 0 || biliSec > 0 {
		if douyinSec > biliSec {
			return true, douyinSec
		}
		return true, biliSec
	}
	return false, 0
}

// ─── B 站风控恢复（手动）────────────────────────────────────────────────────

// ResumeBili 手动恢复 B 站下载器（风控触发后需手动恢复）
func (s *Scheduler) ResumeBili() {
	if s.bili != nil {
		s.bili.ClearCooldown()
	}
	if s.dl != nil {
		s.dl.Resume()
	}
	log.Printf("[scheduler] B站风控已手动恢复")
}

// IsBiliPaused 检查 B 站下载器是否被暂停
func (s *Scheduler) IsBiliPaused() bool {
	if s.dl == nil {
		return false
	}
	return s.dl.IsPaused()
}

// ─── 抖音暂停控制（委托给 dscheduler）────────────────────────────────────────

// PauseDouyin 暂停抖音下载（风控触发，需手动恢复）
func (s *Scheduler) PauseDouyin(reason string) {
	s.douyin.Pause(reason)
}

// ResumeDouyin 手动恢复抖音下载
func (s *Scheduler) ResumeDouyin() {
	s.douyin.Resume()
}

// IsDouyinPaused 检查抖音是否被暂停
func (s *Scheduler) IsDouyinPaused() bool {
	return s.douyin.IsPaused()
}

// GetDouyinPauseStatus 返回抖音暂停状态详情（供 API 使用）
func (s *Scheduler) GetDouyinPauseStatus() (paused bool, reason string, pausedAt time.Time) {
	return s.douyin.GetPauseStatus()
}

// RefreshDouyinUserCookie 热更新：从 DB 重新加载并应用抖音 Cookie
func (s *Scheduler) RefreshDouyinUserCookie(cookie string) {
	s.douyin.RefreshCookie(cookie)
}

// GetDouyinCookieStatus 返回抖音 Cookie 的当前有效性状态（供 API 注入使用）
func (s *Scheduler) GetDouyinCookieStatus() (bool, string) {
	st := s.douyin.GetDouyinCookieStatus()
	return st.Valid, st.Msg
}
