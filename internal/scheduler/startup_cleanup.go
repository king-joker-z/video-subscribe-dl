package scheduler

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// StartupCleanup 启动时清理：扫描非法字符目录并重置全量扫描
// 通过 cleanup_version_done 版本号控制，每次递增 currentVersion 可重新触发清理
func (s *Scheduler) StartupCleanup() {
	// 当前清理版本 — 每次新增非法字符类型时递增
	const currentVersion = "2"

	done, _ := s.db.GetSetting("cleanup_version_done")
	if done == currentVersion {
		return
	}

	log.Printf("[startup-cleanup] Running cleanup v%s...", currentVersion)

	cleaned := s.cleanInvalidDirs()

	if cleaned > 0 {
		s.db.Exec("UPDATE sources SET latest_video_at = 0")
		log.Printf("[startup-cleanup] Reset latest_video_at for all sources (cleaned %d invalid dirs)", cleaned)
	}

	s.db.SetSetting("cleanup_version_done", currentVersion)
	log.Printf("[startup-cleanup] Cleanup v%s completed (cleaned %d dirs)", currentVersion, cleaned)
}

// cleanInvalidDirs 遍历 downloadDir 下所有子目录（最多 3 层），
// 删除包含非法字符的目录，并将相关 DB 记录重置为 pending
func (s *Scheduler) cleanInvalidDirs() int {
	cleaned := 0
	root := s.downloadDir

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的路径
		}

		// 计算深度，限制最多 3 层
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		depth := len(strings.Split(rel, string(os.PathSeparator)))
		if depth > 3 {
			return filepath.SkipDir
		}

		// 只检查目录
		if !info.IsDir() {
			return nil
		}

		dirName := info.Name()
		if !containsInvalidChars(dirName) {
			return nil
		}

		// 发现非法字符目录
		log.Printf("[startup-cleanup] Removing invalid dir: %s", path)
		if removeErr := os.RemoveAll(path); removeErr != nil {
			log.Printf("[startup-cleanup] Failed to remove %s: %v", path, removeErr)
			return filepath.SkipDir
		}

		// 清理 DB 中 file_path 包含该目录的记录
		s.resetDownloadsForDir(path)
		cleaned++

		return filepath.SkipDir // 已删除整个目录，不再遍历子目录
	})

	if err != nil {
		log.Printf("[startup-cleanup] Walk error: %v", err)
	}
	return cleaned
}

// containsInvalidChars 检查名称是否包含 SanitizePath 会过滤的字符
func containsInvalidChars(name string) bool {
	for _, r := range name {
		switch {
		case r <= 0x001F: // C0 控制字符
			return true
		case r == 0x007F: // DEL
			return true
		case r >= 0x0080 && r <= 0x009F: // C1 控制字符
			return true
		case r == 0xFFFD: // 替换字符
			return true
		case r >= 0x200B && r <= 0x200F: // 零宽字符
			return true
		case r >= 0x2028 && r <= 0x202F: // 行/段分隔符等
			return true
		case r >= 0x2060 && r <= 0x206F: // 不可见格式字符
			return true
		case r == 0xFEFF: // BOM/零宽不断空格
			return true
		case r >= 0xFE00 && r <= 0xFE0F: // 变体选择器
			return true
		case r >= 0xE0000 && r <= 0xE007F: // 标签字符
			return true
		}
	}
	return false
}


// resetDownloadsForDir 将 file_path 包含指定目录的 download 记录重置为 pending
func (s *Scheduler) resetDownloadsForDir(dirPath string) {
	// 匹配 file_path 以该目录开头的记录
	pattern := dirPath + "%"
	result, err := s.db.Exec(
		"UPDATE downloads SET status = 'pending', file_path = '', file_size = 0, thumb_path = '', error_message = 'reset by startup cleanup' WHERE file_path LIKE ? AND status IN ('completed', 'relocated')",
		pattern,
	)
	if err != nil {
		log.Printf("[startup-cleanup] Failed to reset downloads for dir %s: %v", dirPath, err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		log.Printf("[startup-cleanup] Reset %d download records for dir: %s", affected, dirPath)
	}
}
