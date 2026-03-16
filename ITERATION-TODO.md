# VSD 抖音优化迭代记录

## 已完成

### Phase 3 — 健壮性增强
- [x] 抖音 GetUserVideos 使用新限流器（AcquireWithBackoff + ReportResult）— f9c01ee
- [x] 抖音 checkDouyin 增加连续错误计数 + 自动降级 — ff0b0f8
- [x] 页面解析方案增加 filter_list 风控检测 + status_code 诊断 — 2bc01f8
- [x] 抖音图集 canonical link 判断优化（多信号: canonical + og:url + aweme_type）— 2156bfc

### Phase 4 — 功能扩展
- [x] 抖音用户详情 API（GetUserProfile）获取头像/简介/粉丝数 — d6f6037
- [x] API 端点集中管理（endpoints.go）— fd85448
- [ ] 抖音合集支持（/aweme/v1/web/mix/aweme/）
- [ ] 抖音用户喜欢列表支持

### Phase 5 — 反风控增强
- [x] UA 池扩充 + sec-ch-ua Client Hints — 5789f6b
- [x] 浏览器指纹生成（fingerprint.go）— b5163db
- [x] Cookie 会话一致性（同一会话内保持相同指纹）— 104304e

### 通用改进
- [x] quickdl.go 拆分（B站 526行 + 抖音 567行）— d5c1fe8
- [ ] 抖音下载进度追踪（与前端 SSE 集成）
- [ ] 单元测试覆盖

## 待做

### Phase 2 — a_bogus 签名
- [ ] 研究 Python 版 ABogus 算法（TikTokDownloader/Evil0ctal），评估 Go 移植可行性
- [ ] 实现 a_bogus 签名（goja + JS 版本优先，或 Go 纯实现）
- [ ] 签名策略链：a_bogus → X-Bogus → 无签名降级
- [ ] 测试验证签名有效性

### 其他
- [ ] 抖音合集支持
- [ ] 抖音用户喜欢列表
- [ ] 抖音下载进度追踪
- [ ] 单元测试覆盖
