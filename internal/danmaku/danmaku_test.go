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
