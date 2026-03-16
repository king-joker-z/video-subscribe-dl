package douyin

import (
	_ "embed"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/dop251/goja"
)

//go:embed abogus.js
var abogusScript string

// abogusPool 池化 goja VM 实例，用于 a_bogus 签名
// a_bogus.js 基于 SM3 哈希 + RC4 加密 + 自定义编码，预编译为 goja.Program 后复用
type abogusPool struct {
	program  *goja.Program    // 预编译的 a_bogus.js
	pool     chan *abogusEntry // VM 实例池
	maxUses  int              // 每个 VM 最大使用次数
	size     int              // 池大小
	created  atomic.Int64     // 统计: 创建 VM 总数
	recycled atomic.Int64     // 统计: 回收 VM 总数
}

// abogusEntry 池中的 VM 实例
type abogusEntry struct {
	vm   *goja.Runtime
	uses int
}

const (
	abogusPoolSize = 4   // 默认池大小
	abogusMaxUses  = 500 // 每个 VM 最大使用次数后丢弃重建
)

var (
	globalABogusPool     *abogusPool
	globalABogusPoolOnce sync.Once
	globalABogusPoolErr  error
)

// getABogusPool 获取全局 a_bogus 签名池（懒初始化单例）
func getABogusPool() (*abogusPool, error) {
	globalABogusPoolOnce.Do(func() {
		globalABogusPool, globalABogusPoolErr = newABogusPool(abogusPoolSize, abogusMaxUses)
	})
	return globalABogusPool, globalABogusPoolErr
}

// resetABogusPool 重置全局签名池（用于测试）
func resetABogusPool() {
	globalABogusPoolOnce = sync.Once{}
	globalABogusPool = nil
	globalABogusPoolErr = nil
}

// newABogusPool 创建 a_bogus 签名 VM 池
func newABogusPool(size, maxUses int) (*abogusPool, error) {
	// 预编译 a_bogus.js 为 goja.Program（只做一次）
	program, err := goja.Compile("a_bogus.js", abogusScript, false)
	if err != nil {
		return nil, fmt.Errorf("compile a_bogus.js: %w", err)
	}

	ap := &abogusPool{
		program: program,
		pool:    make(chan *abogusEntry, size),
		maxUses: maxUses,
		size:    size,
	}

	// 预热: 创建所有 VM 实例
	for i := 0; i < size; i++ {
		entry, err := ap.newEntry()
		if err != nil {
			return nil, fmt.Errorf("preheat a_bogus VM %d: %w", i, err)
		}
		ap.pool <- entry
	}

	slog.Info("a_bogus pool initialized", "module", "douyin", "size", size, "maxUses", maxUses)
	return ap, nil
}

// newEntry 创建一个新的池条目
func (ap *abogusPool) newEntry() (*abogusEntry, error) {
	vm := goja.New()

	// a_bogus.js 使用 console.error，需要提供 console 对象
	console := vm.NewObject()
	console.Set("error", func(call goja.FunctionCall) goja.Value {
		// 静默忽略 console.error（SM3 中的参数校验日志）
		return goja.Undefined()
	})
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		return goja.Undefined()
	})
	vm.Set("console", console)

	// a_bogus.js 使用 Date.now()，goja 默认支持
	// a_bogus.js 使用 Math.random()，goja 默认支持
	// a_bogus.js 使用 encodeURIComponent，goja 默认支持

	if _, err := vm.RunProgram(ap.program); err != nil {
		return nil, fmt.Errorf("load a_bogus.js into VM: %w", err)
	}
	ap.created.Add(1)
	return &abogusEntry{vm: vm, uses: 0}, nil
}

// sign 执行 a_bogus 签名
// 入参: url query string (不含 ?) + user agent
// 出参: a_bogus 签名值
func (ap *abogusPool) sign(queryStr, userAgent string) (string, error) {
	// 从池中获取 VM
	entry := <-ap.pool

	// 对参数进行转义，防止 JS 注入（单引号）
	safeQuery := escapeJSString(queryStr)
	safeUA := escapeJSString(userAgent)

	// 调用 generate_a_bogus(url_search_params, user_agent)
	code := fmt.Sprintf("generate_a_bogus('%s', '%s')", safeQuery, safeUA)
	val, err := entry.vm.RunString(code)
	if err != nil {
		// 执行失败，丢弃这个 VM，创建新的放回池
		ap.recycled.Add(1)
		go ap.replaceEntry()
		return "", fmt.Errorf("execute generate_a_bogus(): %w", err)
	}

	result := val.String()
	entry.uses++

	// 检查是否超过最大使用次数
	if entry.uses >= ap.maxUses {
		ap.recycled.Add(1)
		go ap.replaceEntry()
	} else {
		// 归还池
		ap.pool <- entry
	}

	return result, nil
}

// replaceEntry 异步替换一个池条目
func (ap *abogusPool) replaceEntry() {
	entry, err := ap.newEntry()
	if err != nil {
		slog.Error("failed to create replacement a_bogus VM", "module", "douyin", "error", err)
		// 重试一次
		entry, err = ap.newEntry()
		if err != nil {
			slog.Error("failed to create replacement a_bogus VM (retry)", "module", "douyin", "error", err)
			return
		}
	}
	ap.pool <- entry
}

// stats 返回池统计信息
func (ap *abogusPool) stats() (created, recycled int64) {
	return ap.created.Load(), ap.recycled.Load()
}

// escapeJSString 转义字符串中的特殊字符以安全嵌入 JS 单引号字符串
func escapeJSString(s string) string {
	var result []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			result = append(result, '\\', '\\')
		case '\'':
			result = append(result, '\\', '\'')
		case '\n':
			result = append(result, '\\', 'n')
		case '\r':
			result = append(result, '\\', 'r')
		default:
			result = append(result, s[i])
		}
	}
	return string(result)
}
