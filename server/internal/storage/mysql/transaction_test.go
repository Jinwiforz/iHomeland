package mysql

import (
	"errors"
	"strings"
	"testing"
)

// TestTransactionErrorRedactsDriverText 固定默认错误文本脱敏且内部 error chain 可追踪。
func TestTransactionErrorRedactsDriverText(t *testing.T) {
	t.Parallel()

	cause := errors.New("password=not-for-logs query=private")
	failure := &TransactionError{Outcome: TransactionCommitUnknown, Err: cause}
	if strings.Contains(failure.Error(), "not-for-logs") || strings.Contains(failure.Error(), "private") {
		t.Fatalf("TransactionError 泄露底层文本：%q", failure.Error())
	}
	if !errors.Is(failure, cause) {
		t.Fatal("TransactionError 应保留内部 error chain")
	}
}
