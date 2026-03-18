package bscheduler

import (
	"log"
	"time"

	"video-subscribe-dl/internal/config"
)

// Startup 执行 B 站调度器启动时的初始化工作
// 包括: 初始化热配置监视器、读取并发设置、验证 cookie
func (s *BiliScheduler) Startup() {
	// 启动热配置监视器
	s.configWatcherMu.Lock()
	if s.configWatcher == nil {
		s.configWatcher = config.NewConfigWatcher(s.hotConfig, s.db, 30*time.Second)
		s.configWatcher.Start()
	}
	s.configWatcherMu.Unlock()

	// 从 DB 读取 cookie path（如果未在 Config 中设置）
	if s.cookiePath == "" {
		if cp, err := s.db.GetSetting("cookie_path"); err == nil && cp != "" {
			s.cookiePath = cp
		}
	}

	// 应用并发设置
	s.ApplyConcurrencySettings()

	// 验证 B 站 Cookie
	s.VerifyCookie("startup")

	log.Println("[bscheduler] Startup complete")
}

// Stop 停止 BiliScheduler（释放资源）
func (s *BiliScheduler) Stop() {
	s.configWatcherMu.Lock()
	if s.configWatcher != nil {
		s.configWatcher.Stop()
		s.configWatcher = nil
	}
	s.configWatcherMu.Unlock()

	if s.downloadLimiter != nil {
		s.downloadLimiter.Stop()
	}
}

// ReloadConfig 手动触发配置重载
func (s *BiliScheduler) ReloadConfig() {
	s.configWatcherMu.Lock()
	watcher := s.configWatcher
	s.configWatcherMu.Unlock()
	if watcher != nil {
		watcher.Reload()
	}
}

// GetHotConfig 返回当前热配置快照
func (s *BiliScheduler) GetHotConfig() config.HotConfigSnapshot {
	return s.hotConfig.Get()
}
