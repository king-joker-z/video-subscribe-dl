package dscheduler

import (
	"log"
	"time"
)

const douyinCooldownDuration = 10 * time.Minute

// TriggerCooldown 触发抖音冷却
func (s *DouyinScheduler) TriggerCooldown() {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	s.cooldownUntil = time.Now().Add(douyinCooldownDuration)
	log.Printf("[dscheduler] 触发抖音冷却，恢复时间: %s", s.cooldownUntil.Format("15:04:05"))
}

// IsInCooldown 检查是否在冷却期内
func (s *DouyinScheduler) IsInCooldown() bool {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	return time.Now().Before(s.cooldownUntil)
}

// GetCooldownInfo 返回冷却状态（供 API 使用）
func (s *DouyinScheduler) GetCooldownInfo() (inCooldown bool, remaining string) {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	if time.Now().Before(s.cooldownUntil) {
		return true, time.Until(s.cooldownUntil).String()
	}
	return false, ""
}
