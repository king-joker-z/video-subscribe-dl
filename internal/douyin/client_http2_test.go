package douyin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =====================================================================
// getNoteDetail tests (via GetVideoDetail page scrape path)
// =====================================================================

func TestGetNoteDetail_DirectCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The note API uses iesdouyin.com/web/api/v2/aweme/slidesinfo/
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			// API path: return null aweme_detail to force page scrape
			resp := map[string]interface{}{
				"status_code":  0,
				"aweme_detail": nil,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if strings.Contains(r.URL.Path, "slidesinfo") {
			resp := map[string]interface{}{
				"aweme_details": []interface{}{
					map[string]interface{}{
						"aweme_id":    "7100",
						"desc":        "my beautiful note",
						"create_time": 1700000000.0,
						"aweme_type":  68,
						"author": map[string]interface{}{
							"uid":      "nu1",
							"sec_uid":  "sec_nu1",
							"nickname": "NoteAuthor",
						},
						"video": map[string]interface{}{},
						"images": []interface{}{
							map[string]interface{}{
								"url_list": []interface{}{"https://note-img1.jpg"},
							},
							map[string]interface{}{
								"url_list": []interface{}{"https://note-img2.jpg"},
							},
							map[string]interface{}{
								"url_list": []interface{}{"https://note-img3.jpg"},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		// Page scrape: return HTML with canonical /note/ link to trigger getNoteDetail
		html := `<!DOCTYPE html><html><head>
			<link rel="canonical" href="https://www.douyin.com/note/7100">
			</head><body>` + strings.Repeat("x", 6000) + `</body></html>`
		w.Write([]byte(html))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	video, err := c.GetVideoDetail("7100")
	if err != nil {
		t.Fatalf("GetVideoDetail(note) error: %v", err)
	}
	if !video.IsNote {
		t.Error("expected IsNote=true")
	}
	if video.Desc != "my beautiful note" {
		t.Errorf("Desc = %q, want %q", video.Desc, "my beautiful note")
	}
	if len(video.Images) != 3 {
		t.Errorf("Images count = %d, want 3", len(video.Images))
	}
	if video.Author.Nickname != "NoteAuthor" {
		t.Errorf("Author.Nickname = %q, want %q", video.Author.Nickname, "NoteAuthor")
	}
}

func TestGetNoteDetail_EmptyAwemeDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			resp := map[string]interface{}{
				"status_code":  0,
				"aweme_detail": nil,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if strings.Contains(r.URL.Path, "slidesinfo") {
			resp := map[string]interface{}{
				"aweme_details": []interface{}{},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		html := `<!DOCTYPE html><html><head>
			<link rel="canonical" href="https://www.douyin.com/note/7101">
			</head><body>` + strings.Repeat("x", 6000) + `</body></html>`
		w.Write([]byte(html))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetVideoDetail("7101")
	if err == nil {
		t.Fatal("expected error for empty aweme_details, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, expected 'not found'", err)
	}
}

func TestGetNoteDetail_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			resp := map[string]interface{}{
				"status_code":  0,
				"aweme_detail": nil,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if strings.Contains(r.URL.Path, "slidesinfo") {
			w.Write([]byte("not json"))
			return
		}
		html := `<!DOCTYPE html><html><head>
			<link rel="canonical" href="https://www.douyin.com/note/7102">
			</head><body>` + strings.Repeat("x", 6000) + `</body></html>`
		w.Write([]byte(html))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetVideoDetail("7102")
	if err == nil {
		t.Fatal("expected error for invalid JSON in note API, got nil")
	}
}

func TestGetNoteDetail_ViaOgURL(t *testing.T) {
	// Test note detection via og:url meta tag (not canonical)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			resp := map[string]interface{}{
				"status_code":  0,
				"aweme_detail": nil,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if strings.Contains(r.URL.Path, "slidesinfo") {
			resp := map[string]interface{}{
				"aweme_details": []interface{}{
					map[string]interface{}{
						"aweme_id":   "7103",
						"desc":       "og note",
						"aweme_type": 68,
						"author":     map[string]interface{}{"uid": "u1"},
						"video":      map[string]interface{}{},
						"images": []interface{}{
							map[string]interface{}{
								"url_list": []interface{}{"https://og-img.jpg"},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		// No canonical, but og:url contains /note/
		html := `<!DOCTYPE html><html><head>
			<meta property="og:url" content="https://www.douyin.com/note/7103">
			</head><body>` + strings.Repeat("x", 6000) + `</body></html>`
		w.Write([]byte(html))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	video, err := c.GetVideoDetail("7103")
	if err != nil {
		t.Fatalf("GetVideoDetail(og:url note) error: %v", err)
	}
	if !video.IsNote {
		t.Error("expected IsNote=true via og:url detection")
	}
}

// =====================================================================
// DownloadFile / DownloadThumb tests
// =====================================================================

func TestDownloadFile_Success(t *testing.T) {
	content := "fake video data here 1234567890"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write([]byte(content))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "sub", "video.mp4")

	written, err := DownloadFile(srv.URL+"/video.mp4", dest)
	if err != nil {
		t.Fatalf("DownloadFile() error: %v", err)
	}
	if written != int64(len(content)) {
		t.Errorf("written = %d, want %d", written, len(content))
	}

	// Verify file content
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if string(data) != content {
		t.Errorf("file content = %q, want %q", string(data), content)
	}
}

func TestDownloadFile_SkipExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called for existing file")
		w.Write([]byte("new data"))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "existing.mp4")
	os.WriteFile(dest, []byte("old data"), 0644)

	written, err := DownloadFile(srv.URL+"/video.mp4", dest)
	if err != nil {
		t.Fatalf("DownloadFile() error: %v", err)
	}
	if written != 8 { // len("old data")
		t.Errorf("written = %d, want 8 (existing file size)", written)
	}

	// Verify old content preserved
	data, _ := os.ReadFile(dest)
	if string(data) != "old data" {
		t.Errorf("file content changed to %q", string(data))
	}
}

func TestDownloadFile_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "notfound.mp4")

	_, err := DownloadFile(srv.URL+"/missing.mp4", dest)
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, expected 404", err)
	}
}

func TestDownloadFile_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		// Empty body
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "empty.mp4")

	_, err := DownloadFile(srv.URL+"/empty.mp4", dest)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
	if !strings.Contains(err.Error(), "0 bytes") {
		t.Errorf("error = %q, expected '0 bytes'", err)
	}
}

func TestDownloadThumb_Success(t *testing.T) {
	content := "fake jpeg thumbnail data"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte(content))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "thumb.jpg")

	err := DownloadThumb(srv.URL+"/thumb.jpg", dest)
	if err != nil {
		t.Fatalf("DownloadThumb() error: %v", err)
	}

	data, _ := os.ReadFile(dest)
	if string(data) != content {
		t.Errorf("thumb content = %q, want %q", string(data), content)
	}
}

func TestDownloadThumb_SkipExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called for existing thumb")
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "existing.jpg")
	os.WriteFile(dest, []byte("old thumb"), 0644)

	err := DownloadThumb(srv.URL+"/thumb.jpg", dest)
	if err != nil {
		t.Fatalf("DownloadThumb() error: %v", err)
	}

	data, _ := os.ReadFile(dest)
	if string(data) != "old thumb" {
		t.Errorf("thumb content changed to %q", string(data))
	}
}

func TestDownloadThumb_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "fail.jpg")

	err := DownloadThumb(srv.URL+"/fail.jpg", dest)
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, expected 500", err)
	}
}

// =====================================================================
// getCookieString (without user cookie) + diag tests
// =====================================================================

func TestGetCookieString_AutoGenerate(t *testing.T) {
	// When no user cookie is set, getCookieString should auto-generate
	// We need a server that can respond to the ttwid API
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate ttwid API response
		http.SetCookie(w, &http.Cookie{
			Name:  "ttwid",
			Value: "test_ttwid_value_12345",
		})
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	// Save and restore global cookie state
	old := globalCookieMgr.GetUserCookie()
	globalCookieMgr.SetUserCookie("")
	defer globalCookieMgr.SetUserCookie(old)

	// Reset cached ttwid
	globalCookieMgr.mu.Lock()
	globalCookieMgr.ttwid = ""
	globalCookieMgr.mu.Unlock()

	c := newTestClient(srv.URL)
	defer c.Close()

	cookie := c.GetCookieString()
	if cookie == "" {
		t.Fatal("GetCookieString() returned empty string")
	}
	if !strings.Contains(cookie, "msToken=") {
		t.Error("cookie missing msToken field")
	}
	if !strings.Contains(cookie, "odin_tt=") {
		t.Error("cookie missing odin_tt field")
	}
	if !strings.Contains(cookie, "verify_fp=") {
		t.Error("cookie missing verify_fp field")
	}
	if !strings.Contains(cookie, "s_v_web_id=") {
		t.Error("cookie missing s_v_web_id field")
	}
}

func TestGetCookieString_WithUserCookie(t *testing.T) {
	old := globalCookieMgr.GetUserCookie()
	globalCookieMgr.SetUserCookie("my_custom_cookie=abc123; sessionid=xyz")
	defer globalCookieMgr.SetUserCookie(old)

	c := NewClient()
	defer c.Close()

	cookie := c.GetCookieString()
	if cookie != "my_custom_cookie=abc123; sessionid=xyz" {
		t.Errorf("cookie = %q, want user cookie", cookie)
	}
}

func TestTestXBogusSign(t *testing.T) {
	ok := TestXBogusSign()
	if !ok {
		t.Error("TestXBogusSign() returned false, sign engine not working")
	}
}

// =====================================================================
// GetSignPoolStats / GetABogusPoolStats tests
// =====================================================================

func TestGetSignPoolStats(t *testing.T) {
	stats := GetSignPoolStats()
	if stats == nil {
		t.Fatal("GetSignPoolStats() returned nil")
	}
	if stats.Size <= 0 {
		t.Errorf("stats.Size = %d, want > 0", stats.Size)
	}
}

func TestGetABogusPoolStats(t *testing.T) {
	stats := GetABogusPoolStats()
	if stats == nil {
		t.Fatal("GetABogusPoolStats() returned nil")
	}
	if stats.Size <= 0 {
		t.Errorf("stats.Size = %d, want > 0", stats.Size)
	}
}

// =====================================================================
// getVideoDetailPage tests (ROUTER_DATA parsing via page scrape)
// =====================================================================

func TestGetVideoDetailPage_RouterData(t *testing.T) {
	routerData := map[string]interface{}{
		"loaderData": map[string]interface{}{
			"video_(id)/page": map[string]interface{}{
				"videoInfoRes": map[string]interface{}{
					"status_code": 0,
					"item_list": []interface{}{
						map[string]interface{}{
							"aweme_id":    "7200",
							"desc":        "page scrape video",
							"create_time": 1700000000.0,
							"author": map[string]interface{}{
								"uid":      "pu1",
								"nickname": "PageAuthor",
							},
							"video": map[string]interface{}{
								"cover": map[string]interface{}{
									"url_list": []interface{}{"https://pagecover.jpg"},
								},
								"play_addr": map[string]interface{}{
									"url_list": []interface{}{"https://play.com/playwm/page.mp4"},
								},
								"duration": 20000,
							},
							"statistics": map[string]interface{}{
								"digg_count": 500.0,
							},
						},
					},
				},
			},
		},
	}
	routerJSON, _ := json.Marshal(routerData)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			// API fails
			w.WriteHeader(500)
			w.Write([]byte("error"))
			return
		}
		// Page scrape with _ROUTER_DATA
		html := `<!DOCTYPE html><html><head></head><body>
			<script>window._ROUTER_DATA = ` + string(routerJSON) + `</script>
			` + strings.Repeat("x", 6000) + `</body></html>`
		w.Write([]byte(html))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	video, err := c.GetVideoDetail("7200")
	if err != nil {
		t.Fatalf("GetVideoDetail(page scrape) error: %v", err)
	}
	if video.AwemeID != "7200" {
		t.Errorf("AwemeID = %q, want %q", video.AwemeID, "7200")
	}
	if video.Desc != "page scrape video" {
		t.Errorf("Desc = %q, want %q", video.Desc, "page scrape video")
	}
	if video.Author.Nickname != "PageAuthor" {
		t.Errorf("Author.Nickname = %q, want %q", video.Author.Nickname, "PageAuthor")
	}
	if video.DiggCount != 500 {
		t.Errorf("DiggCount = %d, want 500", video.DiggCount)
	}
}

func TestGetVideoDetailPage_RiskControlStatusCode(t *testing.T) {
	routerData := map[string]interface{}{
		"loaderData": map[string]interface{}{
			"video_(id)/page": map[string]interface{}{
				"videoInfoRes": map[string]interface{}{
					"status_code": 2053.0,
					"status_msg":  "IP risk control",
					"item_list":   []interface{}{},
				},
			},
		},
	}
	routerJSON, _ := json.Marshal(routerData)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			w.WriteHeader(500)
			return
		}
		html := `<!DOCTYPE html><html><head></head><body>
			<script>window._ROUTER_DATA = ` + string(routerJSON) + `</script>
			` + strings.Repeat("x", 6000) + `</body></html>`
		w.Write([]byte(html))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetVideoDetail("7201")
	if err == nil {
		t.Fatal("expected error for risk control status_code in page, got nil")
	}
	if !strings.Contains(err.Error(), "risk control") {
		t.Errorf("error = %q, expected 'risk control'", err)
	}
}

func TestGetVideoDetailPage_FilterList(t *testing.T) {
	routerData := map[string]interface{}{
		"loaderData": map[string]interface{}{
			"video_(id)/page": map[string]interface{}{
				"videoInfoRes": map[string]interface{}{
					"status_code": 0,
					"item_list":   []interface{}{},
					"filter_list": []interface{}{
						map[string]interface{}{
							"aweme_id":      "7202",
							"filter_reason": "video_deleted",
							"filter_type":   1.0,
						},
					},
				},
			},
		},
	}
	routerJSON, _ := json.Marshal(routerData)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aweme/v1/web/aweme/detail") {
			w.WriteHeader(500)
			return
		}
		html := `<!DOCTYPE html><html><head></head><body>
			<script>window._ROUTER_DATA = ` + string(routerJSON) + `</script>
			` + strings.Repeat("x", 6000) + `</body></html>`
		w.Write([]byte(html))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	_, err := c.GetVideoDetail("7202")
	if err == nil {
		t.Fatal("expected error for filtered video, got nil")
	}
	if !strings.Contains(err.Error(), "filtered") {
		t.Errorf("error = %q, expected 'filtered'", err)
	}
}

// =====================================================================
// ValidateCookie test
// =====================================================================

func TestValidateCookie_NoTTWID(t *testing.T) {
	// When ttwid is missing from cookie, validation should fail
	old := globalCookieMgr.GetUserCookie()
	globalCookieMgr.SetUserCookie("msToken=abc123; odin_tt=xyz")
	defer globalCookieMgr.SetUserCookie(old)

	c := NewClient()
	defer c.Close()

	valid, msg := c.ValidateCookie()
	if valid {
		t.Error("expected invalid for cookie without ttwid")
	}
	if !strings.Contains(msg, "ttwid") {
		t.Errorf("msg = %q, expected to mention ttwid", msg)
	}
}

func TestValidateCookie_Empty(t *testing.T) {
	// Test with auto-generated cookie that has no ttwid (when fetch fails)
	old := globalCookieMgr.GetUserCookie()
	globalCookieMgr.SetUserCookie("")
	defer globalCookieMgr.SetUserCookie(old)

	// Clear ttwid cache so auto-gen doesn't have it
	globalCookieMgr.mu.Lock()
	savedTtwid := globalCookieMgr.ttwid
	globalCookieMgr.ttwid = ""
	globalCookieMgr.mu.Unlock()
	defer func() {
		globalCookieMgr.mu.Lock()
		globalCookieMgr.ttwid = savedTtwid
		globalCookieMgr.mu.Unlock()
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ttwid fetch fails
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	defer c.Close()

	valid, msg := c.ValidateCookie()
	if valid {
		t.Error("expected invalid when ttwid fetch fails")
	}
	_ = msg // just ensure no panic
}

// =====================================================================
// SetUserCookie edge cases
// =====================================================================

func TestSetUserCookie_CleanWhitespace(t *testing.T) {
	old := globalCookieMgr.GetUserCookie()
	defer globalCookieMgr.SetUserCookie(old)

	globalCookieMgr.SetUserCookie("  msToken=abc\r\n; ttwid=xyz\t; odin_tt=123  ")
	got := globalCookieMgr.GetUserCookie()

	if strings.Contains(got, "\r") || strings.Contains(got, "\n") {
		t.Errorf("cookie still contains newlines: %q", got)
	}
	if strings.Contains(got, "\t") {
		t.Errorf("cookie still contains tabs: %q", got)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Errorf("cookie has leading/trailing spaces: %q", got)
	}
}

func TestSetUserCookie_ClearCookie(t *testing.T) {
	old := globalCookieMgr.GetUserCookie()
	defer globalCookieMgr.SetUserCookie(old)

	globalCookieMgr.SetUserCookie("something")
	if globalCookieMgr.GetUserCookie() == "" {
		t.Error("cookie should be set")
	}

	globalCookieMgr.SetUserCookie("")
	if globalCookieMgr.GetUserCookie() != "" {
		t.Error("cookie should be cleared")
	}
}

// =====================================================================
// fetchTTWID test (via getCookieString path)
// =====================================================================

func TestFetchTTWID_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			// Read body to prevent connection reset
			io.ReadAll(r.Body)
			http.SetCookie(w, &http.Cookie{
				Name:  "ttwid",
				Value: "test_ttwid_abc",
			})
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	transport := &rewriteTransport{targetURL: srv.URL}
	client := &http.Client{Transport: transport}

	ttwid, err := fetchTTWID(client)
	if err != nil {
		t.Fatalf("fetchTTWID() error: %v", err)
	}
	if ttwid != "test_ttwid_abc" {
		t.Errorf("ttwid = %q, want %q", ttwid, "test_ttwid_abc")
	}
}

func TestFetchTTWID_NoSetCookie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	transport := &rewriteTransport{targetURL: srv.URL}
	client := &http.Client{Transport: transport}

	_, err := fetchTTWID(client)
	if err == nil {
		t.Fatal("expected error when no ttwid in response, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, expected 'not found'", err)
	}
}
