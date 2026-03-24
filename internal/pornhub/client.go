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
	"time"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// phMaxHTMLBodySize HTML 页面最大读取大小（10 MB），防止无限流撑爆内存
const phMaxHTMLBodySize = 10 * 1024 * 1024

const (
	phBaseURL   = "https://www.pornhub.com"
	phUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

var (
	flashvarsVarRe = regexp.MustCompile(`(flashvars_\d+)`)
	viewKeyRe      = regexp.MustCompile(`[?&]viewkey=([a-zA-Z0-9_]+)`)
)

// Client Pornhub HTTP 客户端
type Client struct {
	httpClient *http.Client
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
	c.cookie = strings.TrimSpace(cookie)
}

// Close 释放资源
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
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
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
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
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
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
	if idx := strings.IndexByte(modelURL, '?'); idx != -1 {
		modelURL = modelURL[:idx]
	}
	cleanURL := strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(modelURL, "/"), "/videos"), "/")

	body, status, err := c.get(cleanURL)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, fmt.Errorf("%w: model page returned 404", ErrUnavailable)
	}
	if status != 200 {
		return nil, NewPHError(status, "model page returned non-200")
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
		return nil, NewPHError(status, "videos page returned non-200")
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse html page 1: %w", err)
	}

	maxPage := extractMaxPage(doc)
	if maxPage < 1 {
		maxPage = 1
	}
	const maxPageLimit = 50 // 防止恶意/异常页面返回超大翻页数
	if maxPage > maxPageLimit {
		log.Printf("[pornhub·client] maxPage %d > limit %d, capping", maxPage, maxPageLimit)
		maxPage = maxPageLimit
	}

	var allVideos []Video

	// 解析第一页
	videos := extractVideos(doc)
	allVideos = append(allVideos, videos...)

	log.Printf("[pornhub·client] GetModelVideos: %s, 共 %d 页", modelURL, maxPage)

	// 翻页
	for page := 2; page <= maxPage; page++ {
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
			log.Printf("[pornhub·client] 第 %d 页返回 HTTP %d", page, pageStatus)
			break
		}

		pageDoc, parseErr := html.Parse(strings.NewReader(string(pageBody)))
		if parseErr != nil {
			log.Printf("[pornhub·client] 解析第 %d 页失败: %v", page, parseErr)
			break
		}

		pageVideos := extractVideos(pageDoc)
		if len(pageVideos) == 0 {
			// 空页，停止翻页
			break
		}
		allVideos = append(allVideos, pageVideos...)

		// 页间延迟，避免被限流
		time.Sleep(2 * time.Second)
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
// 选择器：.videoUList .videoPreviewBg > a 或 li.pcVideoListItem
func extractVideos(doc *html.Node) []Video {
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
	walk(doc)
	return videos
}

// extractVideoFromContainer 从视频容器节点中提取 Video 信息
func extractVideoFromContainer(n *html.Node) Video {
	var video Video

	// 递归查找 <a href="/view_video.php?viewkey=...">
	var findLink func(*html.Node)
	findLink = func(child *html.Node) {
		if child.Type == html.ElementNode && child.Data == "a" {
			href := getAttr(child, "href")
			if strings.Contains(href, "viewkey=") {
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
			if src := getAttr(child, "src"); src != "" && !strings.HasSuffix(src, ".gif") && video.Thumbnail == "" {
				video.Thumbnail = src
			}
			// 懒加载图片
			if src := getAttr(child, "data-src"); src != "" && !strings.HasSuffix(src, ".gif") && video.Thumbnail == "" {
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
		return "", fmt.Errorf("%w: video page returned %d", ErrUnavailable, status)
	}
	if status == 429 || status == 503 {
		return "", fmt.Errorf("%w: HTTP %d", ErrRateLimit, status)
	}
	if status != 200 {
		return "", NewPHError(status, fmt.Sprintf("video page returned %d", status))
	}

	// 提取 #player 区域的 script 内容
	scriptContent, err := extractPlayerScript(string(body))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrParseFailed, err)
	}

	// 提取 flashvars 变量名
	flashvarsVar := flashvarsVarRe.FindString(scriptContent)
	if flashvarsVar == "" {
		return "", fmt.Errorf("%w: flashvars variable not found in script", ErrParseFailed)
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
	js := domStub + "var playerObjList = {};\n" + scriptContent + "\nvar _json = JSON.stringify(" + flashvarsVar + ");"
	vm := goja.New()
	// 设置 10 秒超时，防止页面 JS 含死循环导致 goroutine 永久阻塞
	timer := time.AfterFunc(10*time.Second, func() {
		vm.Interrupt("js eval timeout")
	})
	defer timer.Stop()
	if _, err := vm.RunString(js); err != nil {
		return "", fmt.Errorf("%w: goja eval failed: %v", ErrParseFailed, err)
	}

	jsonVal := vm.Get("_json")
	if jsonVal == nil || jsonVal.String() == "undefined" {
		return "", fmt.Errorf("%w: _json is undefined after eval", ErrParseFailed)
	}
	jsonStr := jsonVal.String()

	// 解析 FlashVars JSON
	var fv FlashVars
	if err := json.Unmarshal([]byte(jsonStr), &fv); err != nil {
		return "", fmt.Errorf("%w: unmarshal flashvars: %v", ErrParseFailed, err)
	}

	// Pornhub mediaDefinitions 实际结构（2025+）：
	// - 多条 format=hls，videoUrl 是带时效签名的 .m3u8 直链（240p/480p/720p/1080p）
	// - 一条 format=mp4，videoUrl 是 video/get_media 间接 API（需 Cookie，quality=[]）
	// 策略：优先取 HLS 最高画质 m3u8；fallback 再尝试 mp4 间接 API
	if u := c.extractBestHLSURL(fv.MediaDefinitions); u != "" {
		return u, nil
	}

	// fallback：format=mp4 间接 API（video/get_media，需要已设置 Cookie）
	for _, entry := range fv.MediaDefinitions {
		if entry.Format == "mp4" && entry.VideoURL != "" {
			qualityBody, qStatus, qErr := c.getJSON(entry.VideoURL)
			if qErr != nil || qStatus != 200 {
				log.Printf("[pornhub·client] mp4 indirect API failed (status=%d): %v", qStatus, qErr)
				continue
			}
			var qualities []VideoQuality
			if err := json.Unmarshal(qualityBody, &qualities); err != nil {
				log.Printf("[pornhub·client] unmarshal mp4 quality list failed: %v", err)
				continue
			}
			if len(qualities) > 0 {
				if u := qualities[len(qualities)-1].VideoURL; u != "" {
					return u, nil
				}
			}
		}
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



// extractPlayerScript 从 HTML 中提取 #player 下的 script 内容
func extractPlayerScript(htmlContent string) (string, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}

	// 找 id="player" 的容器节点
	playerNode := findNodeByID(doc, "player")
	if playerNode == nil {
		// fallback：直接在全文中找包含 flashvars_ 的 script
		return extractScriptByContent(doc, "flashvars_")
	}

	// 在 player 容器内找第一个包含 flashvars_ 的 script
	script := findScriptInNode(playerNode, "flashvars_")
	if script != "" {
		return script, nil
	}

	// fallback：全文找
	return extractScriptByContent(doc, "flashvars_")
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
