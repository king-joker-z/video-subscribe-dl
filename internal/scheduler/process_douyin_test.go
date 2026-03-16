package scheduler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
)

// ============================
// downloadDouyinFile 测试
// ============================

func TestDownloadDouyinFile_Success(t *testing.T) {
	content := "fake video content for testing"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(content))
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "video.mp4")
	size, err := douyin.DownloadFile(ts.URL+"/video.mp4", destPath)
	if err != nil {
		t.Fatalf("downloadDouyinFile failed: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), size)
	}

	data, _ := os.ReadFile(destPath)
	if string(data) != content {
		t.Errorf("expected content %q, got %q", content, string(data))
	}
}

func TestDownloadDouyinFile_SkipsExistingNonEmpty(t *testing.T) {
	requestMade := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.WriteHeader(200)
		w.Write([]byte("new content"))
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "existing.mp4")
	existingContent := "already downloaded"
	os.WriteFile(destPath, []byte(existingContent), 0644)

	size, err := douyin.DownloadFile(ts.URL+"/video.mp4", destPath)
	if err != nil {
		t.Fatalf("downloadDouyinFile failed: %v", err)
	}
	if size != int64(len(existingContent)) {
		t.Errorf("expected existing size %d, got %d", len(existingContent), size)
	}
	if requestMade {
		t.Error("should not make HTTP request for existing non-empty file")
	}
}

func TestDownloadDouyinFile_Non200Status(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "video.mp4")
	_, err := douyin.DownloadFile(ts.URL+"/video.mp4", destPath)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestDownloadDouyinFile_EmptyBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		// write 0 bytes
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "video.mp4")
	_, err := douyin.DownloadFile(ts.URL+"/video.mp4", destPath)
	if err == nil {
		t.Fatal("expected error for 0 bytes download")
	}
}

func TestDownloadDouyinFile_CreatesMissingDirs(t *testing.T) {
	content := "video data"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(content))
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "nested", "dirs", "video.mp4")
	size, err := douyin.DownloadFile(ts.URL+"/video.mp4", destPath)
	if err != nil {
		t.Fatalf("downloadDouyinFile failed: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), size)
	}
}

func TestDownloadDouyinFile_InvalidURL(t *testing.T) {
	destPath := filepath.Join(t.TempDir(), "video.mp4")
	_, err := douyin.DownloadFile("http://127.0.0.1:1/nonexistent", destPath)
	if err == nil {
		t.Fatal("expected error for invalid URL/unreachable server")
	}
}

func TestDownloadDouyinFile_ConcurrentDifferentFiles(t *testing.T) {
	content := "concurrent test data"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(content))
	}))
	defer ts.Close()

	dir := t.TempDir()
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func(idx int) {
			destPath := filepath.Join(dir, fmt.Sprintf("video_%d.mp4", idx))
			_, err := douyin.DownloadFile(ts.URL+"/video.mp4", destPath)
			errs <- err
		}(i)
	}

	for i := 0; i < 5; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent download %d failed: %v", i, err)
		}
	}
}

func TestDownloadDouyinFile_TmpFileCleanedOnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(200)
		w.Write([]byte("partial"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "video.mp4")
	_, _ = douyin.DownloadFile(ts.URL+"/video.mp4", destPath)

	// tmp file should not remain regardless of outcome
	tmpPath := destPath + ".tmp"
	if _, statErr := os.Stat(tmpPath); statErr == nil {
		t.Error("tmp file should be cleaned up")
	}
}

// ============================
// downloadDouyinThumb 测试
// ============================

func TestDownloadDouyinThumb_Success(t *testing.T) {
	thumbData := "fake jpeg data"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(thumbData))
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "thumb.jpg")
	err := douyin.DownloadThumb(ts.URL+"/thumb.jpg", destPath)
	if err != nil {
		t.Fatalf("downloadDouyinThumb failed: %v", err)
	}

	data, _ := os.ReadFile(destPath)
	if string(data) != thumbData {
		t.Errorf("expected %q, got %q", thumbData, string(data))
	}
}

func TestDownloadDouyinThumb_SkipsExisting(t *testing.T) {
	requestMade := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.WriteHeader(200)
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "thumb.jpg")
	os.WriteFile(destPath, []byte("existing"), 0644)

	err := douyin.DownloadThumb(ts.URL+"/thumb.jpg", destPath)
	if err != nil {
		t.Fatalf("downloadDouyinThumb failed: %v", err)
	}
	if requestMade {
		t.Error("should not make HTTP request for existing thumb")
	}
}

func TestDownloadDouyinThumb_Non200Status(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "thumb.jpg")
	err := douyin.DownloadThumb(ts.URL+"/thumb.jpg", destPath)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestDownloadDouyinThumb_InvalidURL(t *testing.T) {
	destPath := filepath.Join(t.TempDir(), "thumb.jpg")
	err := douyin.DownloadThumb("http://127.0.0.1:1/nonexistent", destPath)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestDownloadDouyinThumb_LargeImage(t *testing.T) {
	largeData := make([]byte, 1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write(largeData)
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "large_thumb.jpg")
	err := douyin.DownloadThumb(ts.URL+"/thumb.jpg", destPath)
	if err != nil {
		t.Fatalf("downloadDouyinThumb failed for large image: %v", err)
	}

	info, _ := os.Stat(destPath)
	if info.Size() != int64(len(largeData)) {
		t.Errorf("expected size %d, got %d", len(largeData), info.Size())
	}
}

// ============================
// retryOneDouyinDownload 测试
// ============================

func TestRetryOneDouyinDownload_SkipsWhenPaused(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)
	s.douyinPaused = true

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "test_vid_001",
		Title:    "Test Video",
		Status:   "failed",
	})

	dl := db.Download{
		ID:       dlID,
		SourceID: src.ID,
		VideoID:  "test_vid_001",
		Title:    "Test Video",
		Status:   "failed",
	}

	s.retryOneDouyinDownload(dl)

	// Should not make any API calls when paused
	if mock.getCallCount() > 0 {
		t.Errorf("expected 0 API calls when paused, got %d", mock.getCallCount())
	}
}

func TestRetryOneDouyinDownload_SourceNotFound(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	dl := db.Download{
		ID:       999,
		SourceID: 999,
		VideoID:  "test_vid",
		Title:    "Test",
		Status:   "failed",
	}

	// Should not panic
	s.retryOneDouyinDownload(dl)
}
