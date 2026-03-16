package douyin

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const douyinUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// DouyinClient 抖音 API 客户端
type DouyinClient struct {
	http    *http.Client
	limiter *RateLimiter
}

// NewClient 创建抖音客户端
func NewClient() *DouyinClient {
	return &DouyinClient{
		http: &http.Client{
			Timeout: 30 * time.Second,
			// 不自动跟随重定向（用于解析短链接）
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		limiter: DefaultRateLimiter(),
	}
}

// httpGet 辅助方法：自动添加 Cookie 和 UA
func (c *DouyinClient) httpGet(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	cookie := globalCookieMgr.getCookieString(c.http)
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", douyinUA)
	req.Header.Set("Referer", "https://www.douyin.com/")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	return c.http.Do(req)
}

// httpGetFollowRedirect 跟随重定向的 GET 请求
func (c *DouyinClient) httpGetFollowRedirect(rawURL string) (*http.Response, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	cookie := globalCookieMgr.getCookieString(client)
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", douyinUA)
	req.Header.Set("Referer", "https://www.douyin.com/")
	return client.Do(req)
}

// ---- URL 解析 ----

var (
	// 短链接: v.douyin.com/xxx
	reShortURL = regexp.MustCompile(`v\.douyin\.com/([A-Za-z0-9]+)`)
	// 视频链接: douyin.com/video/1234567890
	reVideoURL = regexp.MustCompile(`douyin\.com/video/(\d+)`)
	// 用户链接: douyin.com/user/MS4wLjAB... (sec_user_id)
	reUserURL = regexp.MustCompile(`douyin\.com/user/([A-Za-z0-9_-]+)`)
	// iesdouyin 视频链接: iesdouyin.com/share/video/1234567890
	reIesVideoURL = regexp.MustCompile(`iesdouyin\.com/share/video/(\d+)`)
)

// URLType 解析结果类型
type URLType int

const (
	URLTypeUnknown URLType = iota
	URLTypeVideo
	URLTypeUser
)

// ResolveResult 解析结果
type ResolveResult struct {
	Type     URLType
	VideoID  string // 视频 ID（aweme_id）
	SecUID   string // 用户 sec_user_id
}

// ResolveShareURL 解析抖音分享链接，支持:
// - v.douyin.com/xxx 短链接
// - douyin.com/video/xxx 视频链接
// - douyin.com/user/xxx 用户主页链接
// - iesdouyin.com/share/video/xxx 视频链接
func (c *DouyinClient) ResolveShareURL(shareURL string) (*ResolveResult, error) {
	c.limiter.Acquire()

	// 1. 直接匹配完整视频链接
	if m := reVideoURL.FindStringSubmatch(shareURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}
	if m := reIesVideoURL.FindStringSubmatch(shareURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}

	// 2. 用户主页链接
	if m := reUserURL.FindStringSubmatch(shareURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeUser, SecUID: m[1]}, nil
	}

	// 3. 短链接: 跟随重定向
	if reShortURL.MatchString(shareURL) {
		// 确保有协议前缀
		if !strings.HasPrefix(shareURL, "http") {
			shareURL = "https://" + shareURL
		}
		resp, err := c.httpGet(shareURL)
		if err != nil {
			return nil, fmt.Errorf("resolve short url: %w", err)
		}
		defer resp.Body.Close()

		location := resp.Header.Get("Location")
		if location == "" {
			return nil, fmt.Errorf("no redirect from short url")
		}

		// 从重定向 URL 中解析
		if m := reVideoURL.FindStringSubmatch(location); len(m) > 1 {
			return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
		}
		if m := reUserURL.FindStringSubmatch(location); len(m) > 1 {
			return &ResolveResult{Type: URLTypeUser, SecUID: m[1]}, nil
		}

		return nil, fmt.Errorf("unable to parse redirect url: %s", location)
	}

	return nil, fmt.Errorf("unrecognized douyin url: %s", shareURL)
}

// ---- 视频详情 ----

// routerDataPattern 匹配 window._ROUTER_DATA 或 RENDER_DATA 中的 JSON
var routerDataPattern = regexp.MustCompile(`(?:_ROUTER_DATA|RENDER_DATA)\s*=\s*({.+?})\s*</script>`)

// GetVideoDetail 获取单个视频详情
// 通过 https://www.iesdouyin.com/share/video/{id} 页面解析
func (c *DouyinClient) GetVideoDetail(videoID string) (*DouyinVideo, error) {
	c.limiter.Acquire()

	pageURL := fmt.Sprintf("https://www.iesdouyin.com/share/video/%s", videoID)
	resp, err := c.httpGetFollowRedirect(pageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch video page: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read video page body: %w", err)
	}

	html := string(body)

	// 尝试从 _ROUTER_DATA / RENDER_DATA 提取 JSON
	m := routerDataPattern.FindStringSubmatch(html)
	if len(m) < 2 {
		// 备选: 尝试匹配更宽松的格式
		alt := regexp.MustCompile(`window\._ROUTER_DATA\s*=\s*({[\s\S]+?})\s*;?\s*</script>`)
		m = alt.FindStringSubmatch(html)
	}
	if len(m) < 2 {
		return nil, fmt.Errorf("_ROUTER_DATA not found in page")
	}

	jsonStr := m[1]
	// URL decode（有些版本会 encode）
	if decoded, err := url.QueryUnescape(jsonStr); err == nil {
		jsonStr = decoded
	}

	return parseRouterData(jsonStr, videoID)
}

// parseRouterData 从 _ROUTER_DATA JSON 中提取视频信息
func parseRouterData(jsonStr string, videoID string) (*DouyinVideo, error) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil, fmt.Errorf("parse router data: %w", err)
	}

	// 遍历嵌套查找 aweme_detail 或 awemeDetail
	detail := findNestedKey(data, "aweme_detail")
	if detail == nil {
		detail = findNestedKey(data, "awemeDetail")
	}
	if detail == nil {
		// 尝试从 loaderData 查找
		if ld, ok := data["loaderData"]; ok {
			if ldMap, ok := ld.(map[string]interface{}); ok {
				for _, v := range ldMap {
					if vMap, ok := v.(map[string]interface{}); ok {
						detail = findNestedKey(vMap, "aweme_detail")
						if detail == nil {
							detail = findNestedKey(vMap, "awemeDetail")
						}
						if detail != nil {
							break
						}
					}
				}
			}
		}
	}
	if detail == nil {
		return nil, fmt.Errorf("aweme_detail not found in router data")
	}

	detailMap, ok := detail.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("aweme_detail is not an object")
	}

	video := &DouyinVideo{
		AwemeID: videoID,
	}

	// 描述/标题
	if desc, ok := detailMap["desc"].(string); ok {
		video.Desc = desc
	}

	// 创建时间
	if ct, ok := detailMap["create_time"].(float64); ok {
		video.CreateTime = int64(ct)
	}

	// 作者信息
	if authorData, ok := detailMap["author"].(map[string]interface{}); ok {
		if uid, ok := authorData["uid"].(string); ok {
			video.Author.UID = uid
		}
		if secUID, ok := authorData["sec_uid"].(string); ok {
			video.Author.SecUID = secUID
		}
		if nick, ok := authorData["nickname"].(string); ok {
			video.Author.Nickname = nick
		}
		if av, ok := authorData["avatar_thumb"].(map[string]interface{}); ok {
			if urls, ok := av["url_list"].([]interface{}); ok && len(urls) > 0 {
				if u, ok := urls[0].(string); ok {
					video.Author.AvatarURL = u
				}
			}
		}
	}

	// 封面
	if coverData, ok := detailMap["video"].(map[string]interface{}); ok {
		if cover, ok := coverData["cover"].(map[string]interface{}); ok {
			if urls, ok := cover["url_list"].([]interface{}); ok && len(urls) > 0 {
				if u, ok := urls[0].(string); ok {
					video.Cover = u
				}
			}
		}
		// 动态封面备选
		if video.Cover == "" {
			if cover, ok := coverData["dynamic_cover"].(map[string]interface{}); ok {
				if urls, ok := cover["url_list"].([]interface{}); ok && len(urls) > 0 {
					if u, ok := urls[0].(string); ok {
						video.Cover = u
					}
				}
			}
		}

		// 时长
		if dur, ok := coverData["duration"].(float64); ok {
			video.Duration = int(dur)
		}

		// 视频 URL: play_addr.url_list[0]
		if playAddr, ok := coverData["play_addr"].(map[string]interface{}); ok {
			if urls, ok := playAddr["url_list"].([]interface{}); ok && len(urls) > 0 {
				if u, ok := urls[0].(string); ok {
					// playwm -> play 获取无水印
					video.VideoURL = strings.Replace(u, "playwm", "play", 1)
				}
			}
		}
	}

	// 统计数据
	if stats, ok := detailMap["statistics"].(map[string]interface{}); ok {
		if v, ok := stats["digg_count"].(float64); ok {
			video.DiggCount = int64(v)
		}
		if v, ok := stats["share_count"].(float64); ok {
			video.ShareCount = int64(v)
		}
		if v, ok := stats["comment_count"].(float64); ok {
			video.CommentCount = int64(v)
		}
	}

	return video, nil
}

// ---- 用户视频列表 ----

// GetUserVideos 获取用户视频列表
// 通过 iesdouyin.com 的 web API（不需要 X-Bogus 签名）
func (c *DouyinClient) GetUserVideos(secUID string, maxCursor int64) (*UserVideosResult, error) {
	c.limiter.Acquire()

	apiURL := fmt.Sprintf("https://www.iesdouyin.com/web/api/v2/aweme/post/?sec_user_id=%s&count=20&max_cursor=%d",
		url.QueryEscape(secUID), maxCursor)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	cookie := globalCookieMgr.getCookieString(c.http)
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", douyinUA)
	req.Header.Set("Referer", "https://www.iesdouyin.com/")

	// 用跟随重定向的 client
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch user videos: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read user videos response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("user videos API returned %d: %s", resp.StatusCode, string(body[:min(200, len(body))]))
	}

	var apiResp struct {
		StatusCode int `json:"status_code"`
		AwemeList  []struct {
			AwemeID    string  `json:"aweme_id"`
			Desc       string  `json:"desc"`
			CreateTime float64 `json:"create_time"`
			Author     struct {
				UID      string `json:"uid"`
				SecUID   string `json:"sec_uid"`
				Nickname string `json:"nickname"`
			} `json:"author"`
			Video struct {
				Cover struct {
					URLList []string `json:"url_list"`
				} `json:"cover"`
				PlayAddr struct {
					URLList []string `json:"url_list"`
				} `json:"play_addr"`
				Duration int `json:"duration"`
			} `json:"video"`
			Statistics struct {
				DiggCount    int64 `json:"digg_count"`
				ShareCount   int64 `json:"share_count"`
				CommentCount int64 `json:"comment_count"`
			} `json:"statistics"`
		} `json:"aweme_list"`
		HasMore  bool  `json:"has_more"`
		MaxCursor int64 `json:"max_cursor"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse user videos response: %w", err)
	}

	result := &UserVideosResult{
		HasMore:   apiResp.HasMore,
		MaxCursor: apiResp.MaxCursor,
	}

	for _, item := range apiResp.AwemeList {
		v := DouyinVideo{
			AwemeID:      item.AwemeID,
			Desc:         item.Desc,
			CreateTime:   int64(item.CreateTime),
			Duration:     item.Video.Duration,
			DiggCount:    item.Statistics.DiggCount,
			ShareCount:   item.Statistics.ShareCount,
			CommentCount: item.Statistics.CommentCount,
			Author: DouyinUser{
				UID:      item.Author.UID,
				SecUID:   item.Author.SecUID,
				Nickname: item.Author.Nickname,
			},
		}
		// 封面
		if len(item.Video.Cover.URLList) > 0 {
			v.Cover = item.Video.Cover.URLList[0]
		}
		// 无水印视频 URL
		if len(item.Video.PlayAddr.URLList) > 0 {
			v.VideoURL = strings.Replace(item.Video.PlayAddr.URLList[0], "playwm", "play", 1)
		}
		result.Videos = append(result.Videos, v)
	}

	log.Printf("[douyin] GetUserVideos: secUID=%s, cursor=%d, got=%d, hasMore=%v, nextCursor=%d",
		secUID, maxCursor, len(result.Videos), result.HasMore, result.MaxCursor)

	return result, nil
}

// ResolveVideoURL 解析视频无水印的最终下载地址（跟随 302 重定向）
func (c *DouyinClient) ResolveVideoURL(videoURL string) (string, error) {
	if videoURL == "" {
		return "", fmt.Errorf("empty video url")
	}

	req, err := http.NewRequest("GET", videoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", douyinUA)

	// 不跟随重定向，手动获取 Location
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve video url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 302 || resp.StatusCode == 301 {
		location := resp.Header.Get("Location")
		if location != "" {
			return location, nil
		}
	}

	// 如果没有重定向，原始 URL 就是最终地址
	return videoURL, nil
}

// ---- 辅助函数 ----

// findNestedKey 递归查找 map 中指定 key 的值
func findNestedKey(data map[string]interface{}, key string) interface{} {
	if v, ok := data[key]; ok {
		return v
	}
	for _, v := range data {
		if m, ok := v.(map[string]interface{}); ok {
			if result := findNestedKey(m, key); result != nil {
				return result
			}
		}
	}
	return nil
}

// ExtractSecUID 从 URL 中提取 sec_user_id
func ExtractSecUID(rawURL string) (string, error) {
	if m := reUserURL.FindStringSubmatch(rawURL); len(m) > 1 {
		return m[1], nil
	}
	return "", fmt.Errorf("unable to extract sec_user_id from: %s", rawURL)
}

// IsDouyinURL 判断是否为抖音链接
func IsDouyinURL(rawURL string) bool {
	return strings.Contains(rawURL, "douyin.com") || strings.Contains(rawURL, "iesdouyin.com")
}

// SanitizePath 去除文件路径中的非法字符
func SanitizePath(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_",
		"|", "_", "\n", " ", "\r", "",
	)
	result := replacer.Replace(name)
	result = strings.TrimSpace(result)
	if result == "" {
		result = "unknown"
	}
	return result
}
