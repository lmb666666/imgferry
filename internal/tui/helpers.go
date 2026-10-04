// 交互层的通用小工具（其余排版工具见 text.go）。
package tui

import (
	"net/url"
	"path"
	"strings"
)

// parseExts 解析"后缀过滤"输入：逗号分隔，允许省略点号，全部非法时返回 nil。
func parseExts(s string) map[string]bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, ".") {
			p = "." + p
		}
		m[p] = true
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// urlHasExt 判断链接路径的扩展名是否在集合中（大小写不敏感）。
func urlHasExt(rawURL string, set map[string]bool) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return set[strings.ToLower(path.Ext(u.Path))]
}
