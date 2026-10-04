package tui

import (
	"strings"
	"testing"
)

// TestHintBarCoverage 断言每屏都有键位提示、都提供 ? 帮助，且任何宽度下都不越界。
func TestHintBarCoverage(t *testing.T) {
	for _, step := range []int{stepStart, stepSource, stepSelect, stepTarget, stepPreflight, stepRun, stepReport} {
		for _, width := range []int{24, 40, 56, 96} {
			hint := km.hint(step, false, width)
			if strings.TrimSpace(stripANSI(hint)) == "" {
				t.Errorf("step=%d @%d 缺少键位提示", step, width)
			}
			if !strings.Contains(stripANSI(hint), "?") {
				t.Errorf("step=%d @%d 的提示未包含 ? 帮助：%q", step, width, stripANSI(hint))
			}
			if w := Width(hint); w > width {
				t.Errorf("step=%d @%d 键位行宽 %d 越界：%q", step, width, w, stripANSI(hint))
			}
		}
	}
}

// TestKeyGrammar 断言核心键位在任何屏都是同一语义。
func TestKeyGrammar(t *testing.T) {
	if km.Submit.Keys()[0] != "enter" {
		t.Error("enter 必须是主操作")
	}
	if km.Cancel.Keys()[0] != "esc" {
		t.Error("esc 必须是返回")
	}
	if km.Toggle.Keys()[0] != "space" {
		t.Error("space 必须是切换")
	}
	if km.Filter.Keys()[0] != "/" {
		t.Error("/ 必须是过滤")
	}
	if km.Help.Keys()[0] != "?" {
		t.Error("? 必须是帮助")
	}
	if km.Quit.Keys()[0] != "q" {
		t.Error("q 必须是退出")
	}
	if km.ForceQuit.Keys()[0] != "ctrl+c" {
		t.Error("ctrl+c 必须是强制中断")
	}
	for i, b := range km.Step {
		if len(b.Keys()) == 0 {
			t.Errorf("步骤 %d 缺少数字快捷键", i+1)
		}
	}
}

// TestHelpRowsEveryStep 断言帮助覆盖层每屏都有内容，且都列出全局键。
func TestHelpRowsEveryStep(t *testing.T) {
	for _, step := range []int{stepStart, stepSource, stepSelect, stepTarget, stepPreflight, stepRun, stepReport} {
		local, global := km.helpRows(step)
		if len(local) == 0 {
			t.Errorf("step=%d 的本屏帮助为空", step)
		}
		if len(global) < 4 {
			t.Errorf("step=%d 的全局帮助过少", step)
		}
	}
}

// TestEscLayersContract 断言各屏对 esc 的层数声明与内部状态一致。
func TestEscLayersContract(t *testing.T) {
	sess := sampleSession(t)
	if got := (&sourceScreen{}).EscLayers(); got != 0 {
		t.Errorf("来源屏 esc 层数应为 0，实际 %d", got)
	}
	if got := (&runScreen{}).EscLayers(); got != 1 {
		t.Errorf("迁移屏 esc 用于取消，层数应为 1，实际 %d", got)
	}
	sel := &selectScreen{tree: newCheckTree(nil)}
	if got := sel.EscLayers(); got != 0 {
		t.Errorf("无过滤的选择屏 esc 层数应为 0，实际 %d", got)
	}
	sel.tree.filter = "webp"
	if got := sel.EscLayers(); got != 1 {
		t.Errorf("有过滤的选择屏 esc 应先清过滤，层数 1，实际 %d", got)
	}
	sel.filtering = true
	if got := sel.EscLayers(); got != 1 {
		t.Errorf("过滤编辑态 esc 层数应为 1，实际 %d", got)
	}
	_ = sess
}
