// 复用组件：步骤轨、状态/通知行、键位行、详情栏、统计读数、事件流、失败表、覆盖层。
// 所有组件输出"已定宽"的行（不依赖终端），拼接时不改变整屏高度。
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/lmb666666/imgferry/internal/migrate"
)

// ---- 步骤轨 ----

// rail 是唯一的位置指示：✓ 已完成 / ▍当前 / ○ 未达。窄屏降级为「▍选择 2/6」。
func rail(step, w int, brand string) string {
	g := glyphsFor()
	names := []string{"来源", "选择", "目标", "预检", "迁移", "结果"}
	brandStyle := stAccentB
	if brand == "" {
		brand = "⚓ imgferry"
	}
	if w < 60 {
		if step < 0 || step >= stepCount {
			return brandStyle.Render(brand)
		}
		return brandStyle.Render(brand) + stMuted.Render("  ") +
			stAccentB.Render(g.Bar+names[step]) + stMuted.Render(fmt.Sprintf(" %d/6", step+1))
	}
	parts := []string{brandStyle.Render(brand)}
	for i, n := range names {
		switch {
		case step >= 0 && i < step:
			parts = append(parts, stOK.Render(g.Check+" "+n))
		case i == step:
			parts = append(parts, stAccentB.Render(g.Bar+n))
		default:
			parts = append(parts, stMuted.Render(g.Pending+" "+n))
		}
	}
	return strings.Join(parts, stMuted.Render(" "+g.Dash+" "))
}

// ---- 状态 / 通知行 ----

type notice struct {
	text  string
	level string // "" | ok | warn | err
	until time.Time
}

func (n notice) active(now time.Time) bool {
	return n.text != "" && (n.until.IsZero() || now.Before(n.until))
}

// renderNotice 渲染页脚第一行：临时通知优先，否则显示本屏状态摘要。
func renderNotice(n notice, status string, now time.Time) string {
	if n.active(now) {
		glyph := levelGlyph(n.level)
		style := styleFor(n.level)
		text := n.text
		if glyph != "" {
			text = glyph + " " + text
		}
		// 错误与警告用语义色，普通通知用正文色。
		if n.level == "" {
			return stText.Render(text)
		}
		return style.Render(text)
	}
	if status == "" {
		return ""
	}
	return stMuted.Render(status)
}

// ---- 面板与覆盖层 ----

// modalLines 渲染居中覆盖层（标题 + 内容 + 底部提示），在 w×h 区域内居中。
func modalLines(title string, rows []string, hint string, w, h int) []string {
	inner := minInt(72, maxInt(w-8, 24))
	box := panelBox(title, rows, inner, true)
	if hint != "" {
		box = append(box, "")
		box = append(box, stMuted.Render(hint))
	}
	return centerBlock(box, w, h)
}

// ---- 统计读数 ----

type readoutCell struct {
	value string
	label string
	style string // ok | warn | err | accent | ""（正文）
}

// summaryReadout 是一行数字 + 一行标签的读数（无框）。
func summaryReadout(cells []readoutCell, width int) []string {
	var parts []string
	plainParts := []string{}
	for _, c := range cells {
		var st = stBold
		switch c.style {
		case "ok":
			st = stOKBold
		case "warn":
			st = stWarnBold
		case "err":
			st = stDangerB
		case "accent":
			st = stAccentB
		}
		parts = append(parts, st.Render(c.value)+" "+stMuted.Render(c.label))
		plainParts = append(plainParts, c.value+" "+c.label)
	}
	sep := stFaint.Render("  │  ")
	line := "    " + strings.Join(parts, sep)
	if Width(line) > width { // 窄屏降级为单行紧凑读数
		var compact []string
		for _, c := range cells {
			compact = append(compact, c.value+" "+c.label)
		}
		line = "    " + stMuted.Render(strings.Join(compact, " · "))
	}
	rule := "    " + stFaint.Render(strings.Repeat(glyphsFor().Dash, minInt(Width(strings.Join(plainParts, "  │  "))+4, maxInt(width-8, 10))))
	return []string{line, rule}
}

// ---- 事件流 ----

// eventRow 渲染一条逐项事件（上传/复用/失败/退避）。
func eventRow(it migrate.ItemResult, width int) string {
	g := glyphsFor()
	nameW := minInt(24, maxInt(width/3, 12))
	name := it.Name
	if name == "" {
		name = tailName(it.URL)
	}
	switch it.Status {
	case migrate.StatusReused:
		return stOK.Render(g.Check) + " " + stText.Render(Fit(name, nameW)) + stMuted.Render("映射表已有，跳过")
	case migrate.StatusUploaded:
		return stOK.Render(g.Check) + " " + stText.Render(Fit(name, nameW)) +
			stMuted.Render("→ "+Truncate(it.NewURL, maxInt(width-nameW-6, 10)))
	case migrate.StatusRetry:
		return stWarn.Render(g.Warn + " " + it.Err)
	default: // failed
		advice := failureAdvice(it.Stage, it.Err)
		msg := it.Err
		if advice != "" {
			msg += " · " + advice
		}
		return stDanger.Render(g.Cross) + " " + stDanger.Render(Fit(name, nameW)) +
			stDanger.Render(Truncate(msg, maxInt(width-nameW-4, 10)))
	}
}

// failureAdvice 把失败原因映射为"怎么办"（失败分类与处置动作的对照表）。
func failureAdvice(stage, errMsg string) string {
	e := strings.ToLower(errMsg)
	switch {
	case stage == migrate.StageDownload && (strings.Contains(e, "403") || strings.Contains(e, "401")):
		return "在目标屏高级里填 Referer"
	case stage == migrate.StageDownload && strings.Contains(e, "404"):
		return "旧图已失效，引用保持原样"
	case stage == migrate.StageUpload && (strings.Contains(e, "频繁") || strings.Contains(e, "429")):
		return "把间隔加到 2s、并发降到 1 后重试"
	case stage == migrate.StageUpload && (strings.Contains(e, "401") || strings.Contains(e, "unauth") ||
		strings.Contains(e, "鉴权") || strings.Contains(e, "token")):
		return "重新填写 Token 或改用邮箱密码"
	case stage == migrate.StageUpload && strings.Contains(errMsg, "服务异常"):
		return "改选站点存储策略"
	case strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "超时"):
		return "网络超时，重试即可"
	default:
		return ""
	}
}

// fitReason 在列宽内优先保留"怎么办"，空间不足时先把原始原因截短。
func fitReason(errMsg, advice string, width int) string {
	if advice == "" {
		return Truncate(errMsg, width)
	}
	if full := errMsg + " · " + advice; Width(full) <= width {
		return full
	}
	remain := width - Width(advice) - 3
	if remain >= 10 {
		return Truncate(errMsg, remain) + " · " + advice
	}
	return Truncate(errMsg+" · "+advice, width)
}

// tailName 取 URL 末段作为显示名。
func tailName(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return url
	}
	return s
}

// ---- 失败表 ----

// failureRows 渲染失败明细表：阶段 · 图片（含引用位置）· 原因。
// width 为面板内可用列数；各列宽度在这里算准，避免右侧出现半个词。
func failureRows(fails []migrate.Failure, refs map[string][]string, cursor int, width int) []string {
	const stageW, gapW = 4, 2
	imgW := minInt(22, maxInt(width/3, 12))
	whyW := maxInt(width-2-stageW-gapW-imgW-gapW, 12) // 2 = 光标列 + 空格
	rows := make([]string, 0, len(fails))
	for i, f := range fails {
		row := " "
		if i == cursor {
			row = stAccent.Render(glyphsFor().Cursor) + " "
		}
		stage := "下载"
		if f.Stage == migrate.StageUpload {
			stage = "上传"
		}
		img := tailName(f.URL)
		if ref := refs[f.URL]; len(ref) > 0 {
			img += " (" + ref[0] + ")"
		}
		why := fitReason(f.Err, failureAdvice(f.Stage, f.Err), whyW)
		line := row + stMuted.Render(Fit(stage, stageW)) + strings.Repeat(" ", gapW) +
			stText.Render(Fit(img, imgW)) + strings.Repeat(" ", gapW) +
			stDanger.Render(Truncate(why, whyW))
		rows = append(rows, Fit(line, width))
	}
	return rows
}
