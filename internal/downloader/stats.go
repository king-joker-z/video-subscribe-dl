package downloader

import "sync/atomic"

// DownloaderStats 下载器统计信息
type DownloaderStats struct {
	Active    int `json:"active"`
	Queued    int `json:"queued"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// Stats 返回下载器当前统计信息
func (d *Downloader) Stats() DownloaderStats {
	return DownloaderStats{
		Active:    d.ActiveCount(),
		Queued:    d.QueueLen(),
		Completed: int(atomic.LoadInt64(&d.totalCompleted)),
		Failed:    int(atomic.LoadInt64(&d.totalFailed)),
	}
}
