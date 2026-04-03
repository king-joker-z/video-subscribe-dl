# Project: Video Subscribe DL — v3.0

## What This Project Is
一站式视频订阅自动下载工具（VSD），专为 Emby/Jellyfin/Plex 媒体库打造。
支持 Bilibili、抖音、Pornhub 三平台自动订阅与下载，提供 Web UI 管理界面。

## v3.0 Goals
**Milestone 1 — Stability & Security（稳定化）**✅ 2026-04-03
- 修复所有 Critical/High 安全与可靠性问题
- 解决调度器挂死、认证缺失、性能瓶颈
- Phase 1.1 Auth Hardening ✅ | Phase 1.2 PH Scheduler Reliability ✅ | Phase 1.3 Performance & Resilience ✅

**Milestone 2 — Quality & UX（体验提升）**
- Web UI 体验优化（P1 前端 Bug 修复 + 交互改进）— Phase 2.1 ✅ 2026-04-03
- 可观测性提升（更好的指标与日志）
- 测试覆盖率显著提升

## Core User Need
个人/家庭 NAS 用户订阅视频博主，自动下载到媒体库，无需手动干预，
系统长期稳定运行，异常时有通知，可通过 Web UI 监控和管理。

## Key Constraints
- 单 Go 二进制，无外部依赖（除 ffmpeg）
- Docker 部署优先，纯 Go SQLite（无 CGO）
- 构建/测试通过 GitHub CI（本地不执行 go build/go test）
- git push 前需经宇融确认

## Codebase Map
See `.planning/codebase/` for full analysis:
- STACK.md — Go 1.25 + Vue 3 + SQLite + Goja
- ARCHITECTURE.md — 5-layer monolith
- CONCERNS.md — Critical/High issues inventory
- TESTING.md — 28 test files, key coverage gaps
