package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"net/http"
	_ "net/http/pprof"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/douyin"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/logger"
	"video-subscribe-dl/internal/scanner"
	"video-subscribe-dl/internal/scheduler"
	"video-subscribe-dl/internal/scheduler/phscheduler"
	"video-subscribe-dl/web"
)

var version = "v2.21.0"
var buildTime = "unknown"
var startTime = time.Now()

func main() {
	fmt.Printf("🎬 Video Subscribe DL %s\n", version)

	dataDir := flag.String("data-dir", "./data", "Database directory")
	downloadDir := flag.String("download-dir", "./data/downloads", "Download directory")
	port := flag.Int("port", 8080, "Web UI port")
	flag.Parse()

	// Initialize ring buffer logger (1000 entries)
	appLogger := logger.Init(config.LogRingBufferSize)
	logWriter := appLogger.Writer()
	log.SetOutput(logWriter)
	log.SetFlags(log.Ldate | log.Ltime)
	// slog 也走同一个 writer，避免 slog 直接写 stdout 导致日志重复推送到 SSE
	slog.SetDefault(slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	os.MkdirAll(*dataDir, 0755)
	os.MkdirAll(*downloadDir, 0755)

	database, err := db.Init(*dataDir)
	if err != nil {
		log.Fatalf("DB init: %v", err)
	}
	defer database.Close()

	// 优先从 DB 加载 Credential（新鉴权模式）
	var biliClient *bilibili.Client
	credJSON, _ := database.GetSetting("credential_json")
	cred := bilibili.CredentialFromJSON(credJSON)
	if cred != nil && !cred.IsEmpty() {
		biliClient = bilibili.NewClientWithCredential(cred)
		credSource, _ := database.GetSetting("credential_source")
		log.Printf("Loaded credential from DB (source: %s, user: %s)", credSource, cred.DedeUserID)
	} else {
		// Fallback: 从 cookie 文件加载
		cookiePath, _ := database.GetSetting("cookie_path")
		cookie := bilibili.ReadCookieFile(cookiePath)
		biliClient = bilibili.NewClient(cookie)
		if cookie != "" {
			log.Printf("Loaded cookie from file: %s", cookiePath)
			// 自动转换为 Credential 存 DB
			fileCred := bilibili.CredentialFromCookieFile(cookiePath)
			if fileCred != nil {
				// P1-11: check SetSetting errors so silent save failures are visible in logs
				if err := database.SetSetting("credential_json", fileCred.ToJSON()); err != nil {
					log.Printf("save credential_json failed: %v", err)
				}
				if err := database.SetSetting("credential_source", "cookie_file"); err != nil {
					log.Printf("save credential_source failed: %v", err)
				}
				biliClient = bilibili.NewClientWithCredential(fileCred)
				log.Printf("Cookie file auto-converted to Credential")
			}
		} else {
			log.Printf("No credential or cookie configured - downloads limited to 480p")
		}
	}
	cookiePath, _ := database.GetSetting("cookie_path")

	requestInterval := config.DefaultRequestInterval
	if v, err := database.GetSetting("request_interval_sec"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			requestInterval = n
		}
	}
	dl := downloader.New(downloader.Config{
		MaxConcurrent:   config.DefaultDownloadWorkers,
		RequestInterval: requestInterval,
	}, biliClient)

	// Apply download speed limit from settings
	if speedStr, err := database.GetSetting("max_download_speed_mb"); err == nil && speedStr != "" {
		if speedMB, err := strconv.ParseFloat(speedStr, 64); err == nil && speedMB > 0 {
			dl.SetRateLimit(int64(speedMB * 1024 * 1024))
			log.Printf("Download speed limit: %.1f MB/s", speedMB)
		}

		// Apply download chunks setting
		if chunksStr, err := database.GetSetting("download_chunks"); err == nil && chunksStr != "" {
			if chunks, err := strconv.Atoi(chunksStr); err == nil && chunks > 0 {
				dl.SetDownloadChunks(chunks)
				log.Printf("Download chunks: %d", chunks)
			}
		}
	}

	sc := scanner.New(database, *downloadDir)

	// 启动时轻量对账（只检查不修复），并自动重置 stale downloading
	log.Println("Running startup reconciliation check...")
	if reconcileResult, err := sc.Reconcile(); err != nil {
		log.Printf("Startup reconcile error: %v", err)
	} else if reconcileResult.IsConsistent {
		log.Printf("Reconcile OK: DB(%d records) and local(%d files) are consistent",
			reconcileResult.TotalDBRecords, reconcileResult.TotalLocalFiles)
	} else {
		log.Printf("Reconcile MISMATCH: orphan_files=%d, missing_files=%d, stale_downloading=%d",
			reconcileResult.OrphanCount, reconcileResult.MissingCount, reconcileResult.StaleCount)
		log.Println("Run POST /api/scan/fix or use the UI to fix inconsistencies")
		// 自动重置 stale downloading 状态
		if len(reconcileResult.StaleDownloading) > 0 {
			for _, id := range reconcileResult.StaleDownloading {
				database.ResetDownloadToPending(id)
				log.Printf("Auto-reset stale download #%d to pending", id)
			}
		}
		// 自动标记文件已迁移（DB completed 但本地文件不存在 → relocated）
		// 只清空 file_path，保留去重有效性，不触发重新下载
		if reconcileResult.MissingCount > 0 {
			log.Printf("Auto-marking %d missing files as relocated...", reconcileResult.MissingCount)
			for _, videoID := range reconcileResult.MissingFiles {
				if err := database.MarkVideoRelocated(videoID); err != nil {
					log.Printf("Auto-relocate failed for %s: %v", videoID, err)
				} else {
					log.Printf("Auto-marked relocated: %s", videoID)
				}
			}
		}
	}

	// 配置签名算法热更新（从 DB 读取远端 URL，空则使用内置版本）
	signJSURL, _ := database.GetSetting("sign_js_url")
	abogusJSURL, _ := database.GetSetting("abogus_js_url")
	signUpdater := douyin.GetSignUpdater()
	signUpdater.Configure(signJSURL, abogusJSURL)
	if signJSURL != "" || abogusJSURL != "" {
		if updated, err := signUpdater.CheckAndUpdate(); err != nil {
			log.Printf("[sign-updater] 启动时检查远端签名脚本失败: %v", err)
		} else if updated {
			log.Println("[sign-updater] 签名脚本已从远端更新")
		}
		// 启动定时自动检查（每 6 小时）
		signUpdater.StartAutoUpdate(6 * time.Hour)
	}

	sched := scheduler.New(database, dl, *downloadDir, cookiePath)
	// 如果有 Credential，立即同步给 scheduler（覆盖 New() 中的 cookie-based client）
	if cred != nil && !cred.IsEmpty() {
		sched.UpdateCredential(cred)
	}
	// 一次性启动清理（必须在 Start() 之前：修复存量数据 + 清理非法字符目录）
	sched.StartupCleanup()

	sched.Start()

	server := web.NewServer(database, dl, sc, *port, *dataDir, *downloadDir)
	server.SetCooldownInfoFunc(sched.GetCooldownInfo)
	server.SetPHCooldownInfoFunc(sched.GetPHCooldownInfo)
	server.SetCheckNowFunc(sched.CheckNow)
	server.SetCookieUpdateFunc(sched.UpdateCookie)
	server.SetCredentialUpdateFunc(sched.UpdateCredential)
	server.SetRetryDownloadFunc(sched.RetryByID)
	server.SetSyncAllFunc(sched.SyncAll)
	server.SetSyncSourceFunc(sched.CheckOneSource)
	server.SetFullScanSourceFunc(sched.FullScanSource)
	server.SetProcessPendingFunc(sched.ProcessAllPending)
	server.SetRedownloadFunc(sched.RedownloadByID)
	server.SetNotifier(sched.GetNotifier())
	server.SetBiliClientFunc(sched.GetBiliClient)
	server.SetConfigReloadFunc(sched.ReloadConfig)
	server.SetDouyinCookieUpdateFunc(sched.RefreshDouyinUserCookie)
	server.SetDouyinPauseStatusFunc(sched.GetDouyinPauseStatus)
	server.SetDouyinResumeFunc(sched.ResumeDouyin)
	server.SetDouyinPauseFunc(sched.PauseDouyin)
	server.SetBiliResumeFunc(sched.ResumeBili)
	server.SetDouyinCookieStatusFunc(sched.GetDouyinCookieStatus)
	server.SetPHCookieUpdateFunc(sched.RefreshPHUserCookie)
	server.SetPHPauseStatusFunc(sched.GetPHPauseStatus)
	server.SetPHResumeFunc(sched.ResumePH)
	server.SetPHPauseFunc(sched.PausePH)
	server.SetPHCookieStatusFunc(sched.GetPHCookieStatus)
	server.SetXCPauseStatusFunc(sched.GetXCPauseStatus)
	server.SetXCResumeFunc(sched.ResumeXC)
	server.SetXCPauseFunc(sched.PauseXC)
	server.SetRepairThumbFunc(phscheduler.CaptureThumbFromVideo)
	server.SetSchedulerLastCheckFunc("bilibili", sched.GetBiliLastCheckAt)
	server.SetSchedulerLastCheckFunc("douyin", sched.GetDouyinLastCheckAt)
	server.SetSchedulerLastCheckFunc("pornhub", sched.GetPHLastCheckAt)
	server.SetSchedulerLastCheckFunc("xchina", sched.GetXCLastCheckAt)
	server.SetVersion(version)
	server.SetStartTime(startTime)
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			log.Printf("Web server: %v", err)
		}
	}()

	// pprof 调试服务：仅当 PPROF_ADDR 环境变量设置时启动
	// 默认不启动，防止生产环境意外暴露调试接口
	if pprofAddr := os.Getenv("PPROF_ADDR"); pprofAddr != "" {
		go func() {
			log.Printf("pprof debug server listening on http://%s/debug/pprof/", pprofAddr)
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				log.Printf("pprof server error: %v", err)
			}
		}()
	}

	log.Println("Application started successfully")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	received := <-sig
	log.Printf("收到退出信号 (%v)，等待进行中的下载完成...", received)

	// Graceful shutdown: scheduler → downloader → web server → DB
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// 0. 停止签名自动更新
	signUpdater.StopAutoUpdate()

	// 1. 停止调度器（不再提交新任务）
	log.Println("[shutdown] 停止调度器...")
	sched.Stop()

	// 2. 停止下载器（取消进行中的下载，关闭 worker）
	log.Println("[shutdown] 停止下载器...")
	dl.Stop()

	// 3. 关闭 HTTP 服务
	log.Println("[shutdown] 关闭 HTTP 服务...")
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("[shutdown] Web server shutdown error: %v", err)
	}

	// 4. 关闭数据库（defer 已处理，这里显式记录）
	log.Println("Shutdown complete")
}
