// 会话状态：全流程共享的唯一可变状态（来源、扫描结果、勾选、目标配置、计划、运行态），
// 以及给屏幕用的异步环境（带世代号的消息发送器）。
package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lmb666666/imgferry/internal/config"
	"github.com/lmb666666/imgferry/internal/migrate"
	"github.com/lmb666666/imgferry/internal/scan"
	"github.com/lmb666666/imgferry/internal/upload/lsky"
)

// genMsg 给异步消息打上世代号：用户取消或切屏后，旧任务的消息会被根模型丢弃，
// 避免过期结果污染新状态（取代旧的 channel + 多路 reader）。
type genMsg struct {
	gen   uint64
	inner tea.Msg
}

// env 是屏幕发起异步任务的环境：emit 在读时取 prog，避免构造顺序问题。
type env struct {
	gen  uint64
	emit func(tea.Msg)
}

func (e *env) send(msg tea.Msg) {
	if e != nil && e.emit != nil {
		e.emit(msg)
	}
}

// TargetConfig 是目标图床与迁移选项（目标屏的绑定值）。
type TargetConfig struct {
	Type          string
	BaseURL       string
	Token         string
	Email         string
	Password      string
	CredMode      string // token | password
	StrategyID    string // 空 = 站点默认策略
	AlbumID       int
	UploadRetries int // 站点限流时的退避重试次数（0 = 默认 2，负值 = 关闭）
	Concurrency   int
	Interval      string // 如 "1s"
	Backup        bool
	FreshMap      bool
	MapPath       string
	HeaderReferer string
	HeaderCookie  string
	HeaderUA      string
	Advanced      bool
	// 上一次测试连接的结果（仅展示）
	TestNote   string
	TestLevel  string
	Strategies []lsky.Strategy
}

// IntervalDuration 解析间隔字符串，非法或为空时返回 0。
func (t *TargetConfig) IntervalDuration() time.Duration {
	s := strings.TrimSpace(t.Interval)
	if s == "" {
		return 0
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second
	}
	return 0
}

// DownloadHeaders 组装下载旧图所需的额外请求头。
func (t *TargetConfig) DownloadHeaders() map[string]string {
	h := map[string]string{}
	if strings.TrimSpace(t.HeaderReferer) != "" {
		h["Referer"] = strings.TrimSpace(t.HeaderReferer)
	}
	if strings.TrimSpace(t.HeaderCookie) != "" {
		h["Cookie"] = strings.TrimSpace(t.HeaderCookie)
	}
	if strings.TrimSpace(t.HeaderUA) != "" {
		h["User-Agent"] = strings.TrimSpace(t.HeaderUA)
	}
	if len(h) == 0 {
		return nil
	}
	return h
}

// StrategyIDValue 返回存储策略 ID（0 表示站点默认）。
func (t *TargetConfig) StrategyIDValue() int {
	n, err := strconv.Atoi(strings.TrimSpace(t.StrategyID))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (t *TargetConfig) MapPathValue() string {
	if strings.TrimSpace(t.MapPath) == "" {
		return config.DefaultMapPath
	}
	return strings.TrimSpace(t.MapPath)
}

// NewUploader 依据当前配置构造兰空 adapter。
// Retries 返回退避重试次数（字段值即次数，0 表示关闭；负值按 0 处理）。
func (t *TargetConfig) Retries() int {
	if t.UploadRetries < 0 {
		return 0
	}
	return t.UploadRetries
}

func (t *TargetConfig) NewUploader() (*lsky.Adapter, error) {
	cfg := lsky.Config{
		BaseURL:    strings.TrimSpace(t.BaseURL),
		StrategyID: t.StrategyIDValue(),
		AlbumID:    t.AlbumID,
	}
	if t.CredMode == "password" {
		cfg.Email = strings.TrimSpace(t.Email)
		cfg.Password = t.Password
	} else {
		cfg.Token = strings.TrimSpace(t.Token)
	}
	return lsky.New(cfg)
}

// Session 是全流程共享状态。
type Session struct {
	cfg   *config.Config
	state *config.State

	// ① 来源
	SourcePath  string
	AllFiles    bool
	ExtFilter   string
	PathNote    string
	PathNoteLvl string

	// ② 选择
	Scan *scan.Result
	Exts map[string]bool
	Tree *checkTree

	// ③ 目标
	Target TargetConfig

	// ④⑤⑥ 计划与结果
	Plan   *migrate.Plan
	Report *migrate.Report

	// 流程标记
	AutoScan  bool     // 进入来源屏后自动开始扫描（"继续上次"）
	RetryOnly []string // 只迁移/重试这些 URL
	ReturnTo  int      // 从预检跳去改来源/目标后，esc 回到这里（-1 表示无）

	// 运行态
	SpinFrame string // 当前 spinner 帧（根模型统一驱动，屏幕只读）
	Cancel    context.CancelFunc
	Step      int // 当前步骤
	Reached   int // 已到达的最远步骤
	Canceled  bool
}

// newSession 从配置构造会话（配置值全部带出，密码只在会话内）。
func newSession(cfg *config.Config) *Session {
	s := &Session{cfg: cfg, state: config.LoadState()}
	if s.state == nil {
		s.state = &config.State{}
	}
	s.SourcePath = cfg.LastSourcePath
	s.AllFiles = cfg.Target.AllFiles
	t := TargetConfig{
		Type:          "lsky",
		BaseURL:       cfg.Target.BaseURL,
		Token:         cfg.Target.Token,
		Email:         cfg.Target.Email,
		CredMode:      "token",
		AlbumID:       cfg.Target.AlbumID,
		UploadRetries: cfg.Target.UploadRetries,
		Concurrency:   cfg.Target.Concurrency,
		Interval:      cfg.Target.Interval,
		Backup:        cfg.Target.Backup,
		FreshMap:      cfg.Target.FreshMap,
		MapPath:       cfg.Target.MapPath,
	}
	if t.Interval == "" {
		t.Interval = config.DefaultInterval
	}
	if t.MapPath == "" {
		t.MapPath = config.DefaultMapPath
	}
	if t.Concurrency <= 0 {
		t.Concurrency = config.DefaultConcurrency
	}
	if cfg.Target.StrategyID > 0 {
		t.StrategyID = strconv.Itoa(cfg.Target.StrategyID)
	}
	if h := cfg.Target.DownloadHeaders; h != nil {
		t.HeaderReferer = h["Referer"]
		t.HeaderCookie = h["Cookie"]
		t.HeaderUA = h["User-Agent"]
	}
	if t.Token == "" && t.Email != "" {
		t.CredMode = "password"
	}
	s.Target = t
	s.Step, s.Reached, s.ReturnTo = stepStart, stepStart, -1
	return s
}

// SaveConfig 把会话里的目标配置与来源路径写回配置文件。
func (s *Session) SaveConfig() error {
	c := config.Default()
	c.Target.BaseURL = strings.TrimSpace(s.Target.BaseURL)
	c.Target.Token = strings.TrimSpace(s.Target.Token)
	c.Target.Email = strings.TrimSpace(s.Target.Email)
	c.Target.StrategyID = s.Target.StrategyIDValue()
	c.Target.AlbumID = s.Target.AlbumID
	c.Target.UploadRetries = s.Target.Retries()
	c.Target.Concurrency = maxInt(s.Target.Concurrency, 1)
	c.Target.Interval = s.Target.Interval
	c.Target.Backup = s.Target.Backup
	c.Target.FreshMap = s.Target.FreshMap
	c.Target.MapPath = s.Target.MapPathValue()
	c.Target.AllFiles = s.AllFiles
	if h := s.Target.DownloadHeaders(); h != nil {
		c.Target.DownloadHeaders = h
	}
	c.LastSourcePath = strings.TrimSpace(s.SourcePath)
	c.UI.GroupBy = s.groupByKey()
	if s.Tree != nil {
		c.UI.GroupBy = s.Tree.dim.key()
	}
	return config.Save(c)
}

func (s *Session) groupByKey() string {
	if s.Tree != nil {
		return s.Tree.dim.key()
	}
	return "domain"
}

// RememberSource 记录本次来源路径，供首页"继续上次"。
func (s *Session) RememberSource() {
	if s.state == nil || s.state.LastRun == nil {
		return
	}
}

// RecordRun 写入运行记录（迁移结束/取消后调用）。
func (s *Session) RecordRun(rep *migrate.Report, canceled bool) {
	if s.state == nil {
		s.state = &config.State{}
	}
	rec := &config.RunRecord{
		At:       time.Now(),
		Source:   strings.TrimSpace(s.SourcePath),
		Target:   strings.TrimSpace(s.Target.BaseURL),
		Canceled: canceled,
	}
	if rep != nil {
		rec.Uploaded = rep.Uploaded
		rec.Reused = rep.AlreadyDone
		for _, f := range rep.Failed {
			rec.Failed = append(rec.Failed, config.FailedItem{URL: f.URL, Stage: f.Stage, Err: f.Err})
		}
	}
	s.state.LastRun = rec
	s.state.RememberSource(rec.Source)
	_ = config.SaveState(s.state)
}

// State 返回运行记录（首页用）。
func (s *Session) State() *config.State { return s.state }

// LastFailedURLs 返回上次失败的 URL 列表。
func (s *Session) LastFailedURLs() []string {
	if s.state == nil || s.state.LastRun == nil {
		return nil
	}
	var out []string
	for _, f := range s.state.LastRun.Failed {
		out = append(out, f.URL)
	}
	return out
}
