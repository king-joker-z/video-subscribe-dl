package douyin

import (
	"strings"
	"testing"
)

// TestParseLongURL_VideoFormats 测试各种抖音长链接格式的解析
func TestParseLongURL_VideoFormats(t *testing.T) {
	c := NewClient()
	defer c.limiter.Stop()

	tests := []struct {
		name    string
		url     string
		wantType URLType
		wantID  string
	}{
		{
			name:     "douyin.com/video/xxx",
			url:      "https://www.douyin.com/video/7234567890123456789",
			wantType: URLTypeVideo,
			wantID:   "7234567890123456789",
		},
		{
			name:     "iesdouyin.com/share/video/xxx",
			url:      "https://www.iesdouyin.com/share/video/7234567890123456789",
			wantType: URLTypeVideo,
			wantID:   "7234567890123456789",
		},
		{
			name:     "douyin.com/note/xxx",
			url:      "https://www.douyin.com/note/7234567890123456789",
			wantType: URLTypeVideo,
			wantID:   "7234567890123456789",
		},
		{
			name:     "modal_id parameter",
			url:      "https://www.douyin.com/user/xxx?modal_id=7234567890123456789",
			wantType: URLTypeVideo,
			wantID:   "7234567890123456789",
		},
		{
			name:     "bare numeric path",
			url:      "https://www.douyin.com/7234567890123456789",
			wantType: URLTypeVideo,
			wantID:   "7234567890123456789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := c.parseLongURL(tt.url)
			if err != nil {
				t.Fatalf("parseLongURL(%q) error: %v", tt.url, err)
			}
			if result.Type != tt.wantType {
				t.Errorf("Type = %d, want %d", result.Type, tt.wantType)
			}
			if result.VideoID != tt.wantID {
				t.Errorf("VideoID = %q, want %q", result.VideoID, tt.wantID)
			}
		})
	}
}

// TestParseLongURL_UserFormat 测试用户链接格式
func TestParseLongURL_UserFormat(t *testing.T) {
	c := NewClient()
	defer c.limiter.Stop()

	url := "https://www.douyin.com/user/MS4wLjABAAAAxyz123"
	result, err := c.parseLongURL(url)
	if err != nil {
		t.Fatalf("parseLongURL(%q) error: %v", url, err)
	}
	if result.Type != URLTypeUser {
		t.Errorf("Type = %d, want %d", result.Type, URLTypeUser)
	}
	if result.SecUID != "MS4wLjABAAAAxyz123" {
		t.Errorf("SecUID = %q, want %q", result.SecUID, "MS4wLjABAAAAxyz123")
	}
}

// TestParseLongURL_Unrecognized 测试无法识别的链接
func TestParseLongURL_Unrecognized(t *testing.T) {
	c := NewClient()
	defer c.limiter.Stop()

	_, err := c.parseLongURL("https://www.example.com/something")
	if err == nil {
		t.Error("expected error for unrecognized URL, got nil")
	}
}

// TestExtractVideoID 测试从各种 URL 提取视频 ID
func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		wantID string
	}{
		{"reVideoURL", "https://www.douyin.com/video/7234567890123456789", "7234567890123456789"},
		{"reIesVideoURL", "https://www.iesdouyin.com/share/video/7234567890123456789", "7234567890123456789"},
		{"rePathVideoID /video/", "https://www.douyin.com/video/7234567890123456789?extra=1", "7234567890123456789"},
		{"rePathVideoID /note/", "https://www.douyin.com/note/7234567890123456789", "7234567890123456789"},
		{"reModalID", "https://www.douyin.com/user/xxx?modal_id=7234567890123456789", "7234567890123456789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id string
			if m := reModalID.FindStringSubmatch(tt.url); len(m) > 1 {
				id = m[1]
			} else if m := reVideoURL.FindStringSubmatch(tt.url); len(m) > 1 {
				id = m[1]
			} else if m := reIesVideoURL.FindStringSubmatch(tt.url); len(m) > 1 {
				id = m[1]
			} else if m := rePathVideoID.FindStringSubmatch(tt.url); len(m) > 1 {
				id = m[1]
			}
			if id != tt.wantID {
				t.Errorf("extracted ID = %q, want %q", id, tt.wantID)
			}
		})
	}
}

// TestExtractSecUID 测试从 URL 提取 sec_user_id
func TestExtractSecUID(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantUID string
		wantErr bool
	}{
		{
			name:    "standard user URL",
			url:     "https://www.douyin.com/user/MS4wLjABAAAAxyz123",
			wantUID: "MS4wLjABAAAAxyz123",
		},
		{
			name:    "user URL with query params",
			url:     "https://www.douyin.com/user/MS4wLjABAAAAabc456?tab=post",
			wantUID: "MS4wLjABAAAAabc456",
		},
		{
			name:    "not a user URL",
			url:     "https://www.douyin.com/video/123456",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uid, err := ExtractSecUID(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractSecUID(%q) error: %v", tt.url, err)
			}
			if uid != tt.wantUID {
				t.Errorf("SecUID = %q, want %q", uid, tt.wantUID)
			}
		})
	}
}

// TestIsDouyinURL 测试抖音 URL 识别
func TestIsDouyinURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.douyin.com/video/123", true},
		{"https://v.douyin.com/abc123", true},
		{"https://www.iesdouyin.com/share/video/123", true},
		{"https://www.bilibili.com/video/BV123", false},
		{"https://www.youtube.com/watch?v=abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := IsDouyinURL(tt.url)
			if got != tt.want {
				t.Errorf("IsDouyinURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

// TestSanitizePath 测试路径清理
func TestSanitizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// 基础非法字符
		{"normal name", "normal name"},
		{"with/slash", "with_slash"},
		{"with:colon", "with_colon"},
		{"with*star", "with_star"},
		{"with?question", "with_question"},
		{"with<angle>brackets", "with_angle_brackets"},
		{`with"quotes`, "with_quotes"},
		{"with|pipe", "with_pipe"},
		{"with\\backslash", "with_backslash"},
		// 控制字符（C0: 0x00-0x1F, 包括 \n \r \t）
		{"with\nnewline", "withnewline"},
		{"with\rcarriage", "withcarriage"},
		{"with\ttab", "withtab"},
		{"hello\x00world", "helloworld"},
		// DEL (0x7F)
		{"hello\x7fworld", "helloworld"},
		// C1 控制字符 (0x80-0x9F)
		{"hello\u0085world", "helloworld"},
		{"hello\u008Aworld", "helloworld"},
		// 零宽字符 (0x200B-0x200F)
		{"hello\u200Bworld", "helloworld"},       // zero-width space
		{"hello\u200Dworld", "helloworld"},       // zero-width joiner
		{"hello\u200Fworld", "helloworld"},       // right-to-left mark
		// BOM (0xFEFF)
		{"\uFEFFhello world", "hello world"},
		// 变体选择器 (0xFE00-0xFE0F)
		{"hello\uFE0Fworld", "helloworld"},
		// 替换字符 (0xFFFD)
		{"hello\uFFFDworld", "helloworld"},
		// 行/段分隔符 (0x2028-0x202F)
		{"hello\u2028world", "helloworld"},
		// 不可见格式字符 (0x2060-0x206F)
		{"hello\u2060world", "helloworld"},       // word joiner
		// 连续空格压缩
		{"hello   world", "hello world"},
		{"a  b   c    d", "a b c d"},
		// 特殊值
		{"", "unknown"},
		{"   ", "unknown"},
		{".", "unknown"},
		{"..", "unknown"},
		// 80字符截断
		{strings.Repeat("あ", 100), strings.Repeat("あ", 80)},
		{strings.Repeat("a", 81), strings.Repeat("a", 80)},
		{strings.Repeat("a", 80), strings.Repeat("a", 80)},   // exactly 80: no truncation
		{strings.Repeat("a", 79), strings.Repeat("a", 79)},   // under 80: no truncation
		// Emoji 和 Supplementary Planes（NAS 兼容性）
		{"hello😀world", "helloworld"},          // U+1F600 grinning face emoji
		{"hello📺世界", "hello世界"},              // emoji 在中间，中文保留
		{"🔥火🔥", "火"},                 // 前后 emoji，中间汉字保留
		{"混😊合👍内容", "混合内容"},      // 多个 emoji 混入中文
		{"纯中文内容🎉", "纯中文内容"},             // 末尾 emoji
		{"🎉纯中文内容", "纯中文内容"},             // 开头 emoji
		// BMP 内 Symbol Other（❄☃⛄✨⭐ 等，NAS 兼容性）
		{"❄️", "unknown"},                         // U+2744 snowflake + variation selector → empty → unknown
		{"☃", "unknown"},                               // U+2603 snowman
		{"⛄", "unknown"},                               // U+26C4 snowman without snow
		{"✨", "unknown"},                               // U+2728 sparkles
		{"⭐", "unknown"},                               // U+2B50 star
		{"#宅家才是冬❄️", "#宅家才是冬"},           // hashtag 中 emoji 被过滤
		{"阿彪 #美的全屋智能 #宅家才是冬❄️", "阿彪 #美的全屋智能 #宅家才是冬"}, // 实际场景
		// 中文标点不被误杀
		{"【我的主人有点疯5】", "【我的主人有点疯5】"},
		{"《红楼梦》", "《红楼梦》"},
		{"「你好」世界", "「你好」世界"},
		{"（括号）测试", "（括号）测试"},
		// 混合场景
		{"hello/world\u200B test\n", "hello_world test"},
		{"\uFEFF  多余空格  \u200B", "多余空格"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizePath(tt.input)
			if got != tt.want {
				t.Errorf("SanitizePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSignURL_Pool 测试签名池基本工作
func TestSignURL_Pool(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36"
	query := "aweme_id=7234567890123456789"

	result, err := signURL(query, ua)
	if err != nil {
		t.Fatalf("signURL() error: %v", err)
	}
	if result == "" {
		t.Error("signURL() returned empty string")
	}
	t.Logf("X-Bogus: %s", result)

	// 多次调用确认池复用正常
	for i := 0; i < 10; i++ {
		r, err := signURL(query, ua)
		if err != nil {
			t.Fatalf("signURL() call %d error: %v", i, err)
		}
		if r == "" {
			t.Errorf("signURL() call %d returned empty string", i)
		}
	}
}

// TestPickUA 测试 UA 池随机选择
func TestPickUA(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		ua := pickUA()
		if ua == "" {
			t.Fatal("pickUA() returned empty string")
		}
		seen[ua] = true
	}
	// 应该至少选中了多个不同的 UA（池有 10 个）
	if len(seen) < 2 {
		t.Errorf("pickUA() returned only %d unique UAs in 100 calls, expected diversity", len(seen))
	}
}

// TestPickPCUA 测试 PC UA 池
func TestPickPCUA(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		ua := pickPCUA()
		if ua == "" {
			t.Fatal("pickPCUA() returned empty string")
		}
		seen[ua] = true
	}
	if len(seen) < 2 {
		t.Errorf("pickPCUA() returned only %d unique UAs", len(seen))
	}
}

// TestExtractChromeVersion 测试 Chrome 版本提取
func TestExtractChromeVersion(t *testing.T) {
	tests := []struct {
		ua   string
		want string
	}{
		{"Mozilla/5.0 ... Chrome/131.0.0.0 Safari/537.36", "131"},
		{"Mozilla/5.0 ... Chrome/130.0.0.0 Safari/537.36", "130"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_3 ...) Safari/604.1", ""},
	}

	for _, tt := range tests {
		got := extractChromeVersion(tt.ua)
		if got != tt.want {
			t.Errorf("extractChromeVersion(%q) = %q, want %q", tt.ua, got, tt.want)
		}
	}
}
