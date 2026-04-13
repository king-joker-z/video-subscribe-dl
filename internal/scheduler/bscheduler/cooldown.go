package bscheduler

import (
	"log"
	"time"

	"video-subscribe-dl/internal/notify"
)

// TriggerCooldown 检测到 B站风控时记录日志并发送通知
func (s *BiliScheduler) TriggerCooldown() {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	log.Printf("[bscheduler][WARN] 触发B站风控")

	if time.Since(s.lastCooldownNotify) > 30*time.Minute {
		s.lastCooldownNotify = time.Now()
		s.notifier.Send(notify.EventRateLimited, "B站风控触发", "检测到风控")
	}
}
