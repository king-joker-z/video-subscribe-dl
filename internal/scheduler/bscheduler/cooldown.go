package bscheduler

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/notify"
)

// ─── Per-Source 风控冷却 ───────────────────────────────────────────────────────
// 用 settings 表存储每个 source 的冷却截止时间，key = "source_cooldown_{id}"
// 场景：新 UP 首次全量扫描触发风控时，对该 source 单独设置冷却，
// 冷却期间跳过该 source，到期后重新尝试全量扫描（历史数据不丢失）

const sourceCooldownDuration = 30 * time.Minute

// setSourceCooldown 为指定 source 设置风控冷却
func (s *BiliScheduler) setSourceCooldown(srcID int64) {
	until := time.Now().Add(sourceCooldownDuration)
	key := fmt.Sprintf("source_cooldown_%d", srcID)
	if err := s.db.SetSetting(key, strconv.FormatInt(until.Unix(), 10)); err != nil {
		log.Printf("[bscheduler] setSourceCooldown(%d) failed: %v", srcID, err)
		return
	}
	log.Printf("[bscheduler] source %d 触发风控，冷却 %.0f 分钟后重试", srcID, sourceCooldownDuration.Minutes())
}

// isSourceInCooldown 检查指定 source 是否在冷却期内
func (s *BiliScheduler) isSourceInCooldown(srcID int64) bool {
	key := fmt.Sprintf("source_cooldown_%d", srcID)
	val, err := s.db.GetSetting(key)
	if err != nil || val == "" {
		return false
	}
	ts, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < ts
}

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
