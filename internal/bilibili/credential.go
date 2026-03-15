package bilibili

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
		"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Credential B站登录凭证
type Credential struct {
	Sessdata    string `json:"sessdata"`
	BiliJCT     string `json:"bili_jct"`
	Buvid3      string `json:"buvid3"`
	DedeUserID  string `json:"dedeuserid"`
	ACTimeValue string `json:"ac_time_value"` // refresh_token
	UpdatedAt   int64  `json:"updated_at"`    // 上次更新时间戳
}

// IsEmpty 判断凭证是否为空
func (c *Credential) IsEmpty() bool {
	return c == nil || c.Sessdata == ""
}

// ToCookieString 转换为 Cookie 字符串（用于 HTTP 请求头）
func (c *Credential) ToCookieString() string {
	if c.IsEmpty() {
		return ""
	}
	parts := []string{
		"SESSDATA=" + c.Sessdata,
		"bili_jct=" + c.BiliJCT,
		"buvid3=" + c.Buvid3,
		"DedeUserID=" + c.DedeUserID,
	}
	if c.ACTimeValue != "" {
		parts = append(parts, "ac_time_value="+c.ACTimeValue)
	}
	return strings.Join(parts, "; ")
}

// ToJSON 序列化为 JSON（用于存储到 DB）
func (c *Credential) ToJSON() string {
	data, _ := json.Marshal(c)
	return string(data)
}

// CredentialFromJSON 从 JSON 反序列化
func CredentialFromJSON(s string) *Credential {
	if s == "" {
		return nil
	}
	var cred Credential
	if err := json.Unmarshal([]byte(s), &cred); err != nil {
		return nil
	}
	if cred.Sessdata == "" {
		return nil
	}
	return &cred
}

// CredentialFromCookieString 从 cookie 字符串中解析凭证
func CredentialFromCookieString(cookieStr string) *Credential {
	if cookieStr == "" {
		return nil
	}
	cred := &Credential{UpdatedAt: time.Now().Unix()}
	for _, part := range strings.Split(cookieStr, ";") {
		part = strings.TrimSpace(part)
		if idx := strings.Index(part, "="); idx > 0 {
			key := strings.TrimSpace(part[:idx])
			val := strings.TrimSpace(part[idx+1:])
			switch key {
			case "SESSDATA":
				cred.Sessdata = val
			case "bili_jct":
				cred.BiliJCT = val
			case "buvid3":
				cred.Buvid3 = val
			case "DedeUserID":
				cred.DedeUserID = val
			case "ac_time_value":
				cred.ACTimeValue = val
			}
		}
	}
	if cred.Sessdata == "" {
		return nil
	}
	return cred
}

// CredentialStatus 凭证状态
type CredentialStatus struct {
	HasCredential bool   `json:"has_credential"`
	Source        string `json:"source"`       // "qrcode", "cookie_file", "none"
	NeedRefresh   bool   `json:"need_refresh"`
	Username      string `json:"username,omitempty"`
	VIPLabel      string `json:"vip_label,omitempty"`
	MaxQuality    string `json:"max_quality,omitempty"`
	MaxAudio      string `json:"max_audio,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

// NeedRefresh 检查是否需要刷新 Cookie
// 调用 https://passport.bilibili.com/x/passport-login/web/cookie/info
func (c *Credential) NeedRefresh(httpClient *http.Client) (bool, error) {
	if c.IsEmpty() {
		return false, nil
	}

	req, err := http.NewRequest("GET", "https://passport.bilibili.com/x/passport-login/web/cookie/info", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Cookie", c.ToCookieString())

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("check cookie info: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Code int `json:"code"`
		Data struct {
			Refresh   bool  `json:"refresh"`
			Timestamp int64 `json:"timestamp"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("parse cookie info: %w", err)
	}
	if result.Code != 0 {
		// code != 0 说明 cookie 已失效，需要刷新
		return true, nil
	}
	return result.Data.Refresh, nil
}

// B站 RSA 公钥（用于 Cookie 刷新的 correspond path 加密）
const biliPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDLgd2OAkcGVtoE3ThUREbio0Eg
Uc/prcajMKXvkCKFCWhJYJcLkcM2DKKcSeFpD/j6Boy538YXnR6VhcuUJOhH2x71
nzPjfdTcqMz7djHKETKEIFRnREDEg/iCHZe7Fz7/tmMcBBmHpBnFw0GPvAd2GrC2
jbFJPYEq9a/ehLbVHQIDAQAB
-----END PUBLIC KEY-----`

// getCorrespondPath RSA 公钥加密 "refresh_{timestamp_ms}" 得到 hex 字符串
func getCorrespondPath(ts int64) (string, error) {
	block, _ := pem.Decode([]byte(biliPublicKeyPEM))
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM")
	}
	pubInterface, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse public key: %w", err)
	}
	pubKey, ok := pubInterface.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("not RSA public key")
	}

	plaintext := []byte(fmt.Sprintf("refresh_%d", ts))

	// PKCS1v15 加密
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, pubKey, plaintext)
	if err != nil {
		return "", fmt.Errorf("RSA encrypt: %w", err)
	}
	return hex.EncodeToString(ciphertext), nil
}

// 正则: 从 HTML 中提取 refresh CSRF
var reRefreshCSRF = regexp.MustCompile(`<div\s+id="1-name">([^<]+)</div>`)

// getRefreshCSRF 获取 refresh CSRF token
func (c *Credential) getRefreshCSRF(httpClient *http.Client, correspondPath string) (string, error) {
	reqURL := fmt.Sprintf("https://www.bilibili.com/correspond/1/%s", correspondPath)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Cookie", c.ToCookieString())

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get correspond page: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	matches := reRefreshCSRF.FindSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("refresh CSRF not found in correspond page")
	}
	return string(matches[1]), nil
}

// Refresh 执行完整的 Cookie 刷新流程
// 返回新的 Credential
func (c *Credential) Refresh(httpClient *http.Client) (*Credential, error) {
	if c.IsEmpty() {
		return nil, fmt.Errorf("credential is empty, cannot refresh")
	}
	if c.ACTimeValue == "" {
		return nil, fmt.Errorf("no refresh_token (ac_time_value), cannot refresh")
	}

	// Step 1: 获取 correspond_path
	ts := time.Now().UnixMilli()
	correspondPath, err := getCorrespondPath(ts)
	if err != nil {
		return nil, fmt.Errorf("get correspond path: %w", err)
	}

	// Step 2: 获取 refresh_csrf
	refreshCSRF, err := c.getRefreshCSRF(httpClient, correspondPath)
	if err != nil {
		return nil, fmt.Errorf("get refresh csrf: %w", err)
	}

	// Step 3: 刷新 Cookie
	form := url.Values{}
	form.Set("csrf", c.BiliJCT)
	form.Set("refresh_csrf", refreshCSRF)
	form.Set("refresh_token", c.ACTimeValue)
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
	req.Header.Set("Cookie", c.ToCookieString())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Status       int    `json:"status"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("refresh failed: code=%d, msg=%s", result.Code, result.Message)
	}

	// 从 Set-Cookie 提取新凭证
	newCred := &Credential{
		Buvid3:      c.Buvid3, // buvid3 不变
		DedeUserID:  c.DedeUserID,
		ACTimeValue: result.Data.RefreshToken,
		UpdatedAt:   time.Now().Unix(),
	}

	for _, cookie := range resp.Cookies() {
		switch cookie.Name {
		case "SESSDATA":
			newCred.Sessdata = cookie.Value
		case "bili_jct":
			newCred.BiliJCT = cookie.Value
		case "DedeUserID":
			newCred.DedeUserID = cookie.Value
		}
	}

	if newCred.Sessdata == "" {
		return nil, fmt.Errorf("refresh succeeded but no new SESSDATA in Set-Cookie")
	}

	// Step 4: 确认刷新
	if err := newCred.confirmRefresh(httpClient, c.ACTimeValue); err != nil {
		log.Printf("[credential] confirm refresh warning: %v", err)
		// 不阻塞，新凭证已经可用
	}

	log.Printf("[credential] Cookie 刷新成功 (new refresh_token=%s...)", truncateStr(newCred.ACTimeValue, 8))
	return newCred, nil
}

// confirmRefresh 确认刷新（用新凭证 + 旧 refresh_token）
func (c *Credential) confirmRefresh(httpClient *http.Client, oldRefreshToken string) error {
	form := url.Values{}
	form.Set("csrf", c.BiliJCT)
	form.Set("refresh_token", oldRefreshToken)

	req, err := http.NewRequest("POST",
		"https://passport.bilibili.com/x/passport-login/web/confirm/refresh",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Cookie", c.ToCookieString())

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("confirm refresh: code=%d, msg=%s", result.Code, result.Message)
	}
	return nil
}

// GetBuvid3 调用 API 获取 buvid3
func GetBuvid3(httpClient *http.Client) (string, error) {
	req, err := http.NewRequest("GET", "https://api.bilibili.com/x/frontend/finger/spi", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Code int `json:"code"`
		Data struct {
			B3 string `json:"b_3"`
			B4 string `json:"b_4"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("get buvid3: code=%d", result.Code)
	}
	return result.Data.B3, nil
}

// truncateStr 截断字符串用于日志
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// rsaEncryptOAEP is not needed, B站用的是 PKCS1v15
// 保留 getCorrespondPath 中的 EncryptPKCS1v15 实现即可

// CredentialFromCookieFile 从 cookie.txt 文件解析为 Credential
func CredentialFromCookieFile(path string) *Credential {
	cookieStr := ReadCookieFile(path)
	if cookieStr == "" {
		return nil
	}
	cred := CredentialFromCookieString(cookieStr)
	if cred == nil {
		return nil
	}
	// 尝试从同目录加载 refresh_token
	if cred.ACTimeValue == "" {
		cred.ACTimeValue = extractRefreshToken(path)
	}
	cred.UpdatedAt = time.Now().Unix()
	return cred
}

// VerifyCredential 验证凭证并返回详细状态
func VerifyCredential(cred *Credential, httpClient *http.Client) (*CookieVerifyResult, error) {
	if cred == nil || cred.IsEmpty() {
		return &CookieVerifyResult{LoggedIn: false}, nil
	}
	// 构造临时 client 验证
	client := NewClientWithCredential(cred)
	return client.VerifyCookie()
}
