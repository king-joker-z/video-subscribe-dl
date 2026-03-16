package scheduler

import "testing"

func TestMatchesFilter(t *testing.T) {
	tests := []struct {
		title  string
		filter string
		want   bool
	}{
		// 空 filter 匹配所有
		{"any title", "", true},
		// 关键词 OR
		{"Go语言教程", "Go", true},
		{"Go语言教程", "Python", false},
		{"Go语言教程", "Go|Python", true},
		{"Python入门", "Go|Python", true},
		{"Java基础", "Go|Python", false},
		// 大小写不敏感
		{"GOLANG Tutorial", "golang", true},
		// 正则
		{"EP01 Title", "/EP\\d+/", true},
		{"No match", "/EP\\d+/", false},
		// 排除
		{"正片内容", "!预告|花絮", true},
		{"预告片", "!预告|花絮", false},
		{"幕后花絮", "!预告|花絮", false},
		// 无效正则不过滤
		{"any", "/[invalid/", true},
		// 空格处理
		{"test", " keyword | test ", true},
		// 单个排除关键词
		{"广告视频", "!广告", false},
		{"正常视频", "!广告", true},
		// 空关键词
		{"title", "|", false},
		{"title", "||valid", false},
	}

	for _, tt := range tests {
		got := matchesFilter(tt.title, tt.filter)
		if got != tt.want {
			t.Errorf("matchesFilter(%q, %q) = %v, want %v", tt.title, tt.filter, got, tt.want)
		}
	}
}

// ============================
// ParseFilterRules 测试
// ============================

func TestParseFilterRules_Empty(t *testing.T) {
	if rules := ParseFilterRules(""); rules != nil {
		t.Errorf("expected nil for empty string, got %v", rules)
	}
	if rules := ParseFilterRules("[]"); rules != nil {
		t.Errorf("expected nil for empty array, got %v", rules)
	}
}

func TestParseFilterRules_InvalidJSON(t *testing.T) {
	if rules := ParseFilterRules("not json"); rules != nil {
		t.Errorf("expected nil for invalid JSON, got %v", rules)
	}
}

func TestParseFilterRules_Valid(t *testing.T) {
	input := `[{"target":"title","condition":"contains","value":"test"}]`
	rules := ParseFilterRules(input)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Target != "title" || rules[0].Condition != "contains" || rules[0].Value != "test" {
		t.Errorf("unexpected rule: %+v", rules[0])
	}
}

// ============================
// MatchesFilterRules 测试
// ============================

func TestMatchesFilterRules_EmptyRules(t *testing.T) {
	if !MatchesFilterRules(nil, VideoInfo{Title: "anything"}) {
		t.Error("expected true for nil rules")
	}
	if !MatchesFilterRules([]FilterRule{}, VideoInfo{Title: "anything"}) {
		t.Error("expected true for empty rules")
	}
}

func TestMatchesFilterRules_TitleContains(t *testing.T) {
	rules := []FilterRule{
		{Target: "title", Condition: "contains", Value: "golang"},
	}
	if !MatchesFilterRules(rules, VideoInfo{Title: "Learn Golang Today"}) {
		t.Error("expected match for title containing 'golang'")
	}
	if MatchesFilterRules(rules, VideoInfo{Title: "Learn Python Today"}) {
		t.Error("expected no match for title not containing 'golang'")
	}
}

func TestMatchesFilterRules_TitleNotContains(t *testing.T) {
	rules := []FilterRule{
		{Target: "title", Condition: "not_contains", Value: "广告"},
	}
	if !MatchesFilterRules(rules, VideoInfo{Title: "正片内容"}) {
		t.Error("expected match for title not containing '广告'")
	}
	if MatchesFilterRules(rules, VideoInfo{Title: "这是广告视频"}) {
		t.Error("expected no match for title containing '广告'")
	}
}

func TestMatchesFilterRules_TitleRegex(t *testing.T) {
	rules := []FilterRule{
		{Target: "title", Condition: "regex", Value: `^EP\d+`},
	}
	if !MatchesFilterRules(rules, VideoInfo{Title: "EP01 First Episode"}) {
		t.Error("expected match for regex EP01")
	}
	if MatchesFilterRules(rules, VideoInfo{Title: "No match here"}) {
		t.Error("expected no match for regex")
	}
}

func TestMatchesFilterRules_TitleRegex_Invalid(t *testing.T) {
	rules := []FilterRule{
		{Target: "title", Condition: "regex", Value: `[invalid`},
	}
	// Invalid regex should not filter (return true)
	if !MatchesFilterRules(rules, VideoInfo{Title: "anything"}) {
		t.Error("expected true for invalid regex")
	}
}

func TestMatchesFilterRules_DurationGt(t *testing.T) {
	rules := []FilterRule{
		{Target: "duration", Condition: "gt", Value: "300"},
	}
	if !MatchesFilterRules(rules, VideoInfo{Duration: 600}) {
		t.Error("expected match for duration > 300")
	}
	if MatchesFilterRules(rules, VideoInfo{Duration: 200}) {
		t.Error("expected no match for duration < 300")
	}
}

func TestMatchesFilterRules_DurationLt(t *testing.T) {
	rules := []FilterRule{
		{Target: "duration", Condition: "lt", Value: "60"},
	}
	if !MatchesFilterRules(rules, VideoInfo{Duration: 30}) {
		t.Error("expected match for duration < 60")
	}
	if MatchesFilterRules(rules, VideoInfo{Duration: 120}) {
		t.Error("expected no match for duration > 60")
	}
}

func TestMatchesFilterRules_DurationBetween(t *testing.T) {
	rules := []FilterRule{
		{Target: "duration", Condition: "between", Value: "60", Value2: "300"},
	}
	if !MatchesFilterRules(rules, VideoInfo{Duration: 120}) {
		t.Error("expected match for duration in [60, 300]")
	}
	if !MatchesFilterRules(rules, VideoInfo{Duration: 60}) {
		t.Error("expected match for duration == 60 (inclusive)")
	}
	if !MatchesFilterRules(rules, VideoInfo{Duration: 300}) {
		t.Error("expected match for duration == 300 (inclusive)")
	}
	if MatchesFilterRules(rules, VideoInfo{Duration: 10}) {
		t.Error("expected no match for duration < 60")
	}
	if MatchesFilterRules(rules, VideoInfo{Duration: 500}) {
		t.Error("expected no match for duration > 300")
	}
}

func TestMatchesFilterRules_PagesGt(t *testing.T) {
	rules := []FilterRule{
		{Target: "pages", Condition: "gt", Value: "1"},
	}
	if !MatchesFilterRules(rules, VideoInfo{Pages: 5}) {
		t.Error("expected match for pages > 1")
	}
	if MatchesFilterRules(rules, VideoInfo{Pages: 1}) {
		t.Error("expected no match for pages == 1")
	}
}

func TestMatchesFilterRules_TagsContains(t *testing.T) {
	rules := []FilterRule{
		{Target: "tags", Condition: "contains", Value: "golang"},
	}
	if !MatchesFilterRules(rules, VideoInfo{Tags: "golang,tutorial,programming"}) {
		t.Error("expected match for tags containing 'golang'")
	}
	if MatchesFilterRules(rules, VideoInfo{Tags: "python,java"}) {
		t.Error("expected no match for tags not containing 'golang'")
	}
}

func TestMatchesFilterRules_MultipleRules_AND(t *testing.T) {
	rules := []FilterRule{
		{Target: "title", Condition: "contains", Value: "教程"},
		{Target: "duration", Condition: "gt", Value: "300"},
	}
	// Both match
	if !MatchesFilterRules(rules, VideoInfo{Title: "Go教程", Duration: 600}) {
		t.Error("expected match when both rules pass")
	}
	// Title matches but duration doesn't
	if MatchesFilterRules(rules, VideoInfo{Title: "Go教程", Duration: 100}) {
		t.Error("expected no match when duration rule fails")
	}
	// Duration matches but title doesn't
	if MatchesFilterRules(rules, VideoInfo{Title: "Go入门", Duration: 600}) {
		t.Error("expected no match when title rule fails")
	}
}

func TestMatchesFilterRules_UnknownTarget(t *testing.T) {
	rules := []FilterRule{
		{Target: "unknown_field", Condition: "contains", Value: "test"},
	}
	// Unknown target should not filter
	if !MatchesFilterRules(rules, VideoInfo{Title: "anything"}) {
		t.Error("expected true for unknown target")
	}
}

func TestMatchesFilterRules_InvalidNumericValue(t *testing.T) {
	rules := []FilterRule{
		{Target: "duration", Condition: "gt", Value: "not_a_number"},
	}
	// Invalid numeric value should not filter
	if !MatchesFilterRules(rules, VideoInfo{Duration: 100}) {
		t.Error("expected true for invalid numeric value")
	}
}

func TestMatchesFilterRules_DurationBetween_InvalidValue2(t *testing.T) {
	rules := []FilterRule{
		{Target: "duration", Condition: "between", Value: "60", Value2: "not_a_number"},
	}
	// Invalid Value2 should not filter
	if !MatchesFilterRules(rules, VideoInfo{Duration: 100}) {
		t.Error("expected true for invalid Value2")
	}
}
