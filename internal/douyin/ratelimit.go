package douyin

import (
	"sync"
	"time"
)

// RateLimiter 令牌桶限流器（复用 bilibili 的模式）
type RateLimiter struct {
	mu       sync.Mutex
	tokens   int
	max      int
	refill   int
	interval time.Duration
	stopCh   chan struct{}
}

// NewRateLimiter 创建令牌桶限流器
func NewRateLimiter(max, refill int, interval time.Duration) *RateLimiter {
	rl := &RateLimiter{
		tokens:   max,
		max:      max,
		refill:   refill,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
	go rl.refillLoop()
	return rl
}

// DefaultRateLimiter 创建默认限流器: 每 3s 补充 1 个 token，桶容量 1
// 抖音风控比 B站更严格
func DefaultRateLimiter() *RateLimiter {
	return NewRateLimiter(1, 1, 3*time.Second)
}

// Acquire 获取一个 token，阻塞直到获取成功
func (rl *RateLimiter) Acquire() {
	for {
		rl.mu.Lock()
		if rl.tokens > 0 {
			rl.tokens--
			rl.mu.Unlock()
			return
		}
		rl.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
}

// Stop 停止限流器
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

func (rl *RateLimiter) refillLoop() {
	ticker := time.NewTicker(rl.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			rl.tokens += rl.refill
			if rl.tokens > rl.max {
				rl.tokens = rl.max
			}
			rl.mu.Unlock()
		case <-rl.stopCh:
			return
		}
	}
}
