package web

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"video-subscribe-dl/internal/bilibili"
)

// POST /api/login/qrcode/generate — 生成扫码登录二维码
func (s *Server) handleQRCodeGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	result, err := bilibili.GenerateQRCode(httpClient)
	if err != nil {
		log.Printf("[qrcode] Generate failed: %v", err)
		jsonError(w, "生成二维码失败: "+err.Error(), 500)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"ok":          true,
		"url":         result.URL,
		"qrcode_key":  result.QRCodeKey,
	})
}

// GET /api/login/qrcode/poll?qrcode_key=xxx — 轮询扫码状态
func (s *Server) handleQRCodePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	qrcodeKey := r.URL.Query().Get("qrcode_key")
	if qrcodeKey == "" {
		jsonError(w, "qrcode_key required", 400)
		return
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	result, err := bilibili.PollQRCode(httpClient, qrcodeKey)
	if err != nil {
		log.Printf("[qrcode] Poll failed: %v", err)
		jsonError(w, "轮询失败: "+err.Error(), 500)
		return
	}

	resp := map[string]interface{}{
		"ok":      true,
		"status":  result.Status,
		"message": result.Message,
	}

	if result.Status == bilibili.QRSuccess && result.Credential != nil {
		// 登录成功，保存凭证到 DB
		cred := result.Credential
		if err := s.db.SetSetting("credential_json", cred.ToJSON()); err != nil {
			log.Printf("[qrcode] Save credential failed: %v", err)
			jsonError(w, "保存凭证失败", 500)
			return
		}
		if err := s.db.SetSetting("credential_source", "qrcode"); err != nil {
			log.Printf("[qrcode] Save credential source failed: %v", err)
		}

		// 通知 scheduler 更新凭证
		if s.onCredentialUpdate != nil {
			s.onCredentialUpdate(cred)
		}

		// 验证并返回用户信息
		verifyResult, _ := bilibili.VerifyCredential(cred, httpClient)
		if verifyResult != nil && verifyResult.LoggedIn {
			resp["username"] = verifyResult.Username
			resp["vip_label"] = verifyResult.VIPLabel
			resp["max_quality"] = verifyResult.MaxQuality
			resp["max_audio"] = verifyResult.MaxAudio
		}

		log.Printf("[qrcode] 扫码登录成功: user=%s", resp["username"])
	}

	jsonResponse(w, resp)
}

// GET /api/credential/status — 当前凭证状态
func (s *Server) handleCredentialStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, "method not allowed", 405)
		return
	}

	credJSON, _ := s.db.GetSetting("credential_json")
	credSource, _ := s.db.GetSetting("credential_source")
	cred := bilibili.CredentialFromJSON(credJSON)

	if cred == nil || cred.IsEmpty() {
		jsonResponse(w, &bilibili.CredentialStatus{
			HasCredential: false,
			Source:        "none",
		})
		return
	}

	status := &bilibili.CredentialStatus{
		HasCredential: true,
		Source:        credSource,
	}
	if cred.UpdatedAt > 0 {
		status.UpdatedAt = time.Unix(cred.UpdatedAt, 0).Format("2006-01-02 15:04:05")
	}

	// 检查是否需要刷新
	httpClient := &http.Client{Timeout: 15 * time.Second}
	needRefresh, err := cred.NeedRefresh(httpClient)
	if err != nil {
		log.Printf("[credential] NeedRefresh check error: %v", err)
	}
	status.NeedRefresh = needRefresh

	// 获取用户信息
	verifyResult, err := bilibili.VerifyCredential(cred, httpClient)
	if err != nil {
		log.Printf("[credential] Verify error: %v", err)
	}
	if verifyResult != nil {
		if !verifyResult.LoggedIn {
			status.HasCredential = true
			status.NeedRefresh = true
		} else {
			status.Username = verifyResult.Username
			status.VIPLabel = verifyResult.VIPLabel
			status.MaxQuality = verifyResult.MaxQuality
			status.MaxAudio = verifyResult.MaxAudio
		}
	}

	jsonResponse(w, status)
}

// POST /api/credential/refresh — 手动触发凭证刷新
func (s *Server) handleCredentialRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}

	credJSON, _ := s.db.GetSetting("credential_json")
	cred := bilibili.CredentialFromJSON(credJSON)
	if cred == nil || cred.IsEmpty() {
		jsonError(w, "无凭证可刷新", 400)
		return
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	newCred, err := cred.Refresh(httpClient)
	if err != nil {
		log.Printf("[credential] Manual refresh failed: %v", err)
		jsonError(w, "刷新失败: "+err.Error(), 500)
		return
	}

	// 保存到 DB
	if err := s.db.SetSetting("credential_json", newCred.ToJSON()); err != nil {
		jsonError(w, "保存刷新后凭证失败", 500)
		return
	}

	// 通知 scheduler
	if s.onCredentialUpdate != nil {
		s.onCredentialUpdate(newCred)
	}

	jsonResponse(w, map[string]interface{}{
		"ok":      true,
		"message": "凭证刷新成功",
	})
}

// POST /api/credential/clear — 清除凭证
func (s *Server) handleCredentialClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}

	s.db.SetSetting("credential_json", "")
	s.db.SetSetting("credential_source", "")

	log.Printf("[credential] Credential cleared by user")
	jsonResponse(w, map[string]interface{}{
		"ok":      true,
		"message": "凭证已清除",
	})
}

// handleCookieUploadV2 增强版 cookie 上传：上传后解析为 Credential 存 DB
func (s *Server) convertCookieToCredential(cookiePath string) {
	cred := bilibili.CredentialFromCookieFile(cookiePath)
	if cred == nil {
		log.Printf("[cookie-upload] Could not parse cookie file to Credential")
		return
	}

	// 存储到 DB
	if err := s.db.SetSetting("credential_json", cred.ToJSON()); err != nil {
		log.Printf("[cookie-upload] Save credential failed: %v", err)
		return
	}
	if err := s.db.SetSetting("credential_source", "cookie_file"); err != nil {
		log.Printf("[cookie-upload] Save credential source failed: %v", err)
	}

	// 通知 scheduler 更新
	if s.onCredentialUpdate != nil {
		s.onCredentialUpdate(cred)
	}

	log.Printf("[cookie-upload] Cookie file parsed to Credential and saved (sessdata=%s...)", truncateForLog(cred.Sessdata, 8))
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// unused import guard
var _ = json.Marshal
