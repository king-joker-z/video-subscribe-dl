package bilibili

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// CookieRefreshResult Cookie 刷新结果
type CookieRefreshResult struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// RefreshCookie 尝试通过 B 站 refresh_token 刷新 Cookie
// cookiePath 是 Netscape 格式的 cookie.txt 路径
// 返回是否刷新成功
func (c *Client) RefreshCookie(cookiePath string) (*CookieRefreshResult, error) {
	if cookiePath == "" {
		return &CookieRefreshResult{Success: false, Message: "cookie 路径为空"}, nil
	}

	// 从 cookie 文件中提取 refresh_token（bili_jct 或 SESSDATA 通常伴随 refresh_token）
	refreshToken := extractRefreshToken(cookiePath)
	if refreshToken == "" {
		return &CookieRefreshResult{
			Success: false,
			Message: "cookie 文件中未找到 refresh_token，无法自动刷新",
		}, nil
	}

	// 从 cookie 中提取 csrf (bili_jct)
	csrf := extractCookieValue(c.cookie, "bili_jct")

	// 调用 B 站 Cookie 刷新接口
	form := url.Values{}
	form.Set("csrf", csrf)
	form.Set("refresh_csrf", csrf)
	form.Set("refresh_token", refreshToken)
	form.Set("source", "main_web")

	req, err := http.NewRequest("POST",
		"https://passport.bilibili.com/x/passport-login/web/cookie/refresh",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create refresh request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Status       int    `json:"status"`
			Timestamp    int64  `json:"timestamp"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	if result.Code != 0 {
		return &CookieRefreshResult{
			Success: false,
			Message: fmt.Sprintf("B站返回错误: code=%d, msg=%s", result.Code, result.Message),
		}, nil
	}

	// 从响应 Set-Cookie 中提取新的 cookie
	newCookies := extractSetCookies(resp)
	if len(newCookies) > 0 {
		// 更新 cookie 文件
		if err := updateCookieFile(cookiePath, newCookies); err != nil {
			log.Printf("[cookie-refresh] Update cookie file failed: %v", err)
			return &CookieRefreshResult{
				Success: false,
				Message: "刷新成功但更新文件失败: " + err.Error(),
			}, nil
		}

		// 更新新的 refresh_token 到文件
		if result.Data.RefreshToken != "" {
			saveRefreshToken(cookiePath, result.Data.RefreshToken)
		}

		// 更新内存中的 cookie
		newCookieStr := ReadCookieFile(cookiePath)
		if newCookieStr != "" {
			c.cookie = newCookieStr
		}

		log.Printf("[cookie-refresh] Cookie 刷新成功")
		return &CookieRefreshResult{
			Success:      true,
			Message:      "Cookie 刷新成功",
			RefreshToken: result.Data.RefreshToken,
		}, nil
	}

	return &CookieRefreshResult{
		Success: false,
		Message: "刷新请求成功但未收到新 Cookie",
	}, nil
}

// extractRefreshToken 从 cookie 文件同目录的 .refresh_token 文件或 cookie 注释中提取
func extractRefreshToken(cookiePath string) string {
	// 方法1: 从同目录 .refresh_token 文件读取
	dir := cookiePath[:strings.LastIndex(cookiePath, "/")+1]
	tokenFile := dir + ".refresh_token"
	if data, err := os.ReadFile(tokenFile); err == nil {
		token := strings.TrimSpace(string(data))
		if token != "" {
			return token
		}
	}

	// 方法2: 从 cookie 文件注释行中提取（# refresh_token=xxx）
	if data, err := os.ReadFile(cookiePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "# refresh_token=") {
				return strings.TrimPrefix(line, "# refresh_token=")
			}
		}
	}

	// 方法3: 从 cookie 值中查找 (有些导出工具会将 refresh_token 作为 cookie)
	if data, err := os.ReadFile(cookiePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) >= 7 && parts[5] == "refresh_token" {
				return parts[6]
			}
		}
	}

	return ""
}

// extractCookieValue 从 cookie 字符串中提取指定 key 的值
func extractCookieValue(cookieStr, key string) string {
	for _, part := range strings.Split(cookieStr, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, key+"=") {
			return strings.TrimPrefix(part, key+"=")
		}
	}
	return ""
}

// extractSetCookies 从 HTTP 响应中提取 Set-Cookie
func extractSetCookies(resp *http.Response) map[string]string {
	cookies := map[string]string{}
	for _, cookie := range resp.Cookies() {
		if cookie.Value != "" && cookie.MaxAge != 0 {
			cookies[cookie.Name] = cookie.Value
		}
	}
	return cookies
}

// updateCookieFile 更新 Netscape 格式 cookie 文件中的值
func updateCookieFile(cookiePath string, newValues map[string]string) error {
	data, err := os.ReadFile(cookiePath)
	if err != nil {
		return err
	}

	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			lines = append(lines, line)
			continue
		}
		parts := strings.Split(trimmed, "\t")
		if len(parts) >= 7 {
			if newVal, ok := newValues[parts[5]]; ok {
				parts[6] = newVal
				// 更新过期时间为30天后
				parts[4] = fmt.Sprintf("%d", time.Now().Add(30*24*time.Hour).Unix())
				line = strings.Join(parts, "\t")
				delete(newValues, parts[5])
			}
		}
		lines = append(lines, line)
	}

	// 追加新的 cookie 条目
	for name, value := range newValues {
		expiry := fmt.Sprintf("%d", time.Now().Add(30*24*time.Hour).Unix())
		line := fmt.Sprintf(".bilibili.com\tTRUE\t/\tFALSE\t%s\t%s\t%s", expiry, name, value)
		lines = append(lines, line)
	}

	return os.WriteFile(cookiePath, []byte(strings.Join(lines, "\n")), 0644)
}

// saveRefreshToken 保存新的 refresh_token 到文件
func saveRefreshToken(cookiePath, token string) {
	dir := cookiePath[:strings.LastIndex(cookiePath, "/")+1]
	tokenFile := dir + ".refresh_token"
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		log.Printf("[cookie-refresh] Save refresh_token failed: %v", err)
	}
}
