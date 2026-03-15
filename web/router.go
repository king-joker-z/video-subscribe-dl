package web

import (
	"io/fs"
	"net/http"

	newapi "video-subscribe-dl/web/api"
)

// registerRoutes 注册所有路由
func (s *Server) registerRoutes() {
	// ========== 新版 API（统一响应格式）==========
	s.apiRouter = newapi.NewRouter(s.db, s.downloader, s.downloadDir)
	s.apiRouter.Register(s.mux)

	// 新版已注册的路由：
	//   /api/dashboard, /api/sources, /api/sources/,
	//   /api/videos, /api/videos/, /api/thumb/,
	//   /api/uploaders, /api/uploaders/,
	//   /api/task/status, /api/task/trigger, /api/task/pause, /api/task/resume,
	//   /api/settings, /api/credential, /api/credential/refresh,
	//   /api/login/qrcode/generate, /api/login/qrcode/poll,
	//   /api/events, /api/logs, /api/version

	// ========== 旧版独有路由（不与新版冲突）==========
	s.mux.HandleFunc("/api/progress/stream", s.handleProgressStream)

	s.mux.HandleFunc("/api/downloads/batch/process-pending", s.handleBatchProcessPending)
	s.mux.HandleFunc("/api/downloads/batch/retry-failed", s.handleBatchRetryFailed)
	s.mux.HandleFunc("/api/downloads/batch/completed", s.handleBatchDeleteCompleted)
	s.mux.HandleFunc("/api/downloads/uploaders", s.handleDownloadUploaders)
	s.mux.HandleFunc("/api/downloads/by-uploader", s.handleDownloadsByUploader)
	s.mux.HandleFunc("/api/downloads/actions", s.handleDownloadActions)
	s.mux.HandleFunc("/api/downloads/stats-by-uploader", s.handleDownloadStatsByUploader)
	s.mux.HandleFunc("/api/downloads/retry-failed-by-uploader", s.handleRetryFailedByUploader)
	s.mux.HandleFunc("/api/downloads/process-pending-by-uploader", s.handleProcessPendingByUploader)
	s.mux.HandleFunc("/api/downloads/completed-by-uploader", s.handleCompletedByUploader)
	s.mux.HandleFunc("/api/downloads/", s.handleDownloadByID)
	s.mux.HandleFunc("/api/downloads", s.handleDownloads)

	s.mux.HandleFunc("/api/queue/run", s.handleQueueRun)
	s.mux.HandleFunc("/api/queue/pause", s.handleQueuePause)
	s.mux.HandleFunc("/api/queue/resume", s.handleQueueResume)
	s.mux.HandleFunc("/api/queue", s.handleQueue)

	s.mux.HandleFunc("/api/scan", s.handleScan)
	s.mux.HandleFunc("/api/scan/status", s.handleScanStatus)
	s.mux.HandleFunc("/api/scan/fix", s.handleScanFix)

	s.mux.HandleFunc("/api/settings/", s.handleSettingByKey)

	s.mux.HandleFunc("/api/cookie/upload", s.handleCookieUpload)
	s.mux.HandleFunc("/api/cookie/verify", s.handleCookieVerify)

	s.mux.HandleFunc("/api/credential/status", s.handleCredentialStatus)
	s.mux.HandleFunc("/api/credential/clear", s.handleCredentialClear)

	s.mux.HandleFunc("/api/clean/source/", s.handleCleanSource)
	s.mux.HandleFunc("/api/clean/uploader/", s.handleCleanUploader)
	s.mux.HandleFunc("/api/people/", s.handlePeopleByName)
	s.mux.HandleFunc("/api/people", s.handlePeople)
	s.mux.HandleFunc("/api/stats", s.handleStats)
	s.mux.HandleFunc("/api/logs/stream", s.handleLogStream)
	s.mux.HandleFunc("/api/notify/test", s.handleNotifyTest)
	s.mux.HandleFunc("/api/cleanup/stats", s.handleCleanupStats)
	s.mux.HandleFunc("/api/cleanup/config", s.handleCleanupConfig)

	s.mux.HandleFunc("/health", s.handleHealth)

	// ========== 静态资源 & 首页 ==========
	staticSub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	s.mux.HandleFunc("/", s.handleIndex)
}
