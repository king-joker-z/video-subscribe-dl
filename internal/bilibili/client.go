package bilibili

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FlexInt64 兼容 B站 API 返回的 int64 或 string 类型数字字段
type FlexInt64 int64

func (f *FlexInt64) UnmarshalJSON(data []byte) error {
	// 先尝试直接解析为数字
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*f = FlexInt64(n)
		return nil
	}
	// 再尝试解析为字符串
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "" {
			*f = 0
			return nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("FlexInt64: cannot parse %q as int64: %w", s, err)
		}
		*f = FlexInt64(n)
		return nil
	}
	return fmt.Errorf("FlexInt64: cannot unmarshal %s", string(data))
}

// ErrRateLimited 表示触发了B站风控（-352/-401/-412）
var ErrRateLimited = errors.New("bilibili: rate limited by risk control")

type Client struct {
	http       *http.Client
	cookie     string
	credential *Credential
	limiter    *RateLimiter // API 请求令牌桶限流（下载流不走限流）
	ua         string       // 固定 User-Agent（创建时随机选定，后续复用）
}

func NewClient(cookie string) *Client {
	return &Client{
		http:    &http.Client{Timeout: 30 * time.Second},
		cookie:  cookie,
		limiter: DefaultRateLimiter(),
		ua:      randUA(),
	}
}

// NewClientWithCredential 使用 Credential 构造 Client
func NewClientWithCredential(cred *Credential) *Client {
	if cred == nil || cred.IsEmpty() {
		return NewClient("")
	}
	return &Client{
		http:       &http.Client{Timeout: 30 * time.Second},
		credential: cred,
		cookie:     cred.ToCookieString(),
		limiter:    DefaultRateLimiter(),
		ua:         randUA(),
	}
}

// GetCredential 返回当前 Client 使用的 Credential（可能为 nil）
func (c *Client) GetCredential() *Credential {
	return c.credential
}

// UpdateCredential 更新 Client 的凭证（线程安全由调用方保证）
func (c *Client) UpdateCredential(cred *Credential) {
	if cred != nil && !cred.IsEmpty() {
		c.credential = cred
		c.cookie = cred.ToCookieString()
	}
}

// GetCookieString 返回当前使用的 cookie 字符串
func (c *Client) GetCookieString() string {
	return c.cookie
}

// GetHTTPClient 返回内部 http.Client（供 credential 刷新等使用）
func (c *Client) GetHTTPClient() *http.Client {
	return c.http
}

// === 数据结构 ===

type UPInfo struct {
	MID   int64  `json:"mid"`
	Name  string `json:"name"`
	Face  string `json:"face"`
	Sign  string `json:"sign"`
	Level int    `json:"level"`
	Sex   string `json:"sex"`
}

type VideoItem struct {
	BvID        string `json:"bvid"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Pic         string `json:"pic"`
	Length      string `json:"length"` // "MM:SS"
	Created     int64  `json:"created"`
	SeasonID    int64  `json:"season_id"`
	IsSeason    bool   `json:"is_season_display"`
}

// VideoRights 视频权限标记
type VideoRights struct {
	IsChargePlus int `json:"is_charge_plus"` // 1=充电专属
	ArcPay       int `json:"arc_pay"`        // 1=付费
	UGCPay       int `json:"ugc_pay"`        // 1=UGC付费
}

type VideoDetail struct {
	BvID      string      `json:"bvid"`
	Title     string      `json:"title"`
	Desc      string      `json:"desc"`
	Pic       string      `json:"pic"`
	Duration  int         `json:"duration"`
	PubDate   int64       `json:"pubdate"`
	Owner     UPInfo      `json:"owner"`
	Stat      VideoStat   `json:"stat"`
	Rights    VideoRights `json:"rights"`
	SeasonID  int64       `json:"season_id"`
	UGCSeason *struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	} `json:"ugc_season"`
	RedirectURL string `json:"redirect_url"`
	Tid         int    `json:"tid"`   // 分区 ID
	TName       string `json:"tname"` // 分区名称
	State       int    `json:"state"` // -1=待审 -2=退回 -4=未过审 -6=删除
	Pages       []struct {
		CID  int64  `json:"cid"`
		Page int    `json:"page"`
		Part string `json:"part"`
	} `json:"pages"`
}

// IsBangumi 检测番剧/影视重定向
func (v *VideoDetail) IsBangumi() bool {
	return v.RedirectURL != "" && (strings.Contains(v.RedirectURL, "bangumi") || strings.Contains(v.RedirectURL, "ep"))
}

// IsUnavailable 检测视频不可用状态（已删除/审核中/隐藏等）
func (v *VideoDetail) IsUnavailable() bool {
	return v.State != 0
}

// IsChargePlus 检测充电专属/付费视频
func (v *VideoDetail) IsChargePlus() bool {
	return v.Rights.IsChargePlus == 1 || v.Rights.ArcPay == 1 || v.Rights.UGCPay == 1
}

type VideoStat struct {
	View     int64 `json:"view"`
	Danmaku  int64 `json:"danmaku"`
	Like     int64 `json:"like"`
	Coin     int64 `json:"coin"`
	Reply    int64 `json:"reply"`
	Favorite int64 `json:"favorite"`
	Share    int64 `json:"share"`
}

type SeasonMeta struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Cover string `json:"cover"`
	Intro string `json:"intro"`
	Total int    `json:"total"`
}

type SeasonArchive struct {
	BvID     string `json:"bvid"`
	Title    string `json:"title"`
	Pic      string `json:"pic"`
	Duration int    `json:"duration"`
	PubDate  int64  `json:"pubdate"`
}

// CollectionType 合集类型
type CollectionType string

const (
	CollectionSeason CollectionType = "season"
	CollectionSeries CollectionType = "series"
)

// CollectionInfo 统一合集信息
type CollectionInfo struct {
	Type CollectionType
	MID  int64
	ID   int64 // SeasonID 或 SeriesID
}

// SeriesMeta Series（视频列表）元数据
type SeriesMeta struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MID         int64  `json:"mid"`
	Total       int    `json:"total"`
	Cover       string `json:"cover"`
}

// SeriesArchive Series 内的视频
type SeriesArchive struct {
	BvID     string `json:"bvid"`
	Title    string `json:"title"`
	Pic      string `json:"pic"`
	Duration int    `json:"duration"`
	PubDate  int64  `json:"pubdate"`
}

// === 动态 API 数据结构 ===

// DynamicItem 动态 API 单条动态
type DynamicItem struct {
	Type    string `json:"type"`
	Modules struct {
		ModuleAuthor struct {
			PubTS FlexInt64 `json:"pub_ts"`
		} `json:"module_author"`
		ModuleDynamic struct {
			Major *struct {
				Archive *DynamicArchive `json:"archive"`
			} `json:"major"`
		} `json:"module_dynamic"`
	} `json:"modules"`
}

// DynamicArchive 动态中的视频信息
type DynamicArchive struct {
	BvID         string `json:"bvid"`
	Title        string `json:"title"`
	Cover        string `json:"cover"`
	DurationText string `json:"duration_text"`
	Desc         string `json:"desc"`
}

// DynamicResponse 动态 API 响应
type DynamicResponse struct {
	Items   []DynamicItem `json:"items"`
	HasMore bool          `json:"has_more"`
	Offset  string        `json:"offset"`
}

// VideoBasic 通用的基础视频信息（用于增量拉取返回）
type VideoBasic struct {
	BvID        string
	Title       string
	Cover       string
	Desc        string
	PubTS       int64  // 发布时间戳
	DurationStr string // "MM:SS" 格式
}

// === API 方法 ===

// GetUPInfo 获取 UP 主信息（含头像），需要 WBI 签名
func (c *Client) GetUPInfo(mid int64) (*UPInfo, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data UPInfo `json:"data"`
	}
	params := url.Values{}
	params.Set("mid", fmt.Sprintf("%d", mid))
	err := c.getWbi("https://api.bilibili.com/x/space/wbi/acc/info", params, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return &resp.Data, nil
}

// GetVideoDetail 获取视频详情（含 owner.face 头像）
func (c *Client) GetVideoDetail(bvid string) (*VideoDetail, error) {
	var resp struct {
		Code int         `json:"code"`
		Msg  string      `json:"message"`
		Data VideoDetail `json:"data"`
	}
	err := c.get(fmt.Sprintf("https://api.bilibili.com/x/web-interface/view?bvid=%s", bvid), &resp)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return &resp.Data, nil
}

// GetUPVideos 获取 UP 主投稿视频列表（分页），需要 WBI 签名
func (c *Client) GetUPVideos(mid int64, page, pageSize int) ([]VideoItem, int, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			List struct {
				VList []VideoItem `json:"vlist"`
			} `json:"list"`
			Page struct {
				Count int `json:"count"`
			} `json:"page"`
		} `json:"data"`
	}
	params := url.Values{}
	params.Set("mid", fmt.Sprintf("%d", mid))
	params.Set("ps", fmt.Sprintf("%d", pageSize))
	params.Set("pn", fmt.Sprintf("%d", page))
	params.Set("order", "pubdate")
	params.Set("order_avoided", "true")
	params.Set("platform", "web")
	params.Set("web_location", "1550101")
	if err := c.getWbi("https://api.bilibili.com/x/space/wbi/arc/search", params, &resp); err != nil {
		return nil, 0, err
	}
	if resp.Code != 0 {
		return nil, 0, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return resp.Data.List.VList, resp.Data.Page.Count, nil
}

// GetSeasonVideos 获取合集视频列表
func (c *Client) GetSeasonVideos(mid, seasonID int64, page, pageSize int) ([]SeasonArchive, *SeasonMeta, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Archives []SeasonArchive `json:"archives"`
			Meta     SeasonMeta      `json:"meta"`
			Page     struct {
				Total int `json:"total"`
			} `json:"page"`
		} `json:"data"`
	}
	url := fmt.Sprintf("https://api.bilibili.com/x/polymer/web-space/seasons_archives_list?mid=%d&season_id=%d&page_num=%d&page_size=%d",
		mid, seasonID, page, pageSize)
	if err := c.get(url, &resp); err != nil {
		return nil, nil, err
	}
	if resp.Code != 0 {
		return nil, nil, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return resp.Data.Archives, &resp.Data.Meta, nil
}

// GetVideoTags 获取视频标签
func (c *Client) GetVideoTags(bvid string) ([]string, error) {
	var resp struct {
		Code int `json:"code"`
		Data []struct {
			TagName string `json:"tag_name"`
		} `json:"data"`
	}
	if err := c.get(fmt.Sprintf("https://api.bilibili.com/x/tag/archive/tags?bvid=%s", bvid), &resp); err != nil {
		return nil, err
	}
	var tags []string
	for _, t := range resp.Data {
		tags = append(tags, t.TagName)
	}
	return tags, nil
}

// GetDynamicVideos 获取 UP 主动态中的视频（单页）
func (c *Client) GetDynamicVideos(mid int64, offset string) (*DynamicResponse, error) {
	params := url.Values{}
	params.Set("host_mid", fmt.Sprintf("%d", mid))
	params.Set("offset", offset)
	params.Set("type", "video")

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Items   []DynamicItem `json:"items"`
			HasMore bool          `json:"has_more"`
			Offset  string        `json:"offset"`
		} `json:"data"`
	}

	if err := c.getWbi("https://api.bilibili.com/x/polymer/web-dynamic/v1/feed/space", params, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("bilibili dynamic: %d %s", resp.Code, resp.Msg)
	}
	return &DynamicResponse{
		Items:   resp.Data.Items,
		HasMore: resp.Data.HasMore,
		Offset:  resp.Data.Offset,
	}, nil
}

// FetchDynamicVideosIncremental 通过动态 API 增量拉取 UP 主视频
// latestVideoAt > 0 时，遇到 pub_ts <= latestVideoAt 停止（第一条除外，可能是置顶）
// 返回新视频列表（按时间倒序，最新在前）
func (c *Client) FetchDynamicVideosIncremental(mid int64, latestVideoAt int64) ([]VideoBasic, error) {
	var result []VideoBasic
	offset := ""
	pageIdx := 0

	for {
		dynResp, err := c.GetDynamicVideos(mid, offset)
		if err != nil {
			return result, err
		}

		shouldStop := false
		for itemIdx, item := range dynResp.Items {
			if item.Type != "DYNAMIC_TYPE_AV" {
				continue
			}
			if item.Modules.ModuleDynamic.Major == nil || item.Modules.ModuleDynamic.Major.Archive == nil {
				continue
			}

			archive := item.Modules.ModuleDynamic.Major.Archive
			pubTS := int64(item.Modules.ModuleAuthor.PubTS)

			// 增量截止判断：
			// idx==0 && pageIdx==0 时跳过判断（可能是置顶动态，时间很旧但不应该因此停止）
			isFirstItem := (pageIdx == 0 && itemIdx == 0)
			if latestVideoAt > 0 && pubTS <= latestVideoAt && !isFirstItem {
				shouldStop = true
				break
			}

			result = append(result, VideoBasic{
				BvID:        archive.BvID,
				Title:       archive.Title,
				Cover:       archive.Cover,
				Desc:        archive.Desc,
				PubTS:       pubTS,
				DurationStr: archive.DurationText,
			})
		}

		if shouldStop || !dynResp.HasMore || dynResp.Offset == "" {
			break
		}

		offset = dynResp.Offset
		pageIdx++

		// [FIXED: P2-2] 安全限制：最多翻 200 页（原注释误写为 50 页）
		if pageIdx >= 200 {
			log.Printf("[dynamic] 达到最大翻页限制 200 页，mid=%d", mid)
			break
		}

		time.Sleep(time.Duration(300+rand.Intn(300)) * time.Millisecond)
	}

	return result, nil
}

// === 工具方法 ===

func (c *Client) get(rawURL string, result interface{}) error {
	// API 请求限流
	if c.limiter != nil {
		c.limiter.Acquire()
	}
	// [FIXED: P1-2] 检查 http.NewRequest 错误，避免非法 URL 导致 req 为 nil 后 panic
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Referer", "https://www.bilibili.com")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 412 {
		log.Printf("[WARN] B站风控: HTTP 412 请求过于频繁, url=%s", rawURL)
		return NewInvalidStatusCode(412, "请求过于频繁")
	}
	if resp.StatusCode != 200 {
		return NewInvalidStatusCode(resp.StatusCode, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
	if err := json.Unmarshal(body, result); err != nil {
		return err
	}
	// 检测 API 响应码中的风控信号
	return checkRateLimitCode(body)
}

// checkRateLimitCode 检测 B站 API 响应中的风控码，返回 *BiliError 或 nil
func checkRateLimitCode(body []byte) error {
	var base struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(body, &base); err != nil {
		return nil // 无法解析就不检测
	}
	// [FIXED: P1-1] 将风控错误包装为 ErrRateLimited，使调用者可通过 errors.Is(err, ErrRateLimited) 统一检测
	switch base.Code {
	case -352:
		log.Printf("[WARN] B站风控: code=-352 (风控校验失败)")
		return fmt.Errorf("%s: %w", "风控校验失败", ErrRateLimited)
	case -401:
		log.Printf("[WARN] B站风控: code=-401 (未登录/鉴权失败)")
		return fmt.Errorf("%s: %w", "未登录/鉴权失败", ErrRateLimited)
	case -403:
		log.Printf("[WARN] B站风控: code=-403 (访问权限不足/鉴权异常)")
		return fmt.Errorf("%s: %w", "访问权限不足", ErrRateLimited)
	case -412:
		log.Printf("[WARN] B站风控: code=-412 (请求过于频繁)")
		return fmt.Errorf("%s: %w", "请求过于频繁", ErrRateLimited)
	}

	// v_voucher 风控检测: data.v_voucher 非空即触发
	var voucherCheck struct {
		Data struct {
			VVoucher string `json:"v_voucher"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &voucherCheck); err == nil && voucherCheck.Data.VVoucher != "" {
		log.Printf("[WARN] B站风控: v_voucher detected (需要人机验证)")
		return fmt.Errorf("%s: %w", "v_voucher detected, 需要人机验证", ErrRateLimited)
	}

	return nil
}

// ExtractMID 从 space.bilibili.com/xxx 提取 MID
func ExtractMID(rawURL string) (int64, error) {
	re := reSpaceMID
	m := re.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		// [FIXED: P2-3] 改用 strconv.ParseInt，可检测超出 int64 范围的情况（fmt.Sscanf 会静默截断）
		mid, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("cannot parse MID %q: %w", m[1], err)
		}
		return mid, nil
	}
	return 0, fmt.Errorf("cannot extract MID from: %s", rawURL)
}

// ReadCookieFile 解析 Netscape cookie.txt
func ReadCookieFile(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("Read cookie file failed: %v", err)
		return ""
	}
	var cookies []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 && strings.Contains(parts[0], "bilibili") {
			cookies = append(cookies, parts[5]+"="+parts[6])
		}
	}
	return strings.Join(cookies, "; ")
}

// SanitizePath 清理非法文件名字符
// spaceCollapser 用于合并连续空格
var spaceCollapser = regexp.MustCompile(`\s{2,}`)

func SanitizePath(name string) string {
	// 第一轮：替换文件系统非法字符
	for _, c := range []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"} {
		name = strings.ReplaceAll(name, c, "_")
	}

	// 第二轮：过滤 Unicode 不可见/控制字符
	name = strings.Map(func(r rune) rune {
		switch {
		case r <= 0x001F: // C0 控制字符 (U+0000-U+001F)
			return -1
		case r == 0x007F: // DEL
			return -1
		case r >= 0x0080 && r <= 0x009F: // C1 控制字符
			return -1
		case r == 0xFFFD: // 替换字符 \ufffd
			return -1
		case r >= 0x200B && r <= 0x200F: // 零宽字符
			return -1
		case r >= 0x2028 && r <= 0x202F: // 行/段分隔符等
			return -1
		case r >= 0x2060 && r <= 0x206F: // 不可见格式字符
			return -1
		case r == 0xFEFF: // BOM/零宽不断空格
			return -1
		case r >= 0xFE00 && r <= 0xFE0F: // 变体选择器（emoji 修饰符）
			return -1
		case r >= 0xE0000 && r <= 0xE007F: // 标签字符
			return -1
		default:
			return r
		}
	}, name)

	// 第三轮：清理空格
	name = spaceCollapser.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)

	// 特殊值检查
	if name == "" || name == "." || name == ".." {
		return "unknown"
	}

	// 按 rune 截断到 80 字符（不切断 UTF-8 序列）
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}

	return name
}

// DurationStr 将 "MM:SS" 转为秒
func DurationStr(s string) int {
	parts := strings.Split(s, ":")
	if len(parts) == 2 {
		var m, sec int
		fmt.Sscanf(parts[0], "%d", &m)
		fmt.Sscanf(parts[1], "%d", &sec)
		return m*60 + sec
	}
	return 0
}

// sharedLargeDownloadClient 用于大文件（视频/音频）下载的复用 HTTP Client，不设超时
var sharedLargeDownloadClient = &http.Client{Timeout: 0}

// sharedDownloadClient 用于封面/头像等小文件下载的复用 HTTP Client
var sharedDownloadClient = &http.Client{Timeout: 60 * time.Second}

// DownloadFile 下载文件到本地（带 Referer 和 UA 防盗链，复用 HTTP Client）
func DownloadFile(rawURL, dest string) error {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")

	resp, err := sharedDownloadClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// 预编译正则表达式
var (
	reSpaceMID = regexp.MustCompile(`space\.bilibili\.com/(\d+)`)
	reFID      = regexp.MustCompile(`fid=(\d+)`)
	reListsID  = regexp.MustCompile(`/lists/(\d+)`)
	reSID      = regexp.MustCompile(`sid=(\d+)`)
	reBVID     = regexp.MustCompile(`(BV[a-zA-Z0-9]{10})`)
	reAVID     = regexp.MustCompile(`(?i)av(\d+)`)
)

// ExtractBVID 从各种 B 站 URL 格式或裸 BV 号中提取 BV ID
// 支持格式:
//   - BV1xx411c7mD (裸 BV 号)
//   - https://www.bilibili.com/video/BV1xx411c7mD
//   - https://b23.tv/xxxxxx (短链 - 会自动解析重定向)
//   - https://www.bilibili.com/video/av12345 (AV 号)
//   - av12345 (裸 AV 号)
func ExtractBVID(input string) (bvid string, avid int64, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", 0, fmt.Errorf("empty input")
	}

	// 0. 短链接先解析
	if strings.Contains(input, "b23.tv") {
		if resolved, resolveErr := ResolveShortURL(input); resolveErr == nil {
			input = resolved
		}
	}

	// 1. 尝试提取 BV 号
	if m := reBVID.FindStringSubmatch(input); len(m) > 1 {
		return m[1], 0, nil
	}

	// 2. 尝试提取 AV 号
	if m := reAVID.FindStringSubmatch(input); len(m) > 1 {
		aid, parseErr := strconv.ParseInt(m[1], 10, 64)
		if parseErr == nil && aid > 0 {
			return "", aid, nil
		}
	}

	return "", 0, fmt.Errorf("cannot extract BV/AV ID from: %s", input)
}

// AV2BV 将 AV 号转换为 BV 号（通过 API）
func (c *Client) AV2BV(avid int64) (string, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			BvID string `json:"bvid"`
		} `json:"data"`
	}
	err := c.get(fmt.Sprintf("https://api.bilibili.com/x/web-interface/view?aid=%d", avid), &resp)
	if err != nil {
		return "", err
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	if resp.Data.BvID == "" {
		return "", fmt.Errorf("empty bvid for av%d", avid)
	}
	return resp.Data.BvID, nil
}

// ResolveShortURL 解析 b23.tv 短链接，返回最终 URL
func ResolveShortURL(shortURL string) (string, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随重定向
		},
		Timeout: 10 * time.Second,
	}
	resp, err := client.Get(shortURL)
	if err != nil {
		return "", fmt.Errorf("resolve short URL: %w", err)
	}
	defer resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "" {
		return loc, nil
	}
	return shortURL, fmt.Errorf("no redirect found for: %s", shortURL)
}

var uas = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36 Edg/134.0.0.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
}

func randUA() string { return uas[rand.Intn(len(uas))] }

// === 收藏夹和稍后再看 ===

// FavoriteFolder 收藏夹信息
type FavoriteFolder struct {
	ID         int64  `json:"id"`
	FID        int64  `json:"fid"`
	MID        int64  `json:"mid"`
	Title      string `json:"title"`
	MediaCount int    `json:"media_count"`
	Cover      string `json:"cover"`
	Intro      string `json:"intro"`
}

// FavoriteVideoItem 收藏夹内视频
type FavoriteVideoItem struct {
	BvID     string `json:"bvid"`
	Title    string `json:"title"`
	Desc     string `json:"desc"`
	Pic      string `json:"pic"`
	Duration int    `json:"duration"`
	PubDate  int64  `json:"pubdate"`
	Attr     int    `json:"attr"` // 收藏夹属性, 9=失效
	Type     int    `json:"type"` // 类型
	Owner    struct {
		MID  int64  `json:"mid"`
		Name string `json:"name"`
		Face string `json:"face"`
	} `json:"owner"`
	SeasonID int64 `json:"season_id"`
}

// IsInvalid 检测收藏夹中的失效视频
func (f *FavoriteVideoItem) IsInvalid() bool {
	return f.Attr == 9 || f.Title == "已失效视频"
}

// WatchLaterVideoItem 稍后再看视频
type WatchLaterVideoItem struct {
	BvID     string `json:"bvid"`
	Title    string `json:"title"`
	Desc     string `json:"desc"`
	Pic      string `json:"pic"`
	Duration int    `json:"duration"`
	PubDate  int64  `json:"pubdate"`
	Owner    struct {
		MID  int64  `json:"mid"`
		Name string `json:"name"`
		Face string `json:"face"`
	} `json:"owner"`
}

// GetFavoriteList 获取用户所有收藏夹列表
// API: https://api.bilibili.com/x/v3/fav/folder/created/list-all?up_mid={mid}
func (c *Client) GetFavoriteList(mid int64) ([]FavoriteFolder, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			List []FavoriteFolder `json:"list"`
		} `json:"data"`
	}
	err := c.get(fmt.Sprintf("https://api.bilibili.com/x/v3/fav/folder/created/list-all?up_mid=%d", mid), &resp)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return resp.Data.List, nil
}

// GetFavoriteVideos 获取收藏夹内视频列表（分页）
// API: https://api.bilibili.com/x/v3/fav/resource/list?media_id={id}&pn={page}&ps={pageSize}
func (c *Client) GetFavoriteVideos(mediaID int64, page, pageSize int) ([]FavoriteVideoItem, bool, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Medias  []FavoriteVideoItem `json:"medias"`
			HasMore bool                `json:"has_more"`
		} `json:"data"`
	}
	err := c.get(fmt.Sprintf("https://api.bilibili.com/x/v3/fav/resource/list?media_id=%d&pn=%d&ps=%d", mediaID, page, pageSize), &resp)
	if err != nil {
		return nil, false, err
	}
	if resp.Code != 0 {
		return nil, false, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return resp.Data.Medias, resp.Data.HasMore, nil
}

// GetWatchLater 获取稍后再看列表
// API: https://api.bilibili.com/x/v2/history/toview/web
func (c *Client) GetWatchLater() ([]WatchLaterVideoItem, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			List []WatchLaterVideoItem `json:"list"`
		} `json:"data"`
	}
	err := c.get("https://api.bilibili.com/x/v2/history/toview/web", &resp)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return resp.Data.List, nil
}

// ExtractFavoriteInfo 从 URL 提取收藏夹信息
// 支持: https://space.bilibili.com/{mid}/favlist?fid={mediaID}
func ExtractFavoriteInfo(rawURL string) (mid int64, mediaID int64, err error) {
	// 提取 mid
	reMid := reSpaceMID
	m := reMid.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &mid)
	}
	// 提取 mediaID (fid)
	reFid := reFID
	m = reFid.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &mediaID)
	}
	if mid == 0 {
		err = fmt.Errorf("cannot extract mid from: %s", rawURL)
	}
	return
}

// ExtractWatchLaterInfo 从 URL 提取稍后再看信息
// 支持: watchlater://{mid} 或从 cookie 获取当前用户
func ExtractWatchLaterInfo(rawURL string) (mid int64, err error) {
	if strings.HasPrefix(rawURL, "watchlater://") {
		midStr := strings.TrimPrefix(rawURL, "watchlater://")
		if midStr != "" {
			fmt.Sscanf(midStr, "%d", &mid)
		}
	}
	// 如果没有 mid，返回 0 表示使用当前登录用户
	return
}

// ExtractSeasonInfo 从 URL 提取合集信息
// 支持: https://space.bilibili.com/{mid}/lists/{seasonID}?type=season
//
//	https://space.bilibili.com/{mid}/channel/collectiondetail?sid={seasonID}
func ExtractSeasonInfo(rawURL string) (mid int64, seasonID int64, err error) {
	reMid := reSpaceMID
	m := reMid.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &mid)
	}

	// /lists/{id}?type=season
	reLists := reListsID
	m = reLists.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &seasonID)
	}

	// collectiondetail?sid={id}
	if seasonID == 0 {
		reSid := reSID
		m = reSid.FindStringSubmatch(rawURL)
		if len(m) > 1 {
			fmt.Sscanf(m[1], "%d", &seasonID)
		}
	}

	if mid == 0 || seasonID == 0 {
		err = fmt.Errorf("cannot extract season info from: %s", rawURL)
	}
	return
}

// GetSeriesInfo 获取 Series（视频列表）信息
func (c *Client) GetSeriesInfo(mid, seriesID int64) (*SeriesMeta, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Meta SeriesMeta `json:"meta"`
		} `json:"data"`
	}
	err := c.get(fmt.Sprintf("https://api.bilibili.com/x/series/series?series_id=%d", seriesID), &resp)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	resp.Data.Meta.MID = mid
	return &resp.Data.Meta, nil
}

// GetSeriesVideos 获取 Series 内视频列表（分页）
func (c *Client) GetSeriesVideos(mid, seriesID int64, page, pageSize int) ([]SeriesArchive, int, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Archives []SeriesArchive `json:"archives"`
			Page     struct {
				Total int `json:"total"`
			} `json:"page"`
		} `json:"data"`
	}
	url := fmt.Sprintf("https://api.bilibili.com/x/series/archives?mid=%d&series_id=%d&pn=%d&ps=%d&sort=asc",
		mid, seriesID, page, pageSize)
	if err := c.get(url, &resp); err != nil {
		return nil, 0, err
	}
	if resp.Code != 0 {
		return nil, 0, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return resp.Data.Archives, resp.Data.Page.Total, nil
}

// ExtractCollectionInfo 统一解析合集 URL（Season 和 Series）
func ExtractCollectionInfo(rawURL string) (*CollectionInfo, error) {
	var mid, id int64

	m := reSpaceMID.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &mid)
	}

	// Series: /lists/{id}?type=series 或 seriesdetail?sid={id}
	if strings.Contains(rawURL, "type=series") || strings.Contains(rawURL, "seriesdetail") {
		m = reListsID.FindStringSubmatch(rawURL)
		if len(m) > 1 {
			fmt.Sscanf(m[1], "%d", &id)
		}
		if id == 0 {
			m = reSID.FindStringSubmatch(rawURL)
			if len(m) > 1 {
				fmt.Sscanf(m[1], "%d", &id)
			}
		}
		if mid > 0 && id > 0 {
			return &CollectionInfo{Type: CollectionSeries, MID: mid, ID: id}, nil
		}
	}

	// Season: /lists/{id}?type=season 或 collectiondetail?sid={id}
	m = reListsID.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &id)
	}
	if id == 0 {
		m = reSID.FindStringSubmatch(rawURL)
		if len(m) > 1 {
			fmt.Sscanf(m[1], "%d", &id)
		}
	}

	if mid > 0 && id > 0 {
		return &CollectionInfo{Type: CollectionSeason, MID: mid, ID: id}, nil
	}

	return nil, fmt.Errorf("cannot extract collection info from: %s", rawURL)
}

// GetSeriesVideosSorted 获取 Series 内视频列表（分页，支持排序方向）
// sortOrder: "asc" 或 "desc"
func (c *Client) GetSeriesVideosSorted(mid, seriesID int64, page, pageSize int, sortOrder string) ([]SeriesArchive, int, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Archives []SeriesArchive `json:"archives"`
			Page     struct {
				Total int `json:"total"`
			} `json:"page"`
		} `json:"data"`
	}
	url := fmt.Sprintf("https://api.bilibili.com/x/series/archives?mid=%d&series_id=%d&pn=%d&ps=%d&sort=%s",
		mid, seriesID, page, pageSize, sortOrder)
	if err := c.get(url, &resp); err != nil {
		return nil, 0, err
	}
	if resp.Code != 0 {
		return nil, 0, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return resp.Data.Archives, resp.Data.Page.Total, nil
}

// === /me 接口：关注列表 & 收藏夹 ===

// FavoriteItem 我的收藏夹（list-all 接口返回）
type FavoriteItem struct {
	ID         int64  `json:"id"`
	FID        int64  `json:"fid"`
	MID        int64  `json:"mid"`
	Title      string `json:"title"`
	MediaCount int    `json:"media_count"`
	AttrStr    string `json:"attr"`
}

// FollowedUpper 关注的 UP 主
type FollowedUpper struct {
	MID  int64  `json:"mid"`
	Name string `json:"uname"`
	Face string `json:"face"`
	Sign string `json:"sign"`
}

// FollowedUppers 关注列表分页响应
type FollowedUppers struct {
	List  []FollowedUpper `json:"list"`
	Total int             `json:"total"`
}

// GetMyFavorites 获取我的收藏夹列表
// GET https://api.bilibili.com/x/v3/fav/folder/created/list-all?up_mid={dedeuserid}
func (c *Client) GetMyFavorites() ([]FavoriteItem, error) {
	uid := c.getDedeUserID()
	if uid == "" {
		return nil, fmt.Errorf("未登录：缺少 DedeUserID")
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			List []FavoriteItem `json:"list"`
		} `json:"data"`
	}
	err := c.get(fmt.Sprintf("https://api.bilibili.com/x/v3/fav/folder/created/list-all?up_mid=%s", uid), &resp)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return resp.Data.List, nil
}

// GetFollowedUppers 获取我关注的 UP 主列表（分页）
// GET https://api.bilibili.com/x/relation/followings?vmid={dedeuserid}&pn={page}&ps={pageSize}
func (c *Client) GetFollowedUppers(page, pageSize int) (*FollowedUppers, error) {
	uid := c.getDedeUserID()
	if uid == "" {
		return nil, fmt.Errorf("未登录：缺少 DedeUserID")
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			List  []FollowedUpper `json:"list"`
			Total int             `json:"total"`
		} `json:"data"`
	}
	err := c.get(fmt.Sprintf("https://api.bilibili.com/x/relation/followings?vmid=%s&pn=%d&ps=%d", uid, page, pageSize), &resp)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return &FollowedUppers{List: resp.Data.List, Total: resp.Data.Total}, nil
}

// SearchFollowedUppers 搜索关注的 UP 主
// GET https://api.bilibili.com/x/relation/followings/search?vmid={dedeuserid}&name={name}&pn={page}&ps={pageSize}
func (c *Client) SearchFollowedUppers(name string, page, pageSize int) (*FollowedUppers, error) {
	uid := c.getDedeUserID()
	if uid == "" {
		return nil, fmt.Errorf("未登录：缺少 DedeUserID")
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			List  []FollowedUpper `json:"list"`
			Total int             `json:"total"`
		} `json:"data"`
	}
	encodedName := url.QueryEscape(name)
	err := c.get(fmt.Sprintf("https://api.bilibili.com/x/relation/followings/search?vmid=%s&name=%s&pn=%d&ps=%d", uid, encodedName, page, pageSize), &resp)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("bilibili: %d %s", resp.Code, resp.Msg)
	}
	return &FollowedUppers{List: resp.Data.List, Total: resp.Data.Total}, nil
}

// getDedeUserID 从 credential 或 cookie 中提取 DedeUserID
func (c *Client) getDedeUserID() string {
	if c.credential != nil && c.credential.DedeUserID != "" {
		return c.credential.DedeUserID
	}
	// 从 cookie 字符串解析
	if c.cookie != "" {
		for _, part := range strings.Split(c.cookie, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "DedeUserID=") {
				return strings.TrimPrefix(part, "DedeUserID=")
			}
		}
	}
	return ""
}

// ExtractMIDFromURL 从 B 站 URL 提取 mid
func ExtractMIDFromURL(rawURL string) string {
	m := reSpaceMID.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}
