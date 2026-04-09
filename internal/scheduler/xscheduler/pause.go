package xscheduler

import (
	"log"
	"time"
)

// Pause 暂停 xchina 下载
func (s *XScheduler) Pause(reason string) {
	s.pausedMu.Lock()
	defer s.pausedMu.Unlock()
	if s.paused {
		return
	}
	s.paused = true
	s.pauseReason = reason
	s.pausedAt = time.Now()
	log.Printf("[xscheduler] 已暂停: %s", reason)
}

// Resume 恢复 xchina 下载
func (s *XScheduler) Resume() {
	s.pausedMu.Lock()
	defer s.pausedMu.Unlock()
	s.paused = false
	s.pauseReason = ""
	s.pausedAt = time.Time{}
	log.Printf("[xscheduler] 已恢复")
}

// GetPauseStatus 获取暂停状态
func (s *XScheduler) GetPauseStatus() (paused bool, reason string, pausedAt time.Time) {
	s.pausedMu.RLock()
	defer s.pausedMu.RUnlock()
	return s.paused, s.pauseReason, s.pausedAt
}
