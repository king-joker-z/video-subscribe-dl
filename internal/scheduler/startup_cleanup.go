package scheduler

// containsInvalidChars 检查名称是否包含 SanitizePath 会过滤的字符
// 此函数保留在顶层包，供测试（cleanup_test.go）直接调用。
// B站调度器的 startup_cleanup 也有独立实现（bscheduler/startup_cleanup.go）。
func containsInvalidChars(name string) bool {
	for _, r := range name {
		switch {
		case r <= 0x001F: // C0 控制字符
			return true
		case r == 0x007F: // DEL
			return true
		case r >= 0x0080 && r <= 0x009F: // C1 控制字符
			return true
		case r == 0xFFFD: // 替换字符
			return true
		case r >= 0x200B && r <= 0x200F: // 零宽字符
			return true
		case r >= 0x2028 && r <= 0x202F: // 行/段分隔符等
			return true
		case r >= 0x2060 && r <= 0x206F: // 不可见格式字符
			return true
		case r == 0xFEFF: // BOM/零宽不断空格
			return true
		case r >= 0xFE00 && r <= 0xFE0F: // 变体选择器
			return true
		case r >= 0xE0000 && r <= 0xE007F: // 标签字符
			return true
		}
	}
	return false
}
