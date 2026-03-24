package phscheduler

import (
	"log"
	"time"
)

// Pause 暂停 PH 下载（风控触发后使用）
func (s *PHScheduler) Pause(reason string) {
	s.pausedMu.Lock()
	defer s.pausedMu.Unlock()
	if s.paused {
		return // 幂等
	}
	s.paused = true
	s.pauseReason = reason
	s.pausedAt = time.Now()
	log.Printf("[phscheduler] PH 已暂停: %s", reason)
}

// Resume 恢复 PH 下载
func (s *PHScheduler) Resume() {
	s.pausedMu.Lock()
	defer s.pausedMu.Unlock()
	s.paused = false
	s.pauseReason = ""
	s.pausedAt = time.Time{}
	log.Printf("[phscheduler] PH 已恢复")
}

// GetPauseStatus 获取暂停状态（供 API 使用）
func (s *PHScheduler) GetPauseStatus() (paused bool, reason string, pausedAt time.Time) {
	s.pausedMu.RLock()
	defer s.pausedMu.RUnlock()
	return s.paused, s.pauseReason, s.pausedAt
}
