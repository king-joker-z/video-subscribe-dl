package douyin

import (
	"fmt"
	"strings"
	"time"
)

// ValidateCookie 验证抖音 Cookie 是否可用
// 通过检查必要字段并发送一个轻量的 API 请求来验证
//
// 注意：这是抖音独有的验证方法，和 B 站（bilibili）的 Cookie 体系完全分开。
// B 站使用 SESSDATA、bili_jct 等字段，不要混用。
//
// 返回 (valid bool, message string)
func (c *DouyinClient) ValidateCookie() (bool, string) {
	cookie := c.getSessionCookie()
	if cookie == "" {
		return false, "Cookie 为空"
	}

	// 检查必要字段（抖音 Cookie 必须包含 msToken 和 ttwid）
	required := []string{"msToken", "ttwid"}
	for _, field := range required {
		if !strings.Contains(cookie, field+"=") {
			return false, fmt.Sprintf("缺少必要字段: %s", field)
		}
	}

	// 尝试调用 GetUserProfile 验证 Cookie 是否有效
	// 使用一个已知存在的公开用户 secUID 做探测
	// 抖音 API 偶发返回空 body，最多重试 3 次
	testSecUID := "MS4wLjABAAAAgfP-wrR4bAf4EpXE01yHQEk4Sd0yoJ0zPyEJn1T29b4"
	var profile *DouyinUserProfile
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		profile, err = c.GetUserProfile(testSecUID)
		if err == nil {
			break
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}
	if err != nil {
		return false, fmt.Sprintf("Cookie 验证失败: %v", err)
	}
	if profile.Nickname == "" {
		return false, "Cookie 验证失败: 无法获取用户信息"
	}

	return true, fmt.Sprintf("Cookie 有效 (测试用户: %s)", profile.Nickname)
}
