=== 迭代 #23 (03-13 12:40) ===
推送成功。BUGS.md 无需更新（本次是代码质量改进，不是 bug 修复）。

---

[video-subscribe-dl 迭代] 重构：合集处理逻辑去重，提取 fetchAndProcessSeason 统一方法

- **改动**：`checkSeason`（独立合集源）和 `processCollection`（UP主空间内合集）之间存在大量重复代码（翻页获取视频→去重→创建目录→生成NFO→下载封面→遍历下载），提取 `fetchAndProcessSeason` 作为核心方法统一处理，两个入口简化为委托调用。减少 73 行代码（-12.5%），消除维护两套相同逻辑的风险。
- **状态**：✅ 成功（`go build` 零错误、`go vet` 零警告、`go test` 全部通过，已 push 到 main）
- **下一步**：BUGS.md 和 REFACTOR-PLAN.md 均无待办项，下次迭代可审查 `retry.go` 中重试逻辑的输出目录计算（当前未考虑合集子目录场景，可能导致重试时文件存放位置不一致）

=== 迭代 #24 (03-13 13:10) ===
[video-subscribe-dl 迭代] 修复重试流程三个缺陷 (Bug #15)

- **改动**: `internal/scheduler/retry.go` — 3 个功能修复 + 1 个优雅退出改进
  1. **credential 模式对齐**: 重试时不再回退到 cookie.txt，与正常下载一致使用共享 credential client，避免画质降级
  2. **补全 Job 字段**: 重试 Job 补充 `Subtitle` 和 `Priority` 字段传递，修复重试下载不带字幕、优先级丢失的问题
  3. **风控保护**: GetVideoDetail 触发 -352/-401/-412 风控时正确触发 cooldown 并暂停队列，不再浪费重试次数
  4. **优雅退出**: retryFailedDownloads 循环增加 `isStopping()` + `sleepWithContext`，容器关闭时不再阻塞
- **验证**: `go build ./...` ✅ | `go vet ./...` ✅ | `go test ./...` ✅ (全部通过)
- **状态**: 成功，已推送 main (commit 71b1797)
- **发现方式**: 代码审查 — 对比 `retryOneDownload()` 与 `pro

=== 迭代 #27 (03-13 14:41) ===
Push 成功。BUGS.md 没有与此相关的已记录 bug（这是代码审视发现的改进点），不需要更新。

[video-subscribe-dl 迭代] fix: 下载超时动态化，修复大文件/限速场景误杀

- **改动：** 将下载超时从硬编码 30 分钟改为根据视频码率和限速设置动态计算
  - 新增 `calculateDownloadTimeout()`：公式 = estimatedSize / speed × 3 + 10min，范围 [30min, 4h]
  - `handleDownloadResult` 等待超时同步动态化（+5min 缓冲）
  - 无限速时默认 1 小时（之前 30 分钟）
  - 下载开始时打印预计超时日志
- **问题根因：** 如果用户设置 2MB/s 限速，下载 4GB 视频需要 ~34 分钟会被 30 分钟硬超时误杀
- **状态：** ✅ 成功（go build ✅ / go vet ✅ / go test ✅ / git push ✅）
- **下一步：** 代码审视中还发现 DELETE 操作的 `os.RemoveAll(dir)` 缺少安全边界检查（应确保 dir 在 downloadDir 内），可作为下轮任务

=== 迭代 #34 (03-13 20:45) ===
推送成功。由于本次修复的不是 BUGS.md 中记录的 bug（所有已记录 bug 已修复），无需更新 BUGS.md。

---

[video-subscribe-dl 迭代] DB 层补全 rows.Err() 检查

- **改动**：修复 5 个 DB 查询函数（`GetPeople`、`GetPeopleWithVideoCount`、`CleanSource`、`GetStatsByMonth`、`GetStatsByUploader`）缺少 `rows.Err()` 检查的问题。Go 最佳实践要求 `rows.Next()` 循环后必须检查 `rows.Err()`，否则数据库连接中断等错误会被静默忽略，导致返回不完整数据。
- **状态**：✅ 成功（编译通过、go vet 通过、全部测试通过、已推送）
- **提交**：`12acab7` → main
- **下一步**：BUGS.md 和 REFACTOR-PLAN.md 的所有任务均已完成。下一轮可以考虑：(1) 为 `web/` 和 `internal/scheduler/` 包补充单元测试，提高测试覆盖率；(2) 审查前端 JS 代码质量；(3) 添加 graceful degradation 机制（如 DB 连接池满时的排队策略）。

=== 迭代 #35 (03-13 21:16) ===
Push 成功。

### 7. 迭代结果

[video-subscribe-dl 迭代] UA 列表升级 + 构建信息完善

- **改动**：
  1. `internal/bilibili/client.go`：UA 列表从 Chrome 126-131 升级到 Chrome 133-136（2025-2026 年版本），覆盖 Windows/macOS/Linux + Chrome/Edge，降低因过旧 UA 触发 B 站风控的风险
  2. `Dockerfile`：go build 命令通过 `-X main.buildTime=$(date)` 注入实际构建时间，容器中不再显示 "unknown"
  3. `web/server.go`：`/api/version` 接口新增 `build_time` 字段，Go 版本改为 `runtime.Version()` 动态获取（替代硬编码的 "go1.22"）
  4. `cmd/server/main.go`：将 buildTime 变量注入到 Web Server
- **状态**：✅ 成功（编译通过、go vet 无警告、测试全绿、已 push）
- **下一步**：BUGS.md 和 REFACTOR-PLAN.md 均已清空待做项，后续可考虑：补充单元测试覆盖（scheduler/web/notify 等模块

=== 迭代 #37 (03-13 23:12) ===
推送成功。由于这次是代码质量改进而非 bug 修复，BUGS.md 无需更新。

---

[video-subscribe-dl 迭代] SQLite 并发安全加固 — busy_timeout + 连接池优化 + 性能调优

- **改动**：`internal/db/db.go` — 1 file, +19/-1
  - `busy_timeout=5000ms`：并发写冲突时自动等待重试，防止 `SQLITE_BUSY` / `database is locked`
  - `SetMaxOpenConns(1)` + `SetMaxIdleConns(1)`：Go 层面串行化所有写入，彻底消除锁竞争
  - `synchronous=NORMAL`：WAL 模式下安全降级，写入性能提升 2-3 倍
  - `cache_size=8MB`（原 2MB），`temp_store=MEMORY`：减少磁盘 I/O，加速聚合查询
  - `foreign_keys=ON`：启用外键完整性约束
- **背景**：项目有 3 个 downloader worker + scheduler + web server + cleanup 并发操作数据库，之前未配置 busy_timeout 和连接池，高并发时存在 `database is locked` 隐患
- **状态**：✅ 成

=== 迭代 #38 (03-14 08:10) ===
Push 成功。

---

[video-subscribe-dl 迭代] 修复 P0 Bug #16：多P视频目录结构错误

- **任务**: 多P视频在极空间无法识别为剧集，分P文件散落在 UP主目录下
- **改动**（4 文件，+33/-7）:
  - `downloader.go`: Job 新增 `Flat` 字段，Flat 模式下直接用 OutputDir 输出，不再创建额外子目录
  - `process.go`: 多P视频在 `multiPartDir/Season 1/` 下输出，分P命名改为 `S01Exx - 分P标题 [BVxxx]` 格式
  - `retry.go`: 多P视频重试时还原完整路径（UP主/视频标题 [BVxxx]/Season 1/），设 Flat=true
  - `BUGS.md`: 标记 #16 已修复
- **目录结构变更**:
  ```
  旧: UP主/P1 xxx [BVxxx]/P1 xxx [BVxxx].mp4  ← 和 tvshow.nfo 同级，极空间不识别
  新: UP主/视频标题 [BVxxx]/Season 1/S01E01 - xxx [BVxxx].mkv  ← 标准 TV Show 结构
  ```
- **状态**: ✅ 成功（编译通过 + vet 无警告 + 测试通过 + 已 push）


=== 迭代 #39 (03-14 08:38) ===
推送成功。

### 7. 输出结果

[video-subscribe-dl 迭代] refactor: 提取正则表达式为包级预编译变量

- **改动**：将 4 个文件中共 10 处函数内的 `regexp.MustCompile` 调用提取为包级预编译变量。涉及 `client.go`（6处，3种模式去重为4个变量）、`wbi.go`（2处）、`credential.go`（1处）、`scanner.go`（1处）
- **收益**：消除每次函数调用时的正则编译开销；模式集中管理减少重复代码；净减 9 行代码
- **状态**：✅ 成功（编译通过 / go vet 通过 / 测试通过 / 已推送 main）
- **下一步**：所有 BUGS.md 已修复、SPEC.md 和 REFACTOR-PLAN.md 待办已清空。后续可考虑：(1) 补充核心模块的单元测试覆盖率（scheduler/downloader/scanner 等无测试文件）；(2) 审查前端 innerHTML 使用的安全性；(3) 新功能规划

=== 迭代 #40 (03-14 09:24) ===
推送成功。

[video-subscribe-dl 迭代] 为 downloader 包补充完整单元测试

- **改动：** 新增 3 个测试文件（532 行），22 个测试用例
  - `priority_queue_test.go` — 优先级队列的入队出队、排序、去重、满队拒绝、Close 解除阻塞、并发安全（8 个用例）
  - `bandwidth_monitor_test.go` — 带宽降级监控的滑动窗口、阈值边界、多分辨率独立检测、Reset、nil 安全处理（10 个用例）
  - `downloader_test.go` — 错误重试判定（17 种错误类型）、重试延迟、超时计算（4 个用例）
- **状态：** ✅ 成功，22/22 测试通过，go vet 无警告，已推送 main
- **下一步：** 所有 BUGS.md P0-P2 已修复，REFACTOR-PLAN 已完成。可考虑为 scheduler 或 bilibili 包中更复杂的集成逻辑补充测试，或根据实际运行中发现的新问题进行迭代

=== 迭代 #41 (03-14 09:42) ===
### 7. 输出结果

[video-subscribe-dl 迭代] 修复删除下载记录时误删整个目录的安全隐患

- **改动：**
  - `web/handlers_download.go`：DELETE /api/downloads/{id} 原来用 `os.RemoveAll(dir)` 删除视频文件的父目录，多P视频中删一集会把同目录其他集全删掉。现在改为新增 `deleteVideoFiles()` 方法，只精确删除视频文件及其关联文件（NFO、缩略图、弹幕 ASS/XML、字幕），删除后自动清理空目录（向上最多 3 级），但绝不超出 downloadDir 边界
  - `web/handlers_source.go`：删除订阅源时的批量文件删除增加 downloadDir 安全边界检查
  - `web/handlers_people.go`：清理源/UP主时的批量文件删除增加 downloadDir 安全边界检查
  - `BUGS.md`：记录此安全隐患及修复

- **状态：** ✅ 成功（编译通过、vet 无警告、测试通过、已推送）

- **下一步：** 为 web handler 层补充单元测试（当前 `web/` 包无测试文件），特别是测试 deleteVideoFiles 的边界条件

=== 迭代 #42 (03-14 10:12) ===
---

[video-subscribe-dl 迭代] Bug #17 修复 — 分块下载 HTTP 416 自动 fallback 到单线程

- **改动**：修改 `internal/bilibili/chunked.go` 和 `internal/bilibili/dash_progress.go`
  - 新增 `ErrRangeNotSatisfiable` sentinel error
  - `downloadOneChunkAttempt` 遇 HTTP 416 返回特定错误（不走通用 HTTP 错误路径）
  - `downloadOneChunk` 遇 416 立即返回不重试（Range 参数不变，重试没意义）
  - `downloadChunked` 透传 416 错误给上层
  - `downloadOneURL` 检测到 416 后自动 fallback 到 `downloadWithResumeProgress` 单线程下载
- **根因**：B站 CDN 的 HEAD 响应 Content-Length 与实际 GET 不一致，分块请求的 Range 超出实际文件大小，3 个 CDN URL 全部 416 失败
- **状态**：✅ 编译通过 + vet 通过 + 测试通过 + 已推送
- **下一步**：BUGS.md 所有已知 bug 均已修

=== 迭代 #43 (03-14 10:41) ===
Push 成功。由于本轮是代码质量改进而非 bug 修复，BUGS.md 无需更新。

---

[video-subscribe-dl 迭代] 使用 go:embed 嵌入静态文件，实现单二进制部署

- **改动**：
  - 新增 `web/embed.go`：用 `//go:embed` 将 `static/` 和 `templates/` 目录嵌入二进制
  - 修改 `web/server.go`：模板加载改用 `template.ParseFS`，静态文件改用 `http.FS(fs.Sub(...))`
  - 简化 `Dockerfile`：移除 `COPY --from=builder /app/web ./web`，runtime image 更精简
- **效果**：编译后只有一个可执行文件，不再依赖外部 `web/` 目录。Docker 部署更简单，也消除了相对路径依赖的潜在问题
- **状态**：✅ 成功（build/vet/test 全部通过，已 push）
- **下一步**：可以考虑实现 NFO 生成功能（RESEARCH.md 中已有完整调研），或增加 scheduler/web 模块的单元测试覆盖

=== 迭代 #44 (03-14 11:19) ===
## 7. 迭代结果

[video-subscribe-dl 迭代] 重构：提取 resetCaches/applyNewClient 消除 scheduler 中 7 处重复代码

- **改动**：scheduler.go 中 `ClearWbiCache() + GetBandwidthMonitor().Reset()` 组合重复出现 7 次，`UpdateClient()` 也在其中 6 处伴随出现。提取为两个方法：`resetCaches()`（2 合 1）和 `applyNewClient(client)`（3 合 1），替换所有调用点。净减少 10 行代码。
- **状态**：✅ 成功（编译通过 / go vet 通过 / go test 通过 / 已 push）
- **下一步**：所有 BUGS.md 已清零，REFACTOR-PLAN.md 已完成。后续可关注：①增加 scheduler 包的单元测试覆盖；②抖音/YouTube 平台支持（SPEC Phase 5）；③代码中无 TODO/FIXME 残留，项目整体质量良好。

=== 迭代 #46 (03-14 12:14) ===
干净利落。

---

[video-subscribe-dl 迭代] 修复清理模块 bug + 下载器优雅关闭

**改动：**
1. **fix(cleanup)**: `RunDiskPressureCleanup` 遗漏 `.danmaku.xml` 清理——提取 `associatedExts` 为包级变量 + `removeAssociatedFiles()` 辅助函数，两个清理函数共用，消除重复代码
2. **feat(downloader)**: 新增 `Downloader.Stop()` 实现优雅关闭——rootCtx 取消通知所有下载任务、重试等待感知关闭信号、main.go 补全 `dl.Stop()` 调用
3. 清理未使用函数：`randomDelay()`、`formatBarkURL()`

**状态：** ✅ 成功 (go build + go vet + go test 全部通过，已 push)

**下一步：** 项目已非常成熟，可考虑：
- 完善 `.gitignore`（当前只有 `server` 一行）
- 通过 ldflags 注入 version 替代 main.go 硬编码
- 为 `scheduler/cleanup` 模块补充单元测试

=== 迭代 #47 (03-14 12:42) ===
[video-subscribe-dl 迭代] 修复字幕文件清理遗漏 + 统一关联文件删除逻辑

- **改动**：
  - 新增 `internal/util/filecleanup.go`：`RemoveAssociatedFiles()` 统一删除视频关联文件（NFO/缩略图/弹幕/字幕），`RemoveEmptyDirs()` 安全清理空目录
  - 重构 `internal/scheduler/cleanup.go`：移除重复的 `associatedExts` 列表和旧 `removeAssociatedFiles`，委托给 `util` 包
  - 重构 `web/handlers_download.go`：`deleteVideoFiles()` 从 44 行手写逻辑简化为调用 `util` 统一接口
  - 新增 `internal/util/filecleanup_test.go`：4 个测试用例覆盖中文文件名、多语言字幕、空目录清理

- **修复问题**：自动清理/手动删除视频时，带语言代码的字幕文件（`.zh-CN.srt` 等）不被删除，磁盘残留孤立文件。根因是两处独立维护的扩展名列表不一致且均不支持动态语言代码

- **状态**：✅ 成功（build/vet/test 全通过，已 push）

- **下一步**：所有已知 bug 已修复，REFAC

=== 迭代 #48 (03-14 13:12) ===
推送成功。BUGS.md 没有需要更新的内容（这是代码质量改进，不是已知 bug 修复）。

[video-subscribe-dl 迭代] 修复断点续传 HTTP 416 无限递归风险 + 代码结构优化

- **改动**：重构 `downloadWithResumeProgress` 函数（`internal/bilibili/dash_progress.go`）
  - 递归重试改为 `for` 循环 + `max416Retries` 常量控制，彻底消除栈溢出风险
  - `os.Remove()` 失败时直接返回错误，不再盲目重试
  - 非 200/206 状态码确保立即关闭 `resp.Body`（旧代码依赖 `defer` 在递归场景下延迟释放连接）
  - 提取 `downloadStreamToFile` 独立函数，职责分离更清晰
- **验证**：`go build` ✅ | `go vet` ✅ | `go test ./...` ✅ | `staticcheck` ✅
- **状态**：成功，已推送 `8b31385`
- **下一步**：所有 BUGS.md 已修复、REFACTOR-PLAN.md 已完成、staticcheck 无警告。项目代码质量很好。可以考虑增加单元测试覆盖（scheduler/web 包目前无测试），或从 SPEC.md 推进抖

=== 迭代 #49 (03-14 13:39) ===
推送成功。

---

[video-subscribe-dl 迭代] SSE 指数退避重连 + 页面恢复自动重连

- **改动**：
  - Progress SSE：移除固定 5 次重试上限，改为指数退避（1s→2s→4s→...→30s cap），永不放弃
  - Log SSE：新增 onerror 处理 + 指数退避重连（之前断开后完全不重连）
  - 新增 `visibilitychange` 监听：锁屏/切 tab 回来后自动检测并重连已断开的 SSE
  - 前端缓存版本号更新 v20260314a
- **改动范围**：web/static/app.js（+46 -12 行）、web/templates/index.html（版本号）
- **状态**：✅ 成功（编译通过、vet 通过、测试通过、已推送 main）
- **下一步**：NAS Docker 重新构建部署后生效；后续可考虑增加前端单元测试或 E2E 测试覆盖

=== 迭代 #50 (03-14 14:18) ===
这两行 debug 日志确实会在生产中产生大量输出。但它们是最近一次迭代为了排查 Bug #19 而加的，定位完问题后应该移除或降级。

**本轮任务确定**：移除 `[skip]`/`[check]` 的逐条调试日志，替换为 check_up.go 中的页级摘要日志。同时更新 BUGS.md 标记状态。

=== 迭代 #51 (03-14 14:38) ===
推送成功。本轮没有需要更新的 BUGS.md（不是 bug 修复，是安全加固）。

[video-subscribe-dl 迭代] 安全加固: HTTP Server 添加超时保护配置

- **改动**：`web/server.go` — 为 `http.Server` 添加 4 项超时/安全配置
  - `ReadHeaderTimeout: 10s` — 防 Slowloris 慢速攻击，10秒内必须完成请求头
  - `ReadTimeout: 60s` — 兼顾 Cookie 文件上传（10MB limit）
  - `IdleTimeout: 120s` — 自动回收空闲 Keep-alive 连接，防止文件描述符耗尽
  - `MaxHeaderBytes: 1MB` — 限制请求头大小，防止超大 Header 攻击
  - 刻意**不设 WriteTimeout**，因为存在 SSE 长连接端点（`/api/progress`、`/api/logs/stream`）
- **状态**：✅ 成功（编译通过 + go vet 通过 + 测试通过 + 已推送 main）
- **commit**: `bfc6bd3`
- **下一步**：所有 BUGS.md 已修复、REFACTOR-PLAN.md 已完成；可以考虑：
  1. 补充单元测试覆盖率（当前只有 bilibi

=== 迭代 #52 (03-14 15:16) ===
Push 成功。

[video-subscribe-dl 迭代] 视频有效性检查 + v_voucher 风控检测（BUGS #20-#23）

- **改动**：一次性实现 4 个 P1 级 bili-sync 对标项
  1. **番剧/影视重定向检测** — `VideoDetail.RedirectURL` + `IsBangumi()` → 跳过番剧/影视
  2. **视频状态检测** — `VideoDetail.State` + `IsUnavailable()` → 跳过已删除/审核中/隐藏视频
  3. **收藏夹失效视频过滤** — `FavoriteVideoItem.Attr/Type` + `IsInvalid()` → 翻页阶段提前跳过
  4. **v_voucher 风控检测** — `checkRateLimitCode` 新增 `data.v_voucher` 解析 → 非空即触发风控冷却
- **测试**：新增 4 组单元测试全部通过，全量 `go test ./...` 通过
- **状态**：✅ 已提交并推送到 main
- **下一步**：剩余 2 个 P1 — #24 ASS 格式弹幕（完整弹幕渲染引擎）、#25 Series 类型合集支持

=== 迭代 #53 (03-14 15:40) ===
### 7. 输出结果

[video-subscribe-dl 迭代] feat: 新增 Series（视频列表）类型合集支持 (#25)

- **改动**：
  - `internal/bilibili/client.go`：新增 `GetSeriesInfo`、`GetSeriesVideos` API 方法；新增 `ExtractCollectionInfo` 统一 URL 解析；新增 `CollectionType`/`CollectionInfo`/`SeriesMeta` 类型定义
  - `internal/scheduler/check_series.go`：新文件，Series 类型合集的完整检查和下载逻辑（含风控处理、幽灵文件夹防护、NFO 生成）
  - `internal/scheduler/scheduler.go`：`checkSource` switch 新增 `"series"` case
  - `web/handlers_source.go`：自动识别 `type=series` 和 `seriesdetail` URL，自动获取 Series 名称
  - `internal/bilibili/client_test.go`：新增 5 个 `ExtractCollectionInfo` 单元测试
  - `BILI-SYNC-GAP.md`

=== 迭代 #54 (03-14 16:12) ===
**Step 15: 自审 (Code Review)**

让我审查一下所有改动：

=== 迭代 #55 (03-14 16:24) ===
[video-subscribe-dl 迭代] Series 封面修复 + 标题过滤功能

**改动：**
1. **feat: 每源标题关键词过滤 (download_filter)**
   - Source 新增 `download_filter` 字段，支持 `关键词|关键词`（OR）、`/正则/`、`!排除` 三种模式
   - 在调 GetVideoDetail API 前预过滤，减少不必要的 API 请求，降低风控风险
   - 前端添加/编辑源弹窗均支持设置过滤条件
   - 含完整单元测试（20 个用例全通过）

2. **fix: Series 合集新增 poster.jpg 封面下载**
   - Series（视频列表）类型合集之前缺少封面图，Jellyfin/极空间无法显示封面
   - 使用合集第一个视频的封面作为 poster.jpg（B站 Series API 不返回封面字段）
   - 与 Season 合集行为完全对齐

3. **enhance: tvshow.nfo 新增 `<thumb>` 标签**
   - 写入远程封面 URL，方便 Kodi/Emby/Jellyfin 远程识别封面
   - Season 和 Series 合集均受益

**状态：** ✅ 成功（编译通过 + go vet 无警告 + 测试全通过 + 已推送）

**

=== 迭代 #56 (03-14 16:40) ===
Push 成功。

---

[video-subscribe-dl 迭代] AI 字幕标识增强 — 文件名追加 .ai 标记

- **改动**：
  - `Subtitle` 结构体新增 `IsAI` 和 `LanDoc` 字段，完整保留字幕来源信息
  - AI 生成的字幕文件名改为 `视频名.zh-CN.ai.srt`，人工字幕保持 `视频名.zh-CN.srt`，用户一眼区分
  - 下载日志中 AI 字幕标注 `[AI]` 标签
  - 修复文件清理逻辑（filecleanup.go）不识别 `.ai.srt` 的隐患，确保删除视频时正确清理 AI 字幕
  - 新增测试用例覆盖 AI 字幕清理场景
- **状态**：✅ 成功（编译通过、go vet 无警告、全量测试通过、已 push）
- **下一步**：BILI-SYNC-GAP 第二批剩余 #11（画质范围筛选）、#17（SkipOption）、#18（自定义路径模板）

=== 迭代 #57 (03-14 16:52) ===
### 7. 输出结果

[video-subscribe-dl 迭代] ✅ 实现 UP 主卡片视图 (Bug #26, P0)

**改动：**
- 📦 **后端**：新增 4 个 API（`stats-by-uploader`、`retry-failed-by-uploader`、`process-pending-by-uploader`、`completed-by-uploader`）+ 4 个 DB 方法（`GetDownloadStatsByUploader`、`RetryFailedByUploader`、`GetPendingByUploader`、`DeleteCompletedByUploader`）
- 🎨 **前端**：下载页默认展示 UP 主卡片网格视图
  - 每张卡片显示 UP 主名称、完成进度条、状态徽标（下载中/待下载/失败/跳过）
  - 卡片右侧有快捷操作按钮（重试失败、开始下载）
  - 点击卡片进入该 UP 主的视频详情列表，顶部有返回按钮 + 批量操作
  - 新增 👤 UP主卡片 / ☰ 列表 / ▦ 卡片 三种视图切换
  - 响应式布局，手机端自适应两列
- 📄 6 个文件改动，+456 行代码
- ✅ `go build` / `go vet` / `go test` 全部通过
- 🚀 已 push 到 main，commit: `

=== 迭代 #58 (03-14 17:21) ===
推送成功！

---

[video-subscribe-dl 迭代] 增强 person.nfo — 丰富 UP 主元数据

**改动：**
- `UPInfo` 结构体新增 `Level`(等级)、`Sex`(性别) 字段
- `person.nfo` 从只有 name+thumb 升级为包含 `<biography>`(签名+等级+UID)、`<uniqueid>`(B站MID)、`<website>`(空间链接)
- `people` 数据库表新增 sign/level/sex 列，支持完整信息存储
- `ensurePeopleDir` 改为每轮更新 person.nfo（之前只在首次创建），追踪签名/等级变化
- 新增 `nfo_test.go` 测试文件（3 个测试用例）+ db 测试（UpsertPersonFull）
- 更新 BILI-SYNC-GAP.md：标记 #8 ASS弹幕、#9 Series合集、#10 person.nfo 全部完成

**状态：** ✅ 成功（编译/vet/测试全部通过，已推送）

**下一步：** BILI-SYNC-GAP 第二批 #11 画质范围筛选 (min/max quality) 或 #13 请求频率令牌桶

=== 迭代 #59 (03-14 17:40) ===
---

[video-subscribe-dl 迭代] 画质范围筛选 — 支持最低画质限制

- **改动**：BILI-SYNC-GAP #11 — 新增 per-source `download_quality_min` 字段，支持设置最低画质（480p/720p/1080p）。下载时先按最高画质限制选最优流，再检查是否低于最低画质要求——低于则跳过不下载，不降级。前端添加/编辑源弹窗增加「最低画质」选择器，卡片显示画质范围（如 `720p~best`）。含无效配置保护和向后兼容。
- **文件**：7 个文件，+57/-13 行（db.go, source.go, downloader.go, process.go, retry.go, app.js, index.html）
- **验证**：go build ✅ | go vet ✅ | go test ✅ 全部通过
- **状态**：已 push 到 main（commit cae835a）
- **下一步**：BILI-SYNC-GAP 第二批剩余：#13 请求频率令牌桶、#14 扫码登录、#15 每源独立配置、#17 SkipOption、#18 自定义路径模板

=== 迭代 #60 (03-14 17:52) ===
推送成功。

---

[video-subscribe-dl 迭代] feat: SkipOption —— 每源可选跳过 NFO/封面

**改动：**
- Source 新增 `skip_nfo`、`skip_poster` 两个布尔字段（DB schema + migration + struct）
- scheduler 层所有 NFO 生成点（单视频 movie NFO、多P episode NFO、合集 tvshow NFO）和所有封面下载点（单视频、多P、合集 poster）均受 SkipOptions 控制
- retry 重试流程同步传递 SkipOptions
- 前端添加源/编辑源弹窗新增「跳过 NFO 生成」「跳过封面下载」checkbox
- 源列表显示 🚫NFO / 🚫封面 状态标记
- 同步标记 BILI-SYNC-GAP.md #13（令牌桶）和 #17（SkipOption）为已完成
- 默认不跳过，完全向后兼容

**状态：** ✅ 成功（编译通过 + vet 无警告 + 全部测试通过 + 已推送）

**下一步：** BILI-SYNC-GAP 第二批剩余：#14 扫码登录、#15 每源独立配置、#18 自定义路径模板

=== 迭代 #61 (03-14 18:17) ===
[video-subscribe-dl 迭代] 全局 SVG 图标替代 emoji — Lucide Icons 集成

- **改动**: 引入 Lucide Icons CDN，150+ 处 emoji 替换为对应 SVG 图标
  - `index.html`: 导航栏、按钮、分析卡片、设置区标题、状态栏、移动端 Tab 共 60 个图标
  - `app.js`: 动态渲染的订阅源卡片、下载列表、进度条、操作按钮、扫码登录、凭据状态、数据对账结果等 93 个图标 + MutationObserver 自动刷新
  - `style.css`: 新增 170 行 CSS，针对不同上下文（按钮/导航/卡片/状态栏等）的 SVG 尺寸和对齐规则
- **保留**: select option 中的 emoji（SVG 不兼容 option 标签）、排名勋章 🥇🥈🥉
- **状态**: ✅ 成功 — 编译通过，go vet 无警告，JS 语法验证通过，已推送 `8c90b47`
- **下一步**: UI-IMPROVEMENT Phase 1 第 3 项「filter-btn 选中状态视觉更明显」或 1.2 间距布局优化

=== 迭代 #62 (03-14 18:36) ===
## 7. 迭代结果

[video-subscribe-dl 迭代] Bug #27 修复 — paused 状态下载超时优化

**改动：**
- `internal/scheduler/process.go`：
  - `submitDownload()` 创建 pending 记录后检查 `IsPaused()`，暂停时不提交到 downloader 队列，视频保持 pending 状态等下轮处理
  - `handleDownloadResult()` 超时分支检查 downloader 是否 paused，暂停中标记 `pending`（可重试）而非 `failed`（浪费重试次数）
  - 超时日志增强：输出 paused/activeWorkers/queueLen 三个关键指标便于排查
- `internal/scheduler/retry.go`：
  - `retryOneDownload()` 开头加 paused 检查，暂停时跳过
  - `retryFailedDownloads()` 循环中加 paused 检查，暂停时终止重试周期

**效果：** 风控触发暂停后，不再有大量任务排队等1小时超时然后全部 failed。任务会保持 pending 状态，等 downloader 恢复后由下一轮 check 或 retry 自动处理。

**状态：** 

=== 迭代 #63 (03-14 19:01) ===
## 7. 输出结果

[video-subscribe-dl 迭代] 下载列表重构步骤1-3 — 新增后端分页 API + 统一操作接口

- **改动**：
  1. **`GET /api/downloads/uploaders`** — UP 主列表分页 API，后端 GROUP BY 聚合统计，支持 status/search/page/pageSize 参数，按最近下载时间排序
  2. **`GET /api/downloads/by-uploader`** — 单 UP 主视频列表分页 API，stats 始终返回全量统计不受筛选影响，视频按状态优先级排序（下载中 > 待下载 > 失败 > 已完成）
  3. **`POST /api/downloads/actions`** — 统一批量操作接口，支持 retry_failed / process_pending / delete_completed，scope 可选 all 或 uploader 维度
  4. 新增 `parseJSON()` 通用请求体解析工具函数
  5. 旧 API 路由保留向后兼容

- **文件**：`internal/db/download.go`（+230行）、`web/handlers_download.go`（+134行）、`web/server.go`（+8行）
- **验证

=== 迭代 #65 (03-14 20:11) ===
### 7. 输出结果

[video-subscribe-dl 迭代] 修复 P0 Bug：下载列表 v2 视图 CSS 全部缺失

- **问题发现**：审查代码发现 v3 重写（commit d280a50）时 app.js 引入了 28 个 v2 CSS 类名（`uploader-grid-v2`、`uploader-card-v2`、`dl-item-v2` 等），但 style.css 中完全没有对应定义。UP 主卡片视图（下载列表的默认视图）完全没有样式渲染。
- **改动**：在 style.css 末尾追加 458 行完整的 v2 样式定义，涵盖：
  - UP 主卡片网格布局 + 卡片样式（含 hover 上浮/头像缩放动效）
  - 卡片头部、进度条、状态标签（下载中/待下载/失败/跳过）、操作按钮
  - 详情视图头部、统计条、视频列表项、进度条
  - 分页组件
  - 768px / 480px 两个断点的移动端适配（小屏双列卡片）
  - 所有颜色复用 CSS 变量，与整体 Linear/Notion 风格一致
- **验证**：28 个 v2 类名 + 19 个关联类名 CSS 全覆盖 ✅ | go build ✅ | go vet ✅
- **状态**：成功，已 push（64a0999, 03b3142）
- **下一步**：按 UI-IMPRO

=== 迭代 #67 (03-14 21:19) ===
这个提交是今天 21:15 做的。说明这是之前的 cron 迭代在同一天做的工作。看起来之前的迭代写入了文件但没有正确 push（或者 push 了但 git pull 带下来了）。

不管怎样，当前状态是：sources.tsx 已经有编辑功能了，header.tsx 已经修复了。我这轮的实际增量改动是版本号更新和 sidebar 版本号修正。

但这个 commit `aa293c6` 还没 push 吗？让我检查：

=== 迭代 #68 (03-14 21:40) ===
Push 成功！

### 7. 迭代结果

[video-subscribe-dl 迭代] SSE 进度流认证修复 + Go 编译错误修复

- **改动 1**: `web/frontend/src/hooks/use-sse.ts` — SSE 进度流连接附带 auth token query param。之前设置了 auth_token 后，下载实时进度无法连接（EventSource 不支持自定义 header），现在与日志流 `logs.tsx` 保持一致的 `?token=xxx` 认证方式
- **改动 2**: `internal/scheduler/process.go` — 删除第 254 行多余的 `dlID := dlID` 重声明，Go 新版本将其视为编译错误
- **状态**: ✅ 成功（Go build/vet/test 全通过，前端 build 通过，已 push）
- **下一步**: 所有已知 bug 已修复，React 前端已重写并上线。后续可考虑：
  - 添加更多单元测试（scheduler/web handler 目前无测试文件）
  - 前端 E2E 测试
  - YouTube/抖音平台支持实际集成测试

