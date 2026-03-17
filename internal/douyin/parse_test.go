package douyin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// newDummyRequest creates a dummy HTTP request for header testing
func newDummyRequest() (*http.Request, error) {
	return http.NewRequest("GET", "https://example.com", nil)
}

// ===== parseAwemeDetail tests =====

func TestParseAwemeDetail_VideoBasic(t *testing.T) {
	raw := json.RawMessage(`{
		"aweme_id": "7234567890123456789",
		"desc": "test video desc",
		"create_time": 1700000000,
		"author": {
			"uid": "uid123",
			"sec_uid": "MS4wLjABAAAAtest",
			"nickname": "testuser",
			"avatar_thumb": {
				"url_list": ["https://example.com/avatar.jpg"]
			}
		},
		"video": {
			"cover": {
				"url_list": ["https://example.com/cover.jpg"]
			},
			"play_addr": {
				"url_list": ["https://example.com/playwm/video.mp4"]
			},
			"duration": 15000
		},
		"statistics": {
			"digg_count": 1000,
			"share_count": 200,
			"comment_count": 50
		}
	}`)

	video, err := parseAwemeDetail(raw, "7234567890123456789", false)
	if err != nil {
		t.Fatalf("parseAwemeDetail() error: %v", err)
	}
	if video.AwemeID != "7234567890123456789" {
		t.Errorf("AwemeID = %q, want %q", video.AwemeID, "7234567890123456789")
	}
	if video.Desc != "test video desc" {
		t.Errorf("Desc = %q", video.Desc)
	}
	if video.CreateTime != 1700000000 {
		t.Errorf("CreateTime = %d", video.CreateTime)
	}
	if video.Author.UID != "uid123" {
		t.Errorf("Author.UID = %q", video.Author.UID)
	}
	if video.Author.SecUID != "MS4wLjABAAAAtest" {
		t.Errorf("Author.SecUID = %q", video.Author.SecUID)
	}
	if video.Author.Nickname != "testuser" {
		t.Errorf("Author.Nickname = %q", video.Author.Nickname)
	}
	if video.Author.AvatarURL != "https://example.com/avatar.jpg" {
		t.Errorf("Author.AvatarURL = %q", video.Author.AvatarURL)
	}
	if video.Cover != "https://example.com/cover.jpg" {
		t.Errorf("Cover = %q", video.Cover)
	}
	if strings.Contains(video.VideoURL, "playwm") {
		t.Errorf("VideoURL = %q, should not contain playwm", video.VideoURL)
	}
	if video.Duration != 15000 {
		t.Errorf("Duration = %d", video.Duration)
	}
	if video.DiggCount != 1000 {
		t.Errorf("DiggCount = %d", video.DiggCount)
	}
	if video.ShareCount != 200 {
		t.Errorf("ShareCount = %d", video.ShareCount)
	}
	if video.CommentCount != 50 {
		t.Errorf("CommentCount = %d", video.CommentCount)
	}
	if video.IsNote {
		t.Error("IsNote should be false")
	}
}

func TestParseAwemeDetail_NoteWithImages(t *testing.T) {
	raw := json.RawMessage(`{
		"aweme_id": "note123",
		"desc": "note desc",
		"create_time": 1700000000,
		"author": {"uid": "u", "sec_uid": "s", "nickname": "n"},
		"aweme_type": 68,
		"images": [
			{"url_list": ["https://a.com/img1.webp", "https://a.com/img1.jpg"]},
			{"url_list": ["https://a.com/img2.jpg"]},
			{"url_list": ["https://a.com/img3.webp"]}
		],
		"video": {"play_addr": {"url_list": ["https://a.com/video.mp4"]}}
	}`)

	video, err := parseAwemeDetail(raw, "note123", true)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !video.IsNote {
		t.Error("IsNote should be true")
	}
	if len(video.Images) != 3 {
		t.Fatalf("Images count = %d, want 3", len(video.Images))
	}
	if video.Images[0] != "https://a.com/img1.jpg" {
		t.Errorf("Images[0] = %q, should prefer non-webp", video.Images[0])
	}
	if video.VideoURL != "" {
		t.Errorf("VideoURL = %q, should be empty for note", video.VideoURL)
	}
}

func TestParseAwemeDetail_AwemeType68Override(t *testing.T) {
	raw := json.RawMessage(`{
		"aweme_id": "note789",
		"desc": "aweme_type 68",
		"create_time": 1700000000,
		"author": {"uid": "u", "sec_uid": "s", "nickname": "n"},
		"aweme_type": 68,
		"images": [{"url_list": ["https://a.com/img1.jpg"]}]
	}`)

	video, err := parseAwemeDetail(raw, "note789", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !video.IsNote {
		t.Error("IsNote should be true when aweme_type=68")
	}
}

func TestParseAwemeDetail_DynamicCoverFallback(t *testing.T) {
	raw := json.RawMessage(`{
		"aweme_id": "v1",
		"desc": "dyn cover",
		"create_time": 1700000000,
		"author": {"uid": "u", "sec_uid": "s", "nickname": "n"},
		"video": {
			"cover": {"url_list": []},
			"dynamic_cover": {"url_list": ["https://a.com/dyn.jpg"]}
		}
	}`)

	video, err := parseAwemeDetail(raw, "v1", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if video.Cover != "https://a.com/dyn.jpg" {
		t.Errorf("Cover = %q, want dynamic_cover fallback", video.Cover)
	}
}

func TestParseAwemeDetail_MinimalFields(t *testing.T) {
	raw := json.RawMessage(`{}`)
	video, err := parseAwemeDetail(raw, "minimal123", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if video.AwemeID != "minimal123" {
		t.Errorf("AwemeID = %q", video.AwemeID)
	}
}

func TestParseAwemeDetail_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{invalid`)
	_, err := parseAwemeDetail(raw, "bad", false)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseAwemeDetail_WebpOnly(t *testing.T) {
	raw := json.RawMessage(`{
		"aweme_id": "w1",
		"author": {"uid": "u", "sec_uid": "s", "nickname": "n"},
		"video": {"cover": {"url_list": ["https://a.com/c.webp"]}}
	}`)
	video, err := parseAwemeDetail(raw, "w1", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if video.Cover == "" {
		t.Error("Cover should not be empty even with webp-only")
	}
}

func TestParseAwemeDetail_EmptyImageURLList(t *testing.T) {
	raw := json.RawMessage(`{
		"aweme_id": "ei1",
		"author": {"uid": "u", "sec_uid": "s", "nickname": "n"},
		"images": [
			{"url_list": []},
			{"url_list": ["https://a.com/img.jpg"]}
		]
	}`)
	video, err := parseAwemeDetail(raw, "ei1", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(video.Images) != 1 {
		t.Errorf("Images count = %d, want 1", len(video.Images))
	}
}

func TestParseAwemeDetail_NoAuthor(t *testing.T) {
	raw := json.RawMessage(`{"aweme_id": "na1", "desc": "no author"}`)
	video, err := parseAwemeDetail(raw, "na1", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if video.Author.UID != "" {
		t.Errorf("Author.UID = %q, want empty", video.Author.UID)
	}
}

// ===== parseRouterDataForVideo tests =====

func makeRouterData(key string, videoInfoRes map[string]interface{}) []byte {
	data := map[string]interface{}{
		"loaderData": map[string]interface{}{
			key: map[string]interface{}{
				"videoInfoRes": videoInfoRes,
			},
		},
	}
	b, _ := json.Marshal(data)
	return b
}

func TestParseRouterDataForVideo_Normal(t *testing.T) {
	c := NewClient()
	defer c.Close()

	vir := map[string]interface{}{
		"status_code": float64(0),
		"item_list": []interface{}{
			map[string]interface{}{
				"aweme_id": "v12345", "desc": "router video",
				"create_time": float64(1700000000),
				"author": map[string]interface{}{"uid": "u", "sec_uid": "s", "nickname": "author1"},
				"video": map[string]interface{}{
					"cover":     map[string]interface{}{"url_list": []interface{}{"https://a.com/c.jpg"}},
					"play_addr": map[string]interface{}{"url_list": []interface{}{"https://a.com/playwm/v.mp4"}},
					"duration":  float64(30000),
				},
				"statistics": map[string]interface{}{"digg_count": float64(500)},
			},
		},
	}
	video, err := c.parseRouterDataForVideo(makeRouterData("video_(id)/page", vir), "v12345")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if video.Desc != "router video" {
		t.Errorf("Desc = %q", video.Desc)
	}
}

func TestParseRouterDataForVideo_NoteKey(t *testing.T) {
	c := NewClient()
	defer c.Close()

	vir := map[string]interface{}{
		"status_code": float64(0),
		"item_list": []interface{}{
			map[string]interface{}{
				"aweme_id": "n1", "desc": "note data",
				"create_time": float64(1700000000),
				"author": map[string]interface{}{"uid": "u", "sec_uid": "s", "nickname": "n"},
			},
		},
	}
	video, err := c.parseRouterDataForVideo(makeRouterData("note_(id)/page", vir), "n1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if video.Desc != "note data" {
		t.Errorf("Desc = %q", video.Desc)
	}
}

func TestParseRouterDataForVideo_FilterList(t *testing.T) {
	c := NewClient()
	defer c.Close()

	vir := map[string]interface{}{
		"status_code": float64(0),
		"filter_list": []interface{}{
			map[string]interface{}{
				"aweme_id": "vf", "filter_reason": "violation",
				"filter_type": float64(1), "detail_msg": "bad content",
			},
		},
		"item_list": []interface{}{},
	}
	_, err := c.parseRouterDataForVideo(makeRouterData("video_(id)/page", vir), "vf")
	if err == nil {
		t.Error("expected error for filtered video")
	}
	if !strings.Contains(err.Error(), "filtered") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestParseRouterDataForVideo_FilterListEmptyID(t *testing.T) {
	c := NewClient()
	defer c.Close()

	vir := map[string]interface{}{
		"status_code": float64(0),
		"filter_list": []interface{}{
			map[string]interface{}{
				"aweme_id": "", "filter_reason": "generic", "filter_type": float64(2),
			},
		},
		"item_list": []interface{}{},
	}
	_, err := c.parseRouterDataForVideo(makeRouterData("video_(id)/page", vir), "any_id")
	if err == nil {
		t.Error("expected error for filter_list with empty aweme_id")
	}
}

func TestParseRouterDataForVideo_FilterListDifferentID(t *testing.T) {
	c := NewClient()
	defer c.Close()

	vir := map[string]interface{}{
		"status_code": float64(0),
		"filter_list": []interface{}{
			map[string]interface{}{"aweme_id": "other", "filter_reason": "r", "filter_type": float64(1)},
		},
		"item_list": []interface{}{
			map[string]interface{}{
				"aweme_id": "ours", "desc": "safe",
				"create_time": float64(1700000000),
				"author": map[string]interface{}{"uid": "u", "sec_uid": "s", "nickname": "n"},
			},
		},
	}
	video, err := c.parseRouterDataForVideo(makeRouterData("video_(id)/page", vir), "ours")
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	if video.Desc != "safe" {
		t.Errorf("Desc = %q", video.Desc)
	}
}

func TestParseRouterDataForVideo_RiskControl(t *testing.T) {
	c := NewClient()
	defer c.Close()

	for _, code := range []float64{2053, 2154} {
		vir := map[string]interface{}{
			"status_code": code,
			"status_msg":  "risk",
			"item_list":   []interface{}{},
		}
		_, err := c.parseRouterDataForVideo(makeRouterData("video_(id)/page", vir), "v")
		if err == nil {
			t.Errorf("expected error for status_code=%.0f", code)
		}
	}
}

func TestParseRouterDataForVideo_EmptyItemList(t *testing.T) {
	c := NewClient()
	defer c.Close()
	vir := map[string]interface{}{"status_code": float64(0), "item_list": []interface{}{}}
	_, err := c.parseRouterDataForVideo(makeRouterData("video_(id)/page", vir), "v")
	if err == nil {
		t.Error("expected error")
	}
}

func TestParseRouterDataForVideo_MissingLoaderData(t *testing.T) {
	c := NewClient()
	defer c.Close()
	_, err := c.parseRouterDataForVideo([]byte(`{}`), "v")
	if err == nil {
		t.Error("expected error")
	}
}

func TestParseRouterDataForVideo_InvalidJSON(t *testing.T) {
	c := NewClient()
	defer c.Close()
	_, err := c.parseRouterDataForVideo([]byte(`{bad`), "v")
	if err == nil {
		t.Error("expected error")
	}
}

func TestParseRouterDataForVideo_EnumerateKeys(t *testing.T) {
	c := NewClient()
	defer c.Close()

	data := map[string]interface{}{
		"loaderData": map[string]interface{}{
			"unknown_key": map[string]interface{}{
				"videoInfoRes": map[string]interface{}{
					"status_code": float64(0),
					"item_list": []interface{}{
						map[string]interface{}{
							"aweme_id": "ve", "desc": "enum found",
							"create_time": float64(1700000000),
							"author": map[string]interface{}{"uid": "u", "sec_uid": "s", "nickname": "n"},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(data)
	video, err := c.parseRouterDataForVideo(b, "ve")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if video.Desc != "enum found" {
		t.Errorf("Desc = %q", video.Desc)
	}
}

func TestParseRouterDataForVideo_NoVideoInfoRes(t *testing.T) {
	c := NewClient()
	defer c.Close()
	data := map[string]interface{}{
		"loaderData": map[string]interface{}{
			"video_(id)/page": map[string]interface{}{"other": "data"},
		},
	}
	b, _ := json.Marshal(data)
	_, err := c.parseRouterDataForVideo(b, "v")
	if err == nil {
		t.Error("expected error")
	}
}

// ===== applySignResult tests =====

func TestApplySignResult_BothSigns(t *testing.T) {
	sr := SignResult{ABogus: "ab123", XBogus: "xb456"}
	result := applySignResult("https://api.test/path", "k=v", sr)
	if !strings.Contains(result, "a_bogus=ab123") || !strings.Contains(result, "X-Bogus=xb456") {
		t.Errorf("result = %q", result)
	}
}

func TestApplySignResult_OnlyABogus(t *testing.T) {
	sr := SignResult{ABogus: "ab"}
	result := applySignResult("https://api.test", "k=v", sr)
	if !strings.Contains(result, "a_bogus=ab") || strings.Contains(result, "X-Bogus") {
		t.Errorf("result = %q", result)
	}
}

func TestApplySignResult_OnlyXBogus(t *testing.T) {
	sr := SignResult{XBogus: "xb"}
	result := applySignResult("https://api.test", "k=v", sr)
	if strings.Contains(result, "a_bogus") || !strings.Contains(result, "X-Bogus=xb") {
		t.Errorf("result = %q", result)
	}
}

func TestApplySignResult_NoSign(t *testing.T) {
	result := applySignResult("https://api.test", "k=v", SignResult{})
	if result != "https://api.test?k=v" {
		t.Errorf("result = %q", result)
	}
}

// ===== signURLWithFallback =====

func TestSignURLWithFallback(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36"
	xb, ok := signURLWithFallback("aweme_id=123", ua)
	if !ok || xb == "" {
		t.Error("signURLWithFallback should succeed")
	}
}

// ===== setClientHints =====

func TestSetClientHints_Chrome131(t *testing.T) {
	ua := "Mozilla/5.0 Chrome/131.0.0.0 Safari/537.36"
	req, _ := newDummyRequest()
	setClientHints(req, ua)
	if !strings.Contains(req.Header.Get("sec-ch-ua"), "131") {
		t.Errorf("sec-ch-ua = %q", req.Header.Get("sec-ch-ua"))
	}
	if req.Header.Get("sec-ch-ua-mobile") != "?0" {
		t.Errorf("sec-ch-ua-mobile = %q", req.Header.Get("sec-ch-ua-mobile"))
	}
}

func TestSetClientHints_NonChrome(t *testing.T) {
	req, _ := newDummyRequest()
	setClientHints(req, "Mozilla/5.0 Safari/604.1")
	if req.Header.Get("sec-ch-ua") != "" {
		t.Error("should not set sec-ch-ua for non-Chrome")
	}
}

func TestSetClientHints_UnknownVersion(t *testing.T) {
	req, _ := newDummyRequest()
	setClientHints(req, "Chrome/999.0.0.0")
	if req.Header.Get("sec-ch-ua") != "" {
		t.Error("should not set sec-ch-ua for unknown version")
	}
}

// ===== Helpers =====

func TestPickNonWebpURL(t *testing.T) {
	tests := []struct {
		urls []interface{}
		want string
	}{
		{[]interface{}{"https://a.com/i.webp", "https://a.com/i.jpg"}, "https://a.com/i.jpg"},
		{[]interface{}{"https://a.com/i.webp"}, "https://a.com/i.webp"},
		{[]interface{}{}, ""},
		{[]interface{}{123, "https://a.com/i.jpg"}, "https://a.com/i.jpg"},
		{[]interface{}{"", "https://a.com/i.jpg"}, "https://a.com/i.jpg"},
	}
	for i, tt := range tests {
		got := pickNonWebpURL(tt.urls)
		if got != tt.want {
			t.Errorf("case %d: got %q, want %q", i, got, tt.want)
		}
	}
}

func TestPickNonWebpURLStr(t *testing.T) {
	tests := []struct {
		urls []string
		want string
	}{
		{[]string{"https://a.com/i.webp", "https://a.com/i.jpg"}, "https://a.com/i.jpg"},
		{[]string{"https://a.com/i.webp"}, "https://a.com/i.webp"},
		{[]string{}, ""},
		{[]string{"", "https://a.com/i.jpg"}, "https://a.com/i.jpg"},
	}
	for i, tt := range tests {
		got := pickNonWebpURLStr(tt.urls)
		if got != tt.want {
			t.Errorf("case %d: got %q, want %q", i, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 3) != "hel" {
		t.Error("truncate failed")
	}
	if truncate("hi", 10) != "hi" {
		t.Error("truncate no-op failed")
	}
	if truncate("", 5) != "" {
		t.Error("truncate empty failed")
	}
}

func TestGenerateWebID(t *testing.T) {
	id := generateWebID()
	if !strings.HasPrefix(id, "75") {
		t.Errorf("generateWebID() = %q, want prefix 75", id)
	}
	if len(id) != 17 {
		t.Errorf("len = %d, want 17", len(id))
	}
}

func TestRandAlphaNum(t *testing.T) {
	s := randAlphaNum(64)
	if len(s) != 64 {
		t.Errorf("len = %d", len(s))
	}
	for _, ch := range s {
		valid := (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
		if !valid {
			t.Errorf("invalid char: %c", ch)
		}
	}
}

func TestGetCanonicalFromHTML(t *testing.T) {
	html1 := `<html><head><link rel="canonical" href="https://www.douyin.com/note/123"/></head></html>`
	got := getCanonicalFromHTML(html1)
	if got != "https://www.douyin.com/note/123" {
		t.Errorf("canonical = %q", got)
	}

	got2 := getCanonicalFromHTML("<html><body>no canonical</body></html>")
	if got2 != "" {
		t.Errorf("should be empty, got %q", got2)
	}
}

func TestGetMetaContent(t *testing.T) {
	html1 := `<html><head><meta property="og:url" content="https://www.douyin.com/note/456"/></head></html>`
	got := getMetaContent(html1, "og:url")
	if got != "https://www.douyin.com/note/456" {
		t.Errorf("og:url = %q", got)
	}

	got2 := getMetaContent(html1, "og:title")
	if got2 != "" {
		t.Errorf("should be empty, got %q", got2)
	}
}

func TestBuildEndpointURLs(t *testing.T) {
	if BuildVideoDetailURL("123") != VideoDetailAPI+"?aweme_id=123" {
		t.Error("BuildVideoDetailURL failed")
	}
	if BuildVideoPageURL("456") != "https://www.iesdouyin.com/share/video/456" {
		t.Error("BuildVideoPageURL failed")
	}
	u := BuildNoteSlideInfoURL("web1", "789", "bogus1")
	if !strings.Contains(u, "789") || !strings.Contains(u, "web1") || !strings.Contains(u, "bogus1") {
		t.Errorf("BuildNoteSlideInfoURL = %q", u)
	}
	if BuildVideoWebURL("abc") != "https://www.douyin.com/video/abc" {
		t.Error("BuildVideoWebURL failed")
	}
	if BuildNoteWebURL("def") != "https://www.douyin.com/note/def" {
		t.Error("BuildNoteWebURL failed")
	}
}

func TestCreateTimeUnix(t *testing.T) {
	v := &DouyinVideo{CreateTime: 1700000000}
	ts := v.CreateTimeUnix()
	if ts.Unix() != 1700000000 {
		t.Errorf("CreateTimeUnix = %v", ts)
	}
}

// ===== buildBaseParams & setFullHeaders =====

func TestBuildBaseParams_Windows(t *testing.T) {
	c := NewClient()
	defer c.Close()

	// Force a Windows UA
	c.fingerprint.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36"

	params := c.buildBaseParams()
	if params.Get("device_platform") != "webapp" {
		t.Errorf("device_platform = %q", params.Get("device_platform"))
	}
	if params.Get("pc_libra_divert") != "Windows" {
		t.Errorf("pc_libra_divert = %q", params.Get("pc_libra_divert"))
	}
	if params.Get("os_name") != "Windows" {
		t.Errorf("os_name = %q", params.Get("os_name"))
	}
	if params.Get("msToken") == "" {
		t.Error("msToken should not be empty")
	}
}

func TestBuildBaseParams_Mac(t *testing.T) {
	c := NewClient()
	defer c.Close()

	c.fingerprint.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36"

	params := c.buildBaseParams()
	if params.Get("pc_libra_divert") != "Mac" {
		t.Errorf("pc_libra_divert = %q", params.Get("pc_libra_divert"))
	}
	if params.Get("os_name") != "Mac OS" {
		t.Errorf("os_name = %q", params.Get("os_name"))
	}
}

func TestBuildBaseParams_MsTokenConsistency(t *testing.T) {
	c := NewClient()
	defer c.Close()

	p1 := c.buildBaseParams()
	p2 := c.buildBaseParams()
	if p1.Get("msToken") != p2.Get("msToken") {
		t.Error("msToken should be consistent within same client session")
	}
}

func TestSetFullHeaders(t *testing.T) {
	c := NewClient()
	defer c.Close()

	req, _ := http.NewRequest("GET", "https://example.com", nil)
	c.setFullHeaders(req)

	if req.Header.Get("User-Agent") == "" {
		t.Error("User-Agent should be set")
	}
	if req.Header.Get("Referer") != "https://www.douyin.com/" {
		t.Errorf("Referer = %q", req.Header.Get("Referer"))
	}
	if req.Header.Get("Origin") != "https://www.douyin.com" {
		t.Errorf("Origin = %q", req.Header.Get("Origin"))
	}
	if !strings.Contains(req.Header.Get("Accept"), "application/json") {
		t.Errorf("Accept = %q", req.Header.Get("Accept"))
	}
	if req.Header.Get("Sec-Fetch-Site") != "same-origin" {
		t.Errorf("Sec-Fetch-Site = %q", req.Header.Get("Sec-Fetch-Site"))
	}
}
