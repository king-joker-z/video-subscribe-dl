package bilibili

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// QRCodeGenerateResult 生成二维码结果
type QRCodeGenerateResult struct {
	URL       string `json:"url"`
	QRCodeKey string `json:"qrcode_key"`
}

// QRCodePollResult 轮询扫码结果
type QRCodePollResult struct {
	Status     int         `json:"status"`      // 0=成功, 86101=未扫描, 86090=已扫描未确认, 86038=过期
	Message    string      `json:"message"`
	Credential *Credential `json:"credential,omitempty"`
}

// QR 状态码
const (
	QRSuccess      = 0
	QRNotScanned   = 86101
	QRScanned      = 86090
	QRExpired      = 86038
)

// GenerateQRCode 生成扫码登录二维码
func GenerateQRCode(httpClient *http.Client) (*QRCodeGenerateResult, error) {
	req, err := http.NewRequest("GET", "https://passport.bilibili.com/x/passport-login/web/qrcode/generate", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("generate qrcode: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Code int `json:"code"`
		Data struct {
			URL       string `json:"url"`
			QRCodeKey string `json:"qrcode_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse qrcode response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("generate qrcode: code=%d", result.Code)
	}
	return &QRCodeGenerateResult{
		URL:       result.Data.URL,
		QRCodeKey: result.Data.QRCodeKey,
	}, nil
}

// PollQRCode 轮询扫码登录状态
func PollQRCode(httpClient *http.Client, qrcodeKey string) (*QRCodePollResult, error) {
	reqURL := fmt.Sprintf("https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key=%s", qrcodeKey)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", randUA())
	req.Header.Set("Referer", "https://www.bilibili.com")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll qrcode: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Code int `json:"code"`
		Data struct {
			URL          string `json:"url"`
			RefreshToken string `json:"refresh_token"`
			Timestamp    int64  `json:"timestamp"`
			Code         int    `json:"code"`
			Message      string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse poll response: %w", err)
	}

	pollResult := &QRCodePollResult{
		Status: result.Data.Code,
	}

	switch result.Data.Code {
	case QRSuccess:
		pollResult.Message = "登录成功"

		// 从 Set-Cookie 提取凭证
		cred := &Credential{
			ACTimeValue: result.Data.RefreshToken,
			UpdatedAt:   time.Now().Unix(),
		}
		for _, cookie := range resp.Cookies() {
			switch cookie.Name {
			case "SESSDATA":
				cred.Sessdata = cookie.Value
			case "bili_jct":
				cred.BiliJCT = cookie.Value
			case "DedeUserID":
				cred.DedeUserID = cookie.Value
			}
		}

		if cred.Sessdata == "" {
			return nil, fmt.Errorf("login succeeded but no SESSDATA in Set-Cookie")
		}

		// 获取 buvid3/buvid4 并激活
		buvid3, buvid4, buvidErr := GetBuvidPair(httpClient)
		if buvidErr != nil {
			fmt.Printf("[qrcode] Warning: get buvid pair failed: %v\n", buvidErr)
		} else {
			cred.Buvid3 = buvid3
			cred.Buvid4 = buvid4
			// 激活 buvid（非阻塞，失败不影响登录）
			if actErr := ActivateBuvid(httpClient, buvid3, buvid4); actErr != nil {
				fmt.Printf("[qrcode] Warning: activate buvid failed: %v\n", actErr)
			}
		}

		pollResult.Credential = cred

	case QRNotScanned:
		pollResult.Message = "等待扫码"
	case QRScanned:
		pollResult.Message = "已扫码，等待确认"
	case QRExpired:
		pollResult.Message = "二维码已过期"
	default:
		pollResult.Message = result.Data.Message
	}

	return pollResult, nil
}
