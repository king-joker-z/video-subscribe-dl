package douyin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// UA 池: 移动端 UA（parse-video 用 iPhone UA 效果最好）
var uaPool = []string{
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Mobile Safari/537.36",
}

// pickUA 轮换 UA
func pickUA() string {
	return uaPool[rand.Intn(len(uaPool))]
}

// DouyinClient 抖音 API 客户端
type DouyinClient struct {
	noRedirectClient *http.Client // 不跟随重定向
	normalClient     *http.Client // 正常 client
	limiter          *RateLimiter
}

// NewClient 创建抖音客户端
func NewClient() *DouyinClient {
	return &DouyinClient{
		noRedirectClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		normalClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		limiter: DefaultRateLimiter(),
	}
}

// ---- URL 解析 ----

var (
	// v.douyin.com/xxx 短链接
	reShortURL = regexp.MustCompile(`v\.douyin\.com/([A-Za-z0-9]+)`)
	// douyin.com/video/1234567890
	reVideoURL = regexp.MustCompile(`douyin\.com/video/(\d+)`)
	// douyin.com/user/MS4wLjAB... (sec_user_id)
	reUserURL = regexp.MustCompile(`douyin\.com/user/([A-Za-z0-9_-]+)`)
	// iesdouyin.com/share/video/1234567890
	reIesVideoURL = regexp.MustCompile(`iesdouyin\.com/share/video/(\d+)`)
	// 从路径中提取最后的数字 ID
	rePathVideoID = regexp.MustCompile(`/(?:video|note)/(\d+)`)
	// douyin.com/jingxuan?modal_id=xxx
	reModalID = regexp.MustCompile(`modal_id=(\d+)`)
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
	Type    URLType
	VideoID string
	SecUID  string
}

// ResolveShareURL 解析抖音分享链接
// 参考 parse-video: parseShareUrl → 区分 v.douyin.com / www.douyin.com / www.iesdouyin.com
func (c *DouyinClient) ResolveShareURL(shareURL string) (*ResolveResult, error) {
	c.limiter.Acquire()

	// 确保有协议前缀
	if !strings.HasPrefix(shareURL, "http") {
		shareURL = "https://" + shareURL
	}

	parsed, err := url.Parse(shareURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	switch parsed.Host {
	case "v.douyin.com":
		return c.resolveShortURL(shareURL)
	case "www.douyin.com", "www.iesdouyin.com":
		return c.parseLongURL(shareURL)
	default:
		// 尝试通用匹配
		return c.parseLongURL(shareURL)
	}
}

// resolveShortURL 解析 v.douyin.com 短链接（通过 302 重定向）
// 参考 parse-video: parseAppShareUrl
func (c *DouyinClient) resolveShortURL(shortURL string) (*ResolveResult, error) {
	req, err := http.NewRequest("GET", shortURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", pickUA())

	resp, err := c.noRedirectClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resolve short url: %w", err)
	}
	defer resp.Body.Close()

	location := resp.Header.Get("Location")
	if location == "" {
		return nil, fmt.Errorf("no redirect from short url")
	}

	return c.parseLongURL(location)
}

// parseLongURL 从完整 URL 中解析出视频 ID 或用户 sec_uid
// 参考 parse-video: parsePcShareUrl + parseVideoIdFromPath
func (c *DouyinClient) parseLongURL(rawURL string) (*ResolveResult, error) {
	// 优先: modal_id 参数（精选页面）
	if m := reModalID.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}

	// 用户主页
	if m := reUserURL.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeUser, SecUID: m[1]}, nil
	}

	// 视频链接
	if m := reVideoURL.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}
	if m := reIesVideoURL.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}

	// /video/xxx 或 /note/xxx 路径
	if m := rePathVideoID.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}

	// 最后手段: URL 路径最后一段数字
	parsed, _ := url.Parse(rawURL)
	if parsed != nil {
		path := strings.Trim(parsed.Path, "/")
		parts := strings.Split(path, "/")
		if len(parts) > 0 {
			lastPart := parts[len(parts)-1]
			if matched, _ := regexp.MatchString(`^\d+$`, lastPart); matched {
				return &ResolveResult{Type: URLTypeVideo, VideoID: lastPart}, nil
			}
		}
	}

	return nil, fmt.Errorf("unrecognized douyin url: %s", rawURL)
}

// ---- 视频详情 ----

// _ROUTER_DATA 正则（参考 parse-video: 精确匹配 window._ROUTER_DATA = {...}</script>）
var reRouterData = regexp.MustCompile(`window\._ROUTER_DATA\s*=\s*(.*?)</script>`)

// GetVideoDetail 获取单个视频详情
// 核心方案: 通过 iesdouyin.com/share/video/{id} 页面解析 _ROUTER_DATA
// 参考 parse-video parseVideoID: 不需要 X-Bogus 签名，不需要 goja
func (c *DouyinClient) GetVideoDetail(videoID string) (*DouyinVideo, error) {
	c.limiter.Acquire()

	pageURL := fmt.Sprintf("https://www.iesdouyin.com/share/video/%s", videoID)

	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	// parse-video 用 iPhone UA，实测比桌面 UA 更稳定
	req.Header.Set("User-Agent", pickUA())

	resp, err := c.normalClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch video page: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read video page body: %w", err)
	}

	htmlStr := string(body)

	// Step 1: 检查是否为图集/笔记（参考 parse-video: 通过 canonical link 判断）
	isNote := false
	canonical := getCanonicalFromHTML(htmlStr)
	if strings.Contains(canonical, "/note/") {
		isNote = true
	}

	// Step 2: 图集走 slidesinfo API（参考 parse-video: 随机 web_id + 随机 a_bogus 居然能用）
	if isNote {
		return c.getNoteDetail(videoID)
	}

	// Step 3: 普通视频从 _ROUTER_DATA 解析
	m := reRouterData.FindSubmatch(body)
	if len(m) < 2 {
		return nil, fmt.Errorf("_ROUTER_DATA not found in page (status=%d, bodyLen=%d)", resp.StatusCode, len(body))
	}

	jsonBytes := bytes.TrimSpace(m[1])
	return c.parseRouterDataForVideo(jsonBytes, videoID)
}

// getNoteDetail 获取图集/笔记详情
// 参考 parse-video: iesdouyin.com/web/api/v2/aweme/slidesinfo/ + 随机 a_bogus
func (c *DouyinClient) getNoteDetail(videoID string) (*DouyinVideo, error) {
	webID := generateWebID()
	aBogus := randAlphaNum(64) // parse-video 直接用随机字符串做 a_bogus

	apiURL := fmt.Sprintf(
		"https://www.iesdouyin.com/web/api/v2/aweme/slidesinfo/?reflow_source=reflow_page&web_id=%s&device_id=%s&aweme_ids=%%5B%s%%5D&request_source=200&a_bogus=%s",
		webID, webID, videoID, aBogus,
	)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", pickUA())

	resp, err := c.normalClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch note detail: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read note detail: %w", err)
	}

	var apiResp struct {
		AwemeDetails []json.RawMessage `json:"aweme_details"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse note api response: %w", err)
	}
	if len(apiResp.AwemeDetails) == 0 {
		return nil, fmt.Errorf("note detail not found for %s", videoID)
	}

	return parseAwemeDetail(apiResp.AwemeDetails[0], videoID, true)
}

// parseRouterDataForVideo 从 _ROUTER_DATA JSON 解析视频信息
// 参考 parse-video: gjson 路径 loaderData.video_(id)/page.videoInfoRes.item_list.0
func (c *DouyinClient) parseRouterDataForVideo(jsonBytes []byte, videoID string) (*DouyinVideo, error) {
	var data map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return nil, fmt.Errorf("parse router data json: %w", err)
	}

	// 路径: loaderData -> video_(id)/page -> videoInfoRes -> item_list[0]
	loaderData, ok := data["loaderData"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("loaderData not found")
	}

	var videoPage map[string]interface{}
	// 尝试多个可能的 key（路由可能变化）
	for _, key := range []string{
		"video_(id)/page",
		"video_(id)",
		"note_(id)/page",
		"note_(id)",
	} {
		if page, ok := loaderData[key].(map[string]interface{}); ok {
			videoPage = page
			break
		}
	}
	if videoPage == nil {
		// fallback: 遍历找第一个有 videoInfoRes 的
		for _, v := range loaderData {
			if page, ok := v.(map[string]interface{}); ok {
				if _, has := page["videoInfoRes"]; has {
					videoPage = page
					break
				}
			}
		}
	}
	if videoPage == nil {
		return nil, fmt.Errorf("video page not found in loaderData")
	}

	videoInfoRes, ok := videoPage["videoInfoRes"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("videoInfoRes not found")
	}

	// 检查 filter_list（被风控拦截的情况）
	// 参考 parse-video: 检查 filter_list 中是否有 filter_reason
	if filterList, ok := videoInfoRes["filter_list"].([]interface{}); ok {
		for _, item := range filterList {
			if fm, ok := item.(map[string]interface{}); ok {
				if fmID, _ := fm["aweme_id"].(string); fmID == videoID {
					reason, _ := fm["filter_reason"].(string)
					detail, _ := fm["detail_msg"].(string)
					return nil, fmt.Errorf("video filtered: %s - %s", reason, detail)
				}
			}
		}
	}

	itemList, ok := videoInfoRes["item_list"].([]interface{})
	if !ok || len(itemList) == 0 {
		return nil, fmt.Errorf("item_list empty or not found")
	}

	itemBytes, err := json.Marshal(itemList[0])
	if err != nil {
		return nil, fmt.Errorf("marshal item: %w", err)
	}

	return parseAwemeDetail(itemBytes, videoID, false)
}

// parseAwemeDetail 从 aweme_detail JSON 解析出 DouyinVideo
func parseAwemeDetail(raw json.RawMessage, videoID string, isNote bool) (*DouyinVideo, error) {
	var detail map[string]interface{}
	if err := json.Unmarshal(raw, &detail); err != nil {
		return nil, fmt.Errorf("parse aweme detail: %w", err)
	}

	video := &DouyinVideo{
		AwemeID: videoID,
		IsNote:  isNote,
	}

	// 描述
	if desc, ok := detail["desc"].(string); ok {
		video.Desc = desc
	}

	// 创建时间
	if ct, ok := detail["create_time"].(float64); ok {
		video.CreateTime = int64(ct)
	}

	// 作者
	if authorData, ok := detail["author"].(map[string]interface{}); ok {
		if v, ok := authorData["uid"].(string); ok {
			video.Author.UID = v
		}
		if v, ok := authorData["sec_uid"].(string); ok {
			video.Author.SecUID = v
		}
		if v, ok := authorData["nickname"].(string); ok {
			video.Author.Nickname = v
		}
		if av, ok := authorData["avatar_thumb"].(map[string]interface{}); ok {
			if urls, ok := av["url_list"].([]interface{}); ok && len(urls) > 0 {
				if u, ok := urls[0].(string); ok {
					video.Author.AvatarURL = u
				}
			}
		}
	}

	// 视频数据
	if videoData, ok := detail["video"].(map[string]interface{}); ok {
		// 封面（参考 parse-video: 优先非 webp 格式）
		if cover, ok := videoData["cover"].(map[string]interface{}); ok {
			if urls, ok := cover["url_list"].([]interface{}); ok {
				video.Cover = pickNonWebpURL(urls)
			}
		}
		if video.Cover == "" {
			if cover, ok := videoData["dynamic_cover"].(map[string]interface{}); ok {
				if urls, ok := cover["url_list"].([]interface{}); ok {
					video.Cover = pickNonWebpURL(urls)
				}
			}
		}

		// 时长
		if dur, ok := videoData["duration"].(float64); ok {
			video.Duration = int(dur)
		}

		// 视频 URL: play_addr.url_list[0]
		// 参考 parse-video: playwm → play 获取无水印
		if !isNote {
			if playAddr, ok := videoData["play_addr"].(map[string]interface{}); ok {
				if urls, ok := playAddr["url_list"].([]interface{}); ok && len(urls) > 0 {
					if u, ok := urls[0].(string); ok {
						video.VideoURL = strings.ReplaceAll(u, "playwm", "play")
					}
				}
			}
		}
	}

	// 图集图片（参考 parse-video: images[].url_list 取非 webp）
	if images, ok := detail["images"].([]interface{}); ok {
		for _, img := range images {
			if imgMap, ok := img.(map[string]interface{}); ok {
				if urls, ok := imgMap["url_list"].([]interface{}); ok {
					if imgURL := pickNonWebpURL(urls); imgURL != "" {
						video.Images = append(video.Images, imgURL)
					}
				}
			}
		}
		if len(video.Images) > 0 {
			video.IsNote = true
			video.VideoURL = "" // 图集没有视频
		}
	}

	// 统计
	if stats, ok := detail["statistics"].(map[string]interface{}); ok {
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
// 使用 iesdouyin.com 的 web API（与 slidesinfo 类似，用随机 a_bogus）
// 这个接口比 douyin.com/aweme/v1/web/aweme/post/ 风控宽松
func (c *DouyinClient) GetUserVideos(secUID string, maxCursor int64) (*UserVideosResult, error) {
	c.limiter.Acquire()

	webID := generateWebID()
	aBogus := randAlphaNum(64)

	apiURL := fmt.Sprintf(
		"https://www.iesdouyin.com/web/api/v2/aweme/post/?sec_user_id=%s&count=18&max_cursor=%d&aid=6383&web_id=%s&device_id=%s&a_bogus=%s",
		url.QueryEscape(secUID), maxCursor, webID, webID, aBogus,
	)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	cookie := globalCookieMgr.getCookieString(c.normalClient)
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", pickUA())
	req.Header.Set("Referer", "https://www.iesdouyin.com/")

	resp, err := c.normalClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch user videos: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read user videos: %w", err)
	}

	if resp.StatusCode != 200 {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("user videos API returned %d: %s", resp.StatusCode, snippet)
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
			Images []struct {
				URLList []string `json:"url_list"`
			} `json:"images"`
		} `json:"aweme_list"`
		HasMore   bool  `json:"has_more"`
		MaxCursor int64 `json:"max_cursor"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse user videos: %w (body=%s)", err, truncate(string(body), 200))
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
			v.Cover = pickNonWebpURLStr(item.Video.Cover.URLList)
		}
		// 视频 URL（无水印）
		if len(item.Video.PlayAddr.URLList) > 0 {
			v.VideoURL = strings.ReplaceAll(item.Video.PlayAddr.URLList[0], "playwm", "play")
		}
		// 图集
		if len(item.Images) > 0 {
			v.IsNote = true
			v.VideoURL = "" // 图集没有视频
			for _, img := range item.Images {
				if len(img.URLList) > 0 {
					v.Images = append(v.Images, pickNonWebpURLStr(img.URLList))
				}
			}
		}
		result.Videos = append(result.Videos, v)
	}

	log.Printf("[douyin] GetUserVideos: secUID=%s cursor=%d got=%d hasMore=%v nextCursor=%d",
		secUID, maxCursor, len(result.Videos), result.HasMore, result.MaxCursor)

	return result, nil
}

// ResolveVideoURL 跟随 302 获取无水印视频最终下载地址
// 参考 parse-video: getRedirectUrl
func (c *DouyinClient) ResolveVideoURL(videoURL string) (string, error) {
	if videoURL == "" {
		return "", fmt.Errorf("empty video url")
	}

	req, err := http.NewRequest("GET", videoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", pickUA())

	resp, err := c.noRedirectClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve video url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 302 || resp.StatusCode == 301 {
		loc := resp.Header.Get("Location")
		if loc != "" {
			return loc, nil
		}
	}

	return videoURL, nil
}

// ---- 辅助函数 ----

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

// SanitizePath 文件名清理
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

// generateWebID 生成 web_id（参考 parse-video: "75" + 15位随机数字）
func generateWebID() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("75%015d", r.Int63n(1e15))
}

// randAlphaNum 生成指定长度的随机字母数字串
func randAlphaNum(n int) string {
	const letters = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// pickNonWebpURL 从 url_list 中优先选非 webp 格式的 URL（参考 parse-video: getNoWebpUrl）
func pickNonWebpURL(urls []interface{}) string {
	var first string
	for _, u := range urls {
		s, ok := u.(string)
		if !ok || s == "" {
			continue
		}
		if first == "" {
			first = s
		}
		if !strings.Contains(s, ".webp") {
			return s
		}
	}
	return first
}

// pickNonWebpURLStr 字符串版
func pickNonWebpURLStr(urls []string) string {
	var first string
	for _, s := range urls {
		if s == "" {
			continue
		}
		if first == "" {
			first = s
		}
		if !strings.Contains(s, ".webp") {
			return s
		}
	}
	return first
}

// getCanonicalFromHTML 从 HTML 中提取 canonical link
// 参考 parse-video: getCanonicalFromHTML
func getCanonicalFromHTML(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return ""	}
	return findCanonical(doc)
}

// findCanonical 递归查找 canonical link（参考 parse-video: findCanonical）
func findCanonical(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "link" {
		var rel, href string
		for _, attr := range n.Attr {
			switch attr.Key {
			case "rel":
				rel = attr.Val
			case "href":
				href = attr.Val
			}
		}
		if rel == "canonical" && href != "" {
			return href
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findCanonical(c); result != "" {
			return result
		}
	}
	return ""
}

// truncate 截断字符串
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
