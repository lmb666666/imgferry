package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lmb666666/imgferry/internal/scan"
)

// bigTree 造 n 条图片链接（含 5 个域名、40 个文件）。
func bigTree(n int) *checkTree {
	var links []scan.Link
	for i := 0; i < n; i++ {
		links = append(links, scan.Link{
			File: fmt.Sprintf("./posts/2026/%02d/entry-%02d.md", i%40, i%40),
			Line: i + 1,
			URL:  fmt.Sprintf("https://img%d.example.com/2026/%02d/pic-%05d.webp", i%5, i%12, i),
			Kind: scan.KindImage,
		})
	}
	return newCheckTree(links)
}

// TestLargeListPerformance 断言 2000 项下按键与渲染的开销（规格 §11.5：<16ms）。
func TestLargeListPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过性能断言（-short）")
	}
	tree := bigTree(2000)
	tree.SetAll(true)
	sel := &selectScreen{tree: tree}
	sel.Setup(nil, nil)

	const rounds = 200
	for i := 0; i < rounds; i++ {
		tree.Move(1)
		if out := sel.View(96, 26); !strings.Contains(out, "详情") {
			t.Fatal("渲染结果异常")
		}
	}
	perOp := timePer(rounds, func() {
		tree.Move(1)
		_ = sel.View(96, 26)
	})
	if perOp > 16*time.Millisecond {
		t.Fatalf("2000 项下一次按键+渲染耗时 %v，超过 16ms 预算", perOp)
	}
	t.Logf("2000 项：按键+渲染 %v/次", perOp)
}

func timePer(n int, f func()) time.Duration {
	start := time.Now()
	for i := 0; i < n; i++ {
		f()
	}
	return time.Since(start) / time.Duration(n)
}

// BenchmarkTreeMove 与 BenchmarkSelectView 便于后续回归对比。
func BenchmarkTreeMove(b *testing.B) {
	tree := bigTree(2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.Move(1)
	}
}

func BenchmarkSelectView(b *testing.B) {
	tree := bigTree(2000)
	sel := &selectScreen{tree: tree}
	sel.Setup(nil, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sel.View(96, 26)
	}
}
