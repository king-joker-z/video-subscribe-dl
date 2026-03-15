package scheduler

import (
	"regexp"
	"strings"
)

// matchesFilter 检查标题是否匹配过滤条件
// filter 格式: "关键词|关键词"（OR）、"/正则/"、"!排除"
// 空 filter 表示不过滤，匹配所有标题
func matchesFilter(title, filter string) bool {
	if filter == "" {
		return true
	}

	filter = strings.TrimSpace(filter)

	// 正则模式: /pattern/
	if strings.HasPrefix(filter, "/") && strings.HasSuffix(filter, "/") {
		pattern := filter[1 : len(filter)-1]
		re, err := regexp.Compile(pattern)
		if err != nil {
			return true // 正则无效则不过滤
		}
		return re.MatchString(title)
	}

	// 排除模式: !keyword
	if strings.HasPrefix(filter, "!") {
		keywords := strings.Split(filter[1:], "|")
		for _, kw := range keywords {
			kw = strings.TrimSpace(kw)
			if kw != "" && strings.Contains(strings.ToLower(title), strings.ToLower(kw)) {
				return false // 包含排除关键词，不匹配
			}
		}
		return true
	}

	// 关键词 OR 模式: keyword1|keyword2
	keywords := strings.Split(filter, "|")
	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw != "" && strings.Contains(strings.ToLower(title), strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
