# VSD 迭代记录

> 最后更新: 2026-03-17（第二轮迭代完成，转入前端优化/移动适配方向）

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

### P1 — 前端移动端适配（高优）

**问题：** 移动端（手机/平板）页面目前布局残缺，视频列表表格横向溢出，订阅源卡片堆叠混乱，操作按钮过小。

- [ ] **移动端底部导航栏**（手机端 `lg:hidden` 的底部 tab bar，替代侧边栏）
  - 5 个主要页面图标 + 标签，固定在底部
  - 当前 MobileHeader 只有汉堡菜单，手势不友好
- [ ] **视频列表移动端优化**
  - 手机端强制卡片视图（隐藏表格视图切换按钮）
  - 卡片操作按钮在手机端改为底部弹出菜单（ActionSheet 风格）
  - 批量操作工具栏移动端适配（按钮太小）
- [ ] **订阅源页面移动端**
  - 手机端单列卡片（当前 `md:grid-cols-2 xl:grid-cols-3` 在小屏挤）
  - 添加订阅源表单滚动时不被遮挡
- [ ] **仪表盘统计卡片移动端**
  - `grid-cols-2 lg:grid-cols-3` 在手机上两列数字太小，改为竖排或 2x2

### P2 — 前端 UI 美化

- [ ] **视频卡片视觉升级**
  - 缩略图加载失败时显示平台 icon（B 站/抖音 logo）而非空白
  - 进度条样式优化（当前太细，下载中状态不够明显）
  - 状态 badge 加 tooltip（hover 时显示详细状态文本）
- [ ] **仪表盘最近下载列表**
  - 每行加缩略图（16:9 小图，30px 高）
  - 区分平台（B 站蓝/抖音红小圆点）
- [ ] **侧边栏 LOGO 优化**
  - 当前只是 "V" 字母蓝色方块，太简陋
  - 换成更有设计感的图标/文字组合
- [ ] **空状态页面统一美化**
  - 当前用 Lucide 图标 + 文字，可以加 SVG 插图（内联 SVG，不增加依赖）
- [ ] **全局加载动效**
  - 页面切换时的 skeleton 加载更精细（目前 skeleton 是矩形块，改为模拟真实卡片形状）

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

