package douyin

import (
	"strings"
	"testing"
)

// TestGenerateFingerprint 测试指纹结构完整性
func TestGenerateFingerprint(t *testing.T) {
	fp := GenerateFingerprint()

	if fp.UserAgent == "" {
		t.Error("UserAgent is empty")
	}
	if fp.ChromeVer == "" {
		t.Error("ChromeVer is empty")
	}
	if fp.ScreenWidth == 0 {
		t.Error("ScreenWidth is 0")
	}
	if fp.ScreenHeight == 0 {
		t.Error("ScreenHeight is 0")
	}
	if fp.DevicePixelRatio == 0 {
		t.Error("DevicePixelRatio is 0")
	}
	if fp.ColorDepth != 24 {
		t.Errorf("ColorDepth = %d, want 24", fp.ColorDepth)
	}
	if fp.Platform == "" {
		t.Error("Platform is empty")
	}
	if fp.Language != "zh-CN" {
		t.Errorf("Language = %q, want 'zh-CN'", fp.Language)
	}
	if fp.TimeZone != "Asia/Shanghai" {
		t.Errorf("TimeZone = %q, want 'Asia/Shanghai'", fp.TimeZone)
	}
	if fp.HardwareConcurrency == 0 {
		t.Error("HardwareConcurrency is 0")
	}
	if fp.WebGLVendor == "" {
		t.Error("WebGLVendor is empty")
	}
	if fp.WebGLRenderer == "" {
		t.Error("WebGLRenderer is empty")
	}

	// UA 应该是 PC UA（包含 Chrome）
	if !strings.Contains(fp.UserAgent, "Chrome") {
		t.Errorf("UserAgent should contain 'Chrome': %q", fp.UserAgent)
	}

	// Platform 应该与 UA 匹配
	if strings.Contains(fp.UserAgent, "Windows") && fp.Platform != "Win32" {
		t.Errorf("Platform=%q doesn't match Windows UA", fp.Platform)
	}
	if strings.Contains(fp.UserAgent, "Macintosh") && fp.Platform != "MacIntel" {
		t.Errorf("Platform=%q doesn't match macOS UA", fp.Platform)
	}
}

// TestFingerprintConsistency 测试同一实例返回相同指纹
func TestFingerprintConsistency(t *testing.T) {
	// Reset 后重新获取应得到新的单例
	ResetSessionFingerprint()

	fp1 := GetSessionFingerprint()
	fp2 := GetSessionFingerprint()

	if fp1 != fp2 {
		t.Error("GetSessionFingerprint() should return the same instance")
	}

	if fp1.UserAgent != fp2.UserAgent {
		t.Error("session fingerprint UserAgent should be consistent")
	}
	if fp1.ChromeVer != fp2.ChromeVer {
		t.Error("session fingerprint ChromeVer should be consistent")
	}
	if fp1.ScreenWidth != fp2.ScreenWidth {
		t.Error("session fingerprint ScreenWidth should be consistent")
	}
}

// TestResetSessionFingerprint 测试指纹重置
func TestResetSessionFingerprint(t *testing.T) {
	ResetSessionFingerprint()
	fp1 := GetSessionFingerprint()

	ResetSessionFingerprint()
	fp2 := GetSessionFingerprint()

	// 重置后获取的指纹可能相同也可能不同（随机生成）
	// 但两次调用都不应 panic
	if fp1 == nil || fp2 == nil {
		t.Error("GetSessionFingerprint() should never return nil")
	}
}

// TestFingerprintString 测试指纹摘要
func TestFingerprintString(t *testing.T) {
	fp := GenerateFingerprint()
	s := fp.String()

	if s == "" {
		t.Error("String() returned empty")
	}
	if !strings.Contains(s, "UA=Chrome/") {
		t.Errorf("String() missing Chrome version: %q", s)
	}
	if !strings.Contains(s, "Screen=") {
		t.Errorf("String() missing Screen: %q", s)
	}
	if !strings.Contains(s, "Platform=") {
		t.Errorf("String() missing Platform: %q", s)
	}
}

// TestScreenProfiles 验证屏幕配置表不为空
func TestScreenProfiles(t *testing.T) {
	if len(screenProfiles) == 0 {
		t.Error("screenProfiles is empty")
	}
	for i, sp := range screenProfiles {
		if sp.Width <= 0 || sp.Height <= 0 || sp.DPR <= 0 {
			t.Errorf("screenProfiles[%d] has invalid values: %+v", i, sp)
		}
	}
}

// TestWebGLProfiles 验证 WebGL 配置表不为空
func TestWebGLProfiles(t *testing.T) {
	if len(webglProfiles) == 0 {
		t.Error("webglProfiles is empty")
	}
	for i, wp := range webglProfiles {
		if wp.Vendor == "" || wp.Renderer == "" {
			t.Errorf("webglProfiles[%d] has empty fields: %+v", i, wp)
		}
	}
}
