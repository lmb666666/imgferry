package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunFlagParity 断言 TUI 里可达的能力在 CLI 上都有对应参数，
// 防止两边悄悄漂移（规格 §11.6 的能力对齐测试）。
func TestRunFlagParity(t *testing.T) {
	cmd := runCmd()
	want := map[string]string{
		"站点地址":       "lsky-url",
		"Token":      "lsky-token",
		"邮箱":         "lsky-email",
		"密码":         "lsky-password",
		"存储策略":       "lsky-strategy-id",
		"相册":         "lsky-album-id",
		"并发":         "concurrency",
		"上传重试":       "upload-retries",
		"间隔":         "interval",
		"备份开关":       "no-backup",
		"清空映射表":      "fresh",
		"映射表路径":      "map",
		"扫描全部文件":     "all-files",
		"下载请求头（防盗链）": "download-header",
		"域名过滤":       "filter-img-host",
		"只预览不写文件":    "dry-run",
		"确认执行":       "yes",
	}
	for capability, flag := range want {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("能力 %q 在 CLI 上缺少参数 --%s（TUI 与 CLI 能力已漂移）", capability, flag)
		}
	}
}

// TestRootFlags 断言根命令提供 TUI 相关的开关。
func TestRootFlags(t *testing.T) {
	root := NewRootCmd()
	for _, f := range []string{"ascii"} {
		if root.Flags().Lookup(f) == nil {
			t.Errorf("根命令缺少 --%s", f)
		}
	}
	for _, sub := range []string{"run", "scan", "reset", "version"} {
		found := false
		for _, c := range root.Commands() {
			if c.Name() == sub {
				found = true
			}
		}
		if !found {
			t.Errorf("缺少子命令 %s", sub)
		}
	}
}

// TestDryRunWritesNothing 断言 dry-run 不改文件、不生成备份。
func TestDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "a.md")
	body := "![x](https://old.example.com/a.webp)\n"
	if err := os.WriteFile(md, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"run", dir, "--dry-run", "--map", filepath.Join(dir, "m.json")})
	if err := root.Execute(); err != nil {
		t.Fatalf("dry-run 应成功：%v\n%s", err, buf.String())
	}
	after, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatal("dry-run 不应修改来源文件")
	}
	if _, err := os.Stat(md + ".bak"); err == nil {
		t.Fatal("dry-run 不应生成备份")
	}
	if !strings.Contains(buf.String(), "dry-run") {
		t.Fatalf("输出应说明这是 dry-run：%s", buf.String())
	}
}

// TestRunRequiresYes 断言非交互模式不加 --yes 不写文件（安全默认）。
func TestRunRequiresYes(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"run", dir, "--lsky-url", "https://img.example.com", "--lsky-token", "1|t"})
	err := root.Execute()
	if err == nil {
		t.Fatal("未加 --yes 应报错而不是直接执行")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("错误信息应提示加 --yes：%v", err)
	}
}

// TestResolveVersion 断言版本号来源的优先级：ldflags 注入 > 模块版本 > dev。
func TestResolveVersion(t *testing.T) {
	cases := []struct{ injected, module, want string }{
		{"v1.2.3", "v9.9.9", "v1.2.3"},  // ldflags 注入优先
		{"dev", "v0.1.2", "v0.1.2"},     // go install：无 ldflags，用模块版本
		{"", "v0.1.2", "v0.1.2"},        // 同上（未设置）
		{"v1.2.3", "(devel)", "v1.2.3"}, // 本地构建带 ldflags
		{"dev", "(devel)", "dev"},       // 本地 go build：显示 dev
		{"dev", "", "dev"},              // 构建信息缺失
	}
	for _, c := range cases {
		if got := resolveVersion(c.injected, c.module); got != c.want {
			t.Errorf("resolveVersion(%q, %q) = %q，期望 %q", c.injected, c.module, got, c.want)
		}
	}
}
