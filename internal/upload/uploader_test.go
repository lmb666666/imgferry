package upload

import (
	"errors"
	"fmt"
	"testing"
)

type rateLimitedErr struct{}

func (rateLimitedErr) Error() string     { return "限流" }
func (rateLimitedErr) RateLimited() bool { return true }

// TestIsRateLimited 覆盖直接错误、包装错误与非频控错误。
func TestIsRateLimited(t *testing.T) {
	if IsRateLimited(nil) {
		t.Fatal("nil 不应判定为频控")
	}
	if !IsRateLimited(rateLimitedErr{}) {
		t.Fatal("实现 RateLimiter 的错误应判定为频控")
	}
	wrapped := fmt.Errorf("上传失败: %w", rateLimitedErr{})
	if !IsRateLimited(wrapped) {
		t.Fatal("包装后的频控错误应被识别（退避重试依赖它）")
	}
	if IsRateLimited(errors.New("普通错误")) {
		t.Fatal("普通错误不应判定为频控")
	}
}
