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

## 迭代 #22 (v2.10.0) — 设置页 UX 重构
- [x] Boolean 字段改为 Toggle 开关组件（下载弹幕、充电视频尝试、低磁盘自动清理、通知事件开关）
- [x] 枚举字段改为 Select 下拉选择（下载画质、视频编码、NFO 格式、NFO 日期类型、通知类型）
- [x] 数字字段使用 type=number 输入框（并发数、分片数、速度限制、间隔等）
- [x] 通知设置条件显示：根据通知类型只展示对应通道配置字段
- [x] 通知事件开关：选择通知类型后显示可配置的事件开关（完成/失败/Cookie过期/同步）
- [x] 文件名模板实时预览：输入模板后自动调用后端 API 预览效果
- [x] 设置分组重组：下载设置 / 调度与高级 / 性能与限流 / 存储管理 / 通知设置
- [x] 浮动保存栏：底部 sticky 显示未保存更改数量 + 保存/放弃按钮
- [x] 顶部未保存提示：显示待保存项数量
- [x] 新增 api.previewTemplate() 前端 API 方法
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.10.0

## 迭代 #23 (v2.11.0) — 全局命令面板 (Command Palette)
- [x] 新增 GET /api/search?q=xxx 全局搜索 API：跨视频、UP主、订阅源搜索
- [x] 视频搜索：按标题、UP主名、BV号匹配，返回最近 8 条
- [x] UP 主搜索：按名称匹配，聚合视频数，返回最多 5 条
- [x] 订阅源搜索：按名称、URL 匹配，返回最多 5 条
- [x] 前端 CommandPalette 组件：Spotlight 风格搜索弹窗
- [x] 全局快捷键 Ctrl+K / ⌘K 唤起命令面板
- [x] 页面快速导航：输入页面名直接跳转（仪表盘/订阅源/视频列表/UP主/设置/日志）
- [x] 快捷操作：快速下载（Ctrl+D）、触发同步
- [x] 搜索结果按类型分组显示（页面 > 操作 > 订阅源 > UP主 > 视频）
- [x] 键盘导航：↑↓ 选择、Enter 确认、Esc 关闭
- [x] 250ms 防抖搜索，加载状态指示
- [x] 侧边栏新增搜索按钮（显示 ⌘K 快捷键提示）
- [x] 视频结果显示状态标签（已完成/下载中/待处理/失败/充电）
- [x] 搜索结果直接路由跳转，UP主→筛选视频，订阅源→筛选视频
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.11.0

## 迭代 #24 (v2.12.0) — 订阅源导出/导入（备份恢复）
- [x] 新增 GET /api/sources/export 端点：导出所有订阅源为 JSON 文件（自动生成带时间戳的文件名）
- [x] 新增 POST /api/sources/import 端点：从 JSON 文件导入订阅源，支持 URL 去重检测
- [x] ExportPayload 结构体：含版本号、导出时间、订阅源数量、订阅源列表
- [x] 导入时自动跳过已存在的 URL（避免重复创建）
- [x] 导入结果详细反馈：新增数量、跳过数量、失败数量及逐条明细
- [x] 前端订阅源页面工具栏新增"导出"和"导入"按钮
- [x] 导出：一键下载 JSON 文件（通过浏览器 blob 下载）
- [x] 导入：文件选择器 → JSON 解析 → 调用 API → 结果弹窗展示
- [x] 导入结果弹窗：新增/跳过/失败统计 + 逐条详情列表（颜色区分）
- [x] api.js 新增 exportSources / importSources 方法
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.12.0

## 迭代 #25 (v2.13.0) — 仪表盘凭证状态卡片
- [x] Dashboard API 新增 credential 字段：返回 B 站登录状态、用户名、VIP 信息、最高画质
- [x] 凭证状态 5 分钟 TTL 缓存：避免每次 dashboard 轮询都调 B 站 API，降低风控风险
- [x] 登录/刷新凭证时自动清除缓存（InvalidateCredentialCache）
- [x] Router.SetCallbacks 包装 onCredentialUpdate：更新凭证时联动清除仪表盘缓存
- [x] 仪表盘新增 B 站账号状态卡片：显示用户名、会员等级、最高画质、凭证状态
- [x] 四种状态展示：正常(绿色) / 已过期(黄色) / 验证失败(红色) / 未登录(灰色)
- [x] 凭证异常时顶部横幅警告：提醒用户刷新或重新登录
- [x] 一键刷新凭证按钮：直接从仪表盘刷新，无需跳转设置页
- [x] 前往登录/设置跳转按钮：通过 onNavigate 路由到设置页
- [x] DashboardPage 接收 onNavigate prop，app.js 传递 navigate 回调
- [x] 仪表盘布局调整：任务状态 + 账号状态 + 存储空间三栏并列（lg:grid-cols-3）
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.13.0

## 迭代 #26 (v2.14.0) — 订阅源同步状态展示 + filter_rules 保存修复
- [x] 修复 filter_rules 字段保存 bug：HandleUpdate 缺少 filter_rules 字段解析，编辑高级过滤规则后无法保存
- [x] 订阅源卡片新增同步状态信息栏：显示"上次检查"相对时间和"下次检查"倒计时
- [x] 新增 formatTimeAgo 工具函数：智能展示相对时间（刚刚/X分钟前/X小时前/X天前）
- [x] 新增 formatNextCheck 工具函数：根据 last_check + check_interval 计算下次检查倒计时
- [x] hover title 提示完整日期时间
- [x] 从未检查的订阅源显示"从未检查"黄色提示
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.14.0

## 迭代 #27 (v2.15.0) — 通知测试功能
- [x] 新增 POST /api/notify/test 端点：发送测试通知到已配置的通道（Telegram/Bark/Webhook）
- [x] 新增 GET /api/notify/status 端点：返回通知配置状态（是否已配置、通道类型）
- [x] 新增 web/api/notify.go：NotifyHandler 封装通知测试和状态查询逻辑
- [x] Router 新增 notify 字段和 SetNotifier 方法：通过 notify.Notifier 实例初始化 handler
- [x] server.go setupRoutes 中自动注入 notifier 到 API router
- [x] 发送前检查配置：未配置通道时返回友好错误提示
- [x] 发送失败时返回具体错误信息（网络/配置/服务端错误）
- [x] 前端设置页通知区域新增"发送测试通知"按钮
- [x] 有未保存更改时禁用测试按钮（避免测试旧配置造成困惑）
- [x] api.js 新增 testNotification / getNotifyStatus 方法
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.15.0

## 迭代 #28 (v2.16.0) — 下载速度/ETA 实时展示 + 进度匹配修复
- [x] 新增 formatSpeed 工具函数：将 bytes/sec 格式化为人类可读速度（如 "1.5 MB/s"）
- [x] 新增 formatETA 工具函数：根据已下载/总量/速度计算剩余时间（如 "3分12秒"）
- [x] 修复前端进度匹配 bug：原 getProgress 使用不存在的 p.id 字段，导致视频列表进度条从不显示
- [x] 后端 ProgressInfo 新增 DownloadID 字段（JSON: download_id）：关联数据库下载记录 ID
- [x] 后端 Job 新增 DownloadID 字段：在所有 5 处 Job 创建点传入数据库 ID
- [x] 前端 getProgress 改为 download_id + bvid 双重匹配：确保进度条可靠匹配
- [x] 视频列表表格视图：进度条下方显示速度 + ETA + 已下载/总量
- [x] 视频列表卡片视图：封面底部叠加速度/百分比/ETA 信息条
- [x] 仪表盘新增"下载中"实时进度卡片：展示所有活跃下载项，含标题、进度条、速度、ETA、阶段标签
- [x] 仪表盘显示总下载速度汇总
- [x] 合并阶段特殊显示"合并中..."（amber 色）
- [x] go build + go vet 全量通过
- [x] 版本号更新 → v2.16.0
