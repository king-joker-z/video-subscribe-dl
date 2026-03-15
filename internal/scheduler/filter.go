package scheduler

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// FilterRule 高级过滤规则
type FilterRule struct {
	Target    string `json:"target"`    // "title" | "duration" | "pubdate" | "pages"
	Condition string `json:"condition"` // "contains" | "not_contains" | "regex" | "gt" | "lt" | "between"
	Value     string `json:"value"`     // 条件值
	Value2    string `json:"value2"`    // between 的第二个值
}

// VideoInfo 过滤所需的视频信息
type VideoInfo struct {
	Title    string
	Duration int    // 时长（秒）
	PubDate  string // 发布日期 YYYY-MM-DD
	Pages    int    // 分P 数量
}

// matchesFilter 检查标题是否匹配过滤条件（兼容旧格式）
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

// ParseFilterRules 解析 JSON 格式的过滤规则
func ParseFilterRules(rulesJSON string) []FilterRule {
	if rulesJSON == "" || rulesJSON == "[]" {
		return nil
	}
	var rules []FilterRule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		return nil
	}
	return rules
}

// MatchesFilterRules 检查视频是否通过所有高级过滤规则
// 所有规则为 AND 关系：全部通过才返回 true
// 空规则列表返回 true（不过滤）
func MatchesFilterRules(rules []FilterRule, info VideoInfo) bool {
	for _, rule := range rules {
		if !matchOneRule(rule, info) {
			return false
		}
	}
	return true
}

// matchOneRule 检查单条过滤规则
func matchOneRule(rule FilterRule, info VideoInfo) bool {
	switch rule.Target {
	case "title":
		return matchStringRule(info.Title, rule.Condition, rule.Value)
	case "duration":
		return matchNumericRule(info.Duration, rule.Condition, rule.Value, rule.Value2)
	case "pubdate":
		return matchStringRule(info.PubDate, rule.Condition, rule.Value)
	case "pages":
		return matchNumericRule(info.Pages, rule.Condition, rule.Value, rule.Value2)
	default:
		return true // 未知 target 不过滤
	}
}

// matchStringRule 字符串条件匹配
func matchStringRule(text, condition, value string) bool {
	textLower := strings.ToLower(text)
	valueLower := strings.ToLower(value)

	switch condition {
	case "contains":
		return strings.Contains(textLower, valueLower)
	case "not_contains":
		return !strings.Contains(textLower, valueLower)
	case "regex":
		re, err := regexp.Compile(value)
		if err != nil {
			return true // 正则错误不过滤
		}
		return re.MatchString(text)
	case "gt":
		return text > value
	case "lt":
		return text < value
	case "between":
		return false // 字符串不支持 between（除非日期比较）
	default:
		return true
	}
}

// matchNumericRule 数值条件匹配
func matchNumericRule(actual int, condition, value, value2 string) bool {
	v, err := strconv.Atoi(value)
	if err != nil {
		return true // 值无效不过滤
	}

	switch condition {
	case "gt":
		return actual > v
	case "lt":
		return actual < v
	case "between":
		v2, err2 := strconv.Atoi(value2)
		if err2 != nil {
			return true
		}
		return actual >= v && actual <= v2
	case "contains":
		// 数值 contains 不太有意义，但兼容
		return strconv.Itoa(actual) == value
	default:
		return true
	}
}
