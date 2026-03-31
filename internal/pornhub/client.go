package pornhub

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// phMaxHTMLBodySize HTML 页面最大读取大小（10 MB），防止无限流撑爆内存
const phMaxHTMLBodySize = 10 * 1024 * 1024

// pageDelay GetModelVideos 翻页间延迟，避免被限流 [FIXED: P1-10]
const pageDelay = 2 * time.Second

const (
	phBaseURL   = "https://www.pornhub.com"
	phUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

var (
	// flashvarsVarRe 按优先级依次匹配多种已知的 PH 播放器变量名格式：
	//   flashvars_\d+        — 经典格式（2023 前后）
	//   var flashvars\b      — 简化格式
	//   window\.flashvars\b  — window 挂载格式
	//   LRT_VIDEO_VARS       — 部分改版页面
	//   VIDEO_VARS           — 另一种简化命名
	// 每个 pattern 独立尝试，取第一个命中的
	flashvarsVarPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(flashvars_\d+)`),
		regexp.MustCompile(`var\s+(flashvars)\s*=\s*\{`),
		regexp.MustCompile(`window\.(flashvars)\s*=`),
		regexp.MustCompile(`(LRT_VIDEO_VARS)`),
		regexp.MustCompile(`(VIDEO_VARS)`),
	}
	// flashvarsVarRe 保留兼容旧调用（只用于首选格式）
	flashvarsVarRe = flashvarsVarPatterns[0]

	viewKeyRe = regexp.MustCompile(`[?&]viewkey=([a-zA-Z0-9_]+)`)

	// scriptKeywords 按优先级排列：找到含 mediaDefinitions 的 script 为首选，
	// 兼容多种 PH 页面结构变体
	scriptKeywords = []string{
		"mediaDefinitions",
		"flashvars_",
		"flashvars",
		"LRT_VIDEO_VARS",
		"VIDEO_VARS",
	}
)

// Client Pornhub HTTP 客户端
type Client struct {
	httpClient *http.Client
	mu         sync.RWMutex // [FIXED: PH-2] 保护 cookie 并发读写
	cookie     string
}

// NewClient 创建 Client，自动读取 HTTPS_PROXY / HTTP_PROXY 代理
// cookie 参数可为空（匿名模式）
func NewClient(cookie ...string) *Client {
	transport := &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		TLSHandshakeTimeout: 15 * time.Second,
	}

	// 代理支持
	proxyURL := os.Getenv("HTTPS_PROXY")
	if proxyURL == "" {
		proxyURL = os.Getenv("HTTP_PROXY")
	}
	if proxyURL != "" {
		if parsed, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}

	c := &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}
	if len(cookie) > 0 {
		c.cookie = strings.TrimSpace(cookie[0])
	}
	return c
}

// SetCookie 设置用户 Cookie
func (c *Client) SetCookie(cookie string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cookie = strings.TrimSpace(cookie)
}

// getCookie 线程安全地读取 cookie
func (c *Client) getCookie() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cookie
}

// getWithCookie 发送 GET 请求并使用指定的 cookie（不修改 c.cookie，用于临时覆盖场景）
func (c *Client) getWithCookie(rawURL, cookie string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", phUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Connection", "keep-alive")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http get %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body %s: %w", rawURL, err)
	}
	return body, resp.StatusCode, nil
}

// Close 释放资源
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}

// GetVideoThumbnail 从视频详情页提取封面图 URL（og:image meta 标签）
// 用于补充历史遗留的空 thumbnail 记录
func (c *Client) GetVideoThumbnail(videoPageURL string) string {
	body, status, err := c.get(videoPageURL)
	if err != nil || status != 200 {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return ""
	}
	var thumb string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if thumb != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "meta" {
			prop := getAttr(n, "property")
			name := getAttr(n, "name")
			if prop == "og:image" || name == "twitter:image" {
				if content := getAttr(n, "content"); content != "" && !strings.HasPrefix(content, "data:image/gif") {
					thumb = content
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return thumb
}

// get 发送 GET 请求，返回响应体字节
func (c *Client) get(rawURL string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("User-Agent", phUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	// 不手动设置 Accept-Encoding，让 Go http.Client 自动处理 gzip 解压
	// 手动设置后 Go 不会自动解压，会拿到原始压缩数据
	req.Header.Set("Connection", "keep-alive")
	if cookie := c.getCookie(); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http get %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, phMaxHTMLBodySize))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}

	return body, resp.StatusCode, nil
}

// getJSON 发送 GET 请求，期望 JSON 响应
func (c *Client) getJSON(rawURL string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("User-Agent", phUserAgent)
	req.Header.Set("Accept", "application/json, */*")
	if cookie := c.getCookie(); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http get json %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024)) // JSON 响应最大 1 MB
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}

	return body, resp.StatusCode, nil
}

// ExtractViewKey 从 URL 中提取 viewkey 参数作为 video_id
func ExtractViewKey(videoURL string) string {
	m := viewKeyRe.FindStringSubmatch(videoURL)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// GetModelInfo 获取博主基本信息（名称）
func (c *Client) GetModelInfo(modelURL string) (*ModelInfo, error) {
	// 规范化：去掉 query string、末尾斜杠、/videos 后缀
	// [FIXED: P2-4] 只精确去掉路径末尾的 /videos segment，避免博主名含 "videos" 时被误裁
	if idx := strings.IndexByte(modelURL, '?'); idx != -1 {
		modelURL = modelURL[:idx]
	}
	cleanURL := strings.TrimRight(modelURL, "/")
	if strings.HasSuffix(cleanURL, "/videos") {
		cleanURL = cleanURL[:len(cleanURL)-len("/videos")]
	}
	cleanURL = strings.TrimRight(cleanURL, "/")

	body, status, err := c.get(cleanURL)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, fmt.Errorf("%w: model page returned 404", ErrUnavailable)
	}
	if status != 200 {
		return nil, NewPHErrorAuto(status, "model page returned non-200")
	}

	// 解析 <title> 标签提取博主名称
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	name := extractModelName(doc, cleanURL)

	return &ModelInfo{
		Name:     name,
		ModelURL: cleanURL,
	}, nil
}

// extractModelName 从 HTML 中提取博主名称
func extractModelName(doc *html.Node, modelURL string) string {
	// 尝试从 <h1 class="title"> 或 .pcVideoListItem 等元素提取
	// 简单策略：从 <title> 标签中提取 "Name Porn Videos" 之前的部分
	var titleText string
	var findTitle func(*html.Node)
	findTitle = func(n *html.Node) {
		if titleText != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "title" {
			if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
				titleText = n.FirstChild.Data
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findTitle(c)
		}
	}
	findTitle(doc)

	if titleText != "" {
		// 中文格式: "博主名 的色情片 | Pornhub" 或 "博主名的色情片 | Pornhub"
		if idx := strings.Index(titleText, "的色情片"); idx > 0 {
			return strings.TrimSpace(titleText[:idx])
		}
		// 典型格式: "ModelName Porn Videos | Pornhub.com"
		parts := strings.Split(titleText, " Porn Videos")
		if len(parts) >= 1 && parts[0] != "" {
			return strings.TrimSpace(parts[0])
		}
		// 备用：取 | 之前的部分
		parts = strings.Split(titleText, " | ")
		if len(parts) >= 1 && parts[0] != "" {
			return strings.TrimSpace(parts[0])
		}
	}

	// 最后兜底：从 URL 路径取最后一段
	parts := strings.Split(strings.TrimRight(modelURL, "/"), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// validateModelPage 校验页面确实属于该博主，防止 Pornhub 静默重定向到首页/推荐页
// 检查策略：若 title 命中已知的推荐页/首页特征，则判定为重定向，否则放行
func validateModelPage(doc *html.Node, modelBaseURL string) error {
	// 提取 <title> 文本
	var titleText string
	var find func(*html.Node)
	find = func(n *html.Node) {
		if titleText != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "title" {
			if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
				titleText = n.FirstChild.Data
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)

	if titleText == "" {
		return nil // 无 title，跳过校验
	}

	// 已知的推荐页/首页 title 特征（命中任意一条则判定为重定向）
	lowerTitle := strings.ToLower(titleText)
	redirectSigns := []string{
		"pornhub - the best free porn",
		"free porn videos & sex movies",
		"pornhub.com - the best free porn",
	}
	for _, sign := range redirectSigns {
		if strings.Contains(lowerTitle, sign) {
			return fmt.Errorf("page title %q looks like recommendation/home page (redirected), model url: %s", titleText, modelBaseURL)
		}
	}
	return nil
}

// GetModelVideos 获取博主视频列表（全量翻页）
func (c *Client) GetModelVideos(modelURL string) ([]Video, error) {
	// 规范化：去掉 query string、末尾斜杠、/videos 后缀
	if idx := strings.IndexByte(modelURL, '?'); idx != -1 {
		modelURL = modelURL[:idx]
	}
	baseURL := strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(modelURL, "/"), "/videos"), "/")
	videosURL := baseURL + "/videos"

	// 第一页：同时获取最大页数
	body, status, err := c.get(videosURL)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, fmt.Errorf("%w: videos page returned 404", ErrUnavailable)
	}
	if status == 429 || status == 503 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrRateLimit, status)
	}
	if status != 200 {
		return nil, NewPHErrorAuto(status, "videos page returned non-200")
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse html page 1: %w", err)
	}

	// 校验页面确实是该博主的视频页，防止 Pornhub 静默重定向到推荐页导致抓到其他博主的视频
	// 策略：检查 <title> 是否包含 URL 最后一段（博主 slug），或页面 URL 无重定向
	if err := validateModelPage(doc, baseURL); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseFailed, err)
	}

	// 提示页数（仅用于日志参考，不作为翻页上限）
	hintMaxPage := extractMaxPage(doc)
	const maxPageHardLimit = 1000 // 防止死循环的兜底上限

	var allVideos []Video

	// 解析第一页
	videos := extractVideos(doc)
	allVideos = append(allVideos, videos...)

	log.Printf("[pornhub·client] GetModelVideos: %s, 分页提示=%d页（实际采用探测翻页）", modelURL, hintMaxPage)

	// 探测翻页：不依赖 extractMaxPage，只要当页有视频就继续翻
	// 避免 Pornhub 分页 UI 只展示临近页码导致总页数被低估
	for page := 2; page <= maxPageHardLimit; page++ {
		pageURL := fmt.Sprintf("%s?page=%d", videosURL, page)
		pageBody, pageStatus, pageErr := c.get(pageURL)
		if pageErr != nil {
			log.Printf("[pornhub·client] 获取第 %d 页失败: %v", page, pageErr)
			break
		}
		if pageStatus == 429 || pageStatus == 503 {
			log.Printf("[pornhub·client] 第 %d 页被限流 (HTTP %d)", page, pageStatus)
			break
		}
		if pageStatus != 200 {
			log.Printf("[pornhub·client] 第 %d 页返回 HTTP %d，停止翻页", page, pageStatus)
			break
		}

		pageDoc, parseErr := html.Parse(strings.NewReader(string(pageBody)))
		if parseErr != nil {
			log.Printf("[pornhub·client] 解析第 %d 页失败: %v", page, parseErr)
			break
		}

		pageVideos := extractVideos(pageDoc)
		if len(pageVideos) == 0 {
			log.Printf("[pornhub·client] 第 %d 页无视频，翻页结束，共 %d 条", page, len(allVideos))
			break
		}
		allVideos = append(allVideos, pageVideos...)
		log.Printf("[pornhub·client] 第 %d 页获取 %d 条，累计 %d 条", page, len(pageVideos), len(allVideos))

		// [FIXED: P1-10] 提取页间延迟为具名常量，便于统一调整反爬策略
		time.Sleep(pageDelay)
	}

	return allVideos, nil
}

// extractMaxPage 从文档中解析 .page_number 获取最大页数
func extractMaxPage(doc *html.Node) int {
	maxPage := 1
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// 查找 <li class="page_number"> 或 <a> 内含数字的分页链接
			// Pornhub 分页通常是: <li class="page_number"><a>N</a></li>
			if hasClass(n, "page_number") {
				if n.FirstChild != nil {
					text := strings.TrimSpace(extractText(n))
					var num int
					if _, err := fmt.Sscanf(text, "%d", &num); err == nil && num > maxPage {
						maxPage = num
					}
				}
			}
			// 也尝试解析 paginator 容器中的最后一个数字链接
			if hasClass(n, "paginator") || hasClass(n, "paginatorWrap") {
				// 遍历所有子节点中的数字
				var inner func(*html.Node)
				inner = func(child *html.Node) {
					if child.Type == html.ElementNode && (child.Data == "a" || child.Data == "li") {
						text := strings.TrimSpace(extractText(child))
						var num int
						if _, err := fmt.Sscanf(text, "%d", &num); err == nil && num > maxPage {
							maxPage = num
						}
					}
					for c := child.FirstChild; c != nil; c = c.NextSibling {
						inner(c)
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					inner(c)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return maxPage
}

// extractVideos 从文档中解析视频列表
// 优先在主内容区（#videoInVideoList / .pcVideoListSection / #modelsVideoSection 等）查找
// 避免抓到侧边栏推荐位里其他博主的视频
func extractVideos(doc *html.Node) []Video {
	// 1. 先找主内容区容器节点
	mainContainer := findMainVideoContainer(doc)
	root := doc
	if mainContainer != nil {
		root = mainContainer
	}

	var videos []Video
	seen := map[string]bool{}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// 匹配 .videoPreviewBg 下的 <a> 或 li.pcVideoListItem 下的链接
			if hasClass(n, "videoPreviewBg") || hasClass(n, "pcVideoListItem") {
				video := extractVideoFromContainer(n)
				if video.ViewKey != "" && !seen[video.ViewKey] {
					seen[video.ViewKey] = true
					videos = append(videos, video)
				}
				return // 不再深入，避免重复
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return videos
}

// findMainVideoContainer 在文档中找到主内容区的视频列表容器节点
// Pornhub 博主页面主内容区 id 通常含 videoInVideoList / modelsVideoSection / channelVideoSection 等
func findMainVideoContainer(doc *html.Node) *html.Node {
	var found *html.Node
	mainIDs := []string{
		"videoInVideoList",
		"modelsVideoSection",
		"channelVideoSection",
		"pcVideoListSection",
		"pornstarsVideoSection",
		"usersVideoSection",
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode {
			id := getAttr(n, "id")
			for _, mainID := range mainIDs {
				if strings.EqualFold(id, mainID) {
					found = n
					return
				}
			}
			// 也匹配 class 包含 pcVideoListSection 的节点
			if hasClass(n, "pcVideoListSection") || hasClass(n, "videoUList") {
				found = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

// extractVideoFromContainer 从视频容器节点中提取 Video 信息
func extractVideoFromContainer(n *html.Node) Video {
	var video Video

	// 递归查找 <a href="/view_video.php?viewkey=...">
	var findLink func(*html.Node)
	findLink = func(child *html.Node) {
		if child.Type == html.ElementNode && child.Data == "a" {
			href := getAttr(child, "href")
			// 只处理 /view_video.php?viewkey= 链接，过滤图片集(/photo/)、GIF(/gif/)等非视频内容
			if (strings.Contains(href, "/view_video.php") || strings.Contains(href, "view_video.php")) && strings.Contains(href, "viewkey=") {
				viewKey := ExtractViewKey(href)
				if viewKey != "" && video.ViewKey == "" {
					video.ViewKey = viewKey
					if strings.HasPrefix(href, "http") {
						video.URL = href
					} else {
						video.URL = phBaseURL + href
					}
					// title 属性
					if t := getAttr(child, "title"); t != "" {
						video.Title = strings.TrimSpace(t)
					}
				}
			}
		}
		// 查找 img 获取缩略图
		if child.Type == html.ElementNode && child.Data == "img" {
			if src := getAttr(child, "src"); src != "" && !strings.HasPrefix(src, "data:image/gif") && video.Thumbnail == "" {
				video.Thumbnail = src
			}
			// 懒加载图片（依次 fallback：data-src → data-thumb_url → data-original → data-image）
			if src := getAttr(child, "data-src"); src != "" && !strings.HasPrefix(src, "data:image/gif") && video.Thumbnail == "" {
				video.Thumbnail = src
			}
			if src := getAttr(child, "data-thumb_url"); src != "" && !strings.HasPrefix(src, "data:image/gif") && video.Thumbnail == "" {
				video.Thumbnail = src
			}
			if src := getAttr(child, "data-original"); src != "" && !strings.HasPrefix(src, "data:image/gif") && video.Thumbnail == "" {
				video.Thumbnail = src
			}
			if src := getAttr(child, "data-image"); src != "" && !strings.HasPrefix(src, "data:image/gif") && video.Thumbnail == "" {
				video.Thumbnail = src
			}
			// 从 img alt 获取标题兜底
			if video.Title == "" {
				if alt := getAttr(child, "alt"); alt != "" {
					video.Title = strings.TrimSpace(alt)
				}
			}
		}
		// 查找 span.title 文本作为标题
		if child.Type == html.ElementNode && hasClass(child, "title") {
			if t := strings.TrimSpace(extractText(child)); t != "" && video.Title == "" {
				video.Title = t
			}
		}
		for c := child.FirstChild; c != nil; c = c.NextSibling {
			findLink(c)
		}
	}
	findLink(n)

	return video
}

// GetVideoURL 获取视频 MP4 直链
// 流程：GET 视频页面 → 提取 #player script → goja eval flashvars → 解析 mediaDefinitions
func (c *Client) GetVideoURL(videoPageURL string) (string, error) {
	body, status, err := c.get(videoPageURL)
	if err != nil {
		return "", err
	}
	if status == 404 || status == 410 {
		return "", NewPHError(ErrKindUnavailable, status, fmt.Sprintf("video page returned %d", status))
	}
	if status == 429 || status == 503 {
		return "", NewPHError(ErrKindRateLimit, status, fmt.Sprintf("rate limited HTTP %d", status))
	}
	// 403：检查页面内容区分"真不可用"与"临时 CDN 拒绝"
	if status == 403 {
		bodyStr := strings.ToLower(string(body))
		unavailableKeywords := []string{
			"has been removed", "no longer available", "this video is private",
			"deleted", "flagged for verification", "disabled",
		}
		for _, kw := range unavailableKeywords {
			if strings.Contains(bodyStr, kw) {
				return "", NewPHError(ErrKindUnavailable, 403, "video unavailable: "+kw)
			}
		}
		// 未命中关键词 → CDN 临时 403，可重试
		return "", NewPHError(ErrKindTransient, 403, "temporary 403, may retry")
	}
	if status != 200 {
		return "", NewPHErrorAuto(status, fmt.Sprintf("video page returned %d", status))
	}

	// extractPlayerScriptFromBody 提取 #player script 并返回 titleText（用于诊断）
	extractAndParse := func(b []byte) (scriptContent string, titleText string, parseErr error) {
		scriptContent, parseErr = extractPlayerScript(string(b))
		if parseErr != nil {
			if doc, err2 := html.Parse(strings.NewReader(string(b))); err2 == nil {
				var findT func(*html.Node)
				findT = func(n *html.Node) {
					if titleText != "" {
						return
					}
					if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
						titleText = n.FirstChild.Data
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						findT(c)
					}
				}
				findT(doc)
			}
		}
		return
	}

	// 提取 #player 区域的 script 内容
	scriptContent, titleText, err := extractAndParse(body)
	if err != nil {
		if titleText != "" {
			log.Printf("[pornhub·client] GetVideoURL pc-mode failed, page title: %q", titleText)
		}
		// fallback：用 platform=tv Cookie 重试一次（竖屏/新格式兼容）
		// [FIXED: PH-2] 不直接修改 c.cookie（data race），改为传入临时 cookie 单次请求
		origCookie := c.getCookie()
		tvCookie := "platform=tv"
		if origCookie != "" {
			tvCookie = origCookie + "; platform=tv"
		}
		tvBody, tvStatus, tvErr := c.getWithCookie(videoPageURL, tvCookie)

		if tvErr == nil && tvStatus == 200 {
			scriptContent, titleText, err = extractAndParse(tvBody)
			if err == nil {
				log.Printf("[pornhub·client] GetVideoURL tv-mode fallback succeeded")
			} else {
				log.Printf("[pornhub·client] GetVideoURL tv-mode also failed (title=%q): %v", titleText, err)
			}
		}

		if err != nil {
			return "", NewPHError(ErrKindParseFailed, status, fmt.Sprintf("page parse failed (title=%q): %v", titleText, err))
		}
	}

	// 提取 flashvars 变量名：按多种格式依次尝试
	flashvarsVar := ""
	for _, re := range flashvarsVarPatterns {
		if m := re.FindStringSubmatch(scriptContent); len(m) >= 2 {
			flashvarsVar = m[1]
			break
		}
	}

	// 构造并执行 JS
	// 注入最小 DOM/BOM stub，防止页面 JS 中 document/window/XMLHttpRequest 等引用导致 ReferenceError
	domStub := `
var window = this;
var document = { getElementById: function(){ return null; }, querySelector: function(){ return null; }, querySelectorAll: function(){ return []; }, createElement: function(){ return {}; }, cookie: "" };
var location = { href: "", hostname: "www.pornhub.com" };
var navigator = { userAgent: "", language: "en-US" };
var XMLHttpRequest = function(){};
var console = { log: function(){}, warn: function(){}, error: function(){} };
var setTimeout = function(){};
var clearTimeout = function(){};
var setInterval = function(){};
var clearInterval = function(){};
`

	var fv FlashVars

	if flashvarsVar != "" {
		// 有变量名 → goja eval 后序列化
		js := domStub + "var playerObjList = {};\n" + scriptContent + "\nvar _json = JSON.stringify(" + flashvarsVar + ");"
		vm := goja.New()
		// P0-6: goja VM 非线程安全，所有 VM 操作（RunString、vm.Get、Interrupt）
		// 必须在同一 goroutine 中执行。通过 channel + select timeout 等待结果。
		type jsResult struct {
			jsonStr string
			err     error
		}
		jsCh := make(chan jsResult, 1)
		go func() {
			if _, evalErr := vm.RunString(js); evalErr != nil {
				jsCh <- jsResult{err: evalErr}
				return
			}
			jsonVal := vm.Get("_json")
			if jsonVal == nil || jsonVal.String() == "undefined" {
				jsCh <- jsResult{err: fmt.Errorf("_json is undefined after eval")}
				return
			}
			jsCh <- jsResult{jsonStr: jsonVal.String()}
		}()
		select {
		case res := <-jsCh:
			if res.err != nil {
				return "", fmt.Errorf("%w: goja eval failed: %v", ErrParseFailed, res.err)
			}
			if err := json.Unmarshal([]byte(res.jsonStr), &fv); err != nil {
				return "", fmt.Errorf("%w: unmarshal flashvars: %v", ErrParseFailed, err)
			}
		case <-time.After(10 * time.Second):
			vm.Interrupt("js eval timeout")
			return "", fmt.Errorf("%w: js eval timeout", ErrParseFailed)
		}
	} else {
		// 兜底：直接从 script 文本中提取 mediaDefinitions JSON 数组
		// 用括号计数而非正则，正确处理嵌套结构
		mdArr, mdErr := extractJSONArrayByKey(scriptContent, "mediaDefinitions")
		if mdErr == nil {
			if err := json.Unmarshal([]byte(mdArr), &fv.MediaDefinitions); err != nil {
				return "", fmt.Errorf("%w: unmarshal inline mediaDefinitions: %v", ErrParseFailed, err)
			}
			log.Printf("[pornhub·client] using inline mediaDefinitions fallback (no flashvars var found)")
		} else {
			return "", fmt.Errorf("%w: no playable variable found in script (tried %d patterns, no inline mediaDefinitions: %v)", ErrParseFailed, len(flashvarsVarPatterns), mdErr)
		}
	}

	// Pornhub mediaDefinitions 实际结构（2025+）：
	// - 多条 format=hls：带时效签名的 .m3u8 直链（240p/480p/720p/1080p）
	// - 一条 format=mp4：video/get_media 间接 API，带登录 Cookie 时返回真实 MP4 直链
	//
	// 优先级策略：
	// 1. 有 Cookie → 先走 video/get_media 拿真实 MP4（画质最全，可能有 2K/4K）
	// 2. 无 Cookie 或 video/get_media 失败 → fallback 到 HLS 最高画质 m3u8
	// 3. HLS 也没有 → 报错

	// Step 1: 有 Cookie 时尝试 video/get_media
	if c.cookie != "" {
		for _, entry := range fv.MediaDefinitions {
			if entry.Format == "mp4" && entry.VideoURL != "" {
				qualityBody, qStatus, qErr := c.getJSON(entry.VideoURL)
				if qErr != nil || qStatus != 200 {
					log.Printf("[pornhub·client] video/get_media failed (status=%d): %v，fallback to HLS", qStatus, qErr)
					break
				}
				var qualities []VideoQuality
				if err := json.Unmarshal(qualityBody, &qualities); err != nil {
					log.Printf("[pornhub·client] unmarshal video/get_media failed: %v，fallback to HLS", err)
					break
				}
				if len(qualities) > 0 {
					if u := qualities[len(qualities)-1].VideoURL; u != "" {
						log.Printf("[pornhub·client] using MP4 via video/get_media (quality count=%d)", len(qualities))
						return u, nil
					}
				}
				break
			}
		}
	}

	// Step 2: fallback 到 HLS 最高画质
	if u := c.extractBestHLSURL(fv.MediaDefinitions); u != "" {
		log.Printf("[pornhub·client] using HLS fallback")
		return u, nil
	}

	return "", fmt.Errorf("%w: could not find playable url in mediaDefinitions (count=%d)", ErrNoVideoURL, len(fv.MediaDefinitions))
}

// extractBestHLSURL 从 mediaDefinitions 中取最高画质的 HLS m3u8 URL
// quality 字段是数字字符串（"240","480","720","1080"），取数值最大的
func (c *Client) extractBestHLSURL(defs []MediaDefinition) string {
	bestQ := -1
	bestURL := ""
	for _, d := range defs {
		if d.Format != "hls" || d.VideoURL == "" {
			continue
		}
		// quality 是 "720" 这类数字字符串（RawMessage，去掉引号解析）
		qStr := strings.Trim(string(d.Quality), `"`)
		q := 0
		fmt.Sscanf(qStr, "%d", &q)
		if q > bestQ {
			bestQ = q
			bestURL = d.VideoURL
		}
	}
	return bestURL
}



// extractPlayerScript 从 HTML 中提取播放器 script 内容，兼容多种 PH 页面结构。
// 策略（按优先级）：
//  1. #player 容器内，依次按 scriptKeywords 查找
//  2. 全文依次按 scriptKeywords 查找
//
// 只要 script 包含 mediaDefinitions（或其他已知播放器变量），即视为命中。
func extractPlayerScript(htmlContent string) (string, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}

	// 优先在 #player 容器内按关键词顺序查找
	if playerNode := findNodeByID(doc, "player"); playerNode != nil {
		for _, kw := range scriptKeywords {
			if script := findScriptInNode(playerNode, kw); script != "" {
				return script, nil
			}
		}
	}

	// fallback：全文按关键词顺序查找
	for _, kw := range scriptKeywords {
		if script, err2 := extractScriptByContent(doc, kw); err2 == nil && script != "" {
			return script, nil
		}
	}

	return "", fmt.Errorf("no player script found (tried keywords: %v)", scriptKeywords)
}

// findNodeByID 在文档中查找 id=targetID 的节点
func findNodeByID(doc *html.Node, targetID string) *html.Node {
	var result *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if result != nil {
			return
		}
		if n.Type == html.ElementNode {
			if getAttr(n, "id") == targetID {
				result = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return result
}

// findScriptInNode 在指定节点内查找包含 keyword 的 script 内容
func findScriptInNode(n *html.Node, keyword string) string {
	var result string
	var walk func(*html.Node)
	walk = func(child *html.Node) {
		if result != "" {
			return
		}
		if child.Type == html.ElementNode && child.Data == "script" {
			content := extractText(child)
			if strings.Contains(content, keyword) {
				result = content
				return
			}
		}
		for c := child.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return result
}

// extractScriptByContent 在整个文档中查找包含 keyword 的 script
func extractScriptByContent(doc *html.Node, keyword string) (string, error) {
	script := findScriptInNode(doc, keyword)
	if script == "" {
		return "", fmt.Errorf("no script containing %q found", keyword)
	}
	return script, nil
}

// ─── HTML 工具函数 ──────────────────────────────────────────────────────────

// hasClass 判断节点是否含有指定 CSS class
func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// getAttr 获取节点属性值
func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// extractText 递归提取节点的纯文本内容
func extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(extractText(c))
	}
	return sb.String()
}

// extractJSONArrayByKey 从任意文本中找到 `"key": [...]` 并返回完整 JSON 数组字符串。
// 使用括号计数而非正则，正确处理任意深度的嵌套结构。
func extractJSONArrayByKey(text, key string) (string, error) {
	marker := `"` + key + `"`
	idx := strings.Index(text, marker)
	if idx == -1 {
		return "", fmt.Errorf("key %q not found", key)
	}
	// 跳过 key、冒号、空白，找到 '[' 的位置
	rest := text[idx+len(marker):]
	bracketIdx := strings.IndexByte(rest, '[')
	if bracketIdx == -1 {
		return "", fmt.Errorf("no '[' after key %q", key)
	}
	rest = rest[bracketIdx:]

	// 括号计数，找到匹配的 ']'
	depth := 0
	inStr := false
	escape := false
	for i, ch := range rest {
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inStr {
			escape = true
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch ch {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return rest[:i+1], nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced brackets for key %q", key)
}
