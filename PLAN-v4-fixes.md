# PLAN: v4 评估修复（5 项）

## 背景
v4 评估（8.2/10）发现的 5 个改进项。

## 任务清单

### Task 1: RateLimiter.Stop() 幂等保护（P0）✅
- 文件: internal/douyin/ratelimit.go, internal/bilibili/ratelimit.go
- 改动: Stop() 加 sync.Once，防止 double close channel panic
- 测试: TestRateLimiterDoubleStop, TestBiliRateLimiterDoubleStop
- commit: d33b77e

### Task 2: 签名热更新自动定时轮询（P1）✅
- 文件: internal/douyin/sign_updater.go, cmd/server/main.go
- 改动: StartAutoUpdate/StopAutoUpdate 方法，main.go 启动时调用（6h间隔），graceful shutdown 停止
- 测试: TestSignUpdaterAutoCheck, TestSignUpdaterStopWithoutStart, TestSignUpdaterDoubleStop
- commit: b953349

### Task 3: Prometheus metrics 兼容（P2）✅
- 文件: web/api/metrics.go, web/api/router.go
- 改动: 新增 GET /api/metrics/prometheus，Prometheus text format
- 测试: TestPrometheusMetrics
- commit: 16d7ec7

### Task 4: CI 测试覆盖率报告（P3）✅
- 文件: .github/workflows/docker.yml
- 改动: build 前增加 go test -coverprofile + coverage summary
- commit: cd3f863

### Task 5: 集成测试增加 HTTP mock（P4）✅
- 文件: tests/integration_test.go
- 改动: httptest mock B站/抖音 API 响应，验证解析逻辑
- 测试: TestBiliVideoParseWithMock, TestDouyinVideoParseWithMock, TestBiliErrorResponseWithMock
- commit: 9ea8981

## 验收标准
- [x] go build ./... 通过
- [x] go vet ./... 通过
- [x] go test ./... -count=1 全部 PASS
- [x] RateLimiter.Stop() 重复调用不 panic
- [x] SignUpdater 有 StartAutoUpdate/StopAutoUpdate
- [x] /api/metrics/prometheus 返回标准格式
- [x] CI 配置有 test + coverage step
- [x] 集成测试有 HTTP mock 测试用例
- [x] git push 完成

## 状态: ✅ 完成
