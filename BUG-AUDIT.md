# Bug 审查报告 — video-subscribe-dl
> 审查时间：2026-03-25
> 审查范围：全量代码（8 个核心文件 + 辅助文件）
> 审查人：Claude Opus 4.6（资深 Go + 前端工程师视角）

---

## 严重（P0）— 可能导致数据丢失或崩溃

| 文件 | 行号 | 问题描述 | 建议修复 |
|------|------|----------|----------|
| `internal/bilibili/chunked.go` | 241–251 | **断点续传进度双计** — `downloadOneChunkAttempt` 在检测到已有部分下载文件时，无论是「已完成」（`downloaded >= expected`）还是「继续续传」分支，都调用了 `atomic.AddInt64(totalDownloaded, downloaded)`。这意味着每次 retry 时，已下载的字节量会被重复加进 `totalDownloaded`，导致进度超出 100% 乃至整数溢出（`totalDownloaded > totalSize`），进而使进度回调产生 NaN/∞ 或除零崩溃。 | 仅在首次进入（无既有文件）时计入已有大小；或在函数开头先将 `totalDownloaded` 初始化为 0 并只加增量，而非每次 attempt 都累加历史字节数。 |
| `internal/db/source.go` | 139–145 | **非事务性双步删除** — `DeleteSource` 先判断 `activeCount`，再分两条独立 SQL 删除 downloads 和 source。两次 Exec 之间若进程崩溃或 SQLite 异常，会出现「downloads 已删、source 未删」或「source 已删、downloads 残留」的数据不一致。相同问题在 `DeleteSourceWithFiles`（行 152–192）中也存在：`DELETE FROM downloads` 和 `DELETE FROM sources` 是独立事务。 | 将两次 DELETE 包在同一 `BEGIN/COMMIT` 事务中执行，确保原子性。 |
| `internal/bilibili/chunked.go` | 185–203 | **合并阶段块文件泄露** — `downloadChunked` 合并循环（行 191–203）若 `io.Copy` 失败（`f.Close()` 之后 `return`），后续的块文件（`chunk{i+1}..chunk{n-1}`）**不会被删除**，遗留在磁盘。原文件的错误路径只清理了已打开的那一个块，其余块泄露。 | 合并失败时添加统一清理逻辑：`for _, cf := range chunkFiles { os.Remove(cf) }` 或使用 `defer` 结合 succeeded 标志位。 |
| `internal/db/download.go` | 395–404 | **`GetDownloadsBySourceName` Scan 列数不一致** — SQL SELECT 包含 16 列（含 `downloaded_at`），但 `rows.Scan` 直接把 `downloaded_at` 映射到 `&dl.DownloadedAt`（`*time.Time`）而非 `sql.NullTime`，当 `downloaded_at` 为 NULL 时会 panic（`sql: Scan error: cannot assign nil to *time.Time`）。其他同类函数（`GetDownloads`、`GetDownloadsByStatus` 等）都正确使用了 `sql.NullTime`，唯独此函数遗漏。 | 仿照同文件其他函数，声明 `var downloadedAt sql.NullTime`，Scan 后条件赋值：`if downloadedAt.Valid { dl.DownloadedAt = &downloadedAt.Time }`。 |

---

## 重要（P1）— 功能异常或用户可见 bug

| 文件 | 行号 | 问题描述 | 建议修复 |
|------|------|----------|----------|
| `internal/bilibili/client.go` | 482–512 | **`get` 方法：JSON 反序列化与风控检测串行但共用 body，顺序有问题** — 第 507 行先 `json.Unmarshal(body, result)`，第 511 行再 `checkRateLimitCode(body)` 再次 Unmarshal。若业务层调用者的 `result` struct 的 `Code` 字段正确匹配了风控码（如 -352），业务层会先返回 `"bilibili: -352 风控校验失败"` 错误，而 `checkRateLimitCode` 返回的 `*BiliError` 被丢弃（`return checkRateLimitCode(body)` 覆盖了之前 Unmarshal 成功的情况）。实际逻辑是**先完成业务 JSON 解析，再以 checkRateLimitCode 的返回值覆盖 nil 错误**——当 `json.Unmarshal` 成功但业务 Code 非 0 时，业务调用方已经处理了错误，但如果业务层没有对 Code 做检查（依赖 `get` 内部统一返回错误）就会漏检。不过 `get` 本身并不检查 `resp.Code != 0`，该检查在每个调用处做，因此 **`checkRateLimitCode` 的返回值实际上会覆盖掉 `json.Unmarshal` 成功（nil）的返回**，仅在出现风控时才有意义。问题在于：当 API 返回 `-352` 时，`json.Unmarshal` 不会报错（JSON 有效），`checkRateLimitCode` 返回 `*BiliError`，调用者收到该错误后会把 download 标为 failed，但该错误**不是 `ErrRateLimited` 包装**，导致 scheduler 层如果用 `errors.Is(err, ErrRateLimited)` 判断风控会**失败**（`ErrRateLimited` 变量在 client.go 第 47 行定义但 `checkRateLimitCode` 返回的是 `NewErrorResponse` 而非 `ErrRateLimited`）。 | 在 `NewRiskControlError` 和 `NewErrorResponse`（-352/-401/-412）中将错误包装为 `fmt.Errorf("...: %w", ErrRateLimited)`，使调用者可通过 `errors.Is` 统一检测。 |
| `internal/bilibili/client.go` | 487 | **`http.NewRequest` 错误未检查** — `req, _ := http.NewRequest("GET", rawURL, nil)` 忽略了 error，若 `rawURL` 格式非法，`req` 为 nil，后续 `req.Header.Set(...)` 会 panic（nil pointer dereference）。 | 改为 `req, err := http.NewRequest(...); if err != nil { return err }`。 |
| `internal/pornhub/client.go` | 596–604 | **goja JS 超时只中断 VM 但 goroutine 继续阻塞** — `time.AfterFunc(10*time.Second, func() { vm.Interrupt("js eval timeout") })` 可以中断 JS 执行，但 `vm.RunString(js)` 仍在当前 goroutine 同步执行。若 goja 的 Interrupt 机制未能及时生效（如 JS 在 native Go bridge 里），`RunString` 调用可能继续阻塞超过 10 秒，长时间占用下载 goroutine。 | 在独立 goroutine 中执行 `vm.RunString`，主流程使用 channel + `select` + timeout 等待结果，超时时强制放弃并返回错误。 |
| `internal/pornhub/client.go` | 348–350 | **GetModelVideos 翻页无速率限制策略变更风险** — 固定 `time.Sleep(2 * time.Second)` 延迟，若 Pornhub 调整反爬策略需要更长延迟，代码无法动态调整，且硬编码的 `maxPageHardLimit = 1000` 在一个博主有大量视频时会产生长达 33 分钟的阻塞，占满调度 goroutine。 | 将页间延迟和 `maxPageHardLimit` 提取为可配置参数；或通过 context 传入超时控制，以便外部取消。 |
| `web/api/sources.go` | 233–273 | **`HandleCreate` 中抖音 `dyClient.Close()` 被 `defer` 到函数末尾** — `defer dyClient.Close()` 在 `case "douyin":` 分支内（第 235 行），只有当 source.Type 为 "douyin" 时才执行，但 defer 绑定的是**整个 `HandleCreate` 函数**的生命周期。若后续在函数内有其他操作（如 `h.db.CreateSource`），Close 仍会等到函数返回才执行——这部分没有问题。然而第 611 行 `HandleParse` 中同样 `defer dyClient.Close()` 也绑定了整个函数，而 `HandleParse` 在抖音号查询失败时（第 622 行）调用 `apiError` 并 `return`，`defer` 会正常执行，但**若在 defer Close 之前程序 panic**，Close 方法内部的资源（浏览器进程等）是否能被正确清理取决于 `douyin.Client` 实现。 需确认 `dyClient.Close()` 是幂等且 panic-safe 的。 | 确认 `Close()` 实现是幂等的；如果 `Close()` 内部有可能 panic，需要用 recover 包裹或使用 `defer func() { if r := recover(); r != nil {...}; dyClient.Close() }()`。 |
| `web/api/sources.go` | 56 | **`GetSourcesStats` 错误被忽略** — `statsMap, _ := h.db.GetSourcesStats()` 丢弃了 error。若数据库查询失败（如 disk full），statsMap 为 nil，后续 `buildStats` 循环中 `if st, ok := statsMap[s.ID]` 不会崩溃（nil map 读取安全），但 stats 全为 0，导致前端展示数据静默错误，用户看到全部计数为 0 无法感知。 | 至少 `log.Printf` 记录错误；可在 statsMap == nil 时向前端返回警告字段。 |
| `internal/db/download.go` | 309–316 | **`ResetStaleDownloads` 只重置 `downloading` 为 `pending`，但注释说 `pending → 删除记录`** — 函数注释（第 306 行）写道「pending -> 删除记录」，但实际代码**完全没有处理 pending 记录**，只重置了 downloading → pending。若存量 pending 记录在重启后不被清理，scheduler 会重复提交同一批任务，导致下载队列膨胀和重复下载。 | 若设计意图确实是清理 pending，在函数中增加 `DELETE FROM downloads WHERE status = 'pending'`；若意图已改变（不清理 pending），同步更新注释，避免误导。 |
| `web/static/js/pages/videos.js` | 87 | **SSE download 事件处理：`load` 函数在 stale closure 中捕获** — `useEffect` 的依赖数组是 `[load]`，而 `load` 通过 `useCallback` 随 `[page, pageSize, status, search, sort, uploader, sourceId]` 变化而重建。但第 87 行 `setTimeout(load, 500)` 中的 `load` 是该 effect 注册时的版本——由于 `load` 引用在 `useCallback` 中稳定，这里实际没有 stale closure 问题。**真正的问题**是：当 `evt.type` 为 'completed' 或 'failed' 时，`setVideos` 只做本地状态 patch（行 92–100），但 `v.file_path`、`v.detail_status` 等字段不会更新——用户需要刷新页面才能看到完整的完成状态（如文件路径、文件大小不准确）。 | 在 `completed` 事件处理后，追加一次 `setTimeout(load, 1000)` 完整刷新，以同步所有字段。 |
| `web/static/js/pages/videos.js` | 453 | **`detectPlatform` 正则误匹配 Pornhub** — `if (/^ph[0-9a-f]+$/i.test(videoId) \|\| /^[a-z0-9]{8,20}$/i.test(videoId)) return 'pornhub'`：第二个正则 `/^[a-z0-9]{8,20}$/i` 极宽泛，8–20 位字母数字均匹配，会把抖音的短 video_id（如 `7234567890123456789` 截断后）或其他 ID 误判为 pornhub，导致封面 fallback 显示 🔞 logo 而非正确 logo。 | 移除第二个宽泛正则，或将其排在 pornhub 专属 `ph[0-9a-f]+` 之后并增加长度限制；同时在 pornhub 判断前先排除已知平台（bilibili/douyin）。 |
| `web/static/js/pages/sources.js` | 337–352 | **`load` 未设置 `setLoading(true)`** — `SourcesPage.load` 函数在请求发起时没有调用 `setLoading(true)`（只有在 `finally` 中 `setLoading(false)`），用户在页面切换或刷新时看不到加载状态，视觉上卡顿。（初始状态 `loading=true` 是正确的，但后续刷新时 loading 状态不会闪烁。） | 在 `load` 函数开头添加 `setLoading(true)`。 |

---

## 一般（P2）— 代码质量、边界条件

| 文件 | 行号 | 问题描述 | 建议修复 |
|------|------|----------|----------|
| `internal/bilibili/chunked.go` | 104 | **整除截断导致最后一块不足** — `chunkSize := totalSize / int64(numChunks)` 整除时若 `totalSize` 不被整除，最后一块 End 确实被修正为 `totalSize - 1`（行 113），但 chunk 0..n-2 的 End 各为 `(i+1)*chunkSize - 1`，这是正确的。实际无 bug，但可读性欠佳。 | 添加注释说明最后一块的修正逻辑。 |
| `internal/bilibili/client.go` | 474 | **`FetchDynamicVideosIncremental` 注释说「最多 50 页」但代码限制是 200 页** — 安全限制注释（行 468 `// 安全限制：最多翻 50 页`）与实际判断（`if pageIdx >= 200`）不一致，易造成误解。 | 将注释改为「最多翻 200 页」。 |
| `internal/bilibili/client.go` | 557–560 | **`ExtractMID` 用 `fmt.Sscanf` 解析正则已确认为纯数字的字符串** — 正则 `reSpaceMID` 已确保 `m[1]` 是 `\d+`，再用 `fmt.Sscanf(m[1], "%d", &mid)` 解析多余。若数字超出 int64 范围 `fmt.Sscanf` 会静默截断而不报错。 | 改用 `strconv.ParseInt(m[1], 10, 64)` 并处理错误。 |
| `internal/pornhub/client.go` | 151–154 | **`GetModelInfo` URL 清理逻辑可能过度裁剪** — `strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(modelURL, "/"), "/videos"), "/")` 若 URL 为 `https://www.pornhub.com/model/foo-videos`（博主名本身含 "videos"），会被错误裁剪为 `https://www.pornhub.com/model/foo`。 | 只去掉路径末尾的 `/videos` segment，使用正则或 `path.Base`/`strings.HasSuffix` 精确匹配。 |
| `internal/douyin/download.go` | 49–50 | **`DownloadFile` 的 `MkdirAll` 错误被忽略** — `os.MkdirAll(filepath.Dir(destPath), 0755)` 的错误被丢弃，若目录创建失败（如权限不足），后续 `os.Create(tmpPath)` 会返回不直观的错误信息。 | `if err := os.MkdirAll(...); err != nil { return 0, fmt.Errorf("mkdir: %w", err) }`。 |
| `internal/douyin/download.go` | 102–103 | **`DownloadThumb` 跳过逻辑不检查是否为文件** — `if _, err := os.Stat(destPath); err == nil { return nil }` 当 `destPath` 存在但是一个**空目录**时会跳过下载，导致封面图实际缺失。 | 增加 `info.Size() > 0` 检查，同时确认不是目录（`!info.IsDir()`）。 |
| `internal/db/source.go` | 200–215 | **`CleanSource` 只扫描 `completed` 状态的文件路径，但不实际删除文件** — 函数名为 `CleanSource`，但只统计了 `file_path != ""` 的数量，并没有调用 `os.Remove`，只删除了数据库记录。如果调用方期望清理磁盘文件，会悄悄失败。 | 若函数意图仅是清理 DB 记录（不删文件），改名为 `ClearDownloadRecords` 或在注释中明确说明；若需要删文件，补充 `os.RemoveAll(path)` 逻辑。 |
| `internal/db/download.go` | 433–434 | **`GetDownloadUploaders` COUNT 查询动态 SQL 拼接，但 `where` 变量包含未参数化的 `AND d.uploader != ''`** — `where` 变量由 `"WHERE 1=1"` 和条件拼接而成，虽然目前的条件都通过 `?` 参数化，但若将来有人直接在 `where` 里拼用户输入，会引入 SQL 注入。 | 保持现状并在注释中明确 `where` 只允许白名单条件追加；或改用 query builder。 |
| `web/api/sources.go` | 328–331 | **`HandleGet` 中 4 条 `QueryRow` 独立执行** — `h.db.QueryRow(...)` 被调用 4 次且每次错误被忽略（`.Scan` 返回的 error 丢弃）。若数据库临时不可用，`videoCount` 等字段会静默返回 0，与正常值无法区分。 | 在 `HandleGet` 的统计逻辑中处理 Scan 错误，至少 log；或将 4 条 COUNT 合并为一条 GROUP BY 查询（如 `GetSourcesStats` 的做法）减少查询次数。 |
| `web/api/events.go` | 93–100 | **SSE 发送 `logCh` 为 nil 时 case 永久阻塞** — 当 `appLogger == nil`（logCh 未初始化，为 nil）时，`case entry, ok := <-logCh:` 对 nil channel 的 select 分支永远不会被选中（这是 Go 的正确行为）。但若 `dlEventCh` 也为 nil，for-select 唯一活跃的 case 是 `ticker.C` 和 `ctx.Done()`，这是预期行为。总体无 bug，但应加注释说明 nil channel 的 select 语义，避免维护者误删。 | 添加注释：`// nil channel 的 select case 永远不会被选中，相当于禁用该分支`。 |
| `web/static/js/pages/videos.js` | 109–135 | **定时刷新 `load` 函数通过 closure 捕获，schedule 的递归调用正确** — 但 `visibilitychange` handler 中 `load()` 是直接调用的**闭包引用**，当 `load` 因依赖项变化重新创建后，旧的 effect 的 cleanup 会移除旧的 handler，但在 React StrictMode 下 effect 会双执行，可能导致两次刷新。 | 使用 `useRef` 存储最新 `load` 引用，`visibilitychange` handler 通过 ref 调用，避免 stale closure。 |
| `web/static/js/pages/sources.js` | 481–499 | **`handleImportFile` 中 `input` 元素未 append 到 DOM** — `document.createElement('input')` 后直接 `.click()` 在部分浏览器（Firefox）中不触发文件选择对话框，需要先 `document.body.appendChild(input)` 再 click，事后 remove。 | 在 `input.click()` 前加 `document.body.appendChild(input)`，在 `onchange` 处理完毕后加 `document.body.removeChild(input)`。 |
| `internal/db/db.go` | 179–181 | **迁移循环静默忽略所有错误** — `for _, m := range migrations { db.Exec(m) }` 对已存在列的 `ALTER TABLE ADD COLUMN` 会返回 `duplicate column` 错误，这是预期的（幂等迁移），但若某条迁移真的失败（如语法错误或磁盘满），错误会被完全吞掉，应用继续运行但 schema 不完整。 | 区分「列已存在」错误（可忽略）和其他错误（应 log 或 fatal）：`if err != nil && !strings.Contains(err.Error(), "duplicate column") { log.Printf("[migration] %s: %v", m, err) }`。 |
| `web/api/sources.go` | 853 | **`HandleExport` 中 `w.Write(data)` 忽略错误** — 若写入过程中客户端断开，错误被丢弃，无 log 记录。 | `if _, err := w.Write(data); err != nil { log.Printf("[export] write error: %v", err) }`（Header 已发送，无法再 apiError，但至少记录日志）。 |

---

## 已确认正常（可疑但实际无问题）

- **`internal/bilibili/chunked.go` 第 161–167 行：goroutine 传参** — `go func(idx int) {...}(i)` 正确地通过参数传递 `i`，不存在 loop variable capture 问题（Go 1.22 起已修复，且此处本身也已传参）。

- **`internal/bilibili/client.go` 第 86–91 行：`UpdateCredential` 线程安全注释** — 注释明确「线程安全由调用方保证」，`Client` 本身设计上是单 credential 场景，scheduler 层通过单一 goroutine 更新，实际无竞争。

- **`internal/pornhub/client.go` 第 596–600 行：`goja.New()` + `time.AfterFunc` 超时** — 虽然超时仅中断 JS 而非强制 goroutine 退出（见 P1），但对于正常的 JS 执行（非死循环）`Interrupt` 可靠生效，超时保护在绝大多数场景有效。

- **`internal/db/db.go` 第 148–149 行：`SetMaxOpenConns(1)`** — SQLite 串行化写入模式，配合 `busy_timeout(5000)` 和 WAL 模式，在当前单机部署场景下不会产生死锁，连接池设置合理。

- **`internal/bilibili/client.go` 第 482–512 行：`get` 方法中双次 JSON 反序列化** — 虽然效率略低（body 被 Unmarshal 两次），但 `body` 是已读入内存的 `[]byte`，不涉及 body 流重复读取问题，功能上没有 bug（只是性能小缺陷）。

- **`web/static/js/pages/videos.js` 第 77–81 行：SSE progress 事件通过 `window` 自定义事件分发** — 通过全局单例 EventEmitter 模式分发进度，避免了多个组件各自建立 SSE 连接，设计合理；cleanup 通过 `return () => window.removeEventListener(...)` 正确清理，无内存泄露。

- **`web/api/events.go` 第 339–344 行：WebSocket 连接 defer 注销** — `defer func() { h.wsMu.Lock(); delete(h.wsConns, ws); h.wsMu.Unlock(); ws.close() }()` 正确在连接结束时清理，不存在连接泄露。

- **`web/static/js/pages/sources.js` 第 455–464 行：`checkingIds` 使用 `new Set([...prev, id])` 更新** — 正确使用函数式更新和不可变 Set，并发多次 sync 触发时不会产生状态竞争。

- **`internal/db/download.go` 第 160–168 行：`MarkPermanentFailed`** — 仅标记状态，不做删除，保留审计链，设计正确；配合 `GetRetryableDownloads` 的 `retry_count < maxRetries` 过滤，不会重复标记。

- **`internal/bilibili/chunked.go` 第 148–167 行：errs 数组并发写入安全性** — 每个 goroutine 写入固定下标 `errs[idx]`，不同 goroutine 写不同索引，无竞争条件；`wg.Wait()` 后再读取 `errs` 也确保了 happens-before 关系。

---

*报告完成。共发现 P0 级 bug 4 个、P1 级 bug 10 个、P2 级 bug 13 个。*
