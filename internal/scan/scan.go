// Package scan 发现待扫描文件并提取其中的链接。
// 扫描策略是"宽进严出"：抓取文件里出现的全部 http(s) URL（任意引用形式），
// 自动分类（图片 / 其他链接），音视频 URL 直接跳过；迁移只处理图片类。
package scan

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// 链接分类。
const (
	KindImage = "image" // 图片：图片语法引用，或 URL 带图片扩展名
	KindLink  = "link"  // 其他链接（网页、代码仓库等；不会被迁移）
)

// Link 是从文件里提取到的一条链接引用。
type Link struct {
	File string // 所在文件路径
	Line int    // 1-based 行号
	URL  string // 链接地址
	Kind string // KindImage / KindLink
}

// Result 汇总一次扫描。
type Result struct {
	Files int
	Links []Link
}

// DefaultExts 是默认扫描的文本文件扩展名。
var DefaultExts = map[string]bool{
	// 文档
	".md": true, ".markdown": true, ".html": true, ".htm": true, ".txt": true,
	".org": true, ".rst": true, ".adoc": true, ".tex": true,
	// Web 前端
	".css": true, ".scss": true, ".less": true, ".js": true, ".mjs": true,
	".cjs": true, ".ts": true, ".tsx": true, ".jsx": true, ".vue": true,
	".svelte": true, ".json": true, ".jsonc": true, ".yaml": true, ".yml": true,
	".toml": true, ".xml": true, ".svg": true,
	// 后端与脚本
	".go": true, ".py": true, ".rb": true, ".php": true, ".java": true,
	".kt": true, ".swift": true, ".rs": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".cs": true, ".sh": true, ".bash": true,
	".zsh": true, ".sql": true, ".lua": true, ".pl": true,
	// 配置
	".ini": true, ".cfg": true, ".conf": true, ".env": true,
}

// ExtsAll 是"扫描全部文件"模式的哨兵值：不限扩展名，
// 自动跳过二进制文件、超过 MaxScanFileSize 的文件，以及备份/映射表等派生文件。
var ExtsAll = map[string]bool{"*": true}

// MaxScanFileSize 是全文件模式下单个文件的大小上限。
var MaxScanFileSize int64 = 8 << 20

var (
	// htmlImgTagRe 匹配整个 <img> 标签（允许属性跨行），从中提取 src。
	htmlImgTagRe = regexp.MustCompile(`(?is)<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']`)
	// srcAttrRe 匹配行内的 src / data-src / data-original 等属性（懒加载图片）。
	srcAttrRe = regexp.MustCompile(`(?i)\b(?:src|data-(?:original|thumb|echo|lazy-src))\s*=\s*["']([^"']+)["']`)
	// srcsetAttrRe 匹配 srcset 属性，值按逗号拆分后逐个提取。
	srcsetAttrRe = regexp.MustCompile(`(?i)\bsrcset\s*=\s*["']([^"']+)["']`)
	// cssURLRe 匹配 CSS 的 url(...)。
	cssURLRe = regexp.MustCompile(`(?i)\burl\(\s*['"]?([^'")\s]+)['"]?\s*\)`)
	// kvImageRe 匹配 front matter 的 "键: URL" 形式（Hexo 的 cover/image 等）。
	kvImageRe = regexp.MustCompile(`^\s*[A-Za-z0-9_-]+\s*:\s*<?(https?://[^\s>]+)>?\s*$`)
	// anyURLRe 兜底抓取行内任意 URL（普通链接、裸 URL 等）。
	// 仅匹配可打印 ASCII 字符：URL 出现在中文文本里时天然在中文处截断。
	anyURLRe = regexp.MustCompile(`https?://[!-~]+`)
)

var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".svg": true, ".avif": true, ".bmp": true,
}

// mediaExts 是音视频扩展名：这些 URL 直接跳过、不进入扫描结果（本工具只迁图片）。
var mediaExts = map[string]bool{
	".mp3": true, ".mp4": true, ".webm": true, ".mov": true, ".avi": true,
	".mkv": true, ".m4a": true, ".flac": true, ".wav": true, ".ogg": true,
}

// trailingPunct 是 URL 末尾常见的引号、括号、中英文标点，
// 抓取时剔除（如行尾句号、代码引号），替换时保留在原处。
const trailingPunct = ".,;:!?`'\"<>)、。，！？；：）】」』》〉"

// FindFiles 返回 root（目录或单个文件）下所有匹配扩展名的文件。
// 跳过隐藏目录、node_modules 与 vendor；始终排除 .bak/.tmp 备份和映射表文件。
func FindFiles(root string, exts map[string]bool) ([]string, error) {
	all := exts["*"]
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if all || exts[strings.ToLower(filepath.Ext(root))] {
			return []string{root}, nil
		}
		return nil, fmt.Errorf("%s 不是可扫描的文本文件", root)
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root &&
				(strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
				return fs.SkipDir
			}
			return nil
		}
		// 派生文件一律排除：备份里存的是旧链接，映射表自身也不该被改写
		if strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, ".tmp") ||
			name == "migrate-map.json" {
			return nil
		}
		if all || exts[strings.ToLower(filepath.Ext(name))] {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// isBinary 采用 Git 的判定方式：数据前部出现 NUL 字节即视为二进制。
func isBinary(content []byte) bool {
	if len(content) > 8000 {
		content = content[:8000]
	}
	return bytes.IndexByte(content, 0) >= 0
}

// urlSink 是提取结果的汇聚回调。
type urlSink func(lineNo int, rawURL, kind string)

// ExtractLinks 按行提取一份文件内容中的全部 URL 并分类；音视频 URL 直接跳过。
func ExtractLinks(file string, content []byte) []Link {
	var links []Link
	seen := map[string]bool{}
	add := func(lineNo int, rawURL, kind string) {
		if isMediaURL(rawURL) {
			return
		}
		key := fmt.Sprintf("%d|%s", lineNo, rawURL)
		if seen[key] {
			return
		}
		seen[key] = true
		links = append(links, Link{File: file, Line: lineNo, URL: rawURL, Kind: kind})
	}

	// Markdown 走 goldmark AST：引用式图片、括号嵌套等按 CommonMark 语义解析
	switch strings.ToLower(path.Ext(file)) {
	case ".md", ".markdown":
		extractMarkdown(content, add)
	}

	// HTML 的 <img> 标签属性可能跨行，整篇匹配后按字节偏移换算行号；
	// 同时记录标签区间，逐行扫描时跳过区间内的 URL，避免同一 src 重复捕获。
	var imgRanges [][2]int
	for _, loc := range htmlImgTagRe.FindAllSubmatchIndex(content, -1) {
		lineNo := 1 + bytes.Count(content[:loc[0]], []byte("\n"))
		add(lineNo, string(content[loc[2]:loc[3]]), KindImage)
		for _, u := range tagExtraURLs(content[loc[0]:loc[1]]) {
			add(lineNo, u, classify(u))
		}
		imgRanges = append(imgRanges, [2]int{loc[0], loc[1]})
	}

	// 手动按 '\n' 切行以获得精确的字节偏移
	rest := content
	lineNo := 0
	for {
		lineNo++
		idx := bytes.IndexByte(rest, '\n')
		var line []byte
		if idx >= 0 {
			line = rest[:idx]
		} else {
			line = rest
		}
		lineStart := len(content) - len(rest)
		s := string(line)

		if m := kvImageRe.FindStringSubmatch(s); m != nil {
			u := strings.TrimRight(m[1], trailingPunct)
			if !inRanges(imgRanges, lineStart+strings.Index(s, m[1])) {
				add(lineNo, u, classify(u))
			}
		}
		for _, m := range srcAttrRe.FindAllStringSubmatchIndex(s, -1) {
			if inRanges(imgRanges, lineStart+m[0]) {
				continue
			}
			u := strings.TrimRight(s[m[2]:m[3]], trailingPunct)
			add(lineNo, u, classify(u))
		}
		for _, m := range srcsetAttrRe.FindAllStringSubmatchIndex(s, -1) {
			if inRanges(imgRanges, lineStart+m[0]) {
				continue
			}
			for _, u := range splitSrcset(s[m[2]:m[3]]) {
				add(lineNo, u, classify(u))
			}
		}
		for _, m := range cssURLRe.FindAllStringSubmatchIndex(s, -1) {
			if inRanges(imgRanges, lineStart+m[0]) {
				continue
			}
			u := strings.TrimRight(s[m[2]:m[3]], trailingPunct)
			add(lineNo, u, classify(u))
		}
		for _, m := range anyURLRe.FindAllStringSubmatchIndex(s, -1) {
			if inRanges(imgRanges, lineStart+m[0]) {
				continue
			}
			u := strings.TrimRight(s[m[0]:m[1]], trailingPunct)
			add(lineNo, u, classify(u))
		}

		if idx < 0 {
			break
		}
		rest = rest[idx+1:]
	}

	sort.SliceStable(links, func(i, j int) bool { return links[i].Line < links[j].Line })
	return links
}

// extractMarkdown 用 goldmark 解析 AST，捕获内联与引用式的图片/链接目标。
// 行号取 URL 首次出现的位置，仅供展示。
func extractMarkdown(content []byte, add urlSink) {
	doc := goldmark.New().Parser().Parse(text.NewReader(content))
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest []byte
		switch t := n.(type) {
		case *ast.Image:
			dest = t.Destination
		case *ast.Link:
			dest = t.Destination
		default:
			return ast.WalkContinue, nil
		}
		u := string(dest)
		if u == "" {
			return ast.WalkContinue, nil
		}
		line := 1
		if i := bytes.Index(content, []byte(u)); i >= 0 {
			line = 1 + bytes.Count(content[:i], []byte("\n"))
		}
		add(line, u, classify(u))
		return ast.WalkContinue, nil
	})
}

// tagExtraURLs 从 <img> 标签文本中提取 srcset 逐项与 data-* 懒加载地址。
func tagExtraURLs(tag []byte) []string {
	var out []string
	for _, m := range srcsetAttrRe.FindAllSubmatch(tag, -1) {
		out = append(out, splitSrcset(string(m[1]))...)
	}
	for _, m := range srcAttrRe.FindAllSubmatch(tag, -1) {
		u := strings.TrimRight(string(m[1]), trailingPunct)
		if u != "" {
			out = append(out, u)
		}
	}
	return out
}

// splitSrcset 把 srcset 的值拆成单个 URL（丢弃分辨率描述符）。
func splitSrcset(v string) []string {
	var out []string
	for _, entry := range strings.Split(v, ",") {
		fields := strings.Fields(entry)
		if len(fields) == 0 {
			continue
		}
		out = append(out, strings.TrimRight(fields[0], trailingPunct))
	}
	return out
}

func inRanges(ranges [][2]int, pos int) bool {
	for _, r := range ranges {
		if pos >= r[0] && pos < r[1] {
			return true
		}
	}
	return false
}

// classify 按 URL 扩展名判断链接类型。
func classify(rawURL string) string {
	if isMediaURL(rawURL) {
		return KindLink // 音视频不会进入结果，此处仅为语义完整
	}
	if isImageURL(rawURL) {
		return KindImage
	}
	return KindLink
}

func isImageURL(rawURL string) bool {
	return hasExt(rawURL, imageExts)
}

func isMediaURL(rawURL string) bool {
	return hasExt(rawURL, mediaExts)
}

func hasExt(rawURL string, set map[string]bool) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return set[strings.ToLower(path.Ext(u.Path))]
}

// FilterURLs 只保留 http(s) 外链；domains 非空时仅保留命中域名的链接
// （相对路径与本地文件一律跳过）。
func FilterURLs(links []Link, domains []string) (kept []Link) {
	for _, l := range links {
		u, err := url.Parse(l.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		if len(domains) > 0 && !matchDomain(u.Hostname(), domains) {
			continue
		}
		kept = append(kept, l)
	}
	return kept
}

func matchDomain(host string, domains []string) bool {
	h := strings.ToLower(host)
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		d = strings.TrimPrefix(d, "www.")
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

// UniqueURLs 返回去重后的 URL 列表，保持出现顺序。
func UniqueURLs(links []Link) []string {
	seen := make(map[string]bool, len(links))
	var out []string
	for _, l := range links {
		if !seen[l.URL] {
			seen[l.URL] = true
			out = append(out, l.URL)
		}
	}
	return out
}

// UniqueURLs 返回本结果的去重 URL 列表。
func (r *Result) UniqueURLs() []string { return UniqueURLs(r.Links) }

// Scan 扫描 root 并返回全部链接（含分类，未按域名过滤）。
// exts 传 ExtsAll 时进入全文件模式：不限扩展名，跳过二进制与超大文件。
func Scan(root string, exts map[string]bool, domains []string) (*Result, error) {
	return ScanWithProgress(context.Background(), root, exts, domains, nil)
}

// ScanWithProgress 与 Scan 相同，额外回报"已读文件数 / 已发现链接数"
// （onProgress 可为 nil；节流由调用方决定，扫描本身不等待回调）。
func ScanWithProgress(ctx context.Context, root string, exts map[string]bool, domains []string,
	onProgress func(read, found int)) (*Result, error) {
	if exts == nil {
		exts = DefaultExts
	}
	allFiles := exts["*"]
	files, err := FindFiles(root, exts)
	if err != nil {
		return nil, err
	}
	res := &Result{Files: len(files)}
	var links []Link
	for i, f := range files {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if onProgress != nil {
			onProgress(i+1, len(links))
		}
		if allFiles {
			if fi, statErr := os.Stat(f); statErr == nil && fi.Size() > MaxScanFileSize {
				continue
			}
		}
		content, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if allFiles && isBinary(content) {
			continue
		}
		links = append(links, ExtractLinks(f, content)...)
	}
	res.Links = FilterURLs(links, domains)
	return res, nil
}
