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
