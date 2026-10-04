package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	// 重定向配置路径到临时目录
	old := configDirOverride
	configDirOverride = t.TempDir()
	defer func() { configDirOverride = old }()

	c := Default()
	c.Target.BaseURL = "https://v2.picui.cn"
	c.Target.Token = "1|abc"
	c.Target.StrategyID = 8
	c.Target.Backup = false
	c.Target.FreshMap = true
	c.LastSourcePath = "./posts"
	if err := Save(c); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Target.BaseURL != "https://v2.picui.cn" ||
		got.Target.Token != "1|abc" ||
		got.Target.StrategyID != 8 ||
		got.Target.Backup ||
		!got.Target.FreshMap ||
		got.LastSourcePath != "./posts" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if !got.HasTarget() {
		t.Fatal("HasTarget should be true")
	}
}

func TestLoadMissing(t *testing.T) {
	old := configDirOverride
	configDirOverride = filepath.Join(t.TempDir(), "nonexistent")
	defer func() { configDirOverride = old }()

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Target.Backup || c.Target.Concurrency != 2 || c.Target.Type != "lsky" {
		t.Fatalf("defaults mismatch: %+v", c)
	}
	if c.HasTarget() {
		t.Fatal("fresh config should have no target")
	}
}

func TestFilePermission(t *testing.T) {
	dir := t.TempDir()
	old := configDirOverride
	configDirOverride = dir
	defer func() { configDirOverride = old }()

	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", info.Mode().Perm())
	}
}

// TestPasswordNotPersisted 密码只在会话内使用，绝不写入配置文件。
func TestPasswordNotPersisted(t *testing.T) {
	dir := t.TempDir()
	old := configDirOverride
	configDirOverride = dir
	defer func() { configDirOverride = old }()

	c := Default()
	c.Target.BaseURL = "https://v2.picui.cn"
	c.Target.Password = "s3cret"
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret") {
		t.Fatalf("密码不应落盘：\n%s", data)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Target.Password != "" {
		t.Fatal("重新加载后不应带出密码")
	}
}

// TestStateRoundtrip 运行记录（不含密钥）读写与最近来源去重。
func TestStateRoundtrip(t *testing.T) {
	dir := t.TempDir()
	old := configDirOverride
	configDirOverride = dir
	defer func() { configDirOverride = old }()

	st := LoadState()
	st.LastRun = &RunRecord{Source: "./posts", Target: "https://v2.picui.cn", Uploaded: 3,
		Failed: []FailedItem{{URL: "https://old/a.jpg", Stage: "download", Err: "HTTP 403"}}}
	st.RememberSource("./posts")
	st.RememberSource("./notes")
	st.RememberSource("./posts")
	if err := SaveState(st); err != nil {
		t.Fatal(err)
	}
	got := LoadState()
	if got.LastRun == nil || len(got.LastRun.Failed) != 1 || got.LastRun.Uploaded != 3 {
		t.Fatalf("运行记录不一致：%+v", got.LastRun)
	}
	if len(got.RecentSources) != 2 || got.RecentSources[0] != "./posts" {
		t.Fatalf("最近来源应去重且最新在前：%v", got.RecentSources)
	}
	info, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state.json 权限 = %v，应为 0600", info.Mode().Perm())
	}
}
