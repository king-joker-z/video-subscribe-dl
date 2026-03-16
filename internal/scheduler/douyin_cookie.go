package scheduler

import (
	"log"
	"strings"

	"video-subscribe-dl/internal/douyin"
)

// loadDouyinUserCookie 从 DB 加载用户配置的抖音 Cookie 并应用到全局 Cookie 管理器
// 这是抖音独有的 Cookie 管理，和 B 站的凭证系统完全独立
func (s *Scheduler) loadDouyinUserCookie() {
	cookie, err := s.db.GetSetting("douyin_cookie")
	if err != nil {
		log.Printf("[douyin-cookie] 读取用户 Cookie 失败: %v", err)
		return
	}
	cookie = strings.TrimSpace(cookie)
	if cookie != "" {
		douyin.SetGlobalUserCookie(cookie)
		log.Printf("[douyin-cookie] 已加载用户配置的抖音 Cookie（长度: %d）", len(cookie))
	} else {
		log.Printf("[douyin-cookie] 未配置用户 Cookie，将使用自动生成模式")
	}
}

// RefreshDouyinUserCookie 热更新：从 DB 重新加载并应用抖音 Cookie
// 供 API 层在用户保存新 Cookie 后立即调用
// 参数 cookie 可能为空字符串（表示用户清空了 Cookie），此时从 DB 重新加载确保一致性
func (s *Scheduler) RefreshDouyinUserCookie(_ string) {
	s.loadDouyinUserCookie()
}
