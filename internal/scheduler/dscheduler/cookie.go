package dscheduler

import (
	"log"
	"strings"

	"video-subscribe-dl/internal/douyin"
)

// LoadUserCookie 从 DB 加载用户配置的抖音 Cookie 并应用到全局 Cookie 管理器
func (s *DouyinScheduler) LoadUserCookie() {
	cookie, err := s.db.GetSetting("douyin_cookie")
	if err != nil {
		log.Printf("[dscheduler] 读取用户 Cookie 失败: %v", err)
		return
	}
	cookie = strings.TrimSpace(cookie)
	if cookie != "" {
		douyin.SetGlobalUserCookie(cookie)
		log.Printf("[dscheduler] 已加载用户配置的抖音 Cookie（长度: %d）", len(cookie))
	} else {
		log.Printf("[dscheduler] 未配置用户 Cookie，将使用自动生成模式")
	}
}

// RefreshCookie 热更新：从 DB 重新加载并应用抖音 Cookie
func (s *DouyinScheduler) RefreshCookie(_ string) {
	s.LoadUserCookie()
}
