package douyin

import (
	"sync"
	"time"
)

// RateLimiter 令牌桶限流器
// 抖音风控比 B站更严格，默认 3s/req（宁慢勿快）
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

// DefaultRateLimiter 抖音默认限流: 桶容量 1, 每 3s 补充 1 个 token
func DefaultRateLimiter() *RateLimiter {
	return NewRateLimiter(1, 1, 3*time.Second)
}

// Acquire 获取一个 token，阻塞直到成功
func (rl *RateLimiter) Acquire() {
	for {
		rl.mu.Lock()
		if rl.tokens > 0 {
			rl.tokens--
			rl.mu.Unlock()
			return
		}
		rl.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
}

// Stop 停止补充循环
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
