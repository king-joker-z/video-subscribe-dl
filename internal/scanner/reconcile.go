package scanner

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-subscribe-dl/internal/db"
)

// ReconcileResult 对账结果
type ReconcileResult struct {
	TotalDBRecords    int      `json:"total_db_records"`
	TotalLocalFiles   int      `json:"total_local_files"`
	Consistent        int      `json:"consistent"`
	OrphanFiles       []string `json:"orphan_files"`       // 本地有文件但DB无记录
	MissingFiles      []string `json:"missing_files"`      // DB有记录但本地无文件
	StalePending      []int64  `json:"stale_pending"`      // DB状态为pending但队列已清空（容器重启）
	StaleDownloading  []int64  `json:"stale_downloading"`  // DB状态为downloading但进程已重启
	OrphanCount       int      `json:"orphan_count"`
	MissingCount      int      `json:"missing_count"`
	StaleCount        int      `json:"stale_count"`
	IsConsistent      bool     `json:"is_consistent"`
	CheckedAt         string   `json:"checked_at"`
}

// Reconcile 执行对账检查（只检查不修复）
func (s *Scanner) Reconcile() (*ReconcileResult, error) {
	result := &ReconcileResult{
		CheckedAt:    time.Now().Format(time.RFC3339),
		OrphanFiles:  []string{},
		MissingFiles: []string{},
		StaleDownloading: []int64{},
	}

	// 1. 扫描本地所有视频文件，收集 video_id -> filePath
	localFiles := map[string]string{} // video_id -> filePath
	filepath.Walk(s.downloadDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".info.json") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var vi map[string]interface{}
		if json.Unmarshal(data, &vi) != nil {
			return nil
		}

		videoID := getString(vi, "id")
		if videoID == "" {
			return nil
		}

		videoPath := findVideoNear(path)
		if videoPath != "" {
			localFiles[videoID] = videoPath
		}
		return nil
	})
	result.TotalLocalFiles = len(localFiles)

	// 2. 反向查询：先扫本地文件，再逐一查 DB（避免 GetAllDownloads 的 LIMIT 50000 上限误判）
	// 同时用一次 GetAllDownloads 做 stale pending/downloading 检查（有上限但这两类检查影响小）
	dbRecords, err := s.db.GetAllDownloads()
	if err != nil {
		return nil, err
	}
	result.TotalDBRecords = len(dbRecords)

	// 3. 检查本地有但DB无的 (orphan files) —— 反向查询，不受 LIMIT 影响
	for videoID, filePath := range localFiles {
		exists, _ := s.db.IsVideoExists(videoID)
		if !exists {
			result.OrphanFiles = append(result.OrphanFiles, filePath)
		}
	}
	result.OrphanCount = len(result.OrphanFiles)

	// 4. 检查DB有(completed)但本地无的 (missing files) —— 仍用 dbRecords，受 LIMIT 影响但 missing 检查为保守操作
	for _, dl := range dbRecords {
		if dl.Status == "completed" {
			if _, exists := localFiles[dl.VideoID]; !exists {
				// 也检查 file_path 是否实际存在
				if dl.FilePath != "" {
					if _, err := os.Stat(dl.FilePath); os.IsNotExist(err) {
						result.MissingFiles = append(result.MissingFiles, dl.VideoID)
					} else if err == nil {
						// file_path 文件存在，只是没在 info.json 扫描中发现，算一致
						result.Consistent++
						continue
					}
				} else {
					result.MissingFiles = append(result.MissingFiles, dl.VideoID)
				}
			} else {
				result.Consistent++
			}
		}
	}
	result.MissingCount = len(result.MissingFiles)

	// 4.5 检查 pending 状态的记录（容器重启后队列已清空）
	for _, dl := range dbRecords {
		if dl.Status == "pending" {
			result.StalePending = append(result.StalePending, dl.ID)
		}
	}
	if len(result.StalePending) > 0 {
		result.StaleCount += len(result.StalePending)
		log.Printf("[Reconcile] Found %d stale pending downloads (will be requeued on next sync)", len(result.StalePending))
	}

	// 5. 检查 downloading 状态的记录（进程重启后应该重置）
	for _, dl := range dbRecords {
		if dl.Status == "downloading" {
			result.StaleDownloading = append(result.StaleDownloading, dl.ID)
		}
	}
	result.StaleCount = len(result.StaleDownloading)

	result.IsConsistent = result.OrphanCount == 0 && result.MissingCount == 0 && result.StaleCount == 0

	return result, nil
}

// Fix 执行修复操作
func (s *Scanner) Fix(result *ReconcileResult) (*ReconcileFixResult, error) {
	fix := &ReconcileFixResult{
		FixedAt: time.Now().Format(time.RFC3339),
	}

	// 1. 补录 orphan files（本地有但DB无）
	for _, filePath := range result.OrphanFiles {
		// 找对应的 info.json
		infoPath := findInfoJsonNear(filePath)
		if infoPath == "" {
			continue
		}
		data, err := os.ReadFile(infoPath)
		if err != nil {
			continue
		}
		var vi map[string]interface{}
		if json.Unmarshal(data, &vi) != nil {
			continue
		}
		videoID := getString(vi, "id")
		title := getString(vi, "title")
		uploader := getString(vi, "channel")
		if uploader == "" {
			uploader = getString(vi, "uploader")
		}
		if uploader == "" {
			rel, _ := filepath.Rel(s.downloadDir, filePath)
			parts := strings.Split(rel, string(os.PathSeparator))
			if len(parts) >= 2 {
				uploader = parts[0]
			}
		}

		var fileSize int64
		if fi, err := os.Stat(filePath); err == nil {
			fileSize = fi.Size()
		}

		if err := s.db.UpsertDownloadFromScan(videoID, title, uploader, filePath, fileSize); err == nil {
			fix.OrphansFixed++
			log.Printf("[Reconcile Fix] Registered orphan: %s (%s)", videoID, title)
		}
	}

	// 2. 标记文件已迁移（不影响去重，仅供 UI 展示）
	for _, videoID := range result.MissingFiles {
		if err := s.db.MarkVideoRelocated(videoID); err == nil {
			fix.MissingMarked++
			log.Printf("[Reconcile Fix] Marked relocated: %s (file moved by user, download record preserved)", videoID)
		}
	}

	// 3. 重置 stale downloading 为 pending
	for _, id := range result.StaleDownloading {
		if err := s.db.ResetDownloadToPending(id); err == nil {
			fix.StaleReset++
			log.Printf("[Reconcile Fix] Reset stale download #%d to pending", id)
		}
	}

	return fix, nil
}

// ReconcileFixResult 修复结果
type ReconcileFixResult struct {
	OrphansFixed  int    `json:"orphans_fixed"`
	MissingMarked int    `json:"missing_marked"`
	StaleReset    int    `json:"stale_reset"`
	FixedAt       string `json:"fixed_at"`
}

// findInfoJsonNear 根据视频文件找附近的 .info.json
func findInfoJsonNear(videoPath string) string {
	ext := filepath.Ext(videoPath)
	base := strings.TrimSuffix(videoPath, ext)
	infoPath := base + ".info.json"
	if _, err := os.Stat(infoPath); err == nil {
		return infoPath
	}

	// 同目录找
	dir := filepath.Dir(videoPath)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".info.json") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}
