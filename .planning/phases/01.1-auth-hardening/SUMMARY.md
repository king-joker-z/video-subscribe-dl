---
phase: 01.1-auth-hardening
plan: 01
subsystem: auth
tags: [authentication, websocket, nonce, session, security]

# Dependency graph
requires: []
provides:
  - Auth enabled by default on every VSD deployment (ensureAuthToken wired)
  - POST /api/session endpoint issuing 60s single-use nonces for WebSocket auth
  - WebSocket /api/ws/logs requires ?session=<nonce> before upgrade
  - ?token= query-param auth path removed from both middleware files
  - Frontend createLogSocket fetches nonce before opening WebSocket
affects: [01.2-ph-scheduler, 01.3-performance, 02.3-test-coverage]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Session nonce pattern: short-lived single-use nonces for stateless WS auth"
    - "Middleware chain: rateLimitMiddleware(authMiddleware(mux)) in Server.Start()"

key-files:
  created:
    - web/server_test.go
  modified:
    - web/server.go
    - web/router.go
    - web/api/middleware.go
    - web/api/events.go
    - web/api/router.go
    - web/static/js/api.js
    - web/static/js/pages/logs.js

key-decisions:
  - "Use in-process sync.Map nonce store (not DB) — nonces are ephemeral, 60s TTL, lost on restart is acceptable"
  - "validateNonce is nil when auth disabled (NO_AUTH=1) — allows skipping nonce check cleanly"
  - "?token= query param removed as dead code — frontend never sent it, prevents future accidental use"
  - "api/middleware.go AuthMiddleware cleaned but not wired — consolidation deferred to follow-up"

patterns-established:
  - "Nonce flow: POST /api/session → ?session=<nonce> in WS URL → validateAndConsumeNonce"
  - "Defense-in-depth: cookie auth + nonce both required for WS (when auth enabled)"
  - "Graceful degradation: createLogSocket falls back if /api/session fails"

requirements-completed: [REQ-SEC-1, REQ-SEC-2]

# Metrics
duration: 25min
completed: 2026-04-02
---

# Phase 1.1: Auth Hardening Summary

**Re-enabled authentication by default (ensureAuthToken), wired authMiddleware into handler chain, replaced dead ?token= WebSocket auth with short-lived session nonces (POST /api/session → ?session=<nonce>)**

## Performance

- **Duration:** 25 min
- **Started:** 2026-04-02T00:00:00Z
- **Completed:** 2026-04-02T00:25:00Z
- **Tasks:** 8
- **Files modified:** 7 (+ 1 new test file)

## Accomplishments

- Authentication is now enabled by default on every fresh VSD deployment — `ensureAuthToken()` is called in `Start()` and `authMiddleware` is wired into the handler chain
- Long-lived auth token is never exposed in WebSocket URLs: `POST /api/session` issues 60s single-use nonces, WS auth uses `?session=<nonce>` instead of `?token=<secret>`
- Dead `?token=` query-param auth path removed from both `server.go authMiddleware` and `api/middleware.go AuthMiddleware`, preventing accidental future use
- Frontend `createLogSocket` is now async and fetches nonce before opening WebSocket, with graceful fallback if session endpoint is unavailable
- 8 unit tests covering auth token generation (first-run, idempotent, NO_AUTH bypass), nonce lifecycle (valid, expired, single-use), and session endpoint behaviour

## Task Commits

Each task was committed atomically:

1. **T1: Re-enable ensureAuthToken() and wire authMiddleware** - `e5babb6` (feat)
2. **T2: Add nonce store and session handler to Server** - `14c4d21` (feat)
3. **T3: Register POST /api/session route** - `07bb4a7` (feat)
4. **T4: Remove ?token= query-param auth from both middleware files** - `6c39a8c` (feat)
5. **T5: Add nonce validation to HandleWSLogs in EventsHandler** - `c05f37b` (feat)
6. **T6: Wire nonce validator from Server through Router into EventsHandler** - `0cd6691` (feat)
7. **T7: Update frontend createLogSocket to fetch session nonce** - `fa72d7b` (feat)
8. **T8: Add auth hardening tests in web/server_test.go** - `cdec724` (test)

## Files Created/Modified

- `web/server.go` — uncommented `ensureAuthToken()`, wired `authMiddleware`, added `nonceMu`/`nonceStore` fields, `handleSessionCreate`, `validateAndConsumeNonce`, background cleanup goroutine, `SetValidateNonceFunc` wiring call
- `web/router.go` — registered `POST /api/session` route
- `web/api/middleware.go` — removed `?token=` query-param check from `AuthMiddleware`
- `web/api/events.go` — added `validateNonce` field, `SetValidateNonceFunc` setter, nonce check before WebSocket upgrade in `HandleWSLogs`
- `web/api/router.go` — added `validateNonceFunc` field and `SetValidateNonceFunc` setter to `Router`
- `web/static/js/api.js` — replaced `createLogSocket` with async version that fetches nonce from `POST /api/session`
- `web/static/js/pages/logs.js` — made `connect` callback `async`, awaits `createLogSocket`
- `web/server_test.go` (new) — 8 unit tests for auth token and nonce behaviour

## Decisions Made

- **In-process nonce store:** Used `map[string]time.Time` protected by `sync.Mutex` rather than persisting nonces to DB. Nonces are ephemeral 60s tokens — loss on restart is acceptable and avoids DB write overhead per WS connection.
- **nil validator = auth disabled:** `validateNonce` field in `EventsHandler` is nil when `apiRouter` is not set or auth is disabled (`NO_AUTH=1`), cleanly skipping the nonce check without branching on env vars in the WebSocket handler.
- **?token= as dead code:** Removed without replacement — the frontend never sent it. This closes the accidental exposure risk permanently.
- **api/middleware.go AuthMiddleware not wired:** This handler is defined but unused (server.go's own authMiddleware is wired). Cleaned the `?token=` path but did not consolidate — that is a separate refactoring out of scope for Phase 1.1.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Auth hardening complete; every fresh deployment now generates a token and requires authentication
- Session nonce infrastructure in place for secure WebSocket connections
- Ready for Phase 1.2 — PH Scheduler Reliability

---
*Phase: 01.1-auth-hardening*
*Completed: 2026-04-02*
