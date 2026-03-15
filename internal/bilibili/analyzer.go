package bilibili

import (
	"fmt"
	"sort"
	"strings"
)

// === 画质枚举 ===

// VideoQuality 视频画质
type VideoQuality int

const (
	Video360P    VideoQuality = 16
	Video480P    VideoQuality = 32
	Video720P    VideoQuality = 64
	Video1080P   VideoQuality = 80
	Video1080PPlus VideoQuality = 112 // 1080P+（高码率）
	Video1080P60 VideoQuality = 116
	Video4K      VideoQuality = 120
	VideoHDR     VideoQuality = 125
	VideoDolby   VideoQuality = 126
	Video8K      VideoQuality = 127
)

// VideoQualityOrder 画质排序值（越大越好）
var VideoQualityOrder = map[VideoQuality]int{
	Video360P:      1,
	Video480P:      2,
	Video720P:      3,
	Video1080P:     4,
	Video1080PPlus: 5,
	Video1080P60:   6,
	Video4K:        7,
	VideoHDR:       8,
	VideoDolby:     9,
	Video8K:        10,
}

func (q VideoQuality) String() string {
	switch q {
	case Video360P:
		return "360P"
	case Video480P:
		return "480P"
	case Video720P:
		return "720P"
	case Video1080P:
		return "1080P"
	case Video1080PPlus:
		return "1080P+"
	case Video1080P60:
		return "1080P60"
	case Video4K:
		return "4K"
	case VideoHDR:
		return "HDR"
	case VideoDolby:
		return "Dolby Vision"
	case Video8K:
		return "8K"
	default:
		return fmt.Sprintf("Unknown(%d)", int(q))
	}
}

// AudioQuality 音频画质
type AudioQuality int

const (
	AudioQ64K   AudioQuality = 30216
	AudioQ132K  AudioQuality = 30232
	AudioQ192K  AudioQuality = 30280
	AudioQDolby AudioQuality = 30250
	AudioQHiRes AudioQuality = 30251
)

var AudioQualityOrder = map[AudioQuality]int{
	AudioQ64K:   1,
	AudioQ132K:  2,
	AudioQ192K:  3,
	AudioQDolby: 4,
	AudioQHiRes: 5,
}

func (q AudioQuality) String() string {
	switch q {
	case AudioQ64K:
		return "64K"
	case AudioQ132K:
		return "132K"
	case AudioQ192K:
		return "192K"
	case AudioQDolby:
		return "Dolby Audio"
	case AudioQHiRes:
		return "Hi-Res"
	default:
		return fmt.Sprintf("Unknown(%d)", int(q))
	}
}

// VideoCodec 视频编码
type VideoCodec int

const (
	CodecAVC_  VideoCodec = 7
	CodecHEVC_ VideoCodec = 12
	CodecAV1_  VideoCodec = 13
)

func (c VideoCodec) String() string {
	switch c {
	case CodecAVC_:
		return "AVC/H.264"
	case CodecHEVC_:
		return "HEVC/H.265"
	case CodecAV1_:
		return "AV1"
	default:
		return fmt.Sprintf("Unknown(%d)", int(c))
	}
}

// === FilterOption 流过滤选项 ===

// FilterOption 视频流过滤配置
type FilterOption struct {
	VideoMaxQuality VideoQuality // 0 = 不限制
	VideoMinQuality VideoQuality // 0 = 不限制
	AudioMaxQuality AudioQuality // 0 = 不限制
	AudioMinQuality AudioQuality // 0 = 不限制
	PreferCodecs    []VideoCodec // 编码偏好顺序，空 = 不限制
	NoDolbyVideo    bool         // 排除杜比视界
	NoDolbyAudio    bool         // 排除杜比音效
	NoHDR           bool         // 排除 HDR
	NoHiRes         bool         // 排除 Hi-Res 音频
}

// DefaultFilterOption 默认过滤选项（不做任何限制）
func DefaultFilterOption() FilterOption {
	return FilterOption{}
}

// === BestStream 选择结果 ===

// BestStream 最佳流选择结果
type BestStream struct {
	Video *DashStream
	Audio *DashStream // 可能为 nil（混合流时）
}

// === 分析器 ===

// SelectBestStream 从 DASH 结果中选择最佳视频+音频流
// 参考 bili-sync analyzer.rs 的 best_stream 逻辑
func SelectBestStream(dash *DashResult, opt FilterOption) (*BestStream, error) {
	video := selectBestVideoStream(dash.Video, opt)
	if video == nil {
		return nil, NewVideoStreamsEmpty()
	}

	audio := selectBestAudioStream(dash.Audio, opt)
	// 音频可能为空（某些纯视频流）

	return &BestStream{Video: video, Audio: audio}, nil
}

// selectBestVideoStream 在过滤范围内选最高画质视频流
func selectBestVideoStream(streams []DashStream, opt FilterOption) *DashStream {
	if len(streams) == 0 {
		return nil
	}

	// 过滤
	var filtered []DashStream
	for _, s := range streams {
		q := VideoQuality(s.ID)

		// 画质范围过滤
		if opt.VideoMaxQuality > 0 {
			maxOrder := VideoQualityOrder[opt.VideoMaxQuality]
			curOrder := VideoQualityOrder[q]
			if maxOrder > 0 && curOrder > maxOrder {
				continue
			}
		}
		if opt.VideoMinQuality > 0 {
			minOrder := VideoQualityOrder[opt.VideoMinQuality]
			curOrder := VideoQualityOrder[q]
			if minOrder > 0 && curOrder > 0 && curOrder < minOrder {
				continue
			}
		}

		// 排除杜比视界
		if opt.NoDolbyVideo && q == VideoDolby {
			continue
		}
		// 排除 HDR
		if opt.NoHDR && q == VideoHDR {
			continue
		}

		filtered = append(filtered, s)
	}

	if len(filtered) == 0 {
		// 没有符合条件的，回退到全部
		filtered = streams
	}

	// 排序：画质降序，同画质按编码偏好
	codecPriority := buildCodecPriority(opt.PreferCodecs)

	sort.SliceStable(filtered, func(i, j int) bool {
		qi := videoQualityOrderVal(VideoQuality(filtered[i].ID))
		qj := videoQualityOrderVal(VideoQuality(filtered[j].ID))
		if qi != qj {
			return qi > qj // 画质高的优先
		}
		// 同画质按编码偏好
		ci := codecPriority[VideoCodec(filtered[i].CodecID)]
		cj := codecPriority[VideoCodec(filtered[j].CodecID)]
		if ci != cj {
			return ci < cj // 优先级数字小的在前
		}
		return filtered[i].Bandwidth > filtered[j].Bandwidth
	})

	return &filtered[0]
}

// selectBestAudioStream 在过滤范围内选最高音质音频流
func selectBestAudioStream(streams []DashStream, opt FilterOption) *DashStream {
	if len(streams) == 0 {
		return nil
	}

	var filtered []DashStream
	for _, s := range streams {
		q := AudioQuality(s.ID)

		if opt.AudioMaxQuality > 0 {
			maxOrder := AudioQualityOrder[opt.AudioMaxQuality]
			curOrder := AudioQualityOrder[q]
			if maxOrder > 0 && curOrder > maxOrder {
				continue
			}
		}
		if opt.AudioMinQuality > 0 {
			minOrder := AudioQualityOrder[opt.AudioMinQuality]
			curOrder := AudioQualityOrder[q]
			if minOrder > 0 && curOrder > 0 && curOrder < minOrder {
				continue
			}
		}

		if opt.NoDolbyAudio && q == AudioQDolby {
			continue
		}
		if opt.NoHiRes && q == AudioQHiRes {
			continue
		}

		filtered = append(filtered, s)
	}

	if len(filtered) == 0 {
		filtered = streams
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		qi := audioQualityOrderVal(AudioQuality(filtered[i].ID))
		qj := audioQualityOrderVal(AudioQuality(filtered[j].ID))
		if qi != qj {
			return qi > qj
		}
		return filtered[i].Bandwidth > filtered[j].Bandwidth
	})

	return &filtered[0]
}

// === 辅助函数 ===

func videoQualityOrderVal(q VideoQuality) int {
	if v, ok := VideoQualityOrder[q]; ok {
		return v
	}
	return 0
}

func audioQualityOrderVal(q AudioQuality) int {
	if v, ok := AudioQualityOrder[q]; ok {
		return v
	}
	return 0
}

func buildCodecPriority(prefer []VideoCodec) map[VideoCodec]int {
	m := map[VideoCodec]int{
		CodecAVC_:  50,
		CodecHEVC_: 50,
		CodecAV1_:  50,
	}
	for i, c := range prefer {
		m[c] = i + 1
	}
	return m
}

// ParseCodecPreference 解析编码偏好字符串 "hevc,av1,avc"
func ParseCodecPreference(s string) []VideoCodec {
	if s == "" {
		return nil
	}
	var codecs []VideoCodec
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		switch part {
		case "avc", "h264", "h.264":
			codecs = append(codecs, CodecAVC_)
		case "hevc", "h265", "h.265":
			codecs = append(codecs, CodecHEVC_)
		case "av1":
			codecs = append(codecs, CodecAV1_)
		}
	}
	return codecs
}

// ParseVideoQuality 解析画质字符串
func ParseVideoQuality(s string) VideoQuality {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "360p":
		return Video360P
	case "480p":
		return Video480P
	case "720p":
		return Video720P
	case "1080p":
		return Video1080P
	case "1080p+", "1080phigh":
		return Video1080PPlus
	case "1080p60":
		return Video1080P60
	case "4k", "2160p":
		return Video4K
	case "hdr":
		return VideoHDR
	case "dolby":
		return VideoDolby
	case "8k", "4320p":
		return Video8K
	}
	return 0
}
