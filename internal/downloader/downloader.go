package downloader

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"video-subscribe-dl/internal/bilibili"
	appconfig "video-subscribe-dl/internal/config"
	"video-subscribe-dl/internal/danmaku"
)

// Retry constants
const (
	MaxRetries  = 3
	RetryDelay1 = 5 * time.Second
	RetryDelay2 = 15 * time.Second
	RetryDelay3 = 30 * time.Second
)

type Config struct {
	MaxConcurrent   int
	RequestInterval int // 秒
}

// ProgressInfo 描述一个下载任务的实时进度
type ProgressInfo struct {
	BvID       string  `json:"bvid"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`     // "downloading_video", "downloading_audio", "merging", "done", "error"
	Phase      string  `json:"phase"`      // "video", "audio", "merge"
	Percent    float64 `json:"percent"`    // 0-100
	Speed      int64   `json:"speed"`      // bytes/sec
	Downloaded int64   `json:"downloaded"` // bytes
	Total      int64   `json:"total"`      // bytes
}

type Downloader struct {
	config         Config
	bili           *bilibili.Client
	queue          chan *Job
	paused         bool
	pauseCh        chan struct{}
	resumeCh       chan struct{}
	activeJobs     int64
	mu             sync.Mutex
	rateLimitBps   int64 // bytes per second, 0 = unlimited
	downloadChunks int   // parallel chunks for large files, 0 = use default (4)

	// 优雅关闭
	rootCtx    context.Context
	rootCancel context.CancelFunc

	// 进度追踪
	progressMu sync.RWMutex
	progress   map[string]*ProgressInfo // key = bvid
}

type Job struct {
	BvID        string // 直接用 BV 号
	CID         int64  // 视频 CID
	Title       string
	OutputDir   string
	Quality     string // "best", "1080p", "720p"
	QualityMin  string // 最低画质: "480p", "720p", "1080p"
	Codec       string // "avc", "hevc", "av1", ""
	Danmaku     bool
	Flat        bool   // Flat 模式: 直接用 OutputDir 输出，不创建子目录
	Subtitle    bool   // 是否下载字幕
	SkipNFO     bool   // 跳过 NFO 生成
	SkipPoster       bool   // 跳过封面下载
	UploaderName     string // UP 主名称（用于文件名模板）
	PubDate          string // 发布日期（YYYY-MM-DD，用于文件名模板）
	PartIndex        int    // 分P 索引
	PartTitle        string // 分P 标题
	FilenameTemplate string // 文件名模板（空则使用默认）
	CookiesFile      string
	ResultCh    chan *Result
	OnStart     func() // 开始下载时回调（用于更新 DB 状态）
}

type Result struct {
	Success      bool
	FilePath     string
	FileSize     int64
	Error        error
	DanmakuDone  bool // 弹幕是否下载成功
	SubtitleDone bool // 字幕是否下载成功
}

func New(config Config, biliClient *bilibili.Client) *Downloader {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Downloader{
		config:     config,
		bili:       biliClient,
		queue:      make(chan *Job, appconfig.DefaultQueueSize),
		pauseCh:    make(chan struct{}),
		resumeCh:   make(chan struct{}),
		progress:   make(map[string]*ProgressInfo),
		rootCtx:    ctx,
		rootCancel: cancel,
	}
	for i := 0; i < config.MaxConcurrent; i++ {
		go d.worker(i)
	}
	return d
}

func (d *Downloader) UpdateClient(client *bilibili.Client) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bili = client
}

func (d *Downloader) worker(id int) {
	for job := range d.queue {
		d.processOneJob(id, job)
	}
}

// processOneJob 处理单个下载任务，内含 recover 保护防止 panic 导致 worker 退出
func (d *Downloader) processOneJob(id int, job *Job) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[w%d] PANIC recovered in worker: %v", id, r)
			if job.ResultCh != nil {
				job.ResultCh <- &Result{Error: fmt.Errorf("worker panic: %v", r)}
			}
		}
	}()

	d.mu.Lock()
	paused := d.paused
	d.mu.Unlock()
	if paused {
		<-d.resumeCh
	}

	log.Printf("[w%d] Downloading: %s (%s)", id, job.Title, job.BvID)
	if job.OnStart != nil {
		job.OnStart()
	}
	result := d.downloadWithRetry(job)
	if job.ResultCh != nil {
		job.ResultCh <- result
	}

	// 防封延迟
	jitter := time.Duration(rand.Intn(10)) * time.Second
	delay := time.Duration(d.config.RequestInterval)*time.Second + jitter
	time.Sleep(delay)
}

func (d *Downloader) Submit(job *Job) error {
	select {
	case d.queue <- job:
		return nil
	case <-time.After(5 * time.Second):
		log.Printf("[WARN] Download queue is full, failed to submit job: %s (%s)", job.Title, job.BvID)
		return fmt.Errorf("download queue is full, could not submit %s within 5s timeout", job.BvID)
	}
}

func (d *Downloader) Pause() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.paused {
		d.paused = true
		d.pauseCh = make(chan struct{})
	}
}

// Stop 优雅关闭下载器：取消所有进行中的下载任务
func (d *Downloader) Stop() {
	d.rootCancel()
	close(d.queue) // 关闭队列让 worker 退出
}

func (d *Downloader) Resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.paused {
		d.paused = false
		close(d.resumeCh)
		d.resumeCh = make(chan struct{})
	}
}

func (d *Downloader) IsPaused() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.paused
}

// SetRateLimit sets the download speed limit in bytes per second (0 = unlimited)
func (d *Downloader) SetRateLimit(bytesPerSec int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rateLimitBps = bytesPerSec
}

// GetRateLimit returns the current rate limit in bytes per second
func (d *Downloader) GetRateLimit() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rateLimitBps
}

// SetDownloadChunks sets the number of parallel chunks for large file downloads
func (d *Downloader) SetDownloadChunks(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.downloadChunks = n
}

// GetDownloadChunks returns the current chunk count
func (d *Downloader) GetDownloadChunks() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.downloadChunks <= 0 {
		return bilibili.DefaultChunks
	}
	return d.downloadChunks
}

// GetProgress 返回当前所有活跃下载的进度快照
func (d *Downloader) QueueLen() int { return len(d.queue) }

func (d *Downloader) ActiveCount() int { return int(atomic.LoadInt64(&d.activeJobs)) }

func (d *Downloader) IsBusy() bool { return d.QueueLen() > 0 || d.ActiveCount() > 0 }

func (d *Downloader) GetProgress() []ProgressInfo {
	d.progressMu.RLock()
	defer d.progressMu.RUnlock()
	result := make([]ProgressInfo, 0, len(d.progress))
	for _, p := range d.progress {
		result = append(result, *p)
	}
	return result
}

// setProgress 更新某个 bvid 的下载进度
func (d *Downloader) setProgress(bvid string, info *ProgressInfo) {
	d.progressMu.Lock()
	defer d.progressMu.Unlock()
	d.progress[bvid] = info
}

// removeProgress 移除已完成的进度记录
func (d *Downloader) removeProgress(bvid string) {
	d.progressMu.Lock()
	defer d.progressMu.Unlock()
	delete(d.progress, bvid)
}

// isRetryableError checks if the error is retryable (network errors yes, 403/404 no)
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// 风控错误不重试，由 scheduler 层面统一处理冷却
	if bilibili.IsRiskControl(err) {
		return false
	}
	errStr := err.Error()
	// Non-retryable: resource not found or permission denied
	if strings.Contains(errStr, "HTTP 403") || strings.Contains(errStr, "HTTP 404") ||
		strings.Contains(errStr, "HTTP 410") || strings.Contains(errStr, "HTTP 451") {
		return false
	}
	// Non-retryable: no streams available (content issue, not network)
	if strings.Contains(errStr, "no video streams") || strings.Contains(errStr, "no audio streams") ||
		strings.Contains(errStr, "no suitable video stream") {
		return false
	}
	// Everything else is retryable (network errors, timeouts, 5xx, etc.)
	return true
}

// retryDelay returns the delay for a given retry attempt (1-indexed)
func retryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return RetryDelay1
	case 2:
		return RetryDelay2
	case 3:
		return RetryDelay3
	default:
		return RetryDelay3
	}
}

// downloadWithRetry wraps download with automatic retry logic
func (d *Downloader) downloadWithRetry(job *Job) *Result {
	result := d.download(job)
	if result.Success || result.Error == nil {
		return result
	}

	// 风控错误：暂停下载队列，不重试
	if bilibili.IsRiskControl(result.Error) {
		log.Printf("[downloader] 风控错误，暂停下载队列: %s", job.BvID)
		d.Pause()
		return result
	}

	// Check if error is retryable
	if !isRetryableError(result.Error) {
		log.Printf("[retry] Non-retryable error for %s: %v", job.BvID, result.Error)
		return result
	}

	// Retry with exponential backoff
	for attempt := 1; attempt <= MaxRetries; attempt++ {
		delay := retryDelay(attempt)
		log.Printf("[retry] Attempt %d/%d for %s (waiting %v): %v", attempt, MaxRetries, job.BvID, delay, result.Error)
		time.Sleep(delay)

		result = d.download(job)
		if result.Success || result.Error == nil {
			log.Printf("[retry] Success on attempt %d for %s", attempt, job.BvID)
			return result
		}

		if !isRetryableError(result.Error) {
			log.Printf("[retry] Non-retryable error on attempt %d for %s: %v", attempt, job.BvID, result.Error)
			return result
		}
	}

	log.Printf("[retry] All %d retries exhausted for %s: %v", MaxRetries, job.BvID, result.Error)
	return result
}

func (d *Downloader) download(job *Job) *Result {
	d.mu.Lock()
	client := d.bili
	d.mu.Unlock()

	// 初始化进度
	prog := &ProgressInfo{
		BvID:   job.BvID,
		Title:  job.Title,
		Status: "downloading_video",
		Phase:  "video",
	}
	d.setProgress(job.BvID, prog)
	defer d.removeProgress(job.BvID)

	// 如果 job 有独立 cookie，用独立的 client
	if job.CookiesFile != "" {
		cookie := bilibili.ReadCookieFile(job.CookiesFile)
		if cookie != "" {
			client = bilibili.NewClient(cookie)
		}
	}

	// 1. 获取 DASH 流
	dash, err := client.GetDashStreams(job.BvID, job.CID)
	if err != nil {
		prog.Status = "error"
		d.setProgress(job.BvID, prog)
		return &Result{Error: fmt.Errorf("get dash streams: %w", err)}
	}

	if len(dash.Video) == 0 {
		prog.Status = "error"
		d.setProgress(job.BvID, prog)
		return &Result{Error: fmt.Errorf("no video streams available")}
	}

	// 2. 选择最优视频流
	maxHeight := 0
	switch job.Quality {
	case "1080p":
		maxHeight = 1080
	case "720p":
		maxHeight = 720
	case "480p":
		maxHeight = 480
	}
	bestVideo := bilibili.SelectBestVideo(dash.Video, job.Codec, maxHeight)
	if bestVideo == nil {
		prog.Status = "error"
		d.setProgress(job.BvID, prog)
		return &Result{Error: fmt.Errorf("no suitable video stream")}
	}

	// 最低画质检查: 低于要求则跳过不下载
	if job.QualityMin != "" {
		minHeight := 0
		switch job.QualityMin {
		case "1080p":
			minHeight = 1080
		case "720p":
			minHeight = 720
		case "480p":
			minHeight = 480
		}
		if minHeight > 0 && bestVideo.Height < minHeight {
			prog.Status = "skipped"
			d.setProgress(job.BvID, prog)
			return &Result{Error: fmt.Errorf("quality too low: %dp < %s minimum", bestVideo.Height, job.QualityMin)}
		}
	}

	log.Printf("  Video: %s", bilibili.FormatVideoInfo(bestVideo))

	// 3. 选择最优音频流
	if len(dash.Audio) > 0 {
		log.Printf("  Available audio streams (%d):", len(dash.Audio))
		for i := range dash.Audio {
			log.Printf("    - %s", bilibili.FormatAudioInfo(&dash.Audio[i]))
		}
	}
	bestAudio := bilibili.SelectBestAudio(dash.Audio)
	if bestAudio == nil {
		prog.Status = "error"
		d.setProgress(job.BvID, prog)
		return &Result{Error: fmt.Errorf("no audio streams available")}
	}
	if bestAudio.ID == bilibili.Audio64K || bestAudio.Bandwidth < 100000 {
		log.Printf("  ⚠️  Low audio quality (%s) — consider configuring a valid cookie for better quality", bilibili.FormatAudioInfo(bestAudio))
	}
	log.Printf("  Selected audio: %s", bilibili.FormatAudioInfo(bestAudio))

	// 4. 构建输出路径
	var videoDir, safeName string
	if job.Flat {
		// Flat 模式: 直接使用 OutputDir（多P视频的 Season 1 目录）
		videoDir = job.OutputDir
		safeName = bilibili.SanitizeFilename(job.Title)
	} else {
		// 使用文件名模板生成目录名
		vars := appconfig.FilenameVars{
			Title:        job.Title,
			BvID:         job.BvID,
			UploaderName: job.UploaderName,
			Quality:      bilibili.QualityName(bestVideo.ID),
			Codec:        bestVideo.Codecs,
			PartIndex:    job.PartIndex,
			PartTitle:    job.PartTitle,
			PubDate:      job.PubDate,
		}
		safeName = appconfig.RenderFilename(job.FilenameTemplate, vars)
		videoDir = filepath.Join(job.OutputDir, safeName)
	}
	os.MkdirAll(videoDir, 0755)

	// 5. 构造进度回调
	progressCb := func(phase string, downloaded, total int64, speed float64) {
		prog.Phase = phase
		prog.Downloaded = downloaded
		prog.Total = total
		prog.Speed = int64(speed)
		if phase == "video" {
			prog.Status = "downloading_video"
		} else if phase == "audio" {
			prog.Status = "downloading_audio"
		}
		if total > 0 {
			prog.Percent = float64(downloaded) / float64(total) * 100
		}
		d.setProgress(job.BvID, prog)
	}

	d.mu.Lock()
	currentRateLimit := d.rateLimitBps
	d.mu.Unlock()
	// 6. 下载并合并（带进度回调 + 限速 + 分块并行）
	chunks := d.GetDownloadChunks()
	dlTimeout := calculateDownloadTimeout(int64(bestVideo.Bandwidth+bestAudio.Bandwidth), currentRateLimit)
	// 使用 rootCtx 感知优雅关闭信号
	log.Printf("  Download timeout: %v (bitrate=%dkbps, rateLimit=%d)", dlTimeout, (bestVideo.Bandwidth+bestAudio.Bandwidth)/1000, currentRateLimit)
	downloadCtx, downloadCancel := context.WithTimeout(d.rootCtx, dlTimeout)
	defer downloadCancel()
	outputPath, err := bilibili.DownloadDashWithProgressChunked(downloadCtx, bestVideo, bestAudio, videoDir, safeName, progressCb, currentRateLimit, chunks)
	if err != nil {
		prog.Status = "error"
		d.setProgress(job.BvID, prog)
		return &Result{Error: fmt.Errorf("download dash: %w", err)}
	}

	// 获取文件大小
	var fileSize int64
	if fi, err := os.Stat(outputPath); err == nil {
		fileSize = fi.Size()
	}

	danmakuDone := false
	subtitleDone := false

	// 7. 弹幕下载（如果启用）
	if job.Danmaku && job.CID > 0 {
		log.Printf("  Downloading danmaku for cid=%d...", job.CID)
		ext := filepath.Ext(outputPath)
		baseName := strings.TrimSuffix(filepath.Base(outputPath), ext)
		xmlPath := filepath.Join(videoDir, baseName+".danmaku.xml")
		// 使用 bili-sync 风格命名: .zh-CN.default.ass
		assPath := filepath.Join(videoDir, baseName+".zh-CN.default.ass")

		if err := danmaku.DownloadDanmakuXML(job.CID, xmlPath); err != nil {
			log.Printf("  Danmaku download failed: %v", err)
		} else {
			log.Printf("  Danmaku XML saved: %s", xmlPath)
			if err := danmaku.XMLToASS(xmlPath, assPath, 1920, 1080); err != nil {
				log.Printf("  Danmaku XML->ASS failed: %v", err)
			} else {
				log.Printf("  Danmaku ASS saved: %s", assPath)
				// 删除中间 XML 文件，只保留 ASS
				os.Remove(xmlPath)
				danmakuDone = true
			}
		}
	}

	// 8. 字幕下载（如果启用）
	if job.Subtitle && job.CID > 0 {
		log.Printf("  Fetching subtitles for %s (cid=%d)...", job.BvID, job.CID)
		subs, err := client.GetSubtitles(job.BvID, job.CID)
		if err != nil {
			log.Printf("  Subtitle list fetch failed: %v", err)
		} else if len(subs) > 0 {
			ext := filepath.Ext(outputPath)
			baseName := strings.TrimSuffix(filepath.Base(outputPath), ext)
			bilibili.DownloadSubtitleAsSRT(subs, videoDir, baseName)
			subtitleDone = true
		} else {
			log.Printf("  No subtitles available")
		}
	}

	prog.Status = "done"
	prog.Percent = 100
	d.setProgress(job.BvID, prog)

	// 验证文件确实存在且非空
	if fi, statErr := os.Stat(outputPath); statErr != nil || fi.Size() == 0 {
		log.Printf("  Output file missing or empty after download: %s", outputPath)
		return &Result{Error: fmt.Errorf("output file missing or empty: %s", outputPath)}
	}

	log.Printf("  Done: %s (%.1f MB)", outputPath, float64(fileSize)/1024/1024)
	return &Result{
		Success:      true,
		FilePath:     outputPath,
		FileSize:     fileSize,
		DanmakuDone:  danmakuDone,
		SubtitleDone: subtitleDone,
	}
}

// calculateDownloadTimeout 根据码率和限速动态计算下载超时
// 公式: estimatedSize / effectiveSpeed * safetyMultiplier + basePadding
// 范围: [30min, 4h]
func calculateDownloadTimeout(totalBitrateBps int64, rateLimitBps int64) time.Duration {
	const (
		minTimeout      = 30 * time.Minute
		maxTimeout      = 4 * time.Hour
		basePadding     = 10 * time.Minute
		safetyMultiply  = 3.0
		defaultDuration = 1 * time.Hour // 无限速时默认
	)

	if rateLimitBps <= 0 {
		return defaultDuration
	}

	// 估算 30 分钟视频的文件大小 (bytes)
	estimatedSize := float64(totalBitrateBps) / 8.0 * 1800.0 // 30min 视频
	downloadTime := estimatedSize / float64(rateLimitBps) * safetyMultiply
	timeout := time.Duration(downloadTime)*time.Second + basePadding

	if timeout < minTimeout {
		timeout = minTimeout
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	return timeout
}
