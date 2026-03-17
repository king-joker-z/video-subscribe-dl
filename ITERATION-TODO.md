# VSD 迭代记录

> 最后更新: 2026-03-18（P1 全部完成：底部Tab/自动刷新/视频列表/订阅源+仪表盘）

## 已完成

### 核心能力
- [x] 抖音 DouyinClient（GetUserVideos / GetVideoDetail / GetUserProfile / ResolveVideoURL）
- [x] 用户视频列表翻页（登录 Cookie 解决翻页限制）
- [x] 增量检查 checkDouyin + 全量补漏 fullScanDouyin
- [x] 视频下载（无水印）+ 图集/笔记下载
- [x] 快速下载（粘贴链接）
- [x] 抖音合集下载（douyin_mix 类型，GetMixVideos，cursor 分页）— 4c58342
- [x] Cookie 过期自动检测 + dashboard 降级 banner — bad5a63
- [x] 抖音下载进度追踪 SSE 集成 — 8ba22bf

### Bug 修复
- [x] sign_pool.go escapeJSString 转义修复
- [x] replaceEntry() 池耗尽 fallback
- [x] 充电专属视频标 charge_blocked 而非 failed — e1a8ced
- [x] 抖音文件名 hashtag 整体去除（#话题 不再变 _话题）— 8b8b5f4
- [x] process_douyin.go rune 截断修复（U+FFFD 乱码）— 98f7db5
- [x] 日志页自动滚动（requestAnimationFrame）— 4e5ac33

### 签名与反风控
- [x] a_bogus + X-Bogus 签名引擎，降级链，热更新，UA 池，指纹随机化
- [x] 令牌桶限流 + 连续错误计数 + 风控检测冷却

### 前端
- [x] 视频列表卡片/表格双视图 + 筛选 + 批量操作
- [x] 空状态清除筛选按钮 — 63c8fcc
- [x] 批量操作 loading 状态 — c335389
- [x] 日志页级别过滤（ALL/INFO/WARN/ERROR）+ 暂停滚动按钮 — 577bed1
- [x] 搜索支持 uploader 名称 — 7665df3

---

## 待做

### P1 — 手机端适配 + 数据展示逻辑（高优）

**问题：** 手机端布局残缺，操作按钮过小，各页面数据无定时刷新（靠事件触发，状态陈旧）。

- [x] **手机端底部 tab 导航栏**（app.js）<!-- commit: e727e77 -->
  - fixed bottom-0，5 个主页面图标 + 文字标签，56px 高
  - 当前页高亮，主内容区 `pb-16`（仅手机端）
  - 替代/补充 MobileHeader 汉堡菜单
- [x] **定时自动刷新**（videos.js / sources.js / uploaders.js）<!-- commit: 43c9c0f -->
  - videos.js：每 15s 刷新（下载中状态变化快）
  - sources.js：每 30s 刷新
  - uploaders.js：每 60s 刷新
  - 刷新不重置页码/筛选；页面不可见时暂停，切回立即刷
- [x] **视频列表手机端优化**（videos.js）<!-- commit: 8e8fd3f -->
  - 手机端强制卡片视图，隐藏"切换表格"按钮
  - 卡片操作按钮触摸区域加大
  - 批量操作工具栏手机端图标优先（文字可省）
  - 筛选栏手机端默认折叠
- [x] **订阅源/仪表盘手机端布局修复**（sources.js / dashboard.js）<!-- commit: c70890b -->
  - sources.js 手机端单列，添加按钮改为右下角 FAB
  - dashboard.js 统计数字 2x2，最近列表去掉次要信息

### P2 — UI 美化（P1 完成后）

- [x] **视频卡片升级** <!-- commit: 7c3c007 feat(ui): P2 视频卡片升级 - 平台 logo 兜底 + 状态 badge tooltip + 下载进度条优化 -->
  - 缩略图加载失败显示平台 logo（B 站/抖音 icon，SVG 内联）
  - 进度条样式优化，下载中状态更明显
  - 状态 badge 加 tooltip
- [ ] **侧边栏 LOGO 优化**（"V" 太简陋）
- [ ] **Skeleton 精细化**（模拟真实卡片形状）
- [ ] **全局下载进度浮动条**（顶部/底部，X/Y 下载中）

### P3 — 功能完善

- [ ] **视频详情页增强**
  - 显示文件大小 + 时长 + 分辨率（从 file_path 推断或存 DB）
  - 下载中状态实时进度条（接 SSE）
- [ ] **订阅源"立即检查"按钮**
  - 目前只能等定时触发，手动 trigger 一次检查很有用
- [ ] **批量下载进度汇总**
  - 顶部全局进度条（X/Y 视频下载中）
- [ ] **抖音喜欢列表下载**（API 端点已有 UserLikedAPI）

### P4 — 优化

- [ ] a_bogus 签名升级（对齐 f2 满血版）
- [ ] 代理池支持
- [ ] TikTok 国际版支持

