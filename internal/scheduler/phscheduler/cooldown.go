package phscheduler

import (
	"log"
	"time"
)

const phCooldownDuration = 10 * time.Minute

// TriggerCooldown 触发 PH 冷却
func (s *PHScheduler) TriggerCooldown() {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	s.cooldownUntil = time.Now().Add(phCooldownDuration)
	log.Printf("[phscheduler] 触发 PH 冷却，恢复时间: %s", s.cooldownUntil.Format("15:04:05"))
}

// IsInCooldown 检查是否在冷却期内
func (s *PHScheduler) IsInCooldown() bool {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	return time.Now().Before(s.cooldownUntil)
}

// GetCooldownInfo 返回冷却状态（供 API 使用）
func (s *PHScheduler) GetCooldownInfo() (inCooldown bool, remainingSec int) {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	remaining := time.Until(s.cooldownUntil)
	if remaining > 0 {
		return true, int(remaining.Seconds())
	}
	return false, 0
}

// ClearCooldown 手动清除冷却
func (s *PHScheduler) ClearCooldown() {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	s.cooldownUntil = time.Time{}
	log.Printf("[phscheduler] PH 冷却已手动清除")
}
