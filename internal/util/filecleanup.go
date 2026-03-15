package util

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// associatedExts 视频关联文件扩展名
var associatedExts = []string{
	".nfo",
	"-thumb.jpg",
	".danmaku.ass",
	".danmaku.xml",
}

// subtitlePatterns 字幕文件匹配模式
var subtitlePatterns = []string{
	".srt", ".ass", ".ssa", ".vtt", ".sub",
}

// RemoveAssociatedFiles 删除视频文件的所有关联文件（NFO/缩略图/弹幕/字幕）
// videoPath: 视频文件的完整路径
func RemoveAssociatedFiles(videoPath string) {
	dir := filepath.Dir(videoPath)
	ext := filepath.Ext(videoPath)
	baseName := strings.TrimSuffix(filepath.Base(videoPath), ext)

	// 固定扩展名关联文件
	for _, assocExt := range associatedExts {
		assocPath := filepath.Join(dir, baseName+assocExt)
		if _, err := os.Stat(assocPath); err == nil {
			if err := os.Remove(assocPath); err != nil {
				log.Printf("[cleanup] Failed to remove %s: %v", assocPath, err)
			}
		}
	}

	// 字幕文件: 匹配 baseName.*.srt / baseName.*.ai.srt 等
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, baseName) {
			continue
		}
		suffix := strings.TrimPrefix(name, baseName)
		for _, subExt := range subtitlePatterns {
			if strings.HasSuffix(suffix, subExt) || strings.HasSuffix(suffix, ".ai"+subExt) {
				subPath := filepath.Join(dir, name)
				if err := os.Remove(subPath); err != nil {
					log.Printf("[cleanup] Failed to remove subtitle %s: %v", subPath, err)
				}
				break
			}
		}
	}
}

// RemoveEmptyDirs 安全清理空目录（向上最多 maxLevels 级，不超出 boundary）
func RemoveEmptyDirs(dir, boundary string, maxLevels int) {
	for i := 0; i < maxLevels; i++ {
		// 安全检查: 不超出 boundary
		absDir, _ := filepath.Abs(dir)
		absBoundary, _ := filepath.Abs(boundary)
		if absDir == absBoundary || !strings.HasPrefix(absDir, absBoundary) {
			return
		}

		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return // 非空或无法读取，停止
		}

		if err := os.Remove(dir); err != nil {
			return
		}
		log.Printf("[cleanup] Removed empty dir: %s", dir)
		dir = filepath.Dir(dir) // 向上一级
	}
}
