package douyin

// GetCookieString 返回当前客户端使用的 Cookie 字符串（用于诊断）
func (c *DouyinClient) GetCookieString() string {
	return globalCookieMgr.getCookieString(c.normalClient)
}

// TestXBogusSign 测试 X-Bogus 签名引擎是否正常工作
// 返回 true 表示签名引擎可用
func TestXBogusSign() bool {
	testQuery := "aweme_id=7000000000000000000&aid=6383&channel=channel_pc_web&pc_client_type=1"
	testUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	xBogus, err := signURL(testQuery, testUA)
	return err == nil && xBogus != ""
}
