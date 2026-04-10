package bscheduler

import (
	"log"
	"time"

	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/notify"
)

// TriggerCooldown 触发 B站风控冷却
func (s *BiliScheduler) TriggerCooldown() {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	s.cooldownUntil = time.Now().Add(config.CooldownDuration)
	if s.dl != nil {
		s.dl.Pause()
	}
	log.Printf("[bscheduler][WARN] 触发B站风控，下载器已暂停，需在 Web UI 手动恢复")

	if time.Since(s.lastCooldownNotify) > 30*time.Minute {
		s.lastCooldownNotify = time.Now()
		s.notifier.Send(notify.EventRateLimited, "B站风控触发",
			"下载器已暂停，请在 Web UI 手动恢复")
	}
}

// IsInCooldown 检查 B站是否在风控冷却期内
func (s *BiliScheduler) IsInCooldown() bool {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	return time.Now().Before(s.cooldownUntil)
}

// ClearCooldown 手动清除 B 站风控冷却状态
func (s *BiliScheduler) ClearCooldown() {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	s.cooldownUntil = time.Time{}
	log.Printf("[bscheduler] B站风控冷却已手动清除")
}

// GetCooldownInfo 返回风控冷却状态（供 API 使用）
func (s *BiliScheduler) GetCooldownInfo() (inCooldown bool, remainingSec int) {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	if time.Now().Before(s.cooldownUntil) {
		return true, int(time.Until(s.cooldownUntil).Seconds())
	}
	return false, 0
}
