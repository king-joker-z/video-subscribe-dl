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
) {
	rt.task.SetCheckNowFunc(onCheckNow)
	rt.credential.SetCredentialUpdateFunc(onCredentialUpdate)
	rt.videos.SetRetryDownloadFunc(onRetryDownload)
	rt.videos.SetProcessPendingFunc(onProcessPending)
	rt.sources.SetSyncSourceFunc(onSyncSource)
	rt.settings.SetRefreshRateFunc(onRefreshRate)
}

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

	// Credential & Login
	mux.HandleFunc("/api/credential", rt.credential.HandleStatus)
	mux.HandleFunc("/api/credential/refresh", rt.credential.HandleRefresh)
	mux.HandleFunc("/api/login/qrcode/generate", rt.credential.HandleQRCodeGenerate)
	mux.HandleFunc("/api/login/qrcode/poll", rt.credential.HandleQRCodePoll)

	// Events (SSE) & Logs
	mux.HandleFunc("/api/events", rt.events.HandleEvents)
	mux.HandleFunc("/api/logs", rt.events.HandleLogs)

	// Version
	mux.HandleFunc("/api/version", rt.task.HandleVersion)
}
