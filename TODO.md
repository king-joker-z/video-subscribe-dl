# TODO List — video-subscribe-dl

> 按优先级排列。已完成项归入底部完成记录。

---

## 工作规范

- ⚠️ **构建/测试全部走 GitHub CI，不在本地执行 `go build` / `go test`**
- 代码改完后 push 到 GitHub，等 CI 结果
- git push 前需经宇融确认

---

## 待处理队列

### [1] 前后端 API 交互问题 + 前端 UI 优化
- ~~UP 主卡片按钮溢出（手机端）~~ → 已修（按钮改为纵向堆叠 `flex-col`）
- ~~手机端日志入口缺失~~ → 已修（底部 Tab 将「设置」替换为「日志」）
- API 交互问题（待列举具体接口）
- 其他前端 UI 问题（待描述）

### [3] Bug 专项修复（全量代码审查）
- 由 JARVIS 自主全量审查代码，整理 bug 清单后逐一修复
- 重点模块：下载流程、状态机、并发安全、错误处理、边界条件

### [4] 代码质量小项（低优先级）
- 顶层 `scheduler/retry.go` 的 `retryOneDownload` 可加一道 `src.Enabled` 防御性检查（子调度器内已有，顶层缺少）
- UP 主删除弹窗改为自定义 Dialog（当前用 `window.confirm`，已在 `uploaders.js:91`）
- Dashboard 无任务时 recent_downloads 区域布局优化（当前条件渲染 `length > 0` 时才渲染，空态无占位）

---

## 完成记录

- [x] B站调度逻辑抽离到 bscheduler 子包（v2.1.1）
- [x] dscheduler 子包实现完成（2026-03-19）
- [x] scheduler 重构：顶层抖音逻辑完全委托给 dscheduler（`d9f369b`，2026-03-19）
- [x] Bug：手动迁移本地文件后仍触发重复下载 → completed→relocated，不再重置为 pending（`5fbff79`）
- [x] bscheduler/dscheduler 双平台完全隔离（Cookie/限流/冷却/暂停均独立）
- [x] retryOneDownload 在 bscheduler/dscheduler 内均已有 src.Enabled 检查
- [x] 设置页 dirty 状态提示（已实现，`settings.js:205` hasDirty 提示"N 项更改未保存"）
- [x] 极空间反代兼容：parse 接口 GET + base64 编码绕过 302 拦截（2026-03-23）
- [x] 抖音订阅弹窗重构（只保留长链接解析入口，去除抖音号和子 Tab）（2026-03-23）
- [x] 下载速度慢（2026-03-23 确认已解决）
- [x] 手机端 UP 主卡片按钮溢出修复（2026-03-23）
- [x] 手机端日志入口缺失：底部 Tab 加入日志，替换设置（2026-03-23）
