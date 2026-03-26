package douyin

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

// ErrDouyinRiskControl 定义在 error.go

//go:embed sign.js
var signScript string

// UA 池: 移动端 UA（parse-video 用 iPhone UA 效果最好）
var mobileUAPool = []string{
	// iPhone Safari (iOS 17-18)
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.3 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
	// Android Chrome (最新版本)
	"Mozilla/5.0 (Linux; Android 15; Pixel 9 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Mobile Safari/537.36",
}

// PC 端 UA 池（用于需要签名的 API 请求）
var pcUAPool = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
}

// sec-ch-ua Client Hints 映射表（按 Chrome 主版本号）
// 抖音服务端通过 Client Hints 进一步验证浏览器身份一致性
var clientHintsMap = map[string]ClientHints{
	"131": {SecChUA: `"Chromium";v="131", "Google Chrome";v="131", "Not_A Brand";v="24"`, SecChUAMobile: "?0", SecChUAPlatform: `"Windows"`},
	"130": {SecChUA: `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`, SecChUAMobile: "?0", SecChUAPlatform: `"Windows"`},
	"129": {SecChUA: `"Chromium";v="129", "Google Chrome";v="129", "Not=A?Brand";v="8"`, SecChUAMobile: "?0", SecChUAPlatform: `"Windows"`},
	"128": {SecChUA: `"Chromium";v="128", "Google Chrome";v="128", "Not;A=Brand";v="99"`, SecChUAMobile: "?0", SecChUAPlatform: `"Windows"`},
	"127": {SecChUA: `"Chromium";v="127", "Google Chrome";v="127", "Not)A;Brand";v="99"`, SecChUAMobile: "?0", SecChUAPlatform: `"Windows"`},
}

// ClientHints sec-ch-ua 相关头部
type ClientHints struct {
	SecChUA         string
	SecChUAMobile   string
	SecChUAPlatform string
}

// pickUA 随机选择移动端 UA
func pickUA() string {
	return mobileUAPool[rand.Intn(len(mobileUAPool))]
}

// pickPCUA 随机选择 PC 端 UA
func pickPCUA() string {
	return pcUAPool[rand.Intn(len(pcUAPool))]
}

// extractChromeVersion 从 UA 中提取 Chrome 主版本号
func extractChromeVersion(ua string) string {
	re := regexp.MustCompile(`Chrome/(\d+)`)
	if m := re.FindStringSubmatch(ua); len(m) > 1 {
		return m[1]
	}
	return ""
}

// setClientHints 为请求设置 sec-ch-ua Client Hints 头部
func setClientHints(req *http.Request, ua string) {
	ver := extractChromeVersion(ua)
	if hints, ok := clientHintsMap[ver]; ok {
		req.Header.Set("sec-ch-ua", hints.SecChUA)
		req.Header.Set("sec-ch-ua-mobile", hints.SecChUAMobile)
		req.Header.Set("sec-ch-ua-platform", hints.SecChUAPlatform)
	}
}

// DouyinClient 抖音 API 客户端
type DouyinClient struct {
	noRedirectClient *http.Client          // 不跟随重定向
	normalClient     *http.Client          // 正常 client
	limiter          *RateLimiter
	fingerprint      *BrowserFingerprint   // 会话指纹（同一 client 实例内保持一致）
	sessionMsToken   string                // 会话级 msToken（Cookie 中的 msToken 保持一致）
}

// getSessionCookie 返回使用会话级 msToken 的 Cookie
// msToken 在 client 生命周期内保持一致（抖音要求翻页 msToken 一致）
// verify_fp / s_v_web_id 每次重新生成（模拟真实浏览器行为）
func (c *DouyinClient) getSessionCookie() string {
	if c.sessionMsToken == "" {
		c.sessionMsToken = generateMsToken()
	}
	// 不缓存完整 cookie，每次重新构造（verify_fp/s_v_web_id 重新随机）
	return globalCookieMgr.getCookieStringWithMsToken(c.normalClient, c.sessionMsToken)
}

// NewClient 创建抖音客户端（使用会话级指纹，确保同一实例内请求一致性）
func NewClient() *DouyinClient {
	fp := GetSessionFingerprint()
	logger.Info("client created", "fingerprint", fp)
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
		limiter:     DefaultRateLimiter(),
		fingerprint: fp,
	}
}

// Close 关闭客户端，停止 RateLimiter 的 refill goroutine，释放下载连接池资源
// 每次 NewClient() 都会创建新的 RateLimiter goroutine，必须在使用完毕后调用 Close()
func (c *DouyinClient) Close() {
	if c.limiter != nil {
		c.limiter.Stop()
	}
	CloseDownloadClients()
}

// ---- X-Bogus 签名 ----

// signURL 使用池化的 goja VM 执行 sign.js 计算 X-Bogus 签名
// VM 池预编译 sign.js 为 goja.Program，复用 VM 实例，避免每次都重新创建和解析
func signURL(queryStr, userAgent string) (string, error) {
	pool, err := getSignPool()
	if err != nil {
		return "", fmt.Errorf("get sign pool: %w", err)
	}
	return pool.sign(queryStr, userAgent)
}

// signABogusURL 使用池化的 goja VM 执行 a_bogus.js 计算 a_bogus 签名
func signABogusURL(queryStr, userAgent string) (string, error) {
	pool, err := getABogusPool()
	if err != nil {
		return "", fmt.Errorf("get a_bogus pool: %w", err)
	}
	return pool.sign(queryStr, userAgent)
}

// SignResult 签名结果
type SignResult struct {
	ABogus string // a_bogus 签名值（非空表示成功）
	XBogus string // X-Bogus 签名值（非空表示成功）
}

// signURLWithFallback 带降级的三级签名策略链
// 优先级: a_bogus → X-Bogus → 无签名
// 返回: (xBogus, signed bool) — 保持向后兼容，X-Bogus 仍作为 fallback
func signURLWithFallback(queryStr, userAgent string) (string, bool) {
	xBogus, err := signURL(queryStr, userAgent)
	if err != nil {
		logger.Warn("X-Bogus sign failed, degrading to unsigned", "error", err)
		return "", false
	}
	return xBogus, true
}

// signURLWithABogus 三级签名降级链
// 优先级: a_bogus → X-Bogus → 无签名
// 返回 SignResult，调用方根据结果决定如何拼接 URL
func signURLWithABogus(queryStr, userAgent string) SignResult {
	// 第一级: a_bogus
	aBogus, err := signABogusURL(queryStr, userAgent)
	if err == nil && aBogus != "" {
		// a_bogus 成功，同时也获取 X-Bogus（部分 API 需要）
		xBogus, _ := signURL(queryStr, userAgent)
		return SignResult{ABogus: aBogus, XBogus: xBogus}
	}
	logger.Warn("a_bogus sign failed, degrading to X-Bogus", "error", err)

	// 第二级: X-Bogus
	xBogus, err := signURL(queryStr, userAgent)
	if err == nil && xBogus != "" {
		return SignResult{XBogus: xBogus}
	}
	logger.Warn("X-Bogus sign failed, degrading to unsigned", "error", err)

	// 第三级: 无签名
	return SignResult{}
}

// applySignResult 将签名结果应用到 URL query string
// 返回最终的完整 API URL
func applySignResult(baseURL, queryStr string, sr SignResult) string {
	url := fmt.Sprintf("%s?%s", baseURL, queryStr)
	if sr.ABogus != "" {
		url += "&a_bogus=" + sr.ABogus
	}
	if sr.XBogus != "" {
		url += "&X-Bogus=" + sr.XBogus
	}
	return url
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
	logger.Info("detail API unavailable, using page scrape fallback", "videoID", videoID, "error", err)

	// 降级: 页面解析
	return c.getVideoDetailPage(videoID)
}

// getVideoDetailAPI 使用 douyin.com/aweme/v1/web/aweme/detail/ API 获取视频详情
func (c *DouyinClient) getVideoDetailAPI(videoID string) (*DouyinVideo, error) {
	cookie := c.getSessionCookie()

	// 构建完整的 query 参数（参考 f2 BaseRequestModel + PostDetail）
	params := c.buildBaseParams()
	params.Set("aweme_id", videoID)

	queryStr := params.Encode()

	ua := c.fingerprint.UserAgent
	sr := signURLWithABogus(queryStr, ua)
	apiURL := applySignResult(VideoDetailAPI, queryStr, sr)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
	c.setFullHeaders(req)

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
		if len(body) < 100 {
			return nil, fmt.Errorf("%w: API returned truncated response (bodyLen=%d)", ErrDouyinRiskControl, len(body))
		}
		return nil, fmt.Errorf("parse detail API: %w", err)
	}

	if apiResp.AwemeDetail == nil || string(apiResp.AwemeDetail) == "null" {
		// status_code 诊断
		switch apiResp.StatusCode {
		case 2053:
			return nil, fmt.Errorf("%w: IP risk control (status_code=2053), aweme_detail is null", ErrDouyinRiskControl)
		case 2154:
			return nil, fmt.Errorf("%w: rate limited (status_code=2154), aweme_detail is null", ErrDouyinRiskControl)
		case 8:
			return nil, fmt.Errorf("video not found (status_code=8)")
		default:
			return nil, fmt.Errorf("aweme_detail is null (status_code=%d)", apiResp.StatusCode)
		}
	}

	return parseAwemeDetail(apiResp.AwemeDetail, videoID, false)
}

// getVideoDetailPage 通过 iesdouyin.com 页面解析 _ROUTER_DATA（降级方案）
func (c *DouyinClient) getVideoDetailPage(videoID string) (*DouyinVideo, error) {
	pageURL := BuildVideoPageURL(videoID)

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

	// 图集检测（多信号）:
	// 1. canonical link 包含 /note/
	// 2. 页面 URL 本身包含 /note/
	// 3. og:type 或 og:url 包含 note 信息
	// 4. _ROUTER_DATA 中的 aweme_type == 68
	isNote := false
	canonical := getCanonicalFromHTML(htmlStr)
	if strings.Contains(canonical, "/note/") {
		isNote = true
		logger.Info("note detected via canonical link", "canonical", canonical)
	}

	// 检查 og:url meta tag（有些情况 canonical 不可靠但 og:url 正确）
	if !isNote {
		ogURL := getMetaContent(htmlStr, "og:url")
		if strings.Contains(ogURL, "/note/") {
			isNote = true
			logger.Info("note detected via og:url", "ogURL", ogURL)
		}
	}

	if isNote {
		video, err := c.getNoteDetail(videoID)
		if err == nil {
			return video, nil
		}
		// getNoteDetail 失败时降级到页面解析（body 已在手）
		logger.Info("getNoteDetail unavailable, using page parse fallback", "videoID", videoID, "error", err)
	}

	// 风控检测: 小 body 通常是验证码/captcha 页面
	if len(body) < 5000 {
		return nil, fmt.Errorf("%w: page too small (bodyLen=%d), likely captcha/verification", ErrDouyinRiskControl, len(body))
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

	// 跟随 302 重定向获取最终无水印地址，并标记已 resolve（避免 process.go 二次 resolve 触发 403）
	if video.VideoURL != "" {
		if resolved, err := c.ResolveVideoURL(video.VideoURL); err == nil {
			logger.Info("page scrape: resolved video URL via 302", "urlLen", len(resolved))
			video.VideoURL = resolved
			video.URLResolved = true
		} else {
			logger.Warn("page scrape: resolve 302 failed, keeping original URL", "error", err)
		}
	}

	return video, nil
}

func (c *DouyinClient) getNoteDetail(videoID string) (*DouyinVideo, error) {
	webID := generateWebID()
	aBogus := randAlphaNum(64)

	apiURL := BuildNoteSlideInfoURL(webID, videoID, aBogus)

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

	// 风控检测: filter_list 包含被过滤的视频
	if filterList, ok := videoInfoRes["filter_list"].([]interface{}); ok && len(filterList) > 0 {
		for _, item := range filterList {
			fm, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			fmID, _ := fm["aweme_id"].(string)
			reason, _ := fm["filter_reason"].(string)
			detail, _ := fm["detail_msg"].(string)
			filterType, _ := fm["filter_type"].(float64)

			// 记录所有被过滤的视频（不仅仅是当前请求的）
			logger.Warn("filter_list detected", "awemeID", fmID, "reason", reason, "filterType", filterType, "detail", detail)

			if fmID == videoID || fmID == "" {
				errMsg := fmt.Sprintf("video filtered (type=%.0f): %s", filterType, reason)
				if detail != "" {
					errMsg += " - " + detail
				}
				return nil, fmt.Errorf("%s", errMsg)
			}
		}
	}

	// 检查 status_code（API 级别的风控）
	if statusCode, ok := videoInfoRes["status_code"].(float64); ok && int(statusCode) != 0 {
		statusMsg, _ := videoInfoRes["status_msg"].(string)
		logger.Warn("videoInfoRes risk control", "statusCode", int(statusCode), "statusMsg", statusMsg)
		// 常见风控 status_code:
		// 2053: IP 限制
		// 2154: 请求过于频繁
		// 8: 视频不存在
		if int(statusCode) == 2053 || int(statusCode) == 2154 {
			return nil, fmt.Errorf("risk control: status_code=%d msg=%s", int(statusCode), statusMsg)
		}
	}

	itemList, ok := videoInfoRes["item_list"].([]interface{})
	if !ok || len(itemList) == 0 {
		// 额外诊断: 打印 videoInfoRes 的 key 帮助排查
		keys := make([]string, 0)
		for k := range videoInfoRes {
			keys = append(keys, k)
		}
		logger.Warn("item_list empty", "videoInfoResKeys", keys)
		return nil, fmt.Errorf("item_list empty or not found (keys: %v)", keys)
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

	// 检测图集类型（aweme_type == 68 表示图集）
	if awemeType, ok := detail["aweme_type"].(float64); ok && int(awemeType) == 68 {
		video.IsNote = true
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
// 使用 douyin.com/aweme/v1/web/aweme/post/ API + a_bogus/X-Bogus 签名
// 参考 f2 项目的 BaseRequestModel，发送完整的浏览器指纹参数
// consecutiveErrors: 连续错误次数，用于指数退避限流（0=正常速率）
func (c *DouyinClient) GetUserVideos(secUID string, maxCursor int64, consecutiveErrors ...int) (*UserVideosResult, error) {
	errCount := 0
	if len(consecutiveErrors) > 0 {
		errCount = consecutiveErrors[0]
	}
	c.limiter.AcquireWithBackoff(errCount)








	cookie := c.getSessionCookie()

	// 构建完整的 query 参数（参考 f2 BaseRequestModel + UserPost）
	params := c.buildBaseParams()
	params.Set("sec_user_id", secUID)
	params.Set("max_cursor", fmt.Sprintf("%d", maxCursor))
	params.Set("count", "20")

	queryStr := params.Encode()

	// 三级签名降级链: a_bogus → X-Bogus → 无签名
	ua := c.fingerprint.UserAgent
	sr := signURLWithABogus(queryStr, ua)
	apiURL := applySignResult(UserVideosAPI, queryStr, sr)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
	c.setFullHeaders(req)

	resp, err := c.normalClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch user videos: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read user videos: %w", err)
	}

	// 诊断日志：记录原始响应信息，方便排查空 body 等问题
	logger.Info("GetUserVideos response",
		"secUID", secUID,
		"cursor", maxCursor,
		"statusCode", resp.StatusCode,
		"contentLength", resp.ContentLength,
		"contentType", resp.Header.Get("Content-Type"),
		"bodyLen", len(body))

	// 小 body 完整记录，方便排查翻页失败
	if len(body) < 200 {
		logger.Info("GetUserVideos small body", "body", string(body))
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("user videos API returned empty body (status=%d, contentType=%s)",
			resp.StatusCode, resp.Header.Get("Content-Type"))
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
		HasMore   int   `json:"has_more"` // 抖音返回 1/0 而非 true/false
		MaxCursor int64 `json:"max_cursor"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse user videos: %w (body=%s)", err, truncate(string(body), 200))
	}

	// 记录 API status_code（非零值通常表示风控或参数错误）
	if apiResp.StatusCode != 0 {
		logger.Warn("GetUserVideos non-zero status_code",
			"statusCode", apiResp.StatusCode,
			"cursor", maxCursor,
			"bodyLen", len(body))
	}

	result := &UserVideosResult{
		HasMore:   apiResp.HasMore == 1,
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

	logger.Info("GetUserVideos completed", "secUID", secUID, "cursor", maxCursor, "got", len(result.Videos), "hasMore", result.HasMore, "nextCursor", result.MaxCursor)

	return result, nil
}



// GetUserProfile 获取抖音用户详情（头像、简介、粉丝数等）
// 使用 /aweme/v1/web/user/profile/other/ API + X-Bogus 签名
func (c *DouyinClient) GetUserProfile(secUID string) (*DouyinUserProfile, error) {
	c.limiter.Acquire()

	cookie := c.getSessionCookie()

	params := c.buildBaseParams()
	params.Set("sec_user_id", secUID)

	queryStr := params.Encode()

	ua := c.fingerprint.UserAgent
	sr := signURLWithABogus(queryStr, ua)
	apiURL := applySignResult(UserProfileAPI, queryStr, sr)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
	c.setFullHeaders(req)

	resp, err := c.normalClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch user profile: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read user profile: %w", err)
	}

	// 小 body 完整记录，方便排查翻页失败
	if len(body) < 200 {
		logger.Info("GetUserProfile small body", "body", string(body))
	}

	c.limiter.ReportResult(resp.StatusCode)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("user profile API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var apiResp struct {
		StatusCode int `json:"status_code"`
		User       struct {
			UID              string `json:"uid"`
			SecUID           string `json:"sec_uid"`
			ShortID          string `json:"short_id"`
			Nickname         string `json:"nickname"`
			Signature        string `json:"signature"`
			AvatarLarger     struct {
				URLList []string `json:"url_list"`
			} `json:"avatar_larger"`
			AvatarMedium     struct {
				URLList []string `json:"url_list"`
			} `json:"avatar_medium"`
			AvatarThumb      struct {
				URLList []string `json:"url_list"`
			} `json:"avatar_thumb"`
			FollowerCount    int64  `json:"follower_count"`
			FollowingCount   int64  `json:"following_count"`
			TotalFavorited   int64  `json:"total_favorited"`
			AwemeCount       int64  `json:"aweme_count"`
			FavoritingCount  int64  `json:"favoriting_count"`
			UniqueID         string `json:"unique_id"`
			IPLocation       string `json:"ip_location"`
		} `json:"user"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse user profile: %w (body=%s)", err, truncate(string(body), 200))
	}

	if apiResp.StatusCode != 0 {
		return nil, fmt.Errorf("user profile API status_code=%d", apiResp.StatusCode)
	}

	u := apiResp.User
	profile := &DouyinUserProfile{
		UID:            u.UID,
		SecUID:         u.SecUID,
		ShortID:        u.ShortID,
		UniqueID:       u.UniqueID,
		Nickname:       u.Nickname,
		Signature:      u.Signature,
		FollowerCount:  u.FollowerCount,
		FollowingCount: u.FollowingCount,
		TotalFavorited: u.TotalFavorited,
		AwemeCount:     u.AwemeCount,
		FavoritingCount: u.FavoritingCount,
		IPLocation:     u.IPLocation,
	}

	// 选择头像 URL（优先大图）
	if len(u.AvatarLarger.URLList) > 0 {
		profile.AvatarURL = u.AvatarLarger.URLList[0]
	} else if len(u.AvatarMedium.URLList) > 0 {
		profile.AvatarURL = u.AvatarMedium.URLList[0]
	} else if len(u.AvatarThumb.URLList) > 0 {
		profile.AvatarURL = u.AvatarThumb.URLList[0]
	}

	logger.Info("GetUserProfile completed", "nickname", profile.Nickname, "uniqueID", profile.UniqueID, "followers", profile.FollowerCount, "videos", profile.AwemeCount)

	return profile, nil
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

// GetUserByUniqueID 通过抖音号（uniqueID/unique_id）查询用户，返回 DouyinUserProfile
// 使用 /aweme/v1/web/query/user/ API + ABogus 签名
func (c *DouyinClient) GetUserByUniqueID(uniqueID string) (*DouyinUserProfile, error) {
	c.limiter.Acquire()

	cookie := c.getSessionCookie()

	params := c.buildBaseParams()
	params.Set("unique_id", uniqueID)

	queryStr := params.Encode()
	ua := c.fingerprint.UserAgent
	sr := signURLWithABogus(queryStr, ua)
	apiURL := applySignResult(QueryUserAPI, queryStr, sr)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
	c.setFullHeaders(req)

	resp, err := c.normalClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query user by uniqueID: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read query user: %w", err)
	}

	if len(body) < 200 {
		logger.Info("GetUserByUniqueID small body", "body", string(body))
	}

	c.limiter.ReportResult(resp.StatusCode)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query user API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var apiResp struct {
		StatusCode int `json:"status_code"`
		UserList   []struct {
			UserInfo struct {
				UID          string `json:"uid"`
				SecUID       string `json:"sec_uid"`
				ShortID      string `json:"short_id"`
				UniqueID     string `json:"unique_id"`
				Nickname     string `json:"nickname"`
				Signature    string `json:"signature"`
				AvatarLarger struct {
					URLList []string `json:"url_list"`
				} `json:"avatar_larger"`
				FollowerCount   int64  `json:"follower_count"`
				FollowingCount  int64  `json:"following_count"`
				TotalFavorited  int64  `json:"total_favorited"`
				AwemeCount      int64  `json:"aweme_count"`
				FavoritingCount int64  `json:"favoriting_count"`
				IPLocation      string `json:"ip_location"`
			} `json:"user_info"`
		} `json:"user_list"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse query user: %w (body=%s)", err, truncate(string(body), 200))
	}

	if apiResp.StatusCode != 0 {
		return nil, fmt.Errorf("query user API status_code=%d (uniqueID=%s)", apiResp.StatusCode, uniqueID)
	}

	if len(apiResp.UserList) == 0 {
		return nil, fmt.Errorf("抖音号 @%s 未找到对应用户", uniqueID)
	}

	u := apiResp.UserList[0].UserInfo
	profile := &DouyinUserProfile{
		UID:             u.UID,
		SecUID:          u.SecUID,
		ShortID:         u.ShortID,
		UniqueID:        u.UniqueID,
		Nickname:        u.Nickname,
		Signature:       u.Signature,
		FollowerCount:   u.FollowerCount,
		FollowingCount:  u.FollowingCount,
		TotalFavorited:  u.TotalFavorited,
		AwemeCount:      u.AwemeCount,
		FavoritingCount: u.FavoritingCount,
		IPLocation:      u.IPLocation,
	}
	if len(u.AvatarLarger.URLList) > 0 {
		profile.AvatarURL = u.AvatarLarger.URLList[0]
	}

	logger.Info("GetUserByUniqueID completed", "uniqueID", uniqueID, "nickname", profile.Nickname, "secUID", profile.SecUID)
	return profile, nil
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

// douyinSpaceCollapser 用于合并连续空格
var douyinSpaceCollapser = regexp.MustCompile(`\s{2,}`)

// douyinHashtagRemover 用于去除抖音标题中的 hashtag（#话题 或 # 话题）
// 匹配 # 后跟非空白字符的词，直到空白或字符串结尾
var douyinHashtagRemover = regexp.MustCompile(`#\S+`)

func SanitizePath(name string) string {
	// 第零轮：去除 hashtag（#话题 类），抖音标题常用，保留在文件名里是噪音
	name = douyinHashtagRemover.ReplaceAllString(name, "")

	// 第一轮：替换文件系统非法字符（# 已在上一步清除，保留此步处理其他字符）
	for _, c := range []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*", "#", "@"} {
		name = strings.ReplaceAll(name, c, "_")
	}

	// 第二轮：过滤 Unicode 不可见/控制字符
	name = strings.Map(func(r rune) rune {
		switch {
		case r <= 0x001F: // C0 控制字符
			return -1
		case r == 0x007F: // DEL
			return -1
		case r >= 0x0080 && r <= 0x009F: // C1 控制字符
			return -1
		case r == 0xFFFD: // 替换字符
			return -1
		case r >= 0x200B && r <= 0x200F: // 零宽字符
			return -1
		case r >= 0x2028 && r <= 0x202F: // 行/段分隔符
			return -1
		case r >= 0x2060 && r <= 0x206F: // 不可见格式字符
			return -1
		case r == 0xFEFF: // BOM
			return -1
		case r >= 0xFE00 && r <= 0xFE0F: // 变体选择器
			return -1
		case unicode.Is(unicode.So, r): // Symbol, Other（emoji/symbols, NAS 兼容）
			return -1
		case r > 0xFFFF: // 非 BMP 字符（补充平面 emoji/symbols，NAS 不兼容）
			return -1
		case r >= 0xE0000 && r <= 0xE007F: // 标签字符
			return -1
		default:
			return r
		}
	}, name)

	// 第三轮：清理空格
	name = douyinSpaceCollapser.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)

	// 特殊值检查
	if name == "" || name == "." || name == ".." {
		return "unknown"
	}

	// 截断到 80 字符
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}

	return name
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

// getMetaContent 从 HTML 中提取指定 meta tag 的 content 值
func getMetaContent(htmlStr string, property string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return ""
	}
	return findMetaContent(doc, property)
}

func findMetaContent(n *html.Node, property string) string {
	if n.Type == html.ElementNode && n.Data == "meta" {
		var prop, content string
		for _, attr := range n.Attr {
			switch attr.Key {
			case "property", "name":
				prop = attr.Val
			case "content":
				content = attr.Val
			}
		}
		if prop == property && content != "" {
			return content
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findMetaContent(c, property); result != "" {
			return result
		}
	}
	return ""
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

// GetMixVideos 获取抖音合集中的所有视频列表
// 使用 MixVideosAPI (cursor-based 分页)，最多返回 500 条
func (c *DouyinClient) GetMixVideos(mixID string) ([]DouyinVideo, error) {
	const maxVideos = 500
	var allVideos []DouyinVideo
	cursor := int64(0)
	errCount := 0

	for {
		if len(allVideos) >= maxVideos {
			logger.Info("GetMixVideos reached max limit", "mixID", mixID, "limit", maxVideos)
			break
		}

		c.limiter.AcquireWithBackoff(errCount)

		cookie := c.getSessionCookie()

		params := c.buildBaseParams()
		params.Set("mix_id", mixID)
		params.Set("cursor", fmt.Sprintf("%d", cursor))
		params.Set("count", "20")

		queryStr := params.Encode()

		ua := c.fingerprint.UserAgent
		sr := signURLWithABogus(queryStr, ua)
		apiURL := applySignResult(MixVideosAPI, queryStr, sr)

		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Cookie", cookie)
		c.setFullHeaders(req)

		resp, err := c.normalClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch mix videos: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read mix videos: %w", err)
		}

		logger.Info("GetMixVideos response",
			"mixID", mixID,
			"cursor", cursor,
			"statusCode", resp.StatusCode,
			"bodyLen", len(body))

		if len(body) == 0 {
			return nil, fmt.Errorf("mix videos API returned empty body (status=%d)", resp.StatusCode)
		}

		c.limiter.ReportResult(resp.StatusCode)

		if resp.StatusCode != 200 {
			snippet := string(body)
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			return nil, fmt.Errorf("mix videos API returned %d: %s", resp.StatusCode, snippet)
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
			HasMore int   `json:"has_more"` // 抖音返回 1/0
			Cursor  int64 `json:"cursor"`
		}

		if err := json.Unmarshal(body, &apiResp); err != nil {
			return nil, fmt.Errorf("parse mix videos: %w (body=%s)", err, truncate(string(body), 200))
		}

		if apiResp.StatusCode != 0 {
			logger.Warn("GetMixVideos non-zero status_code",
				"statusCode", apiResp.StatusCode,
				"mixID", mixID,
				"cursor", cursor)
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
			allVideos = append(allVideos, v)
		}

		logger.Info("GetMixVideos page completed",
			"mixID", mixID,
			"cursor", cursor,
			"got", len(apiResp.AwemeList),
			"total", len(allVideos),
			"hasMore", apiResp.HasMore == 1,
			"nextCursor", apiResp.Cursor)

		if apiResp.HasMore != 1 || len(apiResp.AwemeList) == 0 {
			break
		}
		cursor = apiResp.Cursor
		errCount = 0
	}

	return allVideos, nil
}
