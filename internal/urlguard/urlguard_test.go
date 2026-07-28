package urlguard

import "testing"

func TestParsePlatformURL(t *testing.T) {
	valid := []struct {
		raw      string
		platform Platform
	}{
		{"https://v.douyin.com/abc/", Douyin},
		{"https://www.douyin.com/video/123", Douyin},
		{"https://b23.tv/abc", Bilibili},
		{"https://space.bilibili.com/1", Bilibili},
		{"https://www.pornhub.com/model/test", Pornhub},
		{"https://xchina.co/model/id-1", XChina},
	}
	for _, tt := range valid {
		if _, err := ParsePlatformURL(tt.raw, tt.platform); err != nil {
			t.Fatalf("expected valid URL %q: %v", tt.raw, err)
		}
	}

	invalid := []string{
		"http://v.douyin.com/abc",
		"https://douyin.com.evil/video/123",
		"https://v.douyin.com@127.0.0.1/video/123",
		"https://127.0.0.1/?douyin.com",
		"https://[::1]/",
		"https://169.254.169.254/latest/meta-data/",
		"https://v.douyin.com:8443/abc",
	}
	for _, raw := range invalid {
		if _, err := ParsePlatformURL(raw, Douyin); err == nil {
			t.Fatalf("expected unsafe URL to be rejected: %q", raw)
		}
	}
}
