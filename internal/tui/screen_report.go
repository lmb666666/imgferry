// 结果：结论 + 统计读数 + 失败明细（含处置建议）+ 只重试失败项。
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/migrate"
)

type reportScreen struct {
	base
	rep        *migrate.Report
	refs       map[string][]string
	cursor     int
	detailOpen bool
}

func (s *reportScreen) Init(sess *Session, env *env) tea.Cmd {
	s.Setup(sess, env)
	s.step = stepReport
	s.rep = sess.Report
	if s.rep == nil {
		s.rep = &migrate.Report{}
	}
	s.refs = map[string][]string{}
	if sess.Tree != nil {
		for _, u := range sess.Tree.order {
			s.refs[u] = sess.Tree.Refs(u)
		}
	}
	if len(s.rep.Failed) == 0 && s.rep.Uploaded == 0 && s.rep.AlreadyDone == 0 {
		return notify("warn", "本次没有可迁移的链接")
	}
	return nil
}

func (s *reportScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if mouse, ok := msg.(tea.MouseMsg); ok {
		if mouse.Action == tea.MouseActionPress {
			switch mouse.Button {
			case tea.MouseButtonWheelUp:
				s.cursor = maxInt(s.cursor-1, 0)
			case tea.MouseButtonWheelDown:
				s.cursor = minInt(s.cursor+1, maxInt(len(s.rep.Failed)-1, 0))
			}
		}
		return s, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	if s.detailOpen {
		switch key.String() {
		case "esc", "enter", "i":
			s.detailOpen = false
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(s.rep.Failed)-1 {
				s.cursor++
			}
		}
		return s, nil
	}
	switch key.String() {
	case "up", "k":
		s.cursor = maxInt(s.cursor-1, 0)
	case "down", "j":
		s.cursor = minInt(s.cursor+1, maxInt(len(s.rep.Failed)-1, 0))
	case "enter":
		if len(s.rep.Failed) > 0 {
			s.detailOpen = true
		}
	case "r":
		return s, s.retryFailed()
	case "t":
		return s, goTo(stepTarget)
	case "e":
		return s, goTo(stepSource)
	}
	return s, nil
}

// retryFailed 只重试失败项：回到来源屏重新扫描定位引用，再直达预检。
func (s *reportScreen) retryFailed() tea.Cmd {
	if len(s.rep.Failed) == 0 {
		return notify("warn", "本次没有失败项")
	}
	var urls []string
	for _, f := range s.rep.Failed {
		urls = append(urls, f.URL)
	}
	s.sess.RetryOnly = urls
	s.sess.AutoScan = true
	return goTo(stepSource)
}

func (s *reportScreen) Editing() bool { return s.detailOpen }
func (s *reportScreen) EscLayers() int {
	if s.detailOpen {
		return 1
	}
	return 0
}

func (s *reportScreen) Status() string {
	rep := s.rep
	if s.sess.Canceled {
		return "已取消 · 已上传 " + itoa(rep.Uploaded) + " 张已写入映射表；未处理的链接下次可续传"
	}
	if len(rep.Failed) > 0 {
		return "失败的引用未被替换，文件里仍是旧链接"
	}
	return "可以 git diff 复核后删除 *.bak"
}

func (s *reportScreen) Verdict() (string, string) {
	rep := s.rep
	switch {
	case s.sess.Canceled:
		return "■ 已取消（部分完成：上传 " + itoa(rep.Uploaded) + " · 映射复用 " + itoa(rep.AlreadyDone) + "）", "muted"
	case len(rep.Failed) == 0 && rep.Uploaded == 0 && rep.AlreadyDone == 0:
		return "没有可迁移的链接", "warn"
	case len(rep.Failed) == 0:
		return "迁移完成", "ok"
	case rep.Uploaded == 0 && rep.AlreadyDone == 0:
		return "全部失败 · 目标图床配置可能有误 · 按 t 检查站点地址与凭据", "err"
	default:
		return "迁移完成，" + itoa(len(rep.Failed)) + " 项失败", "warn"
	}
}

func (s *reportScreen) View(cw, ch int) string {
	if s.detailOpen {
		return strings.Join(s.detailModal(cw, ch), "\n")
	}
	rep := s.rep
	verdict, level := s.Verdict()
	var out []string
	out = append(out, "")
	glyph := levelGlyph(level)
	if glyph == "" {
		glyph = "■"
	}
	out = append(out, "  "+styleFor(level).Bold(true).Render(glyph+" "+verdict))
	out = append(out, "")
	out = append(out, summaryReadout([]readoutCell{
		{itoa(rep.Uploaded), "上传", "accent"},
		{itoa(rep.AlreadyDone), "复用", ""},
		{itoa(len(rep.Failed)), "失败", failStyle(len(rep.Failed))},
		{itoa(rep.ChangedFiles), "文件", ""},
		{itoa(rep.Replacements), "替换", ""},
	}, cw)...)

	if len(rep.Failed) > 0 {
		out = append(out, "")
		rows := failureRows(rep.Failed, s.refs, s.cursor, cw-6)
		out = append(out, panelBox("失败明细", rows, cw-2, false)...)
		if s.cursor < len(rep.Failed) {
			out = append(out, "", "  "+stMuted.Render("enter 查看该项详情与处置建议"))
		}
	}
	out = append(out, "")
	if s.sess.Canceled {
		out = append(out, "  "+stMuted.Render("映射表 "+s.sess.Target.MapPathValue()+" 已保留本次上传记录"))
	} else {
		out = append(out, "  "+stMuted.Render("映射表 "+s.sess.Target.MapPathValue()))
		if s.sess.Target.Backup {
			out = append(out, "  "+stMuted.Render("备份 "+itoa(rep.ChangedFiles)+" 个 *.bak（确认无误后可删除）"))
		}
	}
	return strings.Join(out, "\n")
}

func failStyle(n int) string {
	if n == 0 {
		return ""
	}
	return "err"
}

// detailModal 展示单个失败项的完整原因、全部引用位置与处置建议。
func (s *reportScreen) detailModal(cw, ch int) []string {
	if s.cursor >= len(s.rep.Failed) {
		return modalLines("失败详情", []string{"没有可显示的项"}, "esc 关闭", cw, ch)
	}
	f := s.rep.Failed[s.cursor]
	inner := minInt(72, maxInt(cw-8, 30))
	stage := "下载"
	if f.Stage == migrate.StageUpload {
		stage = "上传"
	}
	var rows []string
	for _, l := range Wrap(f.URL, inner-4) {
		rows = append(rows, stDanger.Render(l))
	}
	rows = append(rows, "")
	rows = append(rows, stMuted.Render("阶段   ")+stText.Render(stage))
	rows = append(rows, stMuted.Render("原因   ")+stDanger.Render(Truncate(f.Err, inner-14)))
	if advice := failureAdvice(f.Stage, f.Err); advice != "" {
		rows = append(rows, stMuted.Render("建议   ")+stWarn.Render(advice))
	}
	if refs := s.refs[f.URL]; len(refs) > 0 {
		rows = append(rows, "")
		rows = append(rows, stMuted.Render("引用位置（未替换）"))
		for i, r := range refs {
			if i == 8 {
				rows = append(rows, stMuted.Render("  …还有 "+itoa(len(refs)-8)+" 处"))
				break
			}
			rows = append(rows, stText.Render("  "+Truncate(r, inner-6)))
		}
	}
	return modalLines("失败详情", rows, "esc 关闭 · r 只重试失败项", cw, ch)
}
