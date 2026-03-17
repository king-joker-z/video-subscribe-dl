package api

import (
	"net/http"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/notify"
)

// Router 新版 API 路由器
type Router struct {
	dashboard  *DashboardHandler
	sources    *SourcesHandler
	videos     *VideosHandler
	uploaders  *UploadersHandler
	task       *TaskHandler
	settings   *SettingsHandler
	credential *CredentialHandler
	events     *EventsHandler
	me         *MeHandler
	quickdl    *QuickDownloadHandler
	stream     *StreamHandler
	search     *SearchHandler
	notify     *NotifyHandler
	diag       *DiagHandler
	metrics    *MetricsHandler
	signReload   *SignReloadHandler
	douyinCookie  *DouyinCookieHandler
	douyinStatus  *DouyinStatusHandler
	onSyncAll     func()
}

func NewRouter(database *db.DB, dl *downloader.Downloader, downloadDir string) *Router {
	return &Router{
		dashboard:  NewDashboardHandler(database, dl, downloadDir),
		metrics:    NewMetricsHandler(dl),
		signReload: &SignReloadHandler{},
		sources:    NewSourcesHandler(database),
		videos:     NewVideosHandler(database, downloadDir),
		uploaders:  NewUploadersHandler(database, downloadDir),
		task:       NewTaskHandler(database, dl),
		settings:   NewSettingsHandler(database),
		credential: NewCredentialHandler(database),
		events:     NewEventsHandler(dl),
		me:         NewMeHandler(database),
		quickdl:    NewQuickDownloadHandler(database, dl, downloadDir),
		stream:     NewStreamHandler(database, downloadDir),
		search:     NewSearchHandler(database),
		diag:         NewDiagHandler(database),
		douyinCookie:  NewDouyinCookieHandler(database),
		douyinStatus:  NewDouyinStatusHandler(),
	}
}

// SetCallbacks 设置回调函数
func (rt *Router) SetCallbacks(
	onCheckNow func(),
	onCredentialUpdate func(*bilibili.Credential),
	onRetryDownload func(int64),
	onSyncSource func(int64),
	onProcessPending func(),
	onRefreshRate func(),
	onRedownload func(int64),
) {
	rt.task.SetCheckNowFunc(onCheckNow)
	// 包装 credential 回调：更新时同时清除 dashboard 缓存
	wrappedCredUpdate := func(cred *bilibili.Credential) {
		rt.dashboard.InvalidateCredentialCache()
		if onCredentialUpdate != nil {
			onCredentialUpdate(cred)
		}
	}
	rt.credential.SetCredentialUpdateFunc(wrappedCredUpdate)
	rt.videos.SetRetryDownloadFunc(onRetryDownload)
	rt.videos.SetProcessPendingFunc(onProcessPending)
	rt.videos.SetRedownloadFunc(onRedownload)
	rt.sources.SetSyncSourceFunc(onSyncSource)
	rt.settings.SetRefreshRateFunc(onRefreshRate)
	rt.uploaders.SetRedownloadFunc(onRedownload)
	rt.uploaders.SetProcessPendingFunc(onProcessPending)
}

// SetSyncAllFunc 设置全部同步回调
func (rt *Router) SetSyncAllFunc(fn func()) {
	rt.onSyncAll = fn
}

// SetFullScanSourceFunc 设置全量补漏扫描回调
func (rt *Router) SetFullScanSourceFunc(fn func(int64)) {
	rt.sources.SetFullScanSourceFunc(fn)
}

// SetBiliClientFunc 设置获取 bilibili client 的回调
func (rt *Router) SetBiliClientFunc(fn func() *bilibili.Client) {
	rt.me.SetBiliClientFunc(fn)
	rt.quickdl.SetBiliClientFunc(fn)
	rt.diag.SetBiliClientFunc(fn)
}

func (rt *Router) SetConfigReloadFunc(fn func()) {
	rt.settings.SetConfigReloadFunc(fn)
}

// SetDouyinCookieUpdateFunc 设置抖音 Cookie 更新回调
func (rt *Router) SetDouyinCookieUpdateFunc(fn func(string)) {
	rt.settings.SetDouyinCookieUpdateFunc(fn)
}

// SetDouyinStatusFunc 设置抖音暂停状态查询回调
func (rt *Router) SetDouyinStatusFunc(fn func() (bool, string, time.Time)) {
	rt.douyinStatus.SetStatusFunc(fn)
}

// SetDouyinResumeFunc 设置抖音恢复回调
func (rt *Router) SetDouyinResumeFunc(fn func()) {
	rt.douyinStatus.SetResumeFunc(fn)
}

func (rt *Router) SetCooldownInfoFunc(fn func() (bool, int)) {
	rt.dashboard.SetCooldownInfoFunc(fn)
	rt.metrics.SetCooldownInfoFunc(fn)
}
func (rt *Router) SetVersion(v string)         { rt.task.SetVersion(v) }
func (rt *Router) SetBuildTime(t string)        { rt.task.SetBuildTime(t) }
func (rt *Router) SetStartTime(t time.Time) {
	rt.task.SetStartTime(t)
	rt.metrics.SetStartTime(t)
}

// SetNotifier 设置通知处理器
func (rt *Router) SetNotifier(n *notify.Notifier) {
	rt.notify = NewNotifyHandler(n)
	rt.quickdl.SetNotifier(n)
}

// Register 注册新版 API 路由到 mux
func (rt *Router) Register(mux *http.ServeMux) {
	// Dashboard
	mux.HandleFunc("/api/dashboard", rt.dashboard.HandleDashboard)

	// Sources
	mux.HandleFunc("/api/sources", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			rt.sources.HandleList(w, r)
		case "POST":
			rt.sources.HandleCreate(w, r)
		default:
			apiError(w, CodeMethodNotAllow, "method not allowed")
		}
	})
	mux.HandleFunc("/api/sources/parse", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			rt.sources.HandleParse(w, r)
		} else {
			apiError(w, CodeMethodNotAllow, "method not allowed")
		}
	})
	// Sources Export/Import
	mux.HandleFunc("/api/sources/export", rt.sources.HandleExport)
	mux.HandleFunc("/api/sources/import", rt.sources.HandleImport)

	mux.HandleFunc("/api/sources/", rt.sources.HandleByID)

	// Videos
	mux.HandleFunc("/api/videos", rt.videos.HandleList)
	mux.HandleFunc("/api/videos/detect-charge", func(w http.ResponseWriter, r *http.Request) {
		rt.videos.HandleDetectCharge(w, r)
	})
	mux.HandleFunc("/api/videos/", rt.videos.HandleByID)

	// Video Stream (playback)
	mux.HandleFunc("/api/stream/", rt.stream.HandleStream)

	// Uploaders
	mux.HandleFunc("/api/uploaders/suggestions", rt.uploaders.HandleSuggestions)
	mux.HandleFunc("/api/uploaders", rt.uploaders.HandleList)
	mux.HandleFunc("/api/uploaders/", rt.uploaders.HandleByID)

	// Avatar
	mux.HandleFunc("/api/avatar/", rt.uploaders.HandleAvatar)

	// Task
	mux.HandleFunc("/api/task/status", rt.task.HandleStatus)
	mux.HandleFunc("/api/task/trigger", rt.task.HandleTrigger)
	mux.HandleFunc("/api/task/pause", rt.task.HandlePause)
	mux.HandleFunc("/api/task/resume", rt.task.HandleResume)

	// Settings
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			rt.settings.HandleGet(w, r)
		case "PUT", "POST":
			rt.settings.HandleUpdate(w, r)
		default:
			apiError(w, CodeMethodNotAllow, "method not allowed")
		}
	})

	mux.HandleFunc("/api/settings/preview-template", rt.settings.HandlePreviewTemplate)

	// Credential & Login
	mux.HandleFunc("/api/credential", rt.credential.HandleStatus)
	mux.HandleFunc("/api/credential/refresh", rt.credential.HandleRefresh)
	mux.HandleFunc("/api/login/qrcode/generate", rt.credential.HandleQRCodeGenerate)
	mux.HandleFunc("/api/login/qrcode/poll", rt.credential.HandleQRCodePoll)

	// Events (SSE) & Logs
	mux.HandleFunc("/api/events", rt.events.HandleEvents)
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			rt.events.HandleLogs(w, r)
		case "POST":
			// POST /api/logs — 清空日志 buffer
			rt.events.HandleLogsClear(w, r)
		default:
			apiError(w, CodeMethodNotAllow, "method not allowed")
		}
	})

	// Me — 关注列表 & 收藏夹
	mux.HandleFunc("/api/me/favorites", rt.me.HandleFavorites)
	mux.HandleFunc("/api/me/uppers", rt.me.HandleUppers)
	mux.HandleFunc("/api/me/subscribe", rt.me.HandleSubscribe)

	// Quick Download
	mux.HandleFunc("/api/download", rt.quickdl.HandleQuickDownload)
	mux.HandleFunc("/api/download/preview", rt.quickdl.HandlePreview)

	// Auth — token 登录
	mux.HandleFunc("/api/login/token", rt.settings.HandleTokenLogin)

	// WebSocket 日志
	mux.HandleFunc("/api/ws/logs", rt.events.HandleWSLogs)

	// Version
	mux.HandleFunc("/api/version", rt.task.HandleVersion)

	// Global Search
	mux.HandleFunc("/api/search", rt.search.HandleSearch)

	// Notify
	if rt.notify != nil {
		mux.HandleFunc("/api/notify/test", rt.notify.HandleTest)
		mux.HandleFunc("/api/notify/status", rt.notify.HandleStatus)
	}

	// Ping (health check for API layer)
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		apiOK(w, map[string]string{"status": "pong"})
	})

	// Metrics
	mux.HandleFunc("/api/metrics", rt.metrics.HandleMetrics)
	mux.HandleFunc("/api/metrics/prometheus", rt.metrics.HandlePrometheus)

	// Sign Reload
	mux.HandleFunc("/api/sign/reload", rt.signReload.HandleReload)

	// Douyin Cookie Management
	mux.HandleFunc("/api/douyin/cookie/validate", rt.douyinCookie.HandleValidate)
	mux.HandleFunc("/api/douyin/cookie/status", rt.douyinCookie.HandleStatus)

	// Douyin Status (pause/resume)
	mux.HandleFunc("/api/douyin/status", rt.douyinStatus.HandleStatus)
	mux.HandleFunc("/api/douyin/resume", rt.douyinStatus.HandleResume)

	// Diagnostics
	mux.HandleFunc("/api/diag/bili", rt.diag.HandleBili)
	mux.HandleFunc("/api/diag/douyin", rt.diag.HandleDouyin)
}
