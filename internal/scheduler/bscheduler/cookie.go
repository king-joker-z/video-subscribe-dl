package bscheduler

import (
	"fmt"
	"log"
	"time"

	"video-subscribe-dl/internal/bilibili"
	"video-subscribe-dl/internal/notify"
)

// GetBiliClient 线程安全地获取 bilibili client
func (s *BiliScheduler) GetBiliClient() *bilibili.Client {
	s.biliMu.RLock()
	defer s.biliMu.RUnlock()
	return s.bili
}

func (s *BiliScheduler) getBili() *bilibili.Client {
	s.biliMu.RLock()
	defer s.biliMu.RUnlock()
	return s.bili
}

func (s *BiliScheduler) resetCaches() {
	bilibili.ClearWbiCache()
}

func (s *BiliScheduler) applyNewClient(client *bilibili.Client) {
	s.biliMu.Lock()
	s.bili = client
	s.biliMu.Unlock()
	if s.dl != nil {
		s.dl.UpdateClient(client)
	}
	s.resetCaches()
}

// UpdateCredential 使用新 Credential 更新 client
func (s *BiliScheduler) UpdateCredential(cred *bilibili.Credential) {
	client := bilibili.NewClientWithCredential(cred)
	s.applyNewClient(client)
}

// UpdateCookie 重新加载 cookie 文件并更新 client
func (s *BiliScheduler) UpdateCookie(cookiePath string) {
	s.cookiePath = cookiePath
	cookie := bilibili.ReadCookieFile(cookiePath)
	s.applyNewClient(bilibili.NewClient(cookie))
}

// VerifyCookie 验证 cookie 是否有效，即将过期时自动刷新
func (s *BiliScheduler) VerifyCookie(trigger string) {
	result, err := s.getBili().VerifyCookie()
	if err != nil {
		log.Printf("[bscheduler][WARN] Cookie verify failed during %s: %v", trigger, err)
		return
	}
	if !result.LoggedIn {
		log.Printf("[bscheduler][WARN] Cookie is invalid or expired (trigger: %s). Attempting refresh...", trigger)
		s.tryCookieRefresh(trigger)
		return
	}

	vipLabel := "无"
	switch result.VIPType {
	case 1:
		vipLabel = "月度大会员"
	case 2:
		vipLabel = "年度大会员"
	}

	if result.VIPDueDate != "" {
		dueDate, parseErr := time.Parse("2006-01-02", result.VIPDueDate)
		if parseErr == nil {
			daysUntil := time.Until(dueDate).Hours() / 24
			if daysUntil < -30 {
				if trigger == "startup" {
					log.Printf("[bscheduler][INFO] Cookie valid: user=%s, VIP=%s (已过期: %s)",
						result.Username, vipLabel, result.VIPDueDate)
				}
				return
			} else if daysUntil < 7 {
				log.Printf("[bscheduler][INFO] Cookie/VIP 将在 %s 到期（<7天），尝试刷新...", result.VIPDueDate)
				s.tryCookieRefresh(trigger)
			}
		}
	}

	log.Printf("[bscheduler][INFO] Cookie valid: user=%s, VIP=%s, expires=%s (trigger: %s)",
		result.Username, vipLabel, result.VIPDueDate, trigger)
}

func (s *BiliScheduler) tryCookieRefresh(trigger string) {
	// 优先从 DB credential_json 获取 Credential（含正确的 ACTimeValue/refresh_token）
	// 走 credential.go:Refresh() 正确流程（需要 correspond-path 获取 refresh_csrf）
	// 旧的 RefreshCookie(cookiePath) 路径使用错误的 refresh_csrf，会导致刷新后 session 无效
	credJSON, dbErr := s.db.GetSetting("credential_json")
	if dbErr == nil && credJSON != "" {
		cred := bilibili.CredentialFromJSON(credJSON)
		if cred != nil && !cred.IsEmpty() && cred.ACTimeValue != "" {
			httpClient := s.getBili().GetHTTPClient()
			newCred, err := cred.Refresh(httpClient)
			if err != nil {
				log.Printf("[bscheduler][WARN] Credential refresh failed (trigger: %s): %v", trigger, err)
				s.notifier.Send(notify.EventCookieExpired, "Cookie 刷新失败", fmt.Sprintf("错误: %v，请手动更新 Cookie", err))
				return
			}
			if err := s.db.SetSetting("credential_json", newCred.ToJSON()); err != nil {
				log.Printf("[bscheduler][WARN] Save refreshed credential failed: %v", err)
			}
			s.UpdateCredential(newCred)
			log.Printf("[bscheduler][INFO] Credential 自动刷新成功 (trigger: %s)", trigger)
			return
		}
	}

	// fallback：无 DB credential，尝试 cookie 文件路径
	cookiePath := s.cookiePath
	if cookiePath == "" {
		log.Printf("[bscheduler][WARN] No cookie path configured, cannot auto-refresh")
		s.notifier.Send(notify.EventCookieExpired, "Cookie 已过期", "未配置 cookie 路径，请手动更新 Cookie")
		return
	}

	refreshResult, err := s.getBili().RefreshCookie(cookiePath)
	if err != nil {
		log.Printf("[bscheduler][WARN] Cookie refresh error during %s: %v", trigger, err)
		s.notifier.Send(notify.EventCookieExpired, "Cookie 刷新失败", fmt.Sprintf("错误: %v，请手动更新 Cookie", err))
		return
	}

	if refreshResult.Success {
		log.Printf("[bscheduler][INFO] Cookie 自动刷新成功 (trigger: %s)", trigger)
		s.resetCaches()
		if s.dl != nil {
			s.dl.UpdateClient(s.getBili())
		}
	} else {
		log.Printf("[bscheduler][WARN] Cookie 刷新失败: %s (trigger: %s). 请手动更新 Cookie。", refreshResult.Message, trigger)
		s.notifier.Send(notify.EventCookieExpired, "Cookie 需要手动更新", refreshResult.Message)
	}
}

// PeriodicCookieCheck 每 6 小时主动检测 Cookie 有效性
func (s *BiliScheduler) PeriodicCookieCheck() {
	if time.Since(s.lastCookieCheck) < 6*time.Hour {
		return
	}
	s.lastCookieCheck = time.Now()
	log.Println("[bscheduler] Periodic cookie check triggered")

	result, err := s.getBili().VerifyCookie()
	if err != nil {
		log.Printf("[bscheduler][WARN] Periodic cookie check failed: %v", err)
		return
	}
	if !result.LoggedIn {
		log.Printf("[bscheduler][WARN] Periodic cookie check: Cookie is invalid or expired")
		s.notifier.Send(notify.EventCookieExpired, "Cookie 已过期（定期检测）",
			"定期检测发现 Cookie 已失效，请及时更新")
		s.tryCookieRefresh("periodic check")
	}
}

// CheckAndRefreshCredential 检查 DB 中的 Credential 是否需要刷新
func (s *BiliScheduler) CheckAndRefreshCredential() {
	credJSON, err := s.db.GetSetting("credential_json")
	if err != nil || credJSON == "" {
		return
	}
	cred := bilibili.CredentialFromJSON(credJSON)
	if cred == nil || cred.IsEmpty() {
		return
	}

	httpClient := s.getBili().GetHTTPClient()
	needRefresh, err := cred.NeedRefresh(httpClient)
	if err != nil {
		log.Printf("[bscheduler] NeedRefresh check failed: %v", err)
		return
	}
	if !needRefresh {
		return
	}

	log.Printf("[bscheduler] Cookie needs refresh, attempting auto-refresh...")
	newCred, err := cred.Refresh(httpClient)
	if err != nil {
		log.Printf("[bscheduler][WARN] Credential auto-refresh failed: %v", err)
		s.notifier.Send(notify.EventCookieExpired, "凭证自动刷新失败", err.Error())
		return
	}

	if err := s.db.SetSetting("credential_json", newCred.ToJSON()); err != nil {
		log.Printf("[bscheduler][WARN] Save refreshed credential failed: %v", err)
		return
	}

	s.UpdateCredential(newCred)
	log.Printf("[bscheduler] Auto-refresh successful")
}
