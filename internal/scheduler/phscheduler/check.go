package phscheduler

import (
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/pornhub"
)

// CheckPHModel 检查 Pornhub 博主的新视频（增量扫描）
func (s *PHScheduler) CheckPHModel(src db.Source) {
	if s.IsPaused() {
		log.Printf("[phscheduler] PH 已暂停，跳过检查 %s", src.Name)
		return
	}

	if s.IsInCooldown() {
		log.Printf("[phscheduler] PH 冷却中，跳过检查 %s", src.Name)
		return
	}

	client := s.newClient()
	defer client.Close()

	// 设置 Cookie
	if s.getCookie() != "" {
		client.SetCookie(s.getCookie())
	}

	// 首次扫描时获取博主详情更新名称
	if src.Name == "" || src.Name == "未命名" {
		if info, err := client.GetModelInfo(src.URL); err == nil && info.Name != "" {
			src.Name = info.Name
			s.db.UpdateSource(&src)
			log.Printf("[phscheduler] 博主信息更新: %s", info.Name)
		} else if err != nil {
			log.Printf("[phscheduler] 获取博主信息失败（不影响视频检查）: %v", err)
		}
	}

	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	if isFirstScan {
		log.Printf("[phscheduler·首次全量] %s: 开始全量扫描 url=%s", src.Name, src.URL)
	} else {
		log.Printf("[phscheduler·增量] %s: 基准时间 %s url=%s",
			src.Name, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"), src.URL)
	}

	// 获取视频列表（包含翻页）
	var videos []pornhub.Video
	var fetchErr error
	for attempt := 1; attempt <= 3; attempt++ {
		videos, fetchErr = client.GetModelVideos(src.URL)
		if fetchErr == nil {
			break
		}
		errMsg := fetchErr.Error()

		isRateLimit := pornhub.IsRateLimit(fetchErr) ||
			strings.Contains(errMsg, "429") ||
			strings.Contains(errMsg, "503")

		if isRateLimit {
			log.Printf("[phscheduler] ⚠️ 被限流 (第%d次): %v", attempt, fetchErr)
			s.TriggerCooldown()
			if s.notifier != nil {
				s.notifier.Send(notify.EventRateLimited, "Pornhub 限流触发",
					fmt.Sprintf("Pornhub 检查已暂停冷却 10 分钟\n错误: %v", fetchErr))
			}
			return
		}

		log.Printf("[phscheduler] 获取视频列表失败 (第%d次): %v", attempt, fetchErr)
		if attempt < 3 {
			backoff := time.Duration(5*(1<<(attempt-1))) * time.Second
			select {
			case <-s.rootCtx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}
	if fetchErr != nil {
		log.Printf("[phscheduler] 获取视频列表失败，放弃本次检查: %v", fetchErr)
		return
	}

	if len(videos) == 0 {
		log.Printf("[phscheduler] ⚠️ 未获取到视频列表，可能是博主主页为私密或结构变更")
		return
	}

	totalNew := 0
	pendingCreated := 0
	var maxCreated int64

	for _, v := range videos {
		if v.ViewKey == "" {
			continue
		}

		// 增量扫描：PH 视频没有可靠的创建时间，以 DB 记录存在与否做去重
		exists, _ := s.db.IsVideoDownloaded(src.ID, v.ViewKey)
		if exists {
			// 如果是增量扫描，已有记录时可以提前停止（视频列表按新到旧排序）
			// 但 PH 翻页不保证严格时序，保守处理：不提前停止
			continue
		}

		totalNew++
		// 使用固定时间戳标记（PH 页面无精确发布时间，用当前时间占位）
		now := time.Now().Unix()
		if now > maxCreated {
			maxCreated = now
		}

		title := v.Title
		if title == "" {
			title = fmt.Sprintf("pornhub_%s", v.ViewKey)
		}

		dl := &db.Download{
			SourceID:  src.ID,
			VideoID:   v.ViewKey,
			Title:     title,
			Uploader:  src.Name,
			Thumbnail: v.Thumbnail,
			Status:    "pending",
			Duration:  v.Duration,
		}
		if id, err := s.db.CreateDownload(dl); err != nil {
			log.Printf("[phscheduler] 保存待下载记录失败 %s: %v", v.ViewKey, err)
		} else {
			pendingCreated++
			// 直接投递下载，不依赖 ProcessAllPending 的全局锁
			dl.ID = id
			s.DispatchDownload(*dl)
		}
	}

	// 更新 latest_video_at（首次扫描或新视频时更新）
	currentLatest, _ := s.db.GetSourceLatestVideoAt(src.ID)
	if maxCreated > currentLatest {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[phscheduler][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[phscheduler·首次全量] %s: 发现 %d 个视频，创建 %d 个 pending 记录",
			src.Name, len(videos), pendingCreated)
	} else {
		log.Printf("[phscheduler·增量] %s: 发现 %d 个新视频，创建 %d 个 pending 记录（共 %d 个视频）",
			src.Name, totalNew, pendingCreated, len(videos))
	}
}

// FullScanPHModel 全量补漏扫描 Pornhub 博主视频
func (s *PHScheduler) FullScanPHModel(src db.Source) {
	if s.IsPaused() {
		log.Printf("[phscheduler·full-scan] PH 已暂停，跳过全量扫描 %s", src.Name)
		return
	}

	// 防重入
	s.fullScanRunningMu.Lock()
	if s.fullScanRunning[src.ID] {
		s.fullScanRunningMu.Unlock()
		log.Printf("[phscheduler·full-scan] %s 全量扫描已在运行中，跳过", src.Name)
		return
	}
	s.fullScanRunning[src.ID] = true
	s.fullScanRunningMu.Unlock()

	defer func() {
		s.fullScanRunningMu.Lock()
		delete(s.fullScanRunning, src.ID)
		s.fullScanRunningMu.Unlock()
	}()

	client := s.newClient()
	defer client.Close()

	if s.getCookie() != "" {
		client.SetCookie(s.getCookie())
	}

	uploaderName := src.Name
	if uploaderName == "" || uploaderName == "未命名" {
		uploaderName = "ph_model"
	}

	log.Printf("[phscheduler·full-scan] %s: 开始全量补漏扫描", uploaderName)

	videos, err := client.GetModelVideos(src.URL)
	if err != nil {
		log.Printf("[phscheduler·full-scan] 获取视频列表失败: %v", err)
		return
	}

	log.Printf("[phscheduler·full-scan] %s: 获取 %d 个视频", uploaderName, len(videos))

	var missing []pornhub.Video
	for _, v := range videos {
		if v.ViewKey == "" {
			continue
		}
		exists, _ := s.db.IsVideoDownloaded(src.ID, v.ViewKey)
		if !exists {
			missing = append(missing, v)
		}
	}

	log.Printf("[phscheduler·full-scan] %s: 列表 %d 个，已下载 %d 个，缺失 %d 个",
		uploaderName, len(videos), len(videos)-len(missing), len(missing))

	if len(missing) == 0 {
		log.Printf("[phscheduler·full-scan] %s: 无缺失视频，扫描完成", uploaderName)
		return
	}

	created := 0
	for _, v := range missing {
		title := v.Title
		if title == "" {
			title = fmt.Sprintf("pornhub_%s", v.ViewKey)
		}

		dl := &db.Download{
			SourceID:  src.ID,
			VideoID:   v.ViewKey,
			Title:     title,
			Uploader:  src.Name,
			Thumbnail: v.Thumbnail,
			Status:    "pending",
			Duration:  v.Duration,
		}
		if id, err := s.db.CreateDownload(dl); err != nil {
			log.Printf("[phscheduler·full-scan] 创建下载记录失败 %s: %v", v.ViewKey, err)
			continue
		} else {
			dl.ID = id
			s.DispatchDownload(*dl)
		}
		created++

		// 批量创建时适当限速
		jitter := time.Duration(100+rand.Intn(200)) * time.Millisecond
		time.Sleep(jitter)
	}

	log.Printf("[phscheduler·full-scan] %s: 扫描完成，创建 %d 个待下载任务", uploaderName, created)
}
