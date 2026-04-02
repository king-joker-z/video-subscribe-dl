# Technology Stack

## Languages

- **Go** 1.25 — backend server, all platform clients, downloader engine
- **JavaScript** (ES modules, embedded) — Douyin request signing (`sign.js`, `abogus.js`), executed at runtime via Goja JS engine
- **TypeScript/Vue** — frontend SPA (compiled to static assets served by Go)
- **SQL** — SQLite schema and queries via `database/sql`

## Frameworks & Core Libraries

### Backend (Go)
| Package | Version | Purpose |
|---|---|---|
| `github.com/glebarez/sqlite` | v1.10.0 | Pure-Go SQLite driver (CGO-free) |
| `gorm.io/gorm` | v1.25.5 | ORM (used transitively via glebarez/sqlite) |
| `github.com/robfig/cron/v3` | v3.0.1 | Cron-style scheduler for periodic source checks |
| `github.com/dop251/goja` | v0.0.0-20260311 | ECMAScript 5.1 JS runtime — executes Douyin signing JS |
| `golang.org/x/net` | v0.52.0 | HTML parser (`golang.org/x/net/html`) for Pornhub scraping |

### Frontend (Node/Vite)
| Package | Version | Purpose |
|---|---|---|
| `vue` | ^3.5.30 | Frontend framework |
| `vue-router` | ^4.6.4 | SPA routing |
| `pinia` | ^3.0.4 | State management |
| `naive-ui` | ^2.44.1 | Component library |
| `@vueuse/core` | ^14.2.1 | Vue composition utilities |
| `@vicons/ionicons5` | ^0.13.0 | Icon set |
| `vite` | ^8.0.0 | Build tool / dev server |
| `@vitejs/plugin-vue` | ^6.0.5 | Vue SPA Vite plugin |

## Build & Tooling

- **Go toolchain** — `go build`, CGO disabled (`CGO_ENABLED=0`) for cross-compilation
- **Vite** — frontend build (`npm run build` → output embedded in Go binary via `embed.FS`)
- **Docker multi-stage build** — stage 1: `golang:1.25` builder; stage 2: `alpine:3.19` runtime
- **ffmpeg** — installed in Docker runtime image; used to merge DASH video+audio streams (`os/exec`)
- **GOPROXY** — `https://goproxy.io,https://proxy.golang.org,direct` (set as Docker ARG)
- **pprof** — optional Go profiling server, enabled via `PPROF_ADDR` env var

## Runtime Environment

- **Docker** — primary deployment target; image `jokermelove/video-subscribe-dl:latest`
- **Base image** — `alpine:3.19` (~80 MB vs Debian ~200 MB)
- **Go runtime** — 1.25, no CGO, statically linked binary
- **Node.js** — required at build time only (v22+ based on `.nvm` path found); not present at runtime
- **Port** — 8080 (HTTP), configurable via `--port` flag
- **Persistent volumes** — `/app/data` (SQLite DB + config), `/app/downloads` (downloaded videos)
- **TZ** — `Asia/Shanghai` default in docker-compose
- **ffmpeg** — runtime dependency in container for video muxing

## Key Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/glebarez/sqlite` | Pure-Go SQLite — stores sources, downloads, settings, people; no system libs needed |
| `github.com/robfig/cron/v3` | Periodic scheduler — triggers source sync checks every N seconds (default 2 h) |
| `github.com/dop251/goja` | JS engine — runs Douyin's `X-Bogus` and `a_bogus` signing algorithms at runtime; supports hot-reload from remote URLs |
| `golang.org/x/net/html` | HTML tokenizer/parser — scrapes Pornhub model pages and video listing pages |
| `gorm.io/gorm` | ORM layer (indirect, via sqlite driver) |
| `github.com/dlclark/regexp2` | Extended regex (indirect, via Goja) |
| `github.com/google/uuid` | UUID generation (indirect) |
| `net/http` (stdlib) | All outbound HTTP calls to Bilibili/Douyin/Pornhub APIs and file downloads |
| `os/exec` + ffmpeg | DASH stream merging — spawns `ffmpeg -i video -i audio -c copy out.mp4` |
| `embed` (stdlib) | Embeds compiled frontend static assets and JS signing scripts into the binary |
| `database/sql` (stdlib) | Raw SQL for settings, download state machine, reconciliation queries |
