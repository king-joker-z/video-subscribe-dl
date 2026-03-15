package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// EventType 通知事件类型
type EventType string

const (
	EventDownloadComplete EventType = "download_complete"
	EventDownloadFailed   EventType = "download_failed"
	EventCookieExpired    EventType = "cookie_expired"
	EventDiskLow          EventType = "disk_low"
	EventSyncComplete     EventType = "sync_complete"
	EventTest             EventType = "test"
	EventRateLimited      EventType = "rate_limited"
)

// NotifyType 通知通道类型
type NotifyType string

const (
	NotifyWebhook  NotifyType = "webhook"
	NotifyTelegram NotifyType = "telegram"
	NotifyBark     NotifyType = "bark"
)

// Payload Webhook 通知载荷
type Payload struct {
	Event     EventType `json:"event"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Timestamp string    `json:"timestamp"`
}

// SettingsGetter 从 DB 获取设置的接口
type SettingsGetter interface {
	GetSetting(key string) (string, error)
}

// Notifier 通知发送器
type Notifier struct {
	settings SettingsGetter
	client   *http.Client
}

// New 创建 Notifier
func New(settings SettingsGetter) *Notifier {
	return &Notifier{
		settings: settings,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// eventEmoji 为事件类型选择 emoji
func eventEmoji(event EventType) string {
	switch event {
	case EventDownloadComplete:
		return "✅"
	case EventDownloadFailed:
		return "❌"
	case EventCookieExpired:
		return "🍪"
	case EventDiskLow:
		return "💾"
	case EventSyncComplete:
		return "🔄"
	case EventRateLimited:
		return "⚠️"
	case EventTest:
		return "🔔"
	default:
		return "📢"
	}
}

// Send 发送通知（根据事件类型和 DB 开关判断是否真正发送）
func (n *Notifier) Send(event EventType, title, message string) {
	if !n.shouldSend(event) {
		return
	}
	go n.dispatch(event, title, message)
}

// SendTest 强制发送测试通知（忽略开关）
func (n *Notifier) SendTest() error {
	return n.dispatch(EventTest, "测试通知", "Video Subscribe DL 通知配置正常 ✅")
}

// dispatch 根据 notify_type 分发到对应通道
func (n *Notifier) dispatch(event EventType, title, message string) error {
	notifyType, _ := n.settings.GetSetting("notify_type")

	switch NotifyType(notifyType) {
	case NotifyTelegram:
		return n.sendTelegram(event, title, message)
	case NotifyBark:
		return n.sendBark(event, title, message)
	default:
		// 默认使用 generic webhook（兼容旧版本）
		return n.sendWebhook(event, title, message)
	}
}

// sendWebhook 发送通用 Webhook 通知
func (n *Notifier) sendWebhook(event EventType, title, message string) error {
	webhookURL, err := n.settings.GetSetting("webhook_url")
	if err != nil || webhookURL == "" {
		return fmt.Errorf("webhook_url 未配置")
	}

	payload := Payload{
		Event:     event,
		Title:     title,
		Message:   message,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[notify] Marshal error: %v", err)
		return err
	}

	resp, err := n.client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify/webhook] Send failed (event=%s): %v", event, err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		log.Printf("[notify/webhook] Returned %d for event=%s", resp.StatusCode, event)
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}

	log.Printf("[notify/webhook] Sent: event=%s, title=%s", event, title)
	return nil
}

// sendTelegram 通过 Telegram Bot API 发送通知
func (n *Notifier) sendTelegram(event EventType, title, message string) error {
	token, _ := n.settings.GetSetting("telegram_bot_token")
	chatID, _ := n.settings.GetSetting("telegram_chat_id")

	if token == "" || chatID == "" {
		return fmt.Errorf("telegram_bot_token 或 telegram_chat_id 未配置")
	}

	emoji := eventEmoji(event)
	// Telegram MarkdownV2 格式
	text := fmt.Sprintf("%s *%s*\n\n%s\n\n_%s_",
		emoji, escapeMarkdownV2(title), escapeMarkdownV2(message),
		escapeMarkdownV2(time.Now().Format("2006-01-02 15:04:05")))

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]string{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "MarkdownV2",
	}

	body, _ := json.Marshal(payload)
	resp, err := n.client.Post(apiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify/telegram] Send failed (event=%s): %v", event, err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[notify/telegram] API returned %d: %s", resp.StatusCode, string(respBody))
		return fmt.Errorf("telegram API returned %d", resp.StatusCode)
	}

	log.Printf("[notify/telegram] Sent: event=%s, title=%s, chat=%s", event, title, chatID)
	return nil
}

// sendBark 通过 Bark (iOS 推送) 发送通知
func (n *Notifier) sendBark(event EventType, title, message string) error {
	barkServer, _ := n.settings.GetSetting("bark_server")
	barkKey, _ := n.settings.GetSetting("bark_key")

	if barkKey == "" {
		return fmt.Errorf("bark_key 未配置")
	}

	// 默认 Bark 服务器
	if barkServer == "" {
		barkServer = "https://api.day.app"
	}
	barkServer = strings.TrimRight(barkServer, "/")

	emoji := eventEmoji(event)
	fullTitle := fmt.Sprintf("%s %s", emoji, title)

	// Bark v2 API: POST /push
	apiURL := fmt.Sprintf("%s/push", barkServer)
	payload := map[string]interface{}{
		"device_key": barkKey,
		"title":      fullTitle,
		"body":       message,
		"group":      "VideoSubscribe",
		"icon":       "https://www.bilibili.com/favicon.ico",
	}

	// 根据事件类型设置声音和等级
	switch event {
	case EventCookieExpired, EventDiskLow:
		payload["level"] = "timeSensitive"
		payload["sound"] = "alarm"
	case EventDownloadFailed:
		payload["level"] = "timeSensitive"
	default:
		payload["level"] = "active"
	}

	body, _ := json.Marshal(payload)
	resp, err := n.client.Post(apiURL, "application/json; charset=utf-8", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify/bark] Send failed (event=%s): %v", event, err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[notify/bark] API returned %d: %s", resp.StatusCode, string(respBody))
		return fmt.Errorf("bark API returned %d", resp.StatusCode)
	}

	log.Printf("[notify/bark] Sent: event=%s, title=%s", event, title)
	return nil
}

func (n *Notifier) shouldSend(event EventType) bool {
	switch event {
	case EventDownloadComplete:
		v, _ := n.settings.GetSetting("notify_on_complete")
		return v == "true" || v == "1"
	case EventDownloadFailed:
		v, _ := n.settings.GetSetting("notify_on_error")
		return v != "false" && v != "0" // 默认 true
	case EventCookieExpired:
		v, _ := n.settings.GetSetting("notify_on_cookie_expire")
		return v != "false" && v != "0" // 默认 true
	case EventDiskLow:
		return true // 磁盘低始终通知
	case EventRateLimited:
		return true // 风控始终通知
	case EventSyncComplete:
		v, _ := n.settings.GetSetting("notify_on_sync")
		return v == "true" || v == "1"
	case EventTest:
		return true
	}
	return false
}

// escapeMarkdownV2 转义 Telegram MarkdownV2 特殊字符
func escapeMarkdownV2(text string) string {
	// MarkdownV2 需要转义的字符: _ * [ ] ( ) ~ ` > # + - = | { } . !
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(text)
}

// IsConfigured 检查通知是否已配置
func (n *Notifier) IsConfigured() (bool, NotifyType) {
	notifyType, _ := n.settings.GetSetting("notify_type")

	switch NotifyType(notifyType) {
	case NotifyTelegram:
		token, _ := n.settings.GetSetting("telegram_bot_token")
		chatID, _ := n.settings.GetSetting("telegram_chat_id")
		return token != "" && chatID != "", NotifyTelegram
	case NotifyBark:
		key, _ := n.settings.GetSetting("bark_key")
		return key != "", NotifyBark
	default:
		webhookURL, _ := n.settings.GetSetting("webhook_url")
		if webhookURL == "" {
			// 没有配置 notify_type 也没有 webhook_url，尝试检查是否有 telegram 配置
			token, _ := n.settings.GetSetting("telegram_bot_token")
			if token != "" {
				return false, NotifyTelegram
			}
		}
		return webhookURL != "", NotifyWebhook
	}
}

// GetStatusInfo 返回通知配置状态信息
func (n *Notifier) GetStatusInfo() map[string]interface{} {
	configured, notifyType := n.IsConfigured()
	info := map[string]interface{}{
		"configured": configured,
		"type":       string(notifyType),
	}

	if notifyType == NotifyTelegram {
		chatID, _ := n.settings.GetSetting("telegram_chat_id")
		if chatID != "" {
			info["telegram_chat_id"] = chatID
		}
	}

	return info
}

// formatBarkURL 构造 Bark GET URL（兼容旧版 Bark 客户端）
func formatBarkURL(server, key, title, body string) string {
	return fmt.Sprintf("%s/%s/%s/%s",
		strings.TrimRight(server, "/"),
		url.PathEscape(key),
		url.PathEscape(title),
		url.PathEscape(body))
}
