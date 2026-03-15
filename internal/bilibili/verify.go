package bilibili

import (
	"fmt"
	"time"
)

// CookieVerifyResult Cookie 验证结果
type CookieVerifyResult struct {
	LoggedIn    bool   `json:"logged_in"`
	MID         int64  `json:"mid"`
	Username    string `json:"username"`
	VIPType     int    `json:"vip_type"`      // 0=无 1=月度大会员 2=年度大会员
	VIPStatus   int    `json:"vip_status"`    // 0=无VIP 1=VIP有效
	VIPActive   bool   `json:"vip_active"`    // VIP是否在有效期内
	VIPDueDate  string `json:"vip_due_date"`  // 到期时间，格式 2026-01-01
	VIPLabel    string `json:"vip_label"`     // 人类可读的VIP状态描述
	MaxQuality  string `json:"max_quality"`   // 当前能获取的最高画质
	MaxAudio    string `json:"max_audio"`     // 当前能获取的最高音质
}

// VerifyCookie 验证当前 Cookie 是否有效
// 调用 B站 /x/web-interface/nav 接口检测登录状态
func (c *Client) VerifyCookie() (*CookieVerifyResult, error) {
	if c.cookie == "" {
		return &CookieVerifyResult{LoggedIn: false}, nil
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			IsLogin    bool   `json:"isLogin"`
			MID        int64  `json:"mid"`
			Uname      string `json:"uname"`
			VIPStatus  int    `json:"vipStatus"`
			VIPType    int    `json:"vipType"`
			VIPDueDate int64  `json:"vipDueDate"` // 毫秒时间戳
		} `json:"data"`
	}

	err := c.get("https://api.bilibili.com/x/web-interface/nav", &resp)
	if err != nil {
		return nil, fmt.Errorf("verify cookie request failed: %w", err)
	}

	if resp.Code != 0 {
		return &CookieVerifyResult{LoggedIn: false}, nil
	}

	result := &CookieVerifyResult{
		LoggedIn:  resp.Data.IsLogin,
		MID:       resp.Data.MID,
		Username:  resp.Data.Uname,
		VIPType:   resp.Data.VIPType,
		VIPStatus: resp.Data.VIPStatus,
	}

	// 解析VIP到期时间并判断是否仍有效
	if resp.Data.VIPDueDate > 0 {
		t := time.UnixMilli(resp.Data.VIPDueDate)
		result.VIPDueDate = t.Format("2006-01-02")
		result.VIPActive = resp.Data.VIPStatus == 1 && t.After(time.Now())
	}

	// 生成人类可读的VIP状态
	switch {
	case !resp.Data.IsLogin:
		result.VIPLabel = "未登录"
		result.MaxQuality = "480P"
		result.MaxAudio = "64kbps"
	case result.VIPActive:
		if resp.Data.VIPType == 2 {
			result.VIPLabel = "年度大会员（有效）"
		} else {
			result.VIPLabel = "月度大会员（有效）"
		}
		result.MaxQuality = "4K/HDR"
		result.MaxAudio = "Hi-Res/杜比"
	case resp.Data.VIPType > 0:
		result.VIPLabel = fmt.Sprintf("大会员已过期（%s到期）", result.VIPDueDate)
		result.MaxQuality = "1080P"
		result.MaxAudio = "192kbps"
	default:
		result.VIPLabel = "普通用户"
		result.MaxQuality = "1080P"
		result.MaxAudio = "192kbps"
	}

	return result, nil
}
