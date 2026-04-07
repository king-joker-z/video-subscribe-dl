package downloader

import "sync/atomic"

// DownloaderStats 下载器统计信息
type DownloaderStats struct {
	Active             int            `json:"active"`
	Queued             int            `json:"queued"`
	Completed          int            `json:"completed"`
	Failed             int            `json:"failed"`
	PlatformCompleted  map[string]int `json:"platform_completed"`
	PlatformFailed     map[string]int `json:"platform_failed"`
}

// Stats 返回下载器当前统计信息
func (d *Downloader) Stats() DownloaderStats {
	platformCompleted := make(map[string]int, len(d.platformCompleted))
	for p, ptr := range d.platformCompleted {
		if ptr != nil {
			platformCompleted[p] = int(atomic.LoadInt64(ptr))
		}
	}
	platformFailed := make(map[string]int, len(d.platformFailed))
	for p, ptr := range d.platformFailed {
		if ptr != nil {
			platformFailed[p] = int(atomic.LoadInt64(ptr))
		}
	}
	return DownloaderStats{
		Active:            d.ActiveCount(),
		Queued:            d.QueueLen(),
		Completed:         int(atomic.LoadInt64(&d.totalCompleted)),
		Failed:            int(atomic.LoadInt64(&d.totalFailed)),
		PlatformCompleted: platformCompleted,
		PlatformFailed:    platformFailed,
	}
}
