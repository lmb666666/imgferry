// 迁移：进度头 + 逐项事件流；可回看、可优雅取消。
package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/migrate"
)

type runScreen struct {
	base
	stats         migrate.Stats
	items         []migrate.ItemResult
	follow        bool
	failOnly      bool
	confirmCancel bool
	active        bool
	scroll        int // 距底部的事件行数（0 = 跟随最新）
	started       time.Time
	completions   []time.Time
	rep           *migrate.Report
	err           error
}

type itemMsg struct{ it migrate.ItemResult }
type statsMsg struct{ st migrate.Stats }
type runDoneMsg struct {
	rep *migrate.Report
	err error
}

func (s *runScreen) Init(sess *Session, env *env) tea.Cmd {
	s.Setup(sess, env)
	s.step = stepRun
	s.active, s.follow = true, true
	s.started = time.Now()

	// 安全兜底：没有扫描结果（例如异常路径进入本屏）时回来源屏，而不是 panic。
	if sess.Tree == nil || len(sess.Tree.CheckedURLs()) == 0 {
		s.active = false
		return tea.Batch(notify("err", "没有已选中的图片 · 回到来源屏重新扫描"), goTo(stepSource))
	}
	t := sess.Target
	up, err := t.NewUploader()
	if err != nil {
		s.active = false
		s.err = err
		return notify("err", "目标图床配置有误："+err.Error())
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess.Cancel = cancel

	opts := migrate.Options{
		Root:             sess.SourcePath,
		Exts:             sess.Exts,
		AllFiles:         sess.AllFiles,
		ScanResult:       sess.Scan, // 复用扫描结果：既省一次全盘读，也保证清单与选择一致
		SelectedURLs:     sess.Tree.CheckedURLs(),
		Uploader:         up,
		MapPath:          t.MapPathValue(),
		DownloadHeaders:  t.DownloadHeaders(),
		Concurrency:      maxInt(t.Concurrency, 1),
		MaxUploadRetries: t.Retries(),
		Interval:         t.IntervalDuration(),
		NoBackup:         !t.Backup,
		FreshMap:         t.FreshMap,
		OnStats:          func(st migrate.Stats) { env.send(statsMsg{st}) },
		OnItem:           func(it migrate.ItemResult) { env.send(itemMsg{it}) },
		OnLog:            func(string, ...any) {},
	}
	go func() {
		rep, err := migrate.Run(ctx, opts)
		env.send(runDoneMsg{rep: rep, err: err})
	}()
	return nil
}

func (s *runScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case itemMsg:
		s.items = append(s.items, msg.it)
		if len(s.items) > 500 { // 只保留最近 500 条，长时间迁移不涨内存
			s.items = s.items[len(s.items)-500:]
		}
		if msg.it.Status == migrate.StatusUploaded || msg.it.Status == migrate.StatusReused ||
			msg.it.Status == migrate.StatusFailed {
			s.completions = append(s.completions, time.Now())
		}
		return s, nil
	case statsMsg:
		s.stats = msg.st
		return s, nil
	case runDoneMsg:
		s.active = false
		s.rep, s.err = msg.rep, msg.err
		canceled := errors.Is(msg.err, context.Canceled) || errors.Is(msg.err, context.DeadlineExceeded)
		s.sess.Canceled = canceled
		s.sess.Report = msg.rep
		s.sess.RecordRun(msg.rep, canceled)
		return s, goTo(stepReport)
	case tea.KeyMsg:
		if s.confirmCancel {
			switch msg.String() {
			case "enter", "y":
				s.confirmCancel = false
				if s.sess.Cancel != nil {
					s.sess.Cancel()
				}
				return s, notify("warn", "已请求取消 · 等待在途请求结束后进入结果页")
			case "esc", "n":
				s.confirmCancel = false
			}
			return s, nil
		}
		switch msg.String() {
		case "esc":
			if s.active {
				s.confirmCancel = true
				return s, nil
			}
			return s, goTo(stepReport)
		case "f":
			s.failOnly = !s.failOnly
			s.follow = true
		case "up", "k":
			s.follow = false
			s.scroll++
		case "pgup":
			s.follow = false
			s.scroll += 10
		case "down", "j":
			if s.scroll > 0 {
				s.scroll--
			}
			if s.scroll == 0 {
				s.follow = true
			}
		case "pgdn":
			s.scroll = maxInt(s.scroll-10, 0)
			if s.scroll == 0 {
				s.follow = true
			}
		case "G", "end":
			s.follow, s.scroll = true, 0
		}
		return s, nil
	}
	return s, nil
}

// ---- 事件流窗口 ----

func (s *runScreen) visibleItems() []migrate.ItemResult {
	if !s.failOnly {
		return s.items
	}
	out := make([]migrate.ItemResult, 0, len(s.items))
	for _, it := range s.items {
		if it.Status == migrate.StatusFailed || it.Status == migrate.StatusRetry {
			out = append(out, it)
		}
	}
	return out
}

func (s *runScreen) running() bool { return s.active }

// requestCancel 由根模型在 ctrl+c 时调用：立即取消，不弹确认。
func (s *runScreen) requestCancel(m *Model) tea.Cmd {
	if s.sess.Cancel != nil {
		s.sess.Cancel()
	}
	return nil
}

func (s *runScreen) Editing() bool { return s.confirmCancel }
func (s *runScreen) EscLayers() int {
	return 1 // esc 在本屏先用于取消迁移，而不是退回上一步
}
func (s *runScreen) Busy() bool { return s.active }

func (s *runScreen) Status() string {
	if !s.active {
		return "迁移已结束"
	}
	rate := s.rate()
	if rate == "" {
		return "已上传 " + itoa(s.stats.Uploaded) + " · 复用 " + itoa(s.stats.Skipped) +
			" · 失败 " + itoa(s.stats.Failed) + " · 剩余 " + itoa(maxInt(s.stats.Total-s.stats.Done, 0))
	}
	return "已上传 " + itoa(s.stats.Uploaded) + " · 复用 " + itoa(s.stats.Skipped) +
		" · 失败 " + itoa(s.stats.Failed) + " · " + rate
}

// rate 返回速率与预计剩余时间（样本不足时返回空串）。
func (s *runScreen) rate() string {
	if len(s.completions) < 3 {
		return ""
	}
	cut := time.Now().Add(-30 * time.Second)
	recent := 0
	for i := len(s.completions) - 1; i >= 0; i-- {
		if s.completions[i].Before(cut) {
			break
		}
		recent++
	}
	if recent < 3 {
		return ""
	}
	perSec := float64(recent) / 30.0
	remain := maxInt(s.stats.Total-s.stats.Done, 0)
	eta := time.Duration(float64(remain)/perSec) * time.Second
	return "约 " + formatFloat1(perSec) + "/秒 · 已用 " + shortDur(time.Since(s.started)) +
		" · 剩余 " + shortDur(eta)
}

func (s *runScreen) Hint(cw int) string {
	if s.confirmCancel {
		return fitHints([]string{hintPair("enter", "取消迁移"), hintPair("esc", "继续迁移")}, cw)
	}
	if !s.active {
		return fitHints([]string{hintPair("enter", "查看结果"), hintPair("?", "帮助")}, cw)
	}
	return km.hint(stepRun, false, cw)
}

func (s *runScreen) View(cw, ch int) string {
	if s.confirmCancel {
		rows := []string{"",
			"已上传的 " + itoa(s.stats.Uploaded) + " 张会写入映射表保留；",
			"未处理的 " + itoa(maxInt(s.stats.Total-s.stats.Done, 0)) + " 张下次可续传。", "",
			"  ▸ 继续迁移         取消迁移"}
		return strings.Join(modalLines("取消迁移？", rows, "enter 确认 · esc 返回", cw, ch), "\n")
	}
	head := []string{
		"    " + progressBar(s.stats.Done, s.stats.Total, 42) + " " +
			stMuted.Render(itoa(s.stats.Done)+"/"+itoa(s.stats.Total)+" · "+percent(s.stats.Done, s.stats.Total)),
		"    " + stMuted.Render("已上传 ") + stOK.Render(itoa(s.stats.Uploaded)) +
			stMuted.Render(" · 复用 ") + stText.Render(itoa(s.stats.Skipped)) +
			stMuted.Render(" · 失败 ") + failedCountStyle(s.stats.Failed) +
			stMuted.Render(" · 剩余 ") + stText.Render(itoa(maxInt(s.stats.Total-s.stats.Done, 0))),
	}
	if rate := s.rate(); rate != "" {
		head = append(head, "    "+stMuted.Render(rate))
	}
	head = append(head, "")

	items := s.visibleItems()
	panelH := maxInt(ch-len(head)-2, 3)
	title := "事件流 ─ 全部（f 只看失败）"
	if s.failOnly {
		title = "事件流 ─ 只看失败（f 显示全部）"
	}
	if !s.follow {
		title += " · 已暂停跟随（G 回到底部）"
	}
	var rows []string
	if len(items) == 0 {
		msg := "等待第一项结果…"
		if s.failOnly {
			msg = "暂无失败项"
		}
		rows = append(rows, stMuted.Render(msg))
	}
	start := maxInt(len(items)-panelH-s.scroll, 0)
	if start+panelH > len(items) {
		start = maxInt(len(items)-panelH, 0)
	}
	for _, it := range items[start:] {
		rows = append(rows, eventRow(it, cw-6))
	}
	body := append(head, panelBox(title, rows, cw-2, false)...)
	return strings.Join(body, "\n")
}

func progressBar(done, total, width int) string {
	if total <= 0 {
		return stFaint.Render(strings.Repeat("░", width))
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	return stAccent.Render(strings.Repeat("█", filled)) + stFaint.Render(strings.Repeat("░", width-filled))
}

func percent(done, total int) string {
	if total <= 0 {
		return "0%"
	}
	return itoa(done*100/total) + "%"
}

func failedCountStyle(n int) string {
	if n == 0 {
		return stMuted.Render("0")
	}
	return stDangerB.Render(itoa(n))
}

func formatFloat1(f float64) string {
	whole := int(f)
	frac := int((f - float64(whole)) * 10)
	return itoa(whole) + "." + itoa(frac)
}

func shortDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	mm := int(d.Minutes())
	ss := int(d.Seconds()) % 60
	return pad2(mm) + ":" + pad2(ss)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}
