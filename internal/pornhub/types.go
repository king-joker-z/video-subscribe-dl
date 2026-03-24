package pornhub

import (
	"encoding/json"
	"time"
)

// Video Pornhub 视频信息
type Video struct {
	ViewKey   string    // URL viewkey 参数，作为唯一 ID
	Title     string    // 视频标题
	URL       string    // 视频页面完整 URL
	Thumbnail string    // 封面图 URL
	Duration  int       // 秒
	CreatedAt time.Time // 发布时间（可能为零值，若页面无此字段）
}

// ModelInfo Pornhub 博主信息
type ModelInfo struct {
	Name     string // 博主名称
	ModelURL string // 博主主页 URL（不含 /videos）
}

// MediaDefinition 视频媒体定义（来自 flashvars.mediaDefinitions）
// quality 字段在不同视频中可能是 string 或 []string，用 RawMessage 兼容
type MediaDefinition struct {
	VideoURL string          `json:"videoUrl"`
	Format   string          `json:"format"`
	Quality  json.RawMessage `json:"quality"` // 可能是 "720p" 或 ["720p","1080p"]
}

// VideoQuality 从间接 URL 返回的画质条目
type VideoQuality struct {
	VideoURL string          `json:"videoUrl"`
	Quality  json.RawMessage `json:"quality"` // 同 MediaDefinition，可能是 string 或数组
	Format   string          `json:"format"`
}

// FlashVars flashvars JS 对象关键字段
type FlashVars struct {
	MediaDefinitions []MediaDefinition `json:"mediaDefinitions"`
	VideoTitle       string            `json:"video_title"`
	ImageURL         string            `json:"image_url"`
	VideoDuration    int               `json:"video_duration"`
}
