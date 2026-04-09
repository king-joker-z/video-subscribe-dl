package xchina

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	xcBaseURL   = "https://en.xchina.co"
	xcUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	xcReferer   = "https://en.xchina.co/"

	// xcMaxBodySize HTML 页面最大读取大小（10 MB）
	xcMaxBodySize = 10 * 1024 * 1024

	// pageDelay 翻页间隔，避免被限流
	pageDelay = 3 * time.Second

	// maxPageHardLimit 翻页绝对上限，防死循环
	maxPageHardLimit = 500
)

var (
	// videoIDRe 从视频页 URL 中提取 hex ID
	videoIDRe = regexp.MustCompile(`/video/id-([a-fA-F0-9]+)\.html`)

	// modelIDRe 从 model URL 中提取 model ID
	modelIDRe = regexp.MustCompile(`/model/id-([a-zA-Z0-9_-]+)`)
)

// ClientOptions 可选配置
type ClientOptions struct {
	PageDelay        time.Duration
	MaxPageHardLimit int
}

// Client xchina HTTP 客户端
type Client struct {
	http      *http.Client
	pageDelay time.Duration
	maxPage   int
}

// NewClient 创建 xchina 客户端
func NewClient(opts ...ClientOptions) *Client {
	delay := pageDelay
	maxPage := maxPageHardLimit
	if len(opts) > 0 {
		if opts[0].PageDelay > 0 {
			delay = opts[0].PageDelay
		}
		if opts[0].MaxPageHardLimit > 0 {
			maxPage = opts[0].MaxPageHardLimit
		}
	}
	return &Client{
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		pageDelay: delay,
		maxPage:   maxPage,
	}
}

// Close 关闭客户端（保留接口对齐）
func (c *Client) Close() {}

// get 带通用 Header 的 GET 请求
func (c *Client) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", xcUserAgent)
	req.Header.Set("Referer", xcReferer)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	return c.http.Do(req)
}

// fetchHTML 获取页面 HTML 文本
func (c *Client) fetchHTML(ctx context.Context, url string) (string, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 410 {
		return "", fmt.Errorf("HTTP %d: token expired or forbidden (%s)", resp.StatusCode, url)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, xcMaxBodySize))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ─── Model page scraping ────────────────────────────────────────────────────

// GetModelInfo 获取模特基本信息（名称）
func (c *Client) GetModelInfo(modelURL string) (ModelInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 规范化 URL
	modelURL = normalizeModelURL(modelURL)

	body, err := c.fetchHTML(ctx, modelURL)
	if err != nil {
		return ModelInfo{}, err
	}

	name := extractModelName(body)
	modelID := ""
	if m := modelIDRe.FindStringSubmatch(modelURL); len(m) > 1 {
		modelID = m[1]
	}

	return ModelInfo{
		ModelID:  modelID,
		Name:     name,
		ModelURL: modelURL,
	}, nil
}

// GetModelVideos 获取模特所有视频列表（含分页）
// knownIDs 如果非 nil，则遇到整页都是已知 ID 时提前停止（增量模式）
func (c *Client) GetModelVideos(ctx context.Context, modelURL string, knownIDs map[string]bool) ([]Video, error) {
	modelURL = normalizeModelURL(modelURL)

	var allVideos []Video
	currentURL := modelURL
	seen := make(map[string]bool)

	for page := 1; page <= c.maxPage; page++ {
		select {
		case <-ctx.Done():
			return allVideos, ctx.Err()
		default:
		}

		log.Printf("[xchina] scraping page %d: %s", page, currentURL)
		body, err := c.fetchHTML(ctx, currentURL)
		if err != nil {
			if page == 1 {
				return nil, fmt.Errorf("fetch page 1 failed: %w", err)
			}
			log.Printf("[xchina] fetch page %d failed: %v, stopping", page, err)
			break
		}

		videos := extractVideosFromHTML(body, currentURL)
		if len(videos) == 0 {
			log.Printf("[xchina] no videos on page %d, assuming last page", page)
			break
		}

		// 增量停止：整页都是已知 ID
		newOnPage := 0
		for _, v := range videos {
			if seen[v.VideoID] {
				continue
			}
			seen[v.VideoID] = true
			allVideos = append(allVideos, v)
			if knownIDs != nil && !knownIDs[v.VideoID] {
				newOnPage++
			}
		}
		if knownIDs != nil && newOnPage == 0 {
			log.Printf("[xchina] page %d is all known IDs, stopping pagination", page)
			break
		}

		// 找下一页
		nextURL := extractNextPageURL(body, currentURL)
		if nextURL == "" || nextURL == currentURL {
			break
		}
		currentURL = nextURL

		select {
		case <-ctx.Done():
			return allVideos, ctx.Err()
		case <-time.After(c.pageDelay):
		}
	}

	return allVideos, nil
}

// ─── HTML parsing helpers ────────────────────────────────────────────────────

// extractModelName 从 HTML 中提取模特名称
func extractModelName(body string) string {
	// 尝试多个选择器（按优先级）
	selectors := []struct {
		tag   string
		class string
	}{
		{"div", "model-name"},
		{"h1", ""},
		{"title", ""},
	}

	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		// fallback: regex
		return extractModelNameRegex(body)
	}

	for _, sel := range selectors {
		if name := findTextByTagClass(doc, sel.tag, sel.class); name != "" {
			// 清理掉 " - xchina" 等后缀
			name = cleanTitle(name)
			if name != "" {
				return name
			}
		}
	}
	return extractModelNameRegex(body)
}

func extractModelNameRegex(body string) string {
	// <title>xxx - xchina</title>
	re := regexp.MustCompile(`<title>([^<]+)</title>`)
	m := re.FindStringSubmatch(body)
	if len(m) > 1 {
		name := strings.Split(m[1], " - ")[0]
		name = strings.TrimSpace(name)
		if name != "" {
			return name
		}
	}
	return ""
}

// extractVideosFromHTML 从页面 HTML 提取视频列表
// 策略：找到每个视频链接后，精确提取该 <a> 标签自身的 title 属性作为标题，
// 以及链接后方 400 字节内的第一个 img src 作为封面，避免匹配到无关元素。
func extractVideosFromHTML(body, pageURL string) []Video {
	var videos []Video
	seen := make(map[string]bool)

	// 匹配完整的 <a href="/video/id-xxx.html" ... title="视频标题" ...> 标签
	// 同时捕获 title 属性（可能在 href 前或后）
	linkRe := regexp.MustCompile(`(?i)<a\b([^>]*\bhref=["']/video/id-([a-fA-F0-9]+)\.html["'][^>]*)>`)
	// 从 <a> 标签属性串中提取 title 属性
	aTitleRe := regexp.MustCompile(`(?i)\btitle=["']([^"']{2,200})["']`)
	// 从 <a> 标签属性串中提取 href 路径
	aHrefRe := regexp.MustCompile(`(?i)\bhref=["'](/video/id-[a-fA-F0-9]+\.html)["']`)
	// 封面：链接后方的 img src（含 data-src 懒加载）
	thumbRe := regexp.MustCompile(`(?i)<img\b[^>]+\bsrc=["']([^"']+)["']`)
	thumbLazySrcRe := regexp.MustCompile(`(?i)<img\b[^>]+\bdata-src=["']([^"']+)["']`)
	// img alt 属性作为标题备选
	altRe := regexp.MustCompile(`(?i)\balt=["']([^"']{2,200})["']`)
	// 附近标签内文字（仅取链接后方，避免误取前面内容）
	innerTextRe := regexp.MustCompile(`(?i)<(?:p|span|div|h\d)[^>]*>\s*([^<]{2,120})\s*</(?:p|span|div|h\d)>`)

	allIdx := linkRe.FindAllStringSubmatchIndex(body, -1)
	for _, idx := range allIdx {
		if len(idx) < 6 {
			continue
		}
		attrStr := body[idx[2]:idx[3]] // <a> 的全部属性内容
		videoID := body[idx[4]:idx[5]]
		if seen[videoID] {
			continue
		}
		seen[videoID] = true

		linkPath := ""
		if m := aHrefRe.FindStringSubmatch(attrStr); len(m) > 1 {
			linkPath = m[1]
		}
		if linkPath == "" {
			continue
		}

		v := Video{
			VideoID: videoID,
			PageURL: xcBaseURL + linkPath,
		}

		// 优先：<a> 标签自身的 title 属性（最准确）
		if m := aTitleRe.FindStringSubmatch(attrStr); len(m) > 1 {
			v.Title = strings.TrimSpace(m[1])
		}

		// 链接后方 600 字节窗口，用于提取封面和备用标题
		afterStart := idx[1]
		afterEnd := afterStart + 600
		if afterEnd > len(body) {
			afterEnd = len(body)
		}
		afterWindow := body[afterStart:afterEnd]

		// 封面：优先 data-src（懒加载），其次 src；取链接后方第一个 img
		if v.Thumbnail == "" {
			if m := thumbLazySrcRe.FindStringSubmatch(afterWindow); len(m) > 1 {
				src := m[1]
				if !strings.HasPrefix(src, "data:") && len(src) > 10 {
					v.Thumbnail = src
				}
			}
		}
		if v.Thumbnail == "" {
			if m := thumbRe.FindStringSubmatch(afterWindow); len(m) > 1 {
				src := m[1]
				if !strings.HasPrefix(src, "data:") && len(src) > 10 {
					v.Thumbnail = src
				}
			}
		}

		// 标题备选：img alt（链接后方第一个 img 标签内）
		if v.Title == "" {
			imgTagRe := regexp.MustCompile(`(?i)<img\b[^>]+>`)
			if imgTag := imgTagRe.FindString(afterWindow); imgTag != "" {
				if ma := altRe.FindStringSubmatch(imgTag); len(ma) > 1 {
					v.Title = strings.TrimSpace(ma[1])
				}
			}
		}
		// 再次备选：链接后方标签内文字
		if v.Title == "" {
			for _, m := range innerTextRe.FindAllStringSubmatch(afterWindow, -1) {
				if len(m) > 1 {
					t := strings.TrimSpace(m[1])
					if len([]rune(t)) >= 3 {
						v.Title = t
						break
					}
				}
			}
		}
		// 兜底：留空，下载时从视频详情页补充

		videos = append(videos, v)
	}
	return videos
}

// extractNextPageURL 从 HTML 提取下一页 URL
func extractNextPageURL(body, currentURL string) string {
	// 尝试多种分页模式
	patterns := []string{
		`<a[^>]+rel=["']next["'][^>]+href=["']([^"']+)["']`,
		`<a[^>]+href=["']([^"']+)["'][^>]+rel=["']next["']`,
		`<a[^>]+class="[^"]*next[^"]*"[^>]+href=["']([^"']+)["']`,
		`<li[^>]+class="[^"]*next[^"]*"[^>]*>.*?<a[^>]+href=["']([^"']+)["']`,
	}
	for _, p := range patterns {
		re := regexp.MustCompile(`(?s)` + p)
		if m := re.FindStringSubmatch(body); len(m) > 1 {
			href := m[1]
			if strings.HasPrefix(href, "http") {
				return href
			}
			return xcBaseURL + href
		}
	}

	// fallback: 页码递增
	pageRe := regexp.MustCompile(`[?&](page=(\d+))`)
	if m := pageRe.FindStringSubmatch(currentURL); len(m) > 2 {
		n := 0
		fmt.Sscanf(m[2], "%d", &n)
		next := strings.Replace(currentURL, m[1], fmt.Sprintf("page=%d", n+1), 1)
		return next
	}
	// 第一页没有 page 参数时，加上 ?page=2
	if !strings.Contains(currentURL, "page=") {
		sep := "?"
		if strings.Contains(currentURL, "?") {
			sep = "&"
		}
		return currentURL + sep + "page=2"
	}
	return ""
}

// ─── HTML tree helpers ───────────────────────────────────────────────────────

func findTextByTagClass(n *html.Node, tag, class string) string {
	if n.Type == html.ElementNode && n.Data == tag {
		if class == "" || hasClass(n, class) {
			text := extractText(n)
			if text != "" {
				return text
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findTextByTagClass(c, tag, class); result != "" {
			return result
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" && strings.Contains(a.Val, class) {
			return true
		}
	}
	return false
}

func extractText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

func cleanTitle(s string) string {
	// 去掉 " - xchina" 等后缀
	for _, suffix := range []string{" - xchina", " - XChina", " | xchina"} {
		s = strings.TrimSuffix(s, suffix)
	}
	return strings.TrimSpace(s)
}

// ExtractM3U8URL 从视频详情页 HTML 中提取 HLS m3u8 URL
// xchina 视频页通常在 <source src="..."> 或 JS 变量 file: "..." 中内嵌流地址
func ExtractM3U8URL(body, pageURL string) (string, error) {
	// 优先匹配 <source src="...m3u8...">
	patterns := []string{
		`<source[^>]+src=["']([^"']+\.m3u8[^"']*)["']`,
		`src:\s*["']([^"']+\.m3u8[^"']*)["']`,
		`file:\s*["']([^"']+\.m3u8[^"']*)["']`,
		`"file":\s*"([^"]+\.m3u8[^"]*)"`,
		`hls[Ss]ource[^=]*=\s*["']([^"']+\.m3u8[^"']*)["']`,
		`["']?(https?://[^"'\s]+\.m3u8[^"'\s]*)["']?`,
	}

	for _, p := range patterns {
		re := regexp.MustCompile(`(?i)` + p)
		if m := re.FindStringSubmatch(body); len(m) > 1 {
			url := strings.TrimSpace(m[1])
			if url != "" {
				return url, nil
			}
		}
	}

	// 尝试 MP4 直链（部分视频不用 HLS）
	mp4Patterns := []string{
		`<source[^>]+src=["']([^"']+\.mp4[^"']*)["']`,
		`file:\s*["']([^"']+\.mp4[^"']*)["']`,
		`"file":\s*"([^"]+\.mp4[^"]*)"`,
	}
	for _, p := range mp4Patterns {
		re := regexp.MustCompile(`(?i)` + p)
		if m := re.FindStringSubmatch(body); len(m) > 1 {
			url := strings.TrimSpace(m[1])
			if url != "" {
				return url, fmt.Errorf("parse failed: found mp4 direct link instead of m3u8: %s", url)
			}
		}
	}

	return "", fmt.Errorf("parse failed: no m3u8 URL found in page %s", pageURL)
}

// ─── URL helpers ─────────────────────────────────────────────────────────────

// IsXChinaURL 判断是否为 xchina URL
func IsXChinaURL(rawURL string) bool {
	return strings.Contains(rawURL, "xchina.co")
}

// IsModelURL 判断是否为 model 主页 URL
func IsModelURL(rawURL string) bool {
	return strings.Contains(rawURL, "xchina.co") && strings.Contains(rawURL, "/model/id-")
}

// ExtractModelID 从 URL 提取 model ID
func ExtractModelID(rawURL string) (string, error) {
	m := modelIDRe.FindStringSubmatch(rawURL)
	if len(m) < 2 {
		return "", fmt.Errorf("cannot extract model ID from %q", rawURL)
	}
	return m[1], nil
}

// ExtractVideoID 从 URL 提取 video ID
func ExtractVideoID(rawURL string) (string, error) {
	m := videoIDRe.FindStringSubmatch(rawURL)
	if len(m) < 2 {
		return "", fmt.Errorf("cannot extract video ID from %q", rawURL)
	}
	return m[1], nil
}

// normalizeModelURL 规范化 model URL，确保是完整 URL
func normalizeModelURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "http") {
		return rawURL
	}
	if strings.HasPrefix(rawURL, "/model/") {
		return xcBaseURL + rawURL
	}
	// 裸 model ID
	return xcBaseURL + "/model/id-" + rawURL
}
