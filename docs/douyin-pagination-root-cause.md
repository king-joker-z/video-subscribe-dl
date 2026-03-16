# 抖音翻页 status_code=0 空列表 — 根因分析报告

## 结论

**翻页需要登录态 Cookie。未登录只能获取第一页（cursor=0）的数据。**

这是抖音服务端+前端双重限制，不是 bug，不是签名/参数/IP 问题。

## 验证证据

### 证据 1：Playwright 浏览器实测

用 headless Chromium 以**未登录**状态访问用户主页并模拟滚动：

| 行为 | 结果 |
|------|------|
| 页面加载 | 获得 31 个 Cookie（均为匿名 Cookie） |
| API 调用次数 | **仅 1 次**（cursor=0） |
| aweme_list | 18 条 |
| has_more | true |
| 滚动 8 次后视频数 | **始终 26 个**，未触发第二页加载 |

**浏览器前端 JS 检测到未登录，直接不发起翻页请求。**

### 证据 2：绕过前端直接调 API

用 f2 库（真实 msToken + ttwid）直接调用 API：

| cursor 值 | 结果 |
|-----------|------|
| 0（第一页） | ✅ 返回 20 条，has_more=1 |
| 0（重复 3 次） | ✅ 每次都返回 20 条 |
| 1 | ❌ `{"status_code":0}` 空列表 |
| 1000 | ❌ `{"status_code":0}` 空列表 |
| 1772117127000（真实 cursor） | ❌ `{"status_code":0}` 空列表 |

**任何 cursor != 0 的请求全部返回空，与 cookie 来源无关。**

### 证据 3：f2 官方文档明确说明

**f2 FAQ (https://f2.wiki/faq)：**
> "只要是出现第 n 次请求响应内容为空均是 cookie 设置的问题。"
> "完整的网页端 douyin 的 cookie 有超过 60 个键。如果你获取的 cookie 长度过短，那明显是无法正常使用的。"

**f2 CLI 文档 (https://f2.wiki/guide/apps/douyin/cli)：**
> `--cookie`: "登录后的 Cookie。大部分接口需要登录后才能获取数据，所以需要提供登录后的 Cookie。"

### 证据 4：Cookie 数量对比

| 来源 | Cookie 数量 | 翻页 |
|------|------------|------|
| Go 代码合成 | 6 个 | ❌ |
| headless 浏览器未登录 | 7-31 个 | ❌ |
| f2 要求的完整登录 Cookie | 60+ 个 | ✅ |

## 已排除的因素

| 假设 | 排除依据 |
|------|---------|
| msToken 格式（107 vs 184 字符） | f2 的真实 184 字符 msToken 也翻页失败 |
| a_bogus / X-Bogus 签名 | status_code=0 说明签名通过 |
| IP 地理限制 | cursor=0 在 HK IP 上 3/3 成功 |
| 请求频率限制 | cursor=0 连续 3 次都成功 |
| Cookie 不一致 | 完全相同 Cookie 发两次也失败 |
| 参数差异 | 两页请求除 max_cursor 外完全一致 |

## 修复方案

### 方案 A：支持用户提供浏览器 Cookie（推荐）

配置文件增加 browser_cookie 字段，用户从已登录 Douyin 浏览器 DevTools 复制完整 Cookie。

### 方案 B：仅抓首页 + 增量更新

接受只能获取最新 ~20 条的限制，通过高频轮询实现增量覆盖。适合"订阅新视频"场景。

### 方案 C：自动获取登录 Cookie

参考 f2 的 --auto-cookie，从本地浏览器数据库读取。复杂度高。

---
*调研时间: 2026-03-16*
