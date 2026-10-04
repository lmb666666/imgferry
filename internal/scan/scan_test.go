package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExtractLinks(t *testing.T) {
	content := []byte(`# 标题
![封面](https://sm.ms/i/a.png)
普通链接 [文本](https://example.com/page) 不是图片语法
<img src="https://imgur.com/b.jpg" alt="x">
![带标题](https://img.example.com/c.png "t")
---
cover: https://bu.dusays.com/2026/08/29/cover.webp
image: https://bu.dusays.com/2026/08/29/img.png
src: https://cdn.example.com/music/audio.mp3
正文里贴的裸 URL：https://sm.ms/i/bare.png，后面的中文标点不算。
[点开大图](https://sm.ms/i/raw.png)是普通链接但指向图片。
`)
	links := ExtractLinks("demo.md", content)
	want := []Link{
		{"demo.md", 2, "https://sm.ms/i/a.png", KindImage},
		{"demo.md", 3, "https://example.com/page", KindLink},
		{"demo.md", 4, "https://imgur.com/b.jpg", KindImage},
		{"demo.md", 5, "https://img.example.com/c.png", KindImage},
		{"demo.md", 7, "https://bu.dusays.com/2026/08/29/cover.webp", KindImage},
		{"demo.md", 8, "https://bu.dusays.com/2026/08/29/img.png", KindImage},
		// 第 9 行的 mp3 属于音视频，直接跳过、不进结果
		{"demo.md", 10, "https://sm.ms/i/bare.png", KindImage},
		{"demo.md", 11, "https://sm.ms/i/raw.png", KindImage},
	}
	if !reflect.DeepEqual(links, want) {
		t.Fatalf("got %+v\nwant %+v", links, want)
	}
}

func TestExtractHTML(t *testing.T) {
	content := []byte(`<!doctype html>
<html>
<head>
  <script src="/static/app.js"></script>
</head>
<body>
  <img
     src="https://old.example.com/pic/a.png"
     alt="a">
  <img src='https://old.example.com/pic/b.webp' />
  <p>说明文字 <a href="https://old.example.com/pic/c.jpg">查看大图</a></p>
</body>
</html>
`)
	links := ExtractLinks("page.html", content)
	var imgs []string
	for _, l := range links {
		if l.Kind == KindImage {
			imgs = append(imgs, l.URL)
		}
	}
	want := []string{
		"https://old.example.com/pic/a.png",  // img 属性跨行
		"https://old.example.com/pic/b.webp", // 单引号属性
		"https://old.example.com/pic/c.jpg",  // <a href> 指向图片
	}
	if !reflect.DeepEqual(imgs, want) {
		t.Fatalf("got %v\nwant %v", imgs, want)
	}
	// 跨行 img 的行号应对准标签起始行（第 7 行）
	for _, l := range links {
		if l.URL == "https://old.example.com/pic/a.png" && l.Line != 7 {
			t.Fatalf("multiline img line = %d, want 7", l.Line)
		}
	}
}

func TestFilterURLs(t *testing.T) {
	links := []Link{
		{URL: "https://sm.ms/a.png"},
		{URL: "https://img.example.com/b.png"},
		{URL: "https://cdn.img.example.com/c.png"},
		{URL: "https://other.com/d.png"},
		{URL: "./local.png"},
	}
	got := FilterURLs(links, []string{"img.example.com", "sm.ms"})
	if len(got) != 3 ||
		got[0].URL != "https://sm.ms/a.png" ||
		got[2].URL != "https://cdn.img.example.com/c.png" {
		t.Fatalf("unexpected: %+v", got)
	}
	if all := FilterURLs(links, nil); len(all) != 4 {
		t.Fatalf("want 4 http links, got %d", len(all))
	}
}

func TestFindFiles(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.md"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "b.markdown"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "page.html"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "code.go"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "old.md.bak"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "migrate-map.json"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, ".git", "c.md"), nil, 0o644)

	files, err := FindFiles(dir, DefaultExts)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 {
		t.Fatalf("want 5 files, got %v", files)
	}

	// 全文件模式：不限扩展名，但仍排除备份与映射表
	files, err = FindFiles(dir, ExtsAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 {
		t.Fatalf("all mode want 5 files, got %v", files)
	}
}

// TestScanAllMode 验证全文件模式：跳过二进制、超大文件与派生文件。
func TestScanAllMode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"),
		[]byte("see https://old.example.com/a.png\n"), 0o644)
	// 二进制：内容含 NUL 字节，即便内嵌 URL 也不扫描
	os.WriteFile(filepath.Join(dir, "data.bin"),
		[]byte("PK\x03\x04http://old.example.com/bin.png\x00\x00"), 0o644)
	// 备份与映射表：存的是旧链接，必须排除
	os.WriteFile(filepath.Join(dir, "old.md.bak"),
		[]byte("![b](https://old.example.com/bak.png)\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "migrate-map.json"),
		[]byte(`{"entries":{"https://old.example.com/map.png":{"new_url":"https://n/m.png"}}}`), 0o644)
	// 超大文件：超过大小上限不读
	big := strings.Repeat("x", int(MaxScanFileSize)) + "\nhttps://old.example.com/big.png\n"
	os.WriteFile(filepath.Join(dir, "big.log"), []byte(big), 0o644)

	res, err := Scan(dir, ExtsAll, []string{"old.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Links) != 1 || res.Links[0].URL != "https://old.example.com/a.png" {
		t.Fatalf("unexpected links: %+v", res.Links)
	}
}

func TestExtractMarkdownReference(t *testing.T) {
	content := []byte(`# 标题
![封面][cover]

正文引用 ![另一张][pic2] 和 [主页][site]。

[cover]: https://old.example.com/a.png "标题"
[pic2]: https://old.example.com/b.webp
[site]: https://example.com
`)
	links := ExtractLinks("post.md", content)
	var imgs []string
	for _, l := range links {
		if l.Kind == KindImage {
			imgs = append(imgs, l.URL)
		}
	}
	want := []string{"https://old.example.com/a.png", "https://old.example.com/b.webp"}
	if !reflect.DeepEqual(imgs, want) {
		t.Fatalf("got %v\nwant %v", imgs, want)
	}
}

// TestExtractMarkdownParens 验证 goldmark 的解析正确性：
// URL 含括号时正则会截断成错误地址，AST 解析则完整。
func TestExtractMarkdownParens(t *testing.T) {
	content := []byte("![a](https://old.example.com/a_(b).png)\n")
	links := ExtractLinks("p.md", content)
	for _, l := range links {
		if l.URL == "https://old.example.com/a_(b).png" && l.Kind == KindImage {
			return
		}
	}
	t.Fatalf("bracketed URL not extracted correctly: %+v", links)
}

func TestExtractHTMLSrcsetLazy(t *testing.T) {
	content := []byte(`<picture>
  <source srcset="https://old.example.com/a-480.png 480w, https://old.example.com/a-800.png 800w">
  <img data-original="https://old.example.com/lazy.png" src="placeholder.gif">
</picture>
<div style="background-image:url(https://old.example.com/bg.png)"></div>
`)
	links := ExtractLinks("page.html", content)
	var imgs []string
	for _, l := range links {
		if l.Kind == KindImage {
			imgs = append(imgs, l.URL)
		}
	}
	// placeholder.gif 是相对路径，ExtractLinks 层会捕获，Scan 阶段由
	// FilterURLs 统一过滤（只保留 http(s) 外链）
	want := []string{
		"https://old.example.com/a-480.png",
		"https://old.example.com/a-800.png",
		"placeholder.gif",
		"https://old.example.com/lazy.png",
		"https://old.example.com/bg.png",
	}
	if !reflect.DeepEqual(imgs, want) {
		t.Fatalf("got %v\nwant %v", imgs, want)
	}
}

func TestExtractCSS(t *testing.T) {
	content := []byte(`.hero { background: url(https://old.example.com/hero.png) no-repeat; }
.icon { background-image: url("https://old.example.com/i.svg"); }
@font-face { src: url(https://cdn.example.com/font.woff2); }
.relative { background: url(../img/local.png); }
`)
	links := ExtractLinks("style.css", content)
	var imgs []string
	for _, l := range links {
		if l.Kind == KindImage {
			imgs = append(imgs, l.URL)
		}
	}
	// 相对路径 ../img/local.png 同样在 Scan 阶段被过滤
	want := []string{
		"https://old.example.com/hero.png",
		"https://old.example.com/i.svg",
		"../img/local.png",
	}
	if !reflect.DeepEqual(imgs, want) {
		t.Fatalf("got %v\nwant %v", imgs, want)
	}
}

// TestScanContextCancel 断言扫描可取消：已取消的 ctx 立即返回，扫描中取消也生效。
func TestScanContextCancel(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%02d.md", i))
		if err := os.WriteFile(p, []byte("![x](https://old.example.com/a.webp)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanWithProgress(ctx, dir, DefaultExts, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的 ctx 应立即返回 context.Canceled，实际 %v", err)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	n := 0
	_, err := ScanWithProgress(ctx2, dir, DefaultExts, nil, func(read, found int) {
		n = read
		if read == 10 {
			cancel2()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("扫描中取消应返回 context.Canceled，实际 %v", err)
	}
	if n < 10 || n > 20 {
		t.Fatalf("应在取消处附近停止，实际读到 %d 个文件", n)
	}
}
