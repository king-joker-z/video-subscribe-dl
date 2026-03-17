package douyin

import (
	"fmt"
	"strings"
)

// ValidateCookie 验证抖音 Cookie 是否可用
// 只做字段格式检查，不发网络请求：
//   - GetUserProfile 接口对签名敏感，偶发空 body 导致误报失败
//   - 视频能正常下载本身已证明 Cookie 有效，无需再探测
//
// 注意：这是抖音独有的验证方法，和 B 站（bilibili）的 Cookie 体系完全分开。
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

	return true, fmt.Sprintf("Cookie 格式有效 (长度: %d)", len(cookie))
}
