// 布局：固定三区框架（步骤轨 / 内容 / 页脚）、面板盒子、居中与过小终端保护。
// 框架保证输出恰好 h 行、每行不超过 w 列，内容区高度恒定——任何状态变化都不跳屏。
package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// 步骤编号；首页在轨道之外。
const (
	stepStart     = -1
	stepSource    = 0
	stepSelect    = 1
	stepTarget    = 2
	stepPreflight = 3
	stepRun       = 4
	stepReport    = 5
	stepCount     = 6
)

// 布局常量：内容宽度上限、字段列、面板内边距等。
const (
	pageMargin  = 2
	contentMax  = 108
	fieldLabelW = 10
	fieldValueX = 16 // 值列：2 缩进 + 1 光标 + 1 空格 + 10 标签 + 2 空列
	colGap      = 2
	minWidth    = 40
	minHeight   = 12
	noticeTTL   = 3 * time.Second
)

func contentWidth(w int) int {
	if w <= 0 {
		return contentMax
	}
	return maxInt(minInt(w-2*pageMargin, contentMax), 20)
}

func leftMargin(w int) int { return maxInt((w-contentWidth(w))/2, 0) }

// renderFrame 组装框架：步骤轨 + 细线 + 内容区（h-4 行）+ 通知行 + 键位行。
func renderFrame(w, h int, rail, body, notice, hint string) string {
	if w < minWidth || h < minHeight {
		return tooSmall(w, h)
	}
	cw, lm := contentWidth(w), leftMargin(w)
	pad := strings.Repeat(" ", lm)
	bodyH := h - 4

	out := make([]string, 0, h)
	out = append(out, pad+Fit(rail, cw))
	out = append(out, pad+ruleLine(cw))
	for _, l := range FitLines(Lines(body), bodyH, cw) {
		out = append(out, pad+l)
	}
	out = append(out, pad+Fit(notice, cw))
	out = append(out, pad+Fit(hint, cw))
	return strings.Join(out, "\n")
}

// tooSmall 是终端过小时的兜底页：不渲染主界面，避免溢出错乱。
func tooSmall(w, h int) string {
	msg := Truncate("终端过小（当前 "+itoa(w)+"×"+itoa(h)+"）", maxInt(w-2, 8))
	msg2 := Truncate("请调整到至少 40×12", maxInt(w-2, 8))
	blank := strings.Repeat("\n", maxInt((h-2)/2, 0))
	if h < 2 {
		return msg
	}
	return blank + " " + stWarn.Render(msg) + "\n " + stMuted.Render(msg2)
}

// panelBox 画一个带标题的圆角面板；inner 为面板内部宽度（含左右各 1 列内边距）。
// 返回行宽固定为 inner+2，便于并排拼接。
func panelBox(title string, rows []string, inner int, focused bool) []string {
	bd := lipgloss.NewStyle().Foreground(cFaint)
	if focused {
		bd = lipgloss.NewStyle().Foreground(cAccent)
	}
	out := make([]string, 0, len(rows)+2)
	head := ""
	if title != "" {
		head = " " + stMuted.Render(title) + " "
	}
	headW := 0
	if title != "" {
		headW = Width(title) + 2
	}
	out = append(out, bd.Render("┌")+head+bd.Render(strings.Repeat("─", maxInt(inner-headW, 0))+"┐"))
	for _, r := range rows {
		out = append(out, bd.Render("│")+" "+Fit(r, inner-2)+" "+bd.Render("│"))
	}
	out = append(out, bd.Render("└"+strings.Repeat("─", inner)+"┘"))
	return out
}

// centerBlock 把内容块在 w×h 区域内水平、垂直居中。
func centerBlock(lines []string, w, h int) []string {
	out := make([]string, 0, h)
	top := maxInt((h-len(lines))/2, 0)
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	for _, l := range lines {
		lw := Width(l)
		lm := maxInt((w-lw)/2, 0)
		out = append(out, strings.Repeat(" ", lm)+l)
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

// bodyRowFromMouse 把鼠标纵坐标换算成内容区行号（去掉步骤轨与细线两行）。
// 返回 -1 表示落在内容区之外。
func bodyRowFromMouse(y, h int) int {
	row := y - 2
	if row < 0 || row >= h-4 {
		return -1
	}
	return row
}

// panelRowFromMouse 把内容区行号换算成面板内第几行内容（去掉上边框）。
func panelRowFromMouse(bodyRow int) int { return bodyRow - 1 }

// sectionTitle 是节标题：▍扫描范围。
func sectionTitle(text string) string {
	g := glyphsFor()
	return stAccent.Render(g.Bar) + " " + stBold.Render(text)
}

// fieldLabel 渲染表单标签（聚焦时 accent 加粗并带 ▍ 光标）。
func fieldLabel(label string, focused bool) string {
	g := glyphsFor()
	mark := " "
	if focused {
		mark = stAccent.Render(g.Bar)
	}
	style := stMuted
	if focused {
		style = stLabelOn
	}
	return "  " + mark + " " + style.Render(Fit(label, fieldLabelW)) + "  "
}

// bannerRows 是大字报的六行字形（ANSI Shadow）。
func bannerRows() []string {
	letters := map[rune][]string{
		'I': {"██╗██╗", "██║██║", "██║██║", "██║██║", "██║██║", "╚═╝╚═╝"},
		'M': {"███╗   ███╗", "████╗ ████║", "██╔████╔██║", "██║╚██╔╝██║", "██║ ╚═╝ ██║", "╚═╝     ╚═╝"},
		'G': {" ██████╗ ", "██╔════╝ ", "██║  ███╗", "██║   ██║", "╚██████╔╝", " ╚═════╝ "},
		'F': {"███████╗", "██╔════╝", "█████╗  ", "██╔══╝  ", "██║     ", "╚═╝     "},
		'E': {"███████╗", "██╔════╝", "█████╗  ", "██╔══╝  ", "███████╗", "╚══════╝"},
		'R': {"██████╗ ", "██╔══██╗", "██████╔╝", "██╔══██╗", "██║  ██║", "╚═╝  ╚═╝"},
		'Y': {"██╗   ██╗", "╚██╗ ██╔╝", " ╚████╔╝ ", "  ╚██╔╝  ", "   ██║   ", "   ╚═╝   "},
	}
	rows := make([]string, 6)
	for _, r := range "IMGFERRY" {
		for i, line := range letters[r] {
			rows[i] += line + " "
		}
	}
	for i := range rows {
		rows[i] = strings.TrimRight(rows[i], " ")
	}
	return rows
}

// banner 是首页大字报；窄终端或 ASCII 模式退化为单行标题。
func banner(w int) []string {
	if asciiEnabled() || w < 96 {
		return []string{stAccentB.Render("~ imgferry") + stMuted.Render("  ·  图床图片批量迁移")}
	}
	gradient := []lipgloss.AdaptiveColor{
		{Light: "#0B6E7F", Dark: "#56C8D8"}, {Light: "#0F707E", Dark: "#4FB8D6"},
		{Light: "#137280", Dark: "#5AA6D8"}, {Light: "#2A6F8A", Dark: "#6E96DA"},
		{Light: "#3B4CCA", Dark: "#7C8CF8"}, {Light: "#4A56C4", Dark: "#8A82F0"},
	}
	rows := bannerRows()
	out := make([]string, 0, len(rows))
	for i, r := range rows {
		out = append(out, lipgloss.NewStyle().Foreground(gradient[i]).Bold(true).Render(r))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
