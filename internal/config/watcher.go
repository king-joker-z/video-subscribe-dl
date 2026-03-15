package config

import (
	"log"
	"strconv"
	"sync"
	"time"
)

// SettingsStore 抽象数据库设置读取接口
type SettingsStore interface {
	GetSetting(key string) (string, error)
}

// HotConfig 可热更新的运行时配置
type HotConfig struct {
	mu sync.RWMutex

	DownloadWorkers    int           // 下载并发数
	RateLimitPerMinute int           // 每分钟请求限制
	CooldownDuration   time.Duration // 风控冷却时间
	DownloadQuality    string        // 全局画质偏好
	DownloadCodec      string        // 全局编码偏好
	CheckIntervalMin   int           // 全局检查间隔（分钟）
	FilenameTemplate   string        // 文件名模板
	RequestInterval    int           // 请求间隔秒

	// 变更通知
	onChange []func(*HotConfig)
}

// NewHotConfig 创建默认热配置
func NewHotConfig() *HotConfig {
	return &HotConfig{
		DownloadWorkers:    DefaultDownloadWorkers,
		RateLimitPerMinute: 30,
		CooldownDuration:   CooldownDuration,
		DownloadQuality:    "best",
		DownloadCodec:      "all",
		CheckIntervalMin:   30,
		FilenameTemplate:   "{{.Title}} [{{.BvID}}]",
		RequestInterval:    DefaultRequestInterval,
	}
}

// Get 安全读取
func (c *HotConfig) Get() HotConfigSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return HotConfigSnapshot{
		DownloadWorkers:    c.DownloadWorkers,
		RateLimitPerMinute: c.RateLimitPerMinute,
		CooldownDuration:   c.CooldownDuration,
		DownloadQuality:    c.DownloadQuality,
		DownloadCodec:      c.DownloadCodec,
		CheckIntervalMin:   c.CheckIntervalMin,
		FilenameTemplate:   c.FilenameTemplate,
		RequestInterval:    c.RequestInterval,
	}
}

// HotConfigSnapshot 配置快照（值拷贝，无锁）
type HotConfigSnapshot struct {
	DownloadWorkers    int
	RateLimitPerMinute int
	CooldownDuration   time.Duration
	DownloadQuality    string
	DownloadCodec      string
	CheckIntervalMin   int
	FilenameTemplate   string
	RequestInterval    int
}

// OnChange 注册变更回调
func (c *HotConfig) OnChange(fn func(*HotConfig)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onChange = append(c.onChange, fn)
}

// notifyChange 触发变更回调
func (c *HotConfig) notifyChange() {
	for _, fn := range c.onChange {
		fn(c)
	}
}

// LoadFromDB 从数据库加载配置
func (c *HotConfig) LoadFromDB(store SettingsStore) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	changed := false

	if v, err := store.GetSetting("max_concurrent"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n != c.DownloadWorkers {
			c.DownloadWorkers = n
			changed = true
		}
	}

	if v, err := store.GetSetting("rate_limit_per_minute"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n != c.RateLimitPerMinute {
			c.RateLimitPerMinute = n
			changed = true
		}
	}

	if v, err := store.GetSetting("cooldown_minutes"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			d := time.Duration(n) * time.Minute
			if d != c.CooldownDuration {
				c.CooldownDuration = d
				changed = true
			}
		}
	}

	if v, err := store.GetSetting("download_quality"); err == nil && v != "" && v != c.DownloadQuality {
		c.DownloadQuality = v
		changed = true
	}

	if v, err := store.GetSetting("download_codec"); err == nil && v != "" && v != c.DownloadCodec {
		c.DownloadCodec = v
		changed = true
	}

	if v, err := store.GetSetting("check_interval_minutes"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n != c.CheckIntervalMin {
			c.CheckIntervalMin = n
			changed = true
		}
	}

	if v, err := store.GetSetting("filename_template"); err == nil && v != "" && v != c.FilenameTemplate {
		c.FilenameTemplate = v
		changed = true
	}

	if v, err := store.GetSetting("request_interval"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n != c.RequestInterval {
			c.RequestInterval = n
			changed = true
		}
	}

	if changed {
		c.notifyChange()
	}
	return changed
}

// ConfigWatcher 定期从 DB 轮询配置变化
type ConfigWatcher struct {
	config   *HotConfig
	store    SettingsStore
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewConfigWatcher 创建配置监视器
func NewConfigWatcher(cfg *HotConfig, store SettingsStore, interval time.Duration) *ConfigWatcher {
	return &ConfigWatcher{
		config:   cfg,
		store:    store,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start 开始监视
func (w *ConfigWatcher) Start() {
	// 首次加载
	w.config.LoadFromDB(w.store)
	log.Printf("[config-watcher] 已加载运行时配置: workers=%d, rate=%d/min, cooldown=%v",
		w.config.DownloadWorkers, w.config.RateLimitPerMinute, w.config.CooldownDuration)

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if w.config.LoadFromDB(w.store) {
					log.Printf("[config-watcher] 配置已热更新: workers=%d, rate=%d/min",
						w.config.DownloadWorkers, w.config.RateLimitPerMinute)
				}
			case <-w.stopCh:
				return
			}
		}
	}()
}

// Reload 手动触发重载（API 调用后立即生效）
func (w *ConfigWatcher) Reload() {
	if w.config.LoadFromDB(w.store) {
		log.Printf("[config-watcher] 配置手动重载: workers=%d, rate=%d/min",
			w.config.DownloadWorkers, w.config.RateLimitPerMinute)
	}
}

// Stop 停止监视
func (w *ConfigWatcher) Stop() {
	close(w.stopCh)
	w.wg.Wait()
}

// Config 获取当前配置
func (w *ConfigWatcher) Config() *HotConfig {
	return w.config
}
