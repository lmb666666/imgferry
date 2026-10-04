// 选择清单：分组 + 三态勾选 + 折叠 + 过滤 + 窗口化。
// 勾选集合是 URL 集合（与分组、过滤、折叠无关），所以换维度不会丢选择。
package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lmb666666/imgferry/internal/scan"
)

type groupDim int

const (
	byDomain groupDim = iota
	byDir
	byFile
	byNone
)

func (g groupDim) String() string {
	switch g {
	case byDomain:
		return "域名"
	case byDir:
		return "目录"
	case byFile:
		return "文件"
	default:
		return "平铺"
	}
}

func (g groupDim) key() string {
	switch g {
	case byDomain:
		return "domain"
	case byDir:
		return "dir"
	case byFile:
		return "file"
	default:
		return "none"
	}
}

func groupDimOf(key string) groupDim {
	switch key {
	case "dir":
		return byDir
	case "file":
		return byFile
	case "none":
		return byNone
	default:
		return byDomain
	}
}

// treeRow 是清单里的一行：分组行或图片行。
type treeRow struct {
	isGroup bool
	group   string
	members []scan.Link // 分组行：当前过滤下命中的成员
	url     string      // 图片行
}

type checkTree struct {
	byURL   map[string][]scan.Link // URL → 全部引用位置
	order   []string               // URL 首次出现顺序
	checked map[string]bool
	mapped  map[string]bool // 映射表已有
	fresh   bool            // 清空映射表开关（开启时"已映射"改为"将重传"）
	folded  map[string]bool
	filter  string

	dim  groupDim
	rows []treeRow
	idx  int

	focus  bool // 面板是否处于焦点（列表屏恒为真，弹层打开时为假）
	offset int
}

func newCheckTree(links []scan.Link) *checkTree {
	t := &checkTree{
		byURL:   map[string][]scan.Link{},
		checked: map[string]bool{},
		mapped:  map[string]bool{},
		folded:  map[string]bool{},
		dim:     byDomain,
		focus:   true,
	}
	for _, l := range links {
		if _, seen := t.byURL[l.URL]; !seen {
			t.order = append(t.order, l.URL)
		}
		t.byURL[l.URL] = append(t.byURL[l.URL], l)
	}
	t.rebuild()
	return t
}

func (t *checkTree) total() int { return len(t.order) }

// groupKey 返回一条链接在指定维度下的分组名。
func groupKey(dim groupDim, l scan.Link) string {
	switch dim {
	case byDomain:
		return hostOf(l.URL)
	case byDir:
		dir := l.File
		if i := strings.LastIndexByte(dir, '/'); i > 0 {
			dir = dir[:i]
		} else {
			dir = "."
		}
		return dir
	case byFile:
		return l.File
	default:
		return ""
	}
}

func hostOf(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}

// matches 判断一条链接是否命中当前过滤词（URL / 文件名 / 分组名）。
func (t *checkTree) matches(l scan.Link) bool {
	if t.filter == "" {
		return true
	}
	f := strings.ToLower(t.filter)
	return strings.Contains(strings.ToLower(l.URL), f) ||
		strings.Contains(strings.ToLower(tailName(l.URL)), f) ||
		strings.Contains(strings.ToLower(groupKey(t.dim, l)), f)
}

// rebuild 依据过滤、分组维度与折叠状态重建行表，并尽量把光标留在原处。
func (t *checkTree) rebuild() {
	keep := ""
	if r := t.cursor(); r != nil {
		if r.isGroup {
			keep = "g:" + r.group
		} else {
			keep = "u:" + r.url
		}
	}
	t.rows = t.rows[:0]
	if t.dim == byNone {
		for _, u := range t.order {
			l := t.byURL[u][0]
			if t.matches(l) {
				t.rows = append(t.rows, treeRow{url: u})
			}
		}
	} else {
		var groupOrder []string
		members := map[string][]scan.Link{}
		for _, u := range t.order {
			l := t.byURL[u][0]
			if !t.matches(l) {
				continue
			}
			g := groupKey(t.dim, l)
			if _, ok := members[g]; !ok {
				groupOrder = append(groupOrder, g)
			}
			members[g] = append(members[g], l)
		}
		sort.SliceStable(groupOrder, func(i, j int) bool { return groupOrder[i] < groupOrder[j] })
		for _, g := range groupOrder {
			ms := members[g]
			t.rows = append(t.rows, treeRow{isGroup: true, group: g, members: ms})
			if t.folded[g] {
				continue
			}
			for _, l := range ms {
				t.rows = append(t.rows, treeRow{url: l.URL})
			}
		}
	}
	t.idx = minInt(maxInt(t.idx, 0), maxInt(len(t.rows)-1, 0))
	if keep != "" {
		for i, r := range t.rows {
			key := "u:" + r.url
			if r.isGroup {
				key = "g:" + r.group
			}
			if key == keep {
				t.idx = i
				break
			}
		}
	}
}

func (t *checkTree) cursor() *treeRow {
	if t.idx < 0 || t.idx >= len(t.rows) {
		return nil
	}
	return &t.rows[t.idx]
}

func (t *checkTree) Move(delta int) {
	t.idx = minInt(maxInt(t.idx+delta, 0), maxInt(len(t.rows)-1, 0))
}

func (t *checkTree) Top()    { t.idx = 0 }
func (t *checkTree) Bottom() { t.idx = maxInt(len(t.rows)-1, 0) }

// CursorURL 返回光标所在图片的 URL（分组行返回空串）。
func (t *checkTree) CursorURL() string {
	if r := t.cursor(); r != nil && !r.isGroup {
		return r.url
	}
	return ""
}

// groupState 返回分组三态：-1 无成员 / 0 全不选 / 1 部分 / 2 全选。
func (t *checkTree) groupState(ms []scan.Link) int {
	if len(ms) == 0 {
		return -1
	}
	on := 0
	for _, l := range ms {
		if t.checked[l.URL] {
			on++
		}
	}
	switch {
	case on == 0:
		return 0
	case on == len(ms):
		return 2
	default:
		return 1
	}
}

// Toggle 切换光标行：图片行勾选/取消；分组行整组三态切换（非全选→全选，全选→全不选）。
func (t *checkTree) Toggle() {
	r := t.cursor()
	if r == nil {
		return
	}
	if !r.isGroup {
		t.checked[r.url] = !t.checked[r.url]
		return
	}
	selectAll := t.groupState(r.members) != 2
	for _, l := range r.members {
		t.checked[l.URL] = selectAll
	}
}

// VisibleURLs 返回当前过滤命中的全部 URL（与折叠无关）。
func (t *checkTree) VisibleURLs() []string {
	var out []string
	for _, u := range t.order {
		if t.matches(t.byURL[u][0]) {
			out = append(out, u)
		}
	}
	return out
}

// SelectAll 全选可见；若已全选则全部取消。
func (t *checkTree) SelectAll() {
	vis := t.VisibleURLs()
	all := len(vis) > 0
	for _, u := range vis {
		if !t.checked[u] {
			all = false
			break
		}
	}
	for _, u := range vis {
		t.checked[u] = !all
	}
}

// Invert 反选可见项。
func (t *checkTree) Invert() {
	for _, u := range t.VisibleURLs() {
		t.checked[u] = !t.checked[u]
	}
}

// SetFold 折叠（v=true）或展开（v=false）当前分组。
func (t *checkTree) SetFold(v bool) {
	r := t.cursor()
	if r == nil || !r.isGroup {
		return
	}
	t.folded[r.group] = v
	t.rebuild()
}

// Fold 折叠/展开当前分组。
func (t *checkTree) Fold() {
	r := t.cursor()
	if r == nil || !r.isGroup {
		return
	}
	t.folded[r.group] = !t.folded[r.group]
	t.rebuild()
}

// FoldAll 全部折叠；若已全部折叠则全部展开。
func (t *checkTree) FoldAll() {
	anyOpen := false
	for _, r := range t.rows {
		if r.isGroup && !t.folded[r.group] {
			anyOpen = true
			break
		}
	}
	for _, r := range t.rows {
		if r.isGroup {
			t.folded[r.group] = anyOpen
		}
	}
	t.rebuild()
}

// CycleGroup 切换分组维度。
func (t *checkTree) CycleGroup() {
	t.dim = (t.dim + 1) % 4
	t.rebuild()
}

// CheckedURLs 返回勾选集合（按首次出现顺序）。
func (t *checkTree) CheckedURLs() []string {
	var out []string
	for _, u := range t.order {
		if t.checked[u] {
			out = append(out, u)
		}
	}
	return out
}

func (t *checkTree) CheckedCount() int {
	n := 0
	for _, u := range t.order {
		if t.checked[u] {
			n++
		}
	}
	return n
}

// SelectedLinks 返回勾选链接的全部引用位置（迁移按引用位置逐一替换）。
func (t *checkTree) SelectedLinks() []scan.Link {
	var out []scan.Link
	for _, u := range t.order {
		if t.checked[u] {
			out = append(out, t.byURL[u]...)
		}
	}
	return out
}

// Refs 返回某个 URL 的引用位置列表（file:line）。
func (t *checkTree) Refs(url string) []string {
	var out []string
	for _, l := range t.byURL[url] {
		out = append(out, l.File+":"+itoa(l.Line))
	}
	return out
}

// SetAll 全选 / 全不选。
func (t *checkTree) SetAll(on bool) {
	for _, u := range t.order {
		t.checked[u] = on
	}
}

// Only 只勾选给定 URL（用于"只重试失败项"）。
func (t *checkTree) Only(urls []string) {
	keep := map[string]bool{}
	for _, u := range urls {
		keep[u] = true
	}
	for _, u := range t.order {
		t.checked[u] = keep[u]
	}
}

// Status 返回本屏状态摘要（页脚第一行）。
func (t *checkTree) Status() string {
	files := map[string]bool{}
	domains := map[string]bool{}
	for _, l := range t.SelectedLinks() {
		files[l.File] = true
	}
	for _, u := range t.CheckedURLs() {
		domains[hostOf(u)] = true
	}
	mapped, reupload := 0, 0
	for _, u := range t.CheckedURLs() {
		if !t.mapped[u] {
			continue
		}
		if t.fresh {
			reupload++
		} else {
			mapped++
		}
	}
	parts := []string{
		"已选 " + itoa(t.CheckedCount()) + "/" + itoa(t.total()) + " 张",
		itoa(len(domains)) + " 个域名",
		itoa(len(files)) + " 个文件",
	}
	switch {
	case reupload > 0:
		parts = append(parts, itoa(reupload)+" 张将重新上传（已开清空映射表）")
	default:
		parts = append(parts, itoa(mapped)+" 张已有映射（跳过上传）")
	}
	return strings.Join(parts, " · ")
}

// View 渲染清单面板；focused 为假时用灰色边框（弹层打开时）。
func (t *checkTree) View(w, h int, focused bool) []string {
	title := "图片清单 ─ " + t.dim.String() + "分组"
	if t.dim == byNone {
		title = "图片清单 ─ 平铺"
	}
	if t.filter != "" {
		title += " · 过滤 \"" + t.filter + "\" " + itoa(len(t.VisibleURLs())) + "/" + itoa(t.total())
	}
	inner := maxInt(minInt(w, contentMax)-2, 20)
	rowsH := maxInt(h-2, 1)

	// 窗口化：让光标始终可见
	if t.idx >= t.offset+rowsH {
		t.offset = t.idx - rowsH + 1
	}
	if t.idx < t.offset {
		t.offset = t.idx
	}
	t.offset = maxInt(minInt(t.offset, maxInt(len(t.rows)-rowsH, 0)), 0)

	contentW := inner - 2
	overflow := len(t.rows) > rowsH
	if overflow {
		contentW--
	}
	var rows []string
	if len(t.rows) == 0 {
		rows = append(rows, stMuted.Render("没有匹配的图片（按 esc 清除过滤）"))
	}
	thumbStart, thumbLen := 0, rowsH
	if overflow {
		thumbLen = maxInt(rowsH*rowsH/len(t.rows), 1)
		thumbStart = t.offset * (rowsH - thumbLen) / maxInt(len(t.rows)-rowsH, 1)
	}
	end := minInt(t.offset+rowsH, len(t.rows))
	for i := t.offset; i < end; i++ {
		line := t.rowLine(i, contentW, focused)
		if overflow {
			rel := i - t.offset
			if rel >= thumbStart && rel < thumbStart+thumbLen {
				line += stAccent.Render("▕")
			} else {
				line += stFaint.Render("▕")
			}
		}
		rows = append(rows, line)
	}
	return panelBox(title, rows, inner, focused)
}

// rowLine 渲染单行（光标、三态框、名称、右列信息）。
func (t *checkTree) rowLine(i, width int, focused bool) string {
	r := t.rows[i]
	g := glyphsFor()
	isCursor := focused && i == t.idx

	cursor := " "
	if isCursor {
		cursor = stAccent.Render(g.Cursor)
	}

	if r.isGroup {
		state := t.groupState(r.members)
		box, boxStyle := g.SelNone, stMuted
		switch state {
		case 2:
			box, boxStyle = g.SelAll, stOKBox
		case 1:
			box, boxStyle = g.SelPart, stAccent
		}
		fold := g.FoldOpen
		if t.folded[r.group] {
			fold = g.FoldClosed
		}
		name := r.group
		if t.dim == byNone {
			name = "全部图片"
		}
		right := itoa(len(r.members)) + " 张"
		prefix := cursor + " " + stMuted.Render(fold) + " " + boxStyle.Render(box) + " "
		prefixW := 1 + 1 + Width(fold) + 1 + Width(box) + 1
		nameW := maxInt(width-prefixW-Width(right)-2, 6)
		row := prefix + onCursor(stBold, isCursor).Render(Fit(Truncate(name, nameW), nameW)) +
			"  " + stMuted.Render(right)
		return padRow(row, isCursor, width)
	}

	box, boxStyle := g.SelNone, stMuted
	if t.checked[r.url] {
		box, boxStyle = g.SelAll, stOKBox
	}
	name := tailPath(r.url)
	right := itoa(len(t.byURL[r.url])) + " 处"
	if t.mapped[r.url] {
		if t.fresh {
			right += " · 将重传"
		} else {
			right += " · 已映射"
		}
	}
	nameStyle := stText
	if t.mapped[r.url] && !t.fresh {
		nameStyle = stMuted
	}
	prefix := cursor + "   " + boxStyle.Render(box) + " "
	prefixW := 1 + 3 + Width(box) + 1
	nameW := maxInt(width-prefixW-Width(right)-2, 6)
	row := prefix + onCursor(nameStyle, isCursor).Render(Fit(ClipTail(name, nameW), nameW)) +
		"  " + stMuted.Render(right)
	return padRow(row, isCursor, width)
}

// padRow 给光标行铺底色并补齐到固定宽度，避免整屏高度或宽度漂移。
func padRow(row string, isCursor bool, width int) string {
	if d := width - Width(row); d > 0 {
		if isCursor {
			row += stCursor.Render(strings.Repeat(" ", d))
		} else {
			row += strings.Repeat(" ", d)
		}
	}
	return row
}

// onCursor 在光标行上追加底色。
func onCursor(st lipgloss.Style, isCursor bool) lipgloss.Style {
	if isCursor {
		return st.Background(cCursorBG)
	}
	return st
}

// tailPath 取 URL 去掉协议与域名的部分（保留目录与文件名）。
func tailPath(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return rawURL
	}
	return s
}
