// 表单字段层：5 种控件（文本 / 路径 / 选择 / 数字 / 开关 / 动作）+ 焦点环 + 内联错误。
// 自绘而非使用现成表单库，是为了拿到统一的两列网格、‹ › 步进器、内联错误行与
// 完全可控的 esc 语义——这也是不用现成表单库的原因。
package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type fieldKind int

const (
	kText fieldKind = iota
	kSelect
	kRadio
	kNumber
	kToggle
	kAction
)

type option struct {
	Value string
	Label string
}

// field 是一个表单字段。值通过指针双向绑定，屏幕读到的始终是最新值。
type field struct {
	kind  fieldKind
	name  string // 焦点保持用的稳定标识
	label string
	desc  string

	strVal  *string
	intVal  *int
	boolVal *bool

	opts          []option
	inlineChoices bool // kSelect：2–3 个选项时渲染为单选

	minV, maxV int
	unit       string

	onText, offText string

	action   func() tea.Cmd
	validate func() string

	input textinput.Model

	// 运行时状态
	err         string
	note        string
	noteLevel   string // ok | warn | err | muted
	placeholder string
	mask        bool

	numBuf string    // 数字字段的键入缓冲
	numAt  time.Time // 上一次数字输入时间
}

func newTextField(label, desc string, val *string, placeholder string) *field {
	f := &field{kind: kText, label: label, desc: desc, strVal: val, placeholder: placeholder}
	f.input = newInput() // 必须初始化：零值 textinput 的 Focus() 会因内部 cursor 未初始化而 panic
	return f
}

func newMaskedField(label, desc string, val *string, placeholder string) *field {
	f := newTextField(label, desc, val, placeholder)
	f.mask = true
	return f
}

func newSelectField(label, desc string, val *string, opts []option, inline bool) *field {
	return &field{kind: kSelect, label: label, desc: desc, strVal: val, opts: opts, inlineChoices: inline}
}

func newNumberField(label, desc string, val *int, minV, maxV int, unit string) *field {
	return &field{kind: kNumber, label: label, desc: desc, intVal: val, minV: minV, maxV: maxV, unit: unit}
}

func newToggleField(label, desc string, val *bool, onText, offText string) *field {
	return &field{kind: kToggle, label: label, desc: desc, boolVal: val, onText: onText, offText: offText}
}

func newActionField(label string, action func() tea.Cmd) *field {
	return &field{kind: kAction, label: label, action: action}
}

// isText 表示该字段接受字符输入（决定单字母命令是否让位）。
func (f *field) isText() bool { return f.kind == kText }

func (f *field) selectIndex() int {
	if f.strVal == nil || len(f.opts) == 0 {
		return -1
	}
	for i, o := range f.opts {
		if o.Value == *f.strVal {
			return i
		}
	}
	return 0
}

// typeNumber 处理数字字段的直接键入；返回是否已消费该按键。
func (f *field) typeNumber(msg tea.KeyMsg) bool {
	if f.kind != kNumber || f.intVal == nil {
		return false
	}
	switch msg.Type {
	case tea.KeyRunes:
		if len(msg.Runes) != 1 || msg.Runes[0] < '0' || msg.Runes[0] > '9' {
			return false
		}
		if time.Since(f.numAt) > 1200*time.Millisecond {
			f.numBuf = "" // 停顿后再输入视为重新开始
		}
		f.numBuf += string(msg.Runes[0])
		f.numAt = time.Now()
		f.applyNumBuf()
		return true
	case tea.KeyBackspace:
		if len(f.numBuf) > 1 {
			f.numBuf = f.numBuf[:len(f.numBuf)-1]
		} else {
			f.numBuf = ""
			*f.intVal /= 10
		}
		f.numAt = time.Now()
		f.applyNumBuf()
		return true
	}
	return false
}

func (f *field) applyNumBuf() {
	if f.numBuf == "" {
		return
	}
	n := 0
	for _, r := range f.numBuf {
		n = n*10 + int(r-'0')
		if f.maxV > 0 && n > f.maxV {
			break
		}
	}
	if f.maxV > 0 && n > f.maxV {
		n = f.maxV
	}
	if n < f.minV {
		n = f.minV
	}
	*f.intVal = n
}

func (f *field) step(delta int) {
	f.numBuf = "" // ←→ 与键入混用时以 ←→ 为准
	switch f.kind {
	case kSelect, kRadio:
		i := f.selectIndex()
		if i < 0 {
			return
		}
		i = (i + delta + len(f.opts)) % len(f.opts)
		*f.strVal = f.opts[i].Value
	case kNumber:
		if f.intVal == nil {
			return
		}
		v := *f.intVal + delta
		if v < f.minV {
			v = f.minV
		}
		if f.maxV > 0 && v > f.maxV {
			v = f.maxV
		}
		*f.intVal = v
	}
}

// syncInput 把绑定值同步进输入控件（进入编辑态前调用）。
func (f *field) syncInput() {
	if f.input.Value() != *f.strVal {
		f.input.SetValue(*f.strVal)
	}
	f.input.Placeholder = f.placeholder
	f.input.CursorEnd()
	if f.mask {
		f.input.EchoMode = textinput.EchoPassword
		f.input.EchoCharacter = '•'
	} else {
		f.input.EchoMode = textinput.EchoNormal
	}
}

func newInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 1024
	ti.TextStyle = stText
	ti.PlaceholderStyle = stFaint
	ti.Cursor.Style = stAccent
	ti.Cursor.TextStyle = stText
	return ti
}

// lines 渲染字段：标签行 + 说明/错误行（值区从第 14 列开始）。
func (f *field) lines(focused bool, width int) []string {
	valueW := maxInt(width-fieldValueX-1, 8)
	label := fieldLabel(f.label, focused)

	var value string
	switch f.kind {
	case kText:
		if focused {
			f.syncInput()
			f.input.Width = valueW - 1
			value = f.input.View()
		} else if *f.strVal == "" {
			value = stFaint.Render(Fit(f.placeholder, valueW))
		} else if f.mask {
			value = stText.Render(strings.Repeat("•", minInt(len([]rune(*f.strVal)), maxInt(valueW/2, 4))))
		} else {
			value = stText.Render(Truncate(*f.strVal, valueW))
		}
	case kSelect:
		label := ""
		if i := f.selectIndex(); i >= 0 {
			label = f.opts[i].Label
		}
		g := glyphsFor()
		if f.inlineChoices {
			var parts []string
			mark := "( ) "
			for _, o := range f.opts {
				if o.Value == *f.strVal {
					mark = "(•) "
				} else {
					mark = "( ) "
				}
				if focused && o.Value == *f.strVal {
					parts = append(parts, stAccent.Render(mark+o.Label))
				} else {
					parts = append(parts, stMuted.Render(mark)+stText.Render(o.Label))
				}
			}
			value = strings.Join(parts, "  ")
		} else {
			style := stText
			if focused {
				style = stAccentB
			}
			value = stMuted.Render(g.QuoteLeft+" ") + style.Render(Truncate(label, valueW-4)) +
				stMuted.Render(" "+g.QuoteRight)
		}
	case kNumber:
		v := ""
		if f.intVal != nil {
			v = itoa(*f.intVal)
		}
		style := stText
		if focused {
			style = stAccentB
		}
		value = style.Render(v)
		if f.unit != "" {
			value += stMuted.Render(" " + f.unit)
		}
	case kToggle:
		g := glyphsFor()
		on := f.boolVal != nil && *f.boolVal
		box, text := g.SelNone, f.offText
		if on {
			box, text = g.SelAll, f.onText
		}
		boxStyle := stMuted
		if on {
			boxStyle = stOKBox
		}
		if focused {
			boxStyle = boxStyle.Bold(true)
		}
		value = boxStyle.Render(box) + " " + stText.Render(Truncate(text, maxInt(valueW-4, 8)))
	case kAction:
		if focused {
			value = stAccent.Render("[ enter 执行 ]")
		} else {
			value = stFaint.Render("按 tab 移到这里后回车")
		}
	}

	// 只渲染一行：说明、错误、提示都放到表单下方的固定说明区，
	// 这样焦点在字段间移动时整屏高度与内容位置都不变。
	return []string{label + value}
}

// helpLines 返回固定说明区的内容：聚焦字段的说明 + 错误/提示（错误优先）。
// 焦点移开时，其他字段的错误仍会显示出来（带字段名），保证错误不会被藏起来。
func (f *form) helpLines() (label, desc, status, level string) {
	cur := f.current()
	if cur != nil {
		label, desc = cur.label, cur.desc
		switch {
		case cur.err != "":
			status, level = cur.err, "err"
		case cur.note != "":
			status, level = cur.note, cur.noteLevel
		}
	}
	if status == "" {
		for _, fd := range f.fields {
			if fd.err != "" {
				status, level = fd.label+"："+fd.err, "err"
				break
			}
		}
	}
	return label, desc, status, level
}

// HelpView 渲染固定说明区（分隔线 + 说明行 + 状态行），高度恒为 4 行。
func (f *form) HelpView(width int) []string {
	label, desc, status, level := f.helpLines()
	if f.spinner != "" && status != "" && f.current() != nil && f.current().note == status {
		status = f.spinner + " " + status
	}
	line1 := ""
	if desc != "" {
		line1 = "  " + stMuted.Render(Fit(label, fieldLabelW)) + "  " + stMuted.Render(Truncate(desc, width-fieldLabelW-5))
	}
	line2 := ""
	if status != "" {
		// 文案自带字形时不再补一个，避免出现"✓ ✓ 连接正常"
		g := levelGlyph(level)
		prefix := g
		if prefix != "" && (strings.HasPrefix(status, g) || strings.HasPrefix(status, "✓") ||
			strings.HasPrefix(status, "✗") || strings.HasPrefix(status, "⚠")) {
			prefix = ""
		} else if prefix != "" {
			prefix += " "
		}
		line2 = "  " + strings.Repeat(" ", fieldLabelW+2) + styleFor(level).Render(Truncate(prefix+status, width-fieldLabelW-5))
	}
	return []string{"", ruleLine(width), line1, line2}
}

// ---- 表单（焦点环） ----

type form struct {
	fields   []*field
	idx      int
	onChange func()
	onFocus  func(name string)
	spinner  string // 当前 spinner 帧（用于"正在…"类状态）
}

func newForm(fields ...*field) *form { return &form{fields: fields} }

func (f *form) focus(i int) tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}
	for _, fd := range f.fields {
		if fd.isText() {
			fd.input.Blur()
		}
	}
	n := len(f.fields)
	f.idx = ((i % n) + n) % n
	cur := f.fields[f.idx]
	if cur.isText() {
		cur.syncInput()
		cur.input.Focus()
	}
	if f.onFocus != nil {
		f.onFocus(cur.name)
	}
	return nil
}

// focusField 按名称聚焦（重建字段列表后保持焦点用）。
func (f *form) focusField(name string) {
	for i, fd := range f.fields {
		if fd.name == name {
			f.idx = i
			return
		}
	}
	f.idx = minInt(f.idx, maxInt(len(f.fields)-1, 0))
}

// focusName 返回当前聚焦字段的名称。
func (f *form) focusName() string {
	if c := f.current(); c != nil {
		return c.name
	}
	return ""
}

func (f *form) current() *field {
	if len(f.fields) == 0 {
		return nil
	}
	return f.fields[f.idx]
}

// typingNumber 表示当前字段在消费数字键（数字跳步让位）。
func (f *form) typingNumber() bool {
	c := f.current()
	return c != nil && c.kind == kNumber
}

// editing 表示当前处于文本输入态（L1）：此时单字母命令让位给输入。
func (f *form) editing() bool {
	c := f.current()
	return c != nil && c.isText()
}

// update 处理按键；openSelect 为 true 时屏幕需要打开选项覆盖层。
func (f *form) update(msg tea.KeyMsg) (cmd tea.Cmd, openSelect bool) {
	c := f.current()
	if c == nil {
		return nil, false
	}
	key := msg.String()
	switch key {
	case "tab", "down":
		return f.focus(f.idx + 1), false
	case "shift+tab", "up":
		return f.focus(f.idx - 1), false
	case "enter", "esc", "ctrl+c":
		return nil, false // 由屏幕与根模型处理
	case "left":
		if c.kind == kSelect || c.kind == kRadio || c.kind == kNumber {
			c.step(-1)
			f.changed()
			return nil, false
		}
	case "right":
		if c.kind == kSelect || c.kind == kRadio || c.kind == kNumber {
			c.step(1)
			f.changed()
			return nil, false
		}
	case " ":
		switch c.kind {
		case kToggle:
			if c.boolVal != nil {
				*c.boolVal = !*c.boolVal
				f.changed()
			}
			return nil, false
		case kSelect:
			if len(c.opts) > 2 || !c.inlineChoices {
				return nil, true // 打开选项覆盖层
			}
			c.step(1)
			f.changed()
			return nil, false
		case kAction:
			if c.action != nil {
				return c.action(), false
			}
			return nil, false
		}
	}
	if c.isText() {
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		*c.strVal = c.input.Value()
		c.err = ""
		f.changed()
		return cmd, false
	}
	if c.typeNumber(msg) {
		c.err = ""
		f.changed()
		return nil, false
	}
	return nil, false
}

func (f *form) changed() {
	if f.onChange != nil {
		f.onChange()
	}
}

// validateAll 执行全部字段校验，聚焦第一个出错字段并返回是否通过。
func (f *form) validateAll() bool {
	ok := true
	firstErr := -1
	for i, fd := range f.fields {
		fd.err = ""
		if fd.validate != nil {
			if msg := fd.validate(); msg != "" {
				fd.err = msg
				ok = false
				if firstErr < 0 {
					firstErr = i
				}
			}
		}
	}
	if firstErr >= 0 {
		f.idx = firstErr
	}
	return ok
}

// view 渲染整个表单（字段之间 1 行空行分隔）。
func (f *form) view(width int) []string {
	lines, _, _ := f.render(width)
	return lines
}

// render 返回全部字段的渲染行，以及"聚焦字段所占的行区间"（供窗口化定位）。
func (f *form) render(width int) (lines []string, focusStart, focusEnd int) {
	focusStart, focusEnd = -1, -1
	for i, fd := range f.fields {
		if i == f.idx {
			focusStart = len(lines)
		}
		lines = append(lines, fd.lines(i == f.idx, width)...)
		if i == f.idx {
			focusEnd = len(lines)
		}
	}
	return lines, focusStart, focusEnd
}

// viewWindow 渲染表单的可见窗口：保证聚焦字段完整可见，其余按需滚动。
func (f *form) viewWindow(width, height int) []string {
	lines, fs, fe := f.render(width)
	if height <= 0 || len(lines) <= height {
		return lines
	}
	start := 0
	if fs >= 0 && fe > fs {
		start = fs - (height-(fe-fs))/2 // 尽量让聚焦字段居中
	}
	start = maxInt(minInt(start, len(lines)-height), 0)
	win := lines[start : start+height]
	// 上下滚动指示：不影响行数
	if start > 0 {
		win[0] = stMuted.Render("  ↑ 上方还有内容")
	}
	if start+height < len(lines) {
		win[len(win)-1] = stMuted.Render("  ↓ 下方还有内容")
	}
	return win
}

// selectModal 是选项覆盖层的状态。
type selectModal struct {
	field *field
	idx   int
}

func newSelectModal(fd *field) *selectModal {
	m := &selectModal{field: fd, idx: fd.selectIndex()}
	if m.idx < 0 {
		m.idx = 0
	}
	return m
}

func (m *selectModal) update(msg tea.KeyMsg) (confirmed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.idx = maxInt(m.idx-1, 0)
	case "down", "j":
		m.idx = minInt(m.idx+1, len(m.field.opts)-1)
	case "enter", " ":
		if len(m.field.opts) > 0 {
			*m.field.strVal = m.field.opts[m.idx].Value
		}
		return true, nil
	}
	return false, nil
}

func (m *selectModal) lines(w, h int) []string {
	inner := minInt(48, maxInt(w-12, 20))
	g := glyphsFor()
	// 选项多时窗口化：最多 8 行，光标始终可见（策略列表可能有几十条）。
	const maxVisible = 8
	start := 0
	if m.idx >= maxVisible {
		start = m.idx - maxVisible + 1
	}
	end := minInt(start+maxVisible, len(m.field.opts))
	title := m.field.label
	if len(m.field.opts) > maxVisible {
		title += "（" + itoa(start+1) + "-" + itoa(end) + " / " + itoa(len(m.field.opts)) + "）"
	}
	var rows []string
	for i := start; i < end; i++ {
		o := m.field.opts[i]
		cursor := " "
		style := stText
		if i == m.idx {
			cursor = stAccent.Render(g.Cursor)
			style = stAccentB
		}
		rows = append(rows, cursor+" "+style.Render(Truncate(o.Label, inner-6)))
	}
	hint := "↑↓ 选择 · enter 确认 · esc 取消"
	if end < len(m.field.opts) || start > 0 {
		hint = "↑↓ 选择（共 " + itoa(len(m.field.opts)) + " 项）· enter 确认 · esc 取消"
	}
	return modalLines(title, rows, hint, w, h)
}

// debounceMsg 用于路径字段的延迟校验。
type debounceMsg struct {
	gen  uint64
	tag  string
	when time.Time
}
