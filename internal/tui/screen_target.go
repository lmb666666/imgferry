// 目标：连接（图床 / 地址 / 凭据 / 测试连接 / 存储策略）+ 上传选项 + 文件选项 + 高级。
// 最短路径只需填「站点地址 + 凭据」两项，其余全部有默认值。
package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/upload/lsky"
)

type targetScreen struct {
	base
	form      *form
	modal     *selectModal
	testing   bool
	hostType  string
	testedKey string // 已测试过的 (地址|凭据) 组合，避免重复请求
	shapeKey  string // 凭据方式 + 高级项：变化时才重建字段列表
	prevFocus string // 上一焦点字段：只在离开凭据字段时自动测试
}

type testDoneMsg struct {
	profile    *lsky.Profile
	strategies []lsky.Strategy
	connErr    error // 连接/鉴权失败
	stratErr   error // 连接成功但策略列表拉取失败（可手填 ID，不算连接失败）
}

func (s *targetScreen) Init(sess *Session, env *env) tea.Cmd {
	s.Setup(sess, env)
	s.step = stepTarget
	s.hostType = "lsky"
	if sess.Target.Type == "" {
		sess.Target.Type = "lsky"
	}
	if sess.Target.CredMode == "" {
		if sess.Target.Token == "" && sess.Target.Email != "" {
			sess.Target.CredMode = "password"
		} else {
			sess.Target.CredMode = "token"
		}
	}
	s.buildForm("")
	s.shapeKey = s.currentShape()
	return s.form.focus(0)
}

// buildForm 重建字段列表；keepFocus 是重建后要保持焦点的字段名。
func (s *targetScreen) buildForm(keepFocus string) {
	t := &s.sess.Target
	host := newSelectField("图床", "目前支持兰空 Lsky Pro（GitHub / R2 在计划中）",
		&s.hostType, []option{{"lsky", "兰空 Lsky Pro"}}, false)
	host.name = "host"
	url := newTextField("站点地址", "兰空站点根地址", &t.BaseURL, "https://img.example.com")
	url.name = "url"
	url.validate = func() string {
		v := strings.TrimSpace(t.BaseURL)
		if v == "" {
			return "站点地址不能为空"
		}
		if !strings.Contains(v, "://") {
			return "需要带协议，如 https://img.example.com"
		}
		return ""
	}
	cred := newSelectField("凭据", "开源 V2.x 后台没有 token 入口时用邮箱密码换取",
		&t.CredMode, []option{{"token", "Token"}, {"password", "邮箱 + 密码"}}, true)
	cred.name = "cred"

	fields := []*field{host, url, cred}
	switch t.CredMode {
	case "password":
		email := newTextField("邮箱", "仅用于换取 token，不会落盘", &t.Email, "you@example.com")
		email.name = "email"
		email.validate = func() string {
			if strings.TrimSpace(t.Email) == "" {
				return "邮箱不能为空"
			}
			return ""
		}
		pass := newMaskedField("密码", "仅用于换取 token，不会落盘", &t.Password, "密码")
		pass.name = "pass"
		pass.validate = func() string {
			if t.Password == "" {
				return "密码不能为空"
			}
			return ""
		}
		fields = append(fields, email, pass)
	default:
		token := newMaskedField("Token", "站点个人页生成；聚焦时明文便于核对", &t.Token, "1|xxxxxxxx")
		token.name = "token"
		token.validate = func() string {
			if strings.TrimSpace(t.Token) == "" {
				return "Token 不能为空，或改用邮箱 + 密码"
			}
			return ""
		}
		fields = append(fields, token)
	}

	test := newActionField("测试连接", s.testConnection)
	test.name = "test"
	test.desc = "测试站点与凭据；成功后存储策略可下拉选择"
	if s.testing {
		test.note = "正在测试连接…"
		test.noteLevel = "muted"
	} else if t.TestNote != "" {
		test.note = t.TestNote
		test.noteLevel = t.TestLevel
	}

	strategy := s.strategyField()
	fields = append(fields, test, strategy)

	conc := newNumberField("并发", "站点按用户组限流，建议 1–2", &t.Concurrency, 1, 8, "")
	conc.name = "conc"
	conc.validate = func() string {
		if t.Concurrency < 1 {
			return "并发至少为 1"
		}
		return ""
	}
	interval := newTextField("间隔", "两次上传的最小间隔；限流报错时加到 2s", &t.Interval, "1s")
	interval.name = "interval"
	interval.validate = func() string {
		if strings.TrimSpace(t.Interval) == "" {
			return ""
		}
		if d := t.IntervalDuration(); d <= 0 {
			return "格式如 1s / 500ms"
		}
		return ""
	}
	backup := newToggleField("备份原文件", "生成同名 *.bak，只保留首次备份", &t.Backup, "开启（生成同名 *.bak）", "关闭（不可回滚）")
	backup.name = "backup"
	mapPath := newTextField("映射表", "迁移记录，续传与回查都依赖它", &t.MapPath, "migrate-map.json")
	mapPath.name = "map"
	fresh := newToggleField("清空映射表", "开启后忽略历史，全部重新上传", &t.FreshMap, "开启（全部重新上传）", "关闭")
	fresh.name = "fresh"
	adv := newToggleField("高级选项", "相册 · 下载请求头（防盗链）· 映射表路径", &t.Advanced, "收起", "展开（相册 · 下载请求头 · 映射表路径）")
	adv.name = "advanced"
	fields = append(fields, conc, interval, backup, mapPath, fresh, adv)

	if t.Advanced {
		retries := newNumberField("上传重试", "站点限流时的退避重试次数（0 关闭）｜←→ 调整，也可直接输入数字",
			&t.UploadRetries, 0, 5, "次")
		retries.name = "retries"
		fields = append(fields, retries)
		album := newNumberField("相册 ID", "选填：上传到指定相册｜←→ 调整，也可直接输入数字", &t.AlbumID, 0, 999999, "")
		album.name = "album"
		ref := newTextField("Referer", "旧图床有防盗链时填写（下载旧图用）", &t.HeaderReferer, "https://old.host/")
		ref.name = "referer"
		cookie := newTextField("Cookie", "同上，必要时配合 Cookie", &t.HeaderCookie, "")
		cookie.name = "cookie"
		ua := newTextField("User-Agent", "同上，必要时伪造 UA", &t.HeaderUA, "")
		ua.name = "ua"
		fields = append(fields, album, ref, cookie, ua)
	}

	s.form = newForm(fields...)
	s.form.onFocus = s.afterFocus
	s.shapeKey = s.currentShape()
	if keepFocus != "" {
		s.form.focusField(keepFocus)
	}
}

// currentShape 表示会改变字段集合的配置组合。
func (s *targetScreen) currentShape() string {
	t := &s.sess.Target
	return t.CredMode + "|" + strconv.FormatBool(t.Advanced) + "|" + strconv.Itoa(len(t.Strategies))
}

// strategyField 生成存储策略字段：有策略列表时用下拉，否则退化为数字输入。
func (s *targetScreen) strategyField() *field {
	t := &s.sess.Target
	if len(t.Strategies) == 0 {
		f := newTextField("存储策略", "选填；测试连接后可在这里下拉选择", &t.StrategyID, "如 8")
		f.name = "strategy"
		f.validate = func() string {
			v := strings.TrimSpace(t.StrategyID)
			if v == "" {
				return ""
			}
			if _, err := strconv.Atoi(v); err != nil {
				return "需为数字"
			}
			return ""
		}
		return f
	}
	var opts []option
	for _, st := range t.Strategies {
		opts = append(opts, option{Value: strconv.Itoa(st.ID), Label: strconv.Itoa(st.ID) + " · " + st.Name})
	}
	opts = append(opts, option{Value: "", Label: "站点默认策略"})
	f := newSelectField("存储策略", "站点默认策略不可用时可在此改选", &t.StrategyID, opts, false)
	f.name = "strategy"
	return f
}

// afterFocus 在焦点离开"地址 + 凭据"这一组时自动测一次连接（同一组合只测一次）。
func (s *targetScreen) afterFocus(name string) {
	prev := s.prevFocus
	s.prevFocus = name
	if s.testing {
		return
	}
	if !isCredField(prev) || isCredField(name) {
		return // 组内移动不触发，等真正离开这组字段再测
	}
	t := &s.sess.Target
	if strings.TrimSpace(t.BaseURL) == "" {
		return
	}
	if t.CredMode == "token" && strings.TrimSpace(t.Token) == "" {
		return
	}
	if t.CredMode == "password" && (strings.TrimSpace(t.Email) == "" || t.Password == "") {
		return
	}
	key := t.BaseURL + "|" + t.CredMode + "|" + strings.TrimSpace(t.Token) + "|" + strings.TrimSpace(t.Email)
	if key == s.testedKey {
		return
	}
	s.testedKey = key
	s.testConnection()
}

// isCredField 判断字段是否属于"地址 + 凭据"组。
func isCredField(name string) bool {
	switch name {
	case "url", "token", "email", "pass", "cred":
		return true
	}
	return false
}

func (s *targetScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case testDoneMsg:
		s.testing = false
		t := &s.sess.Target
		if msg.connErr != nil {
			t.TestNote, t.TestLevel = testErrorNote(msg.connErr), "err"
			s.buildForm(s.form.focusName())
			return s, notify("err", "测试连接失败："+testErrorNote(msg.connErr))
		}
		t.Strategies = msg.strategies
		note, level := "连接正常", "ok"
		if msg.profile != nil && msg.profile.Name != "" {
			note += " · 账号 " + msg.profile.Name
		}
		if len(msg.strategies) > 0 {
			note += " · 已载入 " + itoa(len(msg.strategies)) + " 个存储策略"
		} else if msg.stratErr != nil {
			note += " · 存储策略列表拉取失败，可在下方手填策略 ID"
			level = "warn"
		}
		t.TestNote, t.TestLevel = note, level
		s.buildForm(s.form.focusName())
		return s, notify(level, note)
	case tea.KeyMsg:
		if s.modal != nil {
			confirmed, _ := s.modal.update(msg)
			if confirmed {
				s.modal = nil
				s.buildForm(s.form.focusName())
			} else if msg.String() == "esc" {
				s.modal = nil
			}
			return s, nil
		}
		if msg.String() == "enter" {
			return s.submit()
		}
		if msg.String() == "t" && !s.form.editing() && !s.testing {
			return s, s.testConnection()
		}
		cmd, openSelect := s.form.update(msg)
		if openSelect {
			if c := s.form.current(); c != nil {
				s.modal = newSelectModal(c)
			}
			return s, cmd
		}
		// 凭据方式、高级项、策略列表变化都会改变字段集合，按需重建并保持焦点。
		if s.currentShape() != s.shapeKey {
			s.buildForm(s.form.focusName())
		}
		return s, cmd
	}
	return s, nil
}

func (s *targetScreen) testConnection() tea.Cmd {
	t := s.sess.Target
	if strings.TrimSpace(t.BaseURL) == "" {
		return notify("err", "请先填写站点地址")
	}
	up, err := t.NewUploader()
	if err != nil {
		return notify("err", err.Error())
	}
	s.testing = true
	s.buildForm(s.form.focusName())
	env := s.env
	go func() {
		prof, err := up.Profile()
		if err != nil {
			env.send(testDoneMsg{connErr: err})
			return
		}
		strats, serr := up.Strategies()
		env.send(testDoneMsg{profile: prof, strategies: strats, stratErr: serr})
	}()
	return nil
}

func (s *targetScreen) submit() (screen, tea.Cmd) {
	if !s.form.validateAll() {
		return s, notify("err", "有字段未填好 · 已跳到第一个出错的字段")
	}
	if err := s.sess.SaveConfig(); err != nil {
		return s, notify("warn", "配置保存失败："+err.Error())
	}
	return s, goTo(stepPreflight)
}

// Digits 供根模型判断数字键归属（数字字段键入优先于步骤跳转）。
func (s *targetScreen) Digits() bool { return s.form != nil && s.form.typingNumber() }

func (s *targetScreen) Editing() bool { return s.modal != nil || (s.form != nil && s.form.editing()) }
func (s *targetScreen) EscLayers() int {
	if s.modal != nil {
		return 1
	}
	return 0
}
func (s *targetScreen) Busy() bool { return s.testing }

func (s *targetScreen) Status() string {
	t := &s.sess.Target
	if t.TestNote != "" {
		return t.TestNote
	}
	return "填好站点地址与凭据后回车下一步；t 可测试连接"
}

func (s *targetScreen) Hint(cw int) string {
	if s.testing {
		return fitHints([]string{hintPair("…", "正在测试连接"), hintPair("?", "帮助")}, cw)
	}
	return km.hint(stepTarget, false, cw)
}

func (s *targetScreen) View(cw, ch int) string {
	if s.modal != nil {
		return strings.Join(s.modal.lines(cw, ch), "\n")
	}
	// 分区标题按字段名插入，保持"节标题 + 字段"的节奏。
	return strings.Join(s.windowedView(cw, ch), "\n")
}

// windowedView 在内容区高度内渲染表单：节标题 + 字段，超出部分滚动。
func (s *targetScreen) windowedView(cw, ch int) []string {
	if s.form != nil {
		s.form.spinner = s.sess.SpinFrame // 测试连接时说明区显示 spinner
	}
	groups := []struct {
		title  string
		fields []string
	}{
		{"连接", []string{"host", "url", "cred", "token", "email", "pass", "test"}},
		{"上传", []string{"strategy", "conc", "interval"}},
		{"文件", []string{"backup", "map", "fresh", "advanced", "retries", "album", "referer", "cookie", "ua"}},
	}
	// 先按"节标题 + 字段"组装整张表单，并记录聚焦字段在其中的行号
	var lines []string
	focusStart, focusEnd := -1, -1
	rendered := map[string][]string{}
	for _, f := range s.form.fields {
		rendered[f.name] = f.lines(s.form.current() == f, cw)
	}
	for _, g := range groups {
		present := false
		for _, name := range g.fields {
			if _, ok := rendered[name]; ok {
				present = true
				break
			}
		}
		if !present {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, sectionTitle(g.title))
		for _, name := range g.fields {
			ls, ok := rendered[name]
			if !ok {
				continue
			}
			if name == s.form.focusName() {
				focusStart = len(lines)
				focusEnd = len(lines) + len(ls)
			}
			lines = append(lines, ls...)
		}
	}
	// 去掉尾随空行
	for len(lines) > 0 && strings.TrimSpace(stripANSI(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	const helpH = 4 // 说明区固定在内容区最后 4 行
	if ch <= 0 || len(lines)+helpH <= ch {
		for len(lines) < ch-helpH { // 表单短时补白，让说明区贴底且位置固定
			lines = append(lines, "")
		}
		return append(lines, s.form.HelpView(cw)...)
	}
	ch -= helpH
	start := 0
	if focusStart >= 0 && focusEnd > focusStart {
		start = focusStart - (ch-(focusEnd-focusStart))/2
	}
	start = maxInt(minInt(start, len(lines)-ch), 0)
	win := append([]string(nil), lines[start:start+ch]...)
	if start > 0 {
		win[0] = stMuted.Render("  ↑ 上方还有内容")
	}
	if start+ch < len(lines) {
		win[len(win)-1] = stMuted.Render("  ↓ 下方还有内容")
	}
	return append(win, s.form.HelpView(cw)...)
}

// testErrorNote 把测试连接的错误翻译成"发生了什么 · 为什么 · 怎么办"。
func testErrorNote(err error) string {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "401") || strings.Contains(low, "unauth") || strings.Contains(msg, "鉴权"):
		return "鉴权失败 · Token 无效或过期 · 到站点个人页重新生成，或改用邮箱密码"
	case strings.Contains(msg, "404") || strings.Contains(msg, "不是兰空"):
		return "接口不存在 · 地址可能不是兰空站点根路径 · 检查站点地址"
	case strings.Contains(low, "timeout") || strings.Contains(msg, "超时"):
		return "连接超时 · 网络不可达或站点无响应 · 检查网络与站点地址"
	case strings.Contains(msg, "频繁"):
		return "站点限流 · 换取 token 的接口限流 3 次/分钟 · 稍后再试"
	default:
		return msg
	}
}
