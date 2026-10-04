# imgferry

> 把存量文章里的旧图床图片，批量搬到新图床。

**imgferry** 扫描 Markdown / HTML / 源码文件里的旧图床链接，下载图片、上传到新图床（目前支持**兰空 Lsky Pro**），再把文件里的链接替换成新地址。全流程安全可控：写盘前预览确认、映射表幂等续传、默认生成备份、失败项给出原因并可单独重试。

```
⚓ imgferry ─ ✓ 来源 ─ ▍选择 ─ ○ 目标 ─ ○ 预检 ─ ○ 迁移 ─ ○ 结果
┌ 图片清单 ─ 域名分组 ──────────────────────────────────────────────┐
│ ▸ ▾ ▣ bu.dusays.com                                      3 张   │
│     ▣ 2026/08/29/6a9299c0be2ef.webp                      1 处   │
│   ▾ ▣ free.picui.cn                                      1 张   │
└──────────────────────────────────────────────────────────────────┘
┌ 详情 ─────────────────────────────────────────────────────────────┐
│ bu.dusays.com/2026/08/29/6a9299c0be2ef.webp          已有映射      │
│ 引用 3 处 · ./posts/hello.md:12 · ./posts/world.md:88              │
└──────────────────────────────────────────────────────────────────┘
  已选 54/62 张 · 5 个域名 · 18 个文件 · 6 张已有映射（跳过上传）
  space 选择 · a 全选 · / 过滤 · v 分组 · enter 下一步 · ? 帮助
```

**English at a glance** — imgferry migrates images that your Markdown/HTML/source files reference on an old image host over to a new host (currently Lsky Pro), then rewrites the links. It ships a wizard-style TUI and a scriptable CLI, with dry-run preview, an idempotent URL map for resume, atomic writes with `.bak` backups, per-item failure reasons, and retry-failures-only.

[![CI](https://github.com/lmb666666/imgferry/actions/workflows/ci.yml/badge.svg)](https://github.com/lmb666666/imgferry/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/lmb666666/imgferry)](https://goreportcard.com/report/github.com/lmb666666/imgferry)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## 为什么需要它

PicGo / PicList 这类工具解决的是"新图片往哪传"（增量上传）；而**存量迁移**——图床跑路、白嫖图床开始收费、GitHub + jsDelivr 访问不了、想把散落在各处的图收拢到自建站点——一直没有趁手的工具。手写脚本的常见问题是一次性、不可复用、没有安全网，每换一次图床就要重写一遍。

imgferry 补的就是这块：一条命令扫描出所有旧图床链接，预览确认后批量迁移；出问题可以按映射表回查、续传、重跑。

## 功能

**扫描与提取**

- 目录或单个文件，递归查找；默认覆盖 50 种文本扩展名（md / markdown / html / txt / 常见源码与配置），`--all-files` 可扫描任意文本文件（按 NUL 字节跳过二进制、跳过 8 MB 以上文件，排除 `*.bak` 与映射表）
- 按文件类型分层提取：Markdown 走 CommonMark 解析器（引用式图片、括号嵌套地址）；HTML 覆盖 `<img src>`（含属性跨行）、`srcset`、`data-original` 等懒加载属性；CSS 覆盖 `url()`；front matter 的 `cover:` / `image:`；其余文件按裸 URL 兜底
- 音视频链接（mp3 / mp4 等）直接跳过——本工具只迁移图片
- 支持按域名过滤（`--filter-img-host`）与按链接后缀过滤，避免误迁无关图片

**迁移**

- **URL 级去重**：同一张图被 10 处引用，只下载上传 1 次，替换 10 处
- 下载支持自定义 `Referer` / `Cookie` / `User-Agent`，应对防盗链
- 上传并发可调（默认 2，兰空按用户组限流）；遇到站点限流自动退避重试（默认 2 次，`--upload-retries` 可调）
- 扫描与迁移都可随时取消：扫描取消立即停止读盘；迁移取消会**跳过替换阶段**（来源文件保持原样），已上传的写入映射表，重跑自动续传

**安全**

- `--dry-run` 只预览不落盘；非交互模式不加 `--yes` 不写任何文件
- `migrate-map.json` 记录每个旧地址到新地址的映射：重跑幂等、中断续传、可回查
- 替换前默认生成 `*.bak` 备份（只保留首次），写入用临时文件 + rename 原子完成
- 失败的图片**不会**替换其引用，失败原因完整列出

**交互界面**（不带参数启动）

- 六步向导：来源 → 选择 → 目标 → 预检 → 迁移 → 结果，另有首页（继续上次 / 只重试上次失败项 / 清空映射表）
- 选择屏按域名 / 目录 / 文件分组浏览，三态勾选、折叠、过滤；选中项的注释显示在列表下方
- 目标屏内置「测试连接」：校验站点与凭据后自动拉取存储策略，直接下拉选择（不必再手工查策略 ID）
- **预检屏**是写盘前唯一的确认点：上传多少、复用多少、改哪些文件、是否备份，全部列清；关闭备份时会变红并要求二次确认
- 失败项可只重试失败项，并按原因给出处置建议（防盗链 → 填 Referer；限流 → 加间隔）

## 安装

需要 Go 1.24.2 或更高版本（依赖 Charm 生态的 TUI 库）：

```bash
go install github.com/lmb666666/imgferry@latest
```

从源码构建：

```bash
git clone https://github.com/lmb666666/imgferry
cd imgferry
go build -o imgferry .
```

Homebrew 与预编译二进制在计划中。

## 快速开始

仓库里的 [`testdata/sample.md`](testdata/sample.md) 是一份包含各种引用写法的示例文件，可以直接拿来试：

```bash
# 1. 先看有哪些图片链接（不联网、不写文件）
imgferry scan testdata/sample.md

# 2. 预览迁移计划（只列计划，不做任何修改）
imgferry run testdata/sample.md \
  --lsky-url https://img.example.com \
  --lsky-token '1|xxxxxxxx' \
  --dry-run

# 3. 确认无误后执行（会修改文件，必须显式加 --yes）
imgferry run ./posts \
  --lsky-url https://img.example.com \
  --lsky-token '1|xxxxxxxx' \
  --filter-img-host bu.dusays.com \
  --interval 1s \
  --yes
```

中断后重新执行同一条命令即可续传；想全部重新迁移，加 `--fresh`（或先 `imgferry reset` 清空映射表）。

### 交互界面

不带参数运行 `imgferry` 进入向导（终端字体不支持 Unicode 时加 `--ascii`）：

| 步骤 | 做什么 | 主操作 |
|---|---|---|
| 首页 | 新建迁移 / 继续上次 / 只重试上次失败项 / 清空映射表；提示 git 状态 | `enter` |
| ① 来源 | 填扫描路径（含文件类型与后缀过滤），路径即时校验 | `enter` 开始扫描 |
| ② 选择 | 分组浏览、三态勾选、过滤；列表下方显示选中项详情 | `enter` 下一步 |
| ③ 目标 | 站点地址与凭据；`t` 测试连接后可直接下拉选存储策略 | `enter` 下一步 |
| ④ 预检 | 写盘前确认影响面（关闭备份需二次确认） | `enter` 开始迁移 |
| ⑤ 迁移 | 进度 + 逐项事件流（含失败原因与处置建议） | `esc` 取消 |
| ⑥ 结果 | 统计读数 + 失败明细 | `r` 只重试失败项 |

键法全屏一致，任何屏幕不发明新语义：

| 键 | 作用 |
|---|---|
| `enter` | 主操作（开始扫描 / 下一步 / 开始迁移） |
| `esc` | 逐层退回：关覆盖层 → 退出过滤编辑 → 清除过滤 → 上一屏 |
| `space` | 列表里勾选（分组行 = 整组三态）；表单里切换开关、打开选项 |
| `↑↓` `j` `k` `pgup` `pgdn` `g` `G` | 移动、翻页、首尾 |
| `tab` / `shift+tab` | 表单字段间移动；`←` `→` 切换选项、数字 ±1（数字也可直接键入） |
| `/` | 过滤（`enter` 保留、`esc` 清除） |
| `v` `z` `Z` | 切换分组维度（域名 / 目录 / 文件 / 平铺）、折叠当前组 / 全部 |
| `a` `u` `i` `y` | 全选可见 / 反选可见 / 完整详情 / 复制 URL |
| `t` `d` `e` `r` | 测试连接 / 只导出计划 / 修改来源 / 只重试失败项 |
| `?` `q` `ctrl+c` | 帮助 / 退出 / 强制中断（迁移中先取消） |
| `1`–`4` | 跳到已到达的步骤（迁移与结果必须经预检进入） |

界面在 40×12 起可用，窗口更大时布局不变、只是留白更多。配置保存在 `~/.config/imgferry/config.toml`（0600，密码不落盘），上次运行结果存在同目录的 `state.json`（不含密钥）。

## 兰空 Token 的获取

| 兰空版本 | 获取方式 |
|---|---|
| 个人页有生成入口（开源 V1.x / Pro+） | 直接粘贴到 `--lsky-token` |
| 开源 V2.x（后台没有生成入口） | 留空 token，填写 `--lsky-email` 与 `--lsky-password`，工具会调用 `POST /api/v1/tokens` 换取（该接口限流 3 次/分钟）。交互界面里可以把凭据方式切成"邮箱 + 密码" |
| 第三方托管的兰空站 | 取决于站点部署的版本，建议先填 token 试试 |

## 常用参数

| 参数 | 说明 | 默认 |
|---|---|---|
| `--filter-img-host` | 只处理这些域名的链接（逗号分隔） | 处理所有域名 |
| `--all-files` | 扫描全部文件，不限扩展名 | 关 |
| `--concurrency` | 上传并发数 | 2 |
| `--upload-retries` | 站点限流时的退避重试次数 | 2 |
| `--interval` | 相邻上传的最小间隔（如 `1s`），站点限流时使用 | 0 |
| `--download-header` | 下载旧图的额外请求头，可重复，如 `--download-header "Referer: https://old.host/"` | — |
| `--map` | 映射表路径 | `./migrate-map.json` |
| `--lsky-url` / `--lsky-token` | 兰空站点地址与 token | — |
| `--lsky-email` / `--lsky-password` | 用邮箱密码换取 token（不落盘） | — |
| `--lsky-strategy-id` / `--lsky-album-id` | 上传到指定存储策略 / 相册 | 0 |
| `--fresh` | 清空映射表，全部重新迁移 | 关 |
| `--no-backup` | 关闭替换前备份 | 默认生成 `*.bak` |
| `--dry-run` | 只预览不执行 | — |
| `--yes` | 确认执行（非交互模式必须显式指定） | — |
| `--to` | 目标图床（当前仅 `lsky`，是新增 adapter 的接入点） | `lsky` |
| `--ascii` | 用 ASCII 图标渲染界面（全局参数，放在命令前） | 关 |

## 工作原理

```
扫描文件 → 提取链接 → 域名/后缀过滤 → URL 去重
   → 下载旧图（可带 Referer/Cookie） → 上传新图床（并发 + 限流退避）
   → 写入映射表 {旧地址 → 新地址} → 批量替换链接（原子写 + 备份）→ 汇总
```

映射表是整个工具的地基：所有替换都基于持久化的 `{旧 → 新}` 映射，而不是边传边改。由此天然获得几项能力——**幂等**（重跑跳过已迁移）、**断点续传**（中断后重跑接着来）、**可回查**（哪张图换到哪去了）。每个被修改的文件默认留一份 `*.bak`，确认无误后删除即可。

## 故障排查

**全部上传失败，提示"服务异常，请稍后再试"**

兰空服务端在上传时抛了异常，最常见原因是该站点的默认存储策略不可用（第三方兰空站常见，实测 PicUI 如此）。交互界面里按 `t` 测试连接后可直接下拉选策略；命令行先查站点有哪些策略：

```bash
curl https://你的兰空地址/api/v1/strategies
# PicUI（v2.picui.cn）返回 8=游客储存
```

然后加 `--lsky-strategy-id 8` 重跑。失败的运行不会修改任何文件，直接重跑即可。

**其他常见失败**

| 现象 | 原因与处置 |
|---|---|
| `下载 …（HTTP 403）` | 旧站有防盗链：加 `--download-header "Referer: https://旧站/"`（必要时再补 Cookie） |
| `操作频繁` / HTTP 429 | 站点限流：加 `--interval 2s`、把 `--concurrency` 降到 1 后重跑 |
| `Unauthenticated` | token 无效或过期，重新生成 |
| `授权失败` | token 与站点不匹配，检查 `--lsky-url` 是否为站点根地址 |
| `下载 …（HTTP 404）` | 旧图已失效，无法迁移，其引用保持原样 |

迁移中途取消或中断：来源文件未被修改，已上传的记录在映射表里，重跑同一条命令即可续传——界面首页也有「继续上次」与「只重试上次失败的 N 项」。

## 参与贡献

欢迎 issue 与 PR。最有价值的贡献是**新增一个图床 adapter**：接口只有三个方法（`Name` / `Validate` / `Upload`），把图片字节变成可公开访问的直链即可——每加一个去向，"任意旧图床 → 你的图床"就多一条通路。开发方式、代码结构、界面改动的约束见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 已知限制与路线图

- [x] 兰空 Lsky Pro（走 `/api/v1` 兼容接口，开源 V1.x / V2.x 已按源码核对）
- [ ] 更多去向：GitHub 图床、Cloudflare R2、S3 兼容（OSS / COS / 七牛）、SM.MS
- [ ] 迁移报告文件（markdown / JSON）与新 URL 可达性校验（HTTP HEAD）
- [ ] 兰空 Pro+ 新版接口自动探测（当前依赖其 `/api/v1` 兼容层）
- [ ] 本地相对路径图片、Base64 内嵌图
- [ ] Homebrew 与安装脚本；预编译二进制

## License

[MIT](LICENSE)
