package config

import "time"

const (
	// DefaultRequestInterval 下载器默认请求间隔（秒）
	DefaultRequestInterval = 30

	// DefaultCheckIntervalSec 源默认检查间隔（秒）
	DefaultCheckIntervalSec = 1800

	// DefaultSchedulerTick 调度器定时检查间隔
	DefaultSchedulerTick = 5 * time.Minute

	// ChunkThreshold 分块下载阈值：50MB
	ChunkThreshold = 50 * 1024 * 1024

	// DownloadBufferSize 下载缓冲区大小：256KB
	DownloadBufferSize = 256 * 1024

	// MinDiskFreeDefault 最小磁盘剩余空间默认值：1GB
	MinDiskFreeDefault = 1 * 1024 * 1024 * 1024

	// DefaultDownloadWorkers 默认下载并发数
	DefaultDownloadWorkers = 3

	// MaxRetryCount 最大重试次数
	MaxRetryCount = 3

	// CooldownDuration 风控冷却时间
	CooldownDuration = 30 * time.Minute

	// DefaultQueueSize 下载队列默认大小
	DefaultQueueSize = 2000

	// LogRingBufferSize 日志环形缓冲区大小
	LogRingBufferSize = 5000
)
