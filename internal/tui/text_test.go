package tui

import (
	"strings"
	"testing"
)

// TestWidthConvention 固定宽度口径：CJK 两列、几何/制表符号一列。
func TestWidthConvention(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"abc", 3},
		{"中文", 4},
		{"─", 1},
		{"│", 1},
		{"▍", 1},
		{"▣", 1},
		{"✓", 1},
		{"⚠", 1},
		{"a中b", 4},
	}
	for _, c := range cases {
		if got := Width(c.s); got != c.want {
			t.Errorf("Width(%q) = %d, 期望 %d", c.s, got, c.want)
		}
	}
}

// TestTruncateWide 中文/超长串截断后宽度正确、且不会切出半个字符。
func TestTruncateWide(t *testing.T) {
	if got := Truncate("中文测试", 4); got != "中…" {
		t.Errorf("Truncate 中文 = %q", got)
	}
	if got := Width(Truncate("中文测试中文测试", 7)); got > 7 {
		t.Errorf("截断后宽度 %d 超过 7：%q", got, Truncate("中文测试中文测试", 7))
	}
	if got := Truncate("short", 10); got != "short" {
		t.Errorf("未超长不应截断：%q", got)
	}
	if got := Truncate("x", 0); got != "" {
		t.Errorf("宽度 0 应返回空：%q", got)
	}
}

// TestTruncateANSI 带样式的字符串截断后仍是合法 ANSI、宽度不超标。
func TestTruncateANSI(t *testing.T) {
	// 手工构造 ANSI 字符串：测试环境非 TTY，lipgloss 不会加转义序列
	styled := "\x1b[1m" + "中文内容" + strings.Repeat("x", 30) + "\x1b[0m"
	got := Truncate(styled, 20)
	if w := Width(got); w > 20 {
		t.Errorf("ANSI 截断后宽度 %d 超过 20", w)
	}
	if !strings.Contains(got, "…") {
		t.Error("超长内容应带省略号")
	}
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Error("截断后应补重置序列，避免样式泄漏到后续行")
	}
}

// TestClipTail 左侧截断保留尾部（URL 尾部信息量更大）。
func TestClipTail(t *testing.T) {
	got := ClipTail("2026/08/29/abcdef.webp", 12)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, ".webp") {
		t.Errorf("ClipTail = %q", got)
	}
	if Width(got) > 12 {
		t.Errorf("ClipTail 宽度超标：%d", Width(got))
	}
	if ClipTail("short.webp", 20) != "short.webp" {
		t.Error("未超长应原样返回")
	}
}

// TestLRAndFit 右对齐与补齐。
func TestLRAndFit(t *testing.T) {
	got := LR("中文名", "12 张", 20)
	if Width(got) != 20 {
		t.Errorf("LR 宽度 = %d, 期望 20（%q）", Width(got), got)
	}
	if !strings.HasSuffix(got, "12 张") {
		t.Error("LR 右侧内容应贴右")
	}
	if got := Fit("abc", 6); got != "abc   " {
		t.Errorf("Fit = %q", got)
	}
	if got := Fit("中文", 3); Width(got) != 3 {
		t.Errorf("Fit 中文到 3 列 = %q（宽 %d）", got, Width(got))
	}
}

// TestWrap 折行不丢字符、每行不超宽。
func TestWrap(t *testing.T) {
	lines := Wrap("https://v2.picui.cn/free/20261003/74a6fc3dba4e8f471c5432a6e2e94874.webp", 20)
	for _, l := range lines {
		if Width(l) > 20 {
			t.Errorf("折行超宽：%q", l)
		}
	}
	if joined := strings.Join(lines, ""); len(joined) == 0 {
		t.Error("折行不应丢字符")
	}
	zh := Wrap("中文折行测试内容需要正确计算宽度", 8)
	for _, l := range zh {
		if Width(l) > 8 {
			t.Errorf("中文折行超宽：%q", l)
		}
	}
}
