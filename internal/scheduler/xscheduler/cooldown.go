package xscheduler

import (
	"log"
	"time"
)

const xcCooldownDuration = 10 * time.Minute

// TriggerCooldown 触发 xchina 冷却
func (s *XScheduler) TriggerCooldown() {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	s.cooldownUntil = time.Now().Add(xcCooldownDuration)
	log.Printf("[xscheduler] 触发冷却，恢复时间: %s", s.cooldownUntil.Format("15:04:05"))
}

// IsInCooldown 检查是否在冷却期内
func (s *XScheduler) IsInCooldown() bool {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	return time.Now().Before(s.cooldownUntil)
}

// GetCooldownInfo 返回冷却状态
func (s *XScheduler) GetCooldownInfo() (inCooldown bool, remainingSec int) {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	remaining := time.Until(s.cooldownUntil)
	if remaining > 0 {
		return true, int(remaining.Seconds())
	}
	return false, 0
}

// ClearCooldown 手动清除冷却
func (s *XScheduler) ClearCooldown() {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	s.cooldownUntil = time.Time{}
	log.Printf("[xscheduler] 冷却已手动清除")
}
