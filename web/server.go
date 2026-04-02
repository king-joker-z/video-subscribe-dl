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
	"strconv"
	"strings"
	"sync"
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
// P0-9: mu protects windowEnd and count together to prevent data races.
// windowEnd is a plain time.Time (not atomically addressable), so all reads
// and writes of windowEnd AND count must be done under the same mutex.
type rateLimitEntry struct {
	mu        sync.Mutex
	count     int64
	windowEnd time.Time
}

var (
	rateLimiter     sync.Map // IP -> *rateLimitEntry
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
	getPHCooldownInfo  func() (bool, int)
	onCheckNow         func()
	onCookieUpdate     func(string)
	onCredentialUpdate func(*bilibili.Credential)
	onRetryDownload    func(int64)
	onSyncSource       func(int64)
	onFullScanSource   func(int64)
	onProcessPending   func()
	onSyncAll          func()
	onRedownload       func(int64)
	getBiliClient      func() *bilibili.Client
	onConfigReload     func()
	onDouyinCookieUpdate  func(string)
	getDouyinPauseStatus  func() (bool, string, time.Time)
	onDouyinResume        func()
	onDouyinPause         func(reason string)
	onBiliResume          func()
	getDouyinCookieStatus func() (bool, string)

	// PH callbacks
	onPHCookieUpdate  func(string)
	getPHPauseStatus  func() (bool, string, time.Time)
	onPHResume        func()
	onPHPause         func(reason string)
	getPHCookieStatus func() (bool, string)

	onRepairThumb func(string, string) error

	version   string
	buildTime string
	startTime time.Time

	cachedRateLimit int
	rateLimitMu     sync.RWMutex

	// Session nonce store for WebSocket auth (short-lived, single-use)
	nonceMu    sync.Mutex
	nonceStore map[string]time.Time // nonce -> expiry
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
		startTime:       time.Now(), // P2-7: initialize so uptime is correct from start
	}

	s.nonceStore = make(map[string]time.Time)

	// Periodically clean up expired nonces (same pattern as rateLimiter cleanup).
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			s.nonceMu.Lock()
			for nonce, exp := range s.nonceStore {
				if now.After(exp) {
					delete(s.nonceStore, nonce)
				}
			}
			s.nonceMu.Unlock()
		}
	}()

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
		if s.getPHCooldownInfo != nil {
			s.apiRouter.SetPHCooldownInfoFunc(s.getPHCooldownInfo)
		}
		s.apiRouter.SetBuildTime(s.buildTime)
		s.apiRouter.SetStartTime(s.startTime)
		if s.onSyncAll != nil {
			s.apiRouter.SetSyncAllFunc(func() { s.onSyncAll() })
		}
		if s.onFullScanSource != nil {
			s.apiRouter.SetFullScanSourceFunc(func(id int64) { s.onFullScanSource(id) })
		}
		s.apiRouter.SetBiliClientFunc(s.getBiliClient)
		s.apiRouter.SetConfigReloadFunc(s.onConfigReload)
		if s.onDouyinCookieUpdate != nil {
			s.apiRouter.SetDouyinCookieUpdateFunc(s.onDouyinCookieUpdate)
		}
		if s.getDouyinPauseStatus != nil {
			s.apiRouter.SetDouyinStatusFunc(s.getDouyinPauseStatus)
		}
		if s.onDouyinResume != nil {
			s.apiRouter.SetDouyinResumeFunc(s.onDouyinResume)
		}
		if s.onDouyinPause != nil {
			s.apiRouter.SetDouyinPauseFunc(s.onDouyinPause)
		}
		if s.onBiliResume != nil {
			s.apiRouter.SetBiliResumeFunc(s.onBiliResume)
		}
		if s.getDouyinCookieStatus != nil {
			s.apiRouter.SetDouyinCookieStatusFunc(s.getDouyinCookieStatus)
		}
		// PH callbacks
		if s.onPHCookieUpdate != nil {
			s.apiRouter.SetPHCookieUpdateFunc(s.onPHCookieUpdate)
		}
		if s.getPHPauseStatus != nil {
			s.apiRouter.SetPHStatusFunc(s.getPHPauseStatus)
		}
		if s.onPHResume != nil {
			s.apiRouter.SetPHResumeFunc(s.onPHResume)
		}
		if s.onPHPause != nil {
			s.apiRouter.SetPHPauseFunc(s.onPHPause)
		}
		if s.getPHCookieStatus != nil {
			s.apiRouter.SetPHCookieStatusFunc(s.getPHCookieStatus)
		}
		if s.notifier != nil {
			s.apiRouter.SetNotifier(s.notifier)
		}
		if s.onRepairThumb != nil {
			s.apiRouter.SetRepairThumbFunc(s.onRepairThumb)
		}
	}
}

func (s *Server) SetCooldownInfoFunc(fn func() (bool, int)) {
	s.getCooldownInfo = fn
}

func (s *Server) SetPHCooldownInfoFunc(fn func() (bool, int)) {
	s.getPHCooldownInfo = fn
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

func (s *Server) SetSyncAllFunc(fn func()) {
	s.onSyncAll = fn
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

func (s *Server) SetDouyinCookieUpdateFunc(fn func(string)) {
	s.onDouyinCookieUpdate = fn
}

func (s *Server) SetDouyinPauseStatusFunc(fn func() (bool, string, time.Time)) {
	s.getDouyinPauseStatus = fn
}

func (s *Server) SetDouyinResumeFunc(fn func()) {
	s.onDouyinResume = fn
}

func (s *Server) SetDouyinPauseFunc(fn func(reason string)) {
	s.onDouyinPause = fn
}

func (s *Server) SetBiliResumeFunc(fn func()) {
	s.onBiliResume = fn
}

func (s *Server) SetDouyinCookieStatusFunc(fn func() (bool, string)) {
	s.getDouyinCookieStatus = fn
}

func (s *Server) SetPHCookieUpdateFunc(fn func(string)) {
	s.onPHCookieUpdate = fn
}

func (s *Server) SetPHPauseStatusFunc(fn func() (bool, string, time.Time)) {
	s.getPHPauseStatus = fn
}

func (s *Server) SetPHResumeFunc(fn func()) {
	s.onPHResume = fn
}

func (s *Server) SetPHPauseFunc(fn func(reason string)) {
	s.onPHPause = fn
}

func (s *Server) SetPHCookieStatusFunc(fn func() (bool, string)) {
	s.getPHCookieStatus = fn
}

func (s *Server) SetRepairThumbFunc(fn func(string, string) error) {
	s.onRepairThumb = fn
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
	s.ensureAuthToken()

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
		// P0-4: Use local `limit` variable directly; never write to the removed global rateLimitMax
		if limit <= 0 {
			limit = 200
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

		entry.mu.Lock()
		if now.After(entry.windowEnd) {
			entry.count = 0
			entry.windowEnd = now.Add(rateLimitWindow)
		}
		entry.count++
		current := entry.count
		entry.mu.Unlock()

		if int(current) > limit {
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

// POST /api/session
// Issues a short-lived session nonce for WebSocket auth.
// Nonce is single-use and expires after 60 seconds.
// authMiddleware must pass first (nonce only issued to authenticated sessions).
func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		jsonError(w, "nonce generation failed", http.StatusInternalServerError)
		return
	}
	nonce := hex.EncodeToString(b)
	s.nonceMu.Lock()
	s.nonceStore[nonce] = time.Now().Add(60 * time.Second)
	s.nonceMu.Unlock()
	jsonResponse(w, map[string]string{"nonce": nonce})
}

// validateAndConsumeNonce checks and single-use-consumes a session nonce.
// Returns true if the nonce exists and has not expired; deletes it in both cases.
func (s *Server) validateAndConsumeNonce(nonce string) bool {
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	exp, ok := s.nonceStore[nonce]
	if !ok {
		return false
	}
	delete(s.nonceStore, nonce) // single-use regardless of expiry
	return time.Now().Before(exp)
}

func (s *Server) SetVersion(v string)      { s.version = v }
func (s *Server) SetStartTime(t time.Time) { s.startTime = t }
func (s *Server) SetBuildTime(t string)    { s.buildTime = t }

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}


