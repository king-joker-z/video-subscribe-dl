package douyin

import (
	"fmt"
	"math/rand"
	"sync"
)

// BrowserFingerprint 浏览器指纹，模拟真实浏览器环境
// 同一会话内保持所有请求使用一致的指纹参数
type BrowserFingerprint struct {
	// 基础标识
	UserAgent    string // UA 字符串
	ClientHints  ClientHints
	ChromeVer    string // Chrome 主版本号

	// 屏幕参数
	ScreenWidth  int
	ScreenHeight int
	DevicePixelRatio float64
	ColorDepth   int

	// 浏览器特征
	Platform     string // navigator.platform
	Language     string // navigator.language
	Languages    string // navigator.languages (JSON array)
	TimeZone     string // Intl.DateTimeFormat().resolvedOptions().timeZone
	HardwareConcurrency int // navigator.hardwareConcurrency

	// 画布/WebGL 指纹（固定值，用于 msToken 等请求）
	WebGLVendor   string
	WebGLRenderer string
}

// 常见屏幕分辨率组合（width x height @ dpr）
var screenProfiles = []struct {
	Width, Height int
	DPR           float64
}{
	{1920, 1080, 1.0},
	{2560, 1440, 1.0},
	{1920, 1080, 1.25},
	{1536, 864, 1.25},
	{1440, 900, 2.0},   // MacBook
	{2560, 1600, 2.0},  // MacBook Pro
	{1680, 1050, 1.0},
	{3840, 2160, 1.5},  // 4K
}

// WebGL 渲染器组合
var webglProfiles = []struct {
	Vendor   string
	Renderer string
}{
	{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
	{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 4070 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
	{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
	{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) Iris(R) Xe Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)"},
	{"Google Inc. (Apple)", "ANGLE (Apple, Apple M1 Pro, OpenGL 4.1)"},
	{"Google Inc. (Apple)", "ANGLE (Apple, Apple M2, OpenGL 4.1)"},
	{"Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 6700 XT Direct3D11 vs_5_0 ps_5_0, D3D11)"},
}

var platformMap = map[string]string{
	"Windows":   "Win32",
	"macOS":     "MacIntel",
	"Linux":     "Linux x86_64",
}

// GenerateFingerprint 生成一个随机但内部一致的浏览器指纹
func GenerateFingerprint() *BrowserFingerprint {
	fp := &BrowserFingerprint{}

	// 选择 UA
	fp.UserAgent = pickPCUA()
	fp.ChromeVer = extractChromeVersion(fp.UserAgent)

	// 匹配 Client Hints
	if hints, ok := clientHintsMap[fp.ChromeVer]; ok {
		fp.ClientHints = hints
	}

	// 屏幕参数
	screen := screenProfiles[rand.Intn(len(screenProfiles))]
	fp.ScreenWidth = screen.Width
	fp.ScreenHeight = screen.Height
	fp.DevicePixelRatio = screen.DPR
	fp.ColorDepth = 24

	// 平台（根据 UA 推断）
	if containsStr(fp.UserAgent, "Windows") {
		fp.Platform = "Win32"
	} else if containsStr(fp.UserAgent, "Macintosh") {
		fp.Platform = "MacIntel"
	} else {
		fp.Platform = "Linux x86_64"
	}

	// 语言
	fp.Language = "zh-CN"
	fp.Languages = `["zh-CN","zh","en"]`
	fp.TimeZone = "Asia/Shanghai"

	// 硬件并发数（常见值）
	concurrencies := []int{4, 8, 12, 16}
	fp.HardwareConcurrency = concurrencies[rand.Intn(len(concurrencies))]

	// WebGL
	webgl := webglProfiles[rand.Intn(len(webglProfiles))]
	fp.WebGLVendor = webgl.Vendor
	fp.WebGLRenderer = webgl.Renderer

	return fp
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && findSubstr(s, sub))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---- Session Fingerprint (单例，一个 Client 实例内保持一致) ----

var (
	sessionFP     *BrowserFingerprint
	sessionFPOnce sync.Once
)

// GetSessionFingerprint 获取当前会话的指纹（进程级别单例）
func GetSessionFingerprint() *BrowserFingerprint {
	sessionFPOnce.Do(func() {
		sessionFP = GenerateFingerprint()
	})
	return sessionFP
}

// ResetSessionFingerprint 重置会话指纹（用于测试或手动刷新）
func ResetSessionFingerprint() {
	sessionFPOnce = sync.Once{}
	sessionFP = nil
}

// String 返回指纹摘要（用于日志）
func (fp *BrowserFingerprint) String() string {
	return fmt.Sprintf("UA=Chrome/%s Screen=%dx%d@%.1f Platform=%s Cores=%d",
		fp.ChromeVer, fp.ScreenWidth, fp.ScreenHeight, fp.DevicePixelRatio,
		fp.Platform, fp.HardwareConcurrency)
}
