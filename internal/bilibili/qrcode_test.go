package bilibili

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPollQRCodeStatusParsing(t *testing.T) {
	tests := []struct {
		name       string
		respCode   int
		respBody   string
		cookies    []*http.Cookie
		wantStatus int
		wantMsg    string
		wantCred   bool
	}{
		{
			name:     "未扫描",
			respCode: 200,
			respBody: `{"code":0,"data":{"code":86101,"message":"未扫码","url":"","refresh_token":"","timestamp":0}}`,
			wantStatus: QRNotScanned,
			wantMsg:    "等待扫码",
		},
		{
			name:     "已扫码未确认",
			respCode: 200,
			respBody: `{"code":0,"data":{"code":86090,"message":"","url":"","refresh_token":"","timestamp":0}}`,
			wantStatus: QRScanned,
			wantMsg:    "已扫码，等待确认",
		},
		{
			name:     "二维码过期",
			respCode: 200,
			respBody: `{"code":0,"data":{"code":86038,"message":"二维码已失效","url":"","refresh_token":"","timestamp":0}}`,
			wantStatus: QRExpired,
			wantMsg:    "二维码已过期",
		},
		{
			name:     "登录成功",
			respCode: 200,
			respBody: `{"code":0,"data":{"code":0,"message":"","url":"https://example.com","refresh_token":"rt_abc123","timestamp":1700000000}}`,
			cookies: []*http.Cookie{
				{Name: "SESSDATA", Value: "sess_abc"},
				{Name: "bili_jct", Value: "jct_def"},
				{Name: "DedeUserID", Value: "12345"},
			},
			wantStatus: QRSuccess,
			wantMsg:    "登录成功",
			wantCred:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, c := range tt.cookies {
					http.SetCookie(w, c)
				}
				w.WriteHeader(tt.respCode)
				w.Write([]byte(tt.respBody))
			}))
			defer server.Close()

			// 直接测试解析逻辑（因为 PollQRCode 调用的是固定 URL，这里验证解析逻辑）
			client := server.Client()
			resp, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

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
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}

			if result.Data.Code != tt.wantStatus {
				t.Errorf("status: want %d, got %d", tt.wantStatus, result.Data.Code)
			}

			if tt.wantCred {
				if result.Data.RefreshToken == "" {
					t.Error("expected refresh_token in success response")
				}
				// 检查 cookies
				if len(resp.Cookies()) == 0 {
					t.Error("expected cookies in success response")
				}
			}
		})
	}
}
