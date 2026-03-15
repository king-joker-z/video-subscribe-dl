package web

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"video-subscribe-dl/internal/bilibili"
)

// POST /api/scan — 扫描本地文件补录数据库 + 生成缺失 NFO
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}

	if s.scanner == nil {
		jsonError(w, "scanner not available", 500)
		return
	}

	go func() {
		log.Println("Starting local file scan...")
		scanned, nfoGenerated, err := s.scanner.ScanAndSync()
		if err != nil {
			log.Printf("Scan error: %v", err)
		} else {
			log.Printf("Scan complete: %d files scanned, %d NFOs generated", scanned, nfoGenerated)
		}
	}()

	jsonResponse(w, map[string]string{"status": "scanning"})
}

// GET/PUT /api/settings
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		// 返回所有设置
		settings := map[string]string{}
		keys := []string{"download_quality", "max_concurrent", "request_interval", "cookie_path", "nfo_type", "download_danmaku", "auth_token", "check_interval_minutes", "notify_type", "webhook_url", "telegram_bot_token", "telegram_chat_id", "bark_server", "bark_key", "notify_on_complete", "notify_on_error", "notify_on_cookie_expire", "notify_on_sync", "download_chunks", "max_download_speed_mb", "min_disk_free_gb"}
		for _, key := range keys {
			val, _ := s.db.GetSetting(key)
			if (key == "auth_token" || key == "telegram_bot_token" || key == "bark_key") && val != "" {
				settings[key] = "***" // 不返回明文敏感信息
			} else {
				settings[key] = val
			}
		}
		jsonResponse(w, settings)

	case "PUT", "POST":
		var settings map[string]string
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			jsonError(w, err.Error(), 400)
			return
		}
		for key, value := range settings {
			if err := s.db.SetSetting(key, value); err != nil {
				jsonError(w, err.Error(), 500)
				return
			}
		}
		// 刷新缓存的 rate limit 值
		if _, ok := settings["rate_limit_per_minute"]; ok {
			s.RefreshRateLimit()
		}
		jsonResponse(w, map[string]bool{"ok": true})

	default:
		jsonError(w, "method not allowed", 405)
	}
}

// GET/PUT /api/settings/{key}
func (s *Server) handleSettingByKey(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/api/settings/")
	if key == "" {
		jsonError(w, "key required", 400)
		return
	}

	switch r.Method {
	case "GET":
		val, err := s.db.GetSetting(key)
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		jsonResponse(w, map[string]string{key: val})

	case "PUT":
		var body struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, err.Error(), 400)
			return
		}
		if err := s.db.SetSetting(key, body.Value); err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})

	default:
		jsonError(w, "method not allowed", 405)
	}
}

// POST /api/cookie/upload - 上传 Cookie 文件
func (s *Server) handleCookieUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}

	r.ParseMultipartForm(10 << 20) // 10MB

	file, handler, err := r.FormFile("cookie")
	if err != nil {
		jsonError(w, "failed to read file: "+err.Error(), 400)
		return
	}
	defer file.Close()

	// 保存到 data 目录
	cookieDir := filepath.Join(s.dataDir, "cookies")
	os.MkdirAll(cookieDir, 0755)

	destPath := filepath.Join(cookieDir, handler.Filename)
	dest, err := os.Create(destPath)
	if err != nil {
		jsonError(w, "failed to save file: "+err.Error(), 500)
		return
	}
	defer dest.Close()

	written, err := io.Copy(dest, file)
	if err != nil {
		jsonError(w, "failed to write file: "+err.Error(), 500)
		return
	}

	// 保存路径到设置
	s.db.SetSetting("cookie_path", destPath)

	log.Printf("Cookie uploaded: %s (%d bytes)", destPath, written)

	// 通知 scheduler 更新 cookie (legacy)
	if s.onCookieUpdate != nil {
		s.onCookieUpdate(destPath)
	}

	// 同时解析为 Credential 存 DB（新鉴权模式）
	s.convertCookieToCredential(destPath)

	// 自动验证上传的 Cookie
	cookie := bilibili.ReadCookieFile(destPath)
	var verifyResult *bilibili.CookieVerifyResult
	if cookie != "" {
		client := bilibili.NewClient(cookie)
		verifyResult, _ = client.VerifyCookie()
	}

	resp := map[string]interface{}{
		"ok":   true,
		"path": destPath,
		"size": written,
	}

	if verifyResult != nil {
		resp["logged_in"] = verifyResult.LoggedIn
		resp["username"] = verifyResult.Username
		resp["vip_type"] = verifyResult.VIPType
		resp["vip_due_date"] = verifyResult.VIPDueDate
	} else {
		resp["logged_in"] = false
		resp["message"] = "Cookie 文件格式可能有误，但已保存"
	}

	jsonResponse(w, resp)
}

// GET /api/scan/status — 返回对账结果
func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.scanner == nil {
		jsonError(w, "scanner not available", 500)
		return
	}
	result, err := s.scanner.Reconcile()
	if err != nil {
		jsonError(w, "reconcile failed: "+err.Error(), 500)
		return
	}
	jsonResponse(w, result)
}

// POST /api/scan/fix — 执行对账修复
func (s *Server) handleScanFix(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.scanner == nil {
		jsonError(w, "scanner not available", 500)
		return
	}

	// 先对账
	reconcileResult, err := s.scanner.Reconcile()
	if err != nil {
		jsonError(w, "reconcile failed: "+err.Error(), 500)
		return
	}

	if reconcileResult.IsConsistent {
		jsonResponse(w, map[string]interface{}{
			"ok":      true,
			"message": "数据已一致，无需修复",
		})
		return
	}

	// 执行修复
	fixResult, err := s.scanner.Fix(reconcileResult)
	if err != nil {
		jsonError(w, "fix failed: "+err.Error(), 500)
		return
	}

	log.Printf("[Reconcile] Fix complete: orphans=%d, missing=%d, stale=%d",
		fixResult.OrphansFixed, fixResult.MissingMarked, fixResult.StaleReset)

	jsonResponse(w, map[string]interface{}{
		"ok":     true,
		"result": fixResult,
	})
}

// GET /api/cookie/verify - 验证当前 Cookie 状态
func (s *Server) handleCookieVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	// 优先使用 Credential 验证
	if credJSON, _ := s.db.GetSetting("credential_json"); credJSON != "" {
		cred := bilibili.CredentialFromJSON(credJSON)
		if cred != nil && !cred.IsEmpty() {
			client := bilibili.NewClientWithCredential(cred)
			result, err := client.VerifyCookie()
			if err != nil {
				jsonResponse(w, map[string]interface{}{"ok": false, "error": err.Error()})
				return
			}
			jsonResponse(w, map[string]interface{}{
				"ok": true, "logged_in": result.LoggedIn,
				"username": result.Username, "vip_type": result.VIPType,
				"vip_status": result.VIPStatus, "vip_active": result.VIPActive,
				"vip_due_date": result.VIPDueDate, "vip_label": result.VIPLabel,
				"max_quality": result.MaxQuality, "max_audio": result.MaxAudio,
			})
			return
		}
	}

	cookiePath, err := s.db.GetSetting("cookie_path")
	if err != nil {
		log.Printf("[WARN] Failed to get cookie_path from DB: %v", err)
	}
	if cookiePath == "" {
		jsonResponse(w, map[string]interface{}{
			"ok":        true,
			"logged_in": false,
			"message":   "未配置 Cookie 文件",
		})
		return
	}

	cookie := bilibili.ReadCookieFile(cookiePath)
	if cookie == "" {
		jsonResponse(w, map[string]interface{}{
			"ok":        true,
			"logged_in": false,
			"message":   "Cookie 文件为空或格式错误",
		})
		return
	}

	client := bilibili.NewClient(cookie)
	result, err := client.VerifyCookie()
	if err != nil {
		jsonResponse(w, map[string]interface{}{
			"ok":      false,
			"error":   err.Error(),
			"message": "验证请求失败",
		})
		return
	}

	jsonResponse(w, map[string]interface{}{
		"ok":           true,
		"logged_in":    result.LoggedIn,
		"username":     result.Username,
		"vip_type":     result.VIPType,
		"vip_due_date": result.VIPDueDate,
	})
}
