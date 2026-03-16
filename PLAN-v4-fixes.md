# PLAN: v4 评估修复（5 项）

## 背景
v4 评估（8.2/10）发现的 5 个改进项。

## 任务清单

### Task 1: RateLimiter.Stop() 幂等保护（P0，5 分钟）
- 文件: internal/douyin/ratelimit.go
- 改动: Stop() 加 sync.Once，防止 double close channel panic
- 验收: `go test ./internal/douyin/... -run TestStop -v` 重复调用不 panic

### Task 2: 签名热更新自动定时轮询（P1，15 分钟）
- 文件: internal/douyin/sign_updater.go
- 改动: 添加 ticker（默认 6h），自动 CheckAndUpdate
- 配置: `sign_check_interval_hours` DB setting，fallback 6
- 验收: 启动时 log "签名自动更新已启用，间隔 6h"

### Task 3: Prometheus metrics 兼容（P2，15 分钟）
- 文件: web/api/metrics.go
- 改动: 新增 GET /api/metrics/prometheus，输出 Prometheus text format
- 保留现有 /api/metrics JSON 端点不变
- 验收: curl /api/metrics/prometheus 返回 # HELP / # TYPE 格式

### Task 4: CI 测试覆盖率报告（P3，10 分钟）
- 文件: .github/workflows/docker.yml
- 改动: 添加 `go test -coverprofile=coverage.out ./...` + 覆盖率摘要输出
- 验收: CI log 中有覆盖率百分比

### Task 5: 集成测试增加 HTTP mock（P4，15 分钟）
- 文件: tests/integration_test.go
- 改动: 用 httptest.Server mock B站/抖音 API 响应，验证解析逻辑端到端
- 新增: TestBiliAPIParseWithMock, TestDouyinAPIParseWithMock
- 验收: `go test ./tests/... -v` 新测试 PASS

## 验收标准（总体）
- go build + go vet 通过
- go test ./... 全部 PASS（含新增测试）
- 无回归（既有 172 个测试不受影响）

## 状态: 进行中
