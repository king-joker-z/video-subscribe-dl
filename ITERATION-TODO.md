# 迭代任务清单

## 第一优先级（用户体验直接相关）

### 1. ✅ Cookie 验证机制 [P0] — 迭代 #1
- 上传 cookie 后立即验证有效性（调 bilibili API 检查登录状态）
- API: /api/cookie/verify — 返回登录用户名、VIP状态、过期时间
- 前端：上传后显示验证结果（绿色=有效/红色=无效/黄色=即将过期）
- 定时检查：scheduler 每次同步前验证 cookie，失效时 UI 提示

### 2. ✅ 下载进度展示 [P1] — 迭代 #4
- SSE 推送下载进度（当前文件、百分比、速度）
- 前端下载队列显示实时进度条

### 3. ✅ 日志查看功能 [P1] — 迭代 #6
- Ring buffer 日志 + SSE 流式推送
- 前端日志面板，支持自动滚动和级别筛选

### 4. ✅ 封面图下载 [P1] — 迭代 #5
- 下载视频时同时保存缩略图（bilibili API 返回的 pic 字段）
- 通过 /api/covers/ 本地服务提供封面图

### 5. ✅ People 目录生成 [P1] — 迭代 #5
- 为每个 UP 主生成 person.nfo + folder.jpg
- downloads/metadata/people/{UP主名}/

## 第二优先级（健壮性）

### 6. ✅ 错误重试机制完善 [P2] — 迭代 #9
- 下载失败自动重试（指数退避，最多3次）
- 失败原因分类：permanent_failed vs 可重试
- 前端显示重试次数和失败原因

### 7. ✅ 数据库一致性 [P2] — 迭代 #7
- 启动时扫描本地文件与数据库对账
- 自动重置 stale downloading 状态
- UI 提供手动修复按钮

### 8. ✅ 弹幕下载 [P2] — 迭代 #8
- bilibili 弹幕 API 获取弹幕
- XML → ASS 字幕转换
- 与视频文件同目录保存

## 第三优先级（体验优化）

### 9. ✅ 前端 UI 打磨 [P3] — 迭代 #12 完成
- [x] 暗色主题完善
- [x] 错误状态和重试信息展示
- [x] 移动端响应式适配（底部Tab导航 + ActionSheet + 安全区适配）
- [x] PWA 支持（manifest.json + apple-mobile-web-app meta）
- [x] 操作确认弹窗优化

### 10. 多平台支持扩展 [P3] — 未开始
- [ ] 抖音支持
- [ ] YouTube 支持（yt-dlp fallback）

## 全局收尾 — 迭代 #10
- [x] 全局编译检查（go build + go vet 通过）
- [x] 文档更新（BUGS.md, REFACTOR-PLAN.md, README.md）
- [x] 版本号更新 → v2.0.0

## 全局收尾 — 迭代 #11 (v2.1.0)
- [x] 分块并行下载实现（大文件 >50MB 自动拆分多线程，默认4线程，可配置 download_chunks）
- [x] go vet ./... 全量检查通过
- [x] 代码清理（无未使用 import）
- [x] 文档更新（README.md 功能列表加入新功能）
- [x] 版本号更新 → v2.1.0

## 迭代 #12 (v2.2.0) — 移动端适配 + PWA
- [x] 移动端底部 Tab 导航（6个标签，56px固定底栏）
- [x] 源卡片移动端 ActionSheet（点击卡片弹出操作面板）
- [x] 移动端操作栏（同步/暂停按钮移到顶部action bar）
- [x] PWA manifest.json + apple-mobile-web-app meta 标签
- [x] 安全区适配（env(safe-area-inset-bottom)）
- [x] 响应式优化：筛选栏横向滚动、表单列堆叠、统计网格自适应
- [x] 超小屏(480px)额外适配

## 迭代 #13 (v2.3.0) — 多通道通知（Telegram + Bark）
- [x] 通知系统重构：支持 Webhook / Telegram / Bark 三种通道
- [x] Telegram Bot API 直接推送（sendMessage + MarkdownV2 格式化）
- [x] Bark (iOS) 推送通知支持（含自建服务器选项）
- [x] 新增同步完成通知事件（EventSyncComplete）
- [x] 前端设置页通道切换 UI（根据选择显示对应配置字段）
- [x] 敏感字段安全处理（token/key 不返回明文，空值不覆盖已有配置）
- [x] Bark 按事件类型设置推送等级和声音（紧急/普通）
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.3.0

## 迭代 #14 (v2.4.0) — 存储管理 + 健康检查增强
- [x] 自动清理策略：支持 retention_days 配置，过期视频自动清理
- [x] 磁盘压力清理：auto_cleanup_on_low_disk + min_disk_free_gb 配置
- [x] 清理时保留数据库记录（status=cleaned），不影响去重逻辑
- [x] 关联文件清理（NFO、封面、弹幕同步删除）
- [x] 清理统计 API：/api/cleanup/stats + /api/cleanup/config
- [x] /health 端点增强：返回版本号、运行时间、活跃下载数、队列状态、磁盘信息
- [x] /api/version 端点：版本和构建信息
- [x] settings API 扩展：新增 retention_days、auto_cleanup_on_low_disk、min_disk_free_gb
- [x] go build + go vet + go test 全量通过
- [x] 版本号更新 → v2.4.0

---

## 下一阶段建议

1. **多平台支持** — YouTube/抖音，利用 yt-dlp 作为 fallback
2. ~~**多 P 视频支持**~~ — ✅ 迭代 #15 完善（tvshow.nfo + episodedetails NFO + 封面）
3. ~~**通知机制增强**~~ — ✅ 迭代 #13 完成（Telegram/Bark/Webhook 三通道）
4. ~~**存储管理**~~ — ✅ 迭代 #14 完成（retention_days + 磁盘压力清理 + /health 增强）
5. ~~**Docker 镜像发布**~~ — ✅ 迭代 #10 已完成（GitHub Actions + DockerHub）
6. **Service Worker** — 离线缓存、后台同步

## 迭代 #15 (v2.5.0) — 多P视频 NFO 元数据完善
- [x] 多P视频父目录生成 tvshow.nfo（含标题、简介、UP主信息、标签、首播日期）
- [x] 多P视频父目录下载 poster.jpg + fanart.jpg 封面
- [x] 每个分P生成 episodedetails NFO（含 season/episode 编号、分P标题、发布日期）
- [x] 新增 nfo.EpisodeMeta 结构和 episodedetailsNFO XML 格式
- [x] 新增 nfo.GenerateEpisodeNFO / GenerateEpisodeNFOFromPath 函数
- [x] 重试逻辑同步支持多P episodedetails NFO 生成（retry.go）
- [x] handleDownloadResult 区分单P（movie NFO）和多P（episodedetails NFO）
- [x] go build + go vet + go test 全量通过

## 迭代 #16 (v2.6.0) — 单视频快速下载
- [x] 新增 ExtractBVID 函数：支持解析 BV 号、AV 号、完整 URL、b23.tv 短链接
- [x] 新增 AV2BV API 转换函数（通过 bilibili API 将 AV 号转为 BV 号）
- [x] 新增 ResolveShortURL 函数（解析 b23.tv 短链接重定向）
- [x] 新增 POST /api/download 端点：一键下载单个视频（自动处理单P/多P）
- [x] 新增 POST /api/download/preview 端点：预览视频信息（不下载）
- [x] 前端快速下载 FAB 按钮（右下角悬浮按钮）
- [x] 前端快速下载弹窗：URL 输入 → 视频预览（封面+标题+UP主+播放量）→ 一键下载
- [x] 支持 Ctrl+D 快捷键打开快速下载弹窗
- [x] 已下载视频重复检测（避免重复下载）
- [x] 充电专属/番剧/不可用视频前置校验
- [x] source_id=0 标识快速下载记录，与订阅源记录区分
- [x] 7 个单元测试覆盖 ExtractBVID 各种输入场景
- [x] go build + go vet + go test 全量通过

## 迭代 #17 (v2.6.1) — 首次扫描风控优化
- [x] checkUP 首次扫描改为"懒加载"模式：只创建 pending 记录，不调 GetVideoDetail
- [x] checkUPDynamic 首次扫描同步改为懒加载模式
- [x] 增量扫描在 processOneVideo 调用间加 1-2s 随机延迟
- [x] 翻页间隔从 500-1000ms 提高到 1500-2500ms（与 fullScanUP 一致）
- [x] 首次扫描完成后自动触发 ProcessAllPending（下载时按需获取详情）
- [x] 参考 bili-sync 的两阶段策略：扫描阶段只拉列表，处理阶段再补详情
- [x] go build + go vet 全量通过

## 迭代 #18 (v2.7.0) — 视频详情弹窗 + 卡片封面图
- [x] 新增 VideoDetailModal 组件：点击视频行/卡片弹出详情弹窗
- [x] 详情弹窗展示：封面图（通过 /api/thumb/:id）、标题、UP主、状态、时长、文件大小
- [x] 详情弹窗展示：视频简介、创建时间、下载时间、重试次数、文件路径
- [x] 详情弹窗展示：错误信息（红色高亮区）、B站直链（外部链接）
- [x] 详情弹窗操作按钮：开始下载/重试/重新下载/删除文件/恢复/删除（按状态动态显示）
- [x] 卡片视图重构：VideoCard 组件带封面图、时长标签、下载进度条
- [x] 表格视图：行点击打开详情（智能排除 checkbox/button 点击）
- [x] ESC 快捷键关闭详情弹窗
- [x] 响应式设计：弹窗适配移动端（max-h-[80vh] 可滚动）
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.7.0

## 迭代 #19 (v2.8.0) — 在线视频播放
- [x] 新增 StreamHandler：GET /api/stream/:id 端点，流式播放视频文件
- [x] 支持 HTTP Range 请求（通过 http.ServeContent），支持浏览器拖拽进度条
- [x] 自动检测视频 MIME 类型（mp4/mkv/webm/flv/avi/mov/ts）
- [x] VideoDetailModal 新增内联播放器：点击封面图或"播放"按钮即可在弹窗内播放
- [x] 封面图 hover 效果：鼠标悬停显示半透明播放图标覆盖层
- [x] 播放器支持：自动播放、原生控件、关闭按钮、播放出错自动回退
- [x] 底部操作栏新增"播放"按钮（仅对已完成且有文件的视频显示）
- [x] MKV 格式兼容性提示（非原生格式提醒用户浏览器兼容性）
- [x] 中间件优化：/api/stream/ 路径跳过请求日志记录
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.8.0

## 迭代 #20 (v2.8.1) — SSE 下载事件实时通知
- [x] 新增 DownloadEvent 结构体：描述下载完成/失败事件（type/bvid/title/file_size/error）
- [x] Downloader 新增事件订阅机制：SubscribeEvents / UnsubscribeEvents / emitEvent
- [x] 下载完成/失败时自动广播事件给所有 SSE 订阅者
- [x] SSE 端点新增 download_event 事件类型推送
- [x] 前端全局 SSE 监听：下载完成弹 toast（✅ + 标题 + 文件大小）
- [x] 前端全局 SSE 监听：下载失败弹 toast（❌ + 标题 + 错误原因）
- [x] 自定义 DOM 事件 vsd:download-event，触发页面自动刷新
- [x] 视频列表页监听事件自动刷新列表数据
- [x] 仪表盘页监听事件自动刷新统计数据
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.8.1

## 迭代 #21 (v2.9.0) — 订阅源视频筛选 + 排序增强
- [x] 订阅源卡片新增"查看视频"导航链接：点击跳转到视频列表并自动按 source_id 过滤
- [x] 视频列表页支持 source_id / source_name URL 参数，自动筛选指定订阅源的视频
- [x] 视频列表页新增"订阅源"筛选标签（青色 chip），支持一键清除
- [x] 后端排序白名单新增 downloaded（downloaded_at）字段
- [x] 前端排序下拉新增"最近下载"选项，按实际下载完成时间排序
- [x] source_id 与 uploader 筛选可组合使用
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.9.0
