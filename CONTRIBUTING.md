# 参与贡献

感谢愿意花时间改进 imgferry。这个项目对贡献者最友好的地方是：核心接口极小、所有逻辑都有测试、界面有黄金快照兜底，改动容易验证。

## 开发环境

```bash
git clone https://github.com/lmb666666/imgferry
cd imgferry
go build ./...          # 构建
go test ./...           # 全量测试（含模拟兰空服务的端到端测试）
go test -race ./...     # 竞态检测（涉及并发上传，改动并发相关代码请务必跑）
go vet ./...            # 静态检查
gofmt -l .              # 格式检查（CI 会卡这条）
```

不需要真实图床账号：所有网络相关测试都用 `httptest` 起本地服务。

## 代码结构

```
cmd/imgferry/        命令行入口
internal/scan/       文件发现 + 链接提取（按文件类型分层解析）
internal/download/   下载器（自定义请求头、重试、文件名推断）
internal/upload/     adapter 接口与注册
internal/upload/lsky/兰空 Lsky Pro adapter
internal/migrate/    编排：去重 → 下载 → 上传 → 映射表 → 替换
internal/replace/    精确替换 + 原子写 + 备份
internal/store/      映射表持久化 {旧 URL → 新 URL}
internal/config/     本地配置与运行记录（TOML / JSON）
internal/cli/        cobra 命令（run / scan / reset / version）
internal/tui/        交互界面（bubbletea；每个文件头部注释说明职责与约束）
```

## 最有价值的贡献：新增一个图床 adapter

adapter 只做一件事——把图片字节变成可公开访问的直链：

```go
type Uploader interface {
    Name() string                                                     // 标识，如 "r2"
    Validate() error                                                  // 校验配置与凭证的连通性
    Upload(filename string, data []byte, contentType string) (string, error) // 返回公开直链
}
```

步骤：

1. 在 `internal/upload/<名字>/` 下新建包，实现上述三个方法（参考 `internal/upload/lsky/lsky.go`）
2. 网络错误请区分"限流"：实现 `RateLimited() bool` 的错误类型（或包装成实现该接口的错误），调度器会据此做指数退避重试
3. 失败信息要**原样透传**服务端的 message——用户排障全靠它
4. 补测试：用 `httptest` 模拟服务端，覆盖成功、鉴权失败、限流三条路径
5. 在 `internal/cli/root.go` 里加对应参数，并更新 README 的参数表（有一个 `TestRunFlagParity` 会检查 CLI 与 TUI 的能力对齐，别忘了同步 TUI）

## 改交互界面

界面的设计意图都写在代码注释里（每个文件头部说明它负责什么、遵循什么规则）。改动时请守住两条硬约束：

- **键法一致**：`enter` 永远是主操作、`esc` 永远逐层退回、`space` 永远是切换——任何屏幕不发明新语义（`internal/tui/keys_test.go` 会检查）
- **布局不跳动**：焦点在字段间移动时，除说明区以外的内容位置必须完全不变（`internal/tui/flow_test.go` 的 `TestNoLayoutJitterOnFocus` 会检查）

界面改动后刷新黄金快照，并把快照 diff 一并提交——评审时能直接看出布局变化：

```bash
make golden    # 等价于 UPDATE_GOLDEN=1 go test ./internal/tui
```

## 提交约定

- 提交信息用中文或英文都可以，说清楚"为什么改"比"改了什么"更重要
- 一个 PR 只做一件事；较大的改动请先开 issue 讨论，避免白写
- CI 会跑 `gofmt` / `go vet` / `go test -race`，本地先跑一遍能省一轮往返

## 安全须知

迁移会**修改用户的文件**。任何涉及写盘的改动都要保证：

- 默认安全（`--dry-run` 只预览、非交互必须 `--yes`、默认备份）
- 失败项绝不替换引用
- 写文件用临时文件 + rename（`internal/replace`），不要直接覆盖

## 行为准则

对事不对人。技术分歧用测试和文档说话，别用语气。
