// Package lsky 实现兰空图床（Lsky Pro）上传 adapter。
//
// 接口事实核对自官方接口文档（lsky-pro.apifox.cn）与开源版 2.1 源码：
//   - 换 token：POST /api/v1/tokens，body {email,password}，返回 data.token，限流 3 次/分钟
//   - 上传：POST /api/v1/upload，multipart 字段 file，直链取 data.links.url
//   - 校验：GET /api/v1/profile
//   - 开源 V2.x 后台无 token 生成入口，只能通过接口换取；Pro+（付费版）保留
//     /api/v1/* 兼容接口，本 adapter 暂走 v1 兼容层。
package lsky

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Config 是兰空 adapter 的配置。
type Config struct {
	BaseURL    string        // 站点地址，如 https://img.example.com
	Token      string        // 个人 token；为空且配置了 Email/Password 时自动换取
	Email      string        // 仅用于换取 token，不落盘
	Password   string        // 同上
	StrategyID int           // 可选：存储策略（本地 / S3 / OSS / COS…）
	AlbumID    int           // 可选：相册
	APIVersion string        // auto | v1（auto 与 v1 均走 /api/v1 兼容层）
	Timeout    time.Duration // HTTP 超时，默认 30s
}

// Adapter 实现 upload.Uploader。
type Adapter struct {
	cfg Config
	hc  *http.Client
}

// APIError 是兰空返回的 HTTP/业务错误。
// 实现 upload.RateLimiter，供调度器识别频控并退避重试。
type APIError struct {
	StatusCode  int
	Message     string
	rateLimited bool
}

func (e *APIError) Error() string {
	return fmt.Sprintf("兰空返回错误（HTTP %d）: %s", e.StatusCode, e.Message)
}

// RateLimited 实现 upload.RateLimiter。
func (e *APIError) RateLimited() bool { return e.rateLimited }

// isRateLimitMessage 识别兰空按用户组频控时的提示文案。
// 不含泛化的"稍后再试"——"服务异常，请稍后再试"是服务端异常的兜底文案，重试无意义。
func isRateLimitMessage(msg string) bool {
	return strings.Contains(msg, "频繁")
}

// New 创建兰空 adapter。
func New(cfg Config) (*Adapter, error) {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" || !strings.Contains(cfg.BaseURL, "://") {
		return nil, errors.New("兰空站点地址必须是带协议的完整 URL，如 https://img.example.com")
	}
	switch cfg.APIVersion {
	case "", "auto", "v1":
	default:
		return nil, fmt.Errorf("暂不支持 api_version=%q（Pro+ 新接口适配中）", cfg.APIVersion)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Adapter{cfg: cfg, hc: &http.Client{Timeout: cfg.Timeout}}, nil
}

// Name 实现 upload.Uploader。
func (a *Adapter) Name() string { return "lsky" }

// apiResponse 覆盖兰空各接口的共同响应结构：
// tokens 接口的直链在 data.token，上传接口的直链在 data.links.url。
type apiResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Token string `json:"token"`
		Name  string `json:"name"`
		Email string `json:"email"`
		Links struct {
			URL string `json:"url"`
		} `json:"links"`
		Strategies []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"strategies"`
	} `json:"data"`
}

// Strategy 是站点上的一个存储策略。
type Strategy struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Profile 是账号信息（用于界面上"测试连接"的结果展示）。
type Profile struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (a *Adapter) do(req *http.Request) (*apiResponse, error) {
	req.Header.Set("Accept", "application/json")
	if a.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求兰空失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取兰空响应失败: %w", err)
	}
	var out apiResponse
	unmarshalErr := json.Unmarshal(body, &out)

	newAPIErr := func(msg string) *APIError {
		return &APIError{
			StatusCode:  resp.StatusCode,
			Message:     msg,
			rateLimited: resp.StatusCode == http.StatusTooManyRequests || isRateLimitMessage(msg),
		}
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, newAPIErr("未找到兰空接口，请确认站点地址与兰空版本")
	}
	// 4xx 优先按 APIError 处理：部分部署（如 PicUI）的错误响应 status 是
	// 字符串 "error" 而非布尔值，不能因结构差异丢掉原始 message。
	if resp.StatusCode >= 400 {
		msg := out.Message
		if msg == "" {
			msg = truncate(body)
		}
		return nil, newAPIErr(msg)
	}
	if unmarshalErr != nil {
		return nil, fmt.Errorf("兰空响应不是合法 JSON（HTTP %d）: %s", resp.StatusCode, truncate(body))
	}
	if !out.Status {
		msg := out.Message
		if msg == "" {
			msg = truncate(body)
		}
		return nil, newAPIErr(msg)
	}
	return &out, nil
}

// Validate 校验凭证：token 为空时用 email/password 换取（接口限流 3 次/分钟，不重试），
// 再请求 /api/v1/profile 确认连通。
func (a *Adapter) Validate() error {
	_, err := a.Profile()
	return err
}

// Profile 请求 /api/v1/profile：校验连通性并返回账号信息。
// token 为空时先用 email/password 换取（换取结果只存在内存里）。
func (a *Adapter) Profile() (*Profile, error) {
	if a.cfg.Token == "" {
		if a.cfg.Email == "" || a.cfg.Password == "" {
			return nil, errors.New("缺少兰空 token；请在个人页生成后填入，或提供 email/password 自动换取")
		}
		if err := a.exchangeToken(); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		a.cfg.BaseURL+"/api/v1/profile", nil)
	if err != nil {
		return nil, err
	}
	out, err := a.do(req)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%s 不是兰空图床站点，或其版本不受支持: %w", a.cfg.BaseURL, err)
		}
		return nil, err
	}
	return &Profile{Name: out.Data.Name, Email: out.Data.Email}, nil
}

// Strategies 列出站点可用存储策略（公开接口，无需 token）。
// 界面用它把"手填策略 ID"变成下拉选择。
func (a *Adapter) Strategies() ([]Strategy, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		a.cfg.BaseURL+"/api/v1/strategies", nil)
	if err != nil {
		return nil, err
	}
	out, err := a.do(req)
	if err != nil {
		return nil, err
	}
	list := make([]Strategy, 0, len(out.Data.Strategies))
	for _, s := range out.Data.Strategies {
		list = append(list, Strategy{ID: s.ID, Name: s.Name})
	}
	return list, nil
}

func (a *Adapter) exchangeToken() error {
	payload, _ := json.Marshal(map[string]string{
		"email":    a.cfg.Email,
		"password": a.cfg.Password,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		a.cfg.BaseURL+"/api/v1/tokens", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	out, err := a.do(req)
	if err != nil {
		return fmt.Errorf("换取兰空 token 失败（该接口限流 3 次/分钟）: %w", err)
	}
	if out.Data.Token == "" {
		return errors.New("兰空未返回 token")
	}
	a.cfg.Token = out.Data.Token
	return nil
}

// Upload 实现 upload.Uploader：multipart 字段 file，直链取 data.links.url。
func (a *Adapter) Upload(filename string, data []byte, contentType string) (string, error) {
	if a.cfg.Token == "" {
		return "", errors.New("未配置兰空 token，请先调用 Validate")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	if a.cfg.StrategyID > 0 {
		if err := mw.WriteField("strategy_id", strconv.Itoa(a.cfg.StrategyID)); err != nil {
			return "", err
		}
	}
	if a.cfg.AlbumID > 0 {
		if err := mw.WriteField("album_id", strconv.Itoa(a.cfg.AlbumID)); err != nil {
			return "", err
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		a.cfg.BaseURL+"/api/v1/upload", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	out, err := a.do(req)
	if err != nil {
		// 兰空默认存储策略不可用时上传会在服务端抛异常（统一文案"服务异常，请稍后再试"，
		// 第三方站点常见），此时查一次可用策略，提示用户显式指定。
		if a.cfg.StrategyID == 0 {
			var apiErr *APIError
			if errors.As(err, &apiErr) && strings.Contains(apiErr.Message, "服务异常") {
				if hint := a.strategyHint(); hint != "" {
					return "", fmt.Errorf("上传到兰空失败: %w\n提示：%s", err, hint)
				}
			}
		}
		return "", fmt.Errorf("上传到兰空失败: %w", err)
	}
	if out.Data.Links.URL == "" {
		return "", fmt.Errorf("兰空未返回图片直链（message: %s）", out.Message)
	}
	return out.Data.Links.URL, nil
}

// strategyHint 查询站点可用存储策略，生成"请指定策略 ID"的提示文案。
func (a *Adapter) strategyHint() string {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		a.cfg.BaseURL+"/api/v1/strategies", nil)
	if err != nil {
		return ""
	}
	out, err := a.do(req)
	if err != nil || len(out.Data.Strategies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(out.Data.Strategies))
	for _, s := range out.Data.Strategies {
		parts = append(parts, fmt.Sprintf("%d=%s", s.ID, s.Name))
	}
	return fmt.Sprintf("该站点默认存储策略不可用。可用策略：%s；请指定策略 ID 后重跑（CLI：--lsky-strategy-id，TUI：策略 ID 输入框）",
		strings.Join(parts, "，"))
}

func truncate(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
