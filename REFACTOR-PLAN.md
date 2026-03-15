# 重构方案

## 核心设计变更

### 1. ✅ 放弃 yt-dlp 的 playlist 管理，自己管理视频列表
**原因**: yt-dlp 的 --flat-playlist 对 B 站合集的处理不可靠
**方案**: 直接调用 bilibili API 获取 UP 主视频列表和合集信息
**完成**: 迭代 #2

### 2. ✅ bilibili API 集成（参考 bili-sync）
- `x/space/wbi/arc/search` → UP 主投稿视频列表
- `x/web-interface/view` → 视频详情
- `x/polymer/web-space/seasons_archives_list` → 合集视频列表
- `x/space/wbi/acc/info` → UP 主个人信息
- 视频下载使用 bilibili DASH API + ffmpeg（替代 yt-dlp）
**完成**: 迭代 #2, #3

### 3. ✅ 目录结构（参考 bili-sync）
```
downloads/
  {UP主名}/
    {视频标题}/
      {视频标题}.mp4
      {视频标题}.nfo
      {视频标题}-thumb.jpg
  {UP主名}/
    {合集名}/
      tvshow.nfo
      poster.jpg
      Season 1/
        {子视频标题}.mp4
        {子视频标题}.nfo
        {子视频标题}-thumb.jpg
```
**完成**: 迭代 #2

### 4. ✅ NFO 格式（参考 bili-sync nfo.rs）
- 单视频 → `<movie>` 标签
- 合集 → `<tvshow>` + `<episodedetails>`
- UP 主 People 目录 → `<person>`
**完成**: 迭代 #2, #5

### 5. ✅ 下载流程重构
```
1. Scheduler 定时检查每个 Source
2. 调 bilibili API 获取视频列表
3. 对比数据库，找出新视频
4. 对于每个新视频：
   a. 调 bilibili API 获取视频详情
   b. 保存到数据库
   c. 使用 bilibili DASH API + ffmpeg 下载
   d. 下载完成后生成 NFO
   e. 下载封面图
   f. 生成/更新 People 目录
```
**完成**: 迭代 #2~#5

### 6. ✅ 模块职责
- **bilibili/** — API 调用（视频列表、详情、DASH 流、WBI 签名、Cookie 验证）
- **downloader/** — DASH 下载 + ffmpeg 合并 + 进度推送
- **nfo/** — NFO XML 生成
- **scheduler/** — 定时检查 + 调度
- **scanner/** — 扫描本地文件 + 数据库对账
- **danmaku/** — 弹幕下载（XML → ASS）
- **logger/** — Ring buffer 日志 + SSE 流
- **db/** — 数据库 CRUD
- **web/** — Web UI + API
**完成**: 迭代 #1~#10

## 修复清单

### P0 同步修复
1. [x] bilibili API 获取视频列表（替代 --flat-playlist）
2. [x] bilibili API 获取 UP 主头像
3. [x] 合集识别和归档
4. [x] NFO 正确生成（每个视频同名 .nfo）
5. [x] 删除源级联删除下载记录
6. [x] 清理功能正常工作

### P1 补充
7. [x] 封面图下载
8. [x] People 目录生成
9. [x] 弹幕下载（XML → ASS 转换）

## 下一阶段待做
- [ ] 多 P 分片视频下载支持
- [ ] 字幕下载（CC 字幕）
- [ ] 下载队列优先级管理
- [ ] 存储空间监控和告警

### 7. ✅ 分块并行下载
- 大文件（>50MB）自动拆分多块并行下载
- HEAD 请求探测 Content-Length，回退用 Range 探测
- 每块独立重试（最多3次），不影响其他块
- 下载完成后合并为完整文件
- 分块数可通过 settings 配置（download_chunks，默认4）
- 集成限速（总限速平分给各块）
**完成**: 迭代 #11 (v2.1.0)
