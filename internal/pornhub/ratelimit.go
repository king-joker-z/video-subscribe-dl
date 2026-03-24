package pornhub

import (
	"sync"
	"time"
)

// RateLimiter 令牌桶限流器
// Pornhub 反爬相对宽松，默认 5s/req（宁慢勿快）
type RateLimiter struct {
	mu       sync.Mutex
	tokens   int
	max      int
	refill   int
	interval time.Duration
	stopCh   chan struct{}

	// HTTP 状态码感知：上次是否收到限流响应
	penaltyUntil time.Time

	// 幂等 Stop 保护
	stopped sync.Once
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

// DefaultRateLimiter Pornhub 默认限流: 桶容量 1, 每 5s 补充 1 个 token
func DefaultRateLimiter() *RateLimiter {
	return NewRateLimiter(1, 1, 5*time.Second)
}

// Acquire 获取一个 token，阻塞直到成功或 limiter 被 Stop
// 返回 false 表示 limiter 已停止（调用方应放弃操作）
func (rl *RateLimiter) Acquire() bool {
	for {
		rl.mu.Lock()
		// 检查是否已 Stop
		select {
		case <-rl.stopCh:
			rl.mu.Unlock()
			return false
		default:
		}
		// 检查是否在 penalty 期间
		if !rl.penaltyUntil.IsZero() && time.Now().Before(rl.penaltyUntil) {
			remaining := time.Until(rl.penaltyUntil)
			rl.mu.Unlock()
			select {
			case <-rl.stopCh:
				return false
			case <-time.After(remaining):
			}
			continue
		}
		if rl.tokens > 0 {
			rl.tokens--
			rl.mu.Unlock()
			return true
		}
		rl.mu.Unlock()
		select {
		case <-rl.stopCh:
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ReportRateLimit 收到 429/503 时调用，触发 penalty 等待 30s
// 由调用方在检测到限流响应后主动上报
func (rl *RateLimiter) ReportRateLimit() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.penaltyUntil = time.Now().Add(30 * time.Second)
}

// Stop 停止补充循环（幂等：重复调用不 panic）
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
