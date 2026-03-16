package douyin

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// cookieManager 管理抖音 Cookie（msToken + ttwid + odin_tt）
// 参考实现:
// - lux: ttwid 从 bytedance.com Set-Cookie 获取, msToken 随机生成
// - parse-video: 使用 iesdouyin.com 页面不需要复杂 Cookie
// - Evil0ctal: 完整 a_bogus 签名, 我们暂不需要
type cookieManager struct {
	mu      sync.Mutex
	ttwid   string
	ttwidAt time.Time
}

var globalCookieMgr = &cookieManager{}

const ttwidTTL = 2 * time.Hour

// 固定 odin_tt（参考 lux 的做法，这个值变化不频繁）
const fixedOdinTT = "324fb4ea4a89c0c05827e18a1ed9cf9bf8a17f7705fcc793fec935b637867e2a5a9b8168c885554d029919117a18ba69"

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

	req, err := http.NewRequest("POST", "https://ttwid.bytedance.com/ttwid/union/register/", strings.NewReader(string(payloadBytes)))
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
// 格式: msToken=xxx;ttwid=xxx;odin_tt=xxx;
func (cm *cookieManager) getCookieString(httpClient *http.Client) string {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	msToken := generateMsToken()

	// ttwid 缓存（有 TTL）
	if cm.ttwid == "" || time.Since(cm.ttwidAt) > ttwidTTL {
		ttwid, err := fetchTTWID(httpClient)
		if err != nil {
			log.Printf("[douyin] fetchTTWID failed: %v", err)
		} else if ttwid != "" {
			cm.ttwid = ttwid
			cm.ttwidAt = time.Now()
			log.Printf("[douyin] ttwid refreshed, len=%d", len(ttwid))
		}
	}

	parts := []string{fmt.Sprintf("msToken=%s", msToken)}
	if cm.ttwid != "" {
		parts = append(parts, fmt.Sprintf("ttwid=%s", cm.ttwid))
	}
	parts = append(parts, fmt.Sprintf("odin_tt=%s", fixedOdinTT))
	return strings.Join(parts, "; ")
}
