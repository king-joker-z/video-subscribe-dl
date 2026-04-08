package bilibili

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/bits"
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
	Buvid4      string `json:"buvid4"`       // 设备指纹（与 buvid3 配合使用，需 ExClimbWuzhi 激活）
	DedeUserID  string `json:"dedeuserid"`
	ACTimeValue string `json:"ac_time_value"` // refresh_token
	UpdatedAt   int64  `json:"updated_at"`    // 上次更新时间戳
}

// IsEmpty 判断凭证是否为空
func (c *Credential) IsEmpty() bool {
	return c == nil || c.Sessdata == ""
}

// ToCookieString 转换为 Cookie 字符串（用于 HTTP 请求头）
func (c *Credential) getUA() string {
	return randUA()
}

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
	if c.Buvid4 != "" {
		parts = append(parts, "buvid4="+c.Buvid4)
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
			case "buvid4":
				cred.Buvid4 = val
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
	Source        string `json:"source"` // "qrcode", "cookie_file", "none"
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
	req.Header.Set("User-Agent", c.getUA())
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
nzPjfdTcqMz7djHum0qSZA0AyCBDABUqCrfNgCiJ00Ra7GmRj+YCK1NJEuewlb40
JNrRuoEUXpabUzGB8QIDAQAB
-----END PUBLIC KEY-----`

// getCorrespondPath RSA 公钥加密 "refresh_{timestamp_ms}" 得到 hex 字符串
// 使用 OAEP(SHA256) 加密，与 B 站最新实现一致
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

	// OAEP(SHA256) 加密，与 bili-sync Rust 实现一致
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pubKey, plaintext, nil)
	if err != nil {
		return "", fmt.Errorf("RSA OAEP encrypt: %w", err)
	}
	return hex.EncodeToString(ciphertext), nil
}

// 正则: 从 HTML 中提取 refresh CSRF
var reRefreshCSRF = regexp.MustCompile(`<div\s+id="1-name">([^<]+)</div>`)

// getRefreshCSRF 获取 refresh CSRF token（带 retry）
func (c *Credential) getRefreshCSRF(httpClient *http.Client, correspondPath string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			log.Printf("[credential] refresh CSRF 重试第 %d 次...", attempt)
			time.Sleep(2 * time.Second)
		}

		csrf, err := c.doGetRefreshCSRF(httpClient, correspondPath)
		if err == nil {
			return csrf, nil
		}
		lastErr = err
		log.Printf("[credential] getRefreshCSRF attempt %d failed: %v", attempt+1, err)
	}
	return "", lastErr
}

func (c *Credential) doGetRefreshCSRF(httpClient *http.Client, correspondPath string) (string, error) {
	reqURL := fmt.Sprintf("https://www.bilibili.com/correspond/1/%s", correspondPath)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.getUA())
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Cookie", c.ToCookieString())

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get correspond page: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// 详细日志：记录状态码和 body 前 500 字符，便于排查
	bodyPreview := string(body)
	if len(bodyPreview) > 500 {
		bodyPreview = bodyPreview[:500]
	}
	log.Printf("[credential] correspond page: status=%d, body_len=%d, preview=%s",
		resp.StatusCode, len(body), bodyPreview)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("correspond page HTTP %d", resp.StatusCode)
	}

	matches := reRefreshCSRF.FindSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("refresh CSRF not found in correspond page (status=%d, body_len=%d)", resp.StatusCode, len(body))
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
	// 提前 20 秒，避免客户端时间比服务器快导致失败（与 bili-sync 一致）
	ts := time.Now().UnixMilli() - 20000
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
	req.Header.Set("User-Agent", c.getUA())
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
		Buvid3:      c.Buvid3, // buvid3/buvid4 不变（设备指纹）
		Buvid4:      c.Buvid4,
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

	// Step 5: 重新激活 buvid
	// Cookie 刷新后服务端 session 已更换，需重新激活设备指纹，否则新 session 与 buvid 不匹配会触发 -352 风控
	if newCred.Buvid3 != "" {
		log.Printf("[credential] 重新激活 buvid...")
		if activateErr := ActivateBuvid(httpClient, newCred.Buvid3, newCred.Buvid4); activateErr != nil {
			log.Printf("[credential] buvid 激活失败（非致命）: %v", activateErr)
		}
	}

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
	req.Header.Set("User-Agent", c.getUA())
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

// GetBuvidPair 从 SPI 接口获取 buvid3 和 buvid4（设备指纹对）
// 返回的值需通过 ActivateBuvid 激活才能被 B站风控系统认可
func GetBuvidPair(httpClient *http.Client) (buvid3, buvid4 string, err error) {
	req, reqErr := http.NewRequest("GET", "https://api.bilibili.com/x/frontend/finger/spi", nil)
	if reqErr != nil {
		return "", "", reqErr
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")

	resp, doErr := httpClient.Do(req)
	if doErr != nil {
		return "", "", doErr
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
		return "", "", err
	}
	if result.Code != 0 {
		return "", "", fmt.Errorf("get buvid pair: code=%d", result.Code)
	}
	return result.Data.B3, result.Data.B4, nil
}

// GetBuvid3 获取 buvid3（兼容旧调用，内部调用 GetBuvidPair）
func GetBuvid3(httpClient *http.Client) (string, error) {
	b3, _, err := GetBuvidPair(httpClient)
	return b3, err
}

// ActivateBuvid 通过 ExClimbWuzhi 接口激活 buvid3/buvid4
// B站风控要求：从 SPI 拿到的 buvid 必须经过此步骤"激活"，否则服务端不认
// 请求格式：Content-Type: application/json，body = {"payload":"<inner_json>"}
// Cookie 需携带 buvid3、buvid4、_uuid、buvid_fp 四个字段
// 参考: https://github.com/Nemo2011/bilibili-api (network.py)
func ActivateBuvid(httpClient *http.Client, buvid3, buvid4 string) error {
	// Step 1: 生成 _uuid（同时用作 innerPayload 的 df35 字段）
	uuid := genUUIDInfoc()

	// Step 2: 构造 inner payload
	//   "5062" 必须是整数（毫秒时间戳）
	//   "df35" 必须与 Cookie 中的 _uuid 值一致
	innerPayload := map[string]interface{}{
		"3064": 1,
		"5062": time.Now().UnixMilli(),
		"03bf": "https://www.bilibili.com/",
		"39c8": "333.788.fp.risk",
		"34f1": "",
		"d402": "",
		"654a": "",
		"6e7c": "841x959",
		"3c43": map[string]interface{}{
			"2673": 0,
			"3c45": "",
			"463d": "",
			"6e23": "Win32",
			"7794": "",
			"21bd": "",
			"b8ce": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
			"641c": 8,
			"07a4": "zh-CN",
			"1c57": 24,
			"0bd0": 24,
			"748e": []int{1920, 1080},
			"d61f": []int{1920, 1032},
			"fc9d": []int{1920, 1032},
			"6aa9": "Asia/Shanghai",
			"75b8": 1,
			"3b21": 1,
			"8a1c": 0,
			"d52f": "not available",
			"adca": "MacIntel",
			"80c9": []interface{}{
				[]interface{}{125, "Apple GPU"},
			},
			"13ab": "",
			"bfe9": "Arial",
			"a3c1": []string{"Arial"},
			"6bc5": "",
			"5f45": 0,
			"aa56": "undefined",
		},
		"54ef": `{"b_ut":"7","home_version":"V8","i-wanna-go-back":"-1","in_new_ab":true,"ab_version":{"for_ai_home_version":"V8","tianma_version":"V8","enable_web_push":"DISABLE"},"ab_split_num":{"for_ai_home_version":54,"tianma_version":54,"enable_web_push":14}}`,
		"8b94": "",
		"07a4": "zh-CN",
		"5f45": 0,
		"ua":   randUA(),
		"df35": uuid,
	}

	// Step 3: 序列化 inner payload → JSON 字符串
	innerBytes, err := json.Marshal(innerPayload)
	if err != nil {
		return fmt.Errorf("marshal ExClimbWuzhi inner payload: %w", err)
	}

	// Step 4: 计算 buvid_fp = MurmurHash3 x64 128(inner_json, seed=31) → hex32
	h1, h2 := murmur3x64_128(innerBytes, 31)
	buvidFP := fmt.Sprintf("%016x%016x", h1, h2)

	// Step 5: 构造 outer payload = {"payload": "<inner_json_string>"}
	outerPayload := map[string]string{"payload": string(innerBytes)}
	bodyBytes, err := json.Marshal(outerPayload)
	if err != nil {
		return fmt.Errorf("marshal ExClimbWuzhi outer payload: %w", err)
	}

	req, err := http.NewRequest("POST",
		"https://api.bilibili.com/x/internal/gaia-gateway/ExClimbWuzhi",
		strings.NewReader(string(bodyBytes)))
	if err != nil {
		return fmt.Errorf("create ExClimbWuzhi request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")

	// Step 6: Cookie 携带 4 个字段：buvid3、buvid4、_uuid、buvid_fp
	cookieParts := []string{
		"buvid3=" + buvid3,
		"_uuid=" + uuid,
		"buvid_fp=" + buvidFP,
	}
	if buvid4 != "" {
		cookieParts = append(cookieParts, "buvid4="+buvid4)
	}
	req.Header.Set("Cookie", strings.Join(cookieParts, "; "))

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ExClimbWuzhi request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("[buvid] ExClimbWuzhi parse response failed: %v (body=%s)", err, string(body))
		return nil
	}
	if result.Code != 0 {
		log.Printf("[buvid] ExClimbWuzhi returned code=%d msg=%s (non-fatal)", result.Code, result.Message)
	} else {
		log.Printf("[buvid] ExClimbWuzhi 激活成功 (buvid_fp=%s...)", buvidFP[:8])
	}
	return nil
}

// genUUIDInfoc 生成 B站风控所需的 _uuid cookie 格式
// 格式: XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX{5位时间戳尾}infoc
// 字符集: 大写十六进制 0123456789ABCDEF
func genUUIDInfoc() string {
	const hexChars = "0123456789ABCDEF"
	genSeg := func(n int) string {
		buf := make([]byte, n)
		_, _ = rand.Read(buf)
		seg := make([]byte, n)
		for i, b := range buf {
			seg[i] = hexChars[b&0x0F]
		}
		return string(seg)
	}
	parts := []string{genSeg(8), genSeg(4), genSeg(4), genSeg(4), genSeg(12)}
	base := strings.Join(parts, "-")
	millis := time.Now().UnixMilli()
	return base + fmt.Sprintf("%05d", millis%100000) + "infoc"
}

// murmur3x64_128 实现 MurmurHash3 x64 128-bit（带 seed）
// 参考: https://github.com/aappleby/smhasher/blob/master/src/MurmurHash3.cpp
// 用于计算 buvid_fp：输入 inner payload JSON 字符串，seed=31
func murmur3x64_128(data []byte, seed uint32) (h1, h2 uint64) {
	const (
		c1 = uint64(0x87c37b91114253d5)
		c2 = uint64(0x4cf5ad432745937f)
	)
	h1 = uint64(seed)
	h2 = uint64(seed)
	length := len(data)
	nblocks := length / 16
	for i := 0; i < nblocks; i++ {
		k1 := readUint64LE(data, i*16)
		k2 := readUint64LE(data, i*16+8)
		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1
		h1 = bits.RotateLeft64(h1, 27)
		h1 += h2
		h1 = h1*5 + 0x52dce729
		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2
		h2 = bits.RotateLeft64(h2, 31)
		h2 += h1
		h2 = h2*5 + 0x38495ab5
	}
	tail := data[nblocks*16:]
	var k1, k2 uint64
	switch len(tail) & 15 {
	case 15:
		k2 ^= uint64(tail[14]) << 48
		fallthrough
	case 14:
		k2 ^= uint64(tail[13]) << 40
		fallthrough
	case 13:
		k2 ^= uint64(tail[12]) << 32
		fallthrough
	case 12:
		k2 ^= uint64(tail[11]) << 24
		fallthrough
	case 11:
		k2 ^= uint64(tail[10]) << 16
		fallthrough
	case 10:
		k2 ^= uint64(tail[9]) << 8
		fallthrough
	case 9:
		k2 ^= uint64(tail[8])
		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2
		fallthrough
	case 8:
		k1 ^= uint64(tail[7]) << 56
		fallthrough
	case 7:
		k1 ^= uint64(tail[6]) << 48
		fallthrough
	case 6:
		k1 ^= uint64(tail[5]) << 40
		fallthrough
	case 5:
		k1 ^= uint64(tail[4]) << 32
		fallthrough
	case 4:
		k1 ^= uint64(tail[3]) << 24
		fallthrough
	case 3:
		k1 ^= uint64(tail[2]) << 16
		fallthrough
	case 2:
		k1 ^= uint64(tail[1]) << 8
		fallthrough
	case 1:
		k1 ^= uint64(tail[0])
		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1
	}
	h1 ^= uint64(length)
	h2 ^= uint64(length)
	h1 += h2
	h2 += h1
	h1 = fmix64(h1)
	h2 = fmix64(h2)
	h1 += h2
	h2 += h1
	return h1, h2
}

func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}

func readUint64LE(data []byte, offset int) uint64 {
	return uint64(data[offset]) |
		uint64(data[offset+1])<<8 |
		uint64(data[offset+2])<<16 |
		uint64(data[offset+3])<<24 |
		uint64(data[offset+4])<<32 |
		uint64(data[offset+5])<<40 |
		uint64(data[offset+6])<<48 |
		uint64(data[offset+7])<<56
}

// truncateStr 截断字符串用于日志
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

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
