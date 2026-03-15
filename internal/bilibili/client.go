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
	"strings"
	"time"
)

// ErrRateLimited 表示触发了B站风控（-352/-401/-412）
var ErrRateLimited = errors.New("bilibili: rate limited by risk control")

type Client struct {
	http       *http.Client
	cookie     string
	credential *Credential
}

func NewClient(cookie string) *Client {
	return &Client{
		http:   &http.Client{Timeout: 30 * time.Second},
		cookie: cookie,
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

type VideoDetail struct {
	BvID      string    `json:"bvid"`
	Title     string    `json:"title"`
	Desc      string    `json:"desc"`
	Pic       string    `json:"pic"`
	Duration  int       `json:"duration"`
	PubDate   int64     `json:"pubdate"`
	Owner     UPInfo    `json:"owner"`
	Stat      VideoStat `json:"stat"`
	SeasonID  int64     `json:"season_id"`
	UGCSeason *struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	} `json:"ugc_season"`
	RedirectURL string `json:"redirect_url"`
	State       int    `json:"state"` // -1=待审 -2=退回 -4=未过审 -6=删除
	Pages []struct {
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

type VideoStat struct {
	View    int64 `json:"view"`
	Danmaku int64 `json:"danmaku"`
	Like    int64 `json:"like"`
	Coin    int64 `json:"coin"`
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
	Type     CollectionType
	MID      int64
	ID       int64 // SeasonID 或 SeriesID
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
		Code int    `json:"code"`
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

// === 工具方法 ===

func (c *Client) get(rawURL string, result interface{}) error {
	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("User-Agent", randUA())
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
		return ErrRateLimited
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, result); err != nil {
		return err
	}
	// 检测 API 响应码中的风控信号
	return checkRateLimitCode(body)
}

// checkRateLimitCode 检测 B站 API 响应中的风控码
func checkRateLimitCode(body []byte) error {
	var base struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(body, &base); err != nil {
		return nil // 无法解析就不检测
	}
	switch base.Code {
	case -352:
		log.Printf("[WARN] B站风控: code=-352 (风控校验失败)")
		return ErrRateLimited
	case -401:
		log.Printf("[WARN] B站风控: code=-401 (未登录/鉴权失败)")
		return ErrRateLimited
	case -412:
		log.Printf("[WARN] B站风控: code=-412 (请求过于频繁)")
		return ErrRateLimited
	}

	// v_voucher 风控检测: data.v_voucher 非空即触发
	var voucherCheck struct {
		Data struct {
			VVoucher string `json:"v_voucher"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &voucherCheck); err == nil && voucherCheck.Data.VVoucher != "" {
		log.Printf("[WARN] B站风控: v_voucher detected (需要人机验证)")
		return ErrRateLimited
	}

	return nil
}

// ExtractMID 从 space.bilibili.com/xxx 提取 MID
func ExtractMID(rawURL string) (int64, error) {
	re := reSpaceMID
	m := re.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		var mid int64
		fmt.Sscanf(m[1], "%d", &mid)
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
func SanitizePath(name string) string {
	for _, c := range []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"} {
		name = strings.ReplaceAll(name, c, "_")
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "unknown"
	}
	if len(name) > 80 {
		name = name[:80]
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
	reSpaceMID  = regexp.MustCompile(`space\.bilibili\.com/(\d+)`)
	reFID       = regexp.MustCompile(`fid=(\d+)`)
	reListsID   = regexp.MustCompile(`/lists/(\d+)`)
	reSID       = regexp.MustCompile(`sid=(\d+)`)
)

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
	Attr     int    `json:"attr"`   // 收藏夹属性, 9=失效
	Type     int    `json:"type"`   // 类型
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
		Code int `json:"code"`
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
//        https://space.bilibili.com/{mid}/channel/collectiondetail?sid={seasonID}
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
