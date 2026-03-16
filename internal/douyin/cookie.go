package douyin

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// cookieManager 管理抖音 Cookie（msToken + ttwid）
type cookieManager struct {
	mu      sync.Mutex
	ttwid   string
	ttwidAt time.Time
}

var globalCookieMgr = &cookieManager{}

const ttwidTTL = 2 * time.Hour

// generateMsToken 生成随机 msToken（107 位 base64 字符串）
func generateMsToken() string {
	b := make([]byte, 80)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)[:107]
}

// fetchTTWID 通过抖音 ttwid API 获取 ttwid cookie
func fetchTTWID(httpClient *http.Client) (string, error) {
	payload := `{"region":"cn","aid":1768,"needFid":false,"service":"www.ixigua.com","migrate_priority":0,"cbUrlProtocol":"https","union":true,"channel":"channel_pc_web","fid":""}`
	req, err := http.NewRequest("POST", "https://ttwid.bytedance.com/ttwid/union/register/", strings.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", douyinUA)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch ttwid: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	for _, c := range resp.Cookies() {
		if c.Name == "ttwid" {
			return c.Value, nil
		}
	}

	// 解析 body 中的 ttwid
	return "", fmt.Errorf("ttwid not found in response cookies")
}

// getCookieString 返回抖音请求所需的 Cookie 字符串
func (cm *cookieManager) getCookieString(httpClient *http.Client) string {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	msToken := generateMsToken()

	// ttwid 缓存
	if cm.ttwid == "" || time.Since(cm.ttwidAt) > ttwidTTL {
		ttwid, err := fetchTTWID(httpClient)
		if err == nil && ttwid != "" {
			cm.ttwid = ttwid
			cm.ttwidAt = time.Now()
		}
	}

	parts := []string{fmt.Sprintf("msToken=%s", msToken)}
	if cm.ttwid != "" {
		parts = append(parts, fmt.Sprintf("ttwid=%s", cm.ttwid))
	}
	return strings.Join(parts, "; ")
}

// ttwidResponse 用于解析 ttwid API 响应
type ttwidResponse struct {
	StatusCode int    `json:"status_code"`
	Data       string `json:"data"`
}

// parseTTWIDFromBody 尝试从 response body 解析 ttwid
func parseTTWIDFromBody(body []byte) string {
	var resp ttwidResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	return resp.Data
}
