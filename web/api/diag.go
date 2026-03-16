package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/db"
	"video-subscribe-dl/internal/douyin"
)

// DiagHandler 诊断工具 API
type DiagHandler struct {
	db            *db.DB
	getBiliClient func() *bilibili.Client
}

func NewDiagHandler(database *db.DB) *DiagHandler {
	return &DiagHandler{db: database}
}

func (h *DiagHandler) SetBiliClientFunc(fn func() *bilibili.Client) {
	h.getBiliClient = fn
}

// DiagResult 单项诊断结果
type DiagResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`  // "ok" | "warn" | "fail"
	Message string `json:"message"`
	Latency string `json:"latency,omitempty"`
}

// DiagResponse 诊断汇总响应
type DiagResponse struct {
	Platform string       `json:"platform"`
	Overall  string       `json:"overall"` // "healthy" | "degraded" | "error"
	Time     string       `json:"time"`
	Checks   []DiagResult `json:"checks"`
}

// GET /api/diag/bili — B站连通性诊断
func (h *DiagHandler) HandleBili(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	resp := DiagResponse{
		Platform: "bilibili",
		Time:     time.Now().Format(time.RFC3339),
		Checks:   []DiagResult{},
	}

	overallOK := true

	// 1. Cookie / Credential 检查
	credJSON, _ := h.db.GetSetting("credential_json")
	cookiePath, _ := h.db.GetSetting("cookie_path")
	if credJSON != "" {
		cred := bilibili.CredentialFromJSON(credJSON)
		if cred != nil && !cred.IsEmpty() {
			resp.Checks = append(resp.Checks, DiagResult{
				Name:    "credential",
				Status:  "ok",
				Message: fmt.Sprintf("Credential 已配置 (DedeUserID=%s)", cred.DedeUserID),
			})
		} else {
			resp.Checks = append(resp.Checks, DiagResult{
				Name:    "credential",
				Status:  "warn",
				Message: "Credential JSON 存在但为空或无效",
			})
			overallOK = false
		}
	} else if cookiePath != "" {
		cookie := bilibili.ReadCookieFile(cookiePath)
		if cookie != "" {
			resp.Checks = append(resp.Checks, DiagResult{
				Name:    "credential",
				Status:  "ok",
				Message: fmt.Sprintf("Cookie 文件已配置: %s", cookiePath),
			})
		} else {
			resp.Checks = append(resp.Checks, DiagResult{
				Name:    "credential",
				Status:  "warn",
				Message: fmt.Sprintf("Cookie 文件为空或不可读: %s", cookiePath),
			})
			overallOK = false
		}
	} else {
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "credential",
			Status:  "warn",
			Message: "未配置 Cookie 或 Credential，下载画质受限 (最高 480P)",
		})
	}

	// 2. Nav API — 检查登录状态
	var client *bilibili.Client
	if h.getBiliClient != nil {
		client = h.getBiliClient()
	}
	if client == nil {
		client = bilibili.NewClient("")
	}

	start := time.Now()
	verifyResult, err := client.VerifyCookie()
	latency := time.Since(start)
	if err != nil {
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "nav_api",
			Status:  "fail",
			Message: fmt.Sprintf("Nav API 请求失败: %v", err),
			Latency: latency.String(),
		})
		overallOK = false
	} else if verifyResult.LoggedIn {
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "nav_api",
			Status:  "ok",
			Message: fmt.Sprintf("登录有效 — 用户: %s, %s, 最高画质: %s", verifyResult.Username, verifyResult.VIPLabel, verifyResult.MaxQuality),
			Latency: latency.String(),
		})
	} else {
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "nav_api",
			Status:  "warn",
			Message: "Nav API 可达但未登录，下载画质受限",
			Latency: latency.String(),
		})
	}

	// 3. WBI 签名 + acc/info — 检查是否被风控
	start = time.Now()
	_, wbiErr := client.GetUPInfo(1) // MID=1 是 B站官方账号，用来测试 WBI
	latency = time.Since(start)
	if wbiErr != nil {
		errMsg := wbiErr.Error()
		if strings.Contains(errMsg, "rate limit") || strings.Contains(errMsg, "-352") || strings.Contains(errMsg, "-401") || strings.Contains(errMsg, "-412") {
			resp.Checks = append(resp.Checks, DiagResult{
				Name:    "wbi_sign",
				Status:  "fail",
				Message: fmt.Sprintf("WBI 签名请求被风控拦截: %v", wbiErr),
				Latency: latency.String(),
			})
			overallOK = false
		} else {
			resp.Checks = append(resp.Checks, DiagResult{
				Name:    "wbi_sign",
				Status:  "warn",
				Message: fmt.Sprintf("WBI 签名请求失败 (可能是网络问题): %v", wbiErr),
				Latency: latency.String(),
			})
		}
	} else {
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "wbi_sign",
			Status:  "ok",
			Message: "WBI 签名正常，API 可正常访问",
			Latency: latency.String(),
		})
	}

	if overallOK {
		resp.Overall = "healthy"
	} else {
		resp.Overall = "degraded"
	}

	// If any check is "fail", overall is "error"
	for _, c := range resp.Checks {
		if c.Status == "fail" {
			resp.Overall = "error"
			break
		}
	}

	apiOK(w, resp)
}

// GET /api/diag/douyin — 抖音连通性诊断
func (h *DiagHandler) HandleDouyin(w http.ResponseWriter, r *http.Request) {
	if !MethodGuard("GET", w, r) {
		return
	}

	resp := DiagResponse{
		Platform: "douyin",
		Time:     time.Now().Format(time.RFC3339),
		Checks:   []DiagResult{},
	}

	overallOK := true

	// 1. Cookie 生成 (msToken + ttwid)
	dyClient := douyin.NewClient()
	defer dyClient.Close()
	cookieStr := dyClient.GetCookieString()
	hasMsToken := strings.Contains(cookieStr, "msToken=")
	hasTTWID := strings.Contains(cookieStr, "ttwid=")

	if hasMsToken && hasTTWID {
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "cookie_gen",
			Status:  "ok",
			Message: "Cookie 生成正常 (msToken + ttwid 均获取成功)",
		})
	} else {
		missing := []string{}
		if !hasMsToken {
			missing = append(missing, "msToken")
		}
		if !hasTTWID {
			missing = append(missing, "ttwid")
		}
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "cookie_gen",
			Status:  "warn",
			Message: fmt.Sprintf("Cookie 部分缺失: %s，可能影响请求成功率", strings.Join(missing, ", ")),
		})
		overallOK = false
	}

	// 2. iesdouyin.com 连通性
	start := time.Now()
	testURL := fmt.Sprintf("https://www.iesdouyin.com/share/video/%s", "7000000000000000000")
	httpClient := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", testURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1")
	httpResp, err := httpClient.Do(req)
	latency := time.Since(start)
	if err != nil {
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "ies_connectivity",
			Status:  "fail",
			Message: fmt.Sprintf("iesdouyin.com 连接失败: %v", err),
			Latency: latency.String(),
		})
		overallOK = false
	} else {
		io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		if httpResp.StatusCode < 400 {
			resp.Checks = append(resp.Checks, DiagResult{
				Name:    "ies_connectivity",
				Status:  "ok",
				Message: fmt.Sprintf("iesdouyin.com 可达 (HTTP %d)", httpResp.StatusCode),
				Latency: latency.String(),
			})
		} else {
			resp.Checks = append(resp.Checks, DiagResult{
				Name:    "ies_connectivity",
				Status:  "warn",
				Message: fmt.Sprintf("iesdouyin.com 返回非预期状态码: HTTP %d", httpResp.StatusCode),
				Latency: latency.String(),
			})
		}
	}

	// 3. X-Bogus 签名测试
	start = time.Now()
	signOK := douyin.TestXBogusSign()
	latency = time.Since(start)
	if signOK {
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "xbogus_sign",
			Status:  "ok",
			Message: "X-Bogus 签名引擎正常",
			Latency: latency.String(),
		})
	} else {
		resp.Checks = append(resp.Checks, DiagResult{
			Name:    "xbogus_sign",
			Status:  "warn",
			Message: "X-Bogus 签名失败，将降级为无签名模式（部分功能可能受限）",
			Latency: latency.String(),
		})
	}

	if overallOK {
		resp.Overall = "healthy"
	} else {
		resp.Overall = "degraded"
	}

	for _, c := range resp.Checks {
		if c.Status == "fail" {
			resp.Overall = "error"
			break
		}
	}

	apiOK(w, resp)
}
