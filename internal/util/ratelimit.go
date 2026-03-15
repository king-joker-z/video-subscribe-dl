package util

import (
	"io"
	"time"
)

// RateLimitedReader wraps an io.Reader with bandwidth throttling
type RateLimitedReader struct {
	reader      io.Reader
	bytesPerSec int64
	bucket      int64     // available bytes to read
	lastRefill  time.Time
}

// NewRateLimitedReader creates a rate-limited reader.
// If bytesPerSec <= 0, no rate limiting is applied (passthrough).
func NewRateLimitedReader(reader io.Reader, bytesPerSec int64) io.Reader {
	if bytesPerSec <= 0 {
		return reader // no limit
	}
	return &RateLimitedReader{
		reader:      reader,
		bytesPerSec: bytesPerSec,
		bucket:      bytesPerSec, // start with 1 second of budget
		lastRefill:  time.Now(),
	}
}

func (r *RateLimitedReader) Read(p []byte) (int, error) {
	// Refill bucket based on elapsed time
	now := time.Now()
	elapsed := now.Sub(r.lastRefill).Seconds()
	if elapsed > 0 {
		refill := int64(elapsed * float64(r.bytesPerSec))
		r.bucket += refill
		if r.bucket > r.bytesPerSec*2 {
			r.bucket = r.bytesPerSec * 2 // cap at 2 seconds of burst
		}
		r.lastRefill = now
	}

	// Wait if bucket is empty
	if r.bucket <= 0 {
		waitDuration := time.Duration(float64(time.Second) * float64(-r.bucket+int64(len(p))) / float64(r.bytesPerSec))
		if waitDuration > 100*time.Millisecond {
			waitDuration = 100 * time.Millisecond // sleep in small intervals
		}
		time.Sleep(waitDuration)
		// Refill after sleep
		now = time.Now()
		elapsed = now.Sub(r.lastRefill).Seconds()
		r.bucket += int64(elapsed * float64(r.bytesPerSec))
		if r.bucket > r.bytesPerSec*2 {
			r.bucket = r.bytesPerSec * 2
		}
		r.lastRefill = now
	}

	// Limit read size to bucket
	toRead := len(p)
	if int64(toRead) > r.bucket {
		toRead = int(r.bucket)
	}
	if toRead <= 0 {
		toRead = 1 // always try to read at least 1 byte
	}

	n, err := r.reader.Read(p[:toRead])
	r.bucket -= int64(n)
	return n, err
}
