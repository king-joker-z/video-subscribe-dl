# 抖音下载支持 — 设计文档

## 目标

在现有 VSD 项目中新增抖音平台支持，实现：
1. 订阅抖音用户，定期检查并下载新视频
2. 粘贴/拖拽抖音链接快速下载单个视频
3. 与 B站现有功能共享 UI、数据库、通知等基础设施

## 技术方案

### 1. 新增 `internal/douyin/` 包

参考开源实现（parse-video、lux）:

**核心 API**:
- `GetVideoDetail(videoID string)` — 通过 `iesdouyin.com/share/video/{id}` 页面解析视频信息
  - 从 `window._ROUTER_DATA` 提取 JSON 数据
  - 获取标题、封面、无水印视频 URL、作者信息
  - 视频 URL: `play_addr.url_list[0]`，将 `playwm` 替换为 `play` 得到无水印地址
  - 跟随 302 重定向获取真实下载地址
  
- `GetUserVideos(secUID string, maxCursor int64)` — 获取用户视频列表
  - API: `https://www.douyin.com/aweme/v1/web/aweme/post/`
  - 参数: `sec_user_id`, `max_cursor`, `count`
  - 需要 X-Bogus 签名（JS Runtime 执行 sign.js）
  - 返回 `aweme_list` + `has_more` + `max_cursor`

- `ResolveShareURL(shareURL string)` — 解析分享链接
  - `v.douyin.com/xxx` 短链接 → 跟随重定向 → 提取 video ID
  - `www.douyin.com/video/xxx` 直接提取 ID
  - `www.douyin.com/user/xxx` 用户主页 → 提取 sec_user_id

**Cookie/签名**:
- 动态生成 Cookie: `msToken`(随机) + `ttwid`(通过 bytedance API 获取)
- X-Bogus 签名: 内嵌 sign.js，用 goja (Go JS Runtime) 执行
- 无需用户登录即可下载公开视频（不同于 B站需要 Cookie）

**限流**:
- 令牌桶限流器（复用现有 RateLimiter）
- 默认: 每 3 秒 1 个请求，桶容量 1
- 抖音风控比 B站更严格，翻页间隔 5-8 秒

### 2. Source 类型扩展

`Source.Type = "douyin"`:
- `URL` 格式: `https://www.douyin.com/user/SEC_USER_ID` 或抖音号
- `Name`: 用户昵称

数据库:
- `sources.type` 允许 `"douyin"` 值（无需 migration，type 是 text 字段）
- `downloads.video_id` 对抖音使用 `aweme_id`

### 3. Scheduler 扩展

`check_douyin.go`:
- 解析 sec_user_id
- 获取用户视频列表（增量：基于 latestVideoAt 时间戳对比）
- 过滤已下载 + 应用 filter_rules
- 创建 pending 下载记录

在 `checkSource()` switch 中添加 `case "douyin":`

### 4. 下载器适配

抖音视频下载比 B站简单:
- 直接 HTTP GET 无水印视频 URL（不需要 DASH/ffmpeg）
- 封面图直接下载
- 复用现有 downloader 的 Job/Progress 框架

### 5. 前端适配

- 添加源表单: 新增 "抖音" 平台选项
- URL 输入: 支持抖音分享链接、用户主页链接
- 快速下载: `extractBiliUrl` 扩展为识别抖音链接

### 6. 实现分期

**Phase 1 — 核心能力** (本次):
- [ ] `internal/douyin/client.go` — API Client（视频详情、用户列表、分享链接解析）
- [ ] `internal/douyin/types.go` — 数据结构
- [ ] `internal/douyin/cookie.go` — Cookie/签名生成
- [ ] `internal/douyin/sign.js` — X-Bogus 签名脚本（从 lux 项目参考）
- [ ] `internal/scheduler/check_douyin.go` — 调度检查
- [ ] 下载器支持抖音视频下载
- [ ] `checkSource()` switch 扩展
- [ ] 前端: 添加源支持抖音类型

**Phase 2 — 增强** (后续):
- [ ] 快速下载支持抖音链接
- [ ] 抖音图集下载
- [ ] 抖音合集/话题支持

## 依赖

- `github.com/dop251/goja` — Go JavaScript Runtime (执行 X-Bogus 签名)
- `github.com/tidwall/gjson` — JSON 解析（项目已有）

## 参考

- https://github.com/wujunwei928/parse-video/blob/master/parser/douyin.go
- https://github.com/iawia002/lux/blob/master/extractors/douyin/douyin.go
