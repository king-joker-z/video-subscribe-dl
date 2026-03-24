package pornhub

import (
	"math"
	"math/rand"
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

// AcquireWithBackoff 带指数退避的 token 获取
// consecutiveErrors: 连续错误次数（0 = 无退避，等同于 Acquire）
// 退避策略: 首次 5s, 第二次 10s, 第三次 20s, 最大 60s，±20% 随机抖动
// 返回 false 表示 limiter 已停止
func (rl *RateLimiter) AcquireWithBackoff(consecutiveErrors int) bool {
	if consecutiveErrors > 0 {
		baseDelay := 5.0 * math.Pow(2.0, float64(consecutiveErrors-1))
		if baseDelay > 60.0 {
			baseDelay = 60.0
		}
		jitter := baseDelay * 0.2 * (2*rand.Float64() - 1)
		delay := time.Duration((baseDelay + jitter) * float64(time.Second))
		select {
		case <-rl.stopCh:
			return false
		case <-time.After(delay):
		}
	}
	return rl.Acquire()
}

// ReportResult 报告 HTTP 请求结果，用于状态码感知限流
// 429/503: 标记为限流响应，下次 acquire 额外等待 30s
// 200: 正常，清除 penalty
func (rl *RateLimiter) ReportResult(statusCode int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	switch statusCode {
	case 429, 503:
		// 收到限流响应，设置 penalty 等待 30s
		rl.penaltyUntil = time.Now().Add(30 * time.Second)
	case 200:
		// 正常响应，清除 penalty
		rl.penaltyUntil = time.Time{}
	}
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
