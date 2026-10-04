// 首页：品牌 + 继续上次 / 重试失败项 + 环境提醒。
package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/store"
)

type startScreen struct {
	base
	idx          int
	confirmClear bool
	gitNote      string // 启动时探测一次，避免每次渲染都起子进程
}

type startAction struct {
	label   string
	hint    string
	enabled bool
}

func (s *startScreen) Init(sess *Session, env *env) tea.Cmd {
	s.Setup(sess, env)
	s.step = stepStart
	s.gitNote = gitAdviceFn(sess.SourcePath)
	return nil
}

func (s *startScreen) actions() []startAction {
	last := s.sess.State().LastRun
	items := []startAction{{label: "新建迁移", hint: "enter", enabled: true}}
	if last != nil && last.Source != "" {
		items = append(items, startAction{
			label: "继续上次   " + last.Source + " → " + last.Target, hint: "c", enabled: true})
		if n := len(last.Failed); n > 0 {
			items = append(items, startAction{
				label: "只重试上次失败的 " + itoa(n) + " 项", hint: "r", enabled: true})
		}
	}
	items = append(items, startAction{label: "清空映射表", hint: "x", enabled: true})
	return items
}

func (s *startScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	if s.confirmClear {
		switch key.String() {
		case "enter", "y":
			s.confirmClear = false
			return s, s.clearMap()
		case "esc", "n", "q":
			s.confirmClear = false
		}
		return s, nil
	}
	items := s.actions()
	switch key.String() {
	case "up", "k":
		s.idx = maxInt(s.idx-1, 0)
	case "down", "j":
		s.idx = minInt(s.idx+1, len(items)-1)
	case "c":
		return s, s.continueLast()
	case "r":
		return s, s.retryFailed()
	case "x":
		s.confirmClear = true
	case "enter", " ":
		if s.idx >= len(items) {
			return s, nil
		}
		switch s.idx {
		case 0:
			return s, goTo(stepSource)
		case 1:
			return s, s.continueLast()
		default:
			if strings.HasPrefix(items[s.idx].label, "只重试") {
				return s, s.retryFailed()
			}
			s.confirmClear = true
		}
	}
	return s, nil
}

// continueLast 恢复上次的来源与目标，进入来源屏并自动扫描。
func (s *startScreen) continueLast() tea.Cmd {
	last := s.sess.State().LastRun
	if last == nil || last.Source == "" {
		return notify("warn", "还没有可继续的记录")
	}
	s.sess.SourcePath = last.Source
	s.sess.AutoScan = true
	return goTo(stepSource)
}

// retryFailed 只重试上次失败的 URL：进入来源屏并自动扫描后直达预检。
func (s *startScreen) retryFailed() tea.Cmd {
	urls := s.sess.LastFailedURLs()
	if len(urls) == 0 {
		return notify("warn", "上次没有失败项")
	}
	last := s.sess.State().LastRun
	if last != nil && last.Source != "" {
		s.sess.SourcePath = last.Source
	}
	s.sess.RetryOnly = urls
	s.sess.AutoScan = true
	return goTo(stepSource)
}

// clearMap 清空映射表（首页只做这一件事，二次确认见 Update）。
func (s *startScreen) clearMap() tea.Cmd {
	path := s.sess.Target.MapPathValue()
	m, err := store.Load(path)
	if err != nil {
		return notify("err", "映射表读取失败："+err.Error())
	}
	n := len(m.Entries)
	if n == 0 {
		return notify("warn", "映射表已是空的（"+path+"）")
	}
	m.Clear()
	if err := m.Save(); err != nil {
		return notify("err", "映射表写入失败："+err.Error())
	}
	return notify("ok", "已清空 "+itoa(n)+" 条映射记录："+path)
}

func (s *startScreen) Editing() bool { return s.confirmClear }
func (s *startScreen) EscLayers() int {
	if s.confirmClear {
		return 1
	}
	return 0
}

func (s *startScreen) Status() string {
	last := s.sess.State().LastRun
	if last == nil {
		return "还没有迁移记录，从「新建迁移」开始"
	}
	when := last.At.Format("2006-01-02 15:04")
	line := "上次 " + when + " · 上传 " + itoa(last.Uploaded) + " · 复用 " + itoa(last.Reused) +
		" · 失败 " + itoa(len(last.Failed))
	if last.Canceled {
		line += " · 已取消（可继续上次续传）"
	}
	return line
}

func (s *startScreen) View(cw, ch int) string {
	var out []string
	out = append(out, "")
	out = append(out, banner(cw)...)
	out = append(out, "")
	out = append(out, "      "+stMuted.Render("图床图片批量迁移：扫描 → 下载 → 上传 → 替换链接"))
	out = append(out, "", ruleLine(cw), "")

	items := s.actions()
	idx := minInt(s.idx, len(items)-1)
	for i, it := range items {
		cursor := "  "
		label := stText.Render(it.label)
		if i == idx {
			cursor = stAccent.Render(glyphsFor().Cursor) + " "
			label = stBold.Render(it.label)
		}
		out = append(out, LR(cursor+label, stMuted.Render(it.hint), cw))
	}
	out = append(out, "", ruleLine(cw), "")
	if adv := s.gitNote; adv != "" {
		out = append(out, "  "+stWarn.Render(glyphsFor().Warn+" "+adv))
		out = append(out, "")
	}
	if s.confirmClear {
		body := modalLines("清空映射表？",
			[]string{"", "将删除 " + s.sess.Target.MapPathValue() + " 里的全部映射记录；", "下次迁移会重新上传所有图片。", "",
				"  ▸ 取消         清空"},
			"enter 清空 · esc 取消", cw, ch)
		return strings.Join(body, "\n")
	}
	return strings.Join(out, "\n")
}

func (s *startScreen) Hint(cw int) string { return km.hint(stepStart, false, cw) }

// gitAdviceFn 便于测试替换：快照必须与运行环境的 git 状态无关。
var gitAdviceFn = gitAdvice

// gitAdvice 提示 git 状态：非仓库 / 有未提交改动时给出复核建议。
// 只在首页 Init 时探测一次（起子进程有成本，不放在渲染路径上）。
func gitAdvice(dir string) string {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	root, ok := gitRoot(abs)
	if !ok {
		return "当前目录不是 git 仓库 · 替换前无法用 git diff 复核（已默认开启 .bak 备份）"
	}
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "-uno").Output()
	if err != nil {
		return ""
	}
	if strings.TrimSpace(string(out)) != "" {
		return "工作区有未提交改动 · 建议先 commit 再迁移，便于用 git diff 复核替换结果"
	}
	return ""
}

// gitRoot 从 dir 向上查找 .git（不依赖 git 可执行文件）。
func gitRoot(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
