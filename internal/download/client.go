// Package download 按 URL 下载图片，支持自定义请求头（防盗链）与重试。
package download

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// DefaultMaxSize 是单张图片的默认大小上限。
const DefaultMaxSize = 50 << 20

// Options 是下载器配置。
type Options struct {
	Headers    map[string]string // 额外请求头（Referer/UA/Cookie 等，应对防盗链），覆盖默认 UA
	Timeout    time.Duration     // 单次请求超时，默认 30s
	MaxRetries int               // 对 429/5xx/网络错误的重试次数，默认 0
	MaxSize    int64             // 响应大小上限，默认 50MB
}

// Client 是可复用的下载器。
type Client struct {
	hc  *http.Client
	opt Options
}

// New 创建下载器。
func New(opt Options) *Client {
	if opt.Timeout <= 0 {
		opt.Timeout = 30 * time.Second
	}
	if opt.MaxRetries < 0 {
		opt.MaxRetries = 0
	}
	if opt.MaxSize <= 0 {
		opt.MaxSize = DefaultMaxSize
	}
	return &Client{hc: &http.Client{Timeout: opt.Timeout}, opt: opt}
}

// File 是下载得到的一份图片。
type File struct {
	Name        string // 带扩展名的文件名
	Data        []byte
	ContentType string
}

// Fetch 下载一张图片；对 429/5xx/网络错误做指数退避重试。
func (c *Client) Fetch(ctx context.Context, rawURL string) (*File, error) {
	var lastErr error
	for attempt := 0; attempt <= c.opt.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(500*(1<<attempt)) * time.Millisecond):
			}
		}
		f, retryable, err := c.fetchOnce(ctx, rawURL)
		if err == nil {
			return f, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) fetchOnce(ctx context.Context, rawURL string) (f *File, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "imgferry/0.1 (+https://github.com/lmb666666/imgferry)")
	for k, v := range c.opt.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, true, fmt.Errorf("下载 %s 被限流（HTTP 429）", rawURL)
	case resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("下载 %s 服务端错误（HTTP %d）", rawURL, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("下载 %s 失败（HTTP %d）", rawURL, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, c.opt.MaxSize+1))
	if err != nil {
		return nil, true, fmt.Errorf("读取 %s 失败: %w", rawURL, err)
	}
	if int64(len(data)) > c.opt.MaxSize {
		return nil, false, fmt.Errorf("%s 超过大小上限 %d 字节", rawURL, c.opt.MaxSize)
	}

	ctype := resp.Header.Get("Content-Type")
	name, err := deriveName(rawURL, ctype, data)
	if err != nil {
		return nil, false, err
	}
	return &File{Name: name, Data: data, ContentType: ctype}, false, nil
}

// deriveName 从 URL 路径推断文件名；无扩展名时按 Content-Type 或内容嗅探补齐。
func deriveName(rawURL, ctype string, data []byte) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	name := path.Base(u.Path)
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "/" || name == "." {
		name = "image"
	}
	name = strings.ReplaceAll(name, "/", "_")
	if path.Ext(name) != "" {
		return name, nil
	}
	ext := extFromContentType(ctype)
	if ext == "" && len(data) > 0 {
		sniff := data
		if len(sniff) > 512 {
			sniff = sniff[:512]
		}
		ext = extFromContentType(http.DetectContentType(sniff))
	}
	if ext == "" {
		return "", fmt.Errorf("无法确定 %s 的文件扩展名", rawURL)
	}
	return name + ext, nil
}

func extFromContentType(ctype string) string {
	mt, _, err := mime.ParseMediaType(ctype)
	if err != nil {
		mt = strings.TrimSpace(strings.Split(ctype, ";")[0])
	}
	switch strings.ToLower(mt) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "image/bmp":
		return ".bmp"
	case "image/avif":
		return ".avif"
	}
	return ""
}
