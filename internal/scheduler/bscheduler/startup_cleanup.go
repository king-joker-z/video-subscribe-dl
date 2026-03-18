package bscheduler

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// StartupCleanup 启动时清理：扫描非法字符目录并重置全量扫描
func (s *BiliScheduler) StartupCleanup() {
	const currentVersion = "2"

	done, _ := s.db.GetSetting("cleanup_version_done")
	if done == currentVersion {
		return
	}

	log.Printf("[bscheduler·startup-cleanup] Running cleanup v%s...", currentVersion)

	cleaned := s.cleanInvalidDirs()

	if cleaned > 0 {
		s.db.Exec("UPDATE sources SET latest_video_at = 0")
		log.Printf("[bscheduler·startup-cleanup] Reset latest_video_at for all sources (cleaned %d invalid dirs)", cleaned)
	}

	s.db.SetSetting("cleanup_version_done", currentVersion)
	log.Printf("[bscheduler·startup-cleanup] Cleanup v%s completed (cleaned %d dirs)", currentVersion, cleaned)
}

// cleanInvalidDirs 遍历 downloadDir 下所有子目录（最多 3 层），删除包含非法字符的目录
func (s *BiliScheduler) cleanInvalidDirs() int {
	cleaned := 0
	root := s.downloadDir

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		depth := len(strings.Split(rel, string(os.PathSeparator)))
		if depth > 3 {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			return nil
		}
		dirName := info.Name()
		if !containsInvalidChars(dirName) {
			return nil
		}
		log.Printf("[bscheduler·startup-cleanup] Removing invalid dir: %s", path)
		if removeErr := os.RemoveAll(path); removeErr != nil {
			log.Printf("[bscheduler·startup-cleanup] Failed to remove %s: %v", path, removeErr)
			return filepath.SkipDir
		}
		s.resetDownloadsForDir(path)
		cleaned++
		return filepath.SkipDir
	})

	if err != nil {
		log.Printf("[bscheduler·startup-cleanup] Walk error: %v", err)
	}
	return cleaned
}

func containsInvalidChars(name string) bool {
	for _, r := range name {
		switch {
		case r <= 0x001F:
			return true
		case r == 0x007F:
			return true
		case r >= 0x0080 && r <= 0x009F:
			return true
		case r == 0xFFFD:
			return true
		case r >= 0x200B && r <= 0x200F:
			return true
		case r >= 0x2028 && r <= 0x202F:
			return true
		case r >= 0x2060 && r <= 0x206F:
			return true
		case r == 0xFEFF:
			return true
		case r >= 0xFE00 && r <= 0xFE0F:
			return true
		case r >= 0xE0000 && r <= 0xE007F:
			return true
		}
	}
	return false
}

func (s *BiliScheduler) resetDownloadsForDir(dirPath string) {
	pattern := dirPath + "%"
	result, err := s.db.Exec(
		"UPDATE downloads SET status = 'pending', file_path = '', file_size = 0, thumb_path = '', error_message = 'reset by startup cleanup' WHERE file_path LIKE ? AND status IN ('completed', 'relocated')",
		pattern,
	)
	if err != nil {
		log.Printf("[bscheduler·startup-cleanup] Failed to reset downloads for dir %s: %v", dirPath, err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		log.Printf("[bscheduler·startup-cleanup] Reset %d download records for dir: %s", affected, dirPath)
	}
}
