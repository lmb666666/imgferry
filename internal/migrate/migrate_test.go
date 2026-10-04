package migrate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lmb666666/imgferry/internal/scan"
	"github.com/lmb666666/imgferry/internal/store"
	"github.com/lmb666666/imgferry/internal/upload/lsky"
)

// TestEndToEnd 覆盖主流程：
// 两份 md 引用同一张图 + 一张已 404 的图 → 上传去重（1 次）→ 替换 3 处 →
// 备份生成 → 失败项不替换 → 重跑幂等（不再上传）。
func TestEndToEnd(t *testing.T) {
	var uploadCalls int32
	lskySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/profile":
			fmt.Fprint(w, `{"status":true,"message":"success"}`)
		case "/api/v1/upload":
			atomic.AddInt32(&uploadCalls, 1)
			fmt.Fprint(w, `{"status":true,"message":"上传成功","data":{"links":{"url":"https://new.example.com/i/x1.png"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer lskySrv.Close()

	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/img/a.png":
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "\x89PNG fake")
		default:
			http.NotFound(w, r)
		}
	}))
	defer oldSrv.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte(fmt.Sprintf(
		"![x](%s/img/a.png)\n![y](%s/img/a.png)\n![gone](%s/img/gone.png)\n",
		oldSrv.URL, oldSrv.URL, oldSrv.URL)), 0o644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte(fmt.Sprintf(
		"![z](%s/img/a.png)\n", oldSrv.URL)), 0o644)

	mapPath := filepath.Join(dir, "map.json")
	up, err := lsky.New(lsky.Config{BaseURL: lskySrv.URL, Token: "1|test"})
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Run(context.Background(), Options{
		Root:     dir,
		Uploader: up,
		MapPath:  mapPath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rep.Files != 2 || rep.References != 4 || rep.UniqueURLs != 2 {
		t.Fatalf("scan mismatch: %+v", rep)
	}
	if rep.Uploaded != 1 || rep.AlreadyDone != 0 {
		t.Fatalf("upload dedupe mismatch: uploaded=%d already=%d", rep.Uploaded, rep.AlreadyDone)
	}
	if len(rep.Failed) != 1 || rep.Failed[0].Stage != "download" {
		t.Fatalf("want 1 download failure, got %+v", rep.Failed)
	}
	if rep.ChangedFiles != 2 || rep.Replacements != 3 {
		t.Fatalf("replace mismatch: files=%d n=%d", rep.ChangedFiles, rep.Replacements)
	}
	if got := atomic.LoadInt32(&uploadCalls); got != 1 {
		t.Fatalf("upload calls = %d, want 1", got)
	}

	aContent, _ := os.ReadFile(filepath.Join(dir, "a.md"))
	want := fmt.Sprintf("![x](https://new.example.com/i/x1.png)\n![y](https://new.example.com/i/x1.png)\n![gone](%s/img/gone.png)\n", oldSrv.URL)
	if string(aContent) != want {
		t.Fatalf("a.md mismatch:\ngot  %q\nwant %q", aContent, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.md.bak")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.md.bak")); err != nil {
		t.Fatalf("b backup missing: %v", err)
	}

	// 重跑：成功迁移的旧链接已被替换掉，不再出现；剩余失败项不重复上传、不改文件
	rep2, err := Run(context.Background(), Options{
		Root:     dir,
		Uploader: up,
		MapPath:  mapPath,
	})
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if rep2.Uploaded != 0 || rep2.ChangedFiles != 0 || rep2.Replacements != 0 {
		t.Fatalf("re-run should be a no-op: %+v", rep2)
	}
	if got := atomic.LoadInt32(&uploadCalls); got != 1 {
		t.Fatalf("re-run upload calls = %d, want 1", got)
	}
}

// TestResumeFromMap 模拟中断续传：图片已上传（映射表已有记录）但文件尚未替换，
// 重跑时命中映射表、不重复上传，且完成替换。
func TestResumeFromMap(t *testing.T) {
	var uploadCalls int32
	lskySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/upload" {
			atomic.AddInt32(&uploadCalls, 1)
			fmt.Fprint(w, `{"status":true,"data":{"links":{"url":"https://new.example.com/i/should-not-happen.png"}}}`)
			return
		}
		fmt.Fprint(w, `{"status":true,"message":"success"}`)
	}))
	defer lskySrv.Close()

	dir := t.TempDir()
	oldURL := "https://old.example.com/img/a.png"
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("![x]("+oldURL+")\n"), 0o644)

	mapPath := filepath.Join(dir, "map.json")
	m, err := store.Load(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Set(oldURL, store.Entry{NewURL: "https://new.example.com/i/x1.png", Name: "a.png"})
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}

	up, _ := lsky.New(lsky.Config{BaseURL: lskySrv.URL, Token: "1|test"})
	rep, err := Run(context.Background(), Options{
		Root:     dir,
		Uploader: up,
		MapPath:  mapPath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.AlreadyDone != 1 || rep.Uploaded != 0 {
		t.Fatalf("resume mismatch: already=%d uploaded=%d", rep.AlreadyDone, rep.Uploaded)
	}
	if rep.ChangedFiles != 1 || rep.Replacements != 1 {
		t.Fatalf("replace mismatch: files=%d n=%d", rep.ChangedFiles, rep.Replacements)
	}
	if got := atomic.LoadInt32(&uploadCalls); got != 0 {
		t.Fatalf("resume should not upload, upload calls = %d", got)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.md"))
	if string(data) != "![x](https://new.example.com/i/x1.png)\n" {
		t.Fatalf("a.md not replaced: %q", data)
	}
}

func TestDryRun(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("![x](https://old/a.png)\n"), 0o644)

	rep, err := Run(context.Background(), Options{
		Root:    dir,
		DryRun:  true,
		MapPath: filepath.Join(dir, "map.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.UniqueURLs != 1 || rep.Uploaded != 0 {
		t.Fatalf("dry-run report mismatch: %+v", rep)
	}
	// 文件未被修改
	data, _ := os.ReadFile(filepath.Join(dir, "a.md"))
	if string(data) != "![x](https://old/a.png)\n" {
		t.Fatal("dry-run must not modify files")
	}
}

func TestNoUploaderWithoutDryRun(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("![x](https://old/a.png)\n"), 0o644)
	if _, err := Run(context.Background(), Options{Root: dir}); err == nil {
		t.Fatal("want error when uploader missing and not dry-run")
	}
}

// TestSelectedURLs 验证 TUI 勾选路径：只迁移显式选定的 URL，其余引用保持原样。
func TestSelectedURLs(t *testing.T) {
	lskySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/upload" {
			fmt.Fprint(w, `{"status":true,"data":{"links":{"url":"https://new.example.com/i/x1.png"}}}`)
			return
		}
		fmt.Fprint(w, `{"status":true,"message":"success"}`)
	}))
	defer lskySrv.Close()

	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, "\x89PNG fake")
	}))
	defer oldSrv.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte(fmt.Sprintf(
		"![x](%s/img/a.png)\n![y](%s/img/b.png)\n", oldSrv.URL, oldSrv.URL)), 0o644)

	up, _ := lsky.New(lsky.Config{BaseURL: lskySrv.URL, Token: "1|test"})
	rep, err := Run(context.Background(), Options{
		Root:         dir,
		Uploader:     up,
		MapPath:      filepath.Join(dir, "map.json"),
		SelectedURLs: []string{oldSrv.URL + "/img/a.png"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.UniqueURLs != 1 || rep.Uploaded != 1 || rep.Replacements != 1 {
		t.Fatalf("selected mismatch: %+v", rep)
	}
	content, _ := os.ReadFile(filepath.Join(dir, "a.md"))
	want := fmt.Sprintf("![x](https://new.example.com/i/x1.png)\n![y](%s/img/b.png)\n", oldSrv.URL)
	if string(content) != want {
		t.Fatalf("content mismatch:\ngot  %q\nwant %q", content, want)
	}
}

// TestMediaSkipped 验证音视频链接不会被扫描迁移，管道只处理图片。
func TestMediaSkipped(t *testing.T) {
	lskySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/upload" {
			fmt.Fprint(w, `{"status":true,"data":{"links":{"url":"https://new.example.com/i/x1.png"}}}`)
			return
		}
		fmt.Fprint(w, `{"status":true,"message":"success"}`)
	}))
	defer lskySrv.Close()

	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		fmt.Fprint(w, "data")
	}))
	defer oldSrv.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte(fmt.Sprintf(
		"![x](%s/img/a.png)\nsrc: %s/media/b.mp3\n",
		oldSrv.URL, oldSrv.URL)), 0o644)
	mapPath := filepath.Join(dir, "map.json")
	up, _ := lsky.New(lsky.Config{BaseURL: lskySrv.URL, Token: "1|test"})

	rep, err := Run(context.Background(), Options{Root: dir, Uploader: up, MapPath: mapPath})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.UniqueURLs != 1 || rep.Uploaded != 1 || rep.Replacements != 1 {
		t.Fatalf("media should be skipped: %+v", rep)
	}
	content, _ := os.ReadFile(filepath.Join(dir, "a.md"))
	if !strings.Contains(string(content), ".mp3") {
		t.Fatal("mp3 link should stay untouched")
	}
	if !strings.Contains(string(content), "https://new.example.com/i/x1.png") {
		t.Fatal("image link should be replaced")
	}
}

// TestBuildPlanMatchesRun 断言预检数字与实际执行一致（同一映射表口径）。
func TestBuildPlanMatchesRun(t *testing.T) {
	dir := t.TempDir()
	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write([]byte("fake-image-bytes"))
	}))
	defer oldSrv.Close()
	lskySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/profile":
			_, _ = w.Write([]byte(`{"status":true,"data":{"name":"tester"}}`))
		case "/api/v1/upload":
			_, _ = w.Write([]byte(`{"status":true,"data":{"links":{"url":"https://new.host/i/1.webp"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer lskySrv.Close()

	md := filepath.Join(dir, "a.md")
	body := "![a](" + oldSrv.URL + "/1.webp)\n![b](" + oldSrv.URL + "/2.webp)\n![c](" + oldSrv.URL + "/3.webp)\n"
	if err := os.WriteFile(md, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(dir, "migrate-map.json")

	res, err := scan.Scan(dir, scan.DefaultExts, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(res.Links, mapPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.UniqueURLs != 3 || plan.Reuse != 0 || plan.ToUpload != 3 {
		t.Fatalf("首次计划不符：%+v", plan)
	}

	// 第一次跑两个 URL，只留下一个未迁移
	up, err := lsky.New(lsky.Config{BaseURL: lskySrv.URL, Token: "1|t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{
		Root: dir, ScanResult: res, MapPath: mapPath, Uploader: up, Concurrency: 1,
		SelectedURLs: []string{res.Links[0].URL, res.Links[1].URL},
	}); err != nil {
		t.Fatal(err)
	}

	plan2, err := BuildPlan(res.Links, mapPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan2.Reuse != 2 || plan2.ToUpload != 1 {
		t.Fatalf("续跑计划不符：%+v", plan2)
	}
	rep, err := Run(context.Background(), Options{
		Root: dir, ScanResult: res, MapPath: mapPath, Uploader: up, Concurrency: 1,
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.AlreadyDone != plan2.Reuse {
		t.Fatalf("dry-run 复用数 %d 与计划 %d 不一致", rep.AlreadyDone, plan2.Reuse)
	}
	if plan2.ToUpload != plan2.UniqueURLs-plan2.Reuse {
		t.Fatalf("待上传数应由去重数与复用数推出：%+v", plan2)
	}
}

// TestCancelSkipsReplace 断言取消迁移后：不执行替换（来源文件未修改、无备份）、
// 已上传的记录写入映射表、取消不算失败项。
func TestCancelSkipsReplace(t *testing.T) {
	dir := t.TempDir()
	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write([]byte("bytes"))
	}))
	defer oldSrv.Close()
	lskySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/upload" {
			_, _ = w.Write([]byte(`{"status":true,"data":{"links":{"url":"https://new.host/i/1.webp"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":true,"data":{"name":"tester"}}`))
	}))
	defer lskySrv.Close()

	var links []string
	for i := 0; i < 5; i++ {
		links = append(links, fmt.Sprintf("![%d](%s/%d.webp)", i, oldSrv.URL, i))
	}
	mdPath := filepath.Join(dir, "a.md")
	body := strings.Join(links, "\n") + "\n"
	if err := os.WriteFile(mdPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := scan.Scan(dir, scan.DefaultExts, nil)
	if err != nil {
		t.Fatal(err)
	}
	up, err := lsky.New(lsky.Config{BaseURL: lskySrv.URL, Token: "1|t"})
	if err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(dir, "migrate-map.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var once sync.Once
	rep, err := Run(ctx, Options{
		Root: dir, ScanResult: res, MapPath: mapPath, Uploader: up, Concurrency: 1,
		OnItem: func(it ItemResult) {
			if it.Status == StatusUploaded { // 第一个上传成功后就取消
				once.Do(cancel)
			}
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消后应返回 context.Canceled，实际 %v", err)
	}
	if rep.ChangedFiles != 0 || rep.Replacements != 0 {
		t.Fatalf("取消后不应执行替换：%+v", rep)
	}
	if len(rep.Failed) != 0 {
		t.Fatalf("取消不算失败项，实际 %d 条：%+v", len(rep.Failed), rep.Failed)
	}
	after, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatal("取消后来源文件必须保持原样")
	}
	if _, err := os.Stat(mdPath + ".bak"); err == nil {
		t.Fatal("取消后不应生成备份")
	}
	m, err := store.Load(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) == 0 {
		t.Fatal("已上传的部分应写入映射表（供续传）")
	}
	if rep.Uploaded != len(m.Entries) {
		t.Fatalf("上传数与映射表条数应一致：%d vs %d", rep.Uploaded, len(m.Entries))
	}
}
