package testclient

import (
	"context"
	"errors"
	"strconv"
	"time"
)

const (
	// lifecycleConnectionProbeTimeout 限制 predecessor connection 的失效探测。
	lifecycleConnectionProbeTimeout = 2 * time.Second
	// lifecycleSnapshotRequestID 是公开 TLS-TCP world snapshot request。
	lifecycleSnapshotRequestID = 2000
)

// runAssignmentReplacement 验证 current assignment 被 successor 替换且旧资格不可复活。
func runAssignmentReplacement(ctx context.Context, runtime *ScenarioRuntime) error {
	return runProcessReplacement(ctx, runtime, FaultAssignmentReplacement)
}

// runChildCrashRestart 验证精确 C++ child 异常退出后进程图和 assignment 被替换。
func runChildCrashRestart(ctx context.Context, runtime *ScenarioRuntime) error {
	return runProcessReplacement(ctx, runtime, FaultChildCrashRestart)
}

// runGoRestart 验证精确 Go parent 替换后持久世界保留且旧进程绑定失效。
func runGoRestart(ctx context.Context, runtime *ScenarioRuntime) error {
	return runProcessReplacement(ctx, runtime, FaultGoRestart)
}

// runProcessReplacement 通过公开 bootstrap/TLS-TCP 观察进程 owner 执行的 replacement。
func runProcessReplacement(
	ctx context.Context,
	runtime *ScenarioRuntime,
	faultKind string,
) (resultErr error) {
	if runtime == nil || runtime.Faults == nil || runtime.Lifecycle == nil {
		return errors.New("process lifecycle runtime is incomplete")
	}
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	before, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil || before.Assignment == nil {
		return errors.Join(err, errors.New("process lifecycle predecessor assignment is missing"))
	}
	if err := recordAssignmentProjection(runtime.Lifecycle.recordPredecessor, before); err != nil {
		return err
	}
	active, err := dialOwnWorldTCP(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	if err := scenario.Track(active); err != nil {
		return err
	}
	ticket, admission, endpoint, err := issueUnusedOwnCredentials(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	defer ticket.Clear()
	defer admission.Clear()
	if err := runtime.Faults.Execute(ctx, faultKind); err != nil {
		return errors.New("process lifecycle owner rejected replacement")
	}
	if err := requireConnectionRejected(ctx, active); err != nil {
		return err
	}
	after, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil || after.Assignment == nil {
		return errors.Join(err, errors.New("process lifecycle successor assignment is missing"))
	}
	if before.World.PersonalWorldID != after.World.PersonalWorldID ||
		before.Assignment.WorldInstanceID == after.Assignment.WorldInstanceID ||
		after.Assignment.Generation <= before.Assignment.Generation {
		return errors.New("process lifecycle successor assignment did not advance")
	}
	if err := requireRejectedTCP(
		ctx,
		runtime,
		endpoint,
		ticket,
		admission,
		"OWN_WORLD",
	); err != nil {
		return err
	}
	return recordAssignmentProjection(runtime.Lifecycle.recordSuccessor, after)
}

// runShutdownDrainDeadline 验证 supervised failure 在 owner deadline 内终止公开输入和旧连接。
func runShutdownDrainDeadline(
	ctx context.Context,
	runtime *ScenarioRuntime,
) (resultErr error) {
	if runtime == nil || runtime.Faults == nil || runtime.Lifecycle == nil {
		return errors.New("shutdown lifecycle runtime is incomplete")
	}
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	before, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil || before.Assignment == nil {
		return errors.Join(err, errors.New("shutdown predecessor assignment is missing"))
	}
	if err := recordAssignmentProjection(runtime.Lifecycle.recordPredecessor, before); err != nil {
		return err
	}
	active, err := dialOwnWorldTCP(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	if err := scenario.Track(active); err != nil {
		return err
	}
	if err := runtime.Faults.Execute(ctx, FaultShutdownDrainDeadline); err != nil {
		return errors.New("shutdown lifecycle owner rejected operation")
	}
	if err := requireConnectionRejected(ctx, active); err != nil {
		return err
	}
	probeContext, cancelProbe := context.WithTimeout(ctx, lifecycleConnectionProbeTimeout)
	defer cancelProbe()
	if _, err := runtime.HTTP.WorldBootstrap(
		probeContext,
		account.actor.AccessToken,
	); err == nil {
		return errors.New("shutdown lifecycle left public input available")
	}
	return runtime.Lifecycle.recordTermination()
}

// recordAssignmentProjection 编码持久世界与不可复活 assignment stamp。
func recordAssignmentProjection(
	record func(...string) error,
	response WorldBootstrapResponse,
) error {
	if record == nil || response.Assignment == nil {
		return errors.New("assignment lifecycle projection is incomplete")
	}
	return record(
		"assignment",
		response.World.PersonalWorldID,
		response.Assignment.WorldInstanceID,
		strconv.FormatUint(response.Assignment.Generation, 10),
	)
}

// requireConnectionRejected 允许 EOF、close 或 timeout 前的 transport failure，但不允许业务响应。
func requireConnectionRejected(ctx context.Context, connection *TCPClient) error {
	if connection == nil {
		return errors.New("process lifecycle predecessor connection is missing")
	}
	probeContext, cancelProbe := context.WithTimeout(ctx, lifecycleConnectionProbeTimeout)
	defer cancelProbe()
	if _, err := connection.Request(
		probeContext,
		lifecycleSnapshotRequestID,
		newWorldSnapshotRequest(),
	); err == nil {
		return errors.New("process lifecycle predecessor connection remained usable")
	}
	return nil
}
