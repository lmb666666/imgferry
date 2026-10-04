package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/migrate"
	"github.com/lmb666666/imgferry/internal/upload/lsky"
)

// keyMsgFor 把易读的按键名转成 tea.KeyMsg。
func keyMsgFor(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdn":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

// drive 依次发送按键，并执行产生的命令（含 Batch 展开），模拟事件循环。
func drive(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_, cmd := m.Update(keyMsgFor(k))
		runCmds(t, m, cmd)
	}
}

func runCmds(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	switch v := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		for _, c := range v {
			runCmds(t, m, c)
		}
	default:
		_, next := m.Update(v)
		runCmds(t, m, next)
	}
}

func newTestModel(t *testing.T) *Model {
	t.Helper()
	oldTick := tickFn
	tickFn = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil } // 测试不等待定时器
	t.Cleanup(func() { tickFn = oldTick })
	setASCIIForTest(false)
	sess := sampleSession(t)
	m := &Model{keys: km, spin: spinnerFor(), sess: sess, version: "0.1.0", w: 100, h: 30}
	sess.SpinFrame = "⠋"
	m.cur = newScreen(stepSelect)
	m.gen++
	runCmds(t, m, m.cur.Init(sess, m.env()))
	return m
}

// TestEscLadder 验证 esc 逐层退回：过滤编辑 → 清除过滤 → 上一屏。
func TestEscLadder(t *testing.T) {
	m := newTestModel(t)
	tree := m.sess.Tree

	drive(t, m, "/")
	if !m.cur.(*selectScreen).filtering {
		t.Fatal("按 / 应进入过滤编辑态")
	}
	if m.cur.EscLayers() == 0 {
		t.Fatal("过滤编辑态下 esc 应由本屏消费")
	}
	drive(t, m, "w", "e", "b", "p")
	if tree.filter != "webp" {
		t.Fatalf("过滤词应为 webp，实际 %q", tree.filter)
	}
	drive(t, m, "esc") // 退出编辑，保留过滤
	if m.cur.(*selectScreen).filtering {
		t.Fatal("esc 应退出过滤编辑态")
	}
	if tree.filter != "webp" {
		t.Fatal("退出编辑应保留过滤词")
	}
	if len(tree.VisibleURLs()) >= tree.total() {
		t.Fatal("过滤应减少可见项")
	}
	drive(t, m, "esc") // 清除过滤
	if tree.filter != "" {
		t.Fatal("第二次 esc 应清除过滤")
	}
	drive(t, m, "esc") // 返回上一屏
	if m.cur.Step() != stepSource {
		t.Fatalf("第三次 esc 应回到来源屏，实际 step=%d", m.cur.Step())
	}
}

// TestSelectKeys 验证选择屏的勾选、全选、反选与进入下一步。
func TestSelectKeys(t *testing.T) {
	m := newTestModel(t)
	tree := m.sess.Tree
	tree.SetAll(false)
	if tree.CheckedCount() != 0 {
		t.Fatal("初始应为全不选")
	}
	drive(t, m, "down") // 移到第一张图片（首行是分组行）
	drive(t, m, " ")
	if tree.CheckedCount() != 1 {
		t.Fatalf("space 应勾选一项，实际 %d", tree.CheckedCount())
	}
	drive(t, m, "up")
	drive(t, m, " ")
	group := tree.rows[0].members
	if tree.CheckedCount() != len(group) {
		t.Fatalf("分组行 space 应整组勾选（%d 张），实际 %d", len(group), tree.CheckedCount())
	}
	drive(t, m, "a")
	if tree.CheckedCount() != tree.total() {
		t.Fatalf("a 应全选，实际 %d/%d", tree.CheckedCount(), tree.total())
	}
	drive(t, m, "a")
	if tree.CheckedCount() != 0 {
		t.Fatal("再次 a 应全部取消")
	}
	drive(t, m, "u")
	if tree.CheckedCount() != tree.total() {
		t.Fatal("u 应反选全部")
	}
	// 至少选一张才能前进
	tree.SetAll(false)
	drive(t, m, "enter")
	if m.cur.Step() != stepSelect {
		t.Fatal("没有勾选时不应前进")
	}
	tree.SetAll(true)
	drive(t, m, "enter")
	if m.cur.Step() != stepTarget {
		t.Fatalf("勾选后 enter 应进入目标屏，实际 %d", m.cur.Step())
	}
}

// TestSelectGrouping 验证分组维度切换与折叠不改变勾选集合。
func TestSelectGrouping(t *testing.T) {
	m := newTestModel(t)
	tree := m.sess.Tree
	before := tree.CheckedCount()
	drive(t, m, "v")
	if tree.dim == byDomain {
		t.Fatal("v 应切换分组维度")
	}
	drive(t, m, "v", "v", "v")
	if tree.dim != byDomain {
		t.Fatal("v 循环一圈应回到域名分组")
	}
	drive(t, m, "z")
	if tree.CheckedCount() != before {
		t.Fatal("折叠不应改变勾选")
	}
}

// TestDigitJump 验证数字跳步只允许已到达的步骤。
func TestDigitJump(t *testing.T) {
	m := newTestModel(t)
	m.sess.Reached = stepSelect
	drive(t, m, "3") // 未到达目标屏
	if m.cur.Step() != stepSelect {
		t.Fatalf("不应跳到未到达的步骤，实际 %d", m.cur.Step())
	}
	drive(t, m, "1")
	if m.cur.Step() != stepSource {
		t.Fatalf("应跳到已到达的来源屏，实际 %d", m.cur.Step())
	}
}

// TestTextEditingBlocksCommands 验证文本输入态下 q/?// 不再触发全局命令。
func TestTextEditingBlocksCommands(t *testing.T) {
	m := newTestModel(t)
	m.cur = newScreen(stepSource)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	src := m.cur.(*sourceScreen)
	if !m.cur.Editing() {
		t.Fatal("来源屏默认应聚焦文本字段（输入态）")
	}
	before := src.path
	drive(t, m, "q", "?", "1", "/")
	if src.path == before {
		t.Fatal("字符应进入输入框")
	}
	if !strings.Contains(src.path, "q?1/") {
		t.Fatalf("输入内容应为 q?1/，实际 %q", src.path)
	}
	if m.cur.Step() != stepSource {
		t.Fatal("输入态下不应切屏")
	}
	if m.helpOpen {
		t.Fatal("输入态下 ? 不应打开帮助")
	}
}

// TestPreflightBackupConfirm 验证关闭备份时需要二次确认才能开始。
func TestPreflightBackupConfirm(t *testing.T) {
	setup := func(backup bool) *Model {
		m := newTestModel(t)
		m.sess.Target.Backup = backup
		m.cur = newScreen(stepPreflight)
		m.gen++
		runCmds(t, m, m.cur.Init(m.sess, m.env()))
		return m
	}
	m := setup(false)
	drive(t, m, "enter")
	pf := m.cur.(*preflightScreen)
	if !pf.confirm {
		t.Fatal("关闭备份时 enter 应弹出二次确认")
	}
	if m.cur.EscLayers() == 0 {
		t.Fatal("确认弹层打开时 esc 应由本屏消费")
	}
	drive(t, m, "esc")
	if pf.confirm {
		t.Fatal("esc 应关闭确认弹层")
	}
	if m.cur.Step() != stepPreflight {
		t.Fatal("取消确认后应留在预检屏")
	}
	drive(t, m, "enter", "enter")
	if m.cur.Step() != stepRun {
		t.Fatalf("确认后应进入迁移屏，实际 %d", m.cur.Step())
	}

	m2 := setup(true)
	drive(t, m2, "enter")
	if m2.cur.Step() != stepRun {
		t.Fatalf("备份开启时 enter 直接开始，实际 %d", m2.cur.Step())
	}
}

// TestRunCancelConfirm 验证迁移屏的取消是"先确认、再取消"。
func TestRunCancelConfirm(t *testing.T) {
	m := newTestModel(t)
	rs := &runScreen{}
	rs.Setup(m.sess, m.env())
	rs.step, rs.active = stepRun, true
	m.cur = rs
	drive(t, m, "esc")
	if !rs.confirmCancel {
		t.Fatal("esc 应弹出取消确认")
	}
	drive(t, m, "esc")
	if rs.confirmCancel {
		t.Fatal("esc 应关闭确认并继续迁移")
	}
	drive(t, m, "f")
	if !rs.failOnly {
		t.Fatal("f 应切换只看失败")
	}
	// 无失败项时不应切换到失败视图之外的状态
	if got := len(rs.visibleItems()); got != 0 {
		t.Fatalf("没有失败项时失败视图应为空，实际 %d", got)
	}
}

// TestReportRetryFailed 验证 r 只把失败项放回流程。
func TestReportRetryFailed(t *testing.T) {
	m := newTestModel(t)
	m.sess.Report = &migrate.Report{Uploaded: 3, AlreadyDone: 2, ChangedFiles: 3, Replacements: 8,
		Failed: []migrate.Failure{
			{URL: "https://bu.dusays.com/a.webp", Stage: migrate.StageDownload, Err: "HTTP 403"},
			{URL: "https://bu.dusays.com/b.webp", Stage: migrate.StageUpload, Err: "操作频繁，请稍后再试"},
		}}
	m.cur = newScreen(stepReport)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	m.sess.SourcePath = sampleDir // 指向真实目录，让重试能真的开始扫描
	drive(t, m, "r")
	if m.cur.Step() != stepSource {
		t.Fatalf("r 应回到来源屏重新扫描，实际 %d", m.cur.Step())
	}
	if m.sess.RetryOnly == nil {
		t.Fatal("应把失败 URL 放进重试集合（由来源屏消费）")
	}
	src, ok := m.cur.(*sourceScreen)
	if !ok {
		t.Fatalf("应进入来源屏，实际 %T", m.cur)
	}
	if !src.scanning {
		t.Fatal("重试应自动开始扫描")
	}
}

// TestQuitConfirm 验证迁移进行中按 q 需要确认。
func TestQuitConfirm(t *testing.T) {
	m := newTestModel(t)
	rs := &runScreen{}
	rs.Setup(m.sess, m.env())
	rs.step, rs.active = stepRun, true
	m.cur = rs
	_, cmd := m.Update(keyMsgFor("q"))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("迁移进行中不应直接退出")
		}
	}
	if !m.quitAsk {
		t.Fatal("迁移进行中按 q 应弹二次确认")
	}
	drive(t, m, "esc")
	if m.quitAsk {
		t.Fatal("esc 应取消退出确认")
	}
}

// TestStaleGenDropped 验证切屏后旧世代的异步消息被丢弃。
func TestStaleGenDropped(t *testing.T) {
	m := newTestModel(t)
	oldGen := m.gen
	m.gen++ // 模拟切屏
	before := m.cur.Step()
	m.Update(genMsg{gen: oldGen, inner: statsMsg{st: migrate.Stats{Done: 1, Total: 2}}})
	if m.cur.Step() != before {
		t.Fatal("过期世代的异步消息不应影响界面")
	}
}

// TestNoticeExpiry 验证瞬时通知到期后回落到状态摘要。
func TestNoticeExpiry(t *testing.T) {
	m := newTestModel(t)
	runCmds(t, m, m.notify("ok", "连接正常", 0))
	if !m.noti.active(time.Now()) {
		t.Fatal("瞬时通知应处于激活状态")
	}
	m.Update(noticeExpireMsg{})
	// until 还未到期，通知仍在
	if !m.noti.active(time.Now()) {
		t.Fatal("通知未到期不应被清除")
	}
	m.noti.until = time.Now().Add(-time.Second)
	m.Update(noticeExpireMsg{})
	if m.noti.text != "" {
		t.Fatal("过期通知应被清除，回落状态摘要")
	}
}

// TestTargetCredentialSwitch 验证凭据方式切换会换掉字段集合，且焦点保持在该字段上。
func TestTargetCredentialSwitch(t *testing.T) {
	m := newTestModel(t)
	m.cur = newScreen(stepTarget)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	ts := m.cur.(*targetScreen)

	has := func(name string) bool {
		for _, f := range ts.form.fields {
			if f.name == name {
				return true
			}
		}
		return false
	}
	if !has("token") || has("pass") {
		t.Fatal("默认应为 Token 模式")
	}
	// 移到凭据字段并切到邮箱密码
	drive(t, m, "tab", "tab", "right")
	if m.sess.Target.CredMode != "password" {
		t.Fatalf("→ 应切到邮箱密码，实际 %q", m.sess.Target.CredMode)
	}
	ts = m.cur.(*targetScreen)
	if !has("email") || !has("pass") || has("token") {
		t.Fatal("切换后应换成邮箱 + 密码字段")
	}
	// 焦点不应被切走
	if got := ts.form.focusName(); got != "cred" {
		t.Fatalf("切换后焦点应保持在 cred，实际 %q", got)
	}
	// 校验：缺邮箱密码时不能提交
	m.sess.Target.Email, m.sess.Target.Password = "", ""
	drive(t, m, "enter")
	if m.cur.Step() != stepTarget {
		t.Fatal("邮箱密码为空时不应前进")
	}
	if ts.form.current().err == "" && ts.form.fields[3].err == "" {
		t.Fatal("应在字段下方给出内联错误")
	}
}

// TestTargetStrategySelect 验证存储策略下拉（选项覆盖层）可选值并写回。
func TestTargetStrategySelect(t *testing.T) {
	m := newTestModel(t)
	m.sess.Target.Strategies = []lsky.Strategy{{ID: 8, Name: "游客储存"}, {ID: 1, Name: "本地"}}
	m.cur = newScreen(stepTarget)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	ts := m.cur.(*targetScreen)

	// 移到存储策略字段（连接节：host url cred token test strategy）
	for i := 0; i < 6; i++ {
		drive(t, m, "tab")
		if ts.form.focusName() == "strategy" {
			break
		}
	}
	if ts.form.focusName() != "strategy" {
		t.Fatalf("应能 tab 到存储策略，当前 %q", ts.form.focusName())
	}
	drive(t, m, " ")
	if ts.modal == nil {
		t.Fatal("space 应打开选项覆盖层")
	}
	if !ts.Editing() || ts.EscLayers() == 0 {
		t.Fatal("选项覆盖层打开时按键应由本屏消费")
	}
	// 当前值为"站点默认策略"（列表末项），向上移到 1 · 本地
	drive(t, m, "up", "enter")
	if ts.modal != nil {
		t.Fatal("enter 应关闭覆盖层")
	}
	if m.sess.Target.StrategyID != "1" {
		t.Fatalf("应选中 ID 1，实际 %q", m.sess.Target.StrategyID)
	}
	// esc 取消应保持原值
	drive(t, m, " ", "up", "esc")
	if m.sess.Target.StrategyID != "1" {
		t.Fatalf("esc 取消应保持原值，实际 %q", m.sess.Target.StrategyID)
	}
}

// TestSourcePathValidation 验证路径校验内联提示与阻止前进。
func TestSourcePathValidation(t *testing.T) {
	m := newTestModel(t)
	m.cur = newScreen(stepSource)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	src := m.cur.(*sourceScreen)
	src.path = "/definitely/not/exists"
	drive(t, m, "enter")
	if src.scanning {
		t.Fatal("路径不存在时不应开始扫描")
	}
	if src.form.fields[0].err == "" {
		t.Fatal("应在路径字段下方给出内联错误")
	}
	src.path = sampleDir
	drive(t, m, "enter")
	if !src.scanning {
		t.Fatal("路径存在时应开始扫描")
	}
}

// TestMouseSelectRow 验证点击行坐标换算正确（滚轮与点击见规格 §7.7）。
func TestMouseSelectRow(t *testing.T) {
	m := newTestModel(t)
	sel := m.cur.(*selectScreen)
	_ = sel.View(contentWidth(100), 26) // 先渲染一次，记录内容区高度
	tree := m.sess.Tree
	tree.offset = 0

	// 内容区第 1 行是面板上边框，第 2 行才是第一条内容；y=2 为步骤轨之后第一行
	click := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 10, Y: 2 + 1 + 3}
	m.Update(click)
	want := 3
	if tree.idx != want {
		t.Fatalf("点击第 3 行应把光标移到索引 %d，实际 %d", want, tree.idx)
	}
	// 滚轮下移
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, X: 10, Y: 10})
	if tree.idx != want+3 {
		t.Fatalf("滚轮下移应 +3，实际 %d", tree.idx)
	}
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp, X: 10, Y: 10})
	if tree.idx != want {
		t.Fatalf("滚轮上移应 -3，实际 %d", tree.idx)
	}
	// 点击内容区之外不应移动
	tree.idx = 2
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 10, Y: 1})
	if tree.idx != 2 {
		t.Fatalf("点击步骤轨不应改变光标，实际 %d", tree.idx)
	}
}

// TestNoLayoutJitterOnFocus 钉住"焦点移动不改变布局"：
// 表单里选中字段时，说明提示不再插在字段下方把内容顶下去，
// 而是固定在表单下方的说明区，所以任意焦点位置的渲染行数与说明区位置都一致。
func TestNoLayoutJitterOnFocus(t *testing.T) {
	check := func(name string, scr screen, form *form, ch int) {
		type frame struct {
			lines []string
			strip int // 说明区起始行（相对内容区）
		}
		var frames []frame
		for i := range form.fields {
			form.focus(i)
			out := scr.View(contentWidth(100), ch)
			lines := strings.Split(out, "\n")
			strip := -1
			for j, l := range lines { // 说明区紧跟在最后一条分隔线之后
				if isRuleLine(l) {
					strip = j
				}
			}
			frames = append(frames, frame{lines: lines, strip: strip})
		}
		base := frames[0]
		for i, fr := range frames[1:] {
			field := i + 1 // frames[1:] 的第 i 个对应第 i+1 个字段
			if len(fr.lines) != len(base.lines) {
				t.Fatalf("%s 焦点第 %d 个字段时行数 %d，期望 %d（出现了布局跳动）",
					name, field, len(fr.lines), len(base.lines))
			}
			if fr.strip != base.strip || fr.strip < 0 {
				t.Fatalf("%s 焦点第 %d 个字段时说明区在第 %d 行，期望第 %d 行",
					name, field, fr.strip, base.strip)
			}
			// 说明区以外的部分必须一致（聚焦标记 ▍ 与行尾补白归一后再比）
			for j := range fr.lines {
				if j >= base.strip-1 && j <= base.strip+3 {
					continue // 说明区本身随焦点变化
				}
				// 只比"标签区"（前 16 列）：聚焦行的值区允许变化（如动作行提示），
				// 但字段的行位置绝不能变——这正是跳动的来源。
				if labelArea(fr.lines[j]) != labelArea(base.lines[j]) {
					t.Fatalf("%s 焦点第 %d 个字段时第 %d 行发生变化：%q → %q",
						name, field, j+1, normLine(base.lines[j]), normLine(fr.lines[j]))
				}
			}
		}
	}

	m := newTestModel(t)
	m.cur = newScreen(stepSource)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	check("来源屏", m.cur, m.cur.(*sourceScreen).form, 26)

	m.cur = newScreen(stepTarget)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	check("目标屏", m.cur, m.cur.(*targetScreen).form, 26)
}

// labelArea 取一行"标签区"（前 16 列），用于跨焦点位置比较结构是否移动。
func labelArea(s string) string { return Truncate(normLine(s), fieldValueX) }

// normLine 归一化：去掉 ANSI、聚焦标记与行尾补白，只留下内容本身。
func normLine(s string) string {
	s = stripANSI(s)
	s = strings.ReplaceAll(s, "▍ ", "  ")
	return strings.TrimRight(s, " ")
}

// isRuleLine 判断某行是否为纯分隔线。
func isRuleLine(line string) bool {
	s := strings.TrimSpace(stripANSI(line))
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '─' && r != '-' {
			return false
		}
	}
	return true
}

// TestRunCancelFlow 断言取消迁移后进入结果屏并显示"已取消"。
func TestRunCancelFlow(t *testing.T) {
	m := newTestModel(t)
	rs := &runScreen{}
	rs.Setup(m.sess, m.env())
	rs.step, rs.active = stepRun, true
	rs.stats = migrate.Stats{Done: 2, Total: 5, Uploaded: 2}
	m.cur = rs

	// esc → 确认取消 → 真正取消（用可控的 cancel 观察调用）
	canceled := false
	m.sess.Cancel = func() { canceled = true }
	drive(t, m, "esc")
	if !rs.confirmCancel {
		t.Fatal("esc 应弹出取消确认")
	}
	drive(t, m, "enter")
	if !canceled {
		t.Fatal("确认后应触发取消")
	}
	if rs.confirmCancel {
		t.Fatal("确认后应关闭弹层")
	}

	// 业务层返回取消错误 → 应进入结果屏并标记已取消
	rep := &migrate.Report{Uploaded: 2, UniqueURLs: 5}
	runCmds(t, m, func() tea.Msg { return runDoneMsg{rep: rep, err: context.Canceled} })
	if m.cur.Step() != stepReport {
		t.Fatalf("取消后应进入结果屏，实际 %d", m.cur.Step())
	}
	if !m.sess.Canceled {
		t.Fatal("会话应标记为已取消")
	}
	view := m.View()
	if !strings.Contains(stripANSI(view), "已取消") {
		t.Fatalf("结果屏应显示已取消：\n%s", stripANSI(view))
	}
	if strings.Contains(stripANSI(view), "备份 2 个") {
		t.Fatal("取消时不应声称生成备份（替换未执行）")
	}
}

// TestRunScreenGuard 断言没有扫描结果时迁移屏不会 panic。
func TestRunScreenGuard(t *testing.T) {
	m := newTestModel(t)
	m.sess.Tree = nil
	m.cur = newScreen(stepRun)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	if m.cur.Step() == stepRun {
		t.Fatal("缺少扫描结果时应离开迁移屏")
	}
	_ = m.View()
}

// TestDigitJumpBlocksMigrationSteps 断言 5/6 不能直达（必须经预检确认）。
func TestDigitJumpBlocksMigrationSteps(t *testing.T) {
	m := newTestModel(t)
	m.sess.Reached = stepReport
	drive(t, m, "5")
	if m.cur.Step() != stepSelect {
		t.Fatalf("不应跳到迁移屏，实际 %d", m.cur.Step())
	}
	drive(t, m, "6")
	if m.cur.Step() != stepSelect {
		t.Fatalf("不应跳到结果屏，实际 %d", m.cur.Step())
	}
	drive(t, m, "3")
	if m.cur.Step() != stepTarget {
		t.Fatalf("1–4 应可跳转，实际 %d", m.cur.Step())
	}
}

// TestPreflightReturnTo 断言从预检去改来源/目标后，esc 回到预检。
func TestPreflightReturnTo(t *testing.T) {
	m := newTestModel(t)
	m.sess.Reached = stepPreflight
	m.cur = newScreen(stepPreflight)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	drive(t, m, "e")
	if m.cur.Step() != stepSource {
		t.Fatalf("e 应跳到来源屏，实际 %d", m.cur.Step())
	}
	drive(t, m, "esc")
	if m.cur.Step() != stepPreflight {
		t.Fatalf("esc 应回到预检屏，实际 %d", m.cur.Step())
	}
	drive(t, m, "e")
	drive(t, m, "tab") // 焦点在文本字段时数字属于输入，先移到选项字段（见规格 §5.1"非输入态"）
	drive(t, m, "2")   // 主动跳到选择屏 → 清掉"退回预检"标记
	if m.cur.Step() != stepSelect {
		t.Fatalf("应跳到选择屏，实际 %d", m.cur.Step())
	}
	drive(t, m, "esc")
	if m.cur.Step() == stepPreflight {
		t.Fatal("数字跳转后不应再保留预检回退（应回来源屏）")
	}
}

// TestNumberFieldTyping 断言数字字段可直接键入（相册 ID 这类大数值不能只靠 ←→）。
func TestNumberFieldTyping(t *testing.T) {
	m := newTestModel(t)
	m.sess.Target.Advanced = true
	m.cur = newScreen(stepTarget)
	m.gen++
	runCmds(t, m, m.cur.Init(m.sess, m.env()))
	ts := m.cur.(*targetScreen)
	for i := 0; i < 20 && ts.form.focusName() != "album"; i++ {
		drive(t, m, "tab")
	}
	if ts.form.focusName() != "album" {
		t.Fatalf("应能 tab 到相册 ID，当前 %q", ts.form.focusName())
	}
	drive(t, m, "1", "2", "3")
	if m.sess.Target.AlbumID != 123 {
		t.Fatalf("连续键入 1 2 3 应得到 123，实际 %d", m.sess.Target.AlbumID)
	}
	drive(t, m, "backspace")
	if m.sess.Target.AlbumID != 12 {
		t.Fatalf("退格应回到 12，实际 %d", m.sess.Target.AlbumID)
	}
	drive(t, m, "right")
	if m.sess.Target.AlbumID != 13 {
		t.Fatalf("→ 应 +1 到 13，实际 %d", m.sess.Target.AlbumID)
	}
	drive(t, m, "9")
	if m.sess.Target.AlbumID != 9 {
		t.Fatalf("停顿后键入应重新开始，实际 %d", m.sess.Target.AlbumID)
	}
	// 并发字段范围 1–8：键入 99 应被夹到 8
	for i := 0; i < 20 && ts.form.focusName() != "conc"; i++ {
		drive(t, m, "tab")
	}
	drive(t, m, "9", "9")
	if m.sess.Target.Concurrency != 8 {
		t.Fatalf("并发应被夹到上限 8，实际 %d", m.sess.Target.Concurrency)
	}
}
