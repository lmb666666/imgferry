package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmb666666/imgferry/internal/migrate"
)

// 尺寸矩阵：覆盖宽屏、标准、窄屏与过小终端。
var screenSizes = [][2]int{{80, 24}, {100, 30}, {140, 40}, {60, 20}, {40, 12}, {32, 10}}

// buildScreen 返回测试用的屏幕与其名称。
func buildScreens(t *testing.T, sess *Session) []struct {
	name string
	scr  screen
} {
	t.Helper()
	rs := &runScreen{}
	rs.Setup(sess, nil)
	rs.step, rs.active, rs.follow = stepRun, true, true
	rs.stats = migrate.Stats{Done: 26, Total: 54, Uploaded: 20, Skipped: 6, Failed: 1}
	rs.items = []migrate.ItemResult{
		{URL: "https://bu.dusays.com/2026/08/29/6a9299c0be2ef.webp", Name: "6a9299c0be2ef.webp",
			Status: migrate.StatusUploaded, NewURL: "https://v2.picui.cn/i/x1.webp"},
		{URL: "https://bu.dusays.com/2026/08/29/6a9294a206293.webp", Name: "6a9294a206293.webp",
			Status: migrate.StatusReused},
		{URL: "https://free.picui.cn/free/20261003/74a6fc3dba4e8f471c5432a6e2e94874.webp",
			Name: "74a6fc3d…e8f471.webp", Stage: migrate.StageDownload,
			Status: migrate.StatusFailed, Err: "HTTP 403（旧站防盗链）"},
		{URL: "https://bu.dusays.com/2026/08/29/6a9294a206294.webp", Name: "6a9294a206294.webp",
			Stage: migrate.StageUpload, Status: migrate.StatusRetry, Err: "站点限流，4 秒后重试（1/2）"},
	}
	sess.Report = &migrate.Report{Files: 3, References: 8, UniqueURLs: 5, Uploaded: 3,
		AlreadyDone: 2, ChangedFiles: 3, Replacements: 8,
		Failed: []migrate.Failure{
			{URL: "https://bu.dusays.com/2026/08/29/6a9299c0be2ef.webp", Stage: "download",
				Err: "HTTP 403（旧站防盗链）"},
			{URL: "https://free.picui.cn/free/20261003/74a6fc3dba4e8f471c5432a6e2e94874.webp",
				Stage: "upload", Err: "操作频繁，请稍后再试"},
		}}
	return []struct {
		name string
		scr  screen
	}{
		{"start", newScreen(stepStart)},
		{"source", newScreen(stepSource)},
		{"select", newScreen(stepSelect)},
		{"target", newScreen(stepTarget)},
		{"preflight", newScreen(stepPreflight)},
		{"run", rs},
		{"report", newScreen(stepReport)},
	}
}

// TestScreenInvariants 断言每个屏幕在任意尺寸下都恰好占满、且不越界（修 B5）。
func TestScreenInvariants(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		setASCIIForTest(ascii)
		sess := sampleSession(t)
		m := &Model{keys: km, spin: spinnerFor(), sess: sess, version: "0.1.0"}
		sess.SpinFrame = "⠋"
		for _, s := range buildScreens(t, sess) {
			for _, size := range screenSizes {
				m.w, m.h = size[0], size[1]
				m.cur = s.scr
				m.gen++
				_ = s.scr.Init(sess, m.env())
				out := m.View()
				lines := strings.Split(out, "\n")
				if size[1] >= minHeight && len(lines) != size[1] {
					t.Errorf("%s @%d×%d: 行数 %d，应为 %d", s.name, size[0], size[1], len(lines), size[1])
				}
				for i, l := range lines {
					if w := Width(l); w > size[0] {
						t.Errorf("%s(ascii=%v) @%d×%d 第 %d 行宽 %d 超出: %q",
							s.name, ascii, size[0], size[1], i+1, w, stripANSI(l))
					}
					if strings.ContainsRune(l, '\t') {
						t.Errorf("%s @%d×%d 第 %d 行含制表符", s.name, size[0], size[1], i+1)
					}
				}
			}
		}
	}
	setASCIIForTest(asciiMode)
}

// TestGoldenScreens 逐屏黄金快照（-update 刷新）。颜色在非 TTY 下自动降级，
// 因此快照是纯文本，便于 code review 时直接看出布局变化。
func TestGoldenScreens(t *testing.T) {
	setASCIIForTest(false)
	defer setASCIIForTest(asciiMode)
	update := os.Getenv("UPDATE_GOLDEN") != ""
	sess := sampleSession(t)
	m := &Model{keys: km, spin: spinnerFor(), sess: sess, version: "0.1.0", w: 100, h: 30}
	sess.SpinFrame = "⠋"
	for _, s := range buildScreens(t, sess) {
		m.cur = s.scr
		m.gen++
		_ = s.scr.Init(sess, m.env())
		got := m.View()
		path := filepath.Join("testdata", s.name+".golden")
		if update {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: 缺少快照（用 UPDATE_GOLDEN=1 go test ./internal/tui 生成）: %v", s.name, err)
		}
		if got != string(want) {
			t.Errorf("%s: 与快照不一致\n--- 实际 ---\n%s\n--- 期望 ---\n%s", s.name, got, want)
		}
	}
}

// TestHelpOverlay 断言帮助覆盖层在任何屏、任何尺寸下都占满内容区。
func TestHelpOverlay(t *testing.T) {
	for _, step := range []int{stepStart, stepSource, stepSelect, stepTarget, stepPreflight, stepRun, stepReport} {
		for _, size := range [][2]int{{60, 20}, {100, 30}} {
			lines := strings.Split(helpOverlay(step, contentWidth(size[0]), size[1]-4), "\n")
			if len(lines) != size[1]-4 {
				t.Errorf("帮助(step=%d) @%d: 行数 %d，应为 %d", step, size[0], len(lines), size[1]-4)
			}
			for _, l := range lines {
				if Width(l) > contentWidth(size[0]) {
					t.Errorf("帮助(step=%d) @%d: 超宽 %q", step, size[0], stripANSI(l))
				}
			}
		}
	}
}
