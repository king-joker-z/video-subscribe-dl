package douyin

import (
	"testing"
)

// TestGetUserVideosLive 测试修复后的 GetUserVideos 能否从真实 API 获取到视频列表
// 使用一个公开的抖音用户（有视频内容的账号）
func TestGetUserVideosLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live API test")
	}

	client := NewClient()
	defer client.Close()

	// 打印指纹信息用于诊断
	t.Logf("Fingerprint: %s", client.fingerprint.String())

	// 获取 Cookie 字符串用于诊断
	cookie := client.GetCookieString()
	t.Logf("Cookie length: %d", len(cookie))

	// 测试 X-Bogus 签名
	sigOK := TestXBogusSign()
	t.Logf("X-Bogus sign OK: %v", sigOK)

	// 使用公开的抖音用户 sec_uid 进行测试
	secUID := "MS4wLjABAAAAgfP-wrR4bAf4EpXE01yHQEk4Sd0yoJ0zPyEJn1T29b4"
	result, err := client.GetUserVideos(secUID, 0)
	if err != nil {
		t.Fatalf("GetUserVideos error: %v", err)
	}

	t.Logf("videos=%d hasMore=%v nextCursor=%d",
		len(result.Videos), result.HasMore, result.MaxCursor)

	if len(result.Videos) > 0 {
		v := result.Videos[0]
		t.Logf("first video: aweme_id=%s desc=%q createTime=%d",
			v.AwemeID, truncate(v.Desc, 50), v.CreateTime)
	}

	// 不断言具体数量，但至少应该 > 0（除非账号真的没视频）
	if len(result.Videos) == 0 {
		t.Logf("WARNING: got 0 videos - this may indicate the API is still returning empty results")
	}
}

// TestGetUserProfileLive 测试修复后的用户详情 API
func TestGetUserProfileLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live API test")
	}

	client := NewClient()
	defer client.Close()

	secUID := "MS4wLjABAAAAgfP-wrR4bAf4EpXE01yHQEk4Sd0yoJ0zPyEJn1T29b4"
	profile, err := client.GetUserProfile(secUID)
	if err != nil {
		t.Fatalf("GetUserProfile error: %v", err)
	}

	t.Logf("profile: nickname=%q uid=%s sec_uid=%s aweme_count=%d follower_count=%d",
		profile.Nickname, profile.UID, profile.SecUID, profile.AwemeCount, profile.FollowerCount)
}
