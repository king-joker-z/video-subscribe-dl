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
2. **多 P 视频支持** — B站分P视频完整下载
3. ~~**通知机制增强**~~ — ✅ 迭代 #13 完成（Telegram/Bark/Webhook 三通道）
4. ~~**存储管理**~~ — ✅ 迭代 #14 完成（retention_days + 磁盘压力清理 + /health 增强）
5. ~~**Docker 镜像发布**~~ — ✅ 迭代 #10 已完成（GitHub Actions + DockerHub）
6. **Service Worker** — 离线缓存、后台同步
