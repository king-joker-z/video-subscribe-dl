package bscheduler

import (
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

// CheckUP 检查 UP 主新视频
func (s *BiliScheduler) CheckUP(src db.Source) {
	client := s.clientForSource(src)

	mid, err := bilibili.ExtractMID(src.URL)
	if err != nil {
		log.Printf("[bscheduler] Extract MID failed: %v", err)
		return
	}

	upInfo, err := s.getUPInfoCached(mid)
	if err != nil {
		if src.Name != "" && src.Name != "未命名" {
			log.Printf("[bscheduler][WARN] Get UP info failed (mid=%d): %v, 使用已有名称继续: %s", mid, err, src.Name)
			upInfo = &bilibili.UPInfo{MID: mid, Name: src.Name}
		} else {
			log.Printf("[bscheduler] Get UP info failed (mid=%d): %v", mid, err)
			return
		}
	}

	if (src.Name == "" || src.Name == "未命名") && upInfo.Name != "" {
		src.Name = upInfo.Name
		s.db.UpdateSource(&src)
	}

	if upInfo.Name != "" {
		s.db.UpsertPerson(fmt.Sprintf("%d", upInfo.MID), upInfo.Name, upInfo.Face)
		s.ensurePeopleDir(upInfo)
	}

	uploaderName := upInfo.Name
	uploaderDir := bilibili.SanitizePath(uploaderName)

	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	firstScanPages := 0
	if val, err := s.db.GetSetting("first_scan_pages"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			firstScanPages = n
		}
	}

	if isFirstScan {
		log.Printf("[bscheduler·首次全量] %s (mid=%d): 开始全量扫描", uploaderName, mid)
	} else {
		log.Printf("[bscheduler·增量] %s (mid=%d): 基准时间 %s",
			uploaderName, mid, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"))
	}

	if src.UseDynamicAPI {
		s.checkUPDynamic(src, client, mid, upInfo, uploaderName, uploaderDir, latestVideoAt, isFirstScan, firstScanPages)
		return
	}

	pageSize := 30
	page := 1
	processedSeasons := map[int64]bool{}
	processedBVIDs := map[string]bool{}
	totalFetched := 0
	totalNew := 0
	firstScanPendingCreated := 0
	var maxCreated int64
	stopped := false

	for {
		videos, total, err := client.GetUPVideos(mid, page, pageSize)
		if err != nil {
			if bilibili.IsRiskControl(err) || bilibili.IsAccessDenied(err) {
				if page == 1 {
					log.Printf("[bscheduler·投稿API] %s: %v，降级到动态 API", uploaderName, err)
					// 首次全量扫描 page1 就风控：降级到动态 API
					s.checkUPDynamic(src, client, mid, upInfo, uploaderName, uploaderDir, latestVideoAt, isFirstScan, firstScanPages)
					return
				}
				// page > 1：已获取到部分数据，先 break 保存，再触发风控
				log.Printf("[bscheduler·投稿API] %s: 第%d页风控，保存已获取的 %d 条后触发冷却", uploaderName, page, totalFetched)
				if bilibili.IsRiskControl(err) {
					s.TriggerCooldown()
				}
				break
			}
			log.Printf("[bscheduler] Get videos page %d failed: %v", page, err)
			break
		}
		if page == 1 {
			if isFirstScan {
				log.Printf("[bscheduler·首次全量] %s: 共 %d 个视频", uploaderName, total)
			} else {
				log.Printf("[bscheduler·增量] %s: 共 %d 个视频，增量检查中...", uploaderName, total)
			}
		}

		for _, v := range videos {
			if !isFirstScan && v.Created <= latestVideoAt {
				stopped = true
				break
			}
			if v.Created > maxCreated {
				maxCreated = v.Created
			}
			totalNew++

			if v.IsSeason && v.SeasonID > 0 && !processedSeasons[v.SeasonID] {
				processedSeasons[v.SeasonID] = true
				s.processCollection(src, client, mid, v.SeasonID, uploaderName, uploaderDir, upInfo)
			}
			if v.IsSeason {
				processedBVIDs[v.BvID] = true
				continue
			}
			if processedBVIDs[v.BvID] {
				continue
			}
			processedBVIDs[v.BvID] = true

			if isFirstScan {
				exists, _ := s.db.IsVideoDownloaded(src.ID, v.BvID)
				if !exists {
					dl := &db.Download{
						SourceID:  src.ID,
						VideoID:   v.BvID,
						Title:     v.Title,
						Uploader:  uploaderName,
						Thumbnail: v.Pic,
						Status:    "pending",
					}
					if _, err := s.db.CreateDownload(dl); err != nil {
						log.Printf("[bscheduler·首次全量] 创建 pending 记录失败 %s: %v", v.BvID, err)
					} else {
						firstScanPendingCreated++
					}
				}
			} else {
				s.processOneVideo(src, client, v.BvID, v.Title, v.Pic, uploaderName, uploaderDir, "", upInfo)
				time.Sleep(time.Duration(1000+rand.Intn(1000)) * time.Millisecond)
			}
		}

		totalFetched += len(videos)
		if stopped {
			break
		}
		if totalFetched >= total || len(videos) < pageSize {
			break
		}
		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[bscheduler·首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}
		page++
		// 翻页间隔：5~10s，首次全量每 3 页额外休息 10~15s 模拟人类节奏
		sleepMs := 5000 + rand.Intn(5000)
		if isFirstScan && page%3 == 0 {
			sleepMs += 10000 + rand.Intn(5000)
			log.Printf("[bscheduler·首次全量] 每3页大间隔，额外休息...")
		}
		time.Sleep(time.Duration(sleepMs) * time.Millisecond)
	}

	if maxCreated > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[bscheduler][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[bscheduler·首次全量] %s: 获取 %d 个新视频，创建 %d 个 pending 记录 (共检查 %d, 翻页 %d)",
			uploaderName, totalNew, firstScanPendingCreated, totalFetched, page)
		if firstScanPendingCreated > 0 {
			// 触发 pending 处理（由调用方决定）
			log.Printf("[bscheduler] 首次全量扫描完成，%d 个 pending 等待下载", firstScanPendingCreated)
		}
	} else if stopped {
		log.Printf("[bscheduler·增量] %s: 获取 %d 个新视频 (共检查 %d, 在第 %d 页停止)",
			uploaderName, totalNew, totalFetched, page)
	} else {
		log.Printf("[bscheduler·增量] %s: 获取 %d 个新视频 (共检查 %d, 翻页 %d)",
			uploaderName, totalNew, totalFetched, page)
	}
}

// checkUPDynamic 使用动态 API 检查 UP 主新视频
func (s *BiliScheduler) checkUPDynamic(src db.Source, client *bilibili.Client, mid int64,
	upInfo *bilibili.UPInfo, uploaderName, uploaderDir string, latestVideoAt int64, isFirstScan bool, firstScanPages int) {

	if isFirstScan {
		log.Printf("[bscheduler·动态API·首次全量] %s (mid=%d): 开始全量扫描", uploaderName, mid)
	} else {
		log.Printf("[bscheduler·动态API·增量] %s (mid=%d): 基准时间 %s",
			uploaderName, mid, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"))
	}

	videos, err := client.FetchDynamicVideosIncremental(mid, latestVideoAt)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.TriggerCooldown()
			return
		}
		log.Printf("[bscheduler·动态API] 拉取动态失败 (mid=%d): %v", mid, err)
		return
	}

	processedBVIDs := map[string]bool{}
	totalNew := 0
	firstScanPendingCreated := 0
	var maxCreated int64

	maxVideos := 0
	if isFirstScan && firstScanPages > 0 {
		maxVideos = firstScanPages * 12
	}

	for _, v := range videos {
		if processedBVIDs[v.BvID] {
			continue
		}
		processedBVIDs[v.BvID] = true

		if v.PubTS > maxCreated {
			maxCreated = v.PubTS
		}
		totalNew++

		if maxVideos > 0 && totalNew > maxVideos {
			log.Printf("[bscheduler·动态API·首次全量] 达到数量限制 %d，停止", maxVideos)
			break
		}

		if isFirstScan {
			exists, _ := s.db.IsVideoDownloaded(src.ID, v.BvID)
			if !exists {
				dl := &db.Download{
					SourceID:  src.ID,
					VideoID:   v.BvID,
					Title:     v.Title,
					Uploader:  uploaderName,
					Thumbnail: v.Cover,
					Status:    "pending",
				}
				if _, err := s.db.CreateDownload(dl); err != nil {
					log.Printf("[bscheduler·动态API·首次全量] 创建 pending 记录失败 %s: %v", v.BvID, err)
				} else {
					firstScanPendingCreated++
				}
			}
		} else {
			s.processOneVideo(src, client, v.BvID, v.Title, v.Cover, uploaderName, uploaderDir, "", upInfo)
			time.Sleep(time.Duration(1000+rand.Intn(1000)) * time.Millisecond)
		}
	}

	if maxCreated > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[bscheduler][WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[bscheduler·动态API] %s: 获取 %d 个新视频，创建 %d 个 pending 记录 (共返回 %d)",
			uploaderName, totalNew, firstScanPendingCreated, len(videos))
	} else {
		log.Printf("[bscheduler·动态API] %s: 获取 %d 个新视频 (共返回 %d)", uploaderName, totalNew, len(videos))
	}
}

// FullScanSource 全量补漏扫描指定 source
func (s *BiliScheduler) FullScanSource(sourceID int64) {
	src, err := s.db.GetSource(sourceID)
	if err != nil || src == nil {
		log.Printf("[bscheduler·full-scan] Source %d not found", sourceID)
		return
	}

	s.fullScanRunningMu.Lock()
	if s.fullScanRunning[sourceID] {
		s.fullScanRunningMu.Unlock()
		log.Printf("[bscheduler·full-scan] Source %d (%s) 已在扫描中，跳过", sourceID, src.Name)
		return
	}
	s.fullScanRunning[sourceID] = true
	s.fullScanRunningMu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.fullScanRunningMu.Lock()
			delete(s.fullScanRunning, sourceID)
			s.fullScanRunningMu.Unlock()
		}()
		log.Printf("[bscheduler·full-scan] 开始全量补漏扫描: %s (id=%d)", src.Name, src.ID)
		s.fullScanUP(*src)
		log.Printf("[bscheduler·full-scan] 全量补漏扫描完成: %s (id=%d)", src.Name, src.ID)
	}()
}

func (s *BiliScheduler) fullScanUP(src db.Source) {
	if s.IsInCooldown() {
		log.Printf("[bscheduler·full-scan] %s: 当前处于风控冷却期，跳过", src.Name)
		return
	}

	client := s.clientForSource(src)

	mid, err := bilibili.ExtractMID(src.URL)
	if err != nil {
		log.Printf("[bscheduler·full-scan] Extract MID failed: %v", err)
		return
	}

	uploaderName := src.Name
	if uploaderName == "" || uploaderName == "未命名" {
		uploaderName = fmt.Sprintf("mid_%d", mid)
	}

	pageSize := 30
	page := 1
	type videoEntry struct {
		BvID  string
		Title string
		Pic   string
	}
	var allVideos []videoEntry
	processedBVIDs := map[string]bool{}

	log.Printf("[bscheduler·full-scan] %s: 第一阶段 - 拉取视频列表", uploaderName)
	for {
		if s.IsInCooldown() {
			log.Printf("[bscheduler·full-scan] %s: 风控冷却中，已拉取 %d 个视频 ID（第 %d 页）", uploaderName, len(allVideos), page)
			return
		}

		videos, total, err := client.GetUPVideos(mid, page, pageSize)
		if err != nil {
			if bilibili.IsRiskControl(err) {
				// 第一页风控：降级到动态 API，避免空列表误判为"无缺失"
				if page == 1 {
					log.Printf("[bscheduler·full-scan] %s: 投稿 API 风控（%v），降级到动态 API 做全量补漏", uploaderName, err)
					s.TriggerCooldown()
					s.fullScanUPDynamic(src, client, mid, uploaderName)
					return
				}
				s.TriggerCooldown()
				log.Printf("[bscheduler·full-scan] %s: 拉取列表时风控，已获取 %d/%d", uploaderName, len(allVideos), total)
				break
			}
			// 第一页 -403（WBI 签名失效），降级到动态 API 做全量补漏
			if page == 1 && bilibili.IsAccessDenied(err) {
				log.Printf("[bscheduler·full-scan] %s: 投稿 API -403，降级到动态 API 做全量补漏", uploaderName)
				s.fullScanUPDynamic(src, client, mid, uploaderName)
				return
			}
			log.Printf("[bscheduler·full-scan] Get videos page %d failed: %v", page, err)
			break
		}
		if page == 1 {
			log.Printf("[bscheduler·full-scan] %s: 共 %d 个视频", uploaderName, total)
		}

		for _, v := range videos {
			if processedBVIDs[v.BvID] {
				continue
			}
			processedBVIDs[v.BvID] = true
			allVideos = append(allVideos, videoEntry{BvID: v.BvID, Title: v.Title, Pic: v.Pic})
		}

		if len(processedBVIDs) >= total || len(videos) < pageSize {
			break
		}
		page++
		// 翻页间隔与常规扫描保持一致：5~10s，每3页额外休息10~15s（防风控）
		sleepMs := 5000 + rand.Intn(5000)
		if page%3 == 0 {
			sleepMs += 10000 + rand.Intn(5000)
			log.Printf("[bscheduler·full-scan] %s: 每3页大间隔，额外休息...", uploaderName)
		}
		time.Sleep(time.Duration(sleepMs) * time.Millisecond)
	}

	var missing []videoEntry
	for _, v := range allVideos {
		exists, _ := s.db.IsVideoDownloaded(src.ID, v.BvID)
		if !exists {
			missing = append(missing, v)
		}
	}

	log.Printf("[bscheduler·full-scan] %s: 列表 %d 个，已下载 %d 个，缺失 %d 个",
		uploaderName, len(allVideos), len(allVideos)-len(missing), len(missing))

	if len(missing) == 0 {
		log.Printf("[bscheduler·full-scan] %s: 无缺失视频，扫描完成", uploaderName)
		return
	}

	created := 0
	for _, v := range missing {
		dl := &db.Download{
			SourceID:  src.ID,
			VideoID:   v.BvID,
			Title:     v.Title,
			Uploader:  uploaderName,
			Thumbnail: v.Pic,
			Status:    "pending",
		}
		if _, err := s.db.CreateDownload(dl); err != nil {
			log.Printf("[bscheduler·full-scan] 创建下载记录失败 %s: %v", v.BvID, err)
			continue
		}
		created++
	}

	log.Printf("[bscheduler·full-scan] %s: 扫描完成，创建 %d 个待下载任务", uploaderName, created)
}

// fullScanUPDynamic 使用动态 API 做全量补漏扫描（投稿 API -403 时的降级路径）
// 拉取该 UP 主所有动态视频，将缺失的创建为 pending 记录
func (s *BiliScheduler) fullScanUPDynamic(src db.Source, client *bilibili.Client, mid int64, uploaderName string) {
	log.Printf("[bscheduler·full-scan·动态API] %s: 开始动态 API 全量补漏", uploaderName)

	videos, err := client.FetchDynamicVideosIncremental(mid, 0) // 0 = 拉取全部
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.TriggerCooldown()
		}
		log.Printf("[bscheduler·full-scan·动态API] %s: 拉取动态失败: %v", uploaderName, err)
		return
	}

	created := 0
	for _, v := range videos {
		exists, _ := s.db.IsVideoDownloaded(src.ID, v.BvID)
		if exists {
			continue
		}
		dl := &db.Download{
			SourceID:  src.ID,
			VideoID:   v.BvID,
			Title:     v.Title,
			Uploader:  uploaderName,
			Thumbnail: v.Cover,
			Status:    "pending",
		}
		if _, err := s.db.CreateDownload(dl); err != nil {
			log.Printf("[bscheduler·full-scan·动态API] 创建记录失败 %s: %v", v.BvID, err)
			continue
		}
		created++
	}

	log.Printf("[bscheduler·full-scan·动态API] %s: 扫描完成，动态共 %d 个，创建 %d 个待下载任务",
		uploaderName, len(videos), created)
}

// processCollection UP 主空间内合集
func (s *BiliScheduler) processCollection(src db.Source, client *bilibili.Client, mid, seasonID int64, uploaderName, uploaderDir string, upInfo *bilibili.UPInfo) {
	s.fetchAndProcessSeason(src, client, mid, seasonID, uploaderName, uploaderDir, upInfo)
}
