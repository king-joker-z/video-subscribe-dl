package phscheduler

import (
	"log"
	"strings"
)

// getCookie 线程安全地读取当前 Cookie
func (s *PHScheduler) getCookie() string {
	s.cookieMu.RLock()
	defer s.cookieMu.RUnlock()
	return s.cookie
}

// setCookie 线程安全地写入 Cookie
func (s *PHScheduler) setCookie(cookie string) {
	s.cookieMu.Lock()
	defer s.cookieMu.Unlock()
	s.cookie = cookie
}

// LoadUserCookie 从 DB 加载用户配置的 Pornhub Cookie 并应用到内存
func (s *PHScheduler) LoadUserCookie() {
	cookie, err := s.db.GetSetting("ph_cookie")
	if err != nil {
		log.Printf("[phscheduler] 读取用户 Cookie 失败: %v", err)
		return
	}
	cookie = strings.TrimSpace(cookie)
	if cookie != "" {
		s.setCookie(cookie)
		log.Printf("[phscheduler] 已加载用户配置的 PH Cookie（长度: %d）", len(cookie))
	} else {
		s.setCookie("")
		log.Printf("[phscheduler] 未配置用户 Cookie，将使用匿名模式")
	}
}

// RefreshCookie 热更新：从 DB 重新加载并应用 PH Cookie
func (s *PHScheduler) RefreshCookie(cookie string) {
	if cookie != "" {
		s.setCookie(strings.TrimSpace(cookie))
		log.Printf("[phscheduler] PH Cookie 已热更新（长度: %d）", len(s.getCookie()))
		return
	}
	// cookie 为空时从 DB 重新加载
	s.LoadUserCookie()
}
