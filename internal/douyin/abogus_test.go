package douyin

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestABogusPoolInit(t *testing.T) {
	resetABogusPool()
	defer resetABogusPool()

	pool, err := getABogusPool()
	if err != nil {
		t.Fatalf("failed to init a_bogus pool: %v", err)
	}
	if pool == nil {
		t.Fatal("pool is nil")
	}

	created, recycled := pool.stats()
	if created != int64(abogusPoolSize) {
		t.Errorf("expected %d created VMs, got %d", abogusPoolSize, created)
	}
	if recycled != 0 {
		t.Errorf("expected 0 recycled VMs, got %d", recycled)
	}
}

func TestABogusSign(t *testing.T) {
	resetABogusPool()
	defer resetABogusPool()

	pool, err := getABogusPool()
	if err != nil {
		t.Fatalf("failed to init pool: %v", err)
	}

	queryStr := "device_platform=webapp&aid=6383&channel=channel_pc_web&cookie_enabled=true&platform=PC&downlink=10"
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	result, err := pool.sign(queryStr, ua)
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	if result == "" {
		t.Fatal("sign result is empty")
	}

	// a_bogus 签名通常是 base64 编码，长度大约在 100-200 字符
	if len(result) < 20 {
		t.Errorf("sign result too short: %q (len=%d)", result, len(result))
	}

	t.Logf("a_bogus result: %s (len=%d)", result, len(result))

	// 确保每次签名结果不同（包含随机成分）
	result2, err := pool.sign(queryStr, ua)
	if err != nil {
		t.Fatalf("second sign failed: %v", err)
	}
	if result == result2 {
		t.Error("two consecutive signs produced identical results — expected randomness")
	}
	t.Logf("a_bogus result2: %s (len=%d)", result2, len(result2))
}

func TestABogusPoolConcurrent(t *testing.T) {
	resetABogusPool()
	defer resetABogusPool()

	pool, err := getABogusPool()
	if err != nil {
		t.Fatalf("failed to init pool: %v", err)
	}

	queryStr := "sec_user_id=test123&max_cursor=0&count=20&cookie_enabled=true&platform=PC"
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errors := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			result, err := pool.sign(queryStr, ua)
			if err != nil {
				errors <- err
				return
			}
			if result == "" {
				errors <- fmt.Errorf("empty result")
				return
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent sign error: %v", err)
	}
}

func TestSignURLWithABogus(t *testing.T) {
	resetABogusPool()
	resetSignPool()
	defer func() {
		resetABogusPool()
		resetSignPool()
	}()

	queryStr := "device_platform=webapp&aid=6383&cookie_enabled=true&platform=PC"
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	sr := signURLWithABogus(queryStr, ua)

	// a_bogus should be populated
	if sr.ABogus == "" {
		t.Error("expected a_bogus to be non-empty")
	} else {
		t.Logf("a_bogus: %s (len=%d)", sr.ABogus, len(sr.ABogus))
	}

	// X-Bogus should also be populated (fallback chain tries both)
	if sr.XBogus == "" {
		t.Log("X-Bogus is empty (may be expected if sign.js pool init failed)")
	} else {
		t.Logf("X-Bogus: %s (len=%d)", sr.XBogus, len(sr.XBogus))
	}
}

func TestApplySignResult(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		queryStr string
		sr       SignResult
		want     string
	}{
		{
			name:     "both signatures",
			baseURL:  "https://api.example.com/v1/test",
			queryStr: "foo=bar",
			sr:       SignResult{ABogus: "abc123", XBogus: "xyz789"},
			want:     "https://api.example.com/v1/test?foo=bar&a_bogus=abc123&X-Bogus=xyz789",
		},
		{
			name:     "only a_bogus",
			baseURL:  "https://api.example.com/v1/test",
			queryStr: "foo=bar",
			sr:       SignResult{ABogus: "abc123"},
			want:     "https://api.example.com/v1/test?foo=bar&a_bogus=abc123",
		},
		{
			name:     "only X-Bogus",
			baseURL:  "https://api.example.com/v1/test",
			queryStr: "foo=bar",
			sr:       SignResult{XBogus: "xyz789"},
			want:     "https://api.example.com/v1/test?foo=bar&X-Bogus=xyz789",
		},
		{
			name:     "no signatures",
			baseURL:  "https://api.example.com/v1/test",
			queryStr: "foo=bar",
			sr:       SignResult{},
			want:     "https://api.example.com/v1/test?foo=bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applySignResult(tt.baseURL, tt.queryStr, tt.sr)
			if got != tt.want {
				t.Errorf("applySignResult() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEscapeJSString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"it's", "it\\'s"},
		{"back\\slash", "back\\\\slash"},
		{"new\nline", "new\\nline"},
		{"cr\rreturn", "cr\\rreturn"},
		{"normal=param&key=value", "normal=param&key=value"},
	}

	for _, tt := range tests {
		got := escapeJSString(tt.input)
		if got != tt.want {
			t.Errorf("escapeJSString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestABogusPoolTimeout verifies that sign() returns an error containing
// "timed out" within abogusPoolGetTimeout + a 2 s buffer when the pool is
// fully drained (no entry is ever returned).
// [FIXED: P1-3] tests REQ-REL-3 acceptance criterion
func TestABogusPoolTimeout(t *testing.T) {
	resetABogusPool()
	defer resetABogusPool()

	// Create a size=1 pool — easy to drain completely
	pool, err := newABogusPool(1, abogusMaxUses)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	// Drain the pool — take the only entry and never return it
	entry := <-pool.pool
	_ = entry // intentionally held; simulates pool exhaustion

	// sign() must time out and return an error
	start := time.Now()
	_, err = pool.sign("q=1", "ua/test")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from sign(), got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected error to contain 'timed out', got: %v", err)
	}
	// Must complete within abogusPoolGetTimeout (10 s) + 2 s buffer
	const maxWait = 12 * time.Second
	if elapsed > maxWait {
		t.Errorf("sign() took %v; expected ≤ %v", elapsed, maxWait)
	}
}
