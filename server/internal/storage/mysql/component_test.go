package mysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

// inertConnector 为 Stop 顺序测试提供不建立网络连接且可安全关闭的 sql.DB。
type inertConnector struct{}

// Connect 不应在本测试中执行；若意外执行则返回稳定错误。
func (inertConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("inert connector does not connect")
}

// Driver 返回与 inertConnector 配套的最小 driver。
func (inertConnector) Driver() driver.Driver { return inertDriver{} }

// inertDriver 仅满足 sql.OpenDB 所需的 driver 边界。
type inertDriver struct{}

// Open 不应在本测试中执行；若意外执行则返回稳定错误。
func (inertDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("inert driver does not open connections")
}

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

// TestComponentStopKeepsPoolUntilProbeOwnerStops 防止 Stop 在 probe 退出前撤销 pool。
func TestComponentStopKeepsPoolUntilProbeOwnerStops(t *testing.T) {
	t.Parallel()

	db := sql.OpenDB(inertConnector{})
	stopErr := errors.New("probe owner stop failed")
	owner := &stopInspectionTaskOwner{stopErr: stopErr}
	component := &Component{db: db, started: true, tasks: owner, stopDone: make(chan struct{})}
	owner.onStop = func() {
		if component.DB() == nil {
			t.Error("probe owner 停止前 pool 不得被撤销")
		}
	}

	if err := component.Stop(context.Background()); !errors.Is(err, stopErr) {
		t.Fatalf("Stop() error = %v", err)
	}
	if component.DB() != nil {
		t.Fatal("probe owner 停止后 pool 应被撤销")
	}
	if err := component.Stop(context.Background()); !errors.Is(err, stopErr) {
		t.Fatalf("重复 Stop() error = %v", err)
	}
}

// TestDriverErrorRedactsDefaultTextAndRetainsCause 固定 MySQL driver 错误的安全外层契约。
func TestDriverErrorRedactsDefaultTextAndRetainsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("endpoint=private query=secret")
	failure := wrapDriverError("mysql operation failed", cause)
	if strings.Contains(failure.Error(), "private") || strings.Contains(failure.Error(), "secret") {
		t.Fatalf("driver error 泄露底层文本：%q", failure.Error())
	}
	if !errors.Is(failure, cause) {
		t.Fatal("driver error 应保留受控 cause")
	}
}
