package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"runtime"
	"sync/atomic"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/scanner"
	"video-subscribe-dl/internal/util"
)

// Rate limiter 结构
type rateLimitEntry struct {
	count     atomic.Int64
	windowEnd time.Time
}

var (
	rateLimiter     sync.Map // IP -> *rateLimitEntry
	rateLimitMax    = 200     // 每分钟最大请求数
	rateLimitWindow = time.Minute
)

func init() {
	// 每分钟重置 rateLimiter，防止 IP 条目无限增长导致内存泄漏
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rateLimiter.Range(func(key, value interface{}) bool {
				rateLimiter.Delete(key)
				return true
			})
		}
	}()
}

type Server struct {
	db             *db.DB
	downloader     *downloader.Downloader
	scanner        *scanner.Scanner
	port           int
	dataDir        string
	downloadDir    string
	mux            *http.ServeMux
	httpServer     *http.Server
	templates      *template.Template
	notifier       *notify.Notifier
	onCheckNow     func()
	onCookieUpdate  func(string)
	onRetryDownload func(int64)
	onSyncSource      func(int64)
	onProcessPending  func()
	version        string
	buildTime      string
	startTime      time.Time

	// 缓存 rate limit 设置值，避免每次请求读 DB
	cachedRateLimit   int
	rateLimitMu       sync.RWMutex
}

func NewServer(database *db.DB, dl *downloader.Downloader, sc *scanner.Scanner, port int, dataDir, downloadDir string) *Server {
	s := &Server{
		db:              database,
		downloader:      dl,
		scanner:         sc,
		port:            port,
		dataDir:         dataDir,
		downloadDir:     downloadDir,
		mux:             http.NewServeMux(),
		cachedRateLimit: 200, // 默认值
	}

	// 启动时从 DB 读取 rate limit 设置
	if val, err := database.GetSetting("rate_limit_per_minute"); err == nil && val != "" {
		if n, parseErr := strconv.Atoi(val); parseErr == nil && n > 0 {
			s.cachedRateLimit = n
		}
	}

	// 加载模板
	s.templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

	// API 路由（必须在 / 之前注册）
	s.mux.HandleFunc("/api/progress/stream", s.handleProgressStream)
	s.mux.HandleFunc("/api/sources/", s.handleSourceByID)
	s.mux.HandleFunc("/api/sources", s.handleSources)
	s.mux.HandleFunc("/api/downloads/batch/process-pending", s.handleBatchProcessPending)
	s.mux.HandleFunc("/api/downloads/batch/retry-failed", s.handleBatchRetryFailed)
	s.mux.HandleFunc("/api/downloads/batch/completed", s.handleBatchDeleteCompleted)
	s.mux.HandleFunc("/api/downloads/", s.handleDownloadByID)
	s.mux.HandleFunc("/api/downloads", s.handleDownloads)
	s.mux.HandleFunc("/api/queue/run", s.handleQueueRun)
	s.mux.HandleFunc("/api/queue/pause", s.handleQueuePause)
	s.mux.HandleFunc("/api/queue/resume", s.handleQueueResume)
	s.mux.HandleFunc("/api/queue", s.handleQueue)
	s.mux.HandleFunc("/api/scan", s.handleScan)
	s.mux.HandleFunc("/api/scan/status", s.handleScanStatus)
	s.mux.HandleFunc("/api/scan/fix", s.handleScanFix)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/settings/", s.handleSettingByKey)
	s.mux.HandleFunc("/api/cookie/upload", s.handleCookieUpload)
	s.mux.HandleFunc("/api/cookie/verify", s.handleCookieVerify)
	s.mux.HandleFunc("/api/clean/source/", s.handleCleanSource)
	s.mux.HandleFunc("/api/clean/uploader/", s.handleCleanUploader)
	s.mux.HandleFunc("/api/people/", s.handlePeopleByName)
	s.mux.HandleFunc("/api/people", s.handlePeople)
	s.mux.HandleFunc("/api/stats", s.handleStats)
	s.mux.HandleFunc("/api/logs/stream", s.handleLogStream)
	s.mux.HandleFunc("/api/logs", s.handleLogs)
	s.mux.HandleFunc("/api/thumb/", s.handleThumb)
	s.mux.HandleFunc("/api/notify/test", s.handleNotifyTest)
	s.mux.HandleFunc("/api/cleanup/stats", s.handleCleanupStats)
	s.mux.HandleFunc("/api/cleanup/config", s.handleCleanupConfig)
	s.mux.HandleFunc("/api/version", s.handleVersion)
	s.mux.HandleFunc("/health", s.handleHealth)
	staticSub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	s.mux.HandleFunc("/", s.handleIndex)

	return s
}

func (s *Server) SetCheckNowFunc(fn func()) {
	s.onCheckNow = fn
}

func (s *Server) SetCookieUpdateFunc(fn func(string)) {
	s.onCookieUpdate = fn
}

func (s *Server) SetRetryDownloadFunc(fn func(int64)) {
	s.onRetryDownload = fn
}

func (s *Server) SetSyncSourceFunc(fn func(int64)) {
	s.onSyncSource = fn
}

func (s *Server) SetProcessPendingFunc(fn func()) {
	s.onProcessPending = fn
}

func (s *Server) SetNotifier(n *notify.Notifier) {
	s.notifier = n
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           s.rateLimitMiddleware(s.authMiddleware(s.mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}
	log.Printf("Web server listening on http://localhost%s", addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the web server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// getAuthToken 获取有效的 auth token（优先环境变量，其次数据库）
func (s *Server) getAuthToken() string {
	if t := os.Getenv("AUTH_TOKEN"); t != "" {
		return t
	}
	if t, err := s.db.GetSetting("auth_token"); err == nil && t != "" {
		return t
	}
	return ""
}

// authMiddleware 鉴权中间件：token 为空时放行（向后兼容），否则校验
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只拦截 /api/ 开头的请求（但排除 /api/settings/auth_token PUT — 设置密码本身需要先鉴权不矛盾）
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		token := s.getAuthToken()
		if token == "" {
			// 未设置 token，不拦截
			next.ServeHTTP(w, r)
			return
		}

		// 从 query param 或 Authorization header 获取请求 token
		reqToken := r.URL.Query().Get("token")
		if reqToken == "" {
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				reqToken = strings.TrimPrefix(auth, "Bearer ")
			}
		}

		if reqToken != token {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware IP 级别请求频率限制
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只限制 /api/ 请求
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// 使用缓存的限制值
		s.rateLimitMu.RLock()
		limit := s.cachedRateLimit
		s.rateLimitMu.RUnlock()
		if limit > 0 {
			rateLimitMax = limit
		}

		// 获取客户端 IP
		ip := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			ip = strings.Split(forwarded, ",")[0]
		} else if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			ip = realIP
		}

		now := time.Now()
		val, _ := rateLimiter.LoadOrStore(ip, &rateLimitEntry{
			windowEnd: now.Add(rateLimitWindow),
		})
		entry := val.(*rateLimitEntry)

		// 窗口过期，重置
		if now.After(entry.windowEnd) {
			entry.count.Store(0)
			entry.windowEnd = now.Add(rateLimitWindow)
		}

		current := entry.count.Add(1)
		if int(current) > rateLimitMax {
			w.Header().Set("Retry-After", "60")
			jsonError(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RefreshRateLimit 刷新缓存的 rate limit 值（设置变更时调用）
func (s *Server) RefreshRateLimit() {
	if val, err := s.db.GetSetting("rate_limit_per_minute"); err == nil && val != "" {
		if n, parseErr := strconv.Atoi(val); parseErr == nil && n > 0 {
			s.rateLimitMu.Lock()
			s.cachedRateLimit = n
			s.rateLimitMu.Unlock()
			return
		}
	}
	// 没有设置或解析失败，使用默认值
	s.rateLimitMu.Lock()
	s.cachedRateLimit = 200
	s.rateLimitMu.Unlock()
}
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.templates.ExecuteTemplate(w, "index.html", nil)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(s.startTime)
	health := map[string]interface{}{
		"status":  "ok",
		"version": s.version,
		"uptime":  uptime.String(),
		"uptime_seconds": int(uptime.Seconds()),
	}
	// Queue status
	if s.downloader != nil {
		progress := s.downloader.GetProgress()
		health["active_downloads"] = len(progress)
		health["queue_paused"] = s.downloader.IsPaused()
	}
	// Disk info
	if diskInfo, err := util.GetDiskInfo(s.downloadDir); err == nil {
		health["disk_free_gb"] = float64(diskInfo.Available) / 1024 / 1024 / 1024
		health["disk_total_gb"] = float64(diskInfo.Total) / 1024 / 1024 / 1024
	}
	jsonResponse(w, health)
}

// SetVersion sets the server version string
func (s *Server) SetVersion(v string) { s.version = v }

// SetStartTime sets the server start time for uptime calculation
func (s *Server) SetStartTime(t time.Time) { s.startTime = t }

// SetBuildTime sets the build time string
func (s *Server) SetBuildTime(t string) { s.buildTime = t }

// GET /api/version — version and build information
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]interface{}{
		"version":    s.version,
		"uptime":     time.Since(s.startTime).String(),
		"go":         runtime.Version(),
		"build_time": s.buildTime,
	})
}

// handleProgressStream SSE 端点：实时推送下载进度
func (s *Server) handleProgressStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", 500)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if origin := getCORSOrigin(); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	// 立即发一条空的 progress 让前端知道连接已建立
	fmt.Fprintf(w, "data: []\n\n")
	flusher.Flush()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			progress := s.downloader.GetProgress()
			data, err := json.Marshal(progress)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// GET /api/queue
func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	stats, _ := s.db.GetStats()
	stats["paused"] = 0
	if s.downloader.IsPaused() {
		stats["paused"] = 1
	}
	jsonResponse(w, stats)
}

// POST /api/queue/run
func (s *Server) handleQueueRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	log.Println("Manual check triggered via API")
	if s.onCheckNow != nil {
		go s.onCheckNow()
	}
	jsonResponse(w, map[string]bool{"ok": true})
}

// POST /api/queue/pause
func (s *Server) handleQueuePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	s.downloader.Pause()
	jsonResponse(w, map[string]bool{"ok": true})
}

// POST /api/queue/resume
func (s *Server) handleQueueResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	s.downloader.Resume()
	jsonResponse(w, map[string]bool{"ok": true})
}

// POST /api/notify/test — 发送测试通知
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}

	if s.notifier == nil {
		jsonError(w, "notifier not initialized", 500)
		return
	}

	if err := s.notifier.SendTest(); err != nil {
		jsonError(w, "发送测试通知失败: "+err.Error(), 400)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"ok":      true,
		"message": "测试通知已发送",
	})
}

// getCORSOrigin 返回 CORS 允许的 Origin
// 默认不设置（同源访问不需要 CORS），仅当设置了 CORS_ORIGIN 环境变量时启用
func getCORSOrigin() string {
	return os.Getenv("CORS_ORIGIN")
}
