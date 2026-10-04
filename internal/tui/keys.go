// 键位表：全屏唯一的按键事实来源，页脚提示与帮助覆盖层都由它生成。
// 键法：enter 主操作、esc 逐层退回、space 切换、
// / 过滤、? 帮助、q 退出——任何屏幕不发明新语义。
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

type keyMap struct {
	Help      key.Binding
	Quit      key.Binding
	ForceQuit key.Binding

	Step [6]key.Binding

	Up, Down, Top, Bottom, PageUp, PageDown key.Binding
	Next, Prev, Submit, Cancel, Toggle      key.Binding
	Left, Right                             key.Binding

	Filter, SelectAll, Invert, Fold, GroupBy, Detail, CopyURL key.Binding
	FailOnly, RetryFailed, TestConn, ExportPlan               key.Binding
	JumpSource, JumpTarget, ClearInput                        key.Binding
}

func defaultKeyMap() keyMap {
	km := keyMap{
		Help:        kb("?", "帮助"),
		Quit:        kb("q", "退出"),
		ForceQuit:   kb("ctrl+c", "强制中断"),
		Up:          kb("↑|k", "上移"),
		Down:        kb("↓|j", "下移"),
		Top:         kb("g", "首项"),
		Bottom:      kb("G", "末项"),
		PageUp:      kb("pgup", "上翻页"),
		PageDown:    kb("pgdn", "下翻页"),
		Next:        kb("tab", "下一字段"),
		Prev:        kb("shift+tab", "上一字段"),
		Submit:      kb("enter", "确认"),
		Cancel:      kb("esc", "返回"),
		Toggle:      kb("space", "切换"),
		Left:        kb("←", "上一步"),
		Right:       kb("→", "下一步"),
		Filter:      kb("/", "过滤"),
		SelectAll:   kb("a", "全选可见"),
		Invert:      kb("u", "反选可见"),
		Fold:        kb("z", "折叠当前组"),
		GroupBy:     kb("v", "切换分组"),
		Detail:      kb("i", "完整详情"),
		CopyURL:     kb("y", "复制 URL"),
		FailOnly:    kb("f", "只看失败"),
		RetryFailed: kb("r", "只重试失败项"),
		TestConn:    kb("t", "测试连接"),
		ExportPlan:  kb("d", "只导出计划"),
		JumpSource:  kb("e", "修改来源"),
		JumpTarget:  kb("t", "修改目标"),
		ClearInput:  kb("ctrl+u", "清空输入"),
	}
	names := []string{"来源", "选择", "目标", "预检", "迁移", "结果"}
	for i, n := range names {
		km.Step[i] = key.NewBinding(key.WithKeys(fmt.Sprintf("%d", i+1)),
			key.WithHelp(fmt.Sprintf("%d", i+1), "跳到"+n))
	}
	return km
}

// km 是供屏幕与根模型共用的默认键位表。
var km = defaultKeyMap()

// kb 用 "|" 分隔同义按键（不能用 "/"，因为 / 本身就是一个按键）。
func kb(keys, help string) key.Binding {
	return key.NewBinding(key.WithKeys(strings.Split(keys, "|")...), key.WithHelp(keys, help))
}

// hint 返回页脚键位行（最多 5 个上下文键 + ? 帮助）。
// width 为可用列数：放不下的键位从尾部丢弃，但永远保留 ? 帮助。
func (k keyMap) hint(step int, editing bool, width int) string {
	var parts []string
	switch {
	case editing:
		return fitHints([]string{hintPair("enter", "确认"), hintPair("esc", "取消")}, width)
	case step == stepStart:
		parts = []string{hintPair("enter", "开始"), hintPair("↑↓", "选择"), hintPair("q", "退出")}
	case step == stepSource:
		parts = []string{hintPair("tab", "切换字段"), hintPair("enter", "开始扫描"), hintPair("q", "退出")}
	case step == stepSelect:
		parts = []string{hintPair("space", "选择"), hintPair("a", "全选"), hintPair("/", "过滤"),
			hintPair("v", "分组"), hintPair("enter", "下一步")}
	case step == stepTarget:
		parts = []string{hintPair("tab", "切换字段"), hintPair("t", "测试连接"),
			hintPair("enter", "下一步"), hintPair("esc", "返回")}
	case step == stepPreflight:
		parts = []string{hintPair("enter", "开始迁移"), hintPair("d", "只导出计划"),
			hintPair("e", "修改来源"), hintPair("t", "修改目标")}
	case step == stepRun:
		parts = []string{hintPair("esc", "取消迁移"), hintPair("f", "只看失败"), hintPair("↑↓", "回看")}
	case step == stepReport:
		parts = []string{hintPair("r", "只重试失败项"), hintPair("t", "修改目标"),
			hintPair("esc", "返回选择"), hintPair("q", "退出")}
	}
	// ? 帮助 永远最后：宽度不够时先丢前面的键位，帮助入口不丢。
	parts = append(parts, hintPair("?", "帮助"))
	return fitHints(parts, width)
}

// fitHints 在可用宽度内拼接键位提示：始终保留最后一个（? 帮助）。
func fitHints(parts []string, width int) string {
	if len(parts) == 0 {
		return ""
	}
	sep := " " + stMuted.Render(glyphsFor().Dot) + " "
	help := parts[len(parts)-1]
	budget := width - Width(help) - Width(sep)
	out := ""
	for _, p := range parts[:len(parts)-1] {
		next := p
		if out != "" {
			next = out + sep + p
		}
		if Width(next) > budget {
			break
		}
		out = next
	}
	if out == "" {
		return help
	}
	return out + sep + help
}

func hintPair(k, d string) string {
	return stAccent.Render(k) + " " + stMuted.Render(d)
}

// helpRows 返回帮助覆盖层的内容：本屏键位 + 全局键位，两列排布。
func (k keyMap) helpRows(step int) (local, global [][2]string) {
	switch step {
	case stepStart:
		local = [][2]string{{"↑ ↓", "移动选择"}, {"enter", "执行选中项"}, {"c", "继续上次"},
			{"r", "重试失败项"}, {"x", "清空映射表"}}
	case stepSource:
		local = [][2]string{{"tab / ↓", "下一字段"}, {"shift+tab / ↑", "上一字段"},
			{"← →", "切换选项 / ±1"}, {"space", "切换开关 / 展开"}, {"enter", "开始扫描"},
			{"ctrl+u", "清空输入"}, {"esc", "返回首页"}}
	case stepSelect:
		local = [][2]string{{"space", "勾选 / 组整选"}, {"a / u", "全选 / 反选可见"},
			{"/", "过滤"}, {"v", "切换分组"}, {"z / Z", "折叠当前组 / 全部"},
			{"i", "完整详情"}, {"y", "复制 URL"}, {"enter", "下一步"}, {"esc", "返回来源"}}
	case stepTarget:
		local = [][2]string{{"tab / ↓", "下一字段"}, {"shift+tab / ↑", "上一字段"},
			{"← →", "切换选项 / ±1"}, {"space", "切换开关"}, {"t", "测试连接"},
			{"enter", "下一步"}, {"esc", "返回选择"}}
	case stepPreflight:
		local = [][2]string{{"enter", "开始迁移"}, {"d", "只导出计划（不写文件）"},
			{"e", "修改来源"}, {"t", "修改目标"}, {"esc", "返回目标"}}
	case stepRun:
		local = [][2]string{{"↑ ↓", "回看事件（暂停跟随）"}, {"G", "回到底部并跟随"},
			{"f", "只看失败"}, {"esc", "取消迁移"}}
	case stepReport:
		local = [][2]string{{"↑ ↓", "移动"}, {"enter", "查看该项详情"},
			{"r", "只重试失败项"}, {"t", "修改目标"}, {"esc", "返回选择"}}
	}
	global = [][2]string{{"?", "帮助"}, {"q", "退出"}, {"1–6", "跳转步骤"},
		{"ctrl+c", "强制中断"}, {"esc", "逐层退回"}, {"滚轮 / 点击", "滚动、选中行"}}
	return local, global
}
