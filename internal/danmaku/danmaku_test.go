package danmaku

import (
	"os"
	"strings"
	"testing"
)

const testXML = `<?xml version="1.0" encoding="UTF-8"?>
<i>
<d p="1.5,1,25,16777215,1609459200,0,abc123,111">Hello World</d>
<d p="3.0,5,25,255,1609459201,0,def456,222">Top Comment</d>
<d p="5.5,4,18,16711680,1609459202,0,ghi789,333">Bottom Red</d>
<d p="0.5,1,25,65280,1609459203,0,jkl012,444">Early Green</d>
<d p="10.0,7,25,16777215,1609459204,0,mno345,555">Advanced Mode</d>
</i>`

func writeTempXML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "danmaku_test_*.xml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.WriteString(content)
	f.Close()
	return f.Name()
}

// === TestParseXML ===

func TestParseXML_Normal(t *testing.T) {
	path := writeTempXML(t, testXML)
	defer os.Remove(path)

	dms, err := ParseXML(path)
	if err != nil {
		t.Fatalf("parse xml: %v", err)
	}
	if len(dms) != 5 {
		t.Fatalf("expected 5 danmakus, got %d", len(dms))
	}
	// Should be sorted by time
	if dms[0].Time != 0.5 {
		t.Errorf("expected first danmaku at 0.5s, got %f", dms[0].Time)
	}
	if dms[0].Text != "Early Green" {
		t.Errorf("expected 'Early Green', got '%s'", dms[0].Text)
	}
}

func TestParseXML_EmptyFile(t *testing.T) {
	path := writeTempXML(t, `<?xml version="1.0" encoding="UTF-8"?><i></i>`)
	defer os.Remove(path)

	dms, err := ParseXML(path)
	if err != nil {
		t.Fatalf("parse xml: %v", err)
	}
	if len(dms) != 0 {
		t.Errorf("expected 0 danmakus, got %d", len(dms))
	}
}

func TestParseXML_ModeAndColor(t *testing.T) {
	path := writeTempXML(t, testXML)
	defer os.Remove(path)

	dms, err := ParseXML(path)
	if err != nil {
		t.Fatalf("parse xml: %v", err)
	}
	// Find the "Top Comment" (mode 5)
	for _, dm := range dms {
		if dm.Text == "Top Comment" {
			if dm.Mode != 5 {
				t.Errorf("expected mode 5, got %d", dm.Mode)
			}
			if dm.Color != 255 { // blue
				t.Errorf("expected color 255, got %d", dm.Color)
			}
			return
		}
	}
	t.Error("'Top Comment' not found")
}

func TestParseXML_FileNotFound(t *testing.T) {
	_, err := ParseXML("/nonexistent/path.xml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// === TestXMLToASS ===

func TestXMLToASS_Normal(t *testing.T) {
	xmlPath := writeTempXML(t, testXML)
	defer os.Remove(xmlPath)

	assPath := xmlPath + ".ass"
	defer os.Remove(assPath)

	err := XMLToASS(xmlPath, assPath, 1920, 1080)
	if err != nil {
		t.Fatalf("XMLToASS: %v", err)
	}

	data, err := os.ReadFile(assPath)
	if err != nil {
		t.Fatalf("read ass: %v", err)
	}
	content := string(data)

	// Check ASS header
	if !strings.Contains(content, "[Script Info]") {
		t.Error("missing [Script Info]")
	}
	if !strings.Contains(content, "PlayResX: 1920") {
		t.Error("missing PlayResX")
	}
	if !strings.Contains(content, "[V4+ Styles]") {
		t.Error("missing [V4+ Styles]")
	}
	if !strings.Contains(content, "[Events]") {
		t.Error("missing [Events]")
	}
	// Should have dialogue lines (mode 7 "Advanced Mode" is skipped)
	if !strings.Contains(content, "Dialogue:") {
		t.Error("missing Dialogue lines")
	}
	// Advanced mode (7) should be skipped
	if strings.Contains(content, "Advanced Mode") {
		t.Error("advanced mode danmaku should be skipped")
	}
	// Check styles have R2L, TOP, BTM
	if !strings.Contains(content, "Style: R2L,") {
		t.Error("missing R2L style")
	}
	if !strings.Contains(content, "Style: TOP,") {
		t.Error("missing TOP style")
	}
	if !strings.Contains(content, "Style: BTM,") {
		t.Error("missing BTM style")
	}
}

func TestXMLToASS_DefaultResolution(t *testing.T) {
	xmlPath := writeTempXML(t, testXML)
	defer os.Remove(xmlPath)

	assPath := xmlPath + ".ass"
	defer os.Remove(assPath)

	// width=0, height=0 should default to 1920x1080
	err := XMLToASS(xmlPath, assPath, 0, 0)
	if err != nil {
		t.Fatalf("XMLToASS: %v", err)
	}

	data, _ := os.ReadFile(assPath)
	content := string(data)
	if !strings.Contains(content, "PlayResX: 1920") {
		t.Error("expected default 1920 width")
	}
	if !strings.Contains(content, "PlayResY: 1080") {
		t.Error("expected default 1080 height")
	}
}

func TestXMLToASS_EmptyDanmaku(t *testing.T) {
	xmlPath := writeTempXML(t, `<?xml version="1.0" encoding="UTF-8"?><i></i>`)
	defer os.Remove(xmlPath)

	assPath := xmlPath + ".ass"
	defer os.Remove(assPath)

	err := XMLToASS(xmlPath, assPath, 1920, 1080)
	if err != nil {
		t.Fatalf("XMLToASS: %v", err)
	}
	// Should still generate valid ASS with headers but no dialogue
	data, _ := os.ReadFile(assPath)
	content := string(data)
	if !strings.Contains(content, "[Script Info]") {
		t.Error("missing [Script Info] in empty danmaku ASS")
	}
}

func TestXMLToASS_WithConfig(t *testing.T) {
	xmlPath := writeTempXML(t, testXML)
	defer os.Remove(xmlPath)

	assPath := xmlPath + ".ass"
	defer os.Remove(assPath)

	cfg := DefaultConfig()
	cfg.BlockTop = true
	cfg.BlockBottom = true
	cfg.ScrollDuration = 8.0
	cfg.FontSize = 30
	cfg.Opacity = 0.5

	err := XMLToASSWithConfig(xmlPath, assPath, 1920, 1080, cfg)
	if err != nil {
		t.Fatalf("XMLToASSWithConfig: %v", err)
	}

	data, _ := os.ReadFile(assPath)
	content := string(data)

	// Top and bottom danmaku should be blocked
	if strings.Contains(content, "Top Comment") {
		t.Error("top danmaku should be blocked")
	}
	if strings.Contains(content, "Bottom Red") {
		t.Error("bottom danmaku should be blocked")
	}
	// Scroll danmaku should still exist
	if !strings.Contains(content, "Hello World") {
		t.Error("scroll danmaku should be present")
	}
}

// === TestCollisionDetection ===

func TestScrollLaneManager_Basic(t *testing.T) {
	mgr := newScrollLaneManager(3, 1920, 12.0)

	// 第一条弹幕应该分配到泳道 0
	lane := mgr.findLane(0.0, 200)
	if lane != 0 {
		t.Errorf("expected lane 0, got %d", lane)
	}
	mgr.occupy(lane, 0.0, 200)

	// 很快又来一条，应该到泳道 1（因为泳道 0 的尾部还没进屏幕）
	lane = mgr.findLane(0.1, 200)
	if lane != 1 {
		t.Errorf("expected lane 1, got %d", lane)
	}
	mgr.occupy(lane, 0.1, 200)

	// 足够久之后，泳道 0 应该又可用了
	lane = mgr.findLane(13.0, 200)
	if lane != 0 {
		t.Errorf("expected lane 0 after scrollDuration, got %d", lane)
	}
}

func TestScrollLaneManager_AllFull(t *testing.T) {
	mgr := newScrollLaneManager(2, 1920, 12.0)

	mgr.occupy(0, 0.0, 200)
	mgr.occupy(1, 0.0, 200)

	// 马上又来一条，两条泳道都满
	lane := mgr.findLane(0.05, 200)
	if lane != -1 {
		t.Errorf("expected -1 (all full), got %d", lane)
	}
}

func TestFixedLaneManager_Basic(t *testing.T) {
	mgr := newFixedLaneManager(3)

	lane := mgr.findLane(0.0)
	if lane != 0 {
		t.Errorf("expected lane 0, got %d", lane)
	}
	mgr.occupy(lane, 5.0)

	// 在 5s 前来一条，泳道 0 还在用，应该到泳道 1
	lane = mgr.findLane(3.0)
	if lane != 1 {
		t.Errorf("expected lane 1, got %d", lane)
	}

	// 在 5s 后来一条，泳道 0 释放了
	lane = mgr.findLane(5.0)
	if lane != 0 {
		t.Errorf("expected lane 0 after release, got %d", lane)
	}
}

// === Test helper functions ===

func TestDecColorToASS(t *testing.T) {
	tests := []struct {
		color    int64
		expected string
	}{
		{16777215, "&HFFFFFF"}, // white
		{255, "&HFF0000"},      // blue (RGB 0,0,255 -> ASS BGR FF0000)
		{16711680, "&H0000FF"}, // red (RGB 255,0,0 -> ASS BGR 0000FF)
		{65280, "&H00FF00"},    // green
	}
	for _, tt := range tests {
		got := decColorToASS(tt.color)
		if got != tt.expected {
			t.Errorf("decColorToASS(%d) = %s, want %s", tt.color, got, tt.expected)
		}
	}
}

func TestFormatASSTime(t *testing.T) {
	tests := []struct {
		seconds  float64
		expected string
	}{
		{0, "0:00:00.00"},
		{65.5, "0:01:05.50"},
		{3661.25, "1:01:01.25"},
		{-1, "0:00:00.00"}, // negative clamped to 0
	}
	for _, tt := range tests {
		got := formatASSTime(tt.seconds)
		if got != tt.expected {
			t.Errorf("formatASSTime(%f) = %s, want %s", tt.seconds, got, tt.expected)
		}
	}
}

func TestEscapeASS(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"a\\b", "a\\\\b"},
		{"{tag}", "\\{tag\\}"},
		{"line1\nline2", "line1\\Nline2"},
	}
	for _, tt := range tests {
		got := escapeASS(tt.input)
		if got != tt.expected {
			t.Errorf("escapeASS(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEstimateTextWidth(t *testing.T) {
	// Pure ASCII: each char = 0.5 * fontSize
	w := estimateTextWidth("abc", 40)
	if w != 60 { // 3 * 0.5 * 40 = 60
		t.Errorf("expected 60, got %d", w)
	}

	// Pure CJK: each char = 1.0 * fontSize
	w = estimateTextWidth("你好", 40)
	if w != 80 { // 2 * 40 = 80
		t.Errorf("expected 80, got %d", w)
	}

	// Mixed
	w = estimateTextWidth("a你", 40)
	if w != 60 { // 0.5*40 + 1.0*40 = 60
		t.Errorf("expected 60, got %d", w)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ScrollDuration != 12.0 {
		t.Errorf("expected scroll duration 12.0, got %f", cfg.ScrollDuration)
	}
	if cfg.FixedDuration != 5.0 {
		t.Errorf("expected fixed duration 5.0, got %f", cfg.FixedDuration)
	}
	if cfg.Opacity != 0.7 {
		t.Errorf("expected opacity 0.7, got %f", cfg.Opacity)
	}
	if cfg.BlockTop || cfg.BlockBottom || cfg.BlockScroll {
		t.Error("block options should default to false")
	}
}

// === 密集弹幕碰撞测试 ===

func TestXMLToASS_DenseDanmaku(t *testing.T) {
	// 生成大量同时间弹幕，验证碰撞检测不会让弹幕重叠
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><i>`)
	for i := 0; i < 100; i++ {
		sb.WriteString(`<d p="0.0,1,25,16777215,1609459200,0,abc,`)
		sb.WriteString(strings.Repeat("1", 3))
		sb.WriteString(`">弹幕`)
		sb.WriteString(strings.Repeat("x", 5))
		sb.WriteString(`</d>`)
	}
	sb.WriteString(`</i>`)

	xmlPath := writeTempXML(t, sb.String())
	defer os.Remove(xmlPath)

	assPath := xmlPath + ".ass"
	defer os.Remove(assPath)

	err := XMLToASS(xmlPath, assPath, 1920, 1080)
	if err != nil {
		t.Fatalf("XMLToASS dense: %v", err)
	}

	data, _ := os.ReadFile(assPath)
	content := string(data)

	// 应该有一些 Dialogue 行，但不是全部 100 条（因为泳道满了会丢弃）
	dialogueCount := strings.Count(content, "Dialogue:")
	if dialogueCount == 0 {
		t.Error("expected at least some dialogue lines")
	}
	if dialogueCount >= 100 {
		t.Errorf("expected some danmaku to be dropped due to collision, got %d", dialogueCount)
	}
	t.Logf("Dense test: %d/100 danmaku rendered", dialogueCount)
}
