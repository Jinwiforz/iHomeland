package app

import (
	"errors"
	"strings"
	"testing"
)

// TestResultExitCodes 固定所有公开进程结果，防止新增分类意外落入错误退出码。
func TestResultExitCodes(t *testing.T) {
	tests := []struct {
		// kind 是 Composition Root 返回的稳定结果分类。
		kind ResultKind
		// expected 是 cmd/server 对操作系统公开的退出码。
		expected int
	}{
		{kind: ResultClean, expected: 0},
		{kind: ResultConfigError, expected: 2},
		{kind: ResultStartupError, expected: 3},
		{kind: ResultRuntimeError, expected: 4},
		{kind: ResultShutdownError, expected: 5},
	}
	for _, test := range tests {
		if actual := (Result{Kind: test.kind}).ExitCode(); actual != test.expected {
			t.Fatalf("result %s returned exit code %d, expected %d", test.kind, actual, test.expected)
		}
	}
}

// TestResultMessageSeparatesCleanAndFailure 验证最终诊断只为带 cause 的失败结果生成。
func TestResultMessageSeparatesCleanAndFailure(t *testing.T) {
	if message := ResultMessage(Result{Kind: ResultClean}); message != "" {
		t.Fatalf("clean result produced diagnostic %q", message)
	}
	message := ResultMessage(Result{Kind: ResultRuntimeError, Err: errors.New("task failed")})
	if !strings.Contains(message, string(ResultRuntimeError)) || !strings.Contains(message, "task failed") {
		t.Fatalf("failure diagnostic lost classification or cause: %q", message)
	}
}
