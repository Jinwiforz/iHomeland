package redis

import (
	"context"
	"errors"
	"strings"
	"testing"

	redisclient "github.com/redis/go-redis/v9"
)

// stopInspectionTaskOwner 在 Stop 回调中检查 component 尚未撤销 probe 依赖。
type stopInspectionTaskOwner struct {
	onStop  func()
	stopErr error
}

// Go 满足 TaskOwner；该单元测试不启动后台任务。
func (*stopInspectionTaskOwner) Go(string, func(context.Context) error) error { return nil }

// Stop 在返回预设结果前同步执行资源所有权断言。
func (owner *stopInspectionTaskOwner) Stop(context.Context, error) error {
	owner.onStop()
	return owner.stopErr
}

// TestComponentStopKeepsClientUntilProbeOwnerStops 防止 Stop 在 probe 退出前撤销 client。
func TestComponentStopKeepsClientUntilProbeOwnerStops(t *testing.T) {
	t.Parallel()

	client := redisclient.NewClient(&redisclient.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	stopErr := errors.New("probe owner stop failed")
	owner := &stopInspectionTaskOwner{stopErr: stopErr}
	component := &Component{client: client, started: true, tasks: owner, stopDone: make(chan struct{})}
	owner.onStop = func() {
		if component.Client() == nil {
			t.Error("probe owner 停止前 client 不得被撤销")
		}
	}

	if err := component.Stop(context.Background()); !errors.Is(err, stopErr) {
		t.Fatalf("Stop() error = %v", err)
	}
	if component.Client() != nil {
		t.Fatal("probe owner 停止后 client 应被撤销")
	}
	if err := component.Stop(context.Background()); !errors.Is(err, stopErr) {
		t.Fatalf("重复 Stop() error = %v", err)
	}
}

// TestDriverErrorRedactsDefaultTextAndRetainsCause 固定 Redis client 错误的安全外层契约。
func TestDriverErrorRedactsDefaultTextAndRetainsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("endpoint=private key=secret")
	failure := wrapDriverError("redis operation failed", cause)
	if strings.Contains(failure.Error(), "private") || strings.Contains(failure.Error(), "secret") {
		t.Fatalf("driver error 泄露底层文本：%q", failure.Error())
	}
	if !errors.Is(failure, cause) {
		t.Fatal("driver error 应保留受控 cause")
	}
}
