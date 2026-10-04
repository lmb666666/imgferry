// 来源：扫描范围（路径 / 文件类型 / 后缀过滤），异步扫描可取消。
package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/scan"
)

type sourceScreen struct {
	base
	form *form

	path      string
	fileKind  string // text | all
	extFilter string

	scanning  bool
	readFiles int
	found     int
	scanErr   string

	pathSeq int
	checked string // 最近一次校验过的路径
	note    string
	noteLvl string

	scanCancel context.CancelFunc // 扫描可取消（esc）
}

type scanDoneMsg struct {
	res *scan.Result
	err error
}

type scanProgressMsg struct {
	read  int
	found int
}

type pathCheckMsg struct {
	seq  int
	path string
}

// pathInfoMsg 是延迟校验的结果（在后台 stat / 统计文件数，避免卡住事件循环）。
type pathInfoMsg struct {
	seq   int
	note  string
	level string
}

func (s *sourceScreen) Init(sess *Session, env *env) tea.Cmd {
	s.Setup(sess, env)
	s.step = stepSource
	s.path = sess.SourcePath
	s.fileKind = "text"
	if sess.AllFiles {
		s.fileKind = "all"
	}
	s.extFilter = sess.ExtFilter
	s.buildForm()
	cmd := s.form.focus(0)
	if sess.AutoScan {
		sess.AutoScan = false
		return tea.Batch(cmd, s.startScan())
	}
	return cmd
}

func (s *sourceScreen) buildForm() {
	pathField := newTextField("路径", "目录或单个文件；回车开始扫描", &s.path, "./posts")
	pathField.name = "path"
	pathField.validate = func() string {
		if strings.TrimSpace(s.path) == "" {
			return "路径不能为空"
		}
		if _, err := os.Stat(strings.TrimSpace(s.path)); err != nil {
			return "路径不存在"
		}
		return ""
	}
	kindField := newSelectField("文件类型", "含 md/html/txt/源码/配置；也可以选全部文件",
		&s.fileKind, []option{{"text", "Markdown 与常见文本（50 种扩展名）"}, {"all", "全部文件（跳过二进制与超大文件）"}}, false)
	kindField.name = "kind"
	extField := newTextField("后缀过滤", "选填：逗号分隔，只列出这些后缀的图片", &s.extFilter, ".webp, .png")
	extField.name = "ext"

	f := newForm(pathField, kindField, extField)
	f.onChange = func() {}
	s.form = f
}

func (s *sourceScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case scanDoneMsg:
		return s.afterScan(msg.res, msg.err)
	case scanProgressMsg:
		s.readFiles, s.found = msg.read, msg.found
		return s, nil
	case pathCheckMsg:
		if msg.seq != s.pathSeq || s.env == nil {
			return s, nil
		}
		s.checked = msg.path
		// 目录可能很大：统计放到后台，结果用消息回填（不阻塞按键与渲染）。
		env, seq, path, kind := s.env, msg.seq, msg.path, s.fileKind
		go func() {
			note, lvl := describePath(path, kind)
			env.send(pathInfoMsg{seq: seq, note: note, level: lvl})
		}()
		return s, nil
	case pathInfoMsg:
		if msg.seq != s.pathSeq {
			return s, nil
		}
		s.note = msg.note
		s.applyPathNote(msg.note, msg.level)
		return s, nil
	case tea.KeyMsg:
		if s.scanning {
			if msg.String() == "esc" { // esc 取消扫描（见 EscLayers）
				if s.scanCancel != nil {
					s.scanCancel()
				}
				return s, notify("warn", "正在取消扫描…")
			}
			return s, nil
		}
		if msg.String() == "enter" {
			return s, s.submit()
		}
		cmd, _ := s.form.update(msg)
		return s, tea.Batch(cmd, s.schedulePathCheck())
	}
	return s, nil
}

// schedulePathCheck 路径变化后 400ms 做一次本地校验（防抖，不阻塞输入）。
func (s *sourceScreen) schedulePathCheck() tea.Cmd {
	p := strings.TrimSpace(s.path)
	if p == s.checked {
		return nil
	}
	s.pathSeq++
	seq := s.pathSeq
	return tickFn(400*time.Millisecond, func(time.Time) tea.Msg {
		return pathCheckMsg{seq: seq, path: p}
	})
}

func describePath(p, kind string) (string, string) {
	if p == "" {
		return "", ""
	}
	info, err := os.Stat(p)
	if err != nil {
		return "路径不存在", "err"
	}
	if !info.IsDir() {
		return "单个文件", "ok"
	}
	n, err := countFiles(p, s_extsFor(kind))
	if err != nil {
		return "目录（统计文件数失败）", "warn"
	}
	return "目录 · 约 " + itoa(n) + " 个文件（按当前文件类型）", "ok"
}

// s_extsFor 返回文件类型对应的扩展名集合（供预览统计与扫描共用）。
func s_extsFor(kind string) map[string]bool {
	if kind == "all" {
		return scan.ExtsAll
	}
	return scan.DefaultExts
}

// countFiles 统计目录下可扫描文件数（上限 20000，避免大仓库卡顿）。
func countFiles(root string, exts map[string]bool) (int, error) {
	files, err := scan.FindFiles(root, exts)
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

func (s *sourceScreen) submit() tea.Cmd {
	if !s.form.validateAll() {
		return notify("err", "请先修正路径错误")
	}
	return s.startScan()
}

func (s *sourceScreen) startScan() tea.Cmd {
	root := strings.TrimSpace(s.path)
	if root == "" {
		return notify("err", "路径不能为空")
	}
	if _, err := os.Stat(root); err != nil {
		return notify("err", "路径不存在："+root)
	}
	s.sess.SourcePath = root
	s.sess.ReturnTo = -1 // 重新扫描属于流程前进，不再保留"退回预检"
	s.sess.AllFiles = s.fileKind == "all"
	s.sess.ExtFilter = s.extFilter
	s.sess.Exts = s_extsFor(s.fileKind)

	s.scanning = true
	s.readFiles, s.found, s.scanErr = 0, 0, ""
	env := s.env
	if env == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.scanCancel = cancel
	exts := s.sess.Exts
	go func() {
		var lastReport time.Time
		res, err := scan.ScanWithProgress(ctx, root, exts, nil, func(read, found int) {
			if time.Since(lastReport) < 120*time.Millisecond {
				return
			}
			lastReport = time.Now()
			env.send(scanProgressMsg{read: read, found: found})
		})
		env.send(scanDoneMsg{res: res, err: err})
	}()
	return nil
}

// Busy 表示扫描进行中（spinner 继续走帧）。
func (s *sourceScreen) Busy() bool { return s.scanning }

func (s *sourceScreen) afterScan(res *scan.Result, err error) (screen, tea.Cmd) {
	s.scanning = false
	s.scanCancel = nil
	if errors.Is(err, context.Canceled) {
		s.note = "已取消扫描"
		s.applyPathNote(s.note, "muted")
		return s, notify("warn", "已取消扫描")
	}
	if err != nil {
		s.scanErr = err.Error()
		return s, notify("err", "扫描失败："+err.Error()+" · 检查路径后重试")
	}
	s.sess.Scan = res
	links := selectImageLinks(res.Links, s.sess.ExtFilter)
	s.sess.Tree = newCheckTree(links)
	s.sess.Tree.SetAll(true)
	if len(links) == 0 {
		s.note = "没有发现图片链接 · 可以换文件类型，或放宽后缀过滤"
		s.applyPathNote(s.note, "warn")
		return s, notify("warn", s.note)
	}
	if len(s.sess.RetryOnly) > 0 {
		s.sess.Tree.Only(s.sess.RetryOnly)
		s.sess.RetryOnly = nil
		return s, goTo(stepPreflight)
	}
	return s, goTo(stepSelect)
}

// selectImageLinks 只保留图片类链接，并应用后缀过滤。
func selectImageLinks(links []scan.Link, extFilter string) []scan.Link {
	suffixes := parseExts(extFilter)
	var out []scan.Link
	for _, l := range links {
		if l.Kind != scan.KindImage {
			continue
		}
		if len(suffixes) > 0 && !urlHasExt(l.URL, suffixes) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// applyPathNote 把路径校验结果写到路径字段上，由底部说明区统一展示。
func (s *sourceScreen) applyPathNote(note, level string) {
	if s.form == nil || len(s.form.fields) == 0 {
		return
	}
	s.form.fields[0].note = note
	s.form.fields[0].noteLevel = level
}

// Digits 供根模型判断数字键归属（数字字段键入优先于步骤跳转）。
func (s *sourceScreen) Digits() bool { return s.form != nil && s.form.typingNumber() }

func (s *sourceScreen) Editing() bool {
	return !s.scanning && s.form != nil && s.form.editing()
}

// EscLayers 在扫描中为 1：esc 先用于取消扫描，而不是退回上一屏。
func (s *sourceScreen) EscLayers() int {
	if s.scanning {
		return 1
	}
	return 0
}

func (s *sourceScreen) Status() string {
	if s.scanning {
		return "正在扫描 " + strings.TrimSpace(s.path) + " · 已读 " + itoa(s.readFiles) + " 个文件 · 发现 " + itoa(s.found) + " 张图片"
	}
	return "填好扫描范围后回车开始扫描"
}

func (s *sourceScreen) Hint(cw int) string {
	if s.scanning {
		return fitHints([]string{hintPair("esc", "取消扫描")}, cw)
	}
	return km.hint(stepSource, false, cw)
}

func (s *sourceScreen) View(cw, ch int) string {
	if s.scanning {
		spin := stAccent.Render(s.sess.SpinFrame) + " "
		lines := []string{
			"",
			sectionTitle("扫描范围"),
			"",
			"    " + spin + stText.Render("正在扫描 "+Truncate(strings.TrimSpace(s.path), cw-24)),
			"",
			"    " + stMuted.Render("已读 "+itoa(s.readFiles)+" 个文件 · 发现 "+itoa(s.found)+" 张图片"),
		}
		return strings.Join(lines, "\n")
	}
	// 说明区固定在内容区最后 4 行：位置与高度都不随焦点变化。
	const helpH = 4
	s.form.spinner = s.sess.SpinFrame
	formH := maxInt(ch-helpH, 6)
	out := []string{"", sectionTitle("扫描范围"), ""}
	out = append(out, s.form.viewWindow(cw, maxInt(formH-3, 6))...)
	for len(out) < formH {
		out = append(out, "")
	}
	out = append(out, s.form.HelpView(cw)...)
	return strings.Join(out[:maxInt(ch, formH+helpH)], "\n")
}
