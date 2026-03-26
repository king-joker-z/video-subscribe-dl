package douyin

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// signPool 池化 goja VM 实例，避免每次 signURL 都重新创建 VM + 解析 sign.js
// sign.js 约 566 行混淆代码，预编译为 goja.Program 后复用，显著降低开销
type signPool struct {
	program  *goja.Program  // 预编译的 sign.js
	pool     chan *poolEntry // VM 实例池
	maxUses  int            // 每个 VM 最大使用次数（防止内存泄漏）
	size     int            // 池大小
	created  atomic.Int64   // 统计: 创建 VM 总数
	recycled atomic.Int64   // 统计: 回收 VM 总数
}

// poolEntry 池中的 VM 实例
type poolEntry struct {
	vm   *goja.Runtime
	uses int
}

const (
	defaultPoolSize   = 4   // 默认池大小
	defaultMaxUses    = 500 // 每个 VM 最大使用次数后丢弃重建
	maxReplaceRetries = 5   // replaceEntry 最大重试次数
)

// signPoolHolder 用 atomic.Value 持有 *signPool，支持并发安全的热更新
// [FIXED: D-3] 用 atomic.Value 替代 sync.Once+plain var，消除并发赋值 data race
type signPoolHolder struct {
	pool *signPool
	err  error
}

var (
	globalSignPoolValue atomic.Value // stores *signPoolHolder
	globalSignPoolOnce  sync.Once
)

// getSignPool 获取全局签名池（懒初始化单例，热更新时原子替换）
func getSignPool() (*signPool, error) {
	globalSignPoolOnce.Do(func() {
		pool, err := newSignPool(defaultPoolSize, defaultMaxUses)
		globalSignPoolValue.Store(&signPoolHolder{pool: pool, err: err})
	})
	if h, ok := globalSignPoolValue.Load().(*signPoolHolder); ok && h != nil {
		return h.pool, h.err
	}
	return nil, fmt.Errorf("sign pool not initialized")
}

// storeSignPool 原子替换全局签名池（热更新专用）
func storeSignPool(pool *signPool) {
	globalSignPoolValue.Store(&signPoolHolder{pool: pool, err: nil})
}

// resetSignPool 重置全局签名池（用于测试）
func resetSignPool() {
	globalSignPoolOnce = sync.Once{}
	globalSignPoolValue.Store((*signPoolHolder)(nil))
}

// newSignPool 创建签名 VM 池
func newSignPool(size, maxUses int) (*signPool, error) {
	// 预编译 sign.js 为 goja.Program（只做一次）
	program, err := goja.Compile("sign.js", signScript, false)
	if err != nil {
		return nil, fmt.Errorf("compile sign.js: %w", err)
	}

	sp := &signPool{
		program: program,
		pool:    make(chan *poolEntry, size),
		maxUses: maxUses,
		size:    size,
	}

	// 预热: 创建所有 VM 实例
	for i := 0; i < size; i++ {
		entry, err := sp.newEntry()
		if err != nil {
			return nil, fmt.Errorf("preheat VM %d: %w", i, err)
		}
		sp.pool <- entry
	}

	slog.Info("sign pool initialized", "module", "douyin", "size", size, "maxUses", maxUses)
	return sp, nil
}

// newEntry 创建一个新的池条目
func (sp *signPool) newEntry() (*poolEntry, error) {
	vm := goja.New()
	// sign.js 使用 module.exports，需要提供 module 对象
	module := vm.NewObject()
	module.Set("exports", vm.NewObject())
	vm.Set("module", module)
	if _, err := vm.RunProgram(sp.program); err != nil {
		return nil, fmt.Errorf("load sign.js into VM: %w", err)
	}
	sp.created.Add(1)
	return &poolEntry{vm: vm, uses: 0}, nil
}

// signPoolGetTimeout 从池中获取 VM 实例的最大等待时间
const signPoolGetTimeout = 10 * time.Second

// sign 执行 X-Bogus 签名
// [FIXED: D-2] VM 批量失败时改用带超时的 select，避免永久阻塞
func (sp *signPool) sign(queryStr, userAgent string) (string, error) {
	// 从池中获取 VM（带超时，防止 VM 批量失败时永久阻塞）
	ctx, cancel := context.WithTimeout(context.Background(), signPoolGetTimeout)
	defer cancel()
	var entry *poolEntry
	select {
	case entry = <-sp.pool:
	case <-ctx.Done():
		return "", fmt.Errorf("sign pool: timed out waiting for available VM (pool may be exhausted)")
	}

	// 对参数进行转义，防止 JS 注入（单引号）— 与 abogus_pool.go 保持一致
	safeQuery := escapeJSString(queryStr)
	safeUA := escapeJSString(userAgent)

	code := fmt.Sprintf("sign('%s', '%s')", safeQuery, safeUA)
	val, err := entry.vm.RunString(code)
	if err != nil {
		// 执行失败，丢弃这个 VM，创建新的放回池
		sp.recycled.Add(1)
		go sp.replaceEntry()
		return "", fmt.Errorf("execute sign(): %w", err)
	}

	result := val.String()
	entry.uses++

	// 检查是否超过最大使用次数
	if entry.uses >= sp.maxUses {
		sp.recycled.Add(1)
		go sp.replaceEntry()
	} else {
		// 归还池
		sp.pool <- entry
	}

	return result, nil
}

// replaceEntry 异步替换一个池条目
// 使用指数退避重试，防止全部失败导致池耗尽永久阻塞
func (sp *signPool) replaceEntry() {
	for attempt := 1; attempt <= maxReplaceRetries; attempt++ {
		entry, err := sp.newEntry()
		if err == nil {
			sp.pool <- entry
			return
		}
		slog.Error("failed to create replacement VM",
			"module", "douyin", "pool", "sign", "attempt", attempt, "error", err)
		if attempt < maxReplaceRetries {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	// 所有重试都失败：池容量永久减 1，记录严重错误
	// 这比之前的静默 return 更明确，且避免了调用方无限阻塞（池仍可用，只是少一个 slot）
	slog.Error("all replacement attempts failed, pool capacity permanently reduced by 1",
		"module", "douyin", "pool", "sign")
}

// stats 返回池统计信息
func (sp *signPool) stats() (created, recycled int64) {
	return sp.created.Load(), sp.recycled.Load()
}
