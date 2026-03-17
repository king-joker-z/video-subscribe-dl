# VSD 迭代计划 2026-03-17b（第二轮）

> 触发: 宇融请求第二轮迭代
> 状态: 规划中
> 负责人: 蟹Bro（规划+Review）/ 码蟹（实现）

---

## 背景

上轮（第一轮）已完成：
- Cookie 过期自动检测 + dashboard banner
- 抖音下载进度 SSE 集成
- 空状态清除筛选按钮

本轮选价值高、工作量适中的方向，优先用户直接感知的功能。

---

## 本轮目标

### Task 1：抖音合集下载 [P2]

**背景：** ITERATION-TODO.md 列为 P2，API 端点已在 endpoints.go 定义好，核心工作是实现合集抓取逻辑并接入调度器。

**API：** `/aweme/v1/web/mix/aweme/`，参数 mix_id，分页方式与用户视频列表类似（cursor-based）。

**方案：**
1. `internal/douyin/client.go` 新增 `GetMixVideos(mixID string) ([]DouyinVideo, error)` 方法
   - 分页抓取合集中所有视频（cursor 分页，与 GetUserVideos 类似）
   - 同样走签名 + 限流 + Referer
2. `internal/douyin/types.go` 补充合集相关数据结构（MixInfo、mix_id 字段）
3. `internal/scheduler/check_douyin.go` 新增 `checkDouyinMix(src db.Source)` 函数
   - 从 source.URL 中解析 mix_id
   - 调用 `GetMixVideos` 获取视频列表，其余逻辑复用 checkDouyin
4. `internal/scheduler/scheduler.go` 根据 source.type == "douyin_mix" 分发到 checkDouyinMix
5. 前端 `pages/sources.js` 的 typeLabels/typeColors 增加 `douyin_mix: '抖音合集'`
   - 添加订阅源时，URL 格式提示支持抖音合集链接

**TDD：**
- `client_test.go` 中 mock HTTP 测试 GetMixVideos 的分页逻辑
- `check_douyin_test.go` 补充 checkDouyinMix 测试用例

**验收：**
1. 可以添加抖音合集订阅源（URL 格式：`https://www.douyin.com/collection/{mix_id}` 或包含 mix_id 的链接）
2. 调度器定时检查合集，增量下载新视频
3. go build/vet/test 通过

---

### Task 2：视频搜索增强 — 支持按上传者名字搜索 [P2-轻量]

**背景：** 当前搜索只搜 title，用户无法通过输入 UP主名字快速找到某人的视频。

**方案：**
- 后端 `web/api/videos.go` HandleList：search 参数同时匹配 `v.uploader LIKE ? OR v.title LIKE ?`
- 前端无需改动（搜索框已有）

**TDD：**
- `web/api/api_test.go` 增加：搜索上传者名字返回对应视频的测试用例

**验收：**
1. 搜索框输入 UP主/作者名能找到对应视频
2. 原有标题搜索不受影响
3. go build/vet/test 通过

---

### Task 3：日志页面增强 — 支持级别过滤 + 暂停/恢复滚动 [P3]

**背景：** 日志页面（logs.js）只有全量日志流，INFO/WARN/ERROR 混在一起，用户排查问题时很难聚焦错误日志。

**方案：**
- 前端 `pages/logs.js` 增加级别过滤器（ALL / INFO / WARN / ERROR）
  - 从 WebSocket 接收的日志条目中，按 `level` 字段（或正则匹配 `[WARN]`/`[ERROR]` 等）过滤
  - 过滤是纯前端操作，不修改后端
- 增加"暂停滚动"按钮（当用户向上滚动时自动暂停自动滚动；点击按钮或滚到底部时恢复）

**验收：**
1. 日志页有级别过滤器，选 ERROR 只显示错误日志
2. 向上滚动时停止自动滚动，底部出现"回到底部"按钮
3. 不修改后端代码，纯前端改动

---

## 实施顺序

Task 2（30分钟）→ Task 3（45分钟）→ Task 1（3小时）

---

## 验收清单（蟹Bro Review 用）

### Stage 1 - Spec 合规
- [ ] Task 1: GetMixVideos 能正确抓取合集视频
- [ ] Task 1: scheduler 能处理 douyin_mix 类型订阅源
- [ ] Task 1: 前端可添加 douyin_mix 订阅源
- [ ] Task 2: 搜索上传者名字能找到视频
- [ ] Task 3: 日志页有级别过滤器
- [ ] Task 3: 日志页暂停/恢复自动滚动

### Stage 2 - 代码质量
- [ ] 新增 Go 代码有测试（TDD）
- [ ] go build/vet 无错误
- [ ] 无 goroutine 泄漏
- [ ] 错误处理完整

---

## 蟹Bro 禁止事项
- 不直接 edit/write VSD 代码文件
- 只跑验证命令
- Review 通过后 push
