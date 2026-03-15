package bilibili

import (
	"testing"
)

// === TestExtractMID ===

func TestExtractMID_NormalURL(t *testing.T) {
	mid, err := ExtractMID("https://space.bilibili.com/12345678")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 12345678 {
		t.Errorf("expected 12345678, got %d", mid)
	}
}

func TestExtractMID_WithPath(t *testing.T) {
	mid, err := ExtractMID("https://space.bilibili.com/99887766/video")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 99887766 {
		t.Errorf("expected 99887766, got %d", mid)
	}
}

func TestExtractMID_WithQueryParams(t *testing.T) {
	mid, err := ExtractMID("https://space.bilibili.com/555666/favlist?fid=123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 555666 {
		t.Errorf("expected 555666, got %d", mid)
	}
}

func TestExtractMID_InvalidURL(t *testing.T) {
	_, err := ExtractMID("https://www.bilibili.com/video/BV123")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestExtractMID_EmptyString(t *testing.T) {
	_, err := ExtractMID("")
	if err == nil {
		t.Fatal("expected error for empty string, got nil")
	}
}

// === TestSanitizePath ===

func TestSanitizePath_Normal(t *testing.T) {
	result := SanitizePath("hello world")
	if result != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", result)
	}
}

func TestSanitizePath_SpecialChars(t *testing.T) {
	result := SanitizePath("file<>:\"/\\|?*name")
	if result != "file_________name" {
		t.Errorf("expected 'file_________name', got '%s'", result)
	}
}

func TestSanitizePath_LongName(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "a"
	}
	result := SanitizePath(long)
	if len(result) != 80 {
		t.Errorf("expected length 80, got %d", len(result))
	}
}

func TestSanitizePath_Empty(t *testing.T) {
	result := SanitizePath("")
	if result != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", result)
	}
}

func TestSanitizePath_DotOnly(t *testing.T) {
	result := SanitizePath(".")
	if result != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", result)
	}
}

func TestSanitizePath_DoubleDot(t *testing.T) {
	result := SanitizePath("..")
	if result != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", result)
	}
}

func TestSanitizePath_WhitespaceOnly(t *testing.T) {
	result := SanitizePath("   ")
	if result != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", result)
	}
}

// === TestExtractFavoriteInfo (ParseVideoURL equivalent) ===

func TestExtractFavoriteInfo_Full(t *testing.T) {
	mid, mediaID, err := ExtractFavoriteInfo("https://space.bilibili.com/12345/favlist?fid=6789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 12345 {
		t.Errorf("expected mid 12345, got %d", mid)
	}
	if mediaID != 6789 {
		t.Errorf("expected mediaID 6789, got %d", mediaID)
	}
}

func TestExtractFavoriteInfo_NoFid(t *testing.T) {
	mid, mediaID, err := ExtractFavoriteInfo("https://space.bilibili.com/12345/favlist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 12345 {
		t.Errorf("expected mid 12345, got %d", mid)
	}
	if mediaID != 0 {
		t.Errorf("expected mediaID 0, got %d", mediaID)
	}
}

func TestExtractFavoriteInfo_InvalidURL(t *testing.T) {
	_, _, err := ExtractFavoriteInfo("https://www.bilibili.com/video/BV123")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

// === TestDurationStr ===

func TestDurationStr_Normal(t *testing.T) {
	got := DurationStr("3:45")
	if got != 225 {
		t.Errorf("expected 225, got %d", got)
	}
}

func TestDurationStr_Zero(t *testing.T) {
	got := DurationStr("0:00")
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestDurationStr_LargeMinutes(t *testing.T) {
	got := DurationStr("120:30")
	if got != 7230 {
		t.Errorf("expected 7230, got %d", got)
	}
}

func TestDurationStr_InvalidFormat(t *testing.T) {
	got := DurationStr("invalid")
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

// === TestExtractWatchLaterInfo ===

func TestExtractWatchLaterInfo_WithMID(t *testing.T) {
	mid, err := ExtractWatchLaterInfo("watchlater://12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 12345 {
		t.Errorf("expected 12345, got %d", mid)
	}
}

func TestExtractWatchLaterInfo_Empty(t *testing.T) {
	mid, err := ExtractWatchLaterInfo("watchlater://")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 0 {
		t.Errorf("expected 0, got %d", mid)
	}
}

func TestExtractWatchLaterInfo_NotWatchLater(t *testing.T) {
	mid, err := ExtractWatchLaterInfo("https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 0 {
		t.Errorf("expected 0, got %d", mid)
	}
}

func TestSanitizePath_ReplacementChar(t *testing.T) {
	result := SanitizePath("hello" + string([]rune{0xFFFD}) + "world")
	if result != "helloworld" {
		t.Errorf("expected 'helloworld', got '%s'", result)
	}
}

func TestSanitizePath_ZeroWidthSpace(t *testing.T) {
	result := SanitizePath("hello" + string(rune(0x200B)) + "world")
	if result != "helloworld" {
		t.Errorf("expected 'helloworld', got '%s'", result)
	}
}

func TestSanitizePath_BOM(t *testing.T) {
	result := SanitizePath(string([]rune{0xFEFF}) + "hello")
	if result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}
}

func TestSanitizePath_ControlChars(t *testing.T) {
	result := SanitizePath("hello" + string([]rune{0x00, 0x01, 0x1F}) + "world")
	if result != "helloworld" {
		t.Errorf("expected 'helloworld', got '%s'", result)
	}
}

func TestSanitizePath_VariationSelector(t *testing.T) {
	// U+FE0F emoji 变体选择器应被移除
	result := SanitizePath("star" + string(rune(0xFE0F)) + "test")
	if result != "startest" {
		t.Errorf("expected 'startest', got '%s'", result)
	}
}

func TestSanitizePath_MixedInvisible(t *testing.T) {
	// 零宽字符 + 替换字符混合
	result := SanitizePath("hello" + string(rune(0x200B)) + "world" + string(rune(0xFFFD)) + "test")
	if result != "helloworldtest" {
		t.Errorf("expected 'helloworldtest', got '%s'", result)
	}
}

func TestSanitizePath_ConsecutiveSpaces(t *testing.T) {
	result := SanitizePath("hello   world")
	if result != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", result)
	}
}

func TestSanitizePath_LeadingTrailingSpaces(t *testing.T) {
	result := SanitizePath("  hello  ")
	if result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}
}

func TestSanitizePath_RuneTruncation(t *testing.T) {
	// 中文字符（每个 3 字节），100 个 rune 应截断到 80 rune
	long := ""
	for i := 0; i < 100; i++ {
		long += "中"
	}
	result := SanitizePath(long)
	runes := []rune(result)
	if len(runes) != 80 {
		t.Errorf("expected 80 runes, got %d", len(runes))
	}
}

func TestSanitizePath_C1ControlChars(t *testing.T) {
	result := SanitizePath("hello" + string([]rune{0x0080, 0x009F}) + "world")
	if result != "helloworld" {
		t.Errorf("expected 'helloworld', got '%s'", result)
	}
}

func TestSanitizePath_InvisibleFormatChars(t *testing.T) {
	// U+2060 word joiner, U+206F nominal digit shapes
	result := SanitizePath("hello" + string([]rune{0x2060, 0x206F}) + "world")
	if result != "helloworld" {
		t.Errorf("expected 'helloworld', got '%s'", result)
	}
}

func TestSanitizePath_LineSeparator(t *testing.T) {
	// U+2028 line separator, U+2029 paragraph separator
	result := SanitizePath("hello" + string([]rune{0x2028, 0x2029}) + "world")
	if result != "helloworld" {
		t.Errorf("expected 'helloworld', got '%s'", result)
	}
}

// === TestExtractBVID ===

func TestExtractBVID_RawBV(t *testing.T) {
	bvid, avid, err := ExtractBVID("BV1xx411c7mD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bvid != "BV1xx411c7mD" {
		t.Errorf("expected BV1xx411c7mD, got %s", bvid)
	}
	if avid != 0 {
		t.Errorf("expected avid 0, got %d", avid)
	}
}

func TestExtractBVID_FullURL(t *testing.T) {
	bvid, _, err := ExtractBVID("https://www.bilibili.com/video/BV1GJ411x7h7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bvid != "BV1GJ411x7h7" {
		t.Errorf("expected BV1GJ411x7h7, got %s", bvid)
	}
}

func TestExtractBVID_WithParams(t *testing.T) {
	bvid, _, err := ExtractBVID("https://www.bilibili.com/video/BV1GJ411x7h7?p=2&vd_source=abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bvid != "BV1GJ411x7h7" {
		t.Errorf("expected BV1GJ411x7h7, got %s", bvid)
	}
}

func TestExtractBVID_AV(t *testing.T) {
	bvid, avid, err := ExtractBVID("av12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bvid != "" {
		t.Errorf("expected empty bvid, got %s", bvid)
	}
	if avid != 12345 {
		t.Errorf("expected avid 12345, got %d", avid)
	}
}

func TestExtractBVID_AVURL(t *testing.T) {
	bvid, avid, err := ExtractBVID("https://www.bilibili.com/video/av99887766")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bvid != "" {
		t.Errorf("expected empty bvid, got %s", bvid)
	}
	if avid != 99887766 {
		t.Errorf("expected avid 99887766, got %d", avid)
	}
}

func TestExtractBVID_Empty(t *testing.T) {
	_, _, err := ExtractBVID("")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestExtractBVID_InvalidURL(t *testing.T) {
	_, _, err := ExtractBVID("https://www.youtube.com/watch?v=abc")
	if err == nil {
		t.Error("expected error for YouTube URL")
	}
}
