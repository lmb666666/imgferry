// 文本排版工具：一切按"显示宽度"处理（CJK 两列、歧义宽字符一列），
// 不使用字节或字符计数——中文按字节截断会产生半个字符的乱码。
package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Width 返回字符串的显示宽度（自动跳过 ANSI 转义序列）。
// 口径统一为 go-runewidth 默认值：CJK 两列、歧义宽字符（─ │ ▍ ▣ 等）一列——
// 与规格里的宽度约定和绝大多数终端一致；lipgloss 对歧义字符按两列计算，
// 混用会导致边框与右对齐列漂移。
func Width(s string) int {
	return runewidth.StringWidth(stripANSI(s))
}

// stripANSI 去掉 ANSI 转义序列，保留可见文本。
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	for _, t := range tokenizeANSI(s) {
		if !t.esc {
			b.WriteString(t.s)
		}
	}
	return b.String()
}

// Truncate 按显示宽度截断，超出部分以 … 结尾；width 过小时退化为省略号。
// 含 ANSI 转义序列时走 ANSI 感知路径，保证不会在转义序列中间切断。
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if strings.ContainsRune(s, 0x1b) {
		return truncateANSI(s, width)
	}
	if Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	acc := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if acc+rw > width-1 {
			break
		}
		b.WriteRune(r)
		acc += rw
	}
	return b.String() + "…"
}

// ansiToken 是切分后的片段：转义序列（不计宽度）或一个可见字符。
type ansiToken struct {
	esc bool
	s   string
	w   int
}

func tokenizeANSI(s string) []ansiToken {
	var out []ansiToken
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			out = append(out, ansiToken{esc: true, s: s[i:j]})
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		out = append(out, ansiToken{s: string(r), w: runewidth.RuneWidth(r)})
		i += size
	}
	return out
}

// truncateANSI 复制转义序列、按显示宽度截断可见内容，结尾补省略号与重置序列。
func truncateANSI(s string, width int) string {
	toks := tokenizeANSI(s)
	total := 0
	for _, t := range toks {
		if !t.esc {
			total += t.w
		}
	}
	if total <= width {
		return s
	}
	limit := width - 1 // 给 … 留一列
	var b strings.Builder
	acc := 0
	for _, t := range toks {
		if t.esc {
			b.WriteString(t.s)
			continue
		}
		if acc+t.w > limit {
			break
		}
		b.WriteString(t.s)
		acc += t.w
	}
	b.WriteString("…\x1b[0m")
	return b.String()
}

// ClipTail 从左侧截断，保留尾部（URL 尾部信息量更大）：…/08/29/xx.webp。
func ClipTail(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(s)
	acc := 0
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		rw := runewidth.RuneWidth(runes[i])
		if acc+rw > width-1 {
			break
		}
		acc += rw
		start = i
	}
	return "…" + string(runes[start:])
}

// Pad 只在不足时右补空格；不截断。
func Pad(s string, width int) string {
	if d := width - Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// Fit 截断并右补空格，得到恰好 width 列的内容。
func Fit(s string, width int) string { return Pad(Truncate(s, width), width) }

// LR 左内容与右内容对齐到同一行（右侧贴右边）。
func LR(left, right string, width int) string {
	rw := Width(right)
	left = Truncate(left, width-rw-1)
	return Fit(left+strings.Repeat(" ", maxInt(1, width-Width(left)-rw))+right, width)
}

// Wrap 按显示宽度折行；长串（URL）硬折，不做单词级换行。
func Wrap(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	if s == "" {
		return []string{""}
	}
	var out []string
	var cur strings.Builder
	acc := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if acc+rw > width {
			out = append(out, cur.String())
			cur.Reset()
			acc = 0
		}
		cur.WriteRune(r)
		acc += rw
	}
	out = append(out, cur.String())
	return out
}

// FitLines 把内容行调整到恰好 n 行 × width 列（不足补空行，超出丢弃）。
func FitLines(lines []string, n, width int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if i < len(lines) {
			out = append(out, Fit(lines[i], width))
		} else {
			out = append(out, strings.Repeat(" ", width))
		}
	}
	return out
}

// Indent 给多行内容统一缩进。
func Indent(lines []string, n int) []string {
	pad := strings.Repeat(" ", n)
	out := make([]string, len(lines))
	for i, l := range lines {
		if l == "" {
			out[i] = l
			continue
		}
		out[i] = pad + l
	}
	return out
}

// Lines 把渲染出的多行字符串切成行。
func Lines(s string) []string { return strings.Split(s, "\n") }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
