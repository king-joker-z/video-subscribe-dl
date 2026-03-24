package phscheduler

import (
	"log"
	"strings"
)

// LoadUserCookie 从 DB 加载用户配置的 Pornhub Cookie 并应用到内存
func (s *PHScheduler) LoadUserCookie() {
	cookie, err := s.db.GetSetting("ph_cookie")
	if err != nil {
		log.Printf("[phscheduler] 读取用户 Cookie 失败: %v", err)
		return
	}
	cookie = strings.TrimSpace(cookie)
	if cookie != "" {
		s.cookie = cookie
		log.Printf("[phscheduler] 已加载用户配置的 PH Cookie（长度: %d）", len(cookie))
	} else {
		s.cookie = ""
		log.Printf("[phscheduler] 未配置用户 Cookie，将使用匿名模式")
	}
}

// RefreshCookie 热更新：从 DB 重新加载并应用 PH Cookie
func (s *PHScheduler) RefreshCookie(cookie string) {
	if cookie != "" {
		s.cookie = strings.TrimSpace(cookie)
		log.Printf("[phscheduler] PH Cookie 已热更新（长度: %d）", len(s.cookie))
		return
	}
	// cookie 为空时从 DB 重新加载
	s.LoadUserCookie()
}
