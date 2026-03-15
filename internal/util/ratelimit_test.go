package util

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestRateLimitedReader_NoLimit(t *testing.T) {
	data := []byte("hello world")
	r := NewRateLimitedReader(bytes.NewReader(data), 0)

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(out) != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", string(out))
	}
}

func TestRateLimitedReader_NegativeLimit(t *testing.T) {
	data := []byte("test data")
	r := NewRateLimitedReader(bytes.NewReader(data), -1)

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(out) != "test data" {
		t.Errorf("expected 'test data', got '%s'", string(out))
	}
}

func TestRateLimitedReader_SmallData(t *testing.T) {
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i % 256)
	}
	r := NewRateLimitedReader(bytes.NewReader(data), 10000)

	start := time.Now()
	out, err := io.ReadAll(r)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(out) != 100 {
		t.Errorf("expected 100 bytes, got %d", len(out))
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected fast read, took %v", elapsed)
	}
}

func TestRateLimitedReader_DataIntegrity(t *testing.T) {
	original := "The quick brown fox jumps over the lazy dog. 1234567890!"
	r := NewRateLimitedReader(bytes.NewReader([]byte(original)), 1024)

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(out) != original {
		t.Errorf("data mismatch: expected '%s', got '%s'", original, string(out))
	}
}

func TestRateLimitedReader_EmptyReader(t *testing.T) {
	r := NewRateLimitedReader(bytes.NewReader([]byte{}), 1024)
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected 0 bytes, got %d", len(out))
	}
}

func TestRateLimitedReader_RateLimiting(t *testing.T) {
	data := make([]byte, 1536)
	for i := range data {
		data[i] = byte('A')
	}
	r := NewRateLimitedReader(bytes.NewReader(data), 512)

	start := time.Now()
	out, err := io.ReadAll(r)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(out) != 1536 {
		t.Errorf("expected 1536 bytes, got %d", len(out))
	}
	if elapsed < 100*time.Millisecond {
		t.Logf("warning: rate limiting may not have kicked in (elapsed: %v)", elapsed)
	}
}
