# Video Subscribe DL - 视频订阅下载刮削一体化工具

> 抖音/B站视频订阅自动下载 + 刮削整理 + Emby/Jellyfin 兼容

## 一、核心特性

| 特性 | 实现 |
|------|------|
| 多平台支持 | B站 + 抖音 + YouTube（ yt-dlp ） |
| 订阅管理 | Go + SQLite，增量检查 |
| 定时任务 | 复用现有 scheduler 模块 |
| 元数据刮削 | yt-dlp info.json → NFO + 封面 |
| 视频整理 | 按 UP主/频道名建目录，NFO + 封面 |
| 防止封号 | 请求间隔、Cookie 轮换、IP 轮换（可选） |
| Docker 部署 | 最小镜像，Alpine + yt-dlp |

## 二、技术架构

```
┌─────────────────────────────────────────────────────┐
│                    Web UI (Go)                      │
│   订阅管理 | 下载队列 | 历史记录 | 设置           │
└─────────────────────────────────────────────────────┘
                         │
┌────────────────────────┼────────────────────────┐
│                  Core Engine (Go)                  │
│  ┌─────────────┐  ┌─────────────┐  ┌──────────┐ │
│  │ Subscription │  │   Scheduler │  │  NFO     │ │
│  │  Manager    │  │   Checker   │  │ Generator│ │
│  └─────────────┘  └─────────────┘  └──────────┘ │
└────────────────────────┼────────────────────────┘
                         │
┌────────────────────────┼────────────────────────┐
│               External Tools                        │
│  ┌─────────────┐  ┌─────────────┐                │
│  │   yt-dlp    │  │   ffmpeg   │  (in Docker)   │
│  └─────────────┘  └─────────────┘                │
└─────────────────────────────────────────────────────┘
```

## 三、数据模型

### SQLite 表

```sql
-- 订阅源
CREATE TABLE sources (
    id INTEGER PRIMARY KEY,
    platform TEXT NOT NULL,  -- bilibili/douyin/youtube
    url TEXT NOT NULL,
    name TEXT,                -- 自动从平台获取
    cookies_file TEXT,        -- B站/抖音登录 Cookie
    check_interval INTEGER DEFAULT 3600,  -- 检查间隔(秒)
    download_quality TEXT DEFAULT 'best', -- 下载质量
    enabled INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 下载记录
CREATE TABLE downloads (
    id INTEGER PRIMARY KEY,
    source_id INTEGER,
    video_id TEXT NOT NULL,
    title TEXT,
    filename TEXT,
    status TEXT DEFAULT 'pending', -- pending/downloading/completed/failed
    file_path TEXT,
    file_size INTEGER,
    downloaded_at DATETIME,
    error_message TEXT,
    FOREIGN KEY (source_id) REFERENCES sources(id)
);

-- 去重 archive（复用 yt-dlp 格式）
-- 直接用 yt-dlp 的 download.archive 文件
```

## 四、核心功能

### 4.1 订阅管理
- 添加 B站/抖音/YouTube 订阅（URL 自动识别平台）
- 支持 Cookie 上传（登录态下载高清）
- 检查间隔配置
- 启用/禁用订阅

### 4.2 下载引擎
- 调用 yt-dlp 下载视频
- 支持质量选择（best/1080p/720p）
- 自动获取元数据（--write-info-json --write-thumbnail）
- 支持 B站分P下载
- 并发控制（避免触发反爬）

### 4.3 防封机制
- 请求间隔（可配置，默认 30s）
- 单 IP 限速
- Cookie 轮换（多账号场景）
- User-Agent 随机
- 失败自动退避

### 4.4 刮削整理
```
output/
├── 李子柒/
│   ├── metadata/
│   │   ├── 1234567890.nfo     (Kodi/Jellyfin 兼容)
│   │   ├── 1234567890.jpg     (封面)
│   │   └── 1234567890.info.json
│   ├── 1234567890.mp4
│   └── ...
├── 罗翔说法律/
│   └── ...
```

NFO 格式（符合 Emby/Jellyfin 标准）：
```xml
<?xml version="1.0" encoding="utf-8"?>
<item>
  <title>视频标题</title>
  <originaltitle>原始标题</originaltitle>
  <plot>简介</plot>
  <aired>2024-01-01</aired>
  <studio>UP主/频道名</studio>
  <genre>来源平台</genre>
  <provider>bilibili/douyin/youtube</provider>
  <provider_url>视频链接</provider_url>
</item>
```

### 4.5 定时检查
- 每 N 秒检查所有订阅
- 新视频加入下载队列
- 并发下载控制

## 五、Docker 部署

```dockerfile
FROM alpine:3.19

# 安装依赖
RUN apk add --no-cache \
    python3 py3-pip \
    ffmpeg \
    && pip3 install yt-dlp \
    && rm -rf /var/cache/apk/*

# 预热 yt-dlp（下载二进制）
RUN yt-dlp --version

# 体积优化：删除不必要文件
RUN rm -rf /root/.cache /tmp/*

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s \
    CMD curl -f http://localhost:8080/health || exit 1
```

## 六、API 设计

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/sources | 订阅列表 |
| POST | /api/sources | 添加订阅 |
| PUT | /api/sources/:id | 更新订阅 |
| DELETE | /api/sources/:id | 删除订阅 |
| GET | /api/downloads | 下载记录 |
| POST | /api/downloads/:id/retry | 重试下载 |
| GET | /api/queue | 下载队列状态 |
| POST | /api/queue/run | 手动触发检查 |

## 七、前端页面

- 订阅管理（列表、新增、编辑、删除）
- 下载队列（实时进度、状态）
- 历史记录（已完成、失败）
- 设置（下载路径、并发数、检查间隔）
- 日志查看

## 八、迭代计划

### Phase 1: 核心下载（3天）
- [ ] Go 项目骨架 + SQLite
- [ ] yt-dlp 集成
- [ ] B站订阅下载
- [ ] 基础 CLI/日志

### Phase 2: 刮削整理（2天）
- [ ] info.json 解析
- [ ] NFO 生成
- [ ] 目录整理

### Phase 3: Web UI（3天）
- [ ] Go Web 框架
- [ ] 订阅管理页面
- [ ] 下载队列页面
- [ ] 设置页面

### Phase 4: 防封 + 增强（3天）
- [ ] 请求间隔控制
- [ ] Cookie 管理
- [ ] 多订阅并发
- [ ] Docker 优化

### Phase 5: 抖音支持（2天）
- [ ] 抖音 Cookie 登录
- [ ] 抖音视频下载
- [ ] 抖音元数据处理

### Phase 6: 稳定化（1天）
- [ ] 错误处理
- [ ] 日志完善
- [ ] 性能调优

---

## 九、验收标准

- [ ] B站订阅可正常下载（公开视频）
- [ ] 登录后可下载高清
- [ ] 元数据 NFO 正确生成
- [ ] Jellyfin/Emby 可正确识别
- [ ] 增量下载不重复
- [ ] Docker 镜像 < 200MB
- [ ] 防封机制生效
- [ ] Web UI 完整可用
