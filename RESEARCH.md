# Bilibili 视频下载/订阅项目调研报告

> 调研时间: 2026-03-11
> 调研项目: bili-sync, ytdl-sub, BBDown, ytdl-nfo

---

## 目录

1. [bili-sync](#1-bili-sync)
2. [ytdl-sub](#2-ytdl-sub)
3. [BBDown](#3-bbdown)
4. [owdevel/ytdl-nfo](#4-owdevelytdl-nfo)
5. [综合对比与最佳实践](#5-综合对比与最佳实践)

---

## 1. bili-sync

**仓库**: https://github.com/amtoaer/bili-sync
**语言**: Rust
**定位**: B站收藏夹/合集/投稿/稍后再看的自动同步工具，生成 Emby/Jellyfin 兼容的 NFO 和目录结构

### 1.1 NFO 生成 - 完整 XML 结构

bili-sync 支持四种 NFO 类型，源码位于 `crates/bili_sync/src/utils/nfo.rs`。

#### (1) Movie NFO (单页视频)

```xml
<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<movie>
    <plot><![CDATA[原始视频：<a href="https://www.bilibili.com/video/BV1nWcSeeEkV/">BV1nWcSeeEkV</a><br/><br/>视频简介内容]]></plot>
    <outline/>
    <title>视频标题</title>
    <actor>
        <name>12345</name>
        <role>UP主昵称</role>
        <thumb>https://i1.hdslb.com/bfs/face/xxxxx.jpg</thumb>
    </actor>
    <year>2024</year>
    <genre>科技</genre>
    <genre>编程</genre>
    <uniqueid type="bilibili">BV1nWcSeeEkV</uniqueid>
    <premiered>2024-01-15</premiered>
</movie>
```

**注意**: `<actor>/<name>` 存的是 UP 主的 **mid (数字ID)**，`<role>` 存的是 UP 主昵称，`<thumb>` 存的是 UP 主头像 URL。

#### (2) TVShow NFO (多P视频/合集的整体信息)

```xml
<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<tvshow>
    <plot><![CDATA[原始视频：<a href="https://www.bilibili.com/video/BV1xxx/">BV1xxx</a><br/><br/>合集简介]]></plot>
    <outline/>
    <title>合集标题</title>
    <actor>
        <name>12345</name>
        <role>UP主昵称</role>
        <thumb>https://i1.hdslb.com/bfs/face/xxxxx.jpg</thumb>
    </actor>
    <year>2024</year>
    <genre>科技</genre>
    <genre>编程</genre>
    <uniqueid type="bilibili">BV1xxx</uniqueid>
    <premiered>2024-01-15</premiered>
</tvshow>
```

#### (3) Episode NFO (episodedetails - 多P视频的单集)

```xml
<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<episodedetails>
    <plot/>
    <outline/>
    <title>分P标题</title>
    <season>1</season>
    <episode>3</episode>
</episodedetails>
```

**注意**: `<season>` 固定为 `1`，`<episode>` 使用 page 的 pid (从1开始的分P序号)。

#### (4) Person NFO (UP 主信息)

```xml
<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<person>
    <plot/>
    <outline/>
    <lockdata>false</lockdata>
    <dateadded>2024-01-15 12:30:00</dateadded>
    <title>12345</title>
    <sorttitle>12345</sorttitle>
</person>
```

**注意**: `<title>` 和 `<sorttitle>` 使用的是 UP 主的数字 mid，不是昵称。

### 1.2 UP 主头像获取方式

bili-sync **不直接调用** `https://api.bilibili.com/x/space/wbi/acc/info?mid=xxx` 来获取头像。

头像来源于视频详情 API 的 `owner.face` 字段：

```rust
// bilibili/mod.rs - VideoInfo 枚举
pub enum VideoInfo {
    Detail {
        title: String,
        bvid: String,
        #[serde(rename = "owner")]
        upper: Upper<i64>,  // Upper 结构体包含 mid, name, face
        // ...
    },
    Favorite {
        upper: Upper<i64>,  // 收藏夹接口也返回 upper
        // ...
    },
    // ...
}

// bilibili/favorite_list.rs - Upper 结构体
pub struct Upper<T> {
    pub mid: T,
    pub name: String,
    pub face: String,  // ← UP主头像 URL，直接从视频列表API获取
}
```

**具体流程**:
1. 从收藏夹/合集/投稿等列表 API 获取视频信息，其中 `owner`/`upper` 字段包含 `face` (头像URL)
2. 存入数据库的 `video.upper_face` 字段
3. 下载时直接用 HTTP 下载该 URL 到 `{upper_path}/{首字母}/{mid}/folder.jpg`

**注意**: 收藏夹 API (`/x/v3/fav/resource/list`) 和视频详情 API (`/x/web-interface/view`) 都会返回 `owner.face`，所以不需要单独调用 space/acc/info 接口。

### 1.3 合集/视频季(Season)的处理逻辑

```rust
// bilibili/collection.rs - 合集类型定义
pub enum CollectionType {
    Season,   // 视频合集（season_id）
    Series,   // 视频列表（series_id）
}

pub struct CollectionItem {
    pub mid: String,      // UP 主 mid
    pub sid: String,      // season_id 或 series_id
    pub collection_type: CollectionType,
}
```

**API 调用**:

```
Season (合集):
GET https://api.bilibili.com/x/polymer/web-space/seasons_archives_list
    ?mid={mid}&season_id={sid}&page_num={page}&page_size=30&sort_reverse=true

Series (列表):
GET https://api.bilibili.com/x/series/archives
    ?mid={mid}&series_id={sid}&pn={page}&ps=30&sort=asc
    (需要 WBI 签名)
```

**关键处理逻辑**:

```rust
// Collection 返回的 VideoInfo 变体只有简略信息
VideoInfo::Collection {
    bvid: String,
    cover: String,
    ctime: DateTime<Utc>,
    pubtime: DateTime<Utc>,
    // 注意: 没有 title, intro, upper 等字段!
}
```

因为合集 API 返回的信息不完整（缺少 title、intro、upper），bili-sync 会在 `fetch_video_details` 阶段单独调用视频详情接口 (`/x/web-interface/view`) 补充每个视频的完整信息。

### 1.4 目录结构

```
{配置的路径}/                        # 视频源根目录（收藏夹/合集名）
├── {视频标题}/                      # 单页视频 → Movie 模式
│   ├── {视频标题}-poster.jpg        # 封面
│   ├── {视频标题}-fanart.jpg        # 同封面
│   ├── {视频标题}.mp4               # 视频
│   ├── {视频标题}.nfo               # Movie NFO
│   ├── {视频标题}.zh-CN.default.ass # 弹幕
│   └── {视频标题}.srt               # 字幕
│
├── {多P视频标题}/                   # 多页视频 → TVShow 模式
│   ├── poster.jpg                   # 剧集封面
│   ├── fanart.jpg                   # 同封面
│   ├── tvshow.nfo                   # TVShow NFO
│   └── Season 1/
│       ├── {base_name} - S01E01-thumb.jpg
│       ├── {base_name} - S01E01.mp4
│       ├── {base_name} - S01E01.nfo      # episodedetails NFO
│       ├── {base_name} - S01E01.zh-CN.default.ass
│       ├── {base_name} - S01E02-thumb.jpg
│       ├── {base_name} - S01E02.mp4
│       └── ...
│
{upper_path}/                        # UP主目录（全局配置）
└── 1/                               # mid 首字符
    └── 12345/                       # UP主 mid
        ├── folder.jpg               # UP主头像
        └── person.nfo               # Person NFO
```

**路径模板**: `video` 和 `page` 模板可通过 Handlebars 配置，默认使用 `video_format_args` 和 `page_format_args` 生成。

**Season 编号**: 固定使用 `Season 1`，episode 编号使用 `S01E{pid:02d}` 格式。

### 1.5 弹幕下载实现

**API**: 使用 gRPC protobuf 接口获取弹幕

```
POST https://api.bilibili.com/x/v2/dm/wbi/web/seg.so
    ?type=1&oid={cid}&segment_index={segment}
```

**关键实现** (位于 `bilibili/danmaku/`):

```rust
// danmaku/mod.rs - DanmakuWriter
pub struct DanmakuWriter {
    danmaku_segments: Vec<DmSegMobileReply>,  // protobuf 解码后的弹幕分段
    duration: i64,                             // 视频时长(秒)
}

// 获取弹幕的流程:
// 1. 按6分钟一个分段(segment)请求弹幕
// 2. 每个segment返回 protobuf 格式的 DmSegMobileReply
// 3. DmSegMobileReply 包含 Vec<DanmakuElem>
// 4. 将所有 DanmakuElem 转换为 ASS 格式的弹幕

// danmaku/writer.rs - ASS 弹幕写入
impl DanmakuWriter {
    pub async fn write(&self, path: PathBuf, option: &DanmakuOption) -> Result<()> {
        // 将 protobuf 弹幕转为 ASS 字幕格式
        // 支持配置:
        //   - duration_compensation: 弹幕显示时长补偿
        //   - 字体大小、颜色等
    }
}
```

**弹幕处理流程**:
1. 计算分段数: `ceil(video_duration / 360.0)` (360秒 = 6分钟)
2. 并发请求所有分段的弹幕数据 (protobuf 格式)
3. 解码 protobuf 得到 `DanmakuElem` 列表
4. 将弹幕转换为 ASS 字幕格式 (`.zh-CN.default.ass`)
5. 支持弹幕显示时长补偿和自定义样式

**弹幕数据结构** (protobuf):

```protobuf
message DanmakuElem {
    int64 id = 1;        // 弹幕ID
    int32 progress = 2;  // 弹幕出现时间(毫秒)
    int32 mode = 3;      // 弹幕模式(1滚动 4底部 5顶部)
    int32 fontsize = 4;  // 字号
    uint32 color = 5;    // 颜色(RGB)
    string content = 7;  // 弹幕内容
    // ...
}
```

---

## 2. ytdl-sub

**仓库**: https://github.com/jmbannon/ytdl-sub
**语言**: Python
**定位**: yt-dlp 的上层封装，自动将 YouTube/各平台视频组织为 Plex/Jellyfin/Kodi/Emby 兼容的媒体库结构

### 2.1 yt-dlp output template 对合集的处理

ytdl-sub 不直接使用 yt-dlp 的 output template 来处理合集，而是通过自己的配置系统 + 插件体系来管理。

**ytdl-sub 的 output template 变量** (内部定义):

```yaml
# ytdl-sub 使用 {source_variables} 风格的变量，不是 yt-dlp 的 %(variable)s
output_options:
  output_directory: "/media/youtube/{subscription_name}"
  file_name: "{episode_file_path}.{ext}"

# 对于 TV show 风格:
output_options:
  output_directory: "/media/youtube"
  file_name: "{tv_show_file_path}.{ext}"
```

**yt-dlp 变量在 bilibili 合集场景的实际值**:

| yt-dlp 变量 | bilibili 合集中的实际值 |
|---|---|
| `%(playlist_title)s` | 合集名称 (如 "Rust 入门教程") |
| `%(playlist_id)s` | 合集的 season_id 或 series_id |
| `%(playlist_index)s` | 视频在合集中的序号 (从1开始) |
| `%(title)s` | 单个视频标题 |
| `%(id)s` | BV号 |
| `%(uploader)s` | UP主昵称 |
| `%(uploader_id)s` | UP主 mid |
| `%(channel)s` | UP主昵称 |
| `%(channel_id)s` | UP主 mid |
| `%(upload_date)s` | 上传日期 YYYYMMDD |
| `%(description)s` | 视频简介 |
| `%(thumbnail)s` | 封面 URL |
| `%(webpage_url)s` | 视频页面 URL |

### 2.2 NFO 生成逻辑

ytdl-sub 使用 `nfo_tags` 插件系统，位于 `src/ytdl_sub/plugins/nfo_tags.py`。

**核心架构**:
- 支持两种 NFO 级别: **Episode NFO** (每个视频) 和 **Source NFO** (tvshow.nfo)
- NFO 内容完全由 YAML 配置驱动，用户可自定义任意 XML 标签
- 使用 `{source_variable}` 模板语法填充值

**配置示例**:

```yaml
nfo_tags:
  # 每个视频的 NFO
  nfo_name: "{episode_file_path}.nfo"
  nfo_root: "episodedetails"
  tags:
    title: "{title}"
    season: "{season}"
    episode: "{episode}"
    year: "{upload_year}"
    aired: "{upload_date_standardized}"
    studio: "{uploader}"
    plot: "{description}"
    uniqueid:
      attributes:
        type: "youtube"
      tag: "{youtube_id}"
  
  # TV Show 级别的 NFO
  kodi_safe: true
  source_nfo:
    nfo_name: "tvshow.nfo"
    nfo_root: "tvshow"
    tags:
      title: "{subscription_name}"
      plot: "{source_description}"
      uniqueid:
        attributes:
          type: "youtube"
        tag: "{source_id}"
```

**生成的 NFO XML 示例**:

```xml
<!-- episodedetails NFO -->
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<episodedetails>
    <title>视频标题</title>
    <season>1</season>
    <episode>5</episode>
    <year>2024</year>
    <aired>2024-01-15</aired>
    <studio>UP主昵称</studio>
    <plot>视频简介</plot>
    <uniqueid type="bilibili">BV1xxx</uniqueid>
</episodedetails>

<!-- tvshow.nfo -->
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<tvshow>
    <title>合集名称</title>
    <plot>合集简介</plot>
    <uniqueid type="bilibili">season_12345</uniqueid>
</tvshow>
```

**关键代码逻辑** (`nfo_tags.py`):

```python
class NfoTagsPlugin(Plugin):
    """
    NFO 标签生成插件
    - 支持嵌套标签 (nested tags)
    - 支持标签属性 (attributes)
    - 支持 kodi_safe 模式（确保兼容性）
    - 支持 source_nfo（生成 tvshow.nfo）
    """
    
    def _generate_nfo_xml(self, nfo_root, tags):
        """
        递归生成 NFO XML
        - 简单标签: <tag>value</tag>
        - 带属性标签: <tag type="xxx">value</tag>
        - 嵌套标签: <parent><child>value</child></parent>
        - 多值标签: 多个同名 <genre> 标签
        """
        pass
```

### 2.3 metadata 获取方式

ytdl-sub 通过以下方式获取 metadata:

1. **yt-dlp**: 主要数据源，调用 yt-dlp 下载视频并提取 metadata (info.json)
2. **内部变量映射**: 将 yt-dlp 的 metadata 字段映射为 ytdl-sub 的 `{source_variables}`
3. **Overrides**: 用户可通过 `overrides` 配置覆盖或补充 metadata

```yaml
# 示例配置
presets:
  bilibili_collection:
    overrides:
      subscription_name: "我的合集"
    
    ytdl_options:
      format: "bestvideo+bestaudio/best"
    
    # yt-dlp 的 metadata 自动映射为内部变量
    # {title} ← yt-dlp 的 title
    # {uploader} ← yt-dlp 的 uploader
    # {upload_date} ← yt-dlp 的 upload_date
```

---

## 3. BBDown

**仓库**: https://github.com/nilaoda/BBDown
**语言**: C#
**定位**: 命令行 B 站视频下载工具，支持多P、合集、番剧等

### 3.1 合集/多P视频的处理

BBDown 通过命令行参数控制多P和合集的下载行为。

**核心命令行参数**:

```bash
# 多P视频
BBDown "BV1xxx" -p ALL          # 下载所有分P
BBDown "BV1xxx" -p 1,3,5       # 下载指定分P
BBDown "BV1xxx" -p 1-5         # 下载范围分P

# 合集(视频列表)
BBDown "https://space.bilibili.com/{mid}/channel/collectiondetail?sid={season_id}"
BBDown "https://space.bilibili.com/{mid}/channel/seriesdetail?sid={series_id}"

# 直接用 season_id
BBDown "ss{season_id}"
BBDown "ep{episode_id}"
```

**多P处理逻辑** (Program.cs 伪代码):

```csharp
// 1. 解析视频信息
var videoInfo = await GetVideoInfoAsync(bvid);
var pages = videoInfo.Pages; // 多P列表

// 2. 根据 -p 参数筛选要下载的分P
var selectedPages = ParsePageSelection(pOption, pages.Count);

// 3. 遍历下载每个分P
foreach (var page in selectedPages) {
    var cid = page.Cid;
    var title = page.Title;
    
    // 获取视频流
    var playInfo = await GetPlayInfoAsync(bvid, cid);
    
    // 下载视频+音频
    await DownloadVideoAsync(playInfo.Video, outputPath);
    await DownloadAudioAsync(playInfo.Audio, outputPath);
    
    // 合并
    await MergeAsync(videoPath, audioPath, finalPath);
}
```

**合集处理逻辑**:

```csharp
// 1. 识别合集类型
if (input.Contains("collectiondetail") || input.StartsWith("ss")) {
    // Season 合集
    var seasonId = ExtractSeasonId(input);
    var videos = await GetSeasonArchivesAsync(mid, seasonId);
    
    foreach (var video in videos) {
        await DownloadSingleVideo(video.Bvid);
    }
}

if (input.Contains("seriesdetail")) {
    // Series 列表
    var seriesId = ExtractSeriesId(input);
    var videos = await GetSeriesArchivesAsync(mid, seriesId);
    
    foreach (var video in videos) {
        await DownloadSingleVideo(video.Bvid);
    }
}
```

### 3.2 目录归档逻辑

BBDown 通过 `--file-pattern` 和 `--multi-file-pattern` 参数控制输出路径。

**默认命名模板**:

```bash
# 单P视频默认模板
--file-pattern "<videoTitle>"

# 多P视频默认模板
--multi-file-pattern "<videoTitle>/[P<pageNumberWithZero>]<pageTitle>"

# 自定义示例
BBDown "BV1xxx" \
  --file-pattern "<videoTitle>_<bvid>" \
  --multi-file-pattern "<videoTitle>/<pageTitle>_P<pageNumber>"
```

**可用的模板变量**:

| 变量 | 说明 |
|---|---|
| `<videoTitle>` | 视频标题 |
| `<pageTitle>` | 分P标题 |
| `<bvid>` | BV号 |
| `<avid>` | AV号 |
| `<cid>` | CID |
| `<pageNumber>` | 分P编号 |
| `<pageNumberWithZero>` | 分P编号(补零) |
| `<ownerName>` | UP主昵称 |
| `<ownerMid>` | UP主 mid |
| `<publishDate>` | 发布日期 |
| `<dfn>` | 清晰度 |
| `<res>` | 分辨率 |
| `<fps>` | 帧率 |
| `<videoCodecs>` | 视频编码 |
| `<audioCodecs>` | 音频编码 |

**默认目录结构**:

```
./
├── 视频标题.mp4                     # 单P视频

├── 多P视频标题/                     # 多P视频
│   ├── [P01]第一集.mp4
│   ├── [P02]第二集.mp4
│   └── [P03]第三集.mp4
```

**BBDown 不生成 NFO 文件** - 它是纯下载工具，不负责媒体库组织。

---

## 4. owdevel/ytdl-nfo

**仓库**: https://github.com/owdevel/ytdl-nfo
**语言**: Python
**定位**: 将 yt-dlp 下载的视频转换为 Kodi/Jellyfin 兼容的 NFO 格式

### 4.1 完整 NFO XML 结构

ytdl-nfo 通过 YAML 配置文件定义 NFO 映射关系，源码位于 `ytdl_nfo/nfo.py`。

**YouTube 配置** (`configs/youtube.yaml`):

```yaml
# youtube.yaml - 定义 yt-dlp metadata 到 NFO 标签的映射
extractor: youtube
nfo:
  title: title           # 视频标题
  plot: description      # 视频简介
  aired: upload_date     # 上传日期
  year: upload_date      # 年份（从日期提取）
  studio: uploader       # 频道名
  id: id                 # 视频ID
  genre: categories      # 分类
  tag: tags             # 标签
  actor:
    name: channel        # 频道名
    thumb: ""            # 频道头像（需额外获取）
  ratings:
    - name: "likes"
      value: like_count
    - name: "views"
      value: view_count
```

**生成的 NFO XML 示例**:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<episodedetails>
    <title>视频标题</title>
    <plot>视频简介内容</plot>
    <aired>2024-01-15</aired>
    <year>2024</year>
    <studio>频道名称</studio>
    <uniqueid type="youtube">dQw4w9WgXcQ</uniqueid>
    <genre>Music</genre>
    <genre>Entertainment</genre>
    <tag>music video</tag>
    <tag>official</tag>
    <actor>
        <name>频道名称</name>
        <thumb>https://yt3.ggpht.com/xxxxx</thumb>
    </actor>
    <ratings>
        <rating name="likes" max="10" default="true">
            <value>1234567</value>
        </rating>
        <rating name="views" max="10" default="false">
            <value>9876543</value>
        </rating>
    </ratings>
</episodedetails>
```

### 4.2 actor/thumb 生成

ytdl-nfo 的 actor/thumb 生成逻辑位于 `nfo.py`：

```python
# nfo.py - NFO 生成核心
class NFO:
    def __init__(self, info_dict, config):
        self.info = info_dict       # yt-dlp 的 info_dict
        self.config = config        # YAML 配置
    
    def generate(self):
        root = ET.Element("episodedetails")
        
        # 处理 actor 标签
        if "actor" in self.config["nfo"]:
            actor_config = self.config["nfo"]["actor"]
            actor_elem = ET.SubElement(root, "actor")
            
            # name: 从 info_dict 中取 channel/uploader
            name = ET.SubElement(actor_elem, "name")
            name.text = self.info.get(actor_config["name"], "")
            
            # thumb: 从 info_dict 中取频道头像
            # yt-dlp 对 YouTube 会提取 channel_url → 频道头像
            # 对 bilibili，uploader_url 为 space 页面
            if actor_config.get("thumb"):
                thumb = ET.SubElement(actor_elem, "thumb")
                thumb.text = self.info.get(actor_config["thumb"], "")
        
        return ET.tostring(root, encoding="unicode", xml_declaration=True)
```

**actor/thumb 获取流程**:

1. **YouTube**: yt-dlp 会自动提取 `channel_url`，通过频道页面获取头像
2. **Bilibili**: yt-dlp 的 bilibili extractor 提供 `uploader_url` (space 页面)，但**不直接提供头像 URL**
3. ytdl-nfo 仅使用 yt-dlp info_dict 中已有的字段，**不额外调用 API**

**Bilibili 场景的限制**: yt-dlp 的 bilibili extractor 不在 info_dict 中包含 UP 主头像 URL。如果需要头像，需要：
- 额外调用 `https://api.bilibili.com/x/space/wbi/acc/info?mid={mid}` 获取
- 或从视频详情 API 的 `owner.face` 字段获取

### 4.3 Ytdl_nfo 整体工作流

```python
# Ytdl_nfo.py - 主流程
class Ytdl_nfo:
    def process(self, video_dir):
        """
        扫描目录中的 .info.json 文件，为每个视频生成 NFO
        """
        for info_json in glob(video_dir + "/*.info.json"):
            info_dict = json.load(open(info_json))
            
            # 根据 extractor 选择配置
            config = self.get_config(info_dict["extractor"])
            
            # 生成 NFO
            nfo = NFO(info_dict, config)
            nfo_content = nfo.generate()
            
            # 写入 .nfo 文件
            nfo_path = info_json.replace(".info.json", ".nfo")
            with open(nfo_path, "w") as f:
                f.write(nfo_content)
```

---

## 5. 综合对比与最佳实践

### 5.1 NFO 结构对比

| 特性 | bili-sync | ytdl-sub | ytdl-nfo |
|---|---|---|---|
| NFO 根标签 | movie/tvshow/episodedetails/person | 可配置 (episodedetails/tvshow) | episodedetails |
| actor/name | UP主 mid (数字) | 可配置 (uploader) | channel name |
| actor/role | UP主昵称 | - | - |
| actor/thumb | 头像 URL (从 API 获取) | 可配置 | 可配置 (需 info_dict 有值) |
| uniqueid | BV号 (type="bilibili") | 可配置 | 视频ID |
| genre/tags | 从 API 获取的视频标签 | 可配置 | categories/tags |
| plot | 含原始链接的 CDATA | 可配置 | description |
| premiered | 支持 favtime/pubtime 切换 | aired (upload_date) | aired |
| season/episode | 固定 S01E{pid} | 可配置 | - |

### 5.2 UP 主头像获取 - Bilibili API

**标准方式**: `https://api.bilibili.com/x/space/wbi/acc/info?mid={mid}`

```json
// 请求
GET https://api.bilibili.com/x/space/wbi/acc/info?mid=12345&w_rid=xxx&wts=xxx

// 响应
{
    "code": 0,
    "data": {
        "mid": 12345,
        "name": "UP主昵称",
        "face": "https://i1.hdslb.com/bfs/face/xxxxx.jpg",
        "sign": "个人签名",
        // ...
    }
}
```

**注意**: 此接口需要 **WBI 签名** (w_rid + wts 参数)。

**WBI 签名流程** (bili-sync 实现):

```rust
// 1. 获取 wbi_img (img_key + sub_key)
GET https://api.bilibili.com/x/web-interface/nav
// 从响应中提取 data.wbi_img.img_url 和 data.wbi_img.sub_url

// 2. 提取文件名 hash 部分，拼接后重排得到 mixin_key (取前32位)

// 3. 对请求参数按 key 排序，拼接成 query_string
// 4. 计算 md5(query_string + mixin_key) 得到 w_rid
// 5. 将 w_rid 和 wts(当前时间戳) 追加到请求参数
```

**各项目获取方式对比**:

| 项目 | 头像获取方式 |
|---|---|
| bili-sync | 从视频列表/详情 API 的 `owner.face` 字段直接获取，不单独调用 space API |
| BBDown | 不处理头像 |
| ytdl-nfo | 依赖 yt-dlp 的 info_dict，bilibili 场景下通常无头像 |
| ytdl-sub | 依赖 yt-dlp 的 info_dict，bilibili 场景下通常无头像 |

### 5.3 合集处理对比

| 项目 | 合集处理方式 |
|---|---|
| bili-sync | 原生支持 Season/Series，通过 Bilibili API 分页获取，每个视频单独补充详情 |
| ytdl-sub | 通过 yt-dlp 的 playlist extractor 处理，配置驱动 |
| BBDown | 支持合集 URL 和 ss/ep 编号，遍历下载 |
| ytdl-nfo | 后处理工具，不直接处理合集 |

### 5.4 yt-dlp 对 bilibili 合集的 output template 最佳实践

```bash
# 基本合集下载
yt-dlp "https://space.bilibili.com/{mid}/channel/collectiondetail?sid={season_id}" \
  -o "%(playlist_title)s/%(playlist_index)03d - %(title)s.%(ext)s"

# 带 UP 主组织的结构
yt-dlp "https://space.bilibili.com/{mid}/channel/collectiondetail?sid={season_id}" \
  -o "%(uploader)s/%(playlist_title)s/S01E%(playlist_index)02d - %(title)s.%(ext)s"

# 写入 metadata (用于 ytdl-nfo 后处理)
yt-dlp "https://space.bilibili.com/{mid}/channel/collectiondetail?sid={season_id}" \
  --write-info-json \
  --write-thumbnail \
  -o "%(playlist_title)s/%(playlist_index)03d - %(title)s.%(ext)s"
```

**变量在 bilibili 合集中的实际值**:

```
URL: https://space.bilibili.com/12345/channel/collectiondetail?sid=67890

%(playlist_title)s  → "Rust 入门到精通"        # 合集名称
%(playlist_id)s     → "67890"                  # season_id
%(playlist_index)s  → "1", "2", "3"...         # 视频在合集中的序号
%(playlist_count)s  → "20"                     # 合集总视频数
%(title)s           → "第1集 Hello World"       # 单个视频标题
%(id)s              → "BV1xxx"                  # BV号
%(uploader)s        → "某UP主"                  # UP主昵称
%(uploader_id)s     → "12345"                  # UP主 mid
%(upload_date)s     → "20240115"               # 上传日期
%(description)s     → "本集介绍..."             # 视频简介
%(thumbnail)s       → "http://i0.hdslb.com/..." # 封面URL
%(duration)s        → "600"                    # 时长(秒)
%(view_count)s      → "10000"                  # 播放数
%(like_count)s      → "500"                    # 点赞数
%(webpage_url)s     → "https://www.bilibili.com/video/BV1xxx"
```

**推荐的 Jellyfin/Emby 兼容 output template**:

```bash
# TV Show 风格 (推荐用于合集)
yt-dlp "{合集URL}" \
  -o "{输出根目录}/%(uploader)s - %(playlist_title)s/Season 01/S01E%(playlist_index)02d - %(title)s.%(ext)s" \
  --write-info-json \
  --write-thumbnail

# 搭配手动创建 tvshow.nfo 和 poster
```

### 5.5 目录结构对比

**bili-sync (推荐结构，Emby/Jellyfin 友好)**:

```
/media/bilibili/
├── 收藏夹名称/
│   ├── 单P视频标题/
│   │   ├── 单P视频标题.mp4
│   │   ├── 单P视频标题.nfo          # <movie>
│   │   ├── 单P视频标题-poster.jpg
│   │   └── 单P视频标题.zh-CN.default.ass
│   └── 多P视频标题/
│       ├── tvshow.nfo                # <tvshow>
│       ├── poster.jpg
│       ├── fanart.jpg
│       └── Season 1/
│           ├── base - S01E01.mp4
│           ├── base - S01E01.nfo     # <episodedetails>
│           └── base - S01E01-thumb.jpg
├── People/                           # UP 主目录
│   └── 1/
│       └── 12345/
│           ├── folder.jpg
│           └── person.nfo            # <person>
```

**BBDown (简单下载)**:

```
./
├── 视频标题.mp4
└── 多P标题/
    ├── [P01]第一集.mp4
    └── [P02]第二集.mp4
```

**ytdl-sub (高度可配置)**:

```
/media/youtube/
└── 订阅名/
    ├── tvshow.nfo
    └── Season 01/
        ├── S01E001 - 标题.mp4
        ├── S01E001 - 标题.nfo
        └── S01E001 - 标题-thumb.jpg
```

### 5.6 关键发现与建议

1. **NFO actor/name 约定不统一**: bili-sync 用 mid(数字ID)，ytdl-nfo 和 ytdl-sub 用昵称。Jellyfin 中 actor name 用于搜索和显示，建议同时填写 mid 和昵称。

2. **bilibili API 头像获取**: bili-sync 的方案最优 —— 直接从视频列表 API 取 `owner.face`，无需额外请求。独立获取可用 `/x/space/wbi/acc/info?mid=xxx` 但需要 WBI 签名。

3. **合集 season 编号**: bili-sync 固定用 `Season 1`/`S01E{pid}`，没有利用 bilibili 合集的 season 信息。如果一个 UP 主有多个合集，建议每个合集作为独立的 tvshow。

4. **弹幕**: 仅 bili-sync 支持弹幕下载 (protobuf gRPC → ASS)。BBDown 也支持弹幕但通过独立参数 `--dd`。

5. **BBDown 不生成 NFO**: 如需 NFO，需配合 ytdl-nfo 等后处理工具。

6. **ytdl-nfo 对 bilibili 支持有限**: 没有专门的 bilibili 配置文件，依赖 yt-dlp 的通用 extractor。UP 主头像在 yt-dlp 的 bilibili info_dict 中通常不可用。
