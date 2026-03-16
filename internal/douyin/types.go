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
	IsNote       bool       `json:"is_note"`        // 是否为图集/笔记
	Images       []string   `json:"images"`         // 图集图片 URL 列表
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


// DouyinUserProfile 抖音用户详细信息（来自 profile API）
type DouyinUserProfile struct {
	UID            string `json:"uid"`
	SecUID         string `json:"sec_uid"`
	ShortID        string `json:"short_id"`       // 抖音号（短 ID）
	UniqueID       string `json:"unique_id"`       // 抖音号（自定义）
	Nickname       string `json:"nickname"`
	Signature      string `json:"signature"`       // 个人简介
	AvatarURL      string `json:"avatar_url"`      // 头像 URL（大图）
	FollowerCount  int64  `json:"follower_count"`  // 粉丝数
	FollowingCount int64  `json:"following_count"` // 关注数
	TotalFavorited int64  `json:"total_favorited"` // 获赞总数
	AwemeCount     int64  `json:"aweme_count"`     // 作品数
	FavoritingCount int64 `json:"favoriting_count"` // 喜欢数
	IPLocation     string `json:"ip_location"`     // IP 属地
}
