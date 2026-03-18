package dscheduler

import "sync"

// douyinCookieState 保存抖音 Cookie 的全局有效性状态（线程安全）
// 默认 valid=true（未检测时视为有效，避免误报）
var douyinCookieState = struct {
	mu    sync.RWMutex
	valid bool
	msg   string
}{valid: true}

// GetDouyinCookieStatus 返回抖音 Cookie 的当前有效性状态
// 返回 (valid bool, msg string)：valid=true 表示有效，false+msg 表示失效原因
func GetDouyinCookieStatus() (bool, string) {
	douyinCookieState.mu.RLock()
	defer douyinCookieState.mu.RUnlock()
	return douyinCookieState.valid, douyinCookieState.msg
}

// SetDouyinCookieInvalid 标记抖音 Cookie 已失效，并记录原因
func SetDouyinCookieInvalid(reason string) {
	douyinCookieState.mu.Lock()
	defer douyinCookieState.mu.Unlock()
	douyinCookieState.valid = false
	douyinCookieState.msg = reason
}

// SetDouyinCookieValid 标记抖音 Cookie 验证通过，重置状态
func SetDouyinCookieValid() {
	douyinCookieState.mu.Lock()
	defer douyinCookieState.mu.Unlock()
	douyinCookieState.valid = true
	douyinCookieState.msg = ""
}

// resetDouyinCookieStatus 重置为初始状态（仅测试使用）
func resetDouyinCookieStatus() {
	douyinCookieState.mu.Lock()
	defer douyinCookieState.mu.Unlock()
	douyinCookieState.valid = true
	douyinCookieState.msg = ""
}
