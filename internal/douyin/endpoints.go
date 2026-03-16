package douyin

import "fmt"

// 抖音 API 端点集中管理
// 所有 API URL 在此统一维护，方便后续 endpoint 变更和 A/B 测试

const (
	// ---- 核心 API ----

	// VideoDetailAPI 视频详情 API（需 X-Bogus 签名 + Cookie）
	VideoDetailAPI = "https://www.douyin.com/aweme/v1/web/aweme/detail/"

	// UserVideosAPI 用户视频列表 API（需 X-Bogus 签名 + Cookie）
	UserVideosAPI = "https://www.douyin.com/aweme/v1/web/aweme/post/"

	// UserProfileAPI 用户详情 API（需 X-Bogus 签名 + Cookie）
	UserProfileAPI = "https://www.douyin.com/aweme/v1/web/user/profile/other/"

	// MixVideosAPI 合集视频列表 API（需 X-Bogus 签名 + Cookie）
	MixVideosAPI = "https://www.douyin.com/aweme/v1/web/mix/aweme/"

	// UserLikedAPI 用户喜欢列表 API（需 X-Bogus 签名 + Cookie）
	UserLikedAPI = "https://www.douyin.com/aweme/v1/web/aweme/favorite/"

	// ---- 降级 API（不需要签名）----

	// VideoPageURL iesdouyin 视频页面（降级方案）
	VideoPageURL = "https://www.iesdouyin.com/share/video/%s"

	// NoteSlideInfoAPI iesdouyin 图集详情 API
	NoteSlideInfoAPI = "https://www.iesdouyin.com/web/api/v2/aweme/slidesinfo/"

	// ---- 认证/Cookie ----


	// TTWidAPI ttwid 注册端点
	TTWidAPI = "https://ttwid.bytedance.com/ttwid/union/register/"

	// ---- 通用 ----

	// DouyinReferer 抖音 Referer 头
	DouyinReferer = "https://www.douyin.com/"

	// DouyinVideoWebURL 抖音视频网页地址模板
	DouyinVideoWebURL = "https://www.douyin.com/video/%s"

	// DouyinNoteWebURL 抖音图集网页地址模板
	DouyinNoteWebURL = "https://www.douyin.com/note/%s"
)

// BuildVideoDetailURL 构建视频详情 API URL
func BuildVideoDetailURL(awemeID string) string {
	return VideoDetailAPI + "?aweme_id=" + awemeID
}

// BuildVideoPageURL 构建 iesdouyin 视频页面 URL
func BuildVideoPageURL(videoID string) string {
	return fmt.Sprintf(VideoPageURL, videoID)
}

// BuildNoteSlideInfoURL 构建图集详情 API URL
func BuildNoteSlideInfoURL(webID, awemeID, aBogus string) string {
	return fmt.Sprintf(
		"%s?reflow_source=reflow_page&web_id=%s&device_id=%s&aweme_ids=%%5B%s%%5D&request_source=200&a_bogus=%s",
		NoteSlideInfoAPI, webID, webID, awemeID, aBogus,
	)
}

// BuildVideoWebURL 构建抖音视频网页地址
func BuildVideoWebURL(videoID string) string {
	return fmt.Sprintf(DouyinVideoWebURL, videoID)
}

// BuildNoteWebURL 构建抖音图集网页地址
func BuildNoteWebURL(videoID string) string {
	return fmt.Sprintf(DouyinNoteWebURL, videoID)
}
