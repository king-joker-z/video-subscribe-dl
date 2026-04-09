package xchina

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var xcSpaceCollapser = regexp.MustCompile(`\s{2,}`)

// SanitizeName 清理名称，使其可安全用于文件名/目录名
func SanitizeName(name string) string {
	for _, c := range []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*", "#", "@"} {
		name = strings.ReplaceAll(name, c, "_")
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r <= 0x001F, r == 0x007F:
			return -1
		case r >= 0x0080 && r <= 0x009F:
			return -1
		case r == 0xFFFD:
			return -1
		case r >= 0x200B && r <= 0x200F:
			return -1
		case unicode.Is(unicode.So, r):
			return -1
		case r > 0xFFFF:
			return -1
		default:
			return r
		}
	}, name)
	name = xcSpaceCollapser.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "unknown"
	}
	return name
}

// SafePath 清理并截断路径组件，最大 80 个 Unicode 字符
func SafePath(name, fallback string) string {
	clean := SanitizeName(name)
	if clean == "unknown" || clean == "" {
		clean = fallback
	}
	if clean == "" {
		clean = "unknown"
	}
	runes := []rune(clean)
	if len(runes) > 80 {
		clean = strings.TrimSpace(string(runes[:80]))
	}
	return clean
}

// BuildVideoDir 构造视频文件目录路径：downloadDir/modelName/videoTitle [videoID]
func BuildVideoDir(downloadDir, modelName, title, videoID string) string {
	modelDir := SafePath(modelName, "xchina_model")
	safeTitle := SafePath(title, "xchina_"+videoID)
	return filepath.Join(downloadDir, modelDir, safeTitle+" ["+videoID+"]")
}
