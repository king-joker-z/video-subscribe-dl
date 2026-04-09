package xscheduler

import (
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/xchina"
)

// CheckXChinaModel 检查 xchina 模特的新视频（增量扫描）
func (s *XScheduler) CheckXChinaModel(src db.Source) {
	if s.IsPaused() {
		log.Printf("[xscheduler] 已暂停，跳过检查 %s", src.Name)
		return
	}
	if s.IsInCooldown() {
		log.Printf("[xscheduler] 冷却中，跳过检查 %s", src.Name)
		return
	}

	client := s.newClient()
	defer client.Close()

	// 首次扫描时更新模特名称
	if src.Name == "" || src.Name == "未命名" {
		if info, err := client.GetModelInfo(src.URL); err == nil && info.Name != "" {
			src.Name = info.Name
			s.db.UpdateSource(&src)
			log.Printf("[xscheduler] 模特信息更新: %s", info.Name)
		} else if err != nil {
			log.Printf("[xscheduler] 获取模特信息失败（不影响视频检查）: %v", err)
		}
	}

	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	if isFirstScan {
		log.Printf("[xscheduler·首次全量] %s: 开始全量扫描 url=%s", src.Name, src.URL)
	} else {
		log.Printf("[xscheduler·增量] %s: 基准时间 %s url=%s",
			src.Name, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"), src.URL)
	}

	// 构建已知 ID 集合（用于增量停止翻页）
	var knownIDs map[string]bool
	if !isFirstScan {
		knownIDs = s.buildKnownIDs(src.ID)
	}

	videos, fetchErr := s.getModelVideosWithRetry(src, knownIDs)
	if fetchErr != nil {
		log.Printf("[xscheduler] 获取视频列表失败，放弃本次检查: %v", fetchErr)
		return
	}
	if len(videos) == 0 {
		log.Printf("[xscheduler] ⚠️ 未获取到视频列表，可能是模特主页结构变更")
		return
	}

	totalNew := 0
	pendingCreated := 0
	var maxCreated int64

	for _, v := range videos {
		if v.VideoID == "" {
			continue
		}

		exists, _ := s.db.IsVideoDownloaded(src.ID, v.VideoID)
		if exists {
			continue
		}

		totalNew++
		now := time.Now().Unix()
		if now > maxCreated {
			maxCreated = now
		}

		title := v.Title
		if title == "" {
			title = fmt.Sprintf("xchina_%s", v.VideoID)
		}

		dl := &db.Download{
			SourceID:    src.ID,
			VideoID:     v.VideoID,
			Title:       title,
			Uploader:    src.Name,
			Thumbnail:   v.Thumbnail,
			Description: v.PageURL, // PageURL 存入 Description 字段
			Status:      "pending",
		}
		if id, err := s.db.CreateDownload(dl); err != nil {
			log.Printf("[xscheduler] 保存待下载记录失败 %s: %v", v.VideoID, err)
		} else {
			pendingCreated++
			dl.ID = id
			s.DispatchDownload(*dl)
		}
	}

	// 更新 latest_video_at
	currentLatest, _ := s.db.GetSourceLatestVideoAt(src.ID)
	if maxCreated > currentLatest {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[xscheduler][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[xscheduler·首次全量] %s: 发现 %d 个视频，创建 %d 个 pending 记录",
			src.Name, len(videos), pendingCreated)
	} else {
		log.Printf("[xscheduler·增量] %s: 发现 %d 个新视频，创建 %d 个 pending 记录（共 %d 个视频）",
			src.Name, totalNew, pendingCreated, len(videos))
	}
}

// FullScanXChinaModel 全量补漏扫描
func (s *XScheduler) FullScanXChinaModel(src db.Source) {
	if s.IsPaused() {
		log.Printf("[xscheduler·full-scan] 已暂停，跳过全量扫描 %s", src.Name)
		return
	}

	s.fullScanRunningMu.Lock()
	if s.fullScanRunning[src.ID] {
		s.fullScanRunningMu.Unlock()
		log.Printf("[xscheduler·full-scan] %s 全量扫描已在运行中，跳过", src.Name)
		return
	}
	s.fullScanRunning[src.ID] = true
	s.fullScanRunningMu.Unlock()

	defer func() {
		s.fullScanRunningMu.Lock()
		delete(s.fullScanRunning, src.ID)
		s.fullScanRunningMu.Unlock()
	}()

	uploaderName := src.Name
	if uploaderName == "" || uploaderName == "未命名" {
		uploaderName = "xchina_model"
	}

	log.Printf("[xscheduler·full-scan] %s: 开始全量补漏扫描", uploaderName)

	videos, err := s.getModelVideosWithRetry(src, nil) // nil = 不提前停止
	if err != nil {
		log.Printf("[xscheduler·full-scan] 获取视频列表失败: %v", err)
		return
	}

	log.Printf("[xscheduler·full-scan] %s: 获取 %d 个视频", uploaderName, len(videos))

	created := 0
	for _, v := range videos {
		if v.VideoID == "" {
			continue
		}
		exists, _ := s.db.IsVideoDownloaded(src.ID, v.VideoID)
		if exists {
			continue
		}

		title := v.Title
		if title == "" {
			title = fmt.Sprintf("xchina_%s", v.VideoID)
		}

		dl := &db.Download{
			SourceID:    src.ID,
			VideoID:     v.VideoID,
			Title:       title,
			Uploader:    src.Name,
			Thumbnail:   v.Thumbnail,
			Description: v.PageURL, // PageURL 存入 Description 字段
			Status:      "pending",
		}
		if id, err := s.db.CreateDownload(dl); err != nil {
			log.Printf("[xscheduler·full-scan] 创建下载记录失败 %s: %v", v.VideoID, err)
			continue
		} else {
			dl.ID = id
			s.DispatchDownload(*dl)
		}
		created++

		jitter := time.Duration(100+rand.Intn(200)) * time.Millisecond
		time.Sleep(jitter)
	}

	log.Printf("[xscheduler·full-scan] %s: 扫描完成，创建 %d 个待下载任务", uploaderName, created)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func (s *XScheduler) buildKnownIDs(sourceID int64) map[string]bool {
	rows, err := s.db.Query(
		"SELECT video_id FROM downloads WHERE source_id = ? AND status IN ('completed','downloading','pending')",
		sourceID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids[id] = true
		}
	}
	return ids
}

// getModelVideosWithRetry 带重试的视频列表获取
func (s *XScheduler) getModelVideosWithRetry(src db.Source, knownIDs map[string]bool) ([]xchina.Video, error) {
	client := s.newClient()
	defer client.Close()

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		videos, err := client.GetModelVideos(s.rootCtx, src.URL, knownIDs)
		if err == nil {
			return videos, nil
		}

		if s.rootCtx.Err() != nil {
			return nil, s.rootCtx.Err()
		}

		errMsg := err.Error()
		isRateLimitErr := strings.Contains(errMsg, "403") ||
			strings.Contains(errMsg, "429") ||
			strings.Contains(errMsg, "503") ||
			strings.Contains(errMsg, "token expired")

		if isRateLimitErr {
			log.Printf("[xscheduler] ⚠️ 被限流 (第%d次): %v", attempt, err)
			s.TriggerCooldown()
			if s.notifier != nil {
				s.notifier.Send(notify.EventRateLimited, "XChina 限流触发",
					fmt.Sprintf("XChina 检查已暂停冷却 10 分钟\n错误: %v", err))
			}
			return nil, err
		}

		log.Printf("[xscheduler] 获取视频列表失败 (第%d次): %v", attempt, err)
		lastErr = err
		if attempt < 3 {
			backoff := time.Duration(5*(1<<(attempt-1))) * time.Second
			select {
			case <-s.rootCtx.Done():
				return nil, s.rootCtx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return nil, lastErr
}
