package douyin

import (
	"strings"
	"testing"
)

// TestGenerateMsToken 测试 msToken 格式
func TestGenerateMsToken(t *testing.T) {
	token := generateMsToken()

	// 长度应为 107
	if len(token) != 107 {
		t.Errorf("msToken length = %d, want 107", len(token))
	}

	// 字符集: A-Za-z0-9
	for i, ch := range token {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
			t.Errorf("msToken[%d] = %q, not alphanumeric", i, string(ch))
		}
	}

	// 不同调用应产生不同的 token
	token2 := generateMsToken()
	if token == token2 {
		t.Error("two consecutive generateMsToken() calls returned the same value")
	}
}

// TestGenerateVerifyFp 测试 verify_fp 格式
func TestGenerateVerifyFp(t *testing.T) {
	fp := generateVerifyFp()

	// 前缀: verify_
	if !strings.HasPrefix(fp, "verify_") {
		t.Errorf("verify_fp = %q, missing 'verify_' prefix", fp)
	}

	// 总长度: "verify_" (7) + 13 = 20
	if len(fp) != 20 {
		t.Errorf("verify_fp length = %d, want 20", len(fp))
	}

	// 随机部分字符集: 0-9a-zA-Z
	suffix := fp[7:]
	for i, ch := range suffix {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
			t.Errorf("verify_fp suffix[%d] = %q, not alphanumeric", i, string(ch))
		}
	}

	// 不同调用应产生不同的值
	fp2 := generateVerifyFp()
	if fp == fp2 {
		t.Error("two consecutive generateVerifyFp() calls returned the same value")
	}
}

// TestGetCookieString_Fields 测试 cookie 字段完整性（不依赖网络）
func TestGetCookieString_Fields(t *testing.T) {
	requiredFields := []string{
		"msToken",
		"ttwid",
		"odin_tt",
		"bd_ticket_guard_client_data",
		"verify_fp",
		"s_v_web_id",
	}

	// 模拟 cookie 组装（与 getCookieString 逻辑一致，但不触发网络请求）
	msToken := generateMsToken()
	verifyFp := generateVerifyFp()
	sVWebID := generateVerifyFp()

	cookie := strings.Join([]string{
		"msToken=" + msToken,
		"ttwid=test_ttwid",
		"odin_tt=" + fixedOdinTT,
		"bd_ticket_guard_client_data=" + fixedBdTicketGuardClientData,
		"verify_fp=" + verifyFp,
		"s_v_web_id=" + sVWebID,
	}, "; ")

	for _, field := range requiredFields {
		if !strings.Contains(cookie, field+"=") {
			t.Errorf("cookie missing field %q", field)
		}
	}

	// 验证每个字段都有非空值
	parts := strings.Split(cookie, "; ")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			t.Errorf("invalid cookie part: %q", part)
			continue
		}
		if kv[1] == "" {
			t.Errorf("cookie field %q has empty value", kv[0])
		}
	}
}

// TestSetUserCookie_SessionCookie 验证: SetUserCookie 后 getSessionCookie 返回用户 Cookie
func TestSetUserCookie_SessionCookie(t *testing.T) {
	// 保存全局状态，测试完毕后还原
	origCookie := globalCookieMgr.GetUserCookie()
	defer globalCookieMgr.SetUserCookie(origCookie)

	userCookie := "uid_tt=test123; sessionid=abc456; msToken=fake; ttwid=fake"

	// 设置用户 Cookie
	globalCookieMgr.SetUserCookie(userCookie)

	// 创建 client 并获取 session cookie
	client := NewClient()
	defer client.Close()

	got := client.getSessionCookie()

	// 用户 Cookie 设置后，getSessionCookie 应原样返回用户 Cookie
	if got != userCookie {
		t.Errorf("getSessionCookie() = %q, want %q", got, userCookie)
	}

	// 清除用户 Cookie 后，应回退到自动生成
	globalCookieMgr.SetUserCookie("")
	auto := client.getSessionCookie()
	if auto == userCookie {
		t.Error("after clearing user cookie, getSessionCookie() should not return user cookie")
	}
	if auto == "" {
		t.Error("after clearing user cookie, getSessionCookie() should return auto-generated cookie")
	}
	// 自动生成的 cookie 应包含 msToken
	if !strings.Contains(auto, "msToken=") {
		t.Errorf("auto-generated cookie missing msToken: %q", auto)
	}
}

// TestSetGlobalUserCookie 验证全局 setter/getter
func TestSetGlobalUserCookie(t *testing.T) {
	origCookie := GetGlobalUserCookie()
	defer SetGlobalUserCookie(origCookie)

	SetGlobalUserCookie("  test_cookie_value  ")
	got := GetGlobalUserCookie()
	if got != "test_cookie_value" {
		t.Errorf("GetGlobalUserCookie() = %q, want %q", got, "test_cookie_value")
	}

	SetGlobalUserCookie("")
	got = GetGlobalUserCookie()
	if got != "" {
		t.Errorf("GetGlobalUserCookie() after clear = %q, want empty", got)
	}
}

// TestFixedConstants 验证固定常量不为空
func TestFixedConstants(t *testing.T) {
	if fixedOdinTT == "" {
		t.Error("fixedOdinTT is empty")
	}
	if fixedBdTicketGuardClientData == "" {
		t.Error("fixedBdTicketGuardClientData is empty")
	}
	// odin_tt 应该是十六进制字符串
	for _, ch := range fixedOdinTT {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			t.Errorf("fixedOdinTT contains non-hex char: %q", string(ch))
			break
		}
	}
}
