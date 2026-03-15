package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"net/http"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/downloader"
	"video-subscribe-dl/internal/logger"
	"video-subscribe-dl/internal/scanner"
	"video-subscribe-dl/internal/scheduler"
	"video-subscribe-dl/web"
)

var version = "v2.5.0"
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
	log.SetOutput(appLogger.Writer())
	log.SetFlags(log.Ldate | log.Ltime)

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
				database.SetSetting("credential_json", fileCred.ToJSON())
				database.SetSetting("credential_source", "cookie_file")
				biliClient = bilibili.NewClientWithCredential(fileCred)
				log.Printf("Cookie file auto-converted to Credential")
			}
		} else {
			log.Printf("No credential or cookie configured - downloads limited to 480p")
		}
	}
	cookiePath, _ := database.GetSetting("cookie_path")

	dl := downloader.New(downloader.Config{
		MaxConcurrent:   config.DefaultDownloadWorkers,
		RequestInterval: config.DefaultRequestInterval,
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
		if reconcileResult.StaleCount > 0 {
			for _, id := range reconcileResult.StaleDownloading {
				database.ResetDownloadToPending(id)
				log.Printf("Auto-reset stale download #%d to pending", id)
			}
		}
	}

	sched := scheduler.New(database, dl, *downloadDir, cookiePath)
	// 如果有 Credential，立即同步给 scheduler（覆盖 New() 中的 cookie-based client）
	if cred != nil && !cred.IsEmpty() {
		sched.UpdateCredential(cred)
	}
	sched.Start()

	// 一次性启动清理：扫描非法字符目录 + 重置全量扫描
	sched.StartupCleanup()

	server := web.NewServer(database, dl, sc, *port, *dataDir, *downloadDir)
	server.SetCooldownInfoFunc(sched.GetCooldownInfo)
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
	server.SetVersion(version)
	server.SetStartTime(startTime)
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			log.Printf("Web server: %v", err)
		}
	}()

	log.Println("Application started successfully")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("Shutting down...")

	// Graceful shutdown: web server → scheduler → wait
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Web server shutdown error: %v", err)
	}
	sched.Stop()
	log.Println("Shutdown complete")
}
