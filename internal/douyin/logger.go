package douyin

import "log/slog"

// logger 是 douyin 包的结构化日志实例
// 使用 slog（Go 1.21+ 标准库），默认 handler 写到标准 log，兼容项目的 RingBufferLogger
var logger = slog.Default().With("module", "douyin")
