package scheduler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
)

// ============================
// retryOneDouyinDownload 核心路径测试
// ============================

func TestRetryOneDouyinDownload_VideoSuccess(t *testing.T) {
	// 启动 HTTP 服务器模拟视频下载
	videoContent := "fake mp4 video data for testing"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(videoContent))
	}))
	defer ts.Close()

	mock := &mockDouyinAPI{
		videoDetailResult: &douyin.DouyinVideo{
			AwemeID:    "test_vid_success",
			Desc:       "测试视频",
			VideoURL:   ts.URL + "/video.mp4",
			Duration:   30000,
			CreateTime: time.Now().Unix(),
			Author:     douyin.DouyinUser{Nickname: "TestAuthor"},
			Cover:      ts.URL + "/cover.jpg",
		},
		resolveVideoResult: ts.URL + "/video.mp4",
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestAuthor", "https://www.douyin.com/user/MS4wLjABAAAAtest")
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "test_vid_success",
		Title:    "测试视频",
		Uploader: "TestAuthor",
		Status:   "failed",
	})

	dl, _ := s.db.GetDownload(dlID)
	s.retryOneDouyinDownload(*dl)

	// 验证状态更新为 completed
	updated, _ := s.db.GetDownload(dlID)
	if updated.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", updated.Status)
	}
	if updated.FileSize == 0 {
		t.Error("expected non-zero file size")
	}
}

func TestRetryOneDouyinDownload_RiskControl_PausesDouyin(t *testing.T) {
	mock := &mockDouyinAPI{
		videoDetailErr: douyin.ErrDouyinRiskControl,
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "risk_vid",
		Title:    "风控测试",
		Status:   "failed",
	})

	dl, _ := s.db.GetDownload(dlID)
	s.retryOneDouyinDownload(*dl)

	// 验证抖音被暂停
	if !s.IsDouyinPaused() {
		t.Error("expected douyin to be paused after risk control")
	}

	// 验证下载状态为 failed
	updated, _ := s.db.GetDownload(dlID)
	if updated.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", updated.Status)
	}
}

func TestRetryOneDouyinDownload_GetVideoDetailFail_NonRisk(t *testing.T) {
	mock := &mockDouyinAPI{
		videoDetailErr: fmt.Errorf("network timeout"),
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "fail_vid",
		Title:    "失败测试",
		Status:   "failed",
	})

	dl, _ := s.db.GetDownload(dlID)
	s.retryOneDouyinDownload(*dl)

	// 不应暂停抖音（非风控错误）
	if s.IsDouyinPaused() {
		t.Error("should not pause douyin for non-risk-control error")
	}

	updated, _ := s.db.GetDownload(dlID)
	if updated.Status != "failed" {
		t.Errorf("expected 'failed', got %q", updated.Status)
	}
}

func TestRetryOneDouyinDownload_NoVideoURL_Completed(t *testing.T) {
	mock := &mockDouyinAPI{
		videoDetailResult: &douyin.DouyinVideo{
			AwemeID:  "no_url_vid",
			Desc:     "无URL",
			VideoURL: "",
			IsNote:   false,
			Author:   douyin.DouyinUser{Nickname: "TestUser"},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "no_url_vid",
		Title:    "无URL",
		Status:   "pending",
	})

	dl, _ := s.db.GetDownload(dlID)
	s.retryOneDouyinDownload(*dl)

	updated, _ := s.db.GetDownload(dlID)
	if updated.Status != "completed" {
		t.Errorf("expected 'completed' (skipped), got %q", updated.Status)
	}
}

func TestRetryOneDouyinDownload_ResolveVideoURLFail(t *testing.T) {
	mock := &mockDouyinAPI{
		videoDetailResult: &douyin.DouyinVideo{
			AwemeID:  "resolve_fail",
			Desc:     "解析失败",
			VideoURL: "https://bad.url/video.mp4",
			Author:   douyin.DouyinUser{Nickname: "TestUser"},
		},
		resolveVideoErr: fmt.Errorf("resolve failed"),
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "TestUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "resolve_fail",
		Title:    "解析失败",
		Status:   "pending",
	})

	dl, _ := s.db.GetDownload(dlID)
	s.retryOneDouyinDownload(*dl)

	updated, _ := s.db.GetDownload(dlID)
	if updated.Status != "failed" {
		t.Errorf("expected 'failed', got %q", updated.Status)
	}
}

func TestRetryOneDouyinDownload_NoteDownload(t *testing.T) {
	imgContent := "fake image data"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(imgContent))
	}))
	defer ts.Close()

	mock := &mockDouyinAPI{
		videoDetailResult: &douyin.DouyinVideo{
			AwemeID:    "note_vid",
			Desc:       "图集测试",
			IsNote:     true,
			Images:     []string{ts.URL + "/img1.jpg", ts.URL + "/img2.jpg"},
			Cover:      ts.URL + "/cover.jpg",
			CreateTime: time.Now().Unix(),
			Author:     douyin.DouyinUser{Nickname: "NoteAuthor"},
		},
	}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "NoteAuthor", "https://www.douyin.com/user/MS4wLjABAAAAtest")
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "note_vid",
		Title:    "图集测试",
		Uploader: "NoteAuthor",
		Status:   "pending",
	})

	dl, _ := s.db.GetDownload(dlID)
	s.retryOneDouyinDownload(*dl)

	updated, _ := s.db.GetDownload(dlID)
	if updated.Status != "completed" {
		t.Errorf("expected 'completed' for note, got %q", updated.Status)
	}
}

// ============================
// resolveDouyinSecUID 测试
// ============================

func TestResolveDouyinSecUID_DirectURL(t *testing.T) {
	mock := &mockDouyinAPI{
		resolveErr: fmt.Errorf("should not be called"),
	}
	s := newTestScheduler(t, mock)

	secUID, err := s.resolveDouyinSecUID(mock, "https://www.douyin.com/user/MS4wLjABAAAAtest_sec_uid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secUID != "MS4wLjABAAAAtest_sec_uid" {
		t.Errorf("expected 'MS4wLjABAAAAtest_sec_uid', got %q", secUID)
	}
}

func TestResolveDouyinSecUID_ShareURL_Success(t *testing.T) {
	mock := &mockDouyinAPI{
		resolveResult: &douyin.ResolveResult{
			Type:   douyin.URLTypeUser,
			SecUID: "resolved_sec_uid",
		},
	}
	s := newTestScheduler(t, mock)

	secUID, err := s.resolveDouyinSecUID(mock, "https://v.douyin.com/some_share_link")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secUID != "resolved_sec_uid" {
		t.Errorf("expected 'resolved_sec_uid', got %q", secUID)
	}
}

func TestResolveDouyinSecUID_ShareURL_VideoType(t *testing.T) {
	mock := &mockDouyinAPI{
		resolveResult: &douyin.ResolveResult{
			Type:    douyin.URLTypeVideo,
			VideoID: "12345",
		},
	}
	s := newTestScheduler(t, mock)

	_, err := s.resolveDouyinSecUID(mock, "https://v.douyin.com/video_link")
	if err == nil {
		t.Error("expected error for video URL type")
	}
}

// ============================
// getDouyinSetting 测试
// ============================

func TestGetDouyinSetting_PlatformSpecific(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	s.db.SetSetting("douyin_check_interval", "10")
	s.db.SetSetting("check_interval", "30")

	val := s.getDouyinSetting("check_interval")
	if val != "10" {
		t.Errorf("expected '10' (platform-specific), got %q", val)
	}
}

func TestGetDouyinSetting_FallbackToGlobal(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	s.db.SetSetting("some_global_key", "global_val")

	val := s.getDouyinSetting("some_global_key")
	if val != "global_val" {
		t.Errorf("expected 'global_val', got %q", val)
	}
}

func TestGetDouyinSetting_NotFound(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	val := s.getDouyinSetting("nonexistent_key")
	if val != "" {
		t.Errorf("expected empty string, got %q", val)
	}
}

// ============================
// downloadDouyinFile 补充边界测试
// ============================

func TestDownloadDouyinFile_OverwritesEmptyFile(t *testing.T) {
	content := "new video content"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(content))
	}))
	defer ts.Close()

	destPath := filepath.Join(t.TempDir(), "video.mp4")
	// 创建空文件
	os.WriteFile(destPath, []byte{}, 0644)

	size, err := downloadDouyinFile(ts.URL+"/video.mp4", destPath)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), size)
	}
}

// ============================
// downloadDouyinNote 测试
// ============================

func TestDownloadDouyinNote_AllImagesFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()

	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "NoteUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "note_fail",
		Title:    "失败图集",
		Uploader: "NoteUser",
		Status:   "downloading",
	})

	detail := &douyin.DouyinVideo{
		AwemeID:    "note_fail",
		Desc:       "失败图集",
		IsNote:     true,
		Images:     []string{ts.URL + "/bad1.jpg", ts.URL + "/bad2.jpg"},
		CreateTime: time.Now().Unix(),
		Author:     douyin.DouyinUser{Nickname: "NoteUser"},
	}

	dl, _ := s.db.GetDownload(dlID)
	s.downloadDouyinNote(*dl, src, detail)

	updated, _ := s.db.GetDownload(dlID)
	if updated.Status != "failed" {
		t.Errorf("expected 'failed' when all images fail, got %q", updated.Status)
	}
}

func TestDownloadDouyinNote_PartialSuccess(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 3 { // first image with 3 retries = fail
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("image data"))
	}))
	defer ts.Close()

	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)

	src := createTestSource(t, s.db, "NoteUser", "https://www.douyin.com/user/MS4wLjABAAAAtest")
	dlID, _ := s.db.CreateDownload(&db.Download{
		SourceID: src.ID,
		VideoID:  "note_partial",
		Title:    "部分成功",
		Uploader: "NoteUser",
		Status:   "downloading",
	})

	detail := &douyin.DouyinVideo{
		AwemeID:    "note_partial",
		Desc:       "部分成功",
		IsNote:     true,
		Images:     []string{ts.URL + "/img1.jpg", ts.URL + "/img2.jpg"},
		CreateTime: time.Now().Unix(),
		Author:     douyin.DouyinUser{Nickname: "NoteUser"},
	}

	dl, _ := s.db.GetDownload(dlID)
	s.downloadDouyinNote(*dl, src, detail)

	updated, _ := s.db.GetDownload(dlID)
	if updated.Status != "completed" {
		t.Errorf("expected 'completed' with partial success, got %q", updated.Status)
	}
}

// ============================
// loadDouyinUserCookie 测试
// ============================

func TestLoadDouyinUserCookie_Empty(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)
	// 没有设置 cookie，不应 panic
	s.loadDouyinUserCookie()
}

func TestLoadDouyinUserCookie_WithValue(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)
	s.db.SetSetting("douyin_cookie", "test_cookie_value")
	// 不应 panic
	s.loadDouyinUserCookie()
}

func TestRefreshDouyinUserCookie(t *testing.T) {
	mock := &mockDouyinAPI{}
	s := newTestScheduler(t, mock)
	s.db.SetSetting("douyin_cookie", "refreshed_cookie")
	// 不应 panic
	s.RefreshDouyinUserCookie("new_cookie")
}
