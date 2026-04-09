package xchina

import (
	"sync"
	"time"
)

// RateLimiter 简单令牌桶限流器（复用 pornhub 相同设计）
type RateLimiter struct {
	mu       sync.Mutex
	tokens   int
	max      int
	refill   int
	interval time.Duration
	ticker   *time.Ticker
	stop     chan struct{}
}

// NewRateLimiter 创建限流器：max 个并发，每 interval 补充 refill 个令牌
func NewRateLimiter(max, refill int, interval time.Duration) *RateLimiter {
	rl := &RateLimiter{
		tokens:   max,
		max:      max,
		refill:   refill,
		interval: interval,
		ticker:   time.NewTicker(interval),
		stop:     make(chan struct{}),
	}
	go rl.refillLoop()
	return rl
}

func (rl *RateLimiter) refillLoop() {
	for {
		select {
		case <-rl.ticker.C:
			rl.mu.Lock()
			rl.tokens += rl.refill
			if rl.tokens > rl.max {
				rl.tokens = rl.max
			}
			rl.mu.Unlock()
		case <-rl.stop:
			return
		}
	}
}

// Acquire 获取一个令牌，返回 false 表示限流器已停止
func (rl *RateLimiter) Acquire() bool {
	for {
		select {
		case <-rl.stop:
			return false
		default:
		}
		rl.mu.Lock()
		if rl.tokens > 0 {
			rl.tokens--
			rl.mu.Unlock()
			return true
		}
		rl.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
	}
}

// Stop 停止限流器
func (rl *RateLimiter) Stop() {
	rl.ticker.Stop()
	select {
	case <-rl.stop:
	default:
		close(rl.stop)
	}
}
