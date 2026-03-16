package douyin

import (
	"fmt"
	"net/http"
	"net/url"
)

// buildBaseParams 构建抖音 API 的基础查询参数
// 参考 f2 项目的 BaseRequestModel，模拟完整的浏览器环境参数
// 抖音后端会校验这些参数的完整性，缺失关键参数会导致返回空列表
func (c *DouyinClient) buildBaseParams() url.Values {
	fp := c.fingerprint
	params := url.Values{}

	// 设备和应用标识
	params.Set("device_platform", "webapp")
	params.Set("aid", "6383")
	params.Set("channel", "channel_pc_web")
	params.Set("pc_client_type", "1")
	params.Set("publish_video_strategy_type", "2")

	// 根据 platform 设置 pc_libra_divert
	switch {
	case containsStr(fp.UserAgent, "Windows"):
		params.Set("pc_libra_divert", "Windows")
	case containsStr(fp.UserAgent, "Macintosh"):
		params.Set("pc_libra_divert", "Mac")
	default:
		params.Set("pc_libra_divert", "Windows")
	}

	// 版本信息（参考 f2 conf.yaml）
	params.Set("version_code", "290100")
	params.Set("version_name", "29.1.0")

	// 浏览器环境
	params.Set("cookie_enabled", "true")
	params.Set("screen_width", fmt.Sprintf("%d", fp.ScreenWidth))
	params.Set("screen_height", fmt.Sprintf("%d", fp.ScreenHeight))
	params.Set("browser_language", fp.Language)
	params.Set("browser_platform", fp.Platform)
	params.Set("browser_name", "Chrome")
	params.Set("browser_version", fp.ChromeVer+".0.0.0")
	params.Set("browser_online", "true")

	// 引擎信息
	params.Set("engine_name", "Blink")
	params.Set("engine_version", fp.ChromeVer+".0.0.0")

	// OS 信息
	switch {
	case containsStr(fp.UserAgent, "Windows"):
		params.Set("os_name", "Windows")
		params.Set("os_version", "10")
	case containsStr(fp.UserAgent, "Macintosh"):
		params.Set("os_name", "Mac OS")
		params.Set("os_version", "10_15_7")
	default:
		params.Set("os_name", "Windows")
		params.Set("os_version", "10")
	}

	// 硬件信息
	params.Set("cpu_core_num", fmt.Sprintf("%d", fp.HardwareConcurrency))
	params.Set("device_memory", "8")
	params.Set("platform", "PC")

	// 网络信息
	params.Set("downlink", "10")
	params.Set("effective_type", "4g")
	params.Set("round_trip_time", "100")

	// msToken 也作为 URL 参数（参考 f2 BaseRequestModel）
	msToken := generateMsToken()
	params.Set("msToken", msToken)

	return params
}

// setFullHeaders 设置完整的浏览器请求头
// 参考 f2 和 Evil0ctal 项目，补全所有必要的浏览器请求头
// 抖音后端会校验请求头的完整性
func (c *DouyinClient) setFullHeaders(req *http.Request) {
	fp := c.fingerprint
	ua := fp.UserAgent

	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", DouyinReferer)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	// 注意: 不手动设置 Accept-Encoding，让 Go http.Transport 自动处理 gzip 解压
	// 手动设置会导致 Transport 不自动解压，需要自己处理

	// Sec-Fetch-* 安全头（Chrome 标准）
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")

	// Client Hints
	setClientHints(req, ua)
}
