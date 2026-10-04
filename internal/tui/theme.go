// 主题：语义色 token、图标集与降级判定。
// 规则：色彩只表达状态；不使用 Nerd Font 私有区图标；
// 每个状态字形都有 ASCII 降级；宽度一律按显示宽度计算。
package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func init() {
	// 宽度口径固定为"歧义宽字符按 1 列"：现代终端（Windows Terminal / iTerm2 /
	// VS Code / kitty / GNOME Terminal）都把 ─ │ ▍ ▣ ✓ ⚠ 这类 East Asian Ambiguous
	// 字符渲染为 1 列，而 go-runewidth 会按 locale 把它们算成 2 列，导致边框与
	// 右对齐列在真实终端里整体偏移。CJK 汉字是 Wide 类，不受此设置影响。
	runewidth.DefaultCondition.EastAsianWidth = false
}

// 语义色 token：浅色/深色自适应，16 色与无色终端由 lipgloss 自动降级。
var (
	cAccent   = lipgloss.AdaptiveColor{Light: "#0B6E7F", Dark: "#56C8D8"}
	cAccent2  = lipgloss.AdaptiveColor{Light: "#3B4CCA", Dark: "#7C8CF8"}
	cOK       = lipgloss.AdaptiveColor{Light: "#1F7A4D", Dark: "#63D19A"}
	cWarn     = lipgloss.AdaptiveColor{Light: "#8A5A00", Dark: "#E9B44C"}
	cDanger   = lipgloss.AdaptiveColor{Light: "#B3261E", Dark: "#F0716B"}
	cText     = lipgloss.AdaptiveColor{Light: "#1F2933", Dark: "#D7DEE8"}
	cMuted    = lipgloss.AdaptiveColor{Light: "#5B6675", Dark: "#8B95A7"}
	cFaint    = lipgloss.AdaptiveColor{Light: "#C7CFDA", Dark: "#39424F"}
	cCursorBG = lipgloss.AdaptiveColor{Light: "#E4E9F0", Dark: "#263040"}
)

var (
	stText     = lipgloss.NewStyle().Foreground(cText)
	stMuted    = lipgloss.NewStyle().Foreground(cMuted)
	stFaint    = lipgloss.NewStyle().Foreground(cFaint)
	stOK       = lipgloss.NewStyle().Foreground(cOK)
	stWarn     = lipgloss.NewStyle().Foreground(cWarn)
	stDanger   = lipgloss.NewStyle().Foreground(cDanger)
	stAccent   = lipgloss.NewStyle().Foreground(cAccent)
	stBold     = lipgloss.NewStyle().Foreground(cText).Bold(true)
	stAccentB  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	stCursor   = lipgloss.NewStyle().Background(cCursorBG)
	stOKBold   = lipgloss.NewStyle().Foreground(cOK).Bold(true)
	stWarnBold = lipgloss.NewStyle().Foreground(cWarn).Bold(true)
	stDangerB  = lipgloss.NewStyle().Foreground(cDanger).Bold(true)
	stLabelOn  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	stOKBox    = lipgloss.NewStyle().Foreground(cOK)
)

// glyphs 是状态字形集；ASCII 版用于不支持 UTF-8 的终端。
type glyphs struct {
	Check, Cross, Warn, Pending string
	Cursor, SelAll, SelNone     string
	SelPart, FoldClosed         string
	FoldOpen, Bar               string
	QuoteLeft, QuoteRight       string
	Dash, VBar, Dot             string
	Bullet                      string
}

var uniGlyphs = glyphs{
	Check: "✓", Cross: "✗", Warn: "⚠", Pending: "○",
	Cursor: "▸", SelAll: "▣", SelNone: "▫", SelPart: "◪",
	FoldClosed: "▸", FoldOpen: "▾", Bar: "▍",
	QuoteLeft: "‹", QuoteRight: "›",
	Dash: "─", VBar: "│", Dot: "·", Bullet: "●",
}

var asciiGlyphs = glyphs{
	Check: "v", Cross: "x", Warn: "!", Pending: "o",
	Cursor: ">", SelAll: "[x]", SelNone: "[ ]", SelPart: "[-]",
	FoldClosed: "+", FoldOpen: "-", Bar: "|",
	QuoteLeft: "<", QuoteRight: ">",
	Dash: "-", VBar: "|", Dot: ".", Bullet: "*",
}

// asciiMode 由 --ascii、TERM=dumb 或非 UTF-8 locale 触发。
var asciiMode = detectASCII()

func detectASCII() bool {
	if os.Getenv("IMGFERRY_ASCII") != "" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return true
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			up := strings.ToUpper(v)
			return !strings.Contains(up, "UTF-8") && !strings.Contains(up, "UTF8")
		}
	}
	return false
}

// asciiOverride 供测试固定字形模式。
var asciiOverride *bool

// asciiEnabled 返回是否使用 ASCII 字形。
func asciiEnabled() bool {
	if asciiOverride != nil {
		return *asciiOverride
	}
	return asciiMode
}

// setASCII 固定字形模式（--ascii、配置 ui.ascii 与测试共用）。
func setASCII(v bool) { asciiOverride = &v }

// setASCIIForTest 是 setASCII 的测试别名。
func setASCIIForTest(v bool) { setASCII(v) }

// glyphsFor 返回当前终端适用的字形集。
func glyphsFor() glyphs {
	if asciiEnabled() {
		return asciiGlyphs
	}
	return uniGlyphs
}

// spinnerFor 返回当前终端的 spinner 模型（ASCII 终端用线框帧）。
func spinnerFor() spinner.Model {
	if asciiEnabled() {
		return spinner.New(spinner.WithSpinner(spinner.Line))
	}
	return spinner.New(spinner.WithSpinner(spinner.Dot))
}

// ---- 常用片段 ----

func styleFor(level string) lipgloss.Style {
	switch level {
	case "ok":
		return stOK
	case "warn":
		return stWarn
	case "err", "danger":
		return stDanger
	case "accent":
		return stAccent
	default:
		return stMuted
	}
}

// levelGlyph 返回通知/状态级别对应的字形。
func levelGlyph(level string) string {
	g := glyphsFor()
	switch level {
	case "ok":
		return g.Check
	case "warn":
		return g.Warn
	case "err", "danger":
		return g.Cross
	default:
		return ""
	}
}

// ruleLine 是一条细分割线。
func ruleLine(width int) string {
	return stFaint.Render(strings.Repeat(glyphsFor().Dash, maxInt(width, 0)))
}
