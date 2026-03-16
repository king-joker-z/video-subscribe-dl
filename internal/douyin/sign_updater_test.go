package douyin

import (
	"testing"
	"time"
)

// TestSignUpdaterAutoCheck 验证 StartAutoUpdate 启动后能正常停止
func TestSignUpdaterAutoCheck(t *testing.T) {
	su := &SignUpdater{
		client: nil, // 不实际发请求
	}
	su.StartAutoUpdate(100 * time.Millisecond)
	time.Sleep(350 * time.Millisecond) // 至少触发 2-3 次 tick
	su.StopAutoUpdate()                // 不 panic，不 goroutine 泄漏
}

// TestSignUpdaterStopWithoutStart 未启动时 StopAutoUpdate 不 panic
func TestSignUpdaterStopWithoutStart(t *testing.T) {
	su := &SignUpdater{}
	su.StopAutoUpdate() // 不 panic
}

// TestSignUpdaterDoubleStop 重复调用 StopAutoUpdate 不 panic
func TestSignUpdaterDoubleStop(t *testing.T) {
	su := &SignUpdater{}
	su.StartAutoUpdate(100 * time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	su.StopAutoUpdate()
	su.StopAutoUpdate() // 第二次不 panic
}
