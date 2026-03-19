# 2026-03-19 变更记录

> 本次会话共进行**十轮代码审查**，涉及 40+ 个 commit，覆盖调度器重构、稳定性修复、前端重构、防风控优化、全量代码质量审查。

---

## 项目整体能力概览

### 核心架构

```
video-subscribe-dl
├── cmd/server          — 入口，组装所有模块
├── internal/
│   ├── bilibili/       — B站 API 客户端（视频、UP主、合集、收藏夹、WBI鉴权）
│   ├── douyin/         — 抖音客户端（用户主页、合集、分享链接解析）
│   ├── db/             — SQLite 数据层（sources / downloads / settings / people）
│   ├── downloader/     — B站下载器（DASH 合并、分块并行、限速、SSE 进度）
│   ├── scheduler/
│   │   ├── bscheduler/ — B站调度器（增量/全量扫描、重试、风控冷却）
│   │   └── dscheduler/ — 抖音调度器（独立暂停、Cookie 检测、限速、SSE）
│   ├── filter/         — 视频过滤规则（简单关键词、高级多字段规则）
│   ├── nfo/            — NFO 元数据生成（movie / tvshow / episode 格式）
│   ├── scanner/        — 文件系统对账（孤儿文件检测、stale 状态修复）
│   ├── notify/         — 通知（Webhook / Telegram / Bark，异步重试队列）
│   └── config/         — 全局配置常量
└── web/
    ├── api/            — REST API（sources/videos/uploaders/settings/events/search…）
    ├── server.go       — HTTP 服务（中间件：鉴权、限速）
    └── static/js/      — 前端 SPA（React + Tailwind，无构建工具）
```

### 支持的订阅类型

| 类型 | 说明 |
|---|---|
| `up` | B站 UP 主主页，增量同步新视频 |
| `season` | B站合集（season/series），全量同步 |
| `favorite` | B站收藏夹，增量同步 |
| `watchlater` | B站稍后再看列表 |
| `douyin` | 抖音用户主页，增量同步 |
| `douyin_mix` | 抖音合集/图集 |

### 下载状态机

```
pending → downloading → completed
                     → failed → (自动重试) → permanent_failed
                     → charge_blocked（充电专属/付费视频）
         → cancelled（用户手动取消）→ 可通过"恢复下载"重新入队
completed → relocated（文件被移动）
completed → deleted（用户软删除，可恢复）
completed → cleaned（磁盘压力自动清理）
```

### 防风控策略

**B站：**
- Worker 单线程串行下载，请求间隔 15s + 随机 jitter
- 翻页间隔 5~10s，每 3 页额外等待 10~15s（fullScan 同步）
- 风控触发（-352/-412/HTTP412/v_voucher）→ 永久冷却（手动恢复）
- WBI keys 缓存 6h，读写分离（RWMutex + 双重检查锁）
- Cookie 每 6 小时自动验证

**抖音：**
- 下载限速器 `(3, 1, 15s)`，稳态约 4 个/分钟
- 翻页间隔 5~10s，fullScanDouyin 同步
- 风控触发 → 自动 Pause，Web UI 手动恢复
- Cookie 每次 CheckDouyin 时，距上次 >1 小时则验证

---

## 今日（2026-03-19）变更详情

### 一、调度器重构（早期）

**顶层 Scheduler 委托化重构**
- 删除旧版抖音相关文件 11 个（~1100 行）
- 顶层 `Scheduler` 新增 `douyin *dscheduler.DouyinScheduler`，与 `bscheduler` 模式统一
- `web/api/douyin_status.go` 改为注入函数，移除全局变量依赖
- 新增 dscheduler 测试文件 5 个

---

### 二、P1 Bug 修复：文件迁移后触发重复下载

- `resetDownloadsForDir`：`completed → relocated`，不再重置为 `pending`
- `StartupCleanup`：移除 `latest_video_at` 全量重置
- `IsVideoDownloaded`：SQL 补充排除 `cleaned` 状态
- 重启后 `MissingFiles` 自动 MarkVideoRelocated（file_path 更新为新路径）
- `StartupCleanup` 放到 `Start()` 之前调用（修复时序 bug）

---

### 三、B站防风控保守化

- Worker 3→1，RequestInterval 3→15s
- 翻页间隔 5~10s，首次全量每 3 页加 10~15s
- fullScanUP 翻页间隔与常规扫描对齐
- **风控改为手动恢复**：CooldownDuration = 365×24h（实质永久）
- 新增 `POST /api/bili/resume` + Dashboard 手动恢复按钮
- WBI keys 改为 `sync.RWMutex` + 双重检查锁，网络 IO 移出锁外

---

### 四、抖音修复

- 目录/NFO/DB 的 `uploader` 字段全部固定用 `src.Name`（消除"幽灵UP主"）
- `dscheduler/check.go` 入库 Uploader 改用 `src.Name`
- `fullScanDouyin` 补翻页间隔 5~10s（之前无间隔直接触发风控）
- Page scrape 路径修复：`DouyinVideo` 加 `URLResolved bool`，避免二次 ResolveVideoURL → 403
- CheckDouyinMix 补充 DownloadFilter/FilterRules 过滤规则
- 抖音下载限速器 `(2,1,30s)` → `(3,1,15s)`
- 新增抖音手动暂停功能：`POST /api/douyin/pause`，Dashboard 专属按钮
- 抖音 SSE 事件接入：新增转发 goroutine 将 dscheduler 事件推送至前端

---

### 五、前端全量重构

- **浅色主题**：全局移除 `dark` class，统一切换为浅色 Tailwind 类
- **Toast**：删死 DOM，加右侧滑出退出动画
- **SSE 单例**：`ensureGlobalSSE()` 模块级单例替代 5 个独立 EventSource
- **视频状态**：`downloading` 补取消按钮，过滤栏加 `cancelled`/`relocated`
- **日志页**：最新日志永远在顶部（prepend），向下滚动停止追随，浮动"↑ 跳到最新"按钮
- **订阅源分页**：后端 `GetSourcesPaged`，前端 `sourcePage`/`filterType`/`sourceTotal`
- **移动端适配**：按钮 `whitespace-nowrap`，dashboard 横幅竖排，logs 顶栏 `flex-wrap`
- **UP 主删除功能**：`DELETE /api/uploaders/:name`，带活跃任务保护

---

### 六、稳定性修复（各轮审查）

| 修复 | 详情 |
|---|---|
| `CreateSource` 未写回 ID | `LastInsertId` 后补 `s.ID = id`，影响所有依赖 `s.ID` 的调用方 |
| `GetAllDownloads` OOM | 加 `ORDER BY id DESC LIMIT 50000` |
| `DeleteUploaderData` 并发安全 | 有 downloading 任务时拒绝删除 |
| `permanent_failed` 手动重试 | `RetryByID` 先重置状态再重试，修复静默跳过 |
| 多P视频 retry 路径 | 补生成 tvshow.nfo + poster/fanart |
| `unknown` 文件名 | 视频/图集 SanitizePath 返回空时改用 `douyin_<videoID>` 兜底 |
| `quickdl_douyin.go` 字节截断 | 两处改为 `[]rune` 截断，修复中文乱码 |
| `GetStats` 性能 | 8 次单独查询 → 2 次 GROUP BY 聚合 |
| `reconcile.go` 孤儿检查 | 改为逐条 `IsVideoExists` 反向查询，不受 LIMIT 50000 影响 |
| settings.go 敏感字段保护 | PUT 时跳过 `"***"` 防覆盖已有值 |
| SSE complete 局部更新 | 补 `file_size`/`downloaded_at` 字段同步 |
| `filter` 独立包 | 解决 bscheduler 与 scheduler 循环依赖 |
| Dashboard 风控倒计时 | 超 1 天不显示天文数字（手动恢复模式） |

---

### 七、第六至十轮审查修复（今日下午）

#### 第六轮
- `dscheduler/check.go` 入库 Uploader 改用 `src.Name`
- 删除 `internal/scheduler/filter.go`（死代码，已迁至 `internal/filter`）
- `quickdl_douyin.go` `dlID=0` 时中断执行
- `CheckDouyinMix` 补过滤规则应用

#### 第七轮
- `retryOneDownload`（bscheduler）在重试前通过 `filter.MatchesRules` 校验
- `GetSourcesPaged` 补 `WHERE type=?` 条件（之前忽略 type 参数）
- notify 添加异步内存队列 + 3 次重试

#### 第八轮
- `submitDownload` 补充 `source.Enabled` 检查，禁用订阅源的 pending 任务跳过
- `GetDownloads` 补 `retry_count`/`last_error` 字段，与 `GetDownloadsByStatus` 对齐
- 删除旧废弃路由 `/api/queue/*`、`/api/progress/stream` 及对应 handler 函数（净删 ~116 行）
- 移除无用 `runtime` import

#### 第九轮
- 抖音事件转发补 `DownloadedAt` 字段（scheduler.go）
- B站 `completed` 事件补 `DownloadedAt`（downloader.go）
- 新增 `started` SSE 事件（B站 + 抖音），前端 `pending→downloading` 即时更新
- 全局搜索过滤 `enabled=0` 的订阅源
- 删除弹窗文案修正

#### 第十轮
- `IsVideoDownloaded` 排除 `cancelled` 状态，用户取消后下次同步可重新入队
- `HandleRetry` fallback 补 `onProcessPending` 调用
- bscheduler `retryOneDownload` 补 filter 过滤校验（`Field` → `Target` 修复编译错误）
- `HandleRedownload` 支持 `failed`/`permanent_failed`/`cancelled` 状态
- dscheduler 新增 `rootCtx`/`rootCancel`，文件下载改用可取消 context，Stop 时中断正在进行的下载
- 前端 `cancelled` 状态行加"恢复下载"按钮
- 批量操作 toast 改用后端返回的 `affected` 数
- `GetDownloads`/`GetDownloadsByStatus`/`GetDownloadsByUploader`/`GetAllDownloads` 统一用 `sql.NullTime`

---

## 当前 Open Items

| 优先级 | 描述 |
|---|---|
| P1 | `retryOneDownload`（顶层 scheduler/retry.go）缺 `src.Enabled` 检查（N9-S1/N10-S3） |
| P3 | 订阅源删除改 Dialog（当前用 `window.confirm`） |
| P3 | 设置页 dirty 提示 |
| P3 | 首次使用引导 |
| P3 | Dashboard 无任务时布局跳变 |

---

## 今日 Commit 统计

- **总 commits**：~35 个（含 build 修复）
- **涉及文件**：60+
- **净增/删行数**：约 -800 行（大量旧代码清理）+ +1200 行（新功能与修复）

---

*生成时间：2026-03-19 19:56 Asia/Shanghai*
