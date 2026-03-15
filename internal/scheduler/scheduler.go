package scheduler

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/nfo"
	"video-subscribe-dl/internal/notify"
)

type Scheduler struct {
	db          *db.DB
	dl          *downloader.Downloader
	bili        *bilibili.Client
	downloadDir string
	cookiePath  string
	notifier    *notify.Notifier
	stopCh      chan struct{}
	wg          sync.WaitGroup

	// bili client 保护
	biliMu sync.RWMutex

	// 风控退避
	rateLimitMu        sync.Mutex
	rateLimitUntil     time.Time // 风控冷却截止时间
	lastCooldownNotify time.Time // 上次风控通知时间（防重复）

	// Cookie 定期检测
	lastCookieCheck time.Time // 上次 Cookie 主动检测时间

	// Credential 管理
	db2 *db.DB // alias, same as db (for clarity in credential methods)

	// 并发控制信号量（参考 bili-sync workflow.rs）
	videoSema *bilibili.Semaphore // video 级别并发限制
	pageSema  *bilibili.Semaphore // page 级别并发限制

	// 热配置
	hotConfig     *config.HotConfig
	configWatcher *config.ConfigWatcher

	// UP 主信息缓存（减少 API 请求）
	upInfoCache   map[int64]*upInfoCacheEntry
	upInfoCacheMu sync.RWMutex

	// cron 调度器
	cronScheduler *cron.Cron

	// 全量扫描去重
	fullScanRunning   map[int64]bool
	fullScanRunningMu sync.Mutex

	// 风控断点续检：被中断的 source 列表
	pendingSourcesMu sync.Mutex
	pendingSources   []db.Source
}

type upInfoCacheEntry struct {
	info      *bilibili.UPInfo
	fetchedAt time.Time
}

func New(database *db.DB, dl *downloader.Downloader, downloadDir, cookiePath string) *Scheduler {
	cookie := bilibili.ReadCookieFile(cookiePath)
	return &Scheduler{
		db:              database,
		dl:              dl,
		bili:            bilibili.NewClient(cookie),
		downloadDir:     downloadDir,
		cookiePath:      cookiePath,
		notifier:        notify.New(database),
		stopCh:          make(chan struct{}),
		hotConfig:       config.NewHotConfig(),
		upInfoCache:     make(map[int64]*upInfoCacheEntry),
		fullScanRunning: make(map[int64]bool),
		videoSema:       bilibili.NewSemaphore(3), // 最多同时处理 3 个视频
		pageSema:        bilibili.NewSemaphore(2), // 每个视频最多同时下载 2 个分P
	}
}

// GetNotifier 返回通知器实例（供 web server 使用）
func (s *Scheduler) GetNotifier() *notify.Notifier {
	return s.notifier
}

// getBili 线程安全地获取 bilibili client
// GetBiliClient 公开方法：线程安全地获取 bilibili client
func (s *Scheduler) GetBiliClient() *bilibili.Client {
	s.biliMu.RLock()
	defer s.biliMu.RUnlock()
	return s.bili
}

func (s *Scheduler) getBili() *bilibili.Client {
	s.biliMu.RLock()
	defer s.biliMu.RUnlock()
	return s.bili
}

// resetCaches 清除 WBI 签名缓存，在 cookie 更新/风控恢复等场景下调用
func (s *Scheduler) resetCaches() {
	bilibili.ClearWbiCache()
}

// applyNewClient 创建新的 bilibili client 并同步更新 scheduler 和 downloader
func (s *Scheduler) applyNewClient(client *bilibili.Client) {
	s.biliMu.Lock()
	s.bili = client
	s.biliMu.Unlock()
	s.dl.UpdateClient(client)
	s.resetCaches()
}

// UpdateCredential 使用新的 Credential 更新 client
func (s *Scheduler) UpdateCredential(cred *bilibili.Credential) {
	client := bilibili.NewClientWithCredential(cred)
	s.applyNewClient(client)
}

func (s *Scheduler) Start() {
	// 初始化 cookiePath 缓存
	if s.cookiePath == "" {
		if cp, err := s.db.GetSetting("cookie_path"); err == nil && cp != "" {
			s.cookiePath = cp
		}

		// 启动配置热更新监视器
		s.configWatcher = config.NewConfigWatcher(s.hotConfig, s.db, 30*time.Second)
		s.configWatcher.Start()
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// 启动时重置 stale pending/downloading 状态（容器重启后队列已清空）
		if reset, err := s.db.ResetStaleDownloads(); err == nil && reset > 0 {
			log.Printf("[startup] Reset %d stale pending/downloading records (will be requeued)", reset)
		}
		s.verifyCookie("startup")
		// 启动时处理容器重启前遗留的 pending 下载
		// 动态并发控制（从设置读取）
		if v, err := s.db.GetSetting("concurrent_video"); err == nil && v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				s.videoSema = bilibili.NewSemaphore(n)
				log.Printf("[scheduler] video 并发数: %d", n)
			}
		}
		if v, err := s.db.GetSetting("concurrent_page"); err == nil && v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				s.pageSema = bilibili.NewSemaphore(n)
				log.Printf("[scheduler] page 并发数: %d", n)
			}
		}
		s.ProcessAllPending()
		s.checkAll()

		// 检查是否配置了 cron 表达式
		cronExpr, _ := s.db.GetSetting("schedule_cron")
		if cronExpr != "" {
			s.cronScheduler = cron.New(cron.WithSeconds())
			_, err := s.cronScheduler.AddFunc(cronExpr, func() {
				// Cookie 检查不受风控冷却影响
				s.periodicCookieCheck()

				if s.isInCooldown() {
					remaining := time.Until(s.rateLimitUntil).Round(time.Second)
					log.Printf("[scheduler] 风控冷却中，剩余 %v，跳过本轮检查", remaining)
					return
				}
				if s.dl.IsPaused() {
					s.dl.Resume()
					log.Printf("[scheduler] 风控冷却结束，恢复下载器")
				}
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
				// Cookie 检查不受风控冷却影响
				s.periodicCookieCheck()

				// 风控冷却期内跳过检查
				if s.isInCooldown() {
					remaining := time.Until(s.rateLimitUntil).Round(time.Second)
					log.Printf("[scheduler] 风控冷却中，剩余 %v，跳过本轮检查", remaining)
					continue
				}
				// 冷却已过期，自动恢复 downloader
				if s.dl.IsPaused() {
					s.dl.Resume()
					log.Printf("[scheduler] 风控冷却结束，恢复下载器")
				}
				s.checkAll()
			case <-s.stopCh:
				return
			}
		}
	}()
	log.Println("Scheduler started (interval: 5min)")
}

// isInCooldown 检查是否在风控冷却期内
func (s *Scheduler) isInCooldown() bool {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	return time.Now().Before(s.rateLimitUntil)
}

// GetCooldownInfo 返回风控冷却状态（供 API 使用）
func (s *Scheduler) GetCooldownInfo() (inCooldown bool, remainingSec int) {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	if time.Now().Before(s.rateLimitUntil) {
		return true, int(time.Until(s.rateLimitUntil).Seconds())
	}
	return false, 0
}

// triggerCooldown 触发风控冷却
func (s *Scheduler) triggerCooldown() {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	s.rateLimitUntil = time.Now().Add(config.CooldownDuration)
	s.dl.Pause()
	log.Printf("[WARN] 触发B站风控，暂停下载器 %v（恢复时间: %s）",
		config.CooldownDuration, s.rateLimitUntil.Format("15:04:05"))

	// 防重复通知：30分钟内只发一次
	if time.Since(s.lastCooldownNotify) > 30*time.Minute {
		s.lastCooldownNotify = time.Now()
		s.notifier.Send(notify.EventRateLimited, "B站风控触发",
			fmt.Sprintf("已暂停 %v，预计 %s 恢复",
				config.CooldownDuration, s.rateLimitUntil.Format("15:04:05")))
	}
}

func (s *Scheduler) Stop() {
	if s.configWatcher != nil {
		s.configWatcher.Stop()
	}
	close(s.stopCh)
	s.wg.Wait()
}

func (s *Scheduler) CheckNow() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.checkAllForce()
	}()
}

// ProcessAllPending 把所有 pending 记录提交到下载队列
func (s *Scheduler) ProcessAllPending() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		downloads, err := s.db.GetDownloadsByStatus("pending", 10000)
		if err != nil {
			log.Printf("[process-pending] Error: %v", err)
			return
		}
		if len(downloads) == 0 {
			log.Printf("[process-pending] No pending downloads")
			return
		}
		// 分批处理：同时只有 3 个在下载队列中（和 worker 数一致）
		batchSize := 3
		log.Printf("[process-pending] Processing %d pending downloads (batch size: %d)", len(downloads), batchSize)
		for i := 0; i < len(downloads); i += batchSize {
			end := i + batchSize
			if end > len(downloads) {
				end = len(downloads)
			}
			batch := downloads[i:end]
			for _, dl := range batch {
				s.retryOneDownload(dl)
				time.Sleep(1 * time.Second)
			}
			// 等当前批次下载完再提交下一批（检查队列是否空了）
			log.Printf("[process-pending] Batch %d-%d submitted, waiting for completion...", i+1, end)
			for s.dl.IsBusy() {
				time.Sleep(5 * time.Second)
			}
		}
		log.Printf("[process-pending] All %d pending downloads processed", len(downloads))
	}()
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

func (s *Scheduler) UpdateCookie(cookiePath string) {
	s.cookiePath = cookiePath // 同步更新缓存
	cookie := bilibili.ReadCookieFile(cookiePath)
	s.applyNewClient(bilibili.NewClient(cookie))
}

func (s *Scheduler) checkAll() {
	// 如果冷却已过期但 downloader 还在 paused，自动恢复
	if s.dl.IsPaused() && !s.isInCooldown() {
		s.dl.Resume()
		log.Printf("[scheduler] checkAll: 风控冷却已过期，恢复下载器")
	}
	// 先检查 Credential 是否需要刷新
	s.checkAndRefreshCredential()
	s.verifyCookie("scheduled sync")

	// Retry failed downloads
	s.retryFailedDownloads()

	// 优先处理风控断点续检
	s.pendingSourcesMu.Lock()
	resumeSources := s.pendingSources
	s.pendingSources = nil
	s.pendingSourcesMu.Unlock()

	if len(resumeSources) > 0 {
		log.Printf("[scheduler] 风控断点续检: 恢复 %d 个未检查的 source", len(resumeSources))
		s.checkSourceList(resumeSources)
		// 断点续检完毕后处理 pending 下载
		s.ProcessAllPending()
		return // 本轮只做断点恢复，不再拉新的 due sources
	}

	// 读取全局 check_interval_minutes 设置
	globalInterval := 0
	if val, err := s.db.GetSetting("check_interval_minutes"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			globalInterval = n * 60 // 转为秒
		}
	}

	sources, err := s.db.GetSourcesDueForCheck(globalInterval)
	if err != nil {
		log.Printf("Get due sources failed: %v", err)
		return
	}
	s.checkSourceList(sources)

	// 所有 source 检查完后，处理遗留的 pending 下载
	s.ProcessAllPending()

	// Auto-cleanup: retention-based and disk-pressure
}

// checkSourceList 检查一组 source，风控时保存剩余到断点队列
func (s *Scheduler) checkSourceList(sources []db.Source) {
	for i, src := range sources {
		s.checkSource(src)
		s.db.UpdateSourceLastCheck(src.ID)

		// 检查风控冷却
		if s.isInCooldown() {
			remaining := sources[i+1:]
			if len(remaining) > 0 {
				s.pendingSourcesMu.Lock()
				s.pendingSources = remaining
				s.pendingSourcesMu.Unlock()
				log.Printf("[scheduler] 风控冷却已触发，保存 %d 个剩余 source 到断点队列", len(remaining))
			}
			return
		}

		// source 间隔 5 秒，避免触发风控
		if i < len(sources)-1 {
			time.Sleep(5 * time.Second)
		}
	}
}

func (s *Scheduler) checkAllForce() {
	log.Println("Manual sync triggered")
	s.verifyCookie("manual sync")
	sources, err := s.db.GetEnabledSources()
	if err != nil {
		log.Printf("Get sources failed: %v", err)
		return
	}
	for i, src := range sources {
		s.checkSource(src)
		s.db.UpdateSourceLastCheck(src.ID)

		// 检查风控冷却
		if s.isInCooldown() {
			log.Printf("[scheduler] 风控冷却已触发，停止当前轮次剩余 source 检查")
			break
		}

		// source 间隔 3 秒，避免触发风控
		if i < len(sources)-1 {
			time.Sleep(5 * time.Second)
		}
	}
	s.ProcessAllPending()
	log.Println("Manual sync completed")
}

func (s *Scheduler) checkSource(src db.Source) {
	log.Printf("Checking: %s (%s) [type=%s]", src.Name, src.URL, src.Type)

	switch src.Type {
	case "season":
		s.checkSeason(src)
	case "series":
		s.checkSeries(src)
	case "favorite":
		s.checkFavorite(src)
	case "watchlater":
		s.checkWatchLater(src)
	case "up", "channel", "":
		s.checkUP(src)
	default:
		log.Printf("[WARN] Unknown source type: %s, treating as UP", src.Type)
		s.checkUP(src)
	}
}

func (s *Scheduler) clientForSource(src db.Source) *bilibili.Client {
	if src.CookiesFile != "" {
		cookie := bilibili.ReadCookieFile(src.CookiesFile)
		if cookie != "" {
			return bilibili.NewClient(cookie)
		}
	}
	return s.getBili()
}

func (s *Scheduler) ensurePeopleDir(upInfo *bilibili.UPInfo) {
	if upInfo == nil || upInfo.Name == "" {
		return
	}
	dir := filepath.Join(s.downloadDir, "metadata", "people", bilibili.SanitizePath(upInfo.Name))
	os.MkdirAll(dir, 0755)
	// 每轮更新 person.nfo（追踪签名/等级变化）
	nfo.GeneratePersonNFO(&nfo.PersonMeta{
		Name:  upInfo.Name,
		Thumb: upInfo.Face,
		MID:   upInfo.MID,
		Sign:  upInfo.Sign,
		Level: upInfo.Level,
		Sex:   upInfo.Sex,
	}, dir)
	if upInfo.Face != "" {
		avatarPath := filepath.Join(dir, "folder.jpg")
		if _, err := os.Stat(avatarPath); os.IsNotExist(err) {
			if err := bilibili.DownloadFile(upInfo.Face, avatarPath); err != nil {
				log.Printf("Avatar download failed for %s: %v", upInfo.Name, err)
			}
		}
	}
}

// periodicCookieCheck 每 6 小时主动检测 Cookie 有效性
func (s *Scheduler) periodicCookieCheck() {
	if time.Since(s.lastCookieCheck) < 6*time.Hour {
		return
	}
	s.lastCookieCheck = time.Now()
	log.Println("[scheduler] Periodic cookie check triggered")

	result, err := s.getBili().VerifyCookie()
	if err != nil {
		log.Printf("[WARN] Periodic cookie check failed: %v", err)
		return
	}
	if !result.LoggedIn {
		log.Printf("[WARN] Periodic cookie check: Cookie is invalid or expired")
		s.notifier.Send(notify.EventCookieExpired, "Cookie 已过期（定期检测）",
			"定期检测发现 Cookie 已失效，请及时更新")
		// 尝试自动刷新
		s.tryCookieRefresh("periodic check")
	}
}

// verifyCookie 验证当前 cookie 是否有效，即将过期时自动刷新，失效打 warning
func (s *Scheduler) verifyCookie(trigger string) {
	result, err := s.getBili().VerifyCookie()
	if err != nil {
		log.Printf("[WARN] Cookie verify failed during %s: %v", trigger, err)
		return
	}
	if !result.LoggedIn {
		log.Printf("[WARN] Cookie is invalid or expired (trigger: %s). Attempting refresh...", trigger)
		s.tryCookieRefresh(trigger)
		return
	}

	vipLabel := "无"
	switch result.VIPType {
	case 1:
		vipLabel = "月度大会员"
	case 2:
		vipLabel = "年度大会员"
	}

	// 检查 VIP 到期时间
	if result.VIPDueDate != "" {
		dueDate, parseErr := time.Parse("2006-01-02", result.VIPDueDate)
		if parseErr == nil {
			daysUntil := time.Until(dueDate).Hours() / 24
			if daysUntil < -30 {
				// 过期超过30天，不再尝试刷新，只在 startup 时提示一次
				if trigger == "startup" {
					log.Printf("[INFO] Cookie valid: user=%s, VIP=%s (已过期: %s). 非大会员仍可下载 1080P/192kbps",
						result.Username, vipLabel, result.VIPDueDate)
				}
				return
			} else if daysUntil < 7 {
				log.Printf("[INFO] Cookie/VIP 将在 %s 到期（<7天），尝试刷新...", result.VIPDueDate)
				s.tryCookieRefresh(trigger)
			}
		}
	}

	log.Printf("[INFO] Cookie valid: user=%s, VIP=%s, expires=%s (trigger: %s)",
		result.Username, vipLabel, result.VIPDueDate, trigger)
}

// tryCookieRefresh 尝试自动刷新 Cookie
func (s *Scheduler) tryCookieRefresh(trigger string) {
	cookiePath := s.cookiePath
	if cookiePath == "" {
		log.Printf("[WARN] No cookie path configured, cannot auto-refresh")
		s.notifier.Send(notify.EventCookieExpired, "Cookie 已过期", "未配置 cookie 路径，请手动更新 Cookie")
		return
	}

	refreshResult, err := s.getBili().RefreshCookie(cookiePath)
	if err != nil {
		log.Printf("[WARN] Cookie refresh error during %s: %v", trigger, err)
		s.notifier.Send(notify.EventCookieExpired, "Cookie 刷新失败", fmt.Sprintf("错误: %v，请手动更新 Cookie", err))
		return
	}

	if refreshResult.Success {
		log.Printf("[INFO] Cookie 自动刷新成功 (trigger: %s)", trigger)
		// 刷新下载器的 client 并清除缓存
		s.resetCaches()
		s.dl.UpdateClient(s.getBili())
	} else {
		log.Printf("[WARN] Cookie 刷新失败: %s (trigger: %s). 请手动更新 Cookie。", refreshResult.Message, trigger)
		s.notifier.Send(notify.EventCookieExpired, "Cookie 需要手动更新", refreshResult.Message)
	}
}

// checkAndRefreshCredential 检查 DB 中的 Credential 是否需要刷新，需要则自动刷新
func (s *Scheduler) checkAndRefreshCredential() {
	credJSON, err := s.db.GetSetting("credential_json")
	if err != nil || credJSON == "" {
		return // 没有 credential，跳过
	}
	cred := bilibili.CredentialFromJSON(credJSON)
	if cred == nil || cred.IsEmpty() {
		return
	}

	httpClient := s.getBili().GetHTTPClient()
	needRefresh, err := cred.NeedRefresh(httpClient)
	if err != nil {
		log.Printf("[credential] NeedRefresh check failed: %v", err)
		return
	}
	if !needRefresh {
		return
	}

	log.Printf("[credential] Cookie needs refresh, attempting auto-refresh...")
	newCred, err := cred.Refresh(httpClient)
	if err != nil {
		log.Printf("[WARN] Credential auto-refresh failed: %v", err)
		s.notifier.Send(notify.EventCookieExpired, "凭证自动刷新失败", err.Error())
		return
	}

	// 保存到 DB
	if err := s.db.SetSetting("credential_json", newCred.ToJSON()); err != nil {
		log.Printf("[WARN] Save refreshed credential failed: %v", err)
		return
	}

	// 更新 scheduler 的 client
	s.UpdateCredential(newCred)
	log.Printf("[credential] Auto-refresh successful")
}

// ReloadConfig 手动触发配置重载（API 调用后立即生效）
func (s *Scheduler) ReloadConfig() {
	if s.configWatcher != nil {
		s.configWatcher.Reload()
	}
}

// GetHotConfig 获取当前热配置快照
func (s *Scheduler) GetHotConfig() config.HotConfigSnapshot {
	return s.hotConfig.Get()
}

// getFilenameTemplate 获取文件名模板（从热配置或 DB）
func (s *Scheduler) getFilenameTemplate() string {
	if s.hotConfig != nil {
		snap := s.hotConfig.Get()
		if snap.FilenameTemplate != "" {
			return snap.FilenameTemplate
		}
	}
	if tmpl, err := s.db.GetSetting("filename_template"); err == nil && tmpl != "" {
		return tmpl
	}
	return config.DefaultFilenameTemplate
}
