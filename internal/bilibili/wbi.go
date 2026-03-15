package bilibili

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
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
	imgKey   string
	subKey   string
	fetchedAt time.Time
	mu       sync.Mutex
}

var wbiCacheInstance = &wbiCache{}

// getWbiKeys 获取 img_key 和 sub_key
func (c *Client) getWbiKeys() (string, string, error) {
	wbiCacheInstance.mu.Lock()
	defer wbiCacheInstance.mu.Unlock()

	// 缓存 1 小时
	if wbiCacheInstance.imgKey != "" && time.Since(wbiCacheInstance.fetchedAt) < time.Hour {
		return wbiCacheInstance.imgKey, wbiCacheInstance.subKey, nil
	}

	// nav 请求也走令牌桶限流
	if c.limiter != nil {
		c.limiter.Acquire()
	}
	req, _ := http.NewRequest("GET", "https://api.bilibili.com/x/web-interface/nav", nil)
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Referer", "https://www.bilibili.com")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
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

	wbiCacheInstance.imgKey = imgKey
	wbiCacheInstance.subKey = subKey
	wbiCacheInstance.fetchedAt = time.Now()

	return imgKey, subKey, nil
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

// getWbi 发起带 WBI 签名的 GET 请求
func (c *Client) getWbi(baseURL string, params url.Values, result interface{}) error {
	signed, err := c.signWbi(params)
	if err != nil {
		// WBI 签名失败，尝试不签名直接请求
		return c.get(baseURL+"?"+params.Encode(), result)
	}
	// get() 内部已包含风控检测（-352/-401/-412 → ErrRateLimited）
	return c.get(baseURL+"?"+signed.Encode(), result)
}

// ClearWbiCache 清除 WBI 签名密钥缓存，强制下次请求重新获取
func ClearWbiCache() {
	wbiCacheInstance.mu.Lock()
	defer wbiCacheInstance.mu.Unlock()
	wbiCacheInstance.imgKey = ""
	wbiCacheInstance.subKey = ""
	wbiCacheInstance.fetchedAt = time.Time{}
}
