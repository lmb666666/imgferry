// Package config 负责 imgferry 的本地配置与运行记录持久化。
// 配置保存在用户配置目录下的 config.toml，目标图床信息与上次扫描路径
// 会在下次启动时自动带出，实现"低频配置一次、高频使用两三键"的体验。
// 注意：Token 以明文保存（与 gh 等工具惯例一致），文件权限 0600；
// 密码只用于会话内换取 token，不落盘。运行记录 state.json 不含任何密钥。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Target 是目标图床配置。
type Target struct {
	Type            string            `toml:"type"` // 目前仅 "lsky"
	BaseURL         string            `toml:"base_url"`
	Token           string            `toml:"token"`
	Email           string            `toml:"email"`
	Password        string            `toml:"-"` // 仅会话内使用（换取 token），绝不落盘
	StrategyID      int               `toml:"strategy_id"`
	AlbumID         int               `toml:"album_id"`
	UploadRetries   int               `toml:"upload_retries"`
	Concurrency     int               `toml:"concurrency"`
	Interval        string            `toml:"interval"` // 相邻上传最小间隔，如 "1s"
	Backup          bool              `toml:"backup"`
	FreshMap        bool              `toml:"fresh_map"`
	MapPath         string            `toml:"map_path"`
	AllFiles        bool              `toml:"all_files"`
	DownloadHeaders map[string]string `toml:"download_headers"`
}

// UI 是界面偏好。
type UI struct {
	GroupBy string `toml:"group_by"` // 选择屏分组维度：domain | dir | file | none
	ASCII   bool   `toml:"ascii"`    // 强制 ASCII 图标（无 UTF-8 字体时）
}

// Config 是完整的本地配置。
type Config struct {
	Target         Target `toml:"target"`
	LastSourcePath string `toml:"last_source_path"`
	UI             UI     `toml:"ui"`
}

// 默认值。
const (
	DefaultMapPath     = "migrate-map.json"
	DefaultConcurrency = 2
	DefaultInterval    = "1s"
)

// Default 返回默认配置。
func Default() *Config {
	return &Config{
		Target: Target{
			Type:          "lsky",
			Concurrency:   DefaultConcurrency,
			UploadRetries: 2,
			Interval:      DefaultInterval,
			Backup:        true,
			MapPath:       DefaultMapPath,
		},
		UI: UI{GroupBy: "domain"},
	}
}

// configDirOverride 供测试重定向配置目录。
var configDirOverride string

// Dir 返回配置目录。
func Dir() string {
	if configDirOverride != "" {
		return configDirOverride
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "imgferry")
	}
	return "."
}

// Path 返回配置文件路径。
func Path() string { return filepath.Join(Dir(), "config.toml") }

// Load 读取配置；文件不存在时返回默认配置。
func Load() (*Config, error) {
	c := Default()
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	if err := toml.Unmarshal(data, c); err != nil {
		return nil, err
	}
	c.normalize()
	return c, nil
}

func (c *Config) normalize() {
	if c.Target.Type == "" {
		c.Target.Type = "lsky"
	}
	if c.Target.Concurrency <= 0 {
		c.Target.Concurrency = DefaultConcurrency
	}
	if c.Target.MapPath == "" {
		c.Target.MapPath = DefaultMapPath
	}
	if c.UI.GroupBy == "" {
		c.UI.GroupBy = "domain"
	}
}

// Save 原子写入配置（0600 权限，Token 为敏感信息）。
func Save(c *Config) error {
	c.normalize()
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

// HasTarget 判断是否已有可沿用的目标配置。
func (c *Config) HasTarget() bool { return c.Target.BaseURL != "" }

// RunRecord 是一次迁移的结果摘要（首页"继续上次 / 重试失败项"用）。
type RunRecord struct {
	At       time.Time    `json:"at"`
	Source   string       `json:"source"`
	Target   string       `json:"target"`
	Uploaded int          `json:"uploaded"`
	Reused   int          `json:"reused"`
	Canceled bool         `json:"canceled,omitempty"`
	Failed   []FailedItem `json:"failed,omitempty"`
}

// FailedItem 是失败项（URL + 阶段 + 原因），供"只重试失败项"。
type FailedItem struct {
	URL   string `json:"url"`
	Stage string `json:"stage"`
	Err   string `json:"err"`
}

// State 是跨会话的运行记录，不含密钥。
type State struct {
	LastRun       *RunRecord `json:"last_run,omitempty"`
	RecentSources []string   `json:"recent_sources,omitempty"`
}

// StatePath 返回运行记录路径。
func StatePath() string { return filepath.Join(Dir(), "state.json") }

// LoadState 读取运行记录；文件不存在或损坏时返回空记录（不阻塞启动）。
func LoadState() *State {
	st := &State{}
	data, err := os.ReadFile(StatePath())
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, st); err != nil {
		return &State{}
	}
	return st
}

// SaveState 写入运行记录（0600，不含密钥）。
func SaveState(st *State) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := StatePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, StatePath())
}

// rememberSource 把来源路径记入最近使用（去重、最多 5 条）。
func (s *State) RememberSource(path string) {
	if path == "" {
		return
	}
	out := []string{path}
	for _, p := range s.RecentSources {
		if p != path && len(out) < 5 {
			out = append(out, p)
		}
	}
	s.RecentSources = out
}
