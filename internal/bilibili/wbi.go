package bilibili

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// 预编译正则
var (
	reWbiImgURL = regexp.MustCompile(`"img_url"\s*:\s*"([^"]+)"`)
	reWbiSubURL = regexp.MustCompile(`"sub_url"\s*:\s*"([^"]+)"`)
)

// WBI 签名实现
// 参考: https://github.com/SocialSisterYi/bilibili-API-collect/blob/master/docs/misc/sign/wbi.md

var mixinKeyEncTab = []int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35, 27, 43, 5, 49,
	33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13, 37, 48, 7, 16, 24, 55, 40,
	61, 26, 17, 0, 1, 60, 51, 30, 4, 22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11,
	36, 20, 34, 44, 52,
}

func getMixinKey(orig string) string {
	var key strings.Builder
	for _, n := range mixinKeyEncTab {
		if n < len(orig) {
			key.WriteByte(orig[n])
		}
	}
	s := key.String()
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

type wbiCache struct {
	imgKey    string
	subKey    string
	fetchedAt time.Time
	mu        sync.RWMutex
}

var wbiCacheInstance = &wbiCache{}

// getWbiKeys 获取 img_key 和 sub_key（双重检查，网络 IO 在锁外执行）
func (c *Client) getWbiKeys() (string, string, error) {
	const cacheTTL = 6 * time.Hour

	// 第一次：RLock 快速读缓存
	wbiCacheInstance.mu.RLock()
	if wbiCacheInstance.imgKey != "" && time.Since(wbiCacheInstance.fetchedAt) < cacheTTL {
		img, sub := wbiCacheInstance.imgKey, wbiCacheInstance.subKey
		wbiCacheInstance.mu.RUnlock()
		return img, sub, nil
	}
	wbiCacheInstance.mu.RUnlock()

	// 缓存未命中，发网络请求（锁外执行，避免持锁阻塞其他协程）

	// nav 请求也走令牌桶限流
	if c.limiter != nil {
		c.limiter.Acquire()
	}
	req, _ := http.NewRequest("GET", "https://api.bilibili.com/x/web-interface/nav", nil)
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Referer", "https://www.bilibili.com")
	// Fix CR-003: read c.cookie under RLock to prevent data race with UpdateCredential
	c.mu.RLock()
	cookieVal := c.cookie
	c.mu.RUnlock()
	if cookieVal != "" {
		req.Header.Set("Cookie", cookieVal)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// 提取 img_url 和 sub_url
	imgRe := reWbiImgURL
	subRe := reWbiSubURL

	imgMatch := imgRe.FindSubmatch(body)
	subMatch := subRe.FindSubmatch(body)

	if imgMatch == nil || subMatch == nil {
		return "", "", fmt.Errorf("failed to extract wbi keys from nav API")
	}

	imgURL := string(imgMatch[1])
	subURL := string(subMatch[1])

	// 提取文件名（去掉扩展名）作为 key
	imgKey := extractKeyFromURL(imgURL)
	subKey := extractKeyFromURL(subURL)

	// 写入缓存：Lock 前二次检查，防止并发时重复写入
	wbiCacheInstance.mu.Lock()
	if wbiCacheInstance.imgKey == "" || time.Since(wbiCacheInstance.fetchedAt) >= cacheTTL {
		wbiCacheInstance.imgKey = imgKey
		wbiCacheInstance.subKey = subKey
		wbiCacheInstance.fetchedAt = time.Now()
	}
	img, sub := wbiCacheInstance.imgKey, wbiCacheInstance.subKey
	wbiCacheInstance.mu.Unlock()

	return img, sub, nil
}

func extractKeyFromURL(u string) string {
	// https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png
	// -> 7cd084941338484aae1ad9425b84077c
	parts := strings.Split(u, "/")
	last := parts[len(parts)-1]
	dot := strings.LastIndex(last, ".")
	if dot > 0 {
		return last[:dot]
	}
	return last
}

// signWbi 对请求参数进行 WBI 签名
func (c *Client) signWbi(params url.Values) (url.Values, error) {
	imgKey, subKey, err := c.getWbiKeys()
	if err != nil {
		return params, err
	}

	mixinKey := getMixinKey(imgKey + subKey)
	wts := fmt.Sprintf("%d", time.Now().Unix())
	params.Set("wts", wts)

	// 按 key 排序
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 拼接
	var buf strings.Builder
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte('&')
		}
		// 过滤特殊字符
		v := sanitizeWbiValue(params.Get(k))
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(v)
	}
	buf.WriteString(mixinKey)

	// MD5
	hash := md5.Sum([]byte(buf.String()))
	wRid := hex.EncodeToString(hash[:])

	params.Set("w_rid", wRid)
	return params, nil
}

func sanitizeWbiValue(s string) string {
	// 移除 !'()* 字符
	result := strings.Map(func(r rune) rune {
		if strings.ContainsRune("!'()*", r) {
			return -1
		}
		return r
	}, s)
	return result
}

// injectDmImgParams 注入浏览器画布/WebGL 指纹参数（B站风控第二层检测）
// 参考: https://github.com/SocialSisterYi/bilibili-API-collect 及 yt-dlp/RSSHub 实现
func injectDmImgParams(params url.Values) {
	// dm_img_list: 鼠标轨迹，空数组即可（B站只校验字段存在）
	params.Set("dm_img_list", "[]")

	// dm_img_str / dm_cover_img_str: WebGL VERSION / UNMASKED_RENDERER 的 base64
	// 使用随机化长度的字符串，更接近真实浏览器行为
	params.Set("dm_img_str", randomBase64Str(24, 48))
	params.Set("dm_cover_img_str", randomBase64Str(32, 80))

	// dm_img_inter: 交互时序数据，使用随机化的合理值
	wh1 := 1200 + rand.Intn(4000)
	wh2 := 800 + rand.Intn(3000)
	wh3 := 24 + rand.Intn(8)
	of1 := rand.Intn(600)
	of2 := rand.Intn(400)
	of3 := rand.Intn(400)
	params.Set("dm_img_inter", fmt.Sprintf(`{"ds":[],"wh":[%d,%d,%d],"of":[%d,%d,%d]}`,
		wh1, wh2, wh3, of1, of2, of3))
}

// randomBase64Str 生成指定长度范围内的随机 base64 字符串（模拟浏览器 WebGL 指纹）
func randomBase64Str(minLen, maxLen int) string {
	n := minLen + rand.Intn(maxLen-minLen+1)
	b := make([]byte, n)
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 /()"
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return base64.StdEncoding.EncodeToString(b)
}

// getWbi 发起带 WBI 签名的 GET 请求
// 内置 -352 风控重试：收到风控信号时清除 WBI 缓存，等待后重试一次
func (c *Client) getWbi(baseURL string, params url.Values, result interface{}) error {
	return c.getWbiWithReferer(baseURL, params, result, "", "")
}

// getWbiWithReferer 发起带 WBI 签名的 GET 请求，支持指定 Referer 和 Origin
func (c *Client) getWbiWithReferer(baseURL string, params url.Values, result interface{}, referer, origin string) error {
	const maxAttempts = 2

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// 每次尝试都重新注入 dm_img_* 参数（随机值）
		p := cloneValues(params)
		injectDmImgParams(p)

		signed, err := c.signWbi(p)
		if err != nil {
			// WBI 签名失败，尝试不签名直接请求（最后手段）
			return c.getWithHeaders(baseURL+"?"+p.Encode(), referer, origin, result)
		}

		reqErr := c.getWithHeaders(baseURL+"?"+signed.Encode(), referer, origin, result)
		if reqErr == nil {
			return nil
		}

		// 收到风控错误且还有重试机会：清除 WBI 缓存，等待后重试
		// -403 同样需要清缓存重试（WBI 签名密钥过期导致鉴权失败）
		if (IsRiskControl(reqErr) || IsAccessDenied(reqErr)) && attempt < maxAttempts {
			log.Printf("[wbi] 风控/鉴权失败，清除 WBI 缓存后重试 (attempt=%d, url=%s, err=%v)", attempt, baseURL, reqErr)
			ClearWbiCache()
			time.Sleep(time.Duration(2000+rand.Intn(1000)) * time.Millisecond)
			continue
		}

		return reqErr
	}
	return fmt.Errorf("getWbi: unreachable")
}

// cloneValues 深拷贝 url.Values，避免重试时修改原始参数
func cloneValues(src url.Values) url.Values {
	dst := make(url.Values, len(src))
	for k, vs := range src {
		cp := make([]string, len(vs))
		copy(cp, vs)
		dst[k] = cp
	}
	return dst
}

// ClearWbiCache 清除 WBI 签名密钥缓存，强制下次请求重新获取
func ClearWbiCache() {
	wbiCacheInstance.mu.Lock()
	defer wbiCacheInstance.mu.Unlock()
	wbiCacheInstance.imgKey = ""
	wbiCacheInstance.subKey = ""
	wbiCacheInstance.fetchedAt = time.Time{}
}
