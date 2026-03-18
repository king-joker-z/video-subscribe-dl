package bscheduler

import (
	"fmt"
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
	log.Printf("[bscheduler][WARN] 触发B站风控，暂停下载器 %v（恢复时间: %s）",
		config.CooldownDuration, s.cooldownUntil.Format("15:04:05"))

	if time.Since(s.lastCooldownNotify) > 30*time.Minute {
		s.lastCooldownNotify = time.Now()
		s.notifier.Send(notify.EventRateLimited, "B站风控触发",
			fmt.Sprintf("已暂停 %v，预计 %s 恢复",
				config.CooldownDuration, s.cooldownUntil.Format("15:04:05")))
	}
}

// IsInCooldown 检查 B站是否在风控冷却期内
func (s *BiliScheduler) IsInCooldown() bool {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	return time.Now().Before(s.cooldownUntil)
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
