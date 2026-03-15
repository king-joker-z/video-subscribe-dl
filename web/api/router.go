package api

import (
	"net/http"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
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
}

func NewRouter(database *db.DB, dl *downloader.Downloader, downloadDir string) *Router {
	return &Router{
		dashboard:  NewDashboardHandler(database, dl, downloadDir),
		sources:    NewSourcesHandler(database),
		videos:     NewVideosHandler(database, downloadDir),
		uploaders:  NewUploadersHandler(database),
		task:       NewTaskHandler(database, dl),
		settings:   NewSettingsHandler(database),
		credential: NewCredentialHandler(database),
		events:     NewEventsHandler(dl),
		me:         NewMeHandler(database),
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
	rt.credential.SetCredentialUpdateFunc(onCredentialUpdate)
	rt.videos.SetRetryDownloadFunc(onRetryDownload)
	rt.videos.SetProcessPendingFunc(onProcessPending)
	rt.videos.SetRedownloadFunc(onRedownload)
	rt.sources.SetSyncSourceFunc(onSyncSource)
	rt.settings.SetRefreshRateFunc(onRefreshRate)
	rt.uploaders.SetRedownloadFunc(onRedownload)
	rt.uploaders.SetProcessPendingFunc(onProcessPending)
}

// SetBiliClientFunc 设置获取 bilibili client 的回调
func (rt *Router) SetBiliClientFunc(fn func() *bilibili.Client) {
	rt.me.SetBiliClientFunc(fn)
}

func (rt *Router) SetConfigReloadFunc(fn func()) {
	rt.settings.SetConfigReloadFunc(fn)
}

func (rt *Router) SetCooldownInfoFunc(fn func() (bool, int)) { rt.dashboard.SetCooldownInfoFunc(fn) }
func (rt *Router) SetVersion(v string)         { rt.task.SetVersion(v) }
func (rt *Router) SetBuildTime(t string)        { rt.task.SetBuildTime(t) }
func (rt *Router) SetStartTime(t time.Time)     { rt.task.SetStartTime(t) }

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
	mux.HandleFunc("/api/sources/", rt.sources.HandleByID)

	// Videos
	mux.HandleFunc("/api/videos", rt.videos.HandleList)
	mux.HandleFunc("/api/videos/detect-charge", func(w http.ResponseWriter, r *http.Request) {
		rt.videos.HandleDetectCharge(w, r)
	})
	mux.HandleFunc("/api/videos/", rt.videos.HandleByID)
	mux.HandleFunc("/api/thumb/", rt.videos.HandleThumb)

	// Uploaders
	mux.HandleFunc("/api/uploaders", rt.uploaders.HandleList)
	mux.HandleFunc("/api/uploaders/", rt.uploaders.HandleByID)

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

	// Auth — token 登录
	mux.HandleFunc("/api/login/token", rt.settings.HandleTokenLogin)

	// WebSocket 日志
	mux.HandleFunc("/api/ws/logs", rt.events.HandleWSLogs)

	// Version
	mux.HandleFunc("/api/version", rt.task.HandleVersion)
}
