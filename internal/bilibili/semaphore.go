package bilibili

// Semaphore 基于 channel 的信号量，控制并发数
// 参考 bili-sync 的 tokio::sync::Semaphore
type Semaphore struct {
	ch chan struct{}
}

// NewSemaphore 创建指定容量的信号量
func NewSemaphore(n int) *Semaphore {
	if n <= 0 {
		n = 1
	}
	return &Semaphore{ch: make(chan struct{}, n)}
}

// Acquire 获取一个许可（阻塞直到获取成功）
func (s *Semaphore) Acquire() {
	s.ch <- struct{}{}
}

// Release 释放一个许可
func (s *Semaphore) Release() {
	<-s.ch
}

// TryAcquire 尝试获取（非阻塞），返回是否成功
func (s *Semaphore) TryAcquire() bool {
	select {
	case s.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

// Available 返回当前可用许可数
func (s *Semaphore) Available() int {
	return cap(s.ch) - len(s.ch)
}
