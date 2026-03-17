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

// RemoveVideoDir 删除视频文件所在的父目录（整个视频文件夹）
// 视频通常存储在 downloadDir/UP主/视频标题 [BVxxx]/ 目录中
// 此函数删除 [BVxxx] 目录及其所有内容（NFO、封面、弹幕、字幕等）
// 然后清理空的上级目录（UP主目录等）
func RemoveVideoDir(filePath, boundary string) {
	if filePath == "" {
		return
	}
	videoDir := filepath.Dir(filePath)
	absVideoDir, _ := filepath.Abs(videoDir)
	absBoundary, _ := filepath.Abs(boundary)

	// 安全检查：不能删除 boundary 本身或 boundary 外的目录
	if absVideoDir == absBoundary || !strings.HasPrefix(absVideoDir, absBoundary+string(filepath.Separator)) {
		// filePath 直接在 boundary 根下，只删文件本身
		os.Remove(filePath)
		return
	}

	// 删除整个视频目录
	if err := os.RemoveAll(videoDir); err != nil {
		log.Printf("[cleanup] Failed to remove video dir %s: %v", videoDir, err)
	} else {
		log.Printf("[cleanup] Removed video dir: %s", videoDir)
	}

	// 向上清理空目录（UP主目录、合集目录等）
	RemoveEmptyDirs(filepath.Dir(videoDir), boundary, 3)
}
