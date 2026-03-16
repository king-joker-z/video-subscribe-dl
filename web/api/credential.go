package api

import (
	"log"
	"net/http"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
)

// CredentialHandler 凭证/登录 API
type CredentialHandler struct {
	db                 *db.DB
	onCredentialUpdate func(*bilibili.Credential)
}

func NewCredentialHandler(database *db.DB) *CredentialHandler {
	return &CredentialHandler{db: database}
}

func (h *CredentialHandler) SetCredentialUpdateFunc(fn func(*bilibili.Credential)) {
	h.onCredentialUpdate = fn
}

// POST /api/login/qrcode/generate — 生成扫码二维码
func (h *CredentialHandler) HandleQRCodeGenerate(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	httpClient := sharedAPIClient
	result, err := bilibili.GenerateQRCode(httpClient)
	if err != nil {
		log.Printf("[qrcode] Generate failed: %v", err)
		apiError(w, CodeInternal, "生成二维码失败: "+err.Error())
		return
	}

	apiOK(w, map[string]interface{}{
		"url":        result.URL,
		"qrcode_key": result.QRCodeKey,
	})
}

// GET /api/login/qrcode/poll — 轮询扫码状态
func (h *CredentialHandler) HandleQRCodePoll(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	qrcodeKey := r.URL.Query().Get("qrcode_key")
	if qrcodeKey == "" {
		apiError(w, CodeInvalidParam, "qrcode_key 不能为空")
		return
	}

	httpClient := sharedAPIClient
	result, err := bilibili.PollQRCode(httpClient, qrcodeKey)
	if err != nil {
		apiError(w, CodeInternal, "轮询失败: "+err.Error())
		return
	}

	resp := map[string]interface{}{
		"status":  result.Status,
		"message": result.Message,
	}

	if result.Status == bilibili.QRSuccess && result.Credential != nil {
		cred := result.Credential

		// 保存凭证
		if err := h.db.SetSetting("credential_json", cred.ToJSON()); err != nil {
			apiError(w, CodeInternal, "保存凭证失败")
			return
		}
		h.db.SetSetting("credential_source", "qrcode")

		// 通知 scheduler
		if h.onCredentialUpdate != nil {
			h.onCredentialUpdate(cred)
		}

		// 验证用户信息
		verifyResult, _ := bilibili.VerifyCredential(cred, httpClient)
		if verifyResult != nil && verifyResult.LoggedIn {
			resp["username"] = verifyResult.Username
			resp["vip_label"] = verifyResult.VIPLabel
			resp["max_quality"] = verifyResult.MaxQuality
			resp["max_audio"] = verifyResult.MaxAudio
		}

		log.Printf("[qrcode] 扫码登录成功: user=%v", resp["username"])
	}

	apiOK(w, resp)
}

// GET /api/credential — 凭证状态
func (h *CredentialHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	credJSON, _ := h.db.GetSetting("credential_json")
	credSource, _ := h.db.GetSetting("credential_source")
	cred := bilibili.CredentialFromJSON(credJSON)

	if cred == nil || cred.IsEmpty() {
		apiOK(w, map[string]interface{}{
			"has_credential": false,
			"source":         "none",
		})
		return
	}

	result := map[string]interface{}{
		"has_credential": true,
		"source":         credSource,
	}

	if cred.UpdatedAt > 0 {
		result["updated_at"] = time.Unix(cred.UpdatedAt, 0).Format("2006-01-02 15:04:05")
	}

	httpClient := sharedAPIClient

	// 检查是否需要刷新
	needRefresh, _ := cred.NeedRefresh(httpClient)
	result["need_refresh"] = needRefresh

	// 获取用户信息
	verifyResult, _ := bilibili.VerifyCredential(cred, httpClient)
	if verifyResult != nil {
		if !verifyResult.LoggedIn {
			result["need_refresh"] = true
		} else {
			result["username"] = verifyResult.Username
			result["vip_label"] = verifyResult.VIPLabel
			result["max_quality"] = verifyResult.MaxQuality
			result["max_audio"] = verifyResult.MaxAudio
		}
	}

	apiOK(w, result)
}

// POST /api/credential/refresh — 刷新凭证
func (h *CredentialHandler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("POST", w, r) {
		return
	}

	credJSON, _ := h.db.GetSetting("credential_json")
	cred := bilibili.CredentialFromJSON(credJSON)
	if cred == nil || cred.IsEmpty() {
		apiError(w, CodeCredentialEmpty, "无凭证可刷新")
		return
	}

	httpClient := sharedAPIClient
	newCred, err := cred.Refresh(httpClient)
	if err != nil {
		apiError(w, CodeInternal, "刷新失败: "+err.Error())
		return
	}

	h.db.SetSetting("credential_json", newCred.ToJSON())

	if h.onCredentialUpdate != nil {
		h.onCredentialUpdate(newCred)
	}

	apiOK(w, map[string]interface{}{"message": "凭证刷新成功"})
}
