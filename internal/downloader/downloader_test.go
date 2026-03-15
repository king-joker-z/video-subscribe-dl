package downloader

import (
	"testing"
	"time"
)

func TestCalculateDownloadTimeout(t *testing.T) {
	tests := []struct {
		name           string
		totalBitrate   int64
		rateLimitBps   int64
		wantMin        time.Duration
		wantMax        time.Duration
	}{
		{
			name:         "无限速返回默认1小时",
			totalBitrate: 5_000_000,
			rateLimitBps: 0,
			wantMin:      1 * time.Hour,
			wantMax:      1 * time.Hour,
		},
		{
			name:         "高码率低限速应返回较长超时",
			totalBitrate: 10_000_000, // 10Mbps
			rateLimitBps: 500_000,    // 500KB/s
			wantMin:      30 * time.Minute,
			wantMax:      4 * time.Hour,
		},
		{
			name:         "低码率高限速不低于30分钟",
			totalBitrate: 1_000_000, // 1Mbps
			rateLimitBps: 10_000_000, // 10MB/s
			wantMin:      30 * time.Minute,
			wantMax:      30 * time.Minute,
		},
		{
			name:         "极端低速不超过4小时",
			totalBitrate: 20_000_000, // 20Mbps
			rateLimitBps: 100_000,    // 100KB/s
			wantMin:      4 * time.Hour,
			wantMax:      4 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateDownloadTimeout(tt.totalBitrate, tt.rateLimitBps)
			if got < tt.wantMin {
				t.Errorf("got %v, want >= %v", got, tt.wantMin)
			}
			if got > tt.wantMax {
				t.Errorf("got %v, want <= %v", got, tt.wantMax)
			}
		})
	}
}

func TestJob_Flat(t *testing.T) {
	job := &Job{
		BvID:     "BV123",
		Title:    "test",
		Flat:     true,
		Quality:  "best",
	}
	if !job.Flat {
		t.Error("expected Flat=true")
	}
}

func TestJob_QualityMin(t *testing.T) {
	job := &Job{
		BvID:       "BV123",
		QualityMin: "720p",
	}
	if job.QualityMin != "720p" {
		t.Errorf("expected QualityMin=720p, got %s", job.QualityMin)
	}
}

func TestJob_SkipOptions(t *testing.T) {
	job := &Job{
		SkipNFO:    true,
		SkipPoster: true,
	}
	if !job.SkipNFO || !job.SkipPoster {
		t.Error("expected skip options to be true")
	}
}
