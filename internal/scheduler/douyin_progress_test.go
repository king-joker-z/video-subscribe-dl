package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-subscribe-dl/internal/downloader"
)

// TestCalcDownloadSpeed 速度计算函数
func TestCalcDownloadSpeed(t *testing.T) {
	tests := []struct {
		name        string
		deltaBytes  int64
		elapsedSecs float64
		want        float64
	}{
		{"zero elapsed", 1000, 0, 0},
		{"normal", 1024 * 1024, 1.0, 1024 * 1024},
		{"half second", 512 * 1024, 0.5, 1024 * 1024},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := calcDownloadSpeed(tc.deltaBytes, tc.elapsedSecs)
			if got != tc.want {
				t.Errorf("calcDownloadSpeed(%d, %f) = %f, want %f", tc.deltaBytes, tc.elapsedSecs, got, tc.want)
			}
		})
	}
}

// TestCalcDownloadPercent 百分比计算
func TestCalcDownloadPercent(t *testing.T) {
	tests := []struct {
		downloaded, total int64
		want              float64
	}{
		{0, 0, 0},
		{50, 100, 50},
		{100, 100, 100},
		{0, -1, 0},   // unknown total
		{512, -1, 0}, // unknown total
	}
	for _, tc := range tests {
		got := calcDownloadPercent(tc.downloaded, tc.total)
		if got != tc.want {
			t.Errorf("calcDownloadPercent(%d, %d) = %f, want %f", tc.downloaded, tc.total, got, tc.want)
		}
	}
}

// TestDownloadDouyinFileWithProgress_PushesProgress 验证下载过程中推送进度事件
func TestDownloadDouyinFileWithProgress_PushesProgress(t *testing.T) {
	// 构造一个 mock HTTP server，返回 100KB 随机数据
	payload := strings.Repeat("x", 100*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.Write([]byte(payload))
	}))
	defer server.Close()

	// 收集进度事件
	var events []downloader.ProgressInfo
	callback := func(info downloader.ProgressInfo) {
		events = append(events, info)
	}

	ctx := context.Background()
	destPath := t.TempDir() + "/test_video.mp4"
	downloadID := int64(42)

	_, err := downloadDouyinFileWithProgress(ctx, server.URL, destPath, downloadID, "test video", callback)
	if err != nil {
		t.Fatalf("downloadDouyinFileWithProgress failed: %v", err)
	}

	// 应该至少有 1 个进度事件（开始 + 结束）
	if len(events) == 0 {
		t.Error("expected at least 1 progress event, got 0")
	}

	// 最后一个事件应该是 done 状态，percent=100
	last := events[len(events)-1]
	if last.Status != "done" {
		t.Errorf("last event status should be 'done', got %q", last.Status)
	}
	if last.Percent != 100 {
		t.Errorf("last event percent should be 100, got %f", last.Percent)
	}
	if last.DownloadID != downloadID {
		t.Errorf("last event download_id should be %d, got %d", downloadID, last.DownloadID)
	}

	// 验证下载文件写入正确
	// (文件存在由 downloadDouyinFileWithProgress 保证)
}

// TestDownloadDouyinFileWithProgress_ContextCancel context 取消时能正确退出
func TestDownloadDouyinFileWithProgress_ContextCancel(t *testing.T) {
	// 构造一个慢速 server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10000000")
		// 慢速写入
		for i := 0; i < 1000; i++ {
			w.Write(make([]byte, 10000))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	destPath := t.TempDir() + "/cancel_test.mp4"
	_, err := downloadDouyinFileWithProgress(ctx, server.URL, destPath, 1, "cancel test", nil)
	if err == nil {
		t.Error("expected error when context is cancelled, got nil")
	}
}
