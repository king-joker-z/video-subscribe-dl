package douyin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// rewriteTransport intercepts HTTP requests and redirects them to a test server.
type rewriteTransport struct {
	targetURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.targetURL, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

// newTestClient creates a DouyinClient whose HTTP clients point to a test server.
func newTestClient(serverURL string) *DouyinClient {
	transport := &rewriteTransport{targetURL: serverURL}
	c := NewClient()
	c.normalClient = &http.Client{Transport: transport, Timeout: c.normalClient.Timeout}
	c.noRedirectClient = &http.Client{
		Transport: transport,
		Timeout:   c.noRedirectClient.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	c.limiter.Stop()
	c.limiter = NewRateLimiter(1000, 1000, 1*time.Millisecond)
	return c
}

// =====================================================================
// GetVideoDetail tests
// =====================================================================

func TestGetVideoDetail_APISuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			resp := map[string]interface{}{
				"status_code": 0,
				"aweme_detail": map[string]interface{}{
					"aweme_id":    "7001",
					"desc":        "test video title",
					"create_time": 1700000000.0,
					"author": map[string]interface{}{
						"uid":      "u1",
						"sec_uid":  "sec_u1",
						"nickname": "TestUser",
					},
					"video": map[string]interface{}{
						"cover": map[string]interface{}{
							"url_list": []interface{}{"https://cover.jpg"},
						},
						"play_addr": map[string]interface{}{
							"url_list": []interface{}{"https://play.com/playwm/video.mp4"},
						},
						"duration": 15000,
					},
					"statistics": map[string]interface{}{
						"digg_count":    100.0,
						"share_count":   20.0,
						"comment_count": 5.0,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	video, err := c.GetVideoDetail("7001")
	if err != nil {
		t.Fatalf("GetVideoDetail() error: %v", err)
	}
	if video.AwemeID != "7001" {
		t.Errorf("AwemeID = %q, want %q", video.AwemeID, "7001")
	}
	if video.Desc != "test video title" {
		t.Errorf("Desc = %q, want %q", video.Desc, "test video title")
	}
	if video.Author.Nickname != "TestUser" {
		t.Errorf("Author.Nickname = %q, want %q", video.Author.Nickname, "TestUser")
	}
	if !strings.Contains(video.VideoURL, "play/video.mp4") {
		t.Errorf("VideoURL = %q, expected 'playwm' replaced with 'play'", video.VideoURL)
	}
	if video.DiggCount != 100 {
		t.Errorf("DiggCount = %d, want 100", video.DiggCount)
	}
	if video.Duration != 15000 {
		t.Errorf("Duration = %d, want 15000", video.Duration)
	}
}

func TestGetVideoDetail_APIRiskControl2053(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			resp := map[string]interface{}{
				"status_code":  2053,
				"aweme_detail": nil,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.Write([]byte("captcha"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetVideoDetail("7002")
	if err == nil {
		t.Fatal("expected error for risk control, got nil")
	}
}

func TestGetVideoDetail_APIRiskControl2154(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			resp := map[string]interface{}{
				"status_code":  2154,
				"aweme_detail": nil,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.Write([]byte("captcha"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetVideoDetail("7003")
	if err == nil {
		t.Fatal("expected error for rate limited, got nil")
	}
}

func TestGetVideoDetail_APIVideoNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			resp := map[string]interface{}{
				"status_code":  8,
				"aweme_detail": nil,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.Write([]byte("captcha"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetVideoDetail("9999")
	if err == nil {
		t.Fatal("expected error for video not found, got nil")
	}
}

func TestGetVideoDetail_APITruncatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			w.Write([]byte("{}"))
			return
		}
		w.Write([]byte("x"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetVideoDetail("7004")
	if err == nil {
		t.Fatal("expected error for truncated response, got nil")
	}
}

func TestGetVideoDetail_APIHTTP500_FallbackFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			w.WriteHeader(500)
			w.Write([]byte("internal error"))
			return
		}
		w.Write([]byte("small"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetVideoDetail("7005")
	if err == nil {
		t.Fatal("expected error when both API and page scrape fail, got nil")
	}
}

func TestGetVideoDetail_NoteDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			resp := map[string]interface{}{
				"status_code": 0,
				"aweme_detail": map[string]interface{}{
					"aweme_id":    "7006",
					"desc":        "my note",
					"create_time": 1700000000.0,
					"aweme_type":  68,
					"author": map[string]interface{}{
						"uid":      "u2",
						"sec_uid":  "sec_u2",
						"nickname": "NoteUser",
					},
					"video": map[string]interface{}{
						"cover": map[string]interface{}{
							"url_list": []interface{}{"https://cover.jpg"},
						},
					},
					"images": []interface{}{
						map[string]interface{}{
							"url_list": []interface{}{"https://img1.jpg", "https://img1.webp"},
						},
						map[string]interface{}{
							"url_list": []interface{}{"https://img2.jpg"},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	video, err := c.GetVideoDetail("7006")
	if err != nil {
		t.Fatalf("GetVideoDetail() error: %v", err)
	}
	if !video.IsNote {
		t.Error("expected IsNote=true for aweme_type=68")
	}
	if len(video.Images) != 2 {
		t.Errorf("Images count = %d, want 2", len(video.Images))
	}
	if video.VideoURL != "" {
		t.Errorf("VideoURL = %q, want empty for note", video.VideoURL)
	}
	if video.Images[0] != "https://img1.jpg" {
		t.Errorf("Images[0] = %q, want non-webp", video.Images[0])
	}
}

// =====================================================================
// GetUserVideos tests
// =====================================================================

func TestGetUserVideos_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status_code": 0,
			"has_more":    1,
			"max_cursor":  1700000001,
			"aweme_list": []interface{}{
				map[string]interface{}{
					"aweme_id":    "8001",
					"desc":        "video one",
					"create_time": 1700000000.0,
					"author": map[string]interface{}{
						"uid":      "u10",
						"sec_uid":  "sec_u10",
						"nickname": "Creator",
					},
					"video": map[string]interface{}{
						"cover": map[string]interface{}{
							"url_list": []string{"https://cover1.jpg"},
						},
						"play_addr": map[string]interface{}{
							"url_list": []string{"https://play.com/playwm/v1.mp4"},
						},
						"duration": 30000,
					},
					"statistics": map[string]interface{}{
						"digg_count":    50,
						"share_count":   10,
						"comment_count": 3,
					},
				},
				map[string]interface{}{
					"aweme_id":    "8002",
					"desc":        "video two",
					"create_time": 1700000001.0,
					"author": map[string]interface{}{
						"uid":      "u10",
						"sec_uid":  "sec_u10",
						"nickname": "Creator",
					},
					"video": map[string]interface{}{
						"cover": map[string]interface{}{
							"url_list": []string{"https://cover2.jpg"},
						},
						"play_addr": map[string]interface{}{
							"url_list": []string{"https://play.com/playwm/v2.mp4"},
						},
						"duration": 60000,
					},
					"statistics": map[string]interface{}{
						"digg_count":    200,
						"share_count":   40,
						"comment_count": 15,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	result, err := c.GetUserVideos("sec_u10", 0)
	if err != nil {
		t.Fatalf("GetUserVideos() error: %v", err)
	}
	if !result.HasMore {
		t.Error("HasMore = false, want true")
	}
	if result.MaxCursor != 1700000001 {
		t.Errorf("MaxCursor = %d, want 1700000001", result.MaxCursor)
	}
	if len(result.Videos) != 2 {
		t.Fatalf("Videos count = %d, want 2", len(result.Videos))
	}
	v := result.Videos[0]
	if v.AwemeID != "8001" {
		t.Errorf("Videos[0].AwemeID = %q, want %q", v.AwemeID, "8001")
	}
	if !strings.Contains(v.VideoURL, "play/v1.mp4") {
		t.Errorf("VideoURL = %q, expected 'playwm' replaced", v.VideoURL)
	}
}

func TestGetUserVideos_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status_code": 0,
			"has_more":    0,
			"max_cursor":  0,
			"aweme_list":  []interface{}{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	result, err := c.GetUserVideos("sec_empty", 0)
	if err != nil {
		t.Fatalf("GetUserVideos() error: %v", err)
	}
	if result.HasMore {
		t.Error("HasMore = true, want false")
	}
	if len(result.Videos) != 0 {
		t.Errorf("Videos count = %d, want 0", len(result.Videos))
	}
}

func TestGetUserVideos_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetUserVideos("sec_bad", 0)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
	if !strings.Contains(err.Error(), "empty body") {
		t.Errorf("error = %q, expected 'empty body'", err)
	}
}

func TestGetUserVideos_HTTP429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetUserVideos("sec_rate", 0)
	if err == nil {
		t.Fatal("expected error for 429, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, expected to mention 429", err)
	}
}

func TestGetUserVideos_WithImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status_code": 0,
			"has_more":    0,
			"max_cursor":  0,
			"aweme_list": []interface{}{
				map[string]interface{}{
					"aweme_id":    "8010",
					"desc":        "a note post",
					"create_time": 1700000000.0,
					"author": map[string]interface{}{
						"uid":      "u20",
						"sec_uid":  "sec_u20",
						"nickname": "NoteCreator",
					},
					"video": map[string]interface{}{
						"cover": map[string]interface{}{
							"url_list": []string{"https://cover.jpg"},
						},
						"play_addr": map[string]interface{}{
							"url_list": []string{"https://play.com/playwm/v.mp4"},
						},
					},
					"images": []interface{}{
						map[string]interface{}{
							"url_list": []string{"https://img1.jpg"},
						},
						map[string]interface{}{
							"url_list": []string{"https://img2.jpg"},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	result, err := c.GetUserVideos("sec_u20", 0)
	if err != nil {
		t.Fatalf("GetUserVideos() error: %v", err)
	}
	if len(result.Videos) != 1 {
		t.Fatalf("Videos count = %d, want 1", len(result.Videos))
	}
	v := result.Videos[0]
	if !v.IsNote {
		t.Error("expected IsNote=true for post with images")
	}
	if v.VideoURL != "" {
		t.Errorf("VideoURL = %q, want empty for note", v.VideoURL)
	}
	if len(v.Images) != 2 {
		t.Errorf("Images count = %d, want 2", len(v.Images))
	}
}

func TestGetUserVideos_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetUserVideos("sec_bad_json", 0)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// =====================================================================
// GetUserProfile tests
// =====================================================================

func TestGetUserProfile_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status_code": 0,
			"user": map[string]interface{}{
				"uid":              "uid100",
				"sec_uid":          "sec_uid100",
				"short_id":         "123456",
				"unique_id":        "testuser",
				"nickname":         "TestNick",
				"signature":        "hello world",
				"follower_count":   10000,
				"following_count":  500,
				"total_favorited":  50000,
				"aweme_count":      200,
				"favoriting_count": 300,
				"ip_location":      "北京",
				"avatar_larger": map[string]interface{}{
					"url_list": []string{"https://avatar-large.jpg"},
				},
				"avatar_medium": map[string]interface{}{
					"url_list": []string{"https://avatar-medium.jpg"},
				},
				"avatar_thumb": map[string]interface{}{
					"url_list": []string{"https://avatar-thumb.jpg"},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	profile, err := c.GetUserProfile("sec_uid100")
	if err != nil {
		t.Fatalf("GetUserProfile() error: %v", err)
	}
	if profile.UID != "uid100" {
		t.Errorf("UID = %q, want %q", profile.UID, "uid100")
	}
	if profile.Nickname != "TestNick" {
		t.Errorf("Nickname = %q, want %q", profile.Nickname, "TestNick")
	}
	if profile.UniqueID != "testuser" {
		t.Errorf("UniqueID = %q, want %q", profile.UniqueID, "testuser")
	}
	if profile.FollowerCount != 10000 {
		t.Errorf("FollowerCount = %d, want 10000", profile.FollowerCount)
	}
	if profile.AwemeCount != 200 {
		t.Errorf("AwemeCount = %d, want 200", profile.AwemeCount)
	}
	if profile.AvatarURL != "https://avatar-large.jpg" {
		t.Errorf("AvatarURL = %q, want large avatar", profile.AvatarURL)
	}
	if profile.IPLocation != "北京" {
		t.Errorf("IPLocation = %q, want 北京", profile.IPLocation)
	}
}

func TestGetUserProfile_AvatarFallbackMedium(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status_code": 0,
			"user": map[string]interface{}{
				"uid":      "uid200",
				"sec_uid":  "sec_uid200",
				"nickname": "MediumAvatar",
				"avatar_larger": map[string]interface{}{
					"url_list": []string{},
				},
				"avatar_medium": map[string]interface{}{
					"url_list": []string{"https://avatar-medium.jpg"},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	profile, err := c.GetUserProfile("sec_uid200")
	if err != nil {
		t.Fatalf("GetUserProfile() error: %v", err)
	}
	if profile.AvatarURL != "https://avatar-medium.jpg" {
		t.Errorf("AvatarURL = %q, want medium fallback", profile.AvatarURL)
	}
}

func TestGetUserProfile_AvatarFallbackThumb(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status_code": 0,
			"user": map[string]interface{}{
				"uid":      "uid300",
				"sec_uid":  "sec_uid300",
				"nickname": "ThumbAvatar",
				"avatar_larger": map[string]interface{}{
					"url_list": []string{},
				},
				"avatar_medium": map[string]interface{}{
					"url_list": []string{},
				},
				"avatar_thumb": map[string]interface{}{
					"url_list": []string{"https://avatar-thumb.jpg"},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	profile, err := c.GetUserProfile("sec_uid300")
	if err != nil {
		t.Fatalf("GetUserProfile() error: %v", err)
	}
	if profile.AvatarURL != "https://avatar-thumb.jpg" {
		t.Errorf("AvatarURL = %q, want thumb fallback", profile.AvatarURL)
	}
}

func TestGetUserProfile_NonZeroStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status_code": 2053,
			"user":        map[string]interface{}{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetUserProfile("sec_risk")
	if err == nil {
		t.Fatal("expected error for non-zero status_code, got nil")
	}
	if !strings.Contains(err.Error(), "status_code=2053") {
		t.Errorf("error = %q, expected status_code=2053", err)
	}
}

func TestGetUserProfile_HTTP403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("forbidden"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetUserProfile("sec_forbidden")
	if err == nil {
		t.Fatal("expected error for 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, expected 403", err)
	}
}

func TestGetUserProfile_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{invalid"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetUserProfile("sec_bad")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// =====================================================================
// ResolveVideoURL tests
// =====================================================================

func TestResolveVideoURL_302Redirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://final-video-url.com/video.mp4")
		w.WriteHeader(302)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	resolved, err := c.ResolveVideoURL("https://play.com/playwm/video.mp4")
	if err != nil {
		t.Fatalf("ResolveVideoURL() error: %v", err)
	}
	if resolved != "https://final-video-url.com/video.mp4" {
		t.Errorf("resolved = %q, want final URL", resolved)
	}
}

func TestResolveVideoURL_301Redirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://moved-permanently.com/v.mp4")
		w.WriteHeader(301)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	resolved, err := c.ResolveVideoURL("https://original.com/v.mp4")
	if err != nil {
		t.Fatalf("ResolveVideoURL() error: %v", err)
	}
	if resolved != "https://moved-permanently.com/v.mp4" {
		t.Errorf("resolved = %q, want moved URL", resolved)
	}
}

func TestResolveVideoURL_NoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	original := "https://direct.com/video.mp4"
	resolved, err := c.ResolveVideoURL(original)
	if err != nil {
		t.Fatalf("ResolveVideoURL() error: %v", err)
	}
	if resolved != original {
		t.Errorf("resolved = %q, want original URL %q", resolved, original)
	}
}

func TestResolveVideoURL_EmptyURL(t *testing.T) {
	c := NewClient()
	defer c.Close()

	_, err := c.ResolveVideoURL("")
	if err == nil {
		t.Fatal("expected error for empty URL, got nil")
	}
	if !strings.Contains(err.Error(), "empty video url") {
		t.Errorf("error = %q, expected 'empty video url'", err)
	}
}

// =====================================================================
// ResolveShareURL tests
// =====================================================================

func TestResolveShareURL_LongVideoURL(t *testing.T) {
	c := NewClient()
	defer c.Close()

	result, err := c.ResolveShareURL("https://www.douyin.com/video/7234567890123456789")
	if err != nil {
		t.Fatalf("ResolveShareURL() error: %v", err)
	}
	if result.Type != URLTypeVideo {
		t.Errorf("Type = %d, want URLTypeVideo", result.Type)
	}
	if result.VideoID != "7234567890123456789" {
		t.Errorf("VideoID = %q, want %q", result.VideoID, "7234567890123456789")
	}
}

func TestResolveShareURL_LongUserURL(t *testing.T) {
	c := NewClient()
	defer c.Close()

	result, err := c.ResolveShareURL("https://www.douyin.com/user/MS4wLjABAAAAxyz123")
	if err != nil {
		t.Fatalf("ResolveShareURL() error: %v", err)
	}
	if result.Type != URLTypeUser {
		t.Errorf("Type = %d, want URLTypeUser", result.Type)
	}
	if result.SecUID != "MS4wLjABAAAAxyz123" {
		t.Errorf("SecUID = %q, want %q", result.SecUID, "MS4wLjABAAAAxyz123")
	}
}

func TestResolveShareURL_ShortURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://www.douyin.com/video/7234567890123456789?extra=1")
		w.WriteHeader(302)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	result, err := c.ResolveShareURL("https://v.douyin.com/abc123")
	if err != nil {
		t.Fatalf("ResolveShareURL() error: %v", err)
	}
	if result.Type != URLTypeVideo {
		t.Errorf("Type = %d, want URLTypeVideo", result.Type)
	}
	if result.VideoID != "7234567890123456789" {
		t.Errorf("VideoID = %q, want %q", result.VideoID, "7234567890123456789")
	}
}

func TestResolveShareURL_ShortURLNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Location header
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.ResolveShareURL("https://v.douyin.com/abc123")
	if err == nil {
		t.Fatal("expected error when no redirect, got nil")
	}
	if !strings.Contains(err.Error(), "no redirect") {
		t.Errorf("error = %q, expected 'no redirect'", err)
	}
}

func TestResolveShareURL_NoteURL(t *testing.T) {
	c := NewClient()
	defer c.Close()

	result, err := c.ResolveShareURL("https://www.douyin.com/note/7234567890123456789")
	if err != nil {
		t.Fatalf("ResolveShareURL() error: %v", err)
	}
	if result.Type != URLTypeVideo {
		t.Errorf("Type = %d, want URLTypeVideo", result.Type)
	}
	if result.VideoID != "7234567890123456789" {
		t.Errorf("VideoID = %q, want %q", result.VideoID, "7234567890123456789")
	}
}

func TestResolveShareURL_WithoutHTTPS(t *testing.T) {
	c := NewClient()
	defer c.Close()

	result, err := c.ResolveShareURL("www.douyin.com/video/7234567890123456789")
	if err != nil {
		t.Fatalf("ResolveShareURL() error: %v", err)
	}
	if result.Type != URLTypeVideo {
		t.Errorf("Type = %d, want URLTypeVideo", result.Type)
	}
}

func TestResolveShareURL_IesDouyin(t *testing.T) {
	c := NewClient()
	defer c.Close()

	result, err := c.ResolveShareURL("https://www.iesdouyin.com/share/video/7234567890123456789")
	if err != nil {
		t.Fatalf("ResolveShareURL() error: %v", err)
	}
	if result.Type != URLTypeVideo {
		t.Errorf("Type = %d, want URLTypeVideo", result.Type)
	}
	if result.VideoID != "7234567890123456789" {
		t.Errorf("VideoID = %q, want %q", result.VideoID, "7234567890123456789")
	}
}

// =====================================================================
// parseAwemeDetail edge cases
// =====================================================================

func TestParseAwemeDetail_StatisticsFields(t *testing.T) {
	raw := `{
		"aweme_id": "9002",
		"desc": "stats",
		"create_time": 1700000000,
		"author": {"uid": "a2"},
		"video": {},
		"statistics": {
			"digg_count": 999,
			"share_count": 50,
			"comment_count": 25
		}
	}`
	video, err := parseAwemeDetail([]byte(raw), "9002", false)
	if err != nil {
		t.Fatalf("parseAwemeDetail() error: %v", err)
	}
	if video.DiggCount != 999 {
		t.Errorf("DiggCount = %d, want 999", video.DiggCount)
	}
	if video.ShareCount != 50 {
		t.Errorf("ShareCount = %d, want 50", video.ShareCount)
	}
	if video.CommentCount != 25 {
		t.Errorf("CommentCount = %d, want 25", video.CommentCount)
	}
}

func TestParseAwemeDetail_AuthorAvatarThumb(t *testing.T) {
	raw := `{
		"aweme_id": "9003",
		"author": {
			"uid": "a3",
			"sec_uid": "sa3",
			"nickname": "WithAvatar",
			"avatar_thumb": {
				"url_list": ["https://thumb.jpg"]
			}
		},
		"video": {}
	}`
	video, err := parseAwemeDetail([]byte(raw), "9003", false)
	if err != nil {
		t.Fatalf("parseAwemeDetail() error: %v", err)
	}
	if video.Author.AvatarURL != "https://thumb.jpg" {
		t.Errorf("Author.AvatarURL = %q, want thumb URL", video.Author.AvatarURL)
	}
}

// =====================================================================
// GetMixVideos tests
// =====================================================================

// TestGetMixVideos_TwoPagePagination 测试正常分页抓取（2页，has_more: true→false）
func TestGetMixVideos_TwoPagePagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/aweme/v1/web/mix/aweme") &&
			!strings.Contains(r.URL.RawQuery, "mix_id") {
			http.NotFound(w, r)
			return
		}
		page++
		var resp map[string]interface{}
		if page == 1 {
			resp = map[string]interface{}{
				"status_code": 0,
				"has_more":    1,
				"cursor":      100,
				"aweme_list": []interface{}{
					map[string]interface{}{
						"aweme_id":    "9001",
						"desc":        "mix video 1",
						"create_time": 1700000001.0,
						"author": map[string]interface{}{
							"uid":      "mu1",
							"sec_uid":  "sec_mu1",
							"nickname": "MixCreator",
						},
						"video": map[string]interface{}{
							"cover": map[string]interface{}{
								"url_list": []string{"https://mixcover1.jpg"},
							},
							"play_addr": map[string]interface{}{
								"url_list": []string{"https://play.com/playwm/mix1.mp4"},
							},
							"duration": 45000,
						},
						"statistics": map[string]interface{}{
							"digg_count":    300.0,
							"share_count":   30.0,
							"comment_count": 12.0,
						},
					},
				},
			}
		} else {
			resp = map[string]interface{}{
				"status_code": 0,
				"has_more":    0,
				"cursor":      200,
				"aweme_list": []interface{}{
					map[string]interface{}{
						"aweme_id":    "9002",
						"desc":        "mix video 2",
						"create_time": 1700000002.0,
						"author": map[string]interface{}{
							"uid":      "mu1",
							"sec_uid":  "sec_mu1",
							"nickname": "MixCreator",
						},
						"video": map[string]interface{}{
							"cover": map[string]interface{}{
								"url_list": []string{"https://mixcover2.jpg"},
							},
							"play_addr": map[string]interface{}{
								"url_list": []string{"https://play.com/playwm/mix2.mp4"},
							},
							"duration": 60000,
						},
						"statistics": map[string]interface{}{
							"digg_count":    150.0,
							"share_count":   15.0,
							"comment_count": 6.0,
						},
					},
				},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	videos, err := c.GetMixVideos("mix12345")
	if err != nil {
		t.Fatalf("GetMixVideos() error: %v", err)
	}
	if len(videos) != 2 {
		t.Fatalf("videos count = %d, want 2", len(videos))
	}
	if videos[0].AwemeID != "9001" {
		t.Errorf("videos[0].AwemeID = %q, want %q", videos[0].AwemeID, "9001")
	}
	if videos[1].AwemeID != "9002" {
		t.Errorf("videos[1].AwemeID = %q, want %q", videos[1].AwemeID, "9002")
	}
	if !strings.Contains(videos[0].VideoURL, "play/mix1.mp4") {
		t.Errorf("videos[0].VideoURL = %q, expected playwm replaced with play", videos[0].VideoURL)
	}
	if videos[0].Author.Nickname != "MixCreator" {
		t.Errorf("Author.Nickname = %q, want MixCreator", videos[0].Author.Nickname)
	}
}

// TestGetMixVideos_Empty 测试空合集（has_more: 0, aweme_list: []）
func TestGetMixVideos_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status_code": 0,
			"has_more":    0,
			"cursor":      0,
			"aweme_list":  []interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	videos, err := c.GetMixVideos("empty_mix")
	if err != nil {
		t.Fatalf("GetMixVideos() error: %v", err)
	}
	if len(videos) != 0 {
		t.Errorf("expected 0 videos for empty mix, got %d", len(videos))
	}
}

// TestGetMixVideos_HTTPError 测试 HTTP 错误处理
func TestGetMixVideos_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetMixVideos("mix_error")
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, expected to mention 500", err)
	}
}

// TestGetMixVideos_InvalidJSON 测试非法 JSON 响应
func TestGetMixVideos_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not valid json"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetMixVideos("mix_bad_json")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
