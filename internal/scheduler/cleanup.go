package scheduler

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"video-subscribe-dl/internal/notify"
	"video-subscribe-dl/internal/util"
)

// CleanupResult holds the result of an automatic cleanup run
type CleanupResult struct {
	FilesDeleted int   `json:"files_deleted"`
	BytesFreed   int64 `json:"bytes_freed"`
	Errors       int   `json:"errors"`
}

// RunAutoCleanup performs automatic cleanup based on retention days setting
// Returns nil result if auto-cleanup is disabled (retention_days = 0 or unset)
func (s *Scheduler) RunAutoCleanup() *CleanupResult {
	retentionStr, err := s.db.GetSetting("retention_days")
	if err != nil || retentionStr == "" || retentionStr == "0" {
		return nil // auto-cleanup disabled
	}
	retentionDays, err := strconv.Atoi(retentionStr)
	if err != nil || retentionDays <= 0 {
		return nil
	}

	log.Printf("[cleanup] Running auto-cleanup: retention=%d days", retentionDays)

	downloads, err := s.db.GetOldCompletedDownloads(retentionDays)
	if err != nil {
		log.Printf("[cleanup] Failed to query old downloads: %v", err)
		return nil
	}

	if len(downloads) == 0 {
		log.Printf("[cleanup] No downloads older than %d days to clean", retentionDays)
		return &CleanupResult{}
	}

	result := &CleanupResult{}
	for _, dl := range downloads {
		if dl.FilePath == "" {
			continue
		}

		// Delete video file
		if err := os.Remove(dl.FilePath); err != nil && !os.IsNotExist(err) {
			log.Printf("[cleanup] Failed to delete %s: %v", dl.FilePath, err)
			result.Errors++
			continue
		}

		// Delete associated files in the same directory (NFO, thumb, danmaku)
		dir := filepath.Dir(dl.FilePath)
		baseName := strings.TrimSuffix(filepath.Base(dl.FilePath), filepath.Ext(dl.FilePath))
		associatedExts := []string{".nfo", ".danmaku.ass", ".danmaku.xml"}
		for _, ext := range associatedExts {
			assocPath := filepath.Join(dir, baseName+ext)
			os.Remove(assocPath) // best effort, ignore errors
		}

		// Try to remove parent dir if empty
		removeEmptyDirs(dir)

		// Mark as cleaned in DB
		if err := s.db.MarkDownloadCleaned(dl.ID); err != nil {
			log.Printf("[cleanup] Failed to mark %d as cleaned: %v", dl.ID, err)
			result.Errors++
			continue
		}

		result.FilesDeleted++
		result.BytesFreed += dl.FileSize
		log.Printf("[cleanup] Deleted: %s (%.1f MB)", dl.Title, float64(dl.FileSize)/1024/1024)
	}

	if result.FilesDeleted > 0 {
		msg := fmt.Sprintf("清理了 %d 个视频，释放 %.1f MB 空间",
			result.FilesDeleted, float64(result.BytesFreed)/1024/1024)
		log.Printf("[cleanup] %s", msg)
		s.notifier.Send(notify.EventDiskLow, "自动清理完成", msg)
	}

	return result
}

// RunDiskPressureCleanup performs emergency cleanup when disk space is critically low
// Deletes oldest completed downloads until free space exceeds the threshold
func (s *Scheduler) RunDiskPressureCleanup() *CleanupResult {
	enableStr, _ := s.db.GetSetting("auto_cleanup_on_low_disk")
	if enableStr != "true" && enableStr != "1" {
		return nil
	}

	minFreeGB := 1.0
	if v, err := s.db.GetSetting("min_disk_free_gb"); err == nil && v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed > 0 {
			minFreeGB = parsed
		}
	}

	free, err := util.GetDiskFree(s.downloadDir)
	if err != nil {
		return nil
	}
	minFreeBytes := uint64(minFreeGB * 1024 * 1024 * 1024)
	if free >= minFreeBytes {
		return nil
	}

	log.Printf("[cleanup] Disk pressure cleanup: %.2f GB free < %.1f GB threshold",
		float64(free)/1024/1024/1024, minFreeGB)

	// Get oldest completed downloads (no retention limit, just oldest first)
	downloads, err := s.db.GetOldCompletedDownloads(0) // 0 = all completed
	if err != nil {
		log.Printf("[cleanup] Failed to query downloads for pressure cleanup: %v", err)
		return nil
	}

	result := &CleanupResult{}
	for _, dl := range downloads {
		// Re-check disk space
		free, err = util.GetDiskFree(s.downloadDir)
		if err != nil || free >= minFreeBytes {
			break // enough space now
		}

		if dl.FilePath == "" {
			continue
		}

		if err := os.Remove(dl.FilePath); err != nil && !os.IsNotExist(err) {
			result.Errors++
			continue
		}

		// Clean associated files
		dir := filepath.Dir(dl.FilePath)
		baseName := strings.TrimSuffix(filepath.Base(dl.FilePath), filepath.Ext(dl.FilePath))
		for _, ext := range []string{".nfo", ".danmaku.ass"} {
			os.Remove(filepath.Join(dir, baseName+ext))
		}
		removeEmptyDirs(dir)

		s.db.MarkDownloadCleaned(dl.ID)
		result.FilesDeleted++
		result.BytesFreed += dl.FileSize
	}

	if result.FilesDeleted > 0 {
		msg := fmt.Sprintf("磁盘空间紧急清理：删除了 %d 个视频，释放 %.1f MB",
			result.FilesDeleted, float64(result.BytesFreed)/1024/1024)
		log.Printf("[cleanup] %s", msg)
		s.notifier.Send(notify.EventDiskLow, "磁盘紧急清理", msg)
	}

	return result
}

// removeEmptyDirs recursively removes empty directories up the tree
func removeEmptyDirs(dir string) {
	for i := 0; i < 3; i++ { // max 3 levels up
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		if err := os.Remove(dir); err != nil {
			break
		}
		dir = filepath.Dir(dir)
	}
}
