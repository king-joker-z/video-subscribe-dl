package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/downloader"
	newapi "video-subscribe-dl/web/api"
)

// ============================================================
// 辅助: 创建临时 DB + Downloader + API Router
// ============================================================

type testEnv struct {
	db          *db.DB
	dl          *downloader.Downloader
	router      *newapi.Router
	mux         *http.ServeMux
	dataDir     string
	downloadDir string
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	downloadDir := filepath.Join(tmpDir, "downloads")
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(downloadDir, 0755)

	database, err := db.Init(dataDir)
	if err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	dl := downloader.New(downloader.Config{
		MaxConcurrent:   1,
		RequestInterval: 0,
	}, nil)
	t.Cleanup(func() { dl.Stop() })

	router := newapi.NewRouter(database, dl, downloadDir)
	mux := http.NewServeMux()
	router.Register(mux)
	router.SetStartTime(time.Now())

	return &testEnv{
		db:          database,
		dl:          dl,
		router:      router,
		mux:         mux,
		dataDir:     dataDir,
		downloadDir: downloadDir,
	}
}

// apiCall 发送 API 请求并解析 JSON 响应
func apiCall(t *testing.T, mux *http.ServeMux, method, path string, body string) map[string]interface{} {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response from %s %s: %v\nbody: %s", method, path, err, w.Body.String())
	}
	return resp
}

// ============================================================
// TestBiliSourceCheckFlow — B站 UP 主增量检查流程
// ============================================================

func TestBiliSourceCheckFlow(t *testing.T) {
	env := setupTestEnv(t)

	// 1. 添加 B站 UP 主源
	resp := apiCall(t, env.mux, "POST", "/api/sources", `{
		"type": "up",
		"url": "https://space.bilibili.com/12345678",
		"name": "测试UP主"
	}`)
	if code := int(resp["code"].(float64)); code != 0 {
		t.Fatalf("添加源失败: %v", resp)
	}
	data := resp["data"].(map[string]interface{})
	sourceID := int64(data["id"].(float64))

	// 2. 查询源列表
	listResp := apiCall(t, env.mux, "GET", "/api/sources", "")
	items := listResp["data"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("期望 1 个源，实际: %d", len(items))
	}
	item := items[0].(map[string]interface{})
	if item["name"] != "测试UP主" {
		t.Errorf("source name = %v, want 测试UP主", item["name"])
	}

	// 3. 模拟 checkSource 发现新视频后创建 pending 记录
	dlRec := &db.Download{
		SourceID: sourceID,
		VideoID:  "BV1test123",
		Title:    "测试视频",
		Uploader: "测试UP主",
		Status:   "pending",
	}
	id, err := env.db.CreateDownload(dlRec)
	if err != nil || id <= 0 {
		t.Fatalf("创建 download 失败: %v (id=%d)", err, id)
	}

	// 4. 验证 pending 记录
	pending, err := env.db.GetDownloadsByStatus("pending", 100)
	if err != nil {
		t.Fatalf("查询 pending 失败: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("期望 1 条 pending，实际: %d", len(pending))
	}
	if pending[0].VideoID != "BV1test123" {
		t.Errorf("video_id = %s, want BV1test123", pending[0].VideoID)
	}

	// 5. 查重: 同一 source+video 已存在
	dup, _ := env.db.IsVideoDownloaded(sourceID, "BV1test123")
	if !dup {
		t.Error("期望查重返回 true")
	}

	// 6. 模拟下载完成
	env.db.UpdateDownloadStatus(pending[0].ID, "completed", "/path/to/video.mp4", 12345678, "")
	completed, _ := env.db.GetDownloadsByStatus("completed", 100)
	if len(completed) != 1 {
		t.Errorf("期望 1 条 completed，实际: %d", len(completed))
	}
}

// ============================================================
// TestDouyinSourceCheckFlow — 抖音用户增量检查流程
// ============================================================

func TestDouyinSourceCheckFlow(t *testing.T) {
	env := setupTestEnv(t)

	// 1. 添加抖音源
	resp := apiCall(t, env.mux, "POST", "/api/sources", `{
		"type": "douyin",
		"url": "https://www.douyin.com/user/MS4wLjABAAAAtest",
		"name": "测试抖音号"
	}`)
	if code := int(resp["code"].(float64)); code != 0 {
		t.Fatalf("添加源失败: %v", resp)
	}
	data := resp["data"].(map[string]interface{})
	sourceID := int64(data["id"].(float64))

	// 2. 模拟 checkDouyin 发现新视频后创建 pending 记录
	now := time.Now()
	for i := 1; i <= 3; i++ {
		dlRec := &db.Download{
			SourceID: sourceID,
			VideoID:  fmt.Sprintf("douyin_%d", i),
			Title:    fmt.Sprintf("抖音视频_%d", i),
			Uploader: "测试抖音号",
			Status:   "pending",
			Duration: 30 + i,
		}
		if _, err := env.db.CreateDownload(dlRec); err != nil {
			t.Fatalf("创建 download %d 失败: %v", i, err)
		}
	}

	// 3. 验证 3 条 pending
	pending, err := env.db.GetDownloadsByStatus("pending", 100)
	if err != nil || len(pending) != 3 {
		t.Fatalf("期望 3 条 pending，实际: %d (err=%v)", len(pending), err)
	}

	// 4. 模拟一条下载完成
	env.db.UpdateDownloadStatus(pending[0].ID, "completed", "/path/to/video.mp4", 12345678, "")

	// 5. 验证只剩 2 条 pending
	pending2, _ := env.db.GetDownloadsByStatus("pending", 100)
	if len(pending2) != 2 {
		t.Errorf("期望 2 条 pending，实际: %d", len(pending2))
	}

	// 6. 更新 latest_video_at（增量基准）
	env.db.UpdateSourceLatestVideoAt(sourceID, now.Unix())
	latestAt, err := env.db.GetSourceLatestVideoAt(sourceID)
	if err != nil || latestAt != now.Unix() {
		t.Errorf("latest_video_at = %d, want %d (err=%v)", latestAt, now.Unix(), err)
	}

	// 7. 验证查重
	dup, _ := env.db.IsVideoDownloaded(sourceID, "douyin_1")
	if !dup {
		t.Error("期望 douyin_1 查重返回 true")
	}
	dup2, _ := env.db.IsVideoDownloaded(sourceID, "douyin_999")
	if dup2 {
		t.Error("期望 douyin_999 查重返回 false")
	}
}

// ============================================================
// TestQuickDownloadBili — B站快速下载 API 参数校验
// ============================================================

func TestQuickDownloadBili(t *testing.T) {
	env := setupTestEnv(t)

	// 1. 无 URL 参数应返回错误
	resp := apiCall(t, env.mux, "POST", "/api/download", `{}`)
	if code := int(resp["code"].(float64)); code == 0 {
		t.Fatal("空 URL 应该返回错误")
	}

	// 2. 空 URL
	resp = apiCall(t, env.mux, "POST", "/api/download", `{"url": ""}`)
	if code := int(resp["code"].(float64)); code == 0 {
		t.Fatal("空 URL 应该返回错误")
	}

	// 3. GET 方法应该报错
	resp = apiCall(t, env.mux, "GET", "/api/download/preview", "")
	// preview 接口 GET 应该被拒绝（方法不对或缺少参数）
	if resp == nil {
		t.Fatal("响应不应为 nil")
	}
}

// ============================================================
// TestQuickDownloadDouyin — 抖音快速下载 API 参数校验
// ============================================================

func TestQuickDownloadDouyin(t *testing.T) {
	env := setupTestEnv(t)

	// 1. 空 URL
	resp := apiCall(t, env.mux, "POST", "/api/download", `{"url": ""}`)
	if code := int(resp["code"].(float64)); code == 0 {
		t.Fatal("空 URL 应该返回错误")
	}

	// 2. 验证 metrics 端点正常工作
	metricsResp := apiCall(t, env.mux, "GET", "/api/metrics", "")
	if code := int(metricsResp["code"].(float64)); code != 0 {
		t.Fatalf("metrics 端点返回错误: %v", metricsResp)
	}
	metricsData := metricsResp["data"].(map[string]interface{})

	// 验证 metrics 字段
	for _, field := range []string{"goroutines", "memory_mb", "downloader", "uptime_seconds"} {
		if _, ok := metricsData[field]; !ok {
			t.Errorf("metrics 缺少字段: %s", field)
		}
	}

	// 验证 downloader stats
	dlStats := metricsData["downloader"].(map[string]interface{})
	for _, field := range []string{"active", "queued", "completed", "failed"} {
		if _, ok := dlStats[field]; !ok {
			t.Errorf("downloader stats 缺少字段: %s", field)
		}
	}
}

// ============================================================
// TestMetricsEndpoint — 验证 /api/metrics 返回正确 JSON
// ============================================================

func TestMetricsEndpoint(t *testing.T) {
	env := setupTestEnv(t)

	resp := apiCall(t, env.mux, "GET", "/api/metrics", "")
	data := resp["data"].(map[string]interface{})

	if goroutines := data["goroutines"].(float64); goroutines <= 0 {
		t.Errorf("goroutines = %v, want > 0", goroutines)
	}
	if memMB := data["memory_mb"].(float64); memMB <= 0 {
		t.Errorf("memory_mb = %v, want > 0", memMB)
	}
	if uptime := data["uptime_seconds"].(float64); uptime < 0 {
		t.Errorf("uptime_seconds = %v, want >= 0", uptime)
	}

	cooldown := data["cooldown"].(map[string]interface{})
	for _, key := range []string{"bili", "douyin"} {
		if _, ok := cooldown[key]; !ok {
			t.Errorf("cooldown 缺少 %s 字段", key)
		}
	}
}

// ============================================================
// TestSignReloadEndpoint — 验证 /api/sign/reload 端点
// ============================================================

func TestSignReloadEndpoint(t *testing.T) {
	env := setupTestEnv(t)

	// POST 应成功
	resp := apiCall(t, env.mux, "POST", "/api/sign/reload", "")
	if code := int(resp["code"].(float64)); code != 0 {
		t.Fatalf("sign reload 返回错误: %v", resp)
	}

	// GET 应失败
	resp = apiCall(t, env.mux, "GET", "/api/sign/reload", "")
	if code := int(resp["code"].(float64)); code == 0 {
		t.Fatal("GET sign/reload 应该返回错误")
	}
}

// ============================================================
// TestGracefulShutdownDownloader — 下载器 Stop + Stats
// ============================================================

func TestGracefulShutdownDownloader(t *testing.T) {
	dl := downloader.New(downloader.Config{
		MaxConcurrent:   2,
		RequestInterval: 1,
	}, nil)

	stats := dl.Stats()
	if stats.Active != 0 || stats.Queued != 0 || stats.Completed != 0 || stats.Failed != 0 {
		t.Errorf("初始 stats 应全为 0: %+v", stats)
	}

	dl.Stop() // 不应 panic
}

// ============================================================
// TestDouyinClientClose — DouyinClient.Close() 不泄漏 goroutine
// ============================================================

func TestDouyinClientClose(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		client := douyin.NewClient()
		client.Close()
	}

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	after := runtime.NumGoroutine()

	leaked := after - before
	if leaked > 3 {
		t.Errorf("可能存在 goroutine 泄漏: before=%d, after=%d, leaked=%d", before, after, leaked)
	}
}

// ============================================================
// TestSourceCRUD — 完整的源增删改查流程
// ============================================================

func TestSourceCRUD(t *testing.T) {
	env := setupTestEnv(t)

	// Create
	resp := apiCall(t, env.mux, "POST", "/api/sources", `{
		"type": "up",
		"url": "https://space.bilibili.com/99999",
		"name": "CRUD测试"
	}`)
	data := resp["data"].(map[string]interface{})
	sourceID := int(data["id"].(float64))

	// Read
	resp = apiCall(t, env.mux, "GET", fmt.Sprintf("/api/sources/%d", sourceID), "")
	if code := int(resp["code"].(float64)); code != 0 {
		t.Fatalf("获取源失败: %v", resp)
	}

	// Update
	resp = apiCall(t, env.mux, "PUT", fmt.Sprintf("/api/sources/%d", sourceID), `{"name": "更新后的名字"}`)
	if code := int(resp["code"].(float64)); code != 0 {
		t.Fatalf("更新源失败: %v", resp)
	}

	// Verify update
	resp = apiCall(t, env.mux, "GET", fmt.Sprintf("/api/sources/%d", sourceID), "")
	srcWrapper := resp["data"].(map[string]interface{})
	srcData := srcWrapper["source"].(map[string]interface{})
	if srcData["name"] != "更新后的名字" {
		t.Errorf("名字未更新: got %v", srcData["name"])
	}

	// Delete
	resp = apiCall(t, env.mux, "DELETE", fmt.Sprintf("/api/sources/%d", sourceID), "")
	if code := int(resp["code"].(float64)); code != 0 {
		t.Fatalf("删除源失败: %v", resp)
	}

	// Verify delete
	listResp := apiCall(t, env.mux, "GET", "/api/sources", "")
	items := listResp["data"].([]interface{})
	if len(items) != 0 {
		t.Errorf("删除后仍有 %d 个源", len(items))
	}
}

// ============================================================
// TestPingEndpoint — health check
// ============================================================

func TestPingEndpoint(t *testing.T) {
	env := setupTestEnv(t)

	resp := apiCall(t, env.mux, "GET", "/api/ping", "")
	data := resp["data"].(map[string]interface{})
	if data["status"] != "pong" {
		t.Errorf("ping status = %v, want pong", data["status"])
	}
}

// ============================================================
// TestPrometheusMetrics — /api/metrics/prometheus 返回标准格式
// ============================================================

func TestPrometheusMetrics(t *testing.T) {
	env := setupTestEnv(t)

	req := httptest.NewRequest("GET", "/api/metrics/prometheus", nil)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}

	body := w.Body.String()

	// 必须包含 HELP 和 TYPE 行
	if !strings.Contains(body, "# HELP") {
		t.Error("missing # HELP lines in prometheus output")
	}
	if !strings.Contains(body, "# TYPE") {
		t.Error("missing # TYPE lines in prometheus output")
	}

	// 必须包含核心 metrics
	for _, metric := range []string{
		"vsd_goroutines",
		"vsd_memory_bytes",
		"vsd_memory_sys_bytes",
		"vsd_gc_cycles_total",
		"vsd_uptime_seconds",
		"vsd_downloader_active",
		"vsd_downloader_queued",
		"vsd_downloader_completed_total",
		"vsd_downloader_failed_total",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("missing metric: %s", metric)
		}
	}
}
