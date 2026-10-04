package replace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyReplacesAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "post.md")
	// CRLF + 同一 URL 出现两次 + 另一个无关 URL
	original := "![a](https://old/a.png)\r\n![b](https://old/a.png)\r\n[链接](https://old/b.png)\r\n"
	os.WriteFile(p, []byte(original), 0o644)

	changed, n, err := Apply([]string{p}, map[string]string{
		"https://old/a.png": "https://new/x.png",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 || n != 2 {
		t.Fatalf("changed=%d replacements=%d, want 1/2", changed, n)
	}

	after, _ := os.ReadFile(p)
	want := "![a](https://new/x.png)\r\n![b](https://new/x.png)\r\n[链接](https://old/b.png)\r\n"
	if string(after) != want {
		t.Fatalf("content mismatch:\ngot  %q\nwant %q", after, want)
	}

	bak, err := os.ReadFile(p + ".bak")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(bak) != original {
		t.Fatalf("backup content mismatch: %q", bak)
	}
}

func TestBackupKeepsFirstCopy(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "post.md")
	os.WriteFile(p, []byte("v1 https://old/a.png"), 0o644)

	Apply([]string{p}, map[string]string{"https://old/a.png": "https://new/1.png"}, true)
	os.WriteFile(p, []byte("v2 https://old/b.png"), 0o644)
	Apply([]string{p}, map[string]string{"https://old/b.png": "https://new/2.png"}, true)

	bak, _ := os.ReadFile(p + ".bak")
	if string(bak) != "v1 https://old/a.png" {
		t.Fatalf("first backup was overwritten: %q", bak)
	}
}

func TestApplyNoChange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "post.md")
	os.WriteFile(p, []byte("no urls here"), 0o644)

	changed, n, err := Apply([]string{p}, map[string]string{"https://old/a.png": "https://new/a.png"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 || n != 0 {
		t.Fatalf("changed=%d n=%d, want 0/0", changed, n)
	}
	if _, err := os.Stat(p + ".bak"); !os.IsNotExist(err) {
		t.Fatal("no backup should be created when nothing changed")
	}
}

func TestWriteFileAtomicPreservesMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.md")
	os.WriteFile(p, []byte("x https://old/a.png"), 0o644)
	os.Chmod(p, 0o755)

	Apply([]string{p}, map[string]string{"https://old/a.png": "https://new/a.png"}, false)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
	if strings.Contains(readFile(t, p), "old") {
		t.Fatal("replacement did not happen")
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
