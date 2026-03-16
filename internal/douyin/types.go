package douyin

import "time"

// DouyinVideo 抖音视频信息
type DouyinVideo struct {
	AwemeID      string     `json:"aweme_id"`      // 视频 ID
	Desc         string     `json:"desc"`           // 视频描述/标题
	CreateTime   int64      `json:"create_time"`    // 发布时间戳（秒）
	Author       DouyinUser `json:"author"`         // 作者信息
	Cover        string     `json:"cover"`          // 封面图 URL
	VideoURL     string     `json:"video_url"`      // 无水印视频下载 URL
	Duration     int        `json:"duration"`       // 时长（毫秒）
	DiggCount    int64      `json:"digg_count"`     // 点赞数
	ShareCount   int64      `json:"share_count"`    // 分享数
	CommentCount int64      `json:"comment_count"`  // 评论数
}

// CreateTimeUnix 返回发布时间
func (v *DouyinVideo) CreateTimeUnix() time.Time {
	return time.Unix(v.CreateTime, 0)
}

// DouyinUser 抖音用户信息
type DouyinUser struct {
	UID       string `json:"uid"`
	SecUID    string `json:"sec_uid"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
	Signature string `json:"signature"`
}

// UserVideosResult 用户视频列表返回
type UserVideosResult struct {
	Videos    []DouyinVideo `json:"videos"`
	HasMore   bool          `json:"has_more"`
	MaxCursor int64         `json:"max_cursor"`
}
