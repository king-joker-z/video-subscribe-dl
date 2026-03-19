package bscheduler

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// StartupCleanup 启动时清理：扫描非法字符目录并修复存量数据
func (s *BiliScheduler) StartupCleanup() {
	const currentVersion = "3"

	done, _ := s.db.GetSetting("cleanup_version_done")
	if done == currentVersion {
		return
	}

	log.Printf("[bscheduler·startup-cleanup] Running cleanup v%s...", currentVersion)

	// v3 新增：修复存量被错误重置为 pending 的 completed 记录
	// （v2 及之前的 resetDownloadsForDir 错误地把 completed 改为 pending）
	// 无法精确判断哪些 pending 是被误重置的，因此只修复 file_path 为空且状态为 pending
	// 且该 video_id 同时存在 completed 记录的情况（这种情况不存在，跳过）
	// 实际上只能做保守修复：file_path='' 且 status='pending' 且 downloaded_at IS NOT NULL 的记录
	// 这类记录是"曾经下载完成但被重置"的特征
	result, err := s.db.Exec(`
		UPDATE downloads SET status = 'relocated'
		WHERE status = 'pending' AND file_path = '' AND downloaded_at IS NOT NULL
	`)
	if err != nil {
		log.Printf("[bscheduler·startup-cleanup] v3 migration error: %v", err)
	} else {
		affected, _ := result.RowsAffected()
		if affected > 0 {
			log.Printf("[bscheduler·startup-cleanup] v3: Fixed %d incorrectly-reset pending records → relocated", affected)
		}
	}

	cleaned := s.cleanInvalidDirs()

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

	// completed → relocated（保留去重，标记文件已不在原位）
	result, err := s.db.Exec(
		"UPDATE downloads SET status = 'relocated', file_path = '', file_size = 0, thumb_path = '', error_message = 'dir removed by startup cleanup' WHERE file_path LIKE ? AND status = 'completed'",
		pattern,
	)
	if err != nil {
		log.Printf("[bscheduler·startup-cleanup] Failed to mark relocated for dir %s: %v", dirPath, err)
	} else {
		affected, _ := result.RowsAffected()
		if affected > 0 {
			log.Printf("[bscheduler·startup-cleanup] Marked %d completed records as relocated for dir: %s", affected, dirPath)
		}
	}

	// relocated → 同样只清空路径，状态不变
	result2, err := s.db.Exec(
		"UPDATE downloads SET file_path = '', file_size = 0, thumb_path = '' WHERE file_path LIKE ? AND status = 'relocated'",
		pattern,
	)
	if err != nil {
		log.Printf("[bscheduler·startup-cleanup] Failed to clear relocated paths for dir %s: %v", dirPath, err)
	} else {
		affected2, _ := result2.RowsAffected()
		if affected2 > 0 {
			log.Printf("[bscheduler·startup-cleanup] Cleared file_path for %d relocated records in dir: %s", affected2, dirPath)
		}
	}
}
