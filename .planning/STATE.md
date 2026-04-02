# Project State — VSD v3.0

## Status
**Active** | Milestone 1: Stability & Security

## Current Phase
**Phase 1.1 — Auth Hardening** (not started)

## Phases

| Phase | Title | Status |
|-------|-------|--------|
| 1.1 | Auth Hardening | ⬜ Pending |
| 1.2 | PH Scheduler Reliability | ⬜ Pending |
| 1.3 | Performance & Resilience | ⬜ Pending |
| 2.1 | Frontend Bug Fixes | ⬜ Pending |
| 2.2 | Observability | ⬜ Pending |
| 2.3 | Test Coverage | ⬜ Pending |
| 2.4 | Migration Hardening | ⬜ Pending |

## Milestones

| Milestone | Title | Status |
|-----------|-------|--------|
| 1 | Stability & Security | 🔄 In Progress |
| 2 | Quality & UX | ⬜ Pending |

## Key Decisions
- Pacing: small steps (≤ 1-2 days per phase)
- Build/test via GitHub CI only (no local go build/go test)
- Git push requires 宇融 confirmation
- WS auth: replace ?token= with short-lived session nonce (POST /api/session)
- Auth: re-enable ensureAuthToken(), auto-generate on first run

## Created
2026-04-02

## Last Updated
2026-04-02
