package scheduler

import "video-subscribe-dl/internal/db"

// PlatformScheduler 平台调度器接口
// 每个平台（B站、抖音）实现此接口，顶层 Scheduler 通过接口调用平台逻辑
type PlatformScheduler interface {
	CheckSource(src db.Source)
	RetryDownload(dl db.Download)
	IsPaused() bool
	Stop()
}
