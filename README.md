# Video Subscribe DL

订阅 · 下载 · 刮削 · 整理

一站式视频订阅自动下载工具，专为 Emby/Jellyfin/Plex 媒体库打造。

## ✨ 功能特性

- 📥 **自动订阅下载** — 添加 UP主 URL，自动检查并下载新视频
- 🎬 **Bilibili 原生 API** — 直接调用 B站 API 获取视频列表和元数据（无 yt-dlp 依赖）
- 📊 **DASH 高清下载** — 支持 4K/HDR，自动选择最佳画质和音质
- 📁 **NFO 元数据** — 自动生成 Emby/Jellyfin/Plex 兼容的 NFO 文件（movie/tvshow 格式）
- 🖼️ **封面图和头像** — 自动下载视频封面、UP 主头像，生成 People 目录
- 💬 **弹幕下载** — 自动下载弹幕并转换为 ASS 字幕格式
- 🍪 **Cookie 验证** — 上传 Cookie 后自动验证登录状态、VIP 信息
- 📡 **实时进度** — SSE 推送下载进度（百分比、速度、剩余时间）
- 📋 **日志查看器** — 内置日志面板，SSE 流式推送，支持级别筛选
- 🔄 **数据一致性** — 启动时自动对账数据库与本地文件，修复异常状态
- 🛡️ **错误重试** — 指数退避自动重试，区分临时/永久失败
- 🗂️ **合集支持** — 自动识别 B站合集，按 tvshow/Season 结构整理
- 🐳 **Docker 部署** — 一键启动，无需安装依赖
- 🎨 **深色主题 UI** — 简洁美观的 Web 管理界面
- 🔐 **API 鉴权** — 可配置 Token 保护 Web UI 和 API 接口
- 📑 **多P视频支持** — B站分P视频完整下载，自动识别所有分P
- 📂 **收藏夹订阅** — 支持订阅 B站合集/收藏夹，增量同步
- 🚦 **风控退避** — 自动检测 B站风控（-352/-401/-412），智能冷却
- ⏬ **分块并行下载** — 大文件（>50MB）自动拆分多线程下载，可配置并行数
- 🐌 **下载限速** — 可配置最大下载速度，避免占满带宽
- 🔔 **多通道通知** — 支持 Webhook / Telegram Bot / Bark (iOS) 三种推送方式
- 📊 **下载统计** — 实时展示下载速度、进度、文件大小

## 🚀 快速开始

### Docker Compose（推荐）

```yaml
version: '3'
services:
  video-subscribe-dl:
    image: video-subscribe-dl:latest
    container_name: video-subscribe-dl
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./data:/app/data              # 配置和数据库
      - ./downloads:/app/downloads     # 下载目录（指向 Emby 媒体库）
      - ./cookies:/app/cookies         # Cookie 文件目录
    environment:
      - TZ=Asia/Shanghai
```

> 💡 **Cookie 挂载说明**: `./cookies` 目录用于存放 bilibili Cookie 文件（Netscape 格式）。
> 容器内路径为 `/app/cookies/cookies.txt`，在 Web UI 设置页上传或手动放入均可。
> Cookie 用于获取高清画质（1080P+）和 VIP 专享内容。

```bash
docker-compose up -d
```

打开 http://localhost:8080 即可使用。

### Docker Run

```bash
docker run -d \
  --name video-subscribe-dl \
  -p 8080:8080 \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/downloads:/app/downloads \
  -v $(pwd)/cookies:/app/cookies \
  video-subscribe-dl:latest
```

## 📖 使用说明

### 第一步：配置 Cookie（推荐）

配置 Cookie 可解锁高清画质和 VIP 内容：

1. 用浏览器登录 [bilibili.com](https://www.bilibili.com)
2. 安装 Cookie 导出扩展：
   - Chrome: [Get cookies.txt](https://chrome.google.com/webstore/detail/get-cookiestxt)
   - Firefox: [cookies.txt](https://addons.mozilla.org/en-US/firefox/addon/cookies-txt/)
3. 导出 Cookie 文件（Netscape 格式）
4. 在 Web UI 设置页上传，系统自动验证并显示：
   - ✅ 绿色 = 登录有效（显示用户名、VIP 状态）
   - ❌ 红色 = Cookie 无效或已过期

### 第二步：添加订阅

1. 点击「添加订阅」按钮
2. 粘贴 UP 主空间 URL：`https://space.bilibili.com/123456`
3. 系统自动识别 UP 主信息（名称、头像、视频数）
4. 设置检查间隔（默认 6 小时）
5. 点击「添加」，完成！


### 第二步 (b)：添加抖音订阅

除了 B站，还支持订阅抖音用户的视频：

**支持的抖音链接格式：**
- 用户主页：`https://www.douyin.com/user/MS4wLjABAAAA...`
- 分享链接：`https://v.douyin.com/xxxxx/`（短链会自动解析）

**添加方式：**
1. 复制抖音用户的主页链接或分享链接
2. 粘贴到「添加订阅」输入框，系统自动识别为抖音类型
3. 自动获取用户名称并开始扫描视频

**抖音订阅 vs 快速下载：**
| 功能 | 订阅模式 | 快速下载 |
|------|---------|---------|
| 用途 | 持续追踪用户新视频 | 单次下载指定视频 |
| 输入 | 用户主页链接 | 视频链接 |
| 行为 | 定期检查 + 增量下载 | 立即下载一个视频 |

**已知限制：**
- 仅支持公开视频（私密账号无法获取视频列表）
- X-Bogus 签名算法可能随抖音更新而失效，届时会自动降级为无签名模式
- 抖音风控较严格，翻页间隔自动控制在 5-10 秒
- 不需要配置 Cookie，系统会自动生成必要的认证参数

### 第三步：自动下载

添加订阅后，系统自动：
1. 获取 UP 主所有视频列表（含合集）
2. 下载视频（DASH 最佳画质 + 最佳音质）
3. 生成 NFO 元数据文件
4. 下载视频封面和 UP 主头像
5. 下载弹幕并转换为 ASS 字幕
6. 定时检查新视频并增量下载

### 目录结构

下载完成后，文件按 Emby/Jellyfin 标准组织：

```
downloads/
├── UP主名称/
│   ├── 视频标题 [BVxxx]/
│   │   ├── 视频标题 [BVxxx].mp4
│   │   ├── 视频标题 [BVxxx].nfo
│   │   ├── 视频标题 [BVxxx]-thumb.jpg
│   │   └── 视频标题 [BVxxx].ass       # 弹幕字幕
│   └── 合集名称/
│       ├── tvshow.nfo
│       ├── poster.jpg
│       └── Season 1/
│           ├── S01E01 - 标题.mp4
│           └── S01E01 - 标题.nfo
├── metadata/
│   └── people/
│       └── UP主名称/
│           ├── person.nfo
│           └── folder.jpg              # UP主头像
```

直接将 `downloads/` 指向 Emby/Jellyfin 媒体库即可自动识别。

<!-- 
## 📸 截图

### 主界面
![主界面](docs/screenshots/main.png)

### 下载进度
![下载进度](docs/screenshots/progress.png)

### 日志面板
![日志面板](docs/screenshots/logs.png)

### Cookie 设置
![Cookie 设置](docs/screenshots/cookie.png)
-->

## ⚙️ 配置说明

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|-----|
| `--data-dir` | `./data` | 数据库和配置目录 |
| `--download-dir` | `./data/downloads` | 视频下载目录 |
| `--port` | `8080` | Web UI 端口 |

### 环境变量

| 环境变量 | 默认值 | 说明 |
|---------|--------|-----|
| `TZ` | `UTC` | 时区（推荐 `Asia/Shanghai`） |
| `AUTH_TOKEN` | *(自动生成)* | Web UI 认证 Token（首次启动自动生成，也可手动指定） |
| `NO_AUTH` | `0` | 设为 `1` 禁用 Web UI 认证 |
| `CORS_ORIGIN` | *(空)* | 允许的跨域来源（如 `http://localhost:3000`） |

### Web UI 运行时配置

以下配置可在 Web UI「设置」页面动态修改，无需重启：

- **下载并发数** — 同时下载的视频数量
- **请求频率限制** — API 限速（次/分钟）
- **下载冷却时间** — 两次下载之间的间隔
- **分块并行线程数** — 大文件分块下载的并行数
- **最大下载速度** — 带宽限速
- **通知设置** — Webhook / Telegram Bot / Bark (iOS)

## 🛠️ 技术栈

- **后端**: Go 1.22 + Pure Go SQLite（无 CGo 依赖，交叉编译友好）
- **前端**: 原生 HTML/CSS/JS（深色主题）
- **下载**: Bilibili DASH API + ffmpeg 合并
- **弹幕**: Bilibili 弹幕 API + XML→ASS 转换
- **元数据**: NFO XML（Emby/Jellyfin/Plex 兼容）
- **部署**: Docker + Alpine Linux

## 🔧 开发

```bash
# 本地运行
export PATH=$PATH:/usr/local/go/bin
go run ./cmd/server

# 编译
go build -v ./cmd/server

# 构建 Docker 镜像
docker build -t video-subscribe-dl .
```

## 📝 版本历史

### v2.1.1 (2026-03-15)
- 修复数据库 schema 遗漏新增列导致测试失败
- 修复 GetSource Scan 缺少 filter_rules 导致的 nil pointer panic
- 删除 7 个旧版 API handler 文件（~1640 行冗余代码）
- 清理临时参考文件（BILI-SYNC-REF/、RECOVERY-REFERENCE.md、BUGS.md）
- 全面代码审查：API 路径一致性、goroutine 泄漏检查、nil pointer 风险排查

### v2.1.0 (2026-03-12)
- 分块并行下载：大文件自动拆分多线程下载（默认4线程，可配置）
- API 鉴权：Token 保护 Web UI 和 API
- 多P视频支持：B站分P视频完整下载
- 收藏夹/合集订阅支持
- 风控智能退避：检测 -352/-401/-412 自动冷却
- 下载限速：可配置最大下载速度
- 下载统计和进度优化
- go vet 全量检查通过

### v2.0.0 (2026-03-12)
- 全面重构：bilibili 原生 API 替代 yt-dlp
- DASH 高清下载 + ffmpeg 合并
- Cookie 验证机制
- 实时下载进度（SSE）
- 日志查看器
- People 目录 + 封面图
- 弹幕下载（ASS 字幕）
- 数据库一致性对账
- 错误重试机制（指数退避）
- 全部 10 个已知 bug 修复

### v1.0.0
- 初始版本，基于 yt-dlp 下载

## License

MIT
