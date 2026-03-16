package douyin

import (
	"bytes"
	_ "embed"
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

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

//go:embed sign.js
var signScript string

// PC 端 UA（用于需要 X-Bogus 签名的 API）
const pcUA = "Mozilla/5.0 (Linux; Android 8.0; Pixel 2 Build/OPD3.170816.012) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.88 Mobile Safari/537.36 Edg/87.0.664.66"

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

// ---- X-Bogus 签名 ----

// signURL 使用 goja 执行 sign.js 计算 X-Bogus 签名
// 参考 lux: vm.RunString(fmt.Sprintf("sign('%s', '%s')", query.RawQuery, ua))
func signURL(queryStr, userAgent string) (string, error) {
	vm := goja.New()
	if _, err := vm.RunString(signScript); err != nil {
		return "", fmt.Errorf("load sign.js: %w", err)
	}
	code := fmt.Sprintf("sign('%s', '%s')", queryStr, userAgent)
	val, err := vm.RunString(code)
	if err != nil {
		return "", fmt.Errorf("execute sign(): %w", err)
	}
	return val.String(), nil
}

// ---- URL 解析 ----

var (
	reShortURL    = regexp.MustCompile(`v\.douyin\.com/([A-Za-z0-9]+)`)
	reVideoURL    = regexp.MustCompile(`douyin\.com/video/(\d+)`)
	reUserURL     = regexp.MustCompile(`douyin\.com/user/([A-Za-z0-9_-]+)`)
	reIesVideoURL = regexp.MustCompile(`iesdouyin\.com/share/video/(\d+)`)
	rePathVideoID = regexp.MustCompile(`/(?:video|note)/(\d+)`)
	reModalID     = regexp.MustCompile(`modal_id=(\d+)`)
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
func (c *DouyinClient) ResolveShareURL(shareURL string) (*ResolveResult, error) {
	c.limiter.Acquire()

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
		return c.parseLongURL(shareURL)
	}
}

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

func (c *DouyinClient) parseLongURL(rawURL string) (*ResolveResult, error) {
	if m := reModalID.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}
	if m := reUserURL.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeUser, SecUID: m[1]}, nil
	}
	if m := reVideoURL.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}
	if m := reIesVideoURL.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}
	if m := rePathVideoID.FindStringSubmatch(rawURL); len(m) > 1 {
		return &ResolveResult{Type: URLTypeVideo, VideoID: m[1]}, nil
	}

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

var reRouterData = regexp.MustCompile(`window\._ROUTER_DATA\s*=\s*(.*?)</script>`)

// GetVideoDetail 获取单个视频详情
// 优先使用 douyin.com/aweme/v1/web/aweme/detail/ API（带 X-Bogus 签名，更可靠）
// 备选: iesdouyin.com/share/video/{id} 页面解析 _ROUTER_DATA
func (c *DouyinClient) GetVideoDetail(videoID string) (*DouyinVideo, error) {
	c.limiter.Acquire()

	// 尝试通过正式 API 获取（更可靠）
	video, err := c.getVideoDetailAPI(videoID)
	if err == nil {
		return video, nil
	}
	log.Printf("[douyin] detail API failed for %s: %v, falling back to page scrape", videoID, err)

	// 降级: 页面解析
	return c.getVideoDetailPage(videoID)
}

// getVideoDetailAPI 使用 douyin.com/aweme/v1/web/aweme/detail/ API 获取视频详情
func (c *DouyinClient) getVideoDetailAPI(videoID string) (*DouyinVideo, error) {
	apiURL := "https://www.douyin.com/aweme/v1/web/aweme/detail/?aweme_id=" + videoID

	parsed, err := url.Parse(apiURL)
	if err != nil {
		return nil, err
	}

	cookie := globalCookieMgr.getCookieString(c.normalClient)

	xBogus, err := signURL(parsed.RawQuery, pcUA)
	if err != nil {
		return nil, fmt.Errorf("sign failed: %w", err)
	}
	apiURL = fmt.Sprintf("%s&X-Bogus=%s", apiURL, xBogus)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Referer", "https://www.douyin.com/")
	req.Header.Set("User-Agent", pcUA)

	resp, err := c.normalClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch detail API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read detail API: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("detail API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var apiResp struct {
		StatusCode  int             `json:"status_code"`
		AwemeDetail json.RawMessage `json:"aweme_detail"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse detail API: %w", err)
	}

	if apiResp.AwemeDetail == nil || string(apiResp.AwemeDetail) == "null" {
		return nil, fmt.Errorf("aweme_detail is null (status_code=%d)", apiResp.StatusCode)
	}

	return parseAwemeDetail(apiResp.AwemeDetail, videoID, false)
}

// getVideoDetailPage 通过 iesdouyin.com 页面解析 _ROUTER_DATA（降级方案）
func (c *DouyinClient) getVideoDetailPage(videoID string) (*DouyinVideo, error) {
	pageURL := fmt.Sprintf("https://www.iesdouyin.com/share/video/%s", videoID)

	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
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

	isNote := false
	canonical := getCanonicalFromHTML(htmlStr)
	if strings.Contains(canonical, "/note/") {
		isNote = true
	}

	if isNote {
		return c.getNoteDetail(videoID)
	}

	m := reRouterData.FindSubmatch(body)
	if len(m) < 2 {
		return nil, fmt.Errorf("_ROUTER_DATA not found in page (status=%d, bodyLen=%d)", resp.StatusCode, len(body))
	}

	jsonBytes := bytes.TrimSpace(m[1])
	video, err := c.parseRouterDataForVideo(jsonBytes, videoID)
	if err != nil {
		return nil, err
	}

	// 跟随 302 重定向获取最终无水印地址
	if video.VideoURL != "" {
		if resolved, err := c.ResolveVideoURL(video.VideoURL); err == nil {
			log.Printf("[douyin] page scrape: resolved video URL via 302, len=%d", len(resolved))
			video.VideoURL = resolved
		} else {
			log.Printf("[douyin] page scrape: resolve 302 failed: %v, keeping original URL", err)
		}
	}

	return video, nil
}

func (c *DouyinClient) getNoteDetail(videoID string) (*DouyinVideo, error) {
	webID := generateWebID()
	aBogus := randAlphaNum(64)

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

func (c *DouyinClient) parseRouterDataForVideo(jsonBytes []byte, videoID string) (*DouyinVideo, error) {
	var data map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return nil, fmt.Errorf("parse router data json: %w", err)
	}

	loaderData, ok := data["loaderData"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("loaderData not found")
	}

	var videoPage map[string]interface{}
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

func parseAwemeDetail(raw json.RawMessage, videoID string, isNote bool) (*DouyinVideo, error) {
	var detail map[string]interface{}
	if err := json.Unmarshal(raw, &detail); err != nil {
		return nil, fmt.Errorf("parse aweme detail: %w", err)
	}

	video := &DouyinVideo{
		AwemeID: videoID,
		IsNote:  isNote,
	}

	if desc, ok := detail["desc"].(string); ok {
		video.Desc = desc
	}
	if ct, ok := detail["create_time"].(float64); ok {
		video.CreateTime = int64(ct)
	}

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

	if videoData, ok := detail["video"].(map[string]interface{}); ok {
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
		if dur, ok := videoData["duration"].(float64); ok {
			video.Duration = int(dur)
		}
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
			video.VideoURL = ""
		}
	}

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

// GetUserVideos 获取用户视频列表
// 使用 douyin.com/aweme/v1/web/aweme/post/ API + X-Bogus 签名
// 参考 lux 项目的实现，旧 iesdouyin.com API 已废弃（返回空 body）
// consecutiveErrors: 连续错误次数，用于指数退避限流（0=正常速率）
func (c *DouyinClient) GetUserVideos(secUID string, maxCursor int64, consecutiveErrors ...int) (*UserVideosResult, error) {
	errCount := 0
	if len(consecutiveErrors) > 0 {
		errCount = consecutiveErrors[0]
	}
	c.limiter.AcquireWithBackoff(errCount)








	cookie := globalCookieMgr.getCookieString(c.normalClient)

	// 构建 query 参数
	params := url.Values{}
	params.Set("sec_user_id", secUID)
	params.Set("max_cursor", fmt.Sprintf("%d", maxCursor))
	params.Set("count", "20")
	params.Set("cookie_enabled", "true")
	params.Set("platform", "PC")
	params.Set("downlink", "10")

	queryStr := params.Encode()

	// X-Bogus 签名
	xBogus, err := signURL(queryStr, pcUA)
	if err != nil {
		return nil, fmt.Errorf("sign failed: %w", err)
	}

	apiURL := fmt.Sprintf("https://www.douyin.com/aweme/v1/web/aweme/post/?%s&X-Bogus=%s", queryStr, xBogus)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Referer", "https://www.douyin.com/")
	req.Header.Set("User-Agent", pcUA)

	resp, err := c.normalClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch user videos: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read user videos: %w", err)
	}

	// 向限流器报告 HTTP 状态码（429/403/503 触发 penalty）
	c.limiter.ReportResult(resp.StatusCode)

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
		if len(item.Video.Cover.URLList) > 0 {
			v.Cover = pickNonWebpURLStr(item.Video.Cover.URLList)
		}
		if len(item.Video.PlayAddr.URLList) > 0 {
			v.VideoURL = strings.ReplaceAll(item.Video.PlayAddr.URLList[0], "playwm", "play")
		}
		if len(item.Images) > 0 {
			v.IsNote = true
			v.VideoURL = ""
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
// 使用 HEAD 请求（不下载 body），跟随 301/302 重定向获取最终无水印地址
func (c *DouyinClient) ResolveVideoURL(videoURL string) (string, error) {
	if videoURL == "" {
		return "", fmt.Errorf("empty video url")
	}

	req, err := http.NewRequest("HEAD", videoURL, nil)
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

func ExtractSecUID(rawURL string) (string, error) {
	if m := reUserURL.FindStringSubmatch(rawURL); len(m) > 1 {
		return m[1], nil
	}
	return "", fmt.Errorf("unable to extract sec_user_id from: %s", rawURL)
}

func IsDouyinURL(rawURL string) bool {
	return strings.Contains(rawURL, "douyin.com") || strings.Contains(rawURL, "iesdouyin.com")
}

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

func generateWebID() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("75%015d", r.Int63n(1e15))
}

func randAlphaNum(n int) string {
	const letters = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

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

func pickNonWebpURLStr(urls []string) string {
	var first string
	for _, s := range urls {
		if s == "" {			continue
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

func getCanonicalFromHTML(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return ""
	}
	return findCanonical(doc)
}

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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
