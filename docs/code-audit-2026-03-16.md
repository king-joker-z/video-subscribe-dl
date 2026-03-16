# video-subscribe-dl 代码审计报告

**日期:** 2026-03-16  
**范围:** 抖音 (Douyin) 相关代码  
**审计员:** 蟹Bro（自动化代码审计）  
**Go 版本:** go1.25.8 linux/amd64

---

## 1. 今日 Commit 统计

共 **29 个 commit**（2026-03-16 全天）

| # | Hash | Message | 改动文件数 | +/- |
|---|------|---------|-----------|-----|
| 1 | adfc186 | fix(cleanup): upgrade to versioned cleanup to re-clean Douyin ghost directories | 1 | +10/-10 |
| 2 | 35af5aa | fix(douyin): align SanitizePath with bilibili implementation (Unicode sanitization + length limit) | 2 | +96/-12 |
| 3 | 41407d5 | fix(douyin): sanitize user cookie to remove invalid HTTP header characters | 1 | +72/-0 |
| 4 | 19e64c6 | fix(douyin): sanitize user cookie to remove invalid HTTP header characters | 2 | +19/-1 |
| 5 | 50083ce | feat(douyin): add user cookie support for authenticated pagination | 2 | +55/-67 |
| 6 | 282bfc9 | feat(douyin): add user cookie management for Douyin | 10 | +308/-9 |
| 7 | 491430e | fix(douyin): reuse sessionMsToken in buildBaseParams for pagination consistency | 4 | +59/-5 |
| 8 | 9754417 | fix(douyin): add Referer and Origin headers to fix pagination | 1 | +1/-0 |
| 9 | 46f95e2 | fix(douyin): implement dedicated fullScanDouyin for full-scan mode | 2 | +174/-2 |
| 10 | 5f67145 | fix(douyin): add diagnostic logging for pagination response | 1 | +17/-0 |
| 11 | 0b024ac | fix(douyin): stop caching full cookie and decouple URL/Cookie msToken | 3 | +30/-20 |
| 12 | 5c3fe70 | feat: distinguish platform in NFO generation and add video subdirectory for Douyin | 5 | +38/-8 |
| 13 | 9471697 | fix(douyin): use session-level cookie cache to fix pagination empty list | 5 | +67/-57 |
| 14 | d0f1a1d | Revert "ci: fix build failure - skip live tests in CI, use Node.js 24 for actions" | 1 | +1/-5 |
| 15 | 939d47a | Revert "fix(douyin): 会话级 Cookie 缓存修复翻页空列表问题" | 1 | +3/-13 |
| 16 | 1a98d62 | fix(douyin): 会话级 Cookie 缓存修复翻页空列表问题 | 1 | +13/-3 |
| 17 | 78d0ae0 | ci: fix build failure - skip live tests in CI, use Node.js 24 for actions | 1 | +5/-1 |
| 18 | 7501f39 | fix(douyin): 修复 GetUserVideos 返回空列表问题 | 3 | +186/-35 |
| 19 | b99ef21 | docs: update PLAN-v4-fixes.md - all 5 tasks completed | 1 | +37/-30 |
| 20 | 9ea8981 | test: integration HTTP mock tests for bili/douyin API parsing (P4) | 1 | +257/-0 |
| 21 | cd3f863 | ci: add test + coverage step before build (P3) | 1 | +7/-0 |
| 22 | 16d7ec7 | feat: Prometheus text format metrics endpoint (P2) | 3 | +93/-0 |
| 23 | b953349 | feat: SignUpdater auto-update with configurable interval (P1) | 3 | +83/-0 |
| 24 | d33b77e | fix: RateLimiter.Stop() idempotent via sync.Once (P0) | 5 | +70/-4 |
| 25 | d5435cd | test: 集成测试框架 — 10 个端到端测试 | 1 | +443/-0 |
| 26 | ddd9b0f | feat: 签名算法热更新机制 | 2 | +255/-0 |
| 27 | b79888a | feat: pprof / metrics 端点 | 5 | +161/-2 |
| 28 | 8f812d1 | fix: RateLimiter 生命周期管理 — DouyinClient.Close() + defer | 6 | +15/-0 |
| 29 | 0a9340d | feat(douyin): a_bogus 签名支持 + 三级降级链 | 4 | +888/-23 |

---

## 2. 代码结构盘点

### 2.1 `internal/douyin/` 文件列表（共 3785 行）

| 文件 | 行数 | 职责 |
|------|------|------|
| `client.go` | 1199 | DouyinClient 核心：NewClient、GetUserVideos、GetVideoDetail、GetUserProfile、ResolveVideoURL、URL 解析、SanitizePath 等所有主逻辑 |
| `abogus_pool.go` | 187 | a_bogus 签名 VM 池（goja 运行 JS，池化复用，含 JS 注入转义） |
| `abogus_test.go` | 205 | a_bogus 签名的单元测试 |
| `cookie.go` | 257 | 全局 Cookie 管理器：自动生成伪造 Cookie / 使用用户 Cookie，msToken 维护 |
| `cookie_test.go` | 247 | Cookie 相关单元测试（sanitize、格式验证等） |
| `cookie_validator.go` | 41 | ValidateCookie：字段检查 + 实际 API 探测验证 Cookie 可用性 |
| `diag.go` | 15 | 诊断工具：GetCookieString、TestXBogusSign |
| `endpoints.go` | 78 | API 常量：所有抖音 API URL 及参数构建辅助函数 |
| `fingerprint.go` | 155 | 浏览器指纹随机生成（UserAgent、屏幕分辨率、语言等） |
| `fingerprint_test.go` | 142 | 指纹生成的单元测试 |
| `logger.go` | 7 | 包级 slog.Logger（module=douyin） |
| `ratelimit.go` | 127 | 令牌桶限流器（DefaultRateLimiter 3s/req、AcquireWithBackoff、ReportResult 状态码感知） |
| `ratelimit_test.go` | 136 | 限流器单元测试 |
| `request_params.go` | 107 | buildBaseParams：构建完整浏览器指纹参数（device_platform、aid、msToken 等）；setFullHeaders：设置 Sec-Fetch 安全头 |
| `sign_pool.go` | 145 | X-Bogus 签名 VM 池（goja，池化，用于 GetUserVideos 降级链） |
| `sign_updater.go` | 274 | 签名 JS 热更新：从远端 URL 拉取新版 sign.js/a_bogus.js，ETag 缓存，定时自动检查 |
| `sign_updater_test.go` | 31 | sign_updater 单元测试 |
| `stats.go` | 36 | GetSignPoolStats / GetABogusPoolStats：暴露签名池统计给 metrics 端点 |
| `types.go` | 58 | 数据类型定义：DouyinVideo、DouyinUser、DouyinUserProfile、UserVideosResult、ResolveResult 等 |

### 2.2 `internal/scheduler/` 抖音相关文件

| 文件 | 内容 |
|------|------|
| `check_douyin.go` | `checkDouyin`（增量扫描）、`fullScanDouyin`（全量补漏）、`resolveDouyinSecUID`、`getDouyinSetting` |
| `process_douyin.go` | `retryOneDouyinDownload`（视频下载主流程）、`downloadDouyinFile`、`downloadDouyinThumb`、`downloadDouyinNote`（图集下载） |
| `douyin_cookie.go` | `loadDouyinUserCookie`（启动加载 Cookie）、`RefreshDouyinUserCookie`（热更新 Cookie） |
| `scheduler.go` | `douyinCooldownUntil`、`lastDouyinCookieCheck`、`isDouyinInCooldown`、`triggerDouyinCooldown`、case "douyin" 分发逻辑 |
| `startup_cleanup.go` | `StartupCleanup`：版本化清理（版本号变化触发重新清理），扫描包含非法字符的目录并删除 |

### 2.3 `web/api/` 抖音相关文件

| 文件 | 端点 |
|------|------|
| `douyin_cookie.go` | `POST /api/douyin/cookie/validate`（验证 Cookie）、`GET /api/douyin/cookie/status`（查询状态） |
| `quickdl_douyin.go` | `handleDouyinQuickDownload`、`handleDouyinPreview`、`executeDouyinDownload`、`executeDouyinNoteDownload` |
| `diag.go` | `GET /api/diag/douyin`（连通性 + 签名引擎诊断） |

### 2.4 `web/static/js/` 抖音相关改动

| 文件 | 改动内容 |
|------|--------|
| `api.js` | `validateDouyinCookie(cookie)`、`getDouyinCookieStatus()` 两个 API 调用函数 |
| `components/quick-download.js` | `extractDouyinUrl()` URL 提取函数；平台识别（douyin/bilibili）；图集提示；预览信息展示 |
| `pages/settings.js` | 抖音 Cookie 配置面板：状态徽章、输入框、验证按钮、保存按钮 |
| `pages/sources.js` | 订阅源类型标签支持 `douyin`；添加订阅源时支持抖音链接占位符 |

---

## 3. 测试覆盖检查

### 3.1 douyin 包覆盖率

```
ok    video-subscribe-dl/internal/douyin    coverage: 32.6% of statements
```

### 3.2 全量测试

```
ok    video-subscribe-dl/internal/bilibili
ok    video-subscribe-dl/internal/danmaku
ok    video-subscribe-dl/internal/db
ok    video-subscribe-dl/internal/douyin
ok    video-subscribe-dl/internal/downloader
ok    video-subscribe-dl/internal/scheduler
ok    video-subscribe-dl/internal/util
ok    video-subscribe-dl/tests
ok    video-subscribe-dl/web/api
```

全部通过，无失败。

### 3.3 有/无测试文件对照

| 文件 | 测试文件 |
|------|--------|
| `abogus_pool.go` | abogus_test.go |
| `client.go` | client_test.go |
| `cookie.go` | cookie_test.go |
| `fingerprint.go` | fingerprint_test.go |
| `ratelimit.go` | ratelimit_test.go |
| `sign_updater.go` | sign_updater_test.go |
| `cookie_validator.go` | 无测试（直接发网络请求，难 mock） |
| `diag.go` | 无测试（工具函数） |
| `endpoints.go` | 无测试（常量 + URL 构建） |
| `logger.go` | 无测试（单行初始化） |
| `request_params.go` | 无测试（间接由 client_test 覆盖） |
| `sign_pool.go` | 无独立测试（通过 abogus_test 间接覆盖） |
| `stats.go` | 无测试（薄封装） |
| `types.go` | 无测试（纯数据结构） |

> 32.6% 覆盖率偏低，主要未覆盖路径：GetVideoDetail、getVideoDetailAPI、getVideoDetailPage、getNoteDetail、GetUserVideos（完整翻页逻辑）、GetUserProfile、ResolveVideoURL。这些都涉及真实网络请求，需要 HTTP mock 或 testcontainer。

---

## 4. 功能完整性检查

| 功能 | 状态 | 实现位置 |
|------|------|--------|
| 添加抖音订阅源（URL 解析 + 用户信息获取） | ✅ | `client.go#ResolveShareURL`、`parseLongURL`、`GetUserProfile`；`check_douyin.go#resolveDouyinSecUID` |
| 增量检查新视频（checkDouyin） | ✅ | `scheduler/check_douyin.go#checkDouyin`：基于 latestVideoAt 时间戳增量，翻页 5-10s 间隔 |
| 全量补漏扫描（fullScanDouyin） | ✅ | `scheduler/check_douyin.go#fullScanDouyin`：三阶段（全量拉 → 比对 → 创建 pending） |
| 视频下载（无水印） | ✅ | `process_douyin.go`：ResolveVideoURL 跟随 302 获取无水印地址，tmp 文件 + Rename 原子写入 |
| 图集/笔记下载 | ✅ | `process_douyin.go#downloadDouyinNote`：多图按序下载，图片间 500ms 随机间隔 |
| NFO 元数据生成（平台区分） | ✅ | `nfo.GenerateVideoNFO/GenerateMovieNFO`，VideoMeta.Platform = "douyin" 与 bilibili 区分 |
| 视频独立子目录 | ✅ | `videoDir = {uploaderDir}/{safeTitle} [{awemeID}]/`（与 B 站保持一致） |
| 快速下载（粘贴链接） | ✅ | `web/api/quickdl_douyin.go`：分享链接解析 → GetVideoDetail → 异步下载 + SSE 通知 |
| 用户 Cookie 配置（Web UI） | ✅ | `web/static/js/pages/settings.js`：输入框 + 验证按钮 + 保存 |
| Cookie 验证 API | ✅ | `POST /api/douyin/cookie/validate`：格式检查 + API 实探 |
| 文件名非法字符清洗 | ✅ | `SanitizePath()`：9 类非法字符替换 + Unicode 控制字符过滤 + 零宽字符 + 长度截断 80 |
| 启动时幽灵目录清理 | ✅ | `startup_cleanup.go#StartupCleanup`：版本化（cleanup_version_done），扫描含非法字符目录，删除并重置 DB 状态 |
| 诊断日志 | ✅ | slog 结构化日志；`GET /api/diag/douyin` 诊断端点；`TestXBogusSign` |
| 风控检测和降级 | ✅ | 多层次：API status_code 2053/2154、filter_list、403/429 识别、ReportResult 限流、triggerDouyinCooldown、三级签名降级 |
| 限流器 | ✅ | 令牌桶（默认 3s/req）、AcquireWithBackoff 指数退避、ReportResult penalty、Stop 幂等 |

**功能完整性：15/15**

---

## 5. 已知 Bug / 风险点

### 5.1 高风险：sign_pool.go 缺少 JS 注入转义

**位置:** `internal/douyin/sign_pool.go:103`

```go
// sign_pool.go — 未转义
code := fmt.Sprintf("sign('%s', '%s')", queryStr, userAgent)

// abogus_pool.go — 有转义（对比）
safeQuery := escapeJSString(queryStr)
safeUA := escapeJSString(userAgent)
code := fmt.Sprintf("generate_a_bogus('%s', '%s')", safeQuery, safeUA)
```

queryStr 是 URL encode 后的字符串，实际上不含单引号，当前低风险。但两个池的实现不一致是隐患，将来若 userAgent 中出现 ' 或 \ 会导致 JS 语法错误。

**建议修复:** 一行改动，补全 escapeJSString 调用。

---

### 5.2 中风险：sign_updater.go 热替换 sync.Once 方式不正确

**位置:** `internal/douyin/sign_updater.go`

直接重置 globalSignPoolOnce（包级变量），这是 sync.Once 的非正常用法。在极端并发场景下可能导致竞态。

**实际影响:** 签名更新是低频操作，实际并发窗口极小，当前概率很低。

**建议修复:** 使用 sync.RWMutex + atomic 指针直接替换。

---

### 5.3 中风险：downloadDouyinFile 和 quickDownloadDouyinFile 代码重复

`scheduler/process_douyin.go#downloadDouyinFile` 与 `web/api/quickdl_douyin.go#quickDownloadDouyinFile` 逻辑完全相同，任何 bug 修复都需要同步两处。

**建议:** 抽取到 internal/douyin/download.go 共享。

---

### 5.4 中风险：Cookie 过期后无自动刷新机制

Cookie 验证失败只 log warning，不阻断也不触发降级。过期 Cookie 会持续产生 403/2053，直到风控冷却兜底。

**建议:** 验证失败后自动切换到无 Cookie 模式并降低请求频率。

---

### 5.5 中风险：HTTP Client 在文件下载中每次新建

`downloadDouyinFile` / `quickDownloadDouyinFile` 中 `&http.Client{Timeout: 10 * time.Minute}` 每次创建新实例，不复用连接池。

**注意:** DouyinClient 内部的 API 请求客户端是正确复用的，此问题仅在文件下载环节。

**建议:** 使用包级共享 http.Client。

---

### 5.6 低风险：replaceEntry 池耗尽潜在死锁

`sign_pool.go` 和 `abogus_pool.go` 的 `replaceEntry()` 如果两次重试都失败，通道少一个槽。极端情况下 4 个 VM 全部失败替换 → pool 通道空 → 所有后续请求永久阻塞。

**建议:** 失败时向池补回一个带标记的占位条目或超时机制。

---

### 5.7 低风险：ValidateCookie 使用硬编码测试用户

`cookie_validator.go:29` 使用固定 secUID 做探测。若该用户注销/私密，ValidateCookie 将永远返回 false。

**建议:** 改为调用无用户上下文的 API，或尝试多个备选 secUID。

---

### 5.8 低风险：goroutine 泄漏已修复

- RateLimiter.Stop() 已用 sync.Once 保护幂等性
- DouyinClient.Close() 在所有调用点都有 defer
- SignUpdater.StopAutoUpdate() 也有 sync.Once 保护

---

### 5.9 低风险：errors 处理整体完善

- 网络请求错误均有 %w 包装传播
- DB 错误有 log 不影响主流程
- NFO/封面失败只 log，不影响主下载（合理降级）

---

## 汇总

| 维度 | 评估 |
|------|------|
| 功能完整性 | 15/15 全部实现 |
| 测试覆盖 | 32.6%（核心网络层未覆盖，可接受） |
| 错误处理 | 整体完善，有明确降级策略 |
| Goroutine 泄漏 | 已修复（Stop 幂等 + Close defer）|
| Panic 风险 | replaceEntry 池耗尽场景有潜在死锁 |
| 资源泄漏 | 文件下载 http.Client 每次新建 |
| JS 注入安全 | sign_pool.go 缺少转义（低概率，高一致性要求） |
| 代码重复 | downloadDouyinFile 两处重复 |
| Cookie 过期 | 无自动降级，依赖风控冷却兜底 |
| 热更新机制 | sync.Once 热替换方式不规范 |

**最优先修复:** sign_pool.go 补全 escapeJSString（一行改动）  
**次优先修复:** replaceEntry 失败时向池补回占位，避免潜在死锁  
**建议重构:** 将 downloadDouyinFile 抽取到 internal/douyin 包共享
