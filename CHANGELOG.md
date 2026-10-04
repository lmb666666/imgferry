# Changelog

本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [0.1.2] - 2026-10-04

- `imgferry version` 在 `go install github.com/lmb666666/imgferry@<版本>` 这类不带 ldflags 的构建里，
  改为从二进制构建信息读取模块版本（之前会显示 `dev`），便于报障时核对版本

## [0.1.1] - 2026-10-04

- 修正安装路径：命令行入口移到模块根，`go install github.com/lmb666666/imgferry@latest` 现在可以直接用（v0.1.0 需要写成 `.../imgferry/cmd/imgferry@v0.1.0`）。功能与 v0.1.0 一致

## [0.1.0] - 2026-10-04

首个可用版本：把存量文章里的旧图床链接迁移到兰空 Lsky Pro，全流程安全可控。

**核心能力**

- 扫描：目录或单文件，默认覆盖 50 种文本扩展名（md/html/txt/源码/配置），`--all-files` 可扫任意文本文件（自动跳过二进制与超大文件）
- 提取：Markdown（goldmark AST，支持引用式图片与括号嵌套）、HTML `<img>`（含跨行标签、`srcset`、`data-original` 懒加载）、CSS `url()`、front matter 的 `cover:`/`image:`、裸 URL；音视频链接不处理
- 迁移：URL 级去重（同一张图只上传一次）、并发上传、站点限流自动退避重试、域名与后缀过滤
- 安全：dry-run 预览、`migrate-map.json` 幂等续传、原子写 + `*.bak` 备份、失败项不替换并给出原因
- 交互界面：六步向导（来源 → 选择 → 目标 → 预检 → 迁移 → 结果）+ 首页；写盘前预检确认；扫描与迁移都可取消；失败项可单独重试；内置"测试连接"并可直接下拉选择存储策略
- CLI：`run` / `scan` / `reset` / `version`，非交互模式必须显式 `--yes`

**图床支持**

- 迁入：兰空 Lsky Pro（开源 V1.x / V2.x / Pro+ 的 `/api/v1` 兼容接口），已对源码与官方文档核对
- 迁出：任意可通过 http(s) 直链下载的旧图床（支持自定义 Referer / Cookie / User-Agent 应对防盗链）

**已知限制**

- 暂无其他去向 adapter（GitHub 图床、Cloudflare R2、S3 兼容在路线图上）
- 迁移报告文件与新 URL 可达性校验（HTTP HEAD）尚未提供
- 尚未在 Windows 上实测（代码走标准库网络与文件接口，理论上可用）
