package bscheduler

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// filterRule 高级过滤规则
type filterRule struct {
	Target    string `json:"target"`
	Condition string `json:"condition"`
	Value     string `json:"value"`
	Value2    string `json:"value2"`
}

// videoInfo 过滤所需的视频信息
type videoInfo struct {
	Title    string
	Duration int
	PubDate  string
	Pages    int
	Tags     string
}

// matchesFilter 检查标题是否匹配过滤条件（兼容旧格式）
func matchesFilter(title, filter string) bool {
	if filter == "" {
		return true
	}
	filter = strings.TrimSpace(filter)
	if strings.HasPrefix(filter, "/") && strings.HasSuffix(filter, "/") {
		pattern := filter[1 : len(filter)-1]
		re, err := regexp.Compile(pattern)
		if err != nil {
			return true
		}
		return re.MatchString(title)
	}
	if strings.HasPrefix(filter, "!") {
		keywords := strings.Split(filter[1:], "|")
		for _, kw := range keywords {
			kw = strings.TrimSpace(kw)
			if kw != "" && strings.Contains(strings.ToLower(title), strings.ToLower(kw)) {
				return false
			}
		}
		return true
	}
	keywords := strings.Split(filter, "|")
	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw != "" && strings.Contains(strings.ToLower(title), strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// parseFilterRules 解析 JSON 格式的过滤规则
func parseFilterRules(rulesJSON string) []filterRule {
	if rulesJSON == "" || rulesJSON == "[]" {
		return nil
	}
	var rules []filterRule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		return nil
	}
	return rules
}

// matchesFilterRules 检查视频是否通过所有高级过滤规则（AND）
func matchesFilterRules(rules []filterRule, info videoInfo) bool {
	for _, rule := range rules {
		if !matchOneFilterRule(rule, info) {
			return false
		}
	}
	return true
}

func matchOneFilterRule(rule filterRule, info videoInfo) bool {
	switch rule.Target {
	case "title":
		return matchStringFilterRule(info.Title, rule.Condition, rule.Value)
	case "duration":
		return matchNumericFilterRule(info.Duration, rule.Condition, rule.Value, rule.Value2)
	case "pubdate":
		return matchStringFilterRule(info.PubDate, rule.Condition, rule.Value)
	case "pages":
		return matchNumericFilterRule(info.Pages, rule.Condition, rule.Value, rule.Value2)
	case "tags":
		return matchStringFilterRule(info.Tags, rule.Condition, rule.Value)
	default:
		return true
	}
}

func matchStringFilterRule(text, condition, value string) bool {
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
			return true
		}
		return re.MatchString(text)
	case "gt":
		return text > value
	case "lt":
		return text < value
	default:
		return true
	}
}

func matchNumericFilterRule(actual int, condition, value, value2 string) bool {
	v, err := strconv.Atoi(value)
	if err != nil {
		return true
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
		return strconv.Itoa(actual) == value
	default:
		return true
	}
}
