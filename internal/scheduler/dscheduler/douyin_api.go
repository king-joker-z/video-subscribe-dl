package dscheduler

import "video-subscribe-dl/internal/douyin"

// DouyinAPI 定义了 checkDouyin/fullScanDouyin/retryOneDouyinDownload 中使用的抖音客户端方法。
// 生产环境使用 douyin.DouyinClient 实现，测试使用 mock 实现。
type DouyinAPI interface {
	Close()
	ValidateCookie() (bool, string)
	GetUserVideos(secUID string, maxCursor int64, consecutiveErrors ...int) (*douyin.UserVideosResult, error)
	GetUserProfile(secUID string) (*douyin.DouyinUserProfile, error)
	ResolveShareURL(shareURL string) (*douyin.ResolveResult, error)
	GetVideoDetail(videoID string) (*douyin.DouyinVideo, error)
	ResolveVideoURL(videoURL string) (string, error)
	GetMixVideos(mixID string) ([]douyin.DouyinVideo, error)
}

// douyinClientAdapter 将 *douyin.DouyinClient 适配到 DouyinAPI 接口
type douyinClientAdapter struct {
	*douyin.DouyinClient
}

// 默认工厂函数，创建真正的 DouyinClient
func defaultNewDouyinClient() DouyinAPI {
	return &douyinClientAdapter{douyin.NewClient()}
}
