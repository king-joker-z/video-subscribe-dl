package douyin

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// cookieManager 管理抖音 Cookie（msToken + ttwid + odin_tt + bd_ticket_guard_client_data + verify_fp + s_v_web_id）
// 参考实现:
// - lux: ttwid 从 bytedance.com Set-Cookie 获取, msToken 随机生成, bd_ticket_guard_client_data 硬编码
// - parse-video: 使用 iesdouyin.com 页面不需要复杂 Cookie
// - Evil0ctal: verify_fp / s_v_web_id 随机字符串
type cookieManager struct {
	mu      sync.Mutex
	ttwid   string
	ttwidAt time.Time
}

var globalCookieMgr = &cookieManager{}

const ttwidTTL = 2 * time.Hour

// 固定 odin_tt（参考 lux 的做法，这个值变化不频繁）
const fixedOdinTT = "324fb4ea4a89c0c05827e18a1ed9cf9bf8a17f7705fcc793fec935b637867e2a5a9b8168c885554d029919117a18ba69"

// 固定 bd_ticket_guard_client_data（参考 lux createCookie，base64 编码的 ticket guard 证书请求）
const fixedBdTicketGuardClientData = "eyJiZC10aWNrZXQtZ3VhcmQtdmVyc2lvbiI6MiwiYmQtdGlja2V0LWd1YXJkLWNsaWVudC1jc3IiOiItLS0tLUJFR0lOIENFUlRJRklDQVRFIFJFUVVFU1QtLS0tLVxyXG5NSUlCRFRDQnRRSUJBREFuTVFzd0NRWURWUVFHRXdKRFRqRVlNQllHQTFVRUF3d1BZbVJmZEdsamEyVjBYMmQxXHJcbllYSmtNRmt3RXdZSEtvWkl6ajBDQVFZSUtvWkl6ajBEQVFjRFFnQUVKUDZzbjNLRlFBNUROSEcyK2F4bXAwNG5cclxud1hBSTZDU1IyZW1sVUE5QTZ4aGQzbVlPUlI4NVRLZ2tXd1FJSmp3Nyszdnc0Z2NNRG5iOTRoS3MvSjFJc3FBc1xyXG5NQ29HQ1NxR1NJYjNEUUVKRGpFZE1Cc3dHUVlEVlIwUkJCSXdFSUlPZDNkM0xtUnZkWGxwYmk1amIyMHdDZ1lJXHJcbktvWkl6ajBFQXdJRFJ3QXdSQUlnVmJkWTI0c0RYS0c0S2h3WlBmOHpxVDRBU0ROamNUb2FFRi9MQnd2QS8xSUNcclxuSURiVmZCUk1PQVB5cWJkcytld1QwSDZqdDg1czZZTVNVZEo5Z2dmOWlmeTBcclxuLS0tLS1FTkQgQ0VSVElGSUNBVEUgUkVRVUVTVC0tLS0tXHJcbiJ9"

// generateVerifyFp 生成 verify_fp / s_v_web_id
// 格式: verify_ + 13位随机字符串（参考 Evil0ctal 方案）
func generateVerifyFp() string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 13)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return "verify_" + string(b)
}

// generateMsToken 生成随机 msToken（107 位随机字母数字）
// 参考 lux: 从 [A-Za-z0-9] 字符集随机选取
func generateMsToken() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 107)
	randomBytes := make([]byte, 107)
	rand.Read(randomBytes)
	for i, rb := range randomBytes {
		b[i] = chars[int(rb)%len(chars)]
	}
	return string(b)
}

// fetchRealMsToken 从 mssdk 获取真实 msToken
// 优先使用真实 token，失败降级为随机生成
func fetchRealMsToken(httpClient *http.Client) string {
	req, err := http.NewRequest("POST", MsTokenAPI, strings.NewReader("{}"))
	if err != nil {
		logger.Warn("msToken request build failed, using random token", "error", err)
		return generateMsToken()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", pickUA())

	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("msToken fetch failed, using random token", "error", err)
		return generateMsToken()
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Warn("msToken read failed, using random token", "error", err)
		return generateMsToken()
	}

	var result struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Data == "" {
		// 尝试直接从 body 取（有些接口直接返回 token 字符串）
		token := strings.TrimSpace(string(body))
		if len(token) >= 100 && len(token) <= 200 {
			logger.Info("msToken fetched from raw body", "len", len(token))
			return token
		}
		logger.Warn("msToken parse failed or empty, using random token", "body", truncate(string(body), 100))
		return generateMsToken()
	}

	logger.Info("msToken fetched from mssdk", "len", len(result.Data))
	return result.Data
}

// fetchTTWID 通过 bytedance ttwid API 获取 ttwid
// 参考 lux: 从 response 的 Set-Cookie header 中提取 ttwid
func fetchTTWID(httpClient *http.Client) (string, error) {
	payload := map[string]interface{}{
		"aid":           1768,
		"union":         true,
		"needFid":       false,
		"region":        "cn",
		"cbUrlProtocol": "https",
		"service":       "www.ixigua.com",
		"migrate_info":  map[string]string{"ticket": "", "source": "node"},
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", TTWidAPI, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", pickUA())

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch ttwid: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) // drain

	// 从 Set-Cookie header 提取 ttwid（lux 的方式，比解析 body 更可靠）
	setCookie := resp.Header.Get("Set-Cookie")
	if setCookie != "" {
		re := regexp.MustCompile(`ttwid=([^;]+)`)
		if m := re.FindStringSubmatch(setCookie); len(m) > 1 {
			return m[1], nil
		}
	}

	// 备选: 从 cookies 中提取
	for _, c := range resp.Cookies() {
		if c.Name == "ttwid" {
			return c.Value, nil
		}
	}

	return "", fmt.Errorf("ttwid not found in response")
}

// getCookieString 返回抖音请求所需的 Cookie 字符串
// 格式: msToken=xxx; ttwid=xxx; odin_tt=xxx; bd_ticket_guard_client_data=xxx; verify_fp=xxx; s_v_web_id=xxx
func (cm *cookieManager) getCookieString(httpClient *http.Client) string {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// msToken: 优先真实 token，失败降级随机
	msToken := fetchRealMsToken(httpClient)

	// ttwid 缓存（有 TTL）
	if cm.ttwid == "" || time.Since(cm.ttwidAt) > ttwidTTL {
		ttwid, err := fetchTTWID(httpClient)
		if err != nil {
			logger.Warn("fetchTTWID failed", "error", err)
		} else if ttwid != "" {
			cm.ttwid = ttwid
			cm.ttwidAt = time.Now()
			logger.Info("ttwid refreshed", "len", len(ttwid))
		}
	}

	// verify_fp 和 s_v_web_id 每次生成（轻量操作，无需缓存）
	verifyFp := generateVerifyFp()
	sVWebID := generateVerifyFp() // 同格式，不同值

	parts := []string{
		fmt.Sprintf("msToken=%s", msToken),
	}
	if cm.ttwid != "" {
		parts = append(parts, fmt.Sprintf("ttwid=%s", cm.ttwid))
	}
	parts = append(parts,
		fmt.Sprintf("odin_tt=%s", fixedOdinTT),
		fmt.Sprintf("bd_ticket_guard_client_data=%s", fixedBdTicketGuardClientData),
		fmt.Sprintf("verify_fp=%s", verifyFp),
		fmt.Sprintf("s_v_web_id=%s", sVWebID),
	)

	cookie := strings.Join(parts, "; ")

	// Cookie 完整性日志
	fields := []string{"msToken", "ttwid", "odin_tt", "bd_ticket_guard_client_data", "verify_fp", "s_v_web_id"}
	var missing []string
	for _, f := range fields {
		if !strings.Contains(cookie, f+"=") {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		logger.Warn("cookie incomplete", "missingFields", missing)
	} else {
		logger.Info("cookie complete", "fields", len(fields), "len", len(cookie))
	}

	return cookie
}
