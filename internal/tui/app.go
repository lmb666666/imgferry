// 根模型：固定框架（步骤轨 / 内容 / 页脚）、屏幕路由、全局键、通知与帮助覆盖层。
// 异步任务用带世代号的 tea.Program.Send 回报，切屏或取消后旧消息自动作废。
package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/config"
)

// screen 是一个步骤屏幕。屏幕之间不互相调用，只上报导航/通知意图。
type screen interface {
	Step() int
	Init(sess *Session, env *env) tea.Cmd
	Update(msg tea.Msg) (screen, tea.Cmd)
	View(cw, ch int) string // 内容区尺寸（宽 = 内容宽，高 = 内容区高）
	Status() string         // 页脚状态摘要
	Hint(cw int) string     // 页脚键位行（按可用宽度裁剪）
	Editing() bool          // 文本输入态：q / ? / 数字等单字母命令让位
	EscLayers() int         // esc 需要先在本屏剥掉的层数（0 = 直接返回上一屏）
	Busy() bool             // 是否有异步任务在跑（决定 spinner 是否继续走帧）
	Digits() bool           // 当前控件在消费数字键（数字字段键入），数字跳步让位
}

// base 提供屏幕的默认实现，具体屏幕按需覆盖。
type base struct {
	step   int
	sess   *Session
	env    *env
	status string
}

func (b *base) Step() int                     { return b.step }
func (b *base) Status() string                { return b.status }
func (b *base) Hint(cw int) string            { return km.hint(b.step, false, cw) }
func (b *base) Editing() bool                 { return false }
func (b *base) EscLayers() int                { return 0 }
func (b *base) Busy() bool                    { return false }
func (b *base) Digits() bool                  { return false }
func (b *base) Setup(sess *Session, env *env) { b.sess, b.env = sess, env }

// 屏幕之间传递的意图。
type gotoMsg struct{ step int }
type notifyMsg struct {
	level string
	text  string
	ttl   time.Duration
}
type quitMsg struct{}

func goTo(step int) tea.Cmd { return func() tea.Msg { return gotoMsg{step} } }
func notify(level, text string) tea.Cmd {
	return func() tea.Msg { return notifyMsg{level: level, text: text} }
}
func notifyTTL(level, text string, ttl time.Duration) tea.Cmd {
	return func() tea.Msg { return notifyMsg{level: level, text: text, ttl: ttl} }
}

type noticeExpireMsg struct{}

// Model 是根模型。
type Model struct {
	prog    *tea.Program
	keys    keyMap
	spin    spinner.Model
	version string

	w, h int
	gen  uint64

	sess *Session
	cur  screen

	noti     notice
	helpOpen bool
	quitAsk  bool

	initial tea.Cmd
}

// Run 启动交互界面。version 用于首页展示。
func Run(version string, forceASCII bool) error {
	m := newModel(version, forceASCII)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	m.prog = p
	_, err := p.Run()
	return err
}

func newModel(version string, forceASCII bool) *Model {
	cfg, err := config.Load()
	cfgErr := err
	if err != nil {
		cfg = config.Default()
	}
	if forceASCII || cfg.UI.ASCII {
		setASCII(true)
	}
	sess := newSession(cfg)
	sess.SpinFrame = firstSpinFrame()
	m := &Model{keys: km, spin: spinnerFor(), sess: sess, version: version}
	m.cur = newScreen(stepStart)
	m.initial = tea.Batch(m.cur.Init(sess, m.env()), m.spin.Tick)
	if cfgErr != nil {
		m.initial = tea.Batch(m.initial, notify("warn", "配置文件读取失败，已用默认配置启动："+truncateErr(cfgErr)))
	}
	return m
}

func (m *Model) env() *env {
	g := m.gen
	return &env{gen: g, emit: func(msg tea.Msg) {
		if m.prog != nil {
			m.prog.Send(genMsg{gen: g, inner: msg})
		}
	}}
}

// newScreen 按步骤构造屏幕。
func newScreen(step int) screen {
	switch step {
	case stepSource:
		return &sourceScreen{}
	case stepSelect:
		return &selectScreen{}
	case stepTarget:
		return &targetScreen{}
	case stepPreflight:
		return &preflightScreen{}
	case stepRun:
		return &runScreen{}
	case stepReport:
		return &reportScreen{}
	default:
		return &startScreen{}
	}
}

func (m *Model) Init() tea.Cmd { return m.initial }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case genMsg:
		if msg.gen != m.gen { // 过期世代：用户已取消或切屏
			return m, nil
		}
		return m.dispatch(msg.inner)
	case tea.KeyMsg:
		return m.key(msg)
	case tea.MouseMsg:
		if m.helpOpen || m.quitAsk {
			return m, nil
		}
		return m, m.forward(msg)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		m.sess.SpinFrame = m.spin.View()
		if m.cur.Busy() {
			return m, tea.Batch(cmd, m.forward(msg))
		}
		return m, cmd
	}
	return m.dispatch(msg)
}

func (m *Model) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case gotoMsg:
		return m.gotoStep(msg.step)
	case notifyMsg:
		return m, m.notify(msg.level, msg.text, msg.ttl)
	case quitMsg:
		return m, tea.Quit
	case noticeExpireMsg:
		if !m.noti.until.IsZero() && !time.Now().Before(m.noti.until) {
			m.noti = notice{}
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		next, dcmd := m.cur.Update(msg)
		if next != nil {
			m.cur = next
		}
		return m, tea.Batch(cmd, dcmd)
	}
	next, cmd := m.cur.Update(msg)
	if next != nil {
		m.cur = next
	}
	return m, cmd
}

// forward 把消息交给当前屏幕。
func (m *Model) forward(msg tea.Msg) tea.Cmd {
	next, cmd := m.cur.Update(msg)
	if next != nil {
		m.cur = next
	}
	return cmd
}

// gotoStep 切屏：世代号 +1 使旧任务的异步消息作废，会话数据保留。
func (m *Model) gotoStep(step int) (tea.Model, tea.Cmd) {
	step = minInt(maxInt(step, stepStart), stepReport)
	m.gen++
	m.sess.Step = step
	if step >= 0 {
		m.sess.Reached = maxInt(m.sess.Reached, step)
	}
	m.cur = newScreen(step)
	cmd := m.cur.Init(m.sess, m.env())
	m.helpOpen, m.quitAsk = false, false
	return m, cmd
}

// key 处理按键：覆盖层 → 内层 → 全局 → 屏幕。
func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 强制中断：立即退出；迁移中先取消迁移并留在结果屏。
	if msg.Type == tea.KeyCtrlC {
		if m.helpOpen {
			m.helpOpen = false
			return m, nil
		}
		if r, ok := m.cur.(*runScreen); ok && r.running() {
			return m, r.requestCancel(m)
		}
		if m.sess.Cancel != nil {
			m.sess.Cancel()
		}
		return m, tea.Quit
	}
	if m.helpOpen {
		switch msg.String() {
		case "esc", "?", "q", "enter":
			m.helpOpen = false
		}
		return m, nil
	}
	if m.quitAsk {
		switch msg.String() {
		case "enter", "y":
			return m, tea.Quit
		case "esc", "q", "n":
			m.quitAsk = false
		}
		return m, nil
	}
	// esc：先剥本屏的层（过滤编辑 / 过滤生效 / 本屏弹层），再回上一屏。
	if msg.Type == tea.KeyEsc {
		if m.noti.level == "err" {
			m.noti = notice{} // 常驻错误由 esc 清除（§7.2）
		}
		if m.cur.EscLayers() > 0 {
			return m, m.forward(msg)
		}
		if m.cur.Step() == stepStart {
			return m, nil
		}
		// 从预检跳来改配置时，esc 回到预检而不是上一屏。
		if to := m.sess.ReturnTo; to >= 0 && to != m.cur.Step() {
			m.sess.ReturnTo = -1
			return m.gotoStep(to)
		}
		m.sess.ReturnTo = -1
		if m.sess.Reached <= 0 || m.cur.Step() == stepSource {
			return m.gotoStep(stepStart)
		}
		return m.gotoStep(m.cur.Step() - 1)
	}
	// 文本输入态：单字母命令让位给输入。
	if m.cur.Editing() {
		return m, m.forward(msg)
	}
	switch msg.String() {
	case "q":
		if r, ok := m.cur.(*runScreen); ok && r.running() {
			m.quitAsk = true
			return m, nil
		}
		return m, tea.Quit
	case "?":
		m.helpOpen = true
		return m, nil
	case "1", "2", "3", "4":
		if m.cur.Digits() {
			return m, m.forward(msg) // 数字是当前字段的输入内容
		}
		n := int(msg.String()[0] - '0')
		if n-1 <= m.sess.Reached && n-1 != m.cur.Step() {
			m.sess.ReturnTo = -1
			return m.gotoStep(n - 1)
		}
		return m, nil
	case "5", "6":
		if m.cur.Digits() {
			return m, m.forward(msg)
		}
		// 迁移与结果不提供直达：必须先过预检确认（安全默认）。
		return m, notify("warn", "迁移与结果不能直接跳转 · 请经预检确认后开始")
	}
	return m, m.forward(msg)
}

// tickFn 由测试替换，避免真实等待定时器。
var tickFn = tea.Tick

// notify 设置页脚通知；瞬时通知到期后自动回落状态摘要。
func (m *Model) notify(level, text string, ttl time.Duration) tea.Cmd {
	n := notice{text: text, level: level}
	if level == "err" {
		m.noti = n // 错误常驻，直到用户操作或 esc 清除
		return nil
	}
	if ttl <= 0 {
		ttl = noticeTTL
	}
	n.until = time.Now().Add(ttl)
	m.noti = n
	return tickFn(ttl, func(time.Time) tea.Msg { return noticeExpireMsg{} })
}

func (m *Model) View() string {
	if m.w == 0 {
		m.w, m.h = 100, 30
	}
	cw := contentWidth(m.w)
	ch := maxInt(m.h-4, 1)

	railStr := rail(m.cur.Step(), cw, brandText())
	body := ""
	switch {
	case m.helpOpen:
		body = helpOverlay(m.cur.Step(), cw, ch)
	case m.quitAsk:
		body = quitConfirm(cw, ch, m.sess.Canceled)
	default:
		body = m.cur.View(cw, ch)
	}
	noticeStr := renderNotice(m.noti, m.cur.Status(), time.Now())
	hintStr := m.cur.Hint(cw)
	if m.helpOpen {
		hintStr = hintPair("esc", "关闭帮助")
	}
	if m.quitAsk {
		hintStr = hintPair("enter", "退出") + " " + stMuted.Render(glyphsFor().Dot) + " " + hintPair("esc", "继续")
	}
	return renderFrame(m.w, m.h, railStr, body, noticeStr, hintStr)
}

// firstSpinFrame 返回 spinner 的首帧，保证异步开始前的瞬间也有字形可显示。
func firstSpinFrame() string {
	m := spinnerFor()
	if f := m.Spinner.Frames; len(f) > 0 {
		return f[0]
	}
	return "*"
}

// truncateErr 把错误压成一行，避免通知行过长。
func truncateErr(err error) string {
	if err == nil {
		return ""
	}
	return Truncate(strings.ReplaceAll(err.Error(), "\n", " "), 60)
}

// brandText 是步骤轨左侧的品牌文字（ASCII 模式去掉锚点字形）。
func brandText() string {
	if asciiEnabled() {
		return "imgferry"
	}
	return "⚓ imgferry"
}

// helpOverlay 是帮助覆盖层：本屏键位 + 全局键位（两列）。
func helpOverlay(step, cw, ch int) string {
	local, global := km.helpRows(step)
	inner := minInt(72, maxInt(cw-8, 30))
	var rows []string
	rows = append(rows, stMuted.Render("本屏"))
	rows = append(rows, twoColPairs(local, inner)...)
	rows = append(rows, "", stMuted.Render("全局"))
	rows = append(rows, twoColPairs(global, inner)...)
	return strings.Join(modalLines("帮助", rows, "esc / ? 关闭", cw, ch), "\n")
}

func twoColPairs(pairs [][2]string, inner int) []string {
	colW := (inner - 4) / 2
	half := (len(pairs) + 1) / 2
	out := make([]string, 0, half)
	for i := 0; i < half; i++ {
		left := pairCell(pairs[i], colW)
		right := ""
		if i+half < len(pairs) {
			right = pairCell(pairs[i+half], colW)
		}
		out = append(out, left+right)
	}
	return out
}

func pairCell(p [2]string, w int) string {
	keyW := 9
	if w-keyW < 4 {
		return " " + stAccent.Render(Truncate(p[0], w))
	}
	return " " + stAccent.Render(Fit(p[0], keyW)) + stMuted.Render(Truncate(p[1], w-keyW-1))
}

// quitConfirm 是退出确认：说明已保留的内容，默认安全项。
func quitConfirm(cw, ch int, canceled bool) string {
	rows := []string{
		"",
		"已上传的图片会写入映射表保留，未处理的链接下次可续传；",
		"来源文件不会被回滚。",
		"",
		"  ▸ 继续         退出",
	}
	return strings.Join(modalLines("退出 imgferry？", rows, "enter 退出 · esc 继续", cw, ch), "\n")
}
