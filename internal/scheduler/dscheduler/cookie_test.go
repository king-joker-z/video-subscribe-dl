package dscheduler

import (
	"testing"
)

// TestLoadUserCookie_NoCookieConfigured 未配置 Cookie 时不报错
func TestLoadUserCookie_NoCookieConfigured(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	// 不配置 douyin_cookie，应正常返回（使用自动生成模式）
	s.LoadUserCookie()
}

// TestLoadUserCookie_WithCookie 配置了 Cookie 时正常加载
func TestLoadUserCookie_WithCookie(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	s.db.SetSetting("douyin_cookie", "test_cookie_value_123")
	// 不应 panic
	s.LoadUserCookie()
}

// TestLoadUserCookie_EmptyAfterTrim 纯空白 Cookie 当作未配置处理
func TestLoadUserCookie_EmptyAfterTrim(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	s.db.SetSetting("douyin_cookie", "   ")
	s.LoadUserCookie()
}

// TestRefreshCookie 热更新不报错
func TestRefreshCookie(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestDouyinScheduler(t, mock)

	s.RefreshCookie("new_cookie")
}
