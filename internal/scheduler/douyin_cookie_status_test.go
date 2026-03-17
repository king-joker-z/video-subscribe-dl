package scheduler

import (
	"testing"
)

// TestDouyinCookieStatus_InitiallyValid 状态初始应为 valid
func TestDouyinCookieStatus_InitiallyValid(t *testing.T) {
	// 重置全局状态
	resetDouyinCookieStatus()

	valid, msg := GetDouyinCookieStatus()
	if !valid {
		t.Errorf("initial state should be valid, got invalid with msg: %s", msg)
	}
	if msg != "" {
		t.Errorf("initial msg should be empty, got: %s", msg)
	}
}

// TestDouyinCookieStatus_MarkInvalid 标记失败后应返回 invalid
func TestDouyinCookieStatus_MarkInvalid(t *testing.T) {
	resetDouyinCookieStatus()

	SetDouyinCookieInvalid("Cookie 已过期，请更新")

	valid, msg := GetDouyinCookieStatus()
	if valid {
		t.Error("after SetDouyinCookieInvalid, state should be invalid")
	}
	if msg != "Cookie 已过期，请更新" {
		t.Errorf("expected msg 'Cookie 已过期，请更新', got: %s", msg)
	}
}

// TestDouyinCookieStatus_MarkValidAgain 标记成功后恢复 valid
func TestDouyinCookieStatus_MarkValidAgain(t *testing.T) {
	resetDouyinCookieStatus()

	SetDouyinCookieInvalid("some error")
	SetDouyinCookieValid()

	valid, msg := GetDouyinCookieStatus()
	if !valid {
		t.Error("after SetDouyinCookieValid, state should be valid")
	}
	if msg != "" {
		t.Errorf("after SetDouyinCookieValid, msg should be empty, got: %s", msg)
	}
}

// TestDouyinCookieStatus_Concurrent 并发读写安全
func TestDouyinCookieStatus_Concurrent(t *testing.T) {
	resetDouyinCookieStatus()

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(i int) {
			if i%2 == 0 {
				SetDouyinCookieInvalid("error")
			} else {
				SetDouyinCookieValid()
			}
			GetDouyinCookieStatus()
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// 只要不 race/panic 即可
}
