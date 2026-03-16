package douyin

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

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
	defaultPoolSize = 4   // 默认池大小
	defaultMaxUses  = 500 // 每个 VM 最大使用次数后丢弃重建
)

var (
	globalSignPool     *signPool
	globalSignPoolOnce sync.Once
	globalSignPoolErr  error
)

// getSignPool 获取全局签名池（懒初始化单例）
func getSignPool() (*signPool, error) {
	globalSignPoolOnce.Do(func() {
		globalSignPool, globalSignPoolErr = newSignPool(defaultPoolSize, defaultMaxUses)
	})
	return globalSignPool, globalSignPoolErr
}

// resetSignPool 重置全局签名池（用于测试）
func resetSignPool() {
	globalSignPoolOnce = sync.Once{}
	globalSignPool = nil
	globalSignPoolErr = nil
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

// sign 执行 X-Bogus 签名
func (sp *signPool) sign(queryStr, userAgent string) (string, error) {
	// 从池中获取 VM
	entry := <-sp.pool

	// 执行签名
	code := fmt.Sprintf("sign('%s', '%s')", queryStr, userAgent)
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
func (sp *signPool) replaceEntry() {
	entry, err := sp.newEntry()
	if err != nil {
		slog.Error("failed to create replacement VM", "module", "douyin", "error", err)
		// 重试一次
		entry, err = sp.newEntry()
		if err != nil {
			slog.Error("failed to create replacement VM (retry)", "module", "douyin", "error", err)
			return
		}
	}
	sp.pool <- entry
}

// stats 返回池统计信息
func (sp *signPool) stats() (created, recycled int64) {
	return sp.created.Load(), sp.recycled.Load()
}
