package douyin

import (
	"testing"
	"time"
)

// TestAcquire 测试基本限流
func TestAcquire(t *testing.T) {
	rl := NewRateLimiter(2, 1, 100*time.Millisecond)
	defer rl.Stop()

	// 桶容量 2，应该能立即获取 2 个 token
	start := time.Now()
	rl.Acquire()
	rl.Acquire()
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("acquiring 2 tokens from capacity-2 bucket took %v, expected near-instant", elapsed)
	}

	// 第 3 个应该需要等待补充（~100ms）
	start = time.Now()
	rl.Acquire()
	elapsed = time.Since(start)

	if elapsed < 50*time.Millisecond {
		t.Errorf("acquiring 3rd token took %v, expected >= 50ms wait", elapsed)
	}
}

// TestAcquireWithBackoff 测试指数退避
func TestAcquireWithBackoff(t *testing.T) {
	rl := NewRateLimiter(10, 10, 100*time.Millisecond) // 高容量，不受令牌桶限制
	defer rl.Stop()

	// consecutiveErrors=0 不应有退避
	start := time.Now()
	rl.AcquireWithBackoff(0)
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Errorf("AcquireWithBackoff(0) took %v, expected near-instant", elapsed)
	}

	// consecutiveErrors=1 应退避约 3s（±20%）
	// 为了快速测试，只验证退避大于 0
	start = time.Now()
	rl.AcquireWithBackoff(1)
	elapsed = time.Since(start)
	if elapsed < 2*time.Second {
		t.Errorf("AcquireWithBackoff(1) took %v, expected >= 2s", elapsed)
	}
	if elapsed > 4*time.Second {
		t.Errorf("AcquireWithBackoff(1) took %v, expected <= 4s", elapsed)
	}
}

// TestReportResult 测试状态码感知限流
func TestReportResult(t *testing.T) {
	rl := NewRateLimiter(10, 10, 100*time.Millisecond)
	defer rl.Stop()

	// 200: 清除 penalty
	rl.ReportResult(200)
	if !rl.penaltyUntil.IsZero() {
		t.Error("ReportResult(200) should clear penalty")
	}

	// 429: 设置 penalty
	rl.ReportResult(429)
	if rl.penaltyUntil.IsZero() {
		t.Error("ReportResult(429) should set penalty")
	}
	if time.Until(rl.penaltyUntil) < 8*time.Second {
		t.Error("penalty should be ~10s in the future")
	}

	// 200: 清除 penalty
	rl.ReportResult(200)
	if !rl.penaltyUntil.IsZero() {
		t.Error("ReportResult(200) after 429 should clear penalty")
	}

	// 403: 设置 penalty
	rl.ReportResult(403)
	if rl.penaltyUntil.IsZero() {
		t.Error("ReportResult(403) should set penalty")
	}

	// 503: 设置 penalty
	rl.ReportResult(200)
	rl.ReportResult(503)
	if rl.penaltyUntil.IsZero() {
		t.Error("ReportResult(503) should set penalty")
	}
}

// TestReportResult_PenaltyBlocking 测试 penalty 期间 Acquire 阻塞
func TestReportResult_PenaltyBlocking(t *testing.T) {
	rl := NewRateLimiter(10, 10, 100*time.Millisecond)
	defer rl.Stop()

	// 设置一个短 penalty（手动覆盖为 500ms）
	rl.mu.Lock()
	rl.penaltyUntil = time.Now().Add(500 * time.Millisecond)
	rl.mu.Unlock()

	start := time.Now()
	rl.Acquire()
	elapsed := time.Since(start)

	if elapsed < 400*time.Millisecond {
		t.Errorf("Acquire during penalty took %v, expected >= 400ms", elapsed)
	}
}

// TestDefaultRateLimiter 测试默认限流器参数
func TestDefaultRateLimiter(t *testing.T) {
	rl := DefaultRateLimiter()
	defer rl.Stop()

	if rl.max != 1 {
		t.Errorf("DefaultRateLimiter max = %d, want 1", rl.max)
	}
	if rl.interval != 3*time.Second {
		t.Errorf("DefaultRateLimiter interval = %v, want 3s", rl.interval)
	}
}

// TestRateLimiterDoubleStop 测试 Stop() 幂等性：重复调用不 panic
func TestRateLimiterDoubleStop(t *testing.T) {
	rl := NewRateLimiter(1, 1, time.Second)
	rl.Stop() // 第一次
	rl.Stop() // 第二次不应 panic
}
