// Package cli 组装 imgferry 的命令行：无参数进 TUI 向导，带参数走非交互模式。
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lmb666666/imgferry/internal/migrate"
	"github.com/lmb666666/imgferry/internal/scan"
	"github.com/lmb666666/imgferry/internal/store"
	"github.com/lmb666666/imgferry/internal/tui"
	"github.com/lmb666666/imgferry/internal/upload"
	"github.com/lmb666666/imgferry/internal/upload/lsky"
)

// version 由构建时 -ldflags 注入；未注入时（例如 go install <模块>@<版本>）
// 从二进制里记录的构建信息取模块版本，保证 imgferry version 能报出真实版本。
var version = "dev"

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		version = resolveVersion(version, info.Main.Version)
	}
}

// resolveVersion 选择要展示的版本号：ldflags 注入优先，其次模块版本，
// "(devel)"/空 表示本地源码构建，保持 "dev"。
func resolveVersion(injected, moduleVersion string) string {
	if injected != "" && injected != "dev" {
		return injected
	}
	if moduleVersion != "" && moduleVersion != "(devel)" {
		return moduleVersion
	}
	return "dev"
}

// Execute 是命令行入口。
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// out 返回命令的输出目标（测试可重定向）。
func out(cmd *cobra.Command) io.Writer { return cmd.OutOrStdout() }

// NewRootCmd 创建根命令。
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "imgferry",
		Short: "批量迁移图床图片并替换文件中的链接",
		Long: `imgferry 扫描文件里的图片链接，下载后上传到新图床（兰空 Lsky Pro），
并把文件中的旧链接替换为新地址；重跑幂等、支持断点续传与备份。

不带参数启动交互界面；带参数直接执行，适合脚本和 CI。`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ascii, _ := cmd.Flags().GetBool("ascii")
			return tui.Run(version, ascii)
		},
	}
	root.Flags().Bool("ascii", false, "用 ASCII 图标渲染界面（终端字体不支持 Unicode 时）")
	root.AddCommand(versionCmd(), scanCmd(), runCmd(), resetCmd())
	return root
}

func resetCmd() *cobra.Command {
	var mapPath string
	var yes bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "清空 URL 映射表（下次迁移将重新上传全部链接）",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := store.Load(mapPath)
			if err != nil {
				return err
			}
			n := len(m.Entries)
			if n == 0 {
				fmt.Fprintln(out(cmd), "映射表已是空的，无需清空")
				return nil
			}
			if !yes {
				fmt.Fprintf(out(cmd), "将清空 %s 中的 %d 条映射记录，确认？[y/N] ", mapPath, n)
				answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				answer = strings.ToLower(strings.TrimSpace(answer))
				if answer != "y" && answer != "yes" {
					fmt.Fprintln(out(cmd), "已取消")
					return nil
				}
			}
			m.Clear()
			if err := m.Save(); err != nil {
				return err
			}
			fmt.Fprintf(out(cmd), "已清空 %d 条映射记录\n", n)
			return nil
		},
	}
	cmd.Flags().StringVar(&mapPath, "map", "migrate-map.json", "映射表路径")
	cmd.Flags().BoolVar(&yes, "yes", false, "跳过确认")
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("imgferry", version)
		},
	}
}

func scanCmd() *cobra.Command {
	var domains string
	var allFiles bool
	cmd := &cobra.Command{
		Use:   "scan <路径>",
		Short: "扫描文件/目录，列出其中的图片链接",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var list []string
			if domains != "" {
				list = strings.Split(domains, ",")
			}
			exts := scan.DefaultExts
			if allFiles {
				exts = scan.ExtsAll
			}
			res, err := scan.Scan(args[0], exts, list)
			if err != nil {
				return err
			}
			fmt.Fprintf(out(cmd), "文件 %d 个，链接 %d 处，去重 URL %d 个\n",
				res.Files, len(res.Links), len(res.UniqueURLs()))
			for _, l := range res.Links {
				fmt.Fprintf(out(cmd), "  %s:%d  [%s] %s\n", l.File, l.Line, kindLabel(l.Kind), l.URL)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&domains, "filter-img-host", "",
		"只显示这些域名的链接（逗号分隔）")
	cmd.Flags().BoolVar(&allFiles, "all-files", false,
		"扫描全部文件（不限扩展名，自动跳过二进制与超大文件）")
	return cmd
}

func kindLabel(kind string) string {
	switch kind {
	case scan.KindImage:
		return "图片"
	default:
		return "链接"
	}
}

func runCmd() *cobra.Command {
	var (
		to           string
		lskyURL      string
		lskyToken    string
		lskyEmail    string
		lskyPassword string
		strategyID   int
		albumID      int
		domains      string
		concurrency  int
		retries      int
		interval     time.Duration
		headers      []string
		mapPath      string
		allFiles     bool
		dryRun       bool
		yes          bool
		noBackup     bool
		fresh        bool
	)
	cmd := &cobra.Command{
		Use:   "run <路径>",
		Short: "执行迁移：扫描、下载、上传、替换链接",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !dryRun && !yes {
				return fmt.Errorf("本次执行会修改文件并上传图片：先加 --dry-run 预览，确认后加 --yes 执行")
			}
			hdr, err := parseHeaders(headers)
			if err != nil {
				return err
			}
			var domainList []string
			if domains != "" {
				domainList = strings.Split(domains, ",")
			}

			var up upload.Uploader
			if !dryRun {
				if to != "lsky" {
					return fmt.Errorf("暂不支持目标图床 %q（当前仅支持 lsky）", to)
				}
				a, err := lsky.New(lsky.Config{
					BaseURL:    lskyURL,
					Token:      lskyToken,
					Email:      lskyEmail,
					Password:   lskyPassword,
					StrategyID: strategyID,
					AlbumID:    albumID,
				})
				if err != nil {
					return err
				}
				up = a
			}

			rep, err := migrate.Run(cmd.Context(), migrate.Options{
				Root:             args[0],
				Domains:          domainList,
				AllFiles:         allFiles,
				Uploader:         up,
				MapPath:          mapPath,
				DownloadHeaders:  hdr,
				Concurrency:      concurrency,
				MaxUploadRetries: retries,
				Interval:         interval,
				NoBackup:         noBackup,
				FreshMap:         fresh,
				DryRun:           dryRun,
				OnLog:            func(f string, a ...any) { fmt.Fprintf(out(cmd), "· %s\n", fmt.Sprintf(f, a...)) },
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(out(cmd))
			fmt.Fprintln(out(cmd), "—— 迁移结果 ——")
			fmt.Fprintln(out(cmd), rep.Summary())
			return nil
		},
	}

	cmd.Flags().StringVar(&to, "to", "lsky", "目标图床（当前仅 lsky）")
	cmd.Flags().StringVar(&lskyURL, "lsky-url", "", "兰空站点地址，如 https://img.example.com")
	cmd.Flags().StringVar(&lskyToken, "lsky-token", "", "兰空 token（个人页生成）")
	cmd.Flags().StringVar(&lskyEmail, "lsky-email", "", "兰空邮箱（token 留空时用于换取）")
	cmd.Flags().StringVar(&lskyPassword, "lsky-password", "", "兰空密码（同上，不落盘）")
	cmd.Flags().IntVar(&strategyID, "lsky-strategy-id", 0, "存储策略 ID（可选）")
	cmd.Flags().IntVar(&albumID, "lsky-album-id", 0, "相册 ID（可选）")
	cmd.Flags().StringVar(&domains, "filter-img-host", "", "只处理这些域名的链接（逗号分隔）")
	cmd.Flags().IntVar(&concurrency, "concurrency", 2, "上传并发数")
	cmd.Flags().IntVar(&retries, "upload-retries", 2, "站点限流时的退避重试次数（0 关闭）")
	cmd.Flags().DurationVar(&interval, "interval", 0, "相邻上传的最小间隔，如 1s（应对图床频控）")
	cmd.Flags().StringArrayVar(&headers, "download-header", nil, "下载旧图的额外请求头，如 \"Referer: https://old.host/\"（可重复）")
	cmd.Flags().StringVar(&mapPath, "map", "migrate-map.json", "URL 映射表路径")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只预览计划，不做任何修改")
	cmd.Flags().BoolVar(&yes, "yes", false, "确认执行（非交互模式必须显式指定）")
	cmd.Flags().BoolVar(&noBackup, "no-backup", false, "关闭替换前备份（默认生成 *.bak）")
	cmd.Flags().BoolVar(&fresh, "fresh", false, "清空映射表，忽略历史记录重新迁移")
	cmd.Flags().BoolVar(&allFiles, "all-files", false, "扫描全部文件（不限扩展名）")
	return cmd
}

func parseHeaders(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	h := make(map[string]string, len(raw))
	for _, s := range raw {
		k, v, ok := strings.Cut(s, ":")
		if !ok {
			return nil, fmt.Errorf("请求头格式应为 \"Key: Value\"，收到 %q", s)
		}
		h[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return h, nil
}
