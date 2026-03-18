package dscheduler

import (
	"fmt"
	"strings"

	"video-subscribe-dl/internal/douyin"
)

// resolveDouyinSecUID 从 source URL 解析 sec_user_id
func (s *DouyinScheduler) resolveDouyinSecUID(client DouyinAPI, rawURL string) (string, error) {
	secUID, err := douyin.ExtractSecUID(rawURL)
	if err == nil {
		return secUID, nil
	}

	result, err := client.ResolveShareURL(rawURL)
	if err != nil {
		return "", err
	}

	if result.Type == douyin.URLTypeUser && result.SecUID != "" {
		return result.SecUID, nil
	}

	return "", fmt.Errorf("URL is not a douyin user page: %s", rawURL)
}

// getDouyinSetting 获取抖音平台配置，优先 douyin_ 前缀
func (s *DouyinScheduler) getDouyinSetting(key string) string {
	if val, err := s.db.GetSetting("douyin_" + key); err == nil && val != "" {
		return val
	}
	if val, err := s.db.GetSetting(key); err == nil && val != "" {
		return val
	}
	return ""
}

// parseMixID 从 URL 中提取 mix_id
func parseMixID(rawURL string) string {
	const collectionPrefix = "/collection/"
	if idx := strings.Index(rawURL, collectionPrefix); idx >= 0 {
		rest := rawURL[idx+len(collectionPrefix):]
		if i := strings.IndexAny(rest, "?#/"); i >= 0 {
			rest = rest[:i]
		}
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rawURL)
}
