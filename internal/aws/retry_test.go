package aws

import (
	"errors"
	"testing"
	"time"

	"github.com/aws/smithy-go"
)

// SafeRetry 必须用 %w 保留错误链，下游 errors.Is/As 才能判断错误码
func TestSafeRetryKeepsErrorChain(t *testing.T) {
	inner := &smithy.GenericAPIError{Code: "NotFoundException", Message: "gone"}
	err := SafeRetry("测试动作", 1, 0, func() error { return inner })

	if err == nil {
		t.Fatal("应返回错误")
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误链被丢弃，errors.As 无法命中 smithy.APIError: %v", err)
	}
	if apiErr.ErrorCode() != "NotFoundException" {
		t.Fatalf("错误码不符: %s", apiErr.ErrorCode())
	}
	if !isStaticIPNotFound(err) {
		t.Fatal("经 SafeRetry 包装后仍应识别出 NotFound")
	}
}

// 成功时不重试不报错
func TestSafeRetrySuccess(t *testing.T) {
	if err := SafeRetry("测试动作", 3, time.Millisecond, func() error { return nil }); err != nil {
		t.Fatalf("成功不应报错: %v", err)
	}
}
