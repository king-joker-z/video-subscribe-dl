package xchina

import "time"

// Video xchina 视频信息
type Video struct {
	VideoID   string    // URL 中的 hex ID，如 693f227d31c38
	Title     string    // 视频标题
	PageURL   string    // 视频页面完整 URL，如 https://en.xchina.co/video/id-xxx.html
	Thumbnail string    // 封面图 URL
	CreatedAt time.Time // 发布时间（可能为零值）
}

// ModelInfo xchina 模特信息
type ModelInfo struct {
	ModelID  string // URL 中的 model ID
	Name     string // 模特名称
	ModelURL string // 模特主页 URL，如 https://en.xchina.co/model/id-xxx
}
