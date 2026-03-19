package dscheduler

import (
	"testing"
	"time"
)

// ============================
// Pause / Resume 测试
// ============================

func TestPause_SetsState(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	if s.IsPaused() {
		t.Error("expected not paused initially")
	}

	s.Pause("test reason")

	if !s.IsPaused() {
		t.Error("expected paused after Pause")
	}

	paused, reason, pausedAt := s.GetPauseStatus()
	if !paused {
		t.Error("expected paused=true")
	}
	if reason != "test reason" {
		t.Errorf("expected reason 'test reason', got %q", reason)
	}
	if pausedAt.IsZero() {
		t.Error("expected non-zero pausedAt")
	}
}

func TestPause_Idempotent(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	s.Pause("first reason")
	_, firstReason, firstPausedAt := s.GetPauseStatus()

	time.Sleep(10 * time.Millisecond)
	s.Pause("second reason")

	_, reason, pausedAt := s.GetPauseStatus()
	if reason != firstReason {
		t.Errorf("expected reason unchanged on second pause, got %q", reason)
	}
	if !pausedAt.Equal(firstPausedAt) {
		t.Error("expected pausedAt unchanged on second pause")
	}
}

func TestResume_ClearsState(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	s.Pause("test reason")
	s.Resume()

	if s.IsPaused() {
		t.Error("expected not paused after resume")
	}

	paused, reason, pausedAt := s.GetPauseStatus()
	if paused {
		t.Error("expected paused=false")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
	if !pausedAt.IsZero() {
		t.Error("expected zero pausedAt after resume")
	}
}

func TestResume_NoopWhenNotPaused(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	s.Resume() // 不应 panic
	if s.IsPaused() {
		t.Error("expected not paused")
	}
}

// ============================
// 冷却 测试
// ============================

func TestIsInCooldown_NoCooldown(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	if s.IsInCooldown() {
		t.Error("expected no cooldown initially")
	}
}

func TestTriggerCooldown_SetsInCooldown(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	s.TriggerCooldown()

	if !s.IsInCooldown() {
		t.Error("expected in cooldown after trigger")
	}

	inCooldown, remaining := s.GetCooldownInfo()
	if !inCooldown {
		t.Error("GetCooldownInfo: expected inCooldown=true")
	}
	if remaining == "" {
		t.Error("expected non-empty remaining duration string")
	}
}

func TestGetCooldownInfo_ExpiredCooldown(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	// 直接设置一个已过期的冷却时间
	s.cooldownMu.Lock()
	s.cooldownUntil = time.Now().Add(-1 * time.Minute)
	s.cooldownMu.Unlock()

	if s.IsInCooldown() {
		t.Error("expected cooldown expired")
	}

	inCooldown, remaining := s.GetCooldownInfo()
	if inCooldown {
		t.Error("GetCooldownInfo: expected inCooldown=false for expired cooldown")
	}
	if remaining != "" {
		t.Errorf("expected empty remaining for expired cooldown, got %q", remaining)
	}
}

// ============================
// Cookie 状态测试
// ============================

func TestCookieStatus_DefaultValid(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	st := s.GetDouyinCookieStatus()
	if !st.Valid {
		t.Error("expected cookie valid by default")
	}
}

func TestCookieStatus_SetInvalid(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	s.SetCookieInvalid("Cookie 已过期")

	st := s.GetDouyinCookieStatus()
	if st.Valid {
		t.Error("expected cookie invalid after SetCookieInvalid")
	}
	if st.Msg != "Cookie 已过期" {
		t.Errorf("expected msg 'Cookie 已过期', got %q", st.Msg)
	}
}

func TestCookieStatus_SetValidAfterInvalid(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	s.SetCookieInvalid("some error")
	s.SetCookieValid()

	st := s.GetDouyinCookieStatus()
	if !st.Valid {
		t.Error("expected cookie valid after SetCookieValid")
	}
	if st.Msg != "" {
		t.Errorf("expected empty msg after SetCookieValid, got %q", st.Msg)
	}
}
