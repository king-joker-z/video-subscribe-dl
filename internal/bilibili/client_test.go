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
