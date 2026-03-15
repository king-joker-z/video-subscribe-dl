package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
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
	rateLimitMax    = 200    // 每分钟最大请求数
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
	db          *db.DB
	downloader  *downloader.Downloader
	scanner     *scanner.Scanner
	port        int
	dataDir     string
	downloadDir string
	mux         *http.ServeMux
	httpServer  *http.Server
	templates   *template.Template
	notifier    *notify.Notifier
	apiRouter   *newapi.Router

	// Callbacks
	getCooldownInfo    func() (bool, int)
	onCheckNow         func()
	onCookieUpdate     func(string)
	onCredentialUpdate func(*bilibili.Credential)
	onRetryDownload    func(int64)
	onSyncSource       func(int64)
	onFullScanSource   func(int64)
	onProcessPending   func()
	onRedownload       func(int64)
	getBiliClient      func() *bilibili.Client
	onConfigReload     func()

	version   string
	buildTime string
	startTime time.Time

	cachedRateLimit int
	rateLimitMu     sync.RWMutex
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
			func() {
				if s.onCheckNow != nil {
					go s.onCheckNow()
				}
			},
			s.onCredentialUpdate,
			func(id int64) {
				if s.onRetryDownload != nil {
					s.onRetryDownload(id)
				}
			},
			func(id int64) {
				if s.onSyncSource != nil {
					s.onSyncSource(id)
				}
			},
			func() {
				if s.onProcessPending != nil {
					s.onProcessPending()
				}
			},
			s.RefreshRateLimit,
			func(id int64) {
				if s.onRedownload != nil {
					s.onRedownload(id)
				}
			},
		)
		s.apiRouter.SetVersion(s.version)
		if s.getCooldownInfo != nil {
			s.apiRouter.SetCooldownInfoFunc(s.getCooldownInfo)
		}
		s.apiRouter.SetBuildTime(s.buildTime)
		s.apiRouter.SetStartTime(s.startTime)
		if s.onFullScanSource != nil {
			s.apiRouter.SetFullScanSourceFunc(func(id int64) { s.onFullScanSource(id) })
		}
		s.apiRouter.SetBiliClientFunc(s.getBiliClient)
		s.apiRouter.SetConfigReloadFunc(s.onConfigReload)
	}
}

func (s *Server) SetCooldownInfoFunc(fn func() (bool, int)) {
	s.getCooldownInfo = fn
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

func (s *Server) SetFullScanSourceFunc(fn func(int64)) {
	s.onFullScanSource = fn
}

func (s *Server) SetProcessPendingFunc(fn func()) {
	s.onProcessPending = fn
}

func (s *Server) SetRedownloadFunc(fn func(int64)) {
	s.onRedownload = fn
}

func (s *Server) SetBiliClientFunc(fn func() *bilibili.Client) {
	s.getBiliClient = fn
}

func (s *Server) SetConfigReloadFunc(fn func()) {
	s.onConfigReload = fn
}

func (s *Server) SetNotifier(n *notify.Notifier) {
	s.notifier = n
}

func (s *Server) ensureAuthToken() {
	// 环境变量优先
	if os.Getenv("AUTH_TOKEN") != "" {
		log.Printf("[auth] 使用环境变量 AUTH_TOKEN")
		return
	}
	// 如果环境变量 NO_AUTH=1 则禁用
	if noAuth := os.Getenv("NO_AUTH"); noAuth == "1" || noAuth == "true" {
		log.Printf("[auth] 认证已禁用 (NO_AUTH=%s)", noAuth)
		return
	}
	// 检查 DB 是否已有 token
	if token, err := s.db.GetSetting("auth_token"); err == nil && token != "" {
		log.Printf("[auth] Web UI 认证已启用，token: %s", token)
		return
	}
	// 自动生成随机 token
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Printf("[auth] 生成 token 失败: %v，认证未启用", err)
		return
	}
	token := hex.EncodeToString(b)
	if err := s.db.SetSetting("auth_token", token); err != nil {
		log.Printf("[auth] 保存 token 失败: %v", err)
		return
	}
	log.Printf("============================================")
	log.Printf("[auth] Web UI 认证 Token（首次生成）: %s", token)
	log.Printf("[auth] 请妥善保存此 Token，用于登录 Web 界面")
	log.Printf("[auth] 可通过设置页面修改或设置环境变量 AUTH_TOKEN")
	log.Printf("[auth] 设置 NO_AUTH=1 可禁用认证")
	log.Printf("============================================")
}

func (s *Server) Start() error {
	// 在启动前设置路由（此时所有 callback 已设置）
	s.setupRoutes()

	// 自动生成 auth_token（如果未设置且未禁用）
	// s.ensureAuthToken() // auth disabled

	addr := fmt.Sprintf(":%d", s.port)
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           s.rateLimitMiddleware(s.mux),
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
	noAuth := os.Getenv("NO_AUTH")
	if noAuth == "1" || noAuth == "true" {
		return "" // 禁用认证
	}
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
		path := r.URL.Path

		// 白名单路径不需要认证
		if s.isAuthWhitelist(path) {
			next.ServeHTTP(w, r)
			return
		}

		token := s.getAuthToken()
		if token == "" {
			// 未设置 token 不启用认证
			next.ServeHTTP(w, r)
			return
		}

		// 1. Authorization: Bearer {token}
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			if strings.TrimPrefix(auth, "Bearer ") == token {
				next.ServeHTTP(w, r)
				return
			}
		}

		// 2. Cookie auth_token
		if cookie, err := r.Cookie("auth_token"); err == nil && cookie.Value == token {
			next.ServeHTTP(w, r)
			return
		}

		// 3. Query param ?token=xxx（WebSocket 连接用）
		if qToken := r.URL.Query().Get("token"); qToken == token {
			next.ServeHTTP(w, r)
			return
		}

		// 未认证: API 请求返回 401, 页面请求重定向到登录
		if strings.HasPrefix(path, "/api/") {
			jsonError(w, "未认证，请先登录", http.StatusUnauthorized)
		} else {
			// 返回登录页面（前端 SPA 会处理）
			jsonError(w, "unauthorized", http.StatusUnauthorized)
		}
	})
}

func (s *Server) isAuthWhitelist(path string) bool {
	// 静态文件和入口页面
	if strings.HasPrefix(path, "/static/") || path == "/" || path == "/index.html" || path == "/favicon.ico" {
		return true
	}
	// 健康检查
	if path == "/health" {
		return true
	}
	// 登录相关 API
	whitelist := []string{
		"/api/login/token",
		"/api/login/qrcode/generate",
		"/api/login/qrcode/poll",
	}
	for _, w := range whitelist {
		if path == w {
			return true
		}
	}
	return false
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
		"status":         "ok",
		"version":        s.version,
		"uptime":         uptime.String(),
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

func (s *Server) SetVersion(v string)      { s.version = v }
func (s *Server) SetStartTime(t time.Time) { s.startTime = t }
func (s *Server) SetBuildTime(t string)    { s.buildTime = t }

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
