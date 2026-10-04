// 选择：分组清单 + 三态勾选 + 过滤 + 详情栏（全流程的主屏）。
package tui

import (
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/store"
)

type selectScreen struct {
	base
	tree       *checkTree
	detailOpen bool
	filtering  bool
	viewH      int // 最近一次渲染的内容区高度，供鼠标命中测试使用
}

func (s *selectScreen) Init(sess *Session, env *env) tea.Cmd {
	s.Setup(sess, env)
	s.step = stepSelect
	if sess.Tree == nil {
		return goTo(stepSource)
	}
	s.tree = sess.Tree
	s.tree.fresh = sess.Target.FreshMap
	s.tree.dim = groupDimOf(sess.cfg.UI.GroupBy)
	s.tree.rebuild()
	s.tree.idx = 0
	return s.refreshMapped()
}

// refreshMapped 标记映射表已有的 URL（状态行与"已映射"后缀都依赖它）。
func (s *selectScreen) refreshMapped() tea.Cmd {
	mapped, err := loadMapKeys(s.sess.Target.MapPathValue())
	if err != nil {
		return notify("warn", "映射表读取失败："+err.Error())
	}
	s.tree.mapped = mapped
	return nil
}

func (s *selectScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if mouse, ok := msg.(tea.MouseMsg); ok {
		return s, s.handleMouse(mouse)
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	if s.filtering {
		switch key.Type {
		case tea.KeyEnter:
			s.filtering = false
		case tea.KeyEsc:
			s.filtering = false // 退出编辑但保留过滤；再按一次 esc 才清除（见 §4.3 阶梯）
		case tea.KeyBackspace:
			r := []rune(s.tree.filter)
			if len(r) > 0 {
				s.tree.filter = string(r[:len(r)-1])
				s.tree.rebuild()
			}
		case tea.KeyRunes, tea.KeySpace:
			s.tree.filter += key.String()
			s.tree.rebuild()
		}
		return s, nil
	}
	if s.detailOpen {
		switch key.String() {
		case "esc", "i", "enter":
			s.detailOpen = false
		case "y":
			return s, s.copyURL()
		}
		return s, nil
	}
	switch key.String() {
	case "up", "k":
		s.tree.Move(-1)
	case "down", "j":
		s.tree.Move(1)
	case "pgup":
		s.tree.Move(-10)
	case "pgdn":
		s.tree.Move(10)
	case "home", "g":
		s.tree.Top()
	case "end", "G":
		s.tree.Bottom()
	case "esc":
		if s.tree.filter != "" {
			s.tree.filter = ""
			s.tree.rebuild()
		}
	case "left":
		s.tree.SetFold(true)
	case "right":
		s.tree.SetFold(false)
	case " ":
		s.tree.Toggle()
	case "a":
		s.tree.SelectAll()
	case "u":
		s.tree.Invert()
	case "z":
		s.tree.Fold()
	case "Z":
		s.tree.FoldAll()
	case "v":
		s.tree.CycleGroup()
		s.sess.cfg.UI.GroupBy = s.tree.dim.key()
	case "/":
		s.filtering = true
	case "i":
		if s.tree.CursorURL() == "" {
			return s, notify("warn", "分组行没有详情，移到具体图片上再按 i")
		}
		s.detailOpen = true
	case "y":
		return s, s.copyURL()
	case "enter":
		if s.tree.CheckedCount() == 0 {
			return s, notify("err", "请至少选择一张图片")
		}
		return s, goTo(stepTarget)
	}
	return s, nil
}

// handleMouse 支持滚轮滚动与点击选中（见规格 §7.7）。
func (s *selectScreen) handleMouse(m tea.MouseMsg) tea.Cmd {
	if m.Action != tea.MouseActionPress {
		return nil
	}
	switch m.Button {
	case tea.MouseButtonWheelUp:
		s.tree.Move(-3)
	case tea.MouseButtonWheelDown:
		s.tree.Move(3)
	case tea.MouseButtonLeft:
		if s.detailOpen || s.filtering {
			return nil
		}
		row := panelRowFromMouse(bodyRowFromMouse(m.Y, s.viewH))
		if row < 0 {
			return nil
		}
		if idx := s.tree.offset + row; idx >= 0 && idx < len(s.tree.rows) {
			s.tree.idx = idx
		}
	}
	return nil
}

func (s *selectScreen) copyURL() tea.Cmd {
	u := s.tree.CursorURL()
	if u == "" {
		return notify("warn", "分组行没有 URL")
	}
	if err := clipboard.WriteAll(u); err != nil {
		return notify("err", "复制失败（缺少剪贴板工具）："+err.Error())
	}
	return notify("ok", "已复制 URL")
}

func (s *selectScreen) Editing() bool { return s.filtering }
func (s *selectScreen) EscLayers() int {
	if s.filtering || s.tree.filter != "" || s.detailOpen {
		return 1
	}
	return 0
}

func (s *selectScreen) Status() string { return s.tree.Status() }

func (s *selectScreen) Hint(cw int) string {
	if s.filtering {
		return fitHints([]string{hintPair("enter", "保留过滤"), hintPair("esc", "清除过滤")}, cw)
	}
	return km.hint(stepSelect, false, cw)
}

func (s *selectScreen) View(cw, ch int) string {
	s.viewH = ch
	if s.detailOpen {
		return strings.Join(s.detailModal(cw, ch), "\n")
	}
	if s.filtering {
		// 过滤输入行贴在清单面板上方，保持内容区高度不变。
		line := "  " + stAccent.Render("过滤") + " " + stText.Render(s.tree.filter) + stAccent.Render("▌")
		return line + "\n" + s.listView(cw, ch-1)
	}
	return s.listView(cw, ch)
}

// listView 渲染清单：列表独占整宽，详情作为底部信息条（避免右侧栏挤断长 URL）。
func (s *selectScreen) listView(cw, ch int) string {
	stripH := 0
	switch {
	case ch >= 24:
		stripH = 5
	case ch >= 18:
		stripH = 4
	case ch >= 12:
		stripH = 3
	}
	list := s.tree.View(cw, ch-stripH, true)
	if stripH == 0 {
		return strings.Join(list, "\n")
	}
	return strings.Join(append(list, s.detailStrip(cw, stripH)...), "\n")
}

// detailStrip 渲染底部的"选中项注释"：URL/分组名 + 状态与引用位置。
// 高度决定信息量：3 行只放一行摘要，5 行再加一行元信息（域名/后缀/映射）。
func (s *selectScreen) detailStrip(cw, h int) []string {
	g := glyphsFor()
	inner := maxInt(minInt(cw, contentMax)-2, 20)
	contentW := inner - 2
	rows := maxInt(h-2, 1)
	var lines []string
	cur := s.tree.cursor()

	switch {
	case cur == nil:
		lines = append(lines, stMuted.Render("（无选中项）"))
	case cur.isGroup:
		on := 0
		files := map[string]bool{}
		for _, l := range cur.members {
			if s.tree.checked[l.URL] {
				on++
			}
			files[l.File] = true
		}
		state := s.tree.groupState(cur.members)
		mark := map[int]string{2: g.SelAll, 1: g.SelPart, 0: g.SelNone, -1: g.SelNone}[state]
		lines = append(lines, LR(mark+" "+stBold.Render(Truncate(cur.group, contentW-16)),
			stMuted.Render(itoa(len(cur.members))+" 张"), contentW))
		sep := stMuted.Render(" · ")
		lines = append(lines, stMuted.Render("已选 ")+stText.Render(itoa(on)+" 张")+sep+
			stMuted.Render("涉及 ")+stText.Render(itoa(len(files))+" 个文件")+sep+
			stAccent.Render("space 整组选择"))
		if rows >= 3 { // 折叠时也能看到组里有什么
			preview := stMuted.Render("成员 ")
			for i, l := range cur.members {
				if i >= 8 {
					preview += stMuted.Render(" …")
					break
				}
				next := preview + stText.Render(tailName(l.URL)) + stMuted.Render(" · ")
				if Width(next) > contentW-2 {
					preview += stMuted.Render("…")
					break
				}
				preview = next
			}
			lines = append(lines, Truncate(strings.TrimSuffix(preview, stMuted.Render(" · ")), contentW))
		}
	default:
		url := cur.url
		status := stMuted.Render("将上传")
		if s.tree.mapped[url] {
			if s.tree.fresh {
				status = stWarn.Render("将重新上传")
			} else {
				status = stOK.Render(g.Check + " 已有映射（跳过上传）")
			}
		}
		lines = append(lines, LR(stText.Render(Truncate(displayURL(url), contentW-Width(status)-2)),
			status, contentW))
		refs := s.tree.Refs(url)
		refLines := packRefLines(refs, stMuted.Render("引用 "+itoa(len(refs))+" 处"), contentW)
		if len(refLines) > rows-1 { // 最后一行留给操作提示
			refLines = refLines[:rows-1]
			refLines[len(refLines)-1] = Truncate(refLines[len(refLines)-1]+stMuted.Render("  …"), contentW)
		}
		lines = append(lines, refLines...)
		if len(lines) < rows {
			lines = append(lines, stAccent.Render("i 完整详情")+stMuted.Render(" · ")+stAccent.Render("y 复制 URL"))
		}
	}
	for len(lines) > rows { // 高度不够时保留最重要的信息
		lines = lines[:rows]
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return panelBox("详情", lines, inner, false)
}

// packRefLines 把引用位置按可用宽度折成若干行（首行带"N 处"前缀）。
func packRefLines(refs []string, first string, width int) []string {
	var lines []string
	cur := first
	for _, r := range refs {
		next := cur + stMuted.Render(" · ") + stText.Render(r)
		if Width(next) > width {
			if strings.TrimSpace(stripANSI(cur)) != "" {
				lines = append(lines, Truncate(cur, width))
			}
			cur = stText.Render(r)
			continue
		}
		cur = next
	}
	if strings.TrimSpace(stripANSI(cur)) != "" {
		lines = append(lines, Truncate(cur, width))
	}
	return lines
}

// displayURL 去掉协议前缀，让 URL 在有限宽度里显示更多有效内容。
func displayURL(rawURL string) string {
	return strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
}

// detailModal 是完整详情覆盖层（窄屏或按 i 打开）。
func (s *selectScreen) detailModal(cw, ch int) []string {
	url := s.tree.CursorURL()
	if url == "" {
		return modalLines("详情", []string{"分组行没有详情"}, "esc 关闭", cw, ch)
	}
	inner := minInt(72, maxInt(cw-8, 30))
	var rows []string
	for _, l := range Wrap(url, inner-4) {
		rows = append(rows, stBold.Render(l))
	}
	rows = append(rows, "")
	rows = append(rows, stMuted.Render("域名   ")+stText.Render(hostOf(url)))
	rows = append(rows, stMuted.Render("后缀   ")+stText.Render(extOf(url)))
	refs := s.tree.Refs(url)
	for i, r := range refs {
		if i == 10 {
			rows = append(rows, stMuted.Render("       …还有 "+itoa(len(refs)-10)+" 处"))
			break
		}
		rows = append(rows, stMuted.Render("       "+Truncate(r, inner-12)))
	}
	if s.tree.mapped[url] && !s.tree.fresh {
		rows = append(rows, stMuted.Render("映射   ")+stOK.Render(glyphsFor().Check+" 已有（上传时跳过）"))
	}
	return modalLines("详情", rows, "y 复制 URL · esc 关闭", cw, ch)
}

func extOf(url string) string {
	i := strings.LastIndexByte(url, '.')
	if i < 0 {
		return "—"
	}
	return strings.ToLower(url[i:])
}

// loadMapKeys 读取映射表，返回"已有映射"的 URL 集合。
func loadMapKeys(path string) (map[string]bool, error) {
	m, err := store.Load(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(m.Entries))
	for u, e := range m.Entries {
		if e.NewURL != "" {
			out[u] = true
		}
	}
	return out, nil
}
