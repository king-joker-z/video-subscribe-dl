package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"runtime"
	"sync/atomic"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/scanner"
	"video-subscribe-dl/internal/util"
	newapi "video-subscribe-dl/web/api"
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
	apiRouter      *newapi.Router

	// Callbacks
	onCheckNow          func()
	onCookieUpdate      func(string)
	onCredentialUpdate  func(*bilibili.Credential)
	onRetryDownload     func(int64)
	onSyncSource        func(int64)
	onProcessPending    func()

	version        string
	buildTime      string
	startTime      time.Time

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
		cachedRateLimit: 200,
	}

	// 启动时从 DB 读取 rate limit 设置
	if val, err := database.GetSetting("rate_limit_per_minute"); err == nil && val != "" {
		if n, parseErr := strconv.Atoi(val); parseErr == nil && n > 0 {
			s.cachedRateLimit = n
		}
	}

	// 加载模板
	s.templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

	// 路由注册延迟到 setupAndStart()，等回调函数设置完毕

	return s
}

// setupRoutes 设置所有路由（在 callback 全部设置后调用）
func (s *Server) setupRoutes() {
	s.registerRoutes()

	// 设置新版 API router 的回调
	if s.apiRouter != nil {
		s.apiRouter.SetCallbacks(
			func() { if s.onCheckNow != nil { go s.onCheckNow() } },
			s.onCredentialUpdate,
			func(id int64) { if s.onRetryDownload != nil { s.onRetryDownload(id) } },
			func(id int64) { if s.onSyncSource != nil { s.onSyncSource(id) } },
			func() { if s.onProcessPending != nil { s.onProcessPending() } },
			s.RefreshRateLimit,
		)
		s.apiRouter.SetVersion(s.version)
		s.apiRouter.SetBuildTime(s.buildTime)
		s.apiRouter.SetStartTime(s.startTime)
	}
}

func (s *Server) SetCheckNowFunc(fn func()) {
	s.onCheckNow = fn
}

func (s *Server) SetCookieUpdateFunc(fn func(string)) {
	s.onCookieUpdate = fn
}

func (s *Server) SetCredentialUpdateFunc(fn func(*bilibili.Credential)) {
	s.onCredentialUpdate = fn
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
	// 在启动前设置路由（此时所有 callback 已设置）
	s.setupRoutes()

	addr := fmt.Sprintf(":%d", s.port)
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           s.rateLimitMiddleware(s.authMiddleware(s.mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	log.Printf("Web server listening on http://localhost%s", addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *Server) getAuthToken() string {
	if t := os.Getenv("AUTH_TOKEN"); t != "" {
		return t
	}
	if t, err := s.db.GetSetting("auth_token"); err == nil && t != "" {
		return t
	}
	return ""
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		token := s.getAuthToken()
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

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

func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		s.rateLimitMu.RLock()
		limit := s.cachedRateLimit
		s.rateLimitMu.RUnlock()
		if limit > 0 {
			rateLimitMax = limit
		}

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

func (s *Server) RefreshRateLimit() {
	if val, err := s.db.GetSetting("rate_limit_per_minute"); err == nil && val != "" {
		if n, parseErr := strconv.Atoi(val); parseErr == nil && n > 0 {
			s.rateLimitMu.Lock()
			s.cachedRateLimit = n
			s.rateLimitMu.Unlock()
			return
		}
	}
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
	if s.downloader != nil {
		progress := s.downloader.GetProgress()
		health["active_downloads"] = len(progress)
		health["queue_paused"] = s.downloader.IsPaused()
	}
	if diskInfo, err := util.GetDiskInfo(s.downloadDir); err == nil {
		health["disk_free_gb"] = float64(diskInfo.Available) / 1024 / 1024 / 1024
		health["disk_total_gb"] = float64(diskInfo.Total) / 1024 / 1024 / 1024
	}
	jsonResponse(w, health)
}

func (s *Server) SetVersion(v string)          { s.version = v }
func (s *Server) SetStartTime(t time.Time)     { s.startTime = t }
func (s *Server) SetBuildTime(t string)         { s.buildTime = t }

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]interface{}{
		"version":    s.version,
		"uptime":     time.Since(s.startTime).String(),
		"go":         runtime.Version(),
		"build_time": s.buildTime,
	})
}

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

func (s *Server) handleQueuePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	s.downloader.Pause()
	jsonResponse(w, map[string]bool{"ok": true})
}

func (s *Server) handleQueueResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	s.downloader.Resume()
	jsonResponse(w, map[string]bool{"ok": true})
}

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

func getCORSOrigin() string {
	return os.Getenv("CORS_ORIGIN")
}
