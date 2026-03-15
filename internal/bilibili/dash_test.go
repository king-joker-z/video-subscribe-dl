package bilibili

import (
	"testing"
)

// === TestSelectBestVideo ===

func TestSelectBestVideo_HighestResolution(t *testing.T) {
	streams := []DashStream{
		{ID: 32, Height: 480, Width: 854, Bandwidth: 500000, CodecID: CodecAVC, Codecs: "avc"},
		{ID: 64, Height: 720, Width: 1280, Bandwidth: 1000000, CodecID: CodecAVC, Codecs: "avc"},
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 2000000, CodecID: CodecAVC, Codecs: "avc"},
	}
	best := SelectBestVideo(streams, "", 0)
	if best == nil {
		t.Fatal("expected non-nil result")
	}
	if best.Height != 1080 {
		t.Errorf("expected height 1080, got %d", best.Height)
	}
}

func TestSelectBestVideo_WithMaxHeight(t *testing.T) {
	streams := []DashStream{
		{ID: 32, Height: 480, Width: 854, Bandwidth: 500000, CodecID: CodecAVC},
		{ID: 64, Height: 720, Width: 1280, Bandwidth: 1000000, CodecID: CodecAVC},
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 2000000, CodecID: CodecAVC},
	}
	best := SelectBestVideo(streams, "", 720)
	if best == nil {
		t.Fatal("expected non-nil result")
	}
	if best.Height != 720 {
		t.Errorf("expected height 720, got %d", best.Height)
	}
}

func TestSelectBestVideo_PreferCodec(t *testing.T) {
	streams := []DashStream{
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 2000000, CodecID: CodecAVC, Codecs: "avc"},
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 1500000, CodecID: CodecHEVC, Codecs: "hevc"},
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 1200000, CodecID: CodecAV1, Codecs: "av1"},
	}
	best := SelectBestVideo(streams, "hevc", 0)
	if best == nil {
		t.Fatal("expected non-nil result")
	}
	if best.CodecID != CodecHEVC {
		t.Errorf("expected HEVC codec, got codecID %d", best.CodecID)
	}
}

func TestSelectBestVideo_PreferCodecH265Alias(t *testing.T) {
	streams := []DashStream{
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 2000000, CodecID: CodecAVC},
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 1500000, CodecID: CodecHEVC},
	}
	best := SelectBestVideo(streams, "h265", 0)
	if best == nil {
		t.Fatal("expected non-nil result")
	}
	if best.CodecID != CodecHEVC {
		t.Errorf("expected HEVC, got %d", best.CodecID)
	}
}

func TestSelectBestVideo_EmptyStreams(t *testing.T) {
	best := SelectBestVideo(nil, "", 0)
	if best != nil {
		t.Errorf("expected nil for empty streams, got %v", best)
	}
}

func TestSelectBestVideo_MaxHeightNoMatch(t *testing.T) {
	// When maxHeight is set but no streams match, should fall back to all streams
	streams := []DashStream{
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 2000000, CodecID: CodecAVC},
	}
	best := SelectBestVideo(streams, "", 360)
	if best == nil {
		t.Fatal("expected non-nil (fallback to all streams)")
	}
	if best.Height != 1080 {
		t.Errorf("expected fallback to 1080, got %d", best.Height)
	}
}

func TestSelectBestVideo_SameHeightPicksHigherBandwidth(t *testing.T) {
	streams := []DashStream{
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 1000000, CodecID: CodecAVC},
		{ID: 80, Height: 1080, Width: 1920, Bandwidth: 3000000, CodecID: CodecHEVC},
	}
	best := SelectBestVideo(streams, "", 0)
	if best == nil {
		t.Fatal("expected non-nil")
	}
	if best.Bandwidth != 3000000 {
		t.Errorf("expected bandwidth 3000000, got %d", best.Bandwidth)
	}
}

// === TestSelectBestAudio ===

func TestSelectBestAudio_HiResPriority(t *testing.T) {
	streams := []DashStream{
		{ID: Audio64K, Bandwidth: 64000, Codecs: "mp4a"},
		{ID: Audio132K, Bandwidth: 132000, Codecs: "mp4a"},
		{ID: AudioHiRes, Bandwidth: 320000, Codecs: "flac"},
	}
	best := SelectBestAudio(streams)
	if best == nil {
		t.Fatal("expected non-nil")
	}
	if best.ID != AudioHiRes {
		t.Errorf("expected Hi-Res (%d), got %d", AudioHiRes, best.ID)
	}
}

func TestSelectBestAudio_DolbyOverNormal(t *testing.T) {
	streams := []DashStream{
		{ID: Audio192K, Bandwidth: 192000, Codecs: "mp4a"},
		{ID: AudioDolby, Bandwidth: 256000, Codecs: "ec-3"},
	}
	best := SelectBestAudio(streams)
	if best == nil {
		t.Fatal("expected non-nil")
	}
	if best.ID != AudioDolby {
		t.Errorf("expected Dolby (%d), got %d", AudioDolby, best.ID)
	}
}

func TestSelectBestAudio_192kOver132k(t *testing.T) {
	streams := []DashStream{
		{ID: Audio132K, Bandwidth: 132000, Codecs: "mp4a"},
		{ID: Audio192K, Bandwidth: 192000, Codecs: "mp4a"},
	}
	best := SelectBestAudio(streams)
	if best == nil {
		t.Fatal("expected non-nil")
	}
	if best.ID != Audio192K {
		t.Errorf("expected 192K (%d), got %d", Audio192K, best.ID)
	}
}

func TestSelectBestAudio_EmptyStreams(t *testing.T) {
	best := SelectBestAudio(nil)
	if best != nil {
		t.Errorf("expected nil for empty streams, got %v", best)
	}
}

func TestSelectBestAudio_UnknownIDFallback(t *testing.T) {
	streams := []DashStream{
		{ID: 99999, Bandwidth: 500000, Codecs: "unknown"},
		{ID: Audio64K, Bandwidth: 64000, Codecs: "mp4a"},
	}
	best := SelectBestAudio(streams)
	if best == nil {
		t.Fatal("expected non-nil")
	}
	// Known IDs should be preferred over unknown
	if best.ID != Audio64K {
		t.Errorf("expected known Audio64K (%d) over unknown, got %d", Audio64K, best.ID)
	}
}

// === TestGetAllPages ===

func TestGetAllPages_MultiPage(t *testing.T) {
	detail := &VideoDetail{
		Pages: []struct {
			CID  int64  `json:"cid"`
			Page int    `json:"page"`
			Part string `json:"part"`
		}{
			{CID: 100, Page: 1, Part: "Part 1"},
			{CID: 200, Page: 2, Part: "Part 2"},
			{CID: 300, Page: 3, Part: "Part 3"},
		},
	}
	pages := GetAllPages(detail)
	if len(pages) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(pages))
	}
	if pages[0].CID != 100 || pages[0].Page != 1 || pages[0].PartName != "Part 1" {
		t.Errorf("page 0 mismatch: %+v", pages[0])
	}
	if pages[2].CID != 300 {
		t.Errorf("page 2 CID expected 300, got %d", pages[2].CID)
	}
}

func TestGetAllPages_SinglePage(t *testing.T) {
	detail := &VideoDetail{
		Pages: []struct {
			CID  int64  `json:"cid"`
			Page int    `json:"page"`
			Part string `json:"part"`
		}{
			{CID: 555, Page: 1, Part: ""},
		},
	}
	pages := GetAllPages(detail)
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
}

func TestGetAllPages_Empty(t *testing.T) {
	detail := &VideoDetail{}
	pages := GetAllPages(detail)
	if len(pages) != 0 {
		t.Errorf("expected 0 pages, got %d", len(pages))
	}
}

// === TestGetVideoCID ===

func TestGetVideoCID_HasPages(t *testing.T) {
	detail := &VideoDetail{
		Pages: []struct {
			CID  int64  `json:"cid"`
			Page int    `json:"page"`
			Part string `json:"part"`
		}{
			{CID: 12345, Page: 1, Part: "main"},
		},
	}
	cid := GetVideoCID(detail)
	if cid != 12345 {
		t.Errorf("expected 12345, got %d", cid)
	}
}

func TestGetVideoCID_NoPages(t *testing.T) {
	detail := &VideoDetail{}
	cid := GetVideoCID(detail)
	if cid != 0 {
		t.Errorf("expected 0, got %d", cid)
	}
}

// === TestFormatVideoInfo / TestFormatAudioInfo ===

func TestFormatVideoInfo(t *testing.T) {
	s := &DashStream{Width: 1920, Height: 1080, CodecID: CodecHEVC, Bandwidth: 2000000}
	info := FormatVideoInfo(s)
	if info != "1920x1080 HEVC/H.265 2000kbps" {
		t.Errorf("unexpected format: %s", info)
	}
}

func TestFormatAudioInfo_HiRes(t *testing.T) {
	s := &DashStream{ID: AudioHiRes, Codecs: "flac", Bandwidth: 320000}
	info := FormatAudioInfo(s)
	if info != "Hi-Res flac 320kbps" {
		t.Errorf("unexpected format: %s", info)
	}
}

// === TestSanitizeFilename ===

func TestSanitizeFilename_SpecialChars(t *testing.T) {
	result := SanitizeFilename("test<>file")
	if result != "test__file" {
		t.Errorf("expected 'test__file', got '%s'", result)
	}
}

func TestSanitizeFilename_Empty(t *testing.T) {
	result := SanitizeFilename("")
	if result != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", result)
	}
}

func TestSanitizeFilename_Long(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	result := SanitizeFilename(long)
	if len(result) != 80 {
		t.Errorf("expected length 80, got %d", len(result))
	}
}
