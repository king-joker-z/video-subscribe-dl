package scheduler

import (
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

func (s *Scheduler) checkUP(src db.Source) {
	client := s.clientForSource(src)

	mid, err := bilibili.ExtractMID(src.URL)
	if err != nil {
		log.Printf("Extract MID failed: %v", err)
		return
	}

	// UP 主信息（优先缓存，6 小时内不重复请求）
	upInfo, err := s.getUPInfoCached(client, mid)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.triggerCooldown()
			s.dl.Pause()
		} else {
			log.Printf("Get UP info failed (mid=%d): %v", mid, err)
		}
		return
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

	// 获取增量基准时间
	latestVideoAt, _ := s.db.GetSourceLatestVideoAt(src.ID)
	isFirstScan := latestVideoAt == 0

	// 首次扫描页数限制
	firstScanPages := 0
	if val, err := s.db.GetSetting("first_scan_pages"); err == nil && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			firstScanPages = n
		}
	}

	if isFirstScan {
		log.Printf("[首次全量] %s (mid=%d): 开始全量扫描", uploaderName, mid)
		if firstScanPages > 0 {
			log.Printf("[首次全量] 页数限制: %d 页", firstScanPages)
		}
	} else {
		log.Printf("[增量] %s (mid=%d): 基准时间 %s",
			uploaderName, mid, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"))
	}

	// 投稿 API 优先（能拿到所有视频），动态 API 作为降级备选
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
			if bilibili.IsRiskControl(err) {
				// 投稿 API 被风控，降级到动态 API
				if page == 1 {
					log.Printf("[投稿API] %s: 触发风控，降级到动态 API", uploaderName)
					s.checkUPDynamic(src, client, mid, upInfo, uploaderName, uploaderDir, latestVideoAt, isFirstScan, firstScanPages)
					return
				}
				s.triggerCooldown()
				s.dl.Pause()
				return
			}
			log.Printf("Get videos page %d failed: %v", page, err)
			break
		}
		if page == 1 {
			if isFirstScan {
				log.Printf("[首次全量] %s: 共 %d 个视频（懒加载模式：先建记录后补详情）", uploaderName, total)
			} else {
				log.Printf("[增量] %s: 共 %d 个视频，增量检查中...", uploaderName, total)
			}
		}

		for _, v := range videos {
			// 增量检查: 视频发布时间 <= latestVideoAt 则停止（后面都是旧视频）
			if !isFirstScan && v.Created <= latestVideoAt {
				stopped = true
				break
			}

			// 追踪本轮最大 created 时间
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
				// 首次扫描：只创建 pending 记录，不调 GetVideoDetail（避免风控）
				// 详情在 retryOneDownload/ProcessAllPending 处理时按需获取
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
						log.Printf("[首次全量] 创建 pending 记录失败 %s: %v", v.BvID, err)
					} else {
						firstScanPendingCreated++
					}
				}
			} else {
				// 增量扫描：保持现有 processOneVideo 逻辑，但加请求间隔降低风控风险
				s.processOneVideo(src, client, v.BvID, v.Title, v.Pic, uploaderName, uploaderDir, "", upInfo)
				// 增量通常只有几个新视频，1-2s 延迟不影响体验
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

		// 首次扫描页数限制
		if isFirstScan && firstScanPages > 0 && page >= firstScanPages {
			log.Printf("[首次全量] 达到页数限制 %d 页，停止翻页", firstScanPages)
			break
		}

		page++
		// 翻页间隔统一为 1.5-2.5s（与 fullScanUP 一致，降低风控风险）
		time.Sleep(time.Duration(1500+rand.Intn(1000)) * time.Millisecond)
	}

	// 更新 latest_video_at
	if maxCreated > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[首次全量] %s: 获取 %d 个新视频，创建 %d 个 pending 记录 (共检查 %d, 翻页 %d)",
			uploaderName, totalNew, firstScanPendingCreated, totalFetched, page)
		// 首次扫描完成后触发 pending 处理（懒加载：下载时再获取详情）
		if firstScanPendingCreated > 0 {
			go s.ProcessAllPending()
		}
	} else if stopped {
		log.Printf("[增量] %s: 获取 %d 个新视频 (共检查 %d, 在第 %d 页停止)",
			uploaderName, totalNew, totalFetched, page)
	} else {
		log.Printf("[增量] %s: 获取 %d 个新视频 (共检查 %d, 翻页 %d)",
			uploaderName, totalNew, totalFetched, page)
	}
}

// checkUPDynamic 使用动态 API 检查 UP 主新视频
func (s *Scheduler) checkUPDynamic(src db.Source, client *bilibili.Client, mid int64,
	upInfo *bilibili.UPInfo, uploaderName, uploaderDir string, latestVideoAt int64, isFirstScan bool, firstScanPages int) {

	if isFirstScan {
		log.Printf("[动态API·首次全量] %s (mid=%d): 开始全量扫描（懒加载模式）", uploaderName, mid)
	} else {
		log.Printf("[动态API·增量] %s (mid=%d): 基准时间 %s",
			uploaderName, mid, time.Unix(latestVideoAt, 0).Format("2006-01-02 15:04:05"))
	}

	videos, err := client.FetchDynamicVideosIncremental(mid, latestVideoAt)
	if err != nil {
		if bilibili.IsRiskControl(err) {
			s.triggerCooldown()
			s.dl.Pause()
			return
		}
		log.Printf("[动态API] 拉取动态失败 (mid=%d): %v", mid, err)
		return
	}

	processedBVIDs := map[string]bool{}
	totalNew := 0
	firstScanPendingCreated := 0
	var maxCreated int64

	// 首次扫描数量限制（每页约 12 条，用 firstScanPages * 12 近似）
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
			log.Printf("[动态API·首次全量] 达到数量限制 %d，停止", maxVideos)
			break
		}

		if isFirstScan {
			// 首次扫描：只创建 pending 记录，不调 GetVideoDetail（避免风控）
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
					log.Printf("[动态API·首次全量] 创建 pending 记录失败 %s: %v", v.BvID, err)
				} else {
					firstScanPendingCreated++
				}
			}
		} else {
			// 增量扫描：保持现有逻辑，加请求间隔
			s.processOneVideo(src, client, v.BvID, v.Title, v.Cover, uploaderName, uploaderDir, "", upInfo)
			time.Sleep(time.Duration(1000+rand.Intn(1000)) * time.Millisecond)
		}
	}

	// 更新 latest_video_at
	if maxCreated > latestVideoAt {
		if err := s.db.UpdateSourceLatestVideoAt(src.ID, maxCreated); err != nil {
			log.Printf("[WARN] 更新 latest_video_at 失败: %v", err)
		}
	}

	if isFirstScan {
		log.Printf("[动态API] %s: 获取 %d 个新视频，创建 %d 个 pending 记录 (共返回 %d)",
			uploaderName, totalNew, firstScanPendingCreated, len(videos))
		if firstScanPendingCreated > 0 {
			go s.ProcessAllPending()
		}
	} else {
		log.Printf("[动态API] %s: 获取 %d 个新视频 (共返回 %d)", uploaderName, totalNew, len(videos))
	}
}

// FullScanSource 全量补漏扫描指定 source（忽略增量基准，扫描所有视频，跳过已下载的）
func (s *Scheduler) FullScanSource(sourceID int64) {
	src, err := s.db.GetSource(sourceID)
	if err != nil || src == nil {
		log.Printf("[full-scan] Source %d not found", sourceID)
		return
	}

	// 防止同一 source 重复扫描
	s.fullScanRunningMu.Lock()
	if s.fullScanRunning[sourceID] {
		s.fullScanRunningMu.Unlock()
		log.Printf("[full-scan] Source %d (%s) 已在扫描中，跳过", sourceID, src.Name)
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
		log.Printf("[full-scan] 开始全量补漏扫描: %s (id=%d)", src.Name, src.ID)
		s.fullScanUP(*src)
		log.Printf("[full-scan] 全量补漏扫描完成: %s (id=%d)", src.Name, src.ID)
	}()
}

func (s *Scheduler) fullScanUP(src db.Source) {
	// 开头就检查冷却状态，避免冷却期内浪费请求
	if s.isInCooldown() {
		log.Printf("[full-scan] %s: 当前处于风控冷却期，跳过", src.Name)
		return
	}

	client := s.clientForSource(src)

	mid, err := bilibili.ExtractMID(src.URL)
	if err != nil {
		log.Printf("[full-scan] Extract MID failed: %v", err)
		return
	}

	// 全量扫描不调 GetUPInfo，完全用 DB 已有信息，零额外 API 请求
	uploaderName := src.Name
	if uploaderName == "" || uploaderName == "未命名" {
		uploaderName = fmt.Sprintf("mid_%d", mid)
	}
	// 全量扫描：先用投稿 API 拉取所有 BVID 列表，再批量过滤已下载的，最后只处理缺失的
	// 第一阶段：拉取所有 BVID（只消耗翻页请求，不调 GetVideoDetail）
	pageSize := 30
	page := 1
	type videoEntry struct {
		BvID  string
		Title string
		Pic   string
	}
	var allVideos []videoEntry
	processedBVIDs := map[string]bool{}

	log.Printf("[full-scan] %s: 第一阶段 - 拉取视频列表", uploaderName)
	for {
		if s.isInCooldown() {
			log.Printf("[full-scan] %s: 风控冷却中，已拉取 %d 个视频 ID（第 %d 页）", uploaderName, len(allVideos), page)
			return
		}

		videos, total, err := client.GetUPVideos(mid, page, pageSize)
		if err != nil {
			if bilibili.IsRiskControl(err) {
				s.triggerCooldown()
				log.Printf("[full-scan] %s: 拉取列表时风控，已获取 %d/%d", uploaderName, len(allVideos), total)
				// 不 return，用已拉到的部分继续处理
				break
			}
			log.Printf("[full-scan] Get videos page %d failed: %v", page, err)
			break
		}
		if page == 1 {
			log.Printf("[full-scan] %s: 共 %d 个视频", uploaderName, total)
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
		time.Sleep(time.Duration(1500+rand.Intn(1000)) * time.Millisecond)
	}

	// 第二阶段：过滤已下载的，只处理缺失的视频
	var missing []videoEntry
	for _, v := range allVideos {
		exists, _ := s.db.IsVideoDownloaded(src.ID, v.BvID)
		if !exists {
			missing = append(missing, v)
		}
	}

	log.Printf("[full-scan] %s: 列表 %d 个，已下载 %d 个，缺失 %d 个",
		uploaderName, len(allVideos), len(allVideos)-len(missing), len(missing))

	if len(missing) == 0 {
		log.Printf("[full-scan] %s: 无缺失视频，扫描完成", uploaderName)
		return
	}

	// 第三阶段：直接创建 pending 下载记录（不调 GetVideoDetail，零额外 API 请求）
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
			log.Printf("[full-scan] 创建下载记录失败 %s: %v", v.BvID, err)
			continue
		}
		created++
	}

	log.Printf("[full-scan] %s: 扫描完成，创建 %d 个待下载任务", uploaderName, created)

	// 触发 pending 处理
	if created > 0 {
		go s.ProcessAllPending()
	}
}

const upInfoCacheTTL = 6 * time.Hour

// getUPInfoCached 带缓存的 UP 主信息获取，减少 API 请求量
func (s *Scheduler) getUPInfoCached(client *bilibili.Client, mid int64) (*bilibili.UPInfo, error) {
	s.upInfoCacheMu.RLock()
	if entry, ok := s.upInfoCache[mid]; ok && time.Since(entry.fetchedAt) < upInfoCacheTTL {
		s.upInfoCacheMu.RUnlock()
		return entry.info, nil
	}
	s.upInfoCacheMu.RUnlock()

	info, err := client.GetUPInfo(mid)
	if err != nil {
		return nil, err
	}

	s.upInfoCacheMu.Lock()
	s.upInfoCache[mid] = &upInfoCacheEntry{info: info, fetchedAt: time.Now()}
	s.upInfoCacheMu.Unlock()

	return info, nil
}
