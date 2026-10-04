// 预检：写盘前的唯一确认点——把"将要发生什么"说清楚。
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/migrate"
	"github.com/lmb666666/imgferry/internal/scan"
)

type preflightScreen struct {
	base
	plan     migrate.Plan
	topFiles []fileChange
	err      string
	confirm  bool
}

type fileChange struct {
	file string
	refs int
}

func (s *preflightScreen) Init(sess *Session, env *env) tea.Cmd {
	s.Setup(sess, env)
	s.step = stepPreflight
	selected := sess.Tree.SelectedLinks()
	plan, err := migrate.BuildPlan(selected, sess.Target.MapPathValue(), sess.Target.FreshMap)
	if err != nil {
		s.err = err.Error()
		return notify("err", "映射表读取失败："+err.Error())
	}
	s.plan = plan
	sess.Plan = &plan
	s.topFiles = topFilesByRefs(selected, 5)
	return nil
}

// topFilesByRefs 找出引用最多（替换处数最多）的几个文件。
func topFilesByRefs(links []scan.Link, n int) []fileChange {
	count := map[string]int{}
	for _, l := range links {
		count[l.File]++
	}
	out := make([]fileChange, 0, len(count))
	for f, c := range count {
		out = append(out, fileChange{file: f, refs: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].refs != out[j].refs {
			return out[i].refs > out[j].refs
		}
		return out[i].file < out[j].file
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (s *preflightScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	if s.confirm {
		switch key.String() {
		case "enter", "y":
			s.confirm = false
			return s, goTo(stepRun)
		case "esc", "n":
			s.confirm = false
		}
		return s, nil
	}
	switch key.String() {
	case "enter":
		if s.err != "" {
			return s, notify("err", "无法开始：映射表不可用")
		}
		if !s.sess.Target.Backup {
			s.confirm = true
			return s, nil
		}
		return s, goTo(stepRun)
	case "d":
		return s, s.exportPlan()
	case "e":
		s.sess.ReturnTo = stepPreflight
		return s, goTo(stepSource)
	case "t":
		s.sess.ReturnTo = stepPreflight
		return s, goTo(stepTarget)
	}
	return s, nil
}

// exportPlan 只导出计划文件（dry-run）：不写被迁移文件、不上传。
func (s *preflightScreen) exportPlan() tea.Cmd {
	path := fmt.Sprintf("imgferry-plan-%s.md", time.Now().Format("20060102-150405"))
	var b strings.Builder
	fmt.Fprintf(&b, "# imgferry 迁移计划\n\n")
	fmt.Fprintf(&b, "生成时间：%s\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "| 项 | 数量 |\n|---|---|\n")
	fmt.Fprintf(&b, "| 扫描文件 | %d |\n", s.plan.Files)
	fmt.Fprintf(&b, "| 图片引用 | %d |\n", s.plan.References)
	fmt.Fprintf(&b, "| 去重 URL | %d |\n", s.plan.UniqueURLs)
	fmt.Fprintf(&b, "| 映射表复用 | %d |\n", s.plan.Reuse)
	fmt.Fprintf(&b, "| 本次上传 | %d |\n", s.plan.ToUpload)
	fmt.Fprintf(&b, "| 将要修改文件 | %d |\n", s.plan.FilesToChange)
	fmt.Fprintf(&b, "| 替换链接 | %d |\n", s.plan.Replacements)
	fmt.Fprintf(&b, "\n目标图床：%s · 并发 %d · 间隔 %s · 备份 %v · 映射表 %s\n",
		s.sess.Target.BaseURL, s.sess.Target.Concurrency, s.sess.Target.Interval,
		s.sess.Target.Backup, s.sess.Target.MapPathValue())
	fmt.Fprintf(&b, "\n> 这是 dry-run 计划文件，未上传任何图片、未修改任何来源文件。\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return notify("err", "计划文件写入失败："+err.Error())
	}
	abs, _ := filepath.Abs(path)
	return notify("ok", "计划已导出："+abs)
}

func (s *preflightScreen) Editing() bool { return s.confirm }
func (s *preflightScreen) EscLayers() int {
	if s.confirm {
		return 1
	}
	return 0
}

func (s *preflightScreen) Status() string {
	t := &s.sess.Target
	return "目标 " + t.BaseURL + " · 并发 " + itoa(t.Concurrency) + " · 间隔 " + t.Interval +
		" · 备份 " + map[bool]string{true: "开", false: "关"}[t.Backup]
}

func (s *preflightScreen) View(cw, ch int) string {
	if s.confirm {
		rows := []string{"", "备份已关闭：替换后无法用 .bak 还原（仍可用映射表反查）。", "",
			"  ▸ 返回开启备份         仍然开始"}
		return strings.Join(modalLines("继续迁移？", rows, "enter 确认 · esc 返回", cw, ch), "\n")
	}
	p := s.plan
	t := &s.sess.Target
	kv := func(k, v string) string {
		return "    " + stMuted.Render(Fit(k, 12)) + stText.Render(v)
	}
	var out []string
	out = append(out, "", sectionTitle("本次迁移计划"), "")
	out = append(out, kv("扫描文件", itoa(p.Files)+" 个（"+Truncate(strings.TrimSpace(s.sess.SourcePath), 40)+"）"))
	out = append(out, kv("图片引用", itoa(p.References)+" 处 → 去重后 "+itoa(p.UniqueURLs)+" 个地址"))
	reuse := itoa(p.Reuse) + " 个（已在映射表中，不会重复上传）"
	if t.FreshMap {
		reuse = stWarn.Render("0 个（已开启清空映射表：历史映射将被忽略）")
	}
	out = append(out, kv("映射复用", reuse))
	out = append(out, kv("本次上传", itoa(p.ToUpload)+" 个（消耗站点配额 "+itoa(p.ToUpload)+" 次）"))
	change := itoa(p.FilesToChange) + " 个文件 · 替换 " + itoa(p.Replacements) + " 处链接"
	if t.Backup {
		change += " · 生成 " + itoa(p.FilesToChange) + " 个 .bak"
	} else {
		change = stWarn.Render(change + " · 未生成备份")
	}
	out = append(out, kv("将要修改", change))

	if len(s.topFiles) > 0 {
		out = append(out, "", sectionTitle("将要修改的文件（前 "+itoa(len(s.topFiles))+" 个）"), "")
		for _, fc := range s.topFiles {
			out = append(out, "    "+stText.Render(Fit(Truncate(fc.file, 44), 46))+stMuted.Render(itoa(fc.refs)+" 处"))
		}
		if p.FilesToChange > len(s.topFiles) {
			out = append(out, "    "+stMuted.Render("…还有 "+itoa(p.FilesToChange-len(s.topFiles))+" 个"))
		}
	}
	if !t.Backup {
		out = append(out, "", "  "+stDangerB.Render(glyphsFor().Warn+" 备份已关闭：替换后无法用 .bak 还原（仍可用映射表反查）"))
	}
	if s.err != "" {
		out = append(out, "", "  "+stDanger.Render(glyphsFor().Cross+" "+s.err))
	}
	out = append(out, "", "  "+stMuted.Render("enter 开始迁移 · d 只导出计划 · e 修改来源 · t 修改目标 · esc 返回"))
	return strings.Join(out, "\n")
}
