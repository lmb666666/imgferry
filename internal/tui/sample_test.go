package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmb666666/imgferry/internal/config"
	"github.com/lmb666666/imgferry/internal/scan"
)

// sampleDir 保存 sampleSession 创建的真实临时目录（供需要真实扫描的用例）。
var sampleDir string

// sampleSession 造一份可复现的会话：临时目录里放两个 md 文件与若干图床链接。
// 路径统一改写成稳定的相对形式，避免快照受随机临时目录名影响。
func sampleSession(t *testing.T) *Session {
	t.Helper()
	// 首页的 git 提示随运行环境变化（是否仓库、有无未提交改动），
	// 这里固定成一段文案，保证快照在任何机器、任何仓库状态下都一致。
	old := gitAdviceFn
	gitAdviceFn = func(string) string {
		return "当前目录不是 git 仓库 · 替换前无法用 git diff 复核（已默认开启 .bak 备份）"
	}
	t.Cleanup(func() { gitAdviceFn = old })
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	sampleDir = dir
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/hello.md", "# 你好\n\n![a](https://bu.dusays.com/2026/08/29/6a9299c0be2ef.webp)\n"+
		"![b](https://bu.dusays.com/2026/08/29/6a9294a206293.webp)\n"+
		"![c](https://free.picui.cn/free/20261003/74a6fc3dba4e8f471c5432a6e2e94874.webp)\n")
	write("posts/world.md", "![d](https://bu.dusays.com/2026/08/29/6a9294a206294.webp)\n"+
		"<img src=\"https://img.example.com/shot.png\">\n")
	write("README.md", "![e](https://img.example.com/shot.png)\n")

	res, err := scan.Scan(dir, scan.DefaultExts, nil)
	if err != nil {
		t.Fatal(err)
	}
	prefix := dir + string(filepath.Separator)
	for i := range res.Links {
		res.Links[i].File = "./" + strings.TrimPrefix(res.Links[i].File, prefix)
	}

	sess := newSession(config.Default())
	sess.SourcePath = "./posts"
	sess.Scan = res
	sess.Exts = scan.DefaultExts
	sess.Tree = newCheckTree(selectImageLinks(res.Links, ""))
	sess.Tree.SetAll(true)
	sess.Step, sess.Reached = stepSelect, stepReport
	sess.Target = TargetConfig{
		Type: "lsky", BaseURL: "https://v2.picui.cn", Token: "1|test-token-not-real",
		CredMode: "token", Concurrency: 2, Interval: "1s", Backup: true,
		MapPath: "migrate-map.json",
	}
	return sess
}
