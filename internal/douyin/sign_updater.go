package douyin

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// SignUpdater 签名算法热更新管理器
// 支持从远端 URL 拉取 JS 签名脚本，不需要重新构建镜像
// 不设置 sign_js_url 时使用 embed 的内置版本（向后兼容）
type SignUpdater struct {
	mu sync.RWMutex

	// 配置
	signJSURL   string // X-Bogus JS 远端 URL（空 = 使用内置）
	abogusJSURL string // a_bogus JS 远端 URL（空 = 使用内置）

	// 缓存
	signETag   string
	abogusETag string

	// HTTP 客户端
	client *http.Client
}

var (
	globalSignUpdater     *SignUpdater
	globalSignUpdaterOnce sync.Once
)

// GetSignUpdater 获取全局签名更新器
func GetSignUpdater() *SignUpdater {
	globalSignUpdaterOnce.Do(func() {
		globalSignUpdater = &SignUpdater{
			client: &http.Client{Timeout: 30 * time.Second},
		}
	})
	return globalSignUpdater
}

// Configure 设置远端 JS URL（空字符串表示使用内置版本）
func (su *SignUpdater) Configure(signJSURL, abogusJSURL string) {
	su.mu.Lock()
	defer su.mu.Unlock()
	su.signJSURL = signJSURL
	su.abogusJSURL = abogusJSURL
	slog.Info("sign updater configured",
		"module", "douyin",
		"sign_js_url", signJSURL,
		"abogus_js_url", abogusJSURL,
	)
}

// CheckAndUpdate 检查远端是否有更新，有则下载并热替换签名池
// 返回是否有更新
func (su *SignUpdater) CheckAndUpdate() (updated bool, err error) {
	su.mu.RLock()
	signURL := su.signJSURL
	abogusURL := su.abogusJSURL
	su.mu.RUnlock()

	if signURL == "" && abogusURL == "" {
		return false, nil // 未配置远端 URL，使用内置版本
	}

	var signUpdated, abogusUpdated bool

	if signURL != "" {
		signUpdated, err = su.updatePool(signURL, &su.signETag, "sign.js", su.reloadSignPool)
		if err != nil {
			slog.Warn("sign.js update check failed", "module", "douyin", "error", err)
		}
	}

	if abogusURL != "" {
		abogusUpdated, err = su.updatePool(abogusURL, &su.abogusETag, "a_bogus.js", su.reloadABogusPool)
		if err != nil {
			slog.Warn("a_bogus.js update check failed", "module", "douyin", "error", err)
		}
	}

	return signUpdated || abogusUpdated, nil
}

// ReloadFromRemote 手动触发从远端重新加载（忽略 ETag 缓存）
func (su *SignUpdater) ReloadFromRemote() error {
	su.mu.Lock()
	su.signETag = ""
	su.abogusETag = ""
	su.mu.Unlock()

	_, err := su.CheckAndUpdate()
	return err
}

// updatePool 检查单个 JS 文件是否有更新并热替换池
func (su *SignUpdater) updatePool(
	jsURL string,
	etag *string,
	name string,
	reloadFn func(string) error,
) (bool, error) {
	req, err := http.NewRequest("GET", jsURL, nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}

	su.mu.RLock()
	currentETag := *etag
	su.mu.RUnlock()

	if currentETag != "" {
		req.Header.Set("If-None-Match", currentETag)
	}

	resp, err := su.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("fetch %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		slog.Debug("sign script not modified", "module", "douyin", "name", name)
		return false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s returned HTTP %d", name, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", name, err)
	}

	jsCode := string(body)

	// 校验: 必须能被 goja 编译
	if _, err := goja.Compile(name, jsCode, false); err != nil {
		return false, fmt.Errorf("validate %s: goja compile failed: %w", name, err)
	}

	// 热替换
	if err := reloadFn(jsCode); err != nil {
		return false, fmt.Errorf("reload %s: %w", name, err)
	}

	// 更新 ETag
	su.mu.Lock()
	*etag = resp.Header.Get("ETag")
	su.mu.Unlock()

	slog.Info("sign script updated from remote", "module", "douyin", "name", name, "size", len(body))
	return true, nil
}

// reloadSignPool 用新的 JS 代码重建 X-Bogus 签名池
func (su *SignUpdater) reloadSignPool(jsCode string) error {
	program, err := goja.Compile("sign.js", jsCode, false)
	if err != nil {
		return fmt.Errorf("compile sign.js: %w", err)
	}

	pool := &signPool{
		program: program,
		pool:    make(chan *poolEntry, defaultPoolSize),
		maxUses: defaultMaxUses,
		size:    defaultPoolSize,
	}

	for i := 0; i < defaultPoolSize; i++ {
		entry, err := pool.newEntry()
		if err != nil {
			return fmt.Errorf("preheat VM %d: %w", i, err)
		}
		pool.pool <- entry
	}

	// 原子替换全局池
	globalSignPoolOnce = sync.Once{}
	globalSignPoolOnce.Do(func() {
		globalSignPool = pool
		globalSignPoolErr = nil
	})

	slog.Info("sign pool reloaded", "module", "douyin", "size", defaultPoolSize)
	return nil
}

// reloadABogusPool 用新的 JS 代码重建 a_bogus 签名池
func (su *SignUpdater) reloadABogusPool(jsCode string) error {
	program, err := goja.Compile("a_bogus.js", jsCode, false)
	if err != nil {
		return fmt.Errorf("compile a_bogus.js: %w", err)
	}

	pool := &abogusPool{
		program: program,
		pool:    make(chan *abogusEntry, abogusPoolSize),
		maxUses: abogusMaxUses,
		size:    abogusPoolSize,
	}

	for i := 0; i < abogusPoolSize; i++ {
		entry, err := pool.newEntry()
		if err != nil {
			return fmt.Errorf("preheat a_bogus VM %d: %w", i, err)
		}
		pool.pool <- entry
	}

	// 原子替换全局池
	globalABogusPoolOnce = sync.Once{}
	globalABogusPoolOnce.Do(func() {
		globalABogusPool = pool
		globalABogusPoolErr = nil
	})

	slog.Info("a_bogus pool reloaded", "module", "douyin", "size", abogusPoolSize)
	return nil
}
