package pornhub

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	// phSpaceCollapser 用于合并连续空格
	phSpaceCollapser = regexp.MustCompile(`\s{2,}`)
)

// SanitizeName 清理视频/博主名称，使其可安全用于文件名/目录名
// 对齐 douyin.SanitizePath 实现风格
func SanitizeName(name string) string {
	// 第一轮：替换文件系统非法字符
	for _, c := range []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*", "#", "@"} {
		name = strings.ReplaceAll(name, c, "_")
	}

	// 第二轮：过滤 Unicode 不可见/控制字符
	name = strings.Map(func(r rune) rune {
		switch {
		case r <= 0x001F: // C0 控制字符
			return -1
		case r == 0x007F: // DEL
			return -1
		case r >= 0x0080 && r <= 0x009F: // C1 控制字符
			return -1
		case r == 0xFFFD: // 替换字符
			return -1
		case r >= 0x200B && r <= 0x200F: // 零宽字符
			return -1
		case r >= 0x2028 && r <= 0x202F: // 行/段分隔符
			return -1
		case r >= 0x2060 && r <= 0x206F: // 不可见格式字符
			return -1
		case r == 0xFEFF: // BOM
			return -1
		case r >= 0xFE00 && r <= 0xFE0F: // 变体选择器
			return -1
		case unicode.Is(unicode.So, r): // Symbol, Other（emoji/symbols）
			return -1
		case r > 0xFFFF: // 非 BMP 字符（补充平面 emoji/symbols）
			return -1
		case r >= 0xE0000 && r <= 0xE007F: // 标签字符
			return -1
		default:
			return r
		}
	}, name)

	// 第三轮：清理空格
	name = phSpaceCollapser.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)

	// 最终兜底：空字符串
	if name == "" {
		return "unknown"
	}

	return name
}

// SafePath 清理并截断路径组件，最大长度 80 个 Unicode 字符
// 如果清理后为空，返回 fallback
func SafePath(name, fallback string) string {
	clean := SanitizeName(name)
	if clean == "unknown" || clean == "" {
		clean = fallback
	}
	if clean == "" {
		clean = "unknown"
	}
	// 截断至 80 字符
	runes := []rune(clean)
	if len(runes) > 80 {
		clean = string(runes[:80])
	}
	return clean
}

// BuildVideoDir 构造视频文件目录路径：downloadDir/uploaderDir/videoTitle [viewkey]
func BuildVideoDir(downloadDir, uploaderName, title, viewKey string) string {
	uploaderDir := SafePath(uploaderName, "ph_"+viewKey[:min8(viewKey)])
	safeTitle := SafePath(title, "ph_"+viewKey)
	return filepath.Join(downloadDir, uploaderDir, safeTitle+" ["+viewKey+"]")
}

// min8 返回 min(len(s), 8)
func min8(s string) int {
	if len(s) < 8 {
		return len(s)
	}
	return 8
}
