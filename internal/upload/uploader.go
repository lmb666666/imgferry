// Package upload 定义图床上传 adapter 的统一接口。
// adapter 只做一件事：输入文件字节，输出可公开直链访问的 URL。
package upload

import "errors"

// Uploader 是所有图床 adapter 的统一接口。
type Uploader interface {
	// Name 返回 adapter 标识（如 "lsky"）。
	Name() string
	// Validate 校验配置与凭证的连通性。
	Validate() error
	// Upload 上传一个文件，返回公开直链。
	Upload(filename string, data []byte, contentType string) (publicURL string, err error)
}

// RateLimiter 由 adapter 的错误类型实现，表示目标图床要求降低上传频率。
type RateLimiter interface {
	RateLimited() bool
}

// IsRateLimited 判断错误链中是否存在频控错误（供调度器决定是否退避重试）。
func IsRateLimited(err error) bool {
	for err != nil {
		if rl, ok := err.(RateLimiter); ok && rl.RateLimited() {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
