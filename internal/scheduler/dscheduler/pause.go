package dscheduler

import (
	"log"
	"time"
)

// Pause 暂停抖音下载（风控触发后使用）
func (s *DouyinScheduler) Pause(reason string) {
	s.pausedMu.Lock()
	defer s.pausedMu.Unlock()
	if s.paused {
		return // 幂等
	}
	s.paused = true
	s.pauseReason = reason
	s.pausedAt = time.Now()
	log.Printf("[dscheduler] 抖音已暂停: %s", reason)
}

// Resume 恢复抖音下载
func (s *DouyinScheduler) Resume() {
	s.pausedMu.Lock()
	defer s.pausedMu.Unlock()
	s.paused = false
	s.pauseReason = ""
	s.pausedAt = time.Time{}
	log.Printf("[dscheduler] 抖音已恢复")
}

// GetPauseStatus 获取暂停状态（供 API 使用）
func (s *DouyinScheduler) GetPauseStatus() (paused bool, reason string, pausedAt time.Time) {
	s.pausedMu.RLock()
	defer s.pausedMu.RUnlock()
	return s.paused, s.pauseReason, s.pausedAt
}
