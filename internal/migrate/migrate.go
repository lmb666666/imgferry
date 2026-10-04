// Package migrate 编排完整迁移流程：
// 扫描 → URL 去重 → 下载 → 上传（并发 + 频控退避 + 映射落盘）→ 批量替换 → 报告。
package migrate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lmb666666/imgferry/internal/download"
	"github.com/lmb666666/imgferry/internal/replace"
	"github.com/lmb666666/imgferry/internal/scan"
	"github.com/lmb666666/imgferry/internal/store"
	"github.com/lmb666666/imgferry/internal/upload"
)

// Stats 是迁移过程中的实时统计（供 TUI 进度屏展示）。
type Stats struct {
	Done     int `json:"done"`
	Total    int `json:"total"`
	Uploaded int `json:"uploaded"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

// 逐项事件的阶段与结果取值。
const (
	StageDownload = "download"
	StageUpload   = "upload"

	StatusUploaded = "uploaded" // 已上传并写入映射表
	StatusReused   = "reused"   // 映射表已有，跳过上传
	StatusFailed   = "failed"   // 失败（原因在 Err）
	StatusRetry    = "retry"    // 频控退避重试中
)

// ItemResult 是单个 URL 的处理结果，供 TUI 事件流逐条展示。
type ItemResult struct {
	URL     string        `json:"url"`
	NewURL  string        `json:"new_url,omitempty"`
	Name    string        `json:"name,omitempty"`
	Stage   string        `json:"stage,omitempty"`
	Status  string        `json:"status"`
	Err     string        `json:"error,omitempty"`
	Elapsed time.Duration `json:"elapsed,omitempty"`
}

// Plan 是一次迁移的影响面预估（纯本地计算：只读扫描结果与映射表，不联网）。
// TUI 预检屏与 CLI --dry-run 共用同一份计算，避免两处口径不一致。
type Plan struct {
	Files         int `json:"files"`           // 涉及的文件数
	References    int `json:"references"`      // 图片引用处数
	UniqueURLs    int `json:"unique_urls"`     // 去重后的 URL 数
	Reuse         int `json:"reuse"`           // 映射表已有（不会重复上传）
	ToUpload      int `json:"to_upload"`       // 本次需要上传
	FilesToChange int `json:"files_to_change"` // 将要修改的文件数（估算：全部成功时）
	Replacements  int `json:"replacements"`    // 将要替换的链接处数
}

// BuildPlan 依据"已选链接"与映射表估算影响面。fresh 置 true 时按"映射表已清空"计算。
func BuildPlan(selected []scan.Link, mapPath string, fresh bool) (Plan, error) {
	if mapPath == "" {
		mapPath = "migrate-map.json"
	}
	p := Plan{References: len(selected)}
	files := make(map[string]bool, len(selected))
	urls := make(map[string]bool, len(selected))
	for _, l := range selected {
		files[l.File] = true
		urls[l.URL] = true
	}
	p.Files = len(files)
	p.UniqueURLs = len(urls)
	p.FilesToChange = len(files)
	p.Replacements = len(selected)
	if !fresh && len(urls) > 0 {
		m, err := store.Load(mapPath)
		if err != nil {
			return p, err
		}
		for u := range urls {
			if e, ok := m.Get(u); ok && e.NewURL != "" {
				p.Reuse++
			}
		}
	}
	p.ToUpload = p.UniqueURLs - p.Reuse
	return p, nil
}

// Options 是一次迁移的配置。
type Options struct {
	Root             string            // 文件或目录
	Exts             map[string]bool   // 扫描的扩展名，nil 用默认列表
	AllFiles         bool              // 扫描全部文件（不限扩展名，跳过二进制与超大文件）
	ScanResult       *scan.Result      // 已有扫描结果时直接复用，避免二次扫描（TUI 传入）
	Domains          []string          // CLI：按旧图床域名过滤（不填=所有域名）
	SelectedURLs     []string          // TUI：显式选定的 URL 集合（优先于 Domains）
	Uploader         upload.Uploader   // 目标图床（dry-run 时可为 nil）
	MapPath          string            // 映射表路径，默认 ./migrate-map.json
	DownloadHeaders  map[string]string // 下载旧图的额外请求头（防盗链）
	Concurrency      int               // 上传并发，默认 2（兰空用户组有频控，别开大）
	Interval         time.Duration     // 相邻两次上传的最小间隔（全局节流）
	MaxUploadRetries int               // 频控退避重试次数（0 = 不重试；CLI 与 TUI 默认 2）
	NoBackup         bool              // 置 true 关闭替换前备份（默认开启）
	FreshMap         bool              // 置 true 先清空映射表，重新迁移全部勾选链接
	DryRun           bool
	OnProgress       func(done, total int)
	OnStats          func(Stats)
	OnItem           func(ItemResult) // 逐项结果（TUI 事件流）
	OnLog            func(format string, args ...any)
}

// Failure 记录一个失败项。
type Failure struct {
	URL   string `json:"url"`
	Stage string `json:"stage"` // download | upload | save-map
	Err   string `json:"error"`
}

// Report 是迁移结果汇总。
type Report struct {
	Files        int       `json:"files"`
	References   int       `json:"references"`
	UniqueURLs   int       `json:"unique_urls"`
	AlreadyDone  int       `json:"already_done"`
	Uploaded     int       `json:"uploaded"`
	Failed       []Failure `json:"failed,omitempty"`
	ChangedFiles int       `json:"changed_files"`
	Replacements int       `json:"replacements"`
	DryRun       bool      `json:"dry_run"`
}

// Summary 返回人类可读的汇总文本。
func (r *Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "文件 %d 个，图片引用 %d 处，去重 URL %d 个",
		r.Files, r.References, r.UniqueURLs)
	if r.DryRun {
		fmt.Fprintf(&b, "\ndry-run 预览：待上传 %d 个，映射表已有 %d 个（未做任何修改）",
			r.UniqueURLs-r.AlreadyDone, r.AlreadyDone)
	} else {
		fmt.Fprintf(&b, "\n新上传 %d 个，映射表复用 %d 个，失败 %d 个\n更新文件 %d 个，替换链接 %d 处",
			r.Uploaded, r.AlreadyDone, len(r.Failed), r.ChangedFiles, r.Replacements)
	}
	for _, f := range r.Failed {
		fmt.Fprintf(&b, "\n✗ [%s] %s：%s", f.Stage, f.URL, f.Err)
	}
	return b.String()
}

// Run 执行一次完整迁移。
func Run(ctx context.Context, opt Options) (*Report, error) {
	if opt.Exts == nil {
		opt.Exts = scan.DefaultExts
	}
	if opt.MapPath == "" {
		opt.MapPath = "migrate-map.json"
	}
	if opt.Concurrency <= 0 {
		opt.Concurrency = 2
	}
	if opt.MaxUploadRetries < 0 {
		opt.MaxUploadRetries = 0
	}
	logf := opt.OnLog
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if !opt.DryRun && opt.Uploader == nil {
		return nil, errors.New("未指定目标图床")
	}

	rep := &Report{DryRun: opt.DryRun}
	exts := opt.Exts
	if opt.AllFiles {
		exts = scan.ExtsAll
	}
	// 已有扫描结果时直接复用（TUI 传入）：既省一次全量读盘，也保证
	// "用户看到并勾选的清单"与"实际迁移的清单"完全一致。
	res := opt.ScanResult
	if res == nil {
		var err error
		res, err = scan.Scan(opt.Root, exts, nil)
		if err != nil {
			return nil, err
		}
	}
	// 映射表自身存的是旧链接，无论叫什么名字都不参与迁移
	mapFile := filepath.Clean(opt.MapPath)
	var scanned []scan.Link
	for _, l := range res.Links {
		if filepath.Clean(l.File) != mapFile {
			scanned = append(scanned, l)
		}
	}
	links := chooseLinks(scanned, opt)
	// 涉及文件数 = 出现所选图片的文件数
	fileSet := make(map[string]bool)
	for _, l := range links {
		fileSet[l.File] = true
	}
	rep.Files = len(fileSet)
	rep.References = len(links)
	urls := scan.UniqueURLs(links)
	rep.UniqueURLs = len(urls)
	if len(urls) == 0 {
		logf("未发现可迁移的图片链接")
		return rep, nil
	}

	m, err := store.Load(opt.MapPath)
	if err != nil {
		return nil, err
	}
	if opt.FreshMap {
		m.Clear()
		logf("已清空映射表")
	}

	if opt.DryRun {
		plan, err := BuildPlan(links, opt.MapPath, opt.FreshMap)
		if err != nil {
			return nil, err
		}
		rep.AlreadyDone = plan.Reuse
		logf("dry-run：%s", rep.Summary())
		return rep, nil
	}

	if err := opt.Uploader.Validate(); err != nil {
		return nil, fmt.Errorf("目标图床校验失败: %w", err)
	}

	dl := download.New(download.Options{Headers: opt.DownloadHeaders})

	var limiter <-chan time.Time
	if opt.Interval > 0 {
		t := time.NewTicker(opt.Interval)
		defer t.Stop()
		limiter = t.C
	}

	itemf := opt.OnItem
	if itemf == nil {
		itemf = func(ItemResult) {}
	}

	var (
		mu sync.Mutex
		st Stats
	)
	st.Total = len(urls)

	// 注意：errgroup 派生的 ctx 在 Wait 返回时必然被取消，不能用它判断"用户是否取消"，
	// 因此保留用户传入的 ctx（下称 userCtx）用于取消判定，goroutine 内用派生 ctx。
	userCtx := ctx
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(opt.Concurrency)
	for _, u := range urls {
		u := u
		g.Go(func() error {
			started := time.Now()
			defer func() {
				mu.Lock()
				st.Done++
				if opt.OnProgress != nil {
					opt.OnProgress(st.Done, st.Total)
				}
				if opt.OnStats != nil {
					opt.OnStats(st)
				}
				mu.Unlock()
			}()

			if e, ok := m.Get(u); ok && e.NewURL != "" {
				mu.Lock()
				rep.AlreadyDone++
				st.Skipped++
				mu.Unlock()
				itemf(ItemResult{URL: u, Status: StatusReused, Name: e.Name,
					Elapsed: time.Since(started)})
				logf("跳过（映射表已有）: %s", u)
				return nil
			}

			if ctx.Err() != nil { // 已取消：不再开始新的下载
				return nil
			}
			f, err := dl.Fetch(ctx, u)
			if err != nil {
				if ctx.Err() != nil { // 取消导致的中断不算失败（下次续传）
					return nil
				}
				mu.Lock()
				rep.Failed = append(rep.Failed, Failure{URL: u, Stage: StageDownload, Err: err.Error()})
				st.Failed++
				mu.Unlock()
				itemf(ItemResult{URL: u, Stage: StageDownload, Status: StatusFailed,
					Err: err.Error(), Elapsed: time.Since(started)})
				logf("下载失败: %s（%v）", u, err)
				return nil
			}

			publicURL, err := uploadWithRetry(ctx, opt.Uploader, f.Name, f.Data, f.ContentType,
				opt.MaxUploadRetries, limiter, logf,
				func(wait time.Duration, attempt, maxAttempts int) {
					itemf(ItemResult{URL: u, Name: f.Name, Stage: StageUpload, Status: StatusRetry,
						Err: fmt.Sprintf("站点限流，%v 后重试（%d/%d）",
							wait.Round(time.Second), attempt, maxAttempts)})
				})
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				mu.Lock()
				rep.Failed = append(rep.Failed, Failure{URL: u, Stage: StageUpload, Err: err.Error()})
				st.Failed++
				mu.Unlock()
				itemf(ItemResult{URL: u, Name: f.Name, Stage: StageUpload, Status: StatusFailed,
					Err: err.Error(), Elapsed: time.Since(started)})
				logf("上传失败: %s（%v）", u, err)
				return nil
			}

			mu.Lock()
			m.Set(u, store.Entry{NewURL: publicURL, Name: f.Name, UploadedAt: time.Now()})
			if err := m.Save(); err != nil {
				rep.Failed = append(rep.Failed, Failure{URL: u, Stage: "save-map", Err: err.Error()})
			}
			rep.Uploaded++
			st.Uploaded++
			mu.Unlock()
			itemf(ItemResult{URL: u, NewURL: publicURL, Name: f.Name, Stage: StageUpload,
				Status: StatusUploaded, Elapsed: time.Since(started)})
			logf("已迁移: %s → %s", u, publicURL)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return rep, err
	}
	// 用户取消：跳过替换阶段——来源文件保持原样，已写入映射表的上传记录留作续传，
	// 下次重跑时会命中映射表直接替换（幂等）。
	if err := userCtx.Err(); err != nil {
		logf("已取消：跳过替换阶段，来源文件未修改；已上传 %d 张已写入映射表，重跑可续传", rep.Uploaded)
		logf("%s", rep.Summary())
		return rep, err
	}

	// 替换阶段：只改扫描涉及、且至少有一条映射命中的文件
	mappings := make(map[string]string, len(urls))
	for _, u := range urls {
		if e, ok := m.Get(u); ok && e.NewURL != "" {
			mappings[u] = e.NewURL
		}
	}
	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}
	sort.Strings(files)
	changed, replaced, err := replace.Apply(files, mappings, !opt.NoBackup)
	if err != nil {
		return rep, fmt.Errorf("替换失败: %w", err)
	}
	rep.ChangedFiles = changed
	rep.Replacements = replaced

	if len(rep.Failed) > 0 {
		logf("有 %d 个 URL 失败，其引用未替换", len(rep.Failed))
	}
	logf("%s", rep.Summary())
	return rep, nil
}

// chooseLinks 依据选项从扫描结果中挑选要迁移的链接：
// TUI 显式集合优先；CLI 按域名过滤，只迁图片类。
func chooseLinks(links []scan.Link, opt Options) []scan.Link {
	if len(opt.SelectedURLs) > 0 {
		want := make(map[string]bool, len(opt.SelectedURLs))
		for _, u := range opt.SelectedURLs {
			want[u] = true
		}
		var out []scan.Link
		for _, l := range links {
			if want[l.URL] {
				out = append(out, l)
			}
		}
		return out
	}
	links = scan.FilterURLs(links, opt.Domains)
	var out []scan.Link
	for _, l := range links {
		if l.Kind == scan.KindImage {
			out = append(out, l)
		}
	}
	return out
}

// uploadWithRetry 对频控类错误做指数退避重试（2s、4s、…）。
func uploadWithRetry(ctx context.Context, up upload.Uploader, name string, data []byte,
	contentType string, maxRetries int, limiter <-chan time.Time, logf func(string, ...any),
	onRetry func(wait time.Duration, attempt, maxAttempts int),
) (string, error) {
	for attempt := 0; ; attempt++ {
		if limiter != nil {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-limiter:
			}
		}
		publicURL, err := up.Upload(name, data, contentType)
		if err == nil {
			return publicURL, nil
		}
		if attempt >= maxRetries || !upload.IsRateLimited(err) {
			return "", err
		}
		wait := time.Duration(1<<uint(attempt+1)) * time.Second
		if onRetry != nil {
			onRetry(wait, attempt+1, maxRetries)
		}
		logf("触发图床频控，%v 后重试（第 %d/%d 次）: %s", wait, attempt+1, maxRetries, name)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
}
