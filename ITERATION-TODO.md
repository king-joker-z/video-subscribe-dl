# VSD 迭代记录

> 最后更新: 2026-03-16（含 emoji过滤 + 删除订阅清文件）

## 已完成

### 核心能力
- [x] 抖音 DouyinClient（GetUserVideos / GetVideoDetail / GetUserProfile / ResolveVideoURL）
- [x] 用户视频列表翻页（登录 Cookie 解决翻页限制）
- [x] 增量检查 checkDouyin + 全量补漏 fullScanDouyin
- [x] 视频下载（无水印）+ 图集/笔记下载
- [x] 快速下载（粘贴链接）

### 签名与反风控
- [x] a_bogus 签名引擎（goja JS Runtime，池化复用）— 0a9340d
- [x] X-Bogus 签名引擎（降级链: a_bogus → X-Bogus → 无签名）
- [x] 签名 JS 热更新（SignUpdater，ETag 缓存）— ddd9b0f / b953349
- [x] UA 池 + sec-ch-ua Client Hints — 5789f6b
- [x] 浏览器指纹随机生成 — b5163db
- [x] 令牌桶限流器（AcquireWithBackoff + ReportResult）— f9c01ee
- [x] Cookie 会话一致性（同一会话内保持相同指纹）— 104304e
- [x] Referer/Origin 头 — 9754417
- [x] msToken 翻页一致性（URL 和 Cookie 共用 sessionMsToken）— 491430e

### Cookie 管理
- [x] 用户 Cookie 配置 Web UI（设置页 textarea + 验证 + 保存 + 清除）— 282bfc9 / 50083ce
- [x] Cookie 验证 API（POST /api/douyin/cookie/validate）— 282bfc9
- [x] Cookie 热更新（保存后即时生效，无需重启）— 282bfc9
- [x] Cookie 非法字符清洗（换行符/制表符/多余空格）— 19e64c6 / 41407d5
- [x] Scheduler 启动自动加载用户 Cookie — 282bfc9

### NFO 与文件管理
- [x] 平台区分 NFO（actor role: B 站→UP主，抖音→作者；uniqueid type 按平台）— 5c3fe70
- [x] 抖音视频独立子目录（{title} [{awemeID}]/）— 5c3fe70
- [x] SanitizePath 对齐 B 站（Unicode 不可见字符 + 80 字符截断）— 35af5aa
- [x] 启动时版本化清理幽灵目录（cleanup v2）— adfc186
- [x] SanitizePath 过滤 emoji（U+1F000-U+1FFFF，NAS 兼容性）— 4e42765
- [x] 删除订阅时可选清除本地文件 + DB 记录（前端两步确认）— 16dbbb6

### 基础设施
- [x] RateLimiter 生命周期管理 + DouyinClient.Close() — 8f812d1 / d33b77e
- [x] Prometheus metrics 端点 — 16d7ec7
- [x] pprof 性能分析端点 — b79888a
- [x] CI test + coverage — cd3f863
- [x] 诊断日志（翻页响应 statusCode/bodyLen）— 5f67145
- [x] API 端点集中管理 endpoints.go — fd85448
- [x] quickdl.go 拆分（B 站 / 抖音独立文件）— d5c1fe8
- [x] 连续错误计数 + 自动降级 — ff0b0f8
- [x] 风控检测（filter_list + status_code 诊断）— 2bc01f8

## 待做

### P0 — Bug 修复
- [ ] sign_pool.go:103 缺少 escapeJSString 转义（与 abogus_pool.go 不一致，一行修复）
- [ ] replaceEntry() 全部失败时池耗尽导致永久阻塞（加 fallback）

### P1 — 代码质量
- [ ] 单元测试覆盖提升（当前 32.6%，目标 >50%）— checkDouyin/fullScanDouyin mock 测试
- [ ] downloadDouyinFile 去重（scheduler 和 web/api 中有完全重复实现）
- [ ] HTTP Client 复用（文件下载每次新建 Client，应使用连接池）

### P2 — 功能增强
- [ ] 抖音合集下载（/aweme/v1/web/mix/aweme/，API 端点已定义）
- [ ] 抖音喜欢列表下载（API 端点已定义）
- [ ] Cookie 过期自动检测 + 降级通知（当前过期后静默失败）
- [ ] 抖音下载进度追踪（与前端 SSE 集成，参考 B 站实现）

### P3 — 新能力
- [ ] TikTok 国际版支持（参考 yt-dlp Mobile API 方案，不需要 Web 签名）
- [ ] 抖音直播录制（ffmpeg 已在 Docker 中）
- [ ] 抖音收藏夹下载

### P4 — 优化
- [ ] a_bogus 签名升级（对齐 f2 满血版）
- [ ] 代理池支持（高频使用场景下绕 IP 限制）
