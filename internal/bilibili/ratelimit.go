package bilibili

import (
	"sync"
	"time"
)

// RateLimiter 令牌桶限流器
// 参考 bili-sync 的 leaky_bucket::RateLimiter：每个 interval 补充 refill 个 token
type RateLimiter struct {
	mu       sync.Mutex
	tokens   int
	max      int
	refill   int
	interval time.Duration
	stopCh   chan struct{}

	// 幂等 Stop 保护
	stopped sync.Once
}

// NewRateLimiter 创建令牌桶限流器
// max: 桶最大容量（也是初始 token 数）
// refill: 每次补充的 token 数
// interval: 补充间隔
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

// DefaultRateLimiter 创建默认限流器: 每 2s 补充 1 个 token，桶容量 1
// B 站风控严格，保守限流
func DefaultRateLimiter() *RateLimiter {
	return NewRateLimiter(1, 1, 2*time.Second)
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
		// 等待一小段时间后再尝试
		time.Sleep(5 * time.Millisecond)
	}
}

// TryAcquire 尝试获取一个 token，不阻塞
func (rl *RateLimiter) TryAcquire() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.tokens > 0 {
		rl.tokens--
		return true
	}
	return false
}

// Stop 停止限流器的补充协程（幂等：重复调用不 panic）
func (rl *RateLimiter) Stop() {
	rl.stopped.Do(func() {
		close(rl.stopCh)
	})
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
