package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	simulationprocess "github.com/jinwiforz/ihomeland/server/internal/simulationcontrol/process"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
	storagesimulationresult "github.com/jinwiforz/ihomeland/server/internal/storage/simulationresult"
)

// simulationNodeObserver 只接收固定 operation/outcome 与数量，不接收 identity、nonce、路径或 payload。
type simulationNodeObserver interface {
	RecordStorageOperation(string, string, string)
	SetSimulationNodeHealth(string)
	SetSimulationInstances(int)
	SetSimulationCapacity(string, int)
	ObserveSimulationControl(string, string, time.Duration)
	SetSimulationControlQueue(int)
	ObserveSimulationDrain(string)
	ObserveSimulationResult(string)
	ObserveSimulationProcessExit(string)
	ObserveSimulationShutdown(string)
}

// simulationNodeComponent 拥有精确 C++ child、pipes、registry、result inbox 与监督任务。
type simulationNodeComponent struct {
	// settings 是已严格验证的无 secret control policy。
	settings config.SimulationControl
	// mysql 提供已迁移且先于本组件启动的 DB。
	mysql *storagemysql.Component
	// tasks 监督 unexpected process exit 与 health probe。
	tasks *TaskOwner
	// clock 为 result receipt 提供 UTC 时间。
	clock Clock
	// metrics 同时满足 storage observer。
	metrics simulationNodeObserver
	// logger 只记录低基数 control outcome。
	logger *slog.Logger
	// controllerConfig 是启动前已解析的 exact registration。
	controllerConfig simulationcontrol.ControllerConfig
	// processConfig 是启动前已解析的 artifact binding。
	processConfig simulationprocess.Config
	// inbox 有界保存等待持久裁决的 proposal。
	inbox *simulationcontrol.ProposalInbox
	// owner 持有 OS process 与 session。
	owner *simulationprocess.Owner
	// controller 持有 node/instance registry。
	controller *simulationcontrol.Controller
	// resultMutex 保护 coordinator bind 与同步消费。
	resultMutex sync.Mutex
	// results 在 public placement store 构造后绑定。
	results *simulationcontrol.ResultCoordinator
	// targets 只为 current active/lease-valid/healthy/ready binding 返回内部 target。
	targets *simulationcontrol.TargetResolver
}

// newSimulationNodeComponent 解析配置并生成不可复活 node identities，不启动进程。
func newSimulationNodeComponent(settings config.SimulationControl, mysql *storagemysql.Component, tasks *TaskOwner, clock Clock, ids IDGenerator, metrics simulationNodeObserver, logger *slog.Logger) (*simulationNodeComponent, error) {
	if !settings.Enabled || mysql == nil || tasks == nil || clock == nil ||
		ids == nil || metrics == nil || logger == nil {
		return nil, errors.New("simulation node component dependencies are invalid")
	}
	nodeMaterial, err := ids.NewID()
	if err != nil {
		return nil, err
	}
	runtimeMaterial, err := ids.NewID()
	if err != nil {
		return nil, err
	}
	nodeID, err := simulationcontrol.NewSimulationNodeID("snode_" + nodeMaterial)
	if err != nil {
		return nil, err
	}
	runtimeNodeID, err := placement.NewRuntimeNodeID("rnode_" + runtimeMaterial)
	if err != nil {
		return nil, err
	}
	digest := func(value string) (simulationcontrol.Digest, error) {
		return simulationcontrol.NewDigest(value)
	}
	buildIdentity, err := digest(settings.BuildIdentity)
	if err != nil {
		return nil, err
	}
	modelManifest, err := digest(settings.ModelManifest)
	if err != nil {
		return nil, err
	}
	profileManifest, err := digest(settings.ProfileManifest)
	if err != nil {
		return nil, err
	}
	configIdentity, err := digest(settings.ConfigIdentity)
	if err != nil {
		return nil, err
	}
	navigationIdentity, err := digest(settings.NavigationIdentity)
	if err != nil {
		return nil, err
	}
	physicsIdentity, err := digest(settings.PhysicsIdentity)
	if err != nil {
		return nil, err
	}
	binaryDigest, err := digest(settings.BinarySHA256)
	if err != nil {
		return nil, err
	}
	receiptDigest, err := digest(settings.QualificationReceiptSHA256)
	if err != nil {
		return nil, err
	}
	return &simulationNodeComponent{
		settings: settings,
		mysql:    mysql,
		tasks:    tasks,
		clock:    clock,
		metrics:  metrics,
		logger:   logger,
		controllerConfig: simulationcontrol.ControllerConfig{
			NodeID:        nodeID,
			RuntimeNodeID: runtimeNodeID,
			Build: simulationcontrol.BuildBinding{
				BuildIdentity:         buildIdentity,
				ModelManifest:         modelManifest,
				ProfileManifest:       profileManifest,
				PlatformQualification: "implementation-qualified-windows-x64",
			},
			Capacity: simulationcontrol.NodeCapacity{
				Instances: settings.InstanceCapacity,
				Actors:    settings.ActorCapacity,
			},
			ConfigIdentity:     configIdentity,
			NavigationIdentity: navigationIdentity,
			PhysicsIdentity:    physicsIdentity,
			DrainDeadline:      settings.DrainTimeout,
			StopDeadline:       settings.ShutdownTimeout,
		},
		processConfig: simulationprocess.Config{
			BinaryPath:                 settings.BinaryPath,
			BinarySHA256:               binaryDigest,
			QualificationReceiptPath:   settings.QualificationReceiptPath,
			QualificationReceiptSHA256: receiptDigest,
			RequestTimeout:             settings.RequestTimeout,
			ShutdownTimeout:            settings.ShutdownTimeout,
			StderrLineLimit:            settings.StderrLineBytes,
		},
		inbox: simulationcontrol.NewProposalInbox(),
	}, nil
}

// Name 返回 lifecycle 稳定名称。
func (*simulationNodeComponent) Name() string { return "simulation_node" }

// Start 在 storage 后启动 exact child、完成 hello 并登记监督任务。
func (component *simulationNodeComponent) Start(ctx context.Context) error {
	if component.mysql.DB() == nil {
		return errors.New("simulation node requires started MySQL")
	}
	component.metrics.SetSimulationNodeHealth("starting")
	component.metrics.SetSimulationInstances(0)
	component.metrics.SetSimulationControlQueue(0)
	nonce, err := simulationcontrol.NewSessionNonce()
	if err != nil {
		return err
	}
	owner, err := simulationprocess.Start(
		component.processConfig,
		nonce,
		component.inbox,
		simulationDiagnosticSink{logger: component.logger},
	)
	if err != nil {
		component.metrics.ObserveSimulationProcessExit("failed")
		return err
	}
	helloStarted := time.Now()
	controller, err := simulationcontrol.BootstrapController(ctx, owner.Session(), component.controllerConfig)
	if err != nil {
		component.metrics.ObserveSimulationControl("hello", simulationControlOutcome(err), time.Since(helloStarted))
		component.metrics.SetSimulationNodeHealth("unhealthy")
		_ = owner.Terminate(context.Background())
		return err
	}
	component.metrics.ObserveSimulationControl("hello", "ok", time.Since(helloStarted))
	component.metrics.SetSimulationNodeHealth("ready")
	component.metrics.SetSimulationCapacity("instances", component.settings.InstanceCapacity)
	component.metrics.SetSimulationCapacity("actors", component.settings.ActorCapacity)
	component.owner = owner
	component.controller = controller
	if err := component.tasks.Go("process_wait", component.waitProcess); err != nil {
		_ = controller.Shutdown(context.Background())
		_ = owner.Terminate(context.Background())
		return err
	}
	if err := component.tasks.Go("health", component.runHealth); err != nil {
		_ = component.tasks.Stop(context.Background(), errors.New("simulation health task startup failed"))
		_ = controller.Shutdown(context.Background())
		_ = owner.Terminate(context.Background())
		return err
	}
	return nil
}

// BindCurrent 在 public listener 前绑定 placement current 与 MySQL receipt store。
func (component *simulationNodeComponent) BindCurrent(current simulationcontrol.CurrentAssignmentReader) error {
	if component == nil || current == nil || component.controller == nil {
		return errors.New("simulation result current binding is invalid")
	}
	component.resultMutex.Lock()
	defer component.resultMutex.Unlock()
	if component.results != nil {
		return errors.New("simulation result current binding already exists")
	}
	receipts, err := storagesimulationresult.New(component.mysql.DB(), component.metrics)
	if err != nil {
		return err
	}
	results, err := simulationcontrol.NewResultCoordinator(
		receipts,
		component.controller,
		current,
		component.controller,
		resultClock{Clock: component.clock},
	)
	if err != nil {
		return err
	}
	targets, err := simulationcontrol.NewTargetResolver(current, component.controller, resultClock{Clock: component.clock})
	if err != nil {
		return err
	}
	component.results = results
	component.targets = targets
	return nil
}

// Start 满足 placement.RuntimeController 的适配由 simulationRuntimeAdapter 提供，避免与 Component.Start 冲突。
type simulationRuntimeAdapter struct {
	// component 是唯一 node lifecycle owner。
	component *simulationNodeComponent
}

// Start 委托 C++ controller。
func (adapter simulationRuntimeAdapter) Start(ctx context.Context, snapshot placement.AssignmentSnapshot) error {
	started := time.Now()
	err := adapter.component.controller.Start(ctx, snapshot)
	adapter.component.metrics.ObserveSimulationControl("start", simulationControlOutcome(err), time.Since(started))
	adapter.component.metrics.SetSimulationInstances(len(adapter.component.controller.Stamps()))
	return err
}

// Drain 委托 C++ controller 并在返回前持久裁决全部 flush proposals。
func (adapter simulationRuntimeAdapter) Drain(ctx context.Context, stamp placement.AssignmentStamp) error {
	started := time.Now()
	drainErr := adapter.component.controller.Drain(ctx, stamp)
	adapter.component.metrics.ObserveSimulationControl("drain", simulationControlOutcome(drainErr), time.Since(started))
	drainOutcome := "drained"
	if drainErr != nil {
		drainOutcome = simulationDrainOutcome(drainErr)
	}
	adapter.component.metrics.ObserveSimulationDrain(drainOutcome)
	return errors.Join(drainErr, adapter.component.flushResults(ctx))
}

// Stop 委托 exact C++ stop。
func (adapter simulationRuntimeAdapter) Stop(ctx context.Context, stamp placement.AssignmentStamp) error {
	started := time.Now()
	err := adapter.component.controller.Stop(ctx, stamp)
	adapter.component.metrics.ObserveSimulationControl("stop", simulationControlOutcome(err), time.Since(started))
	adapter.component.metrics.SetSimulationInstances(len(adapter.component.controller.Stamps()))
	return err
}

// Contains 报告 exact node binding。
func (adapter simulationRuntimeAdapter) Contains(stamp placement.AssignmentStamp) bool {
	return adapter.component.controller.Contains(stamp)
}

// StampsForWorld 返回 node-local world bindings。
func (adapter simulationRuntimeAdapter) StampsForWorld(worldID personalworld.PersonalWorldID) []placement.AssignmentStamp {
	return adapter.component.controller.StampsForWorld(worldID)
}

// Stamps 返回 node-local 全部 runtime bindings 的稳定副本。
func (adapter simulationRuntimeAdapter) Stamps() []placement.AssignmentStamp {
	return adapter.component.controller.Stamps()
}

// Runtime 返回 placement/coordinator 共用的窄 adapter。
func (component *simulationNodeComponent) Runtime() simulationRuntimeAdapter {
	return simulationRuntimeAdapter{component: component}
}

// RuntimeNodeID 返回 placement selector 使用的受信 node identity。
func (component *simulationNodeComponent) RuntimeNodeID() placement.RuntimeNodeID {
	return component.controllerConfig.RuntimeNodeID
}

// TargetResolver 返回已绑定 placement current 的内部 target resolver。
func (component *simulationNodeComponent) TargetResolver() *simulationcontrol.TargetResolver {
	if component == nil {
		return nil
	}
	return component.targets
}

// Stop 先取消监督任务，再 drain/result/stop instances，最后 shutdown child。
func (component *simulationNodeComponent) Stop(ctx context.Context) error {
	if component == nil || ctx == nil {
		return errors.New("simulation node stop context is invalid")
	}
	taskErr := component.tasks.Stop(ctx, errors.New("simulation node component stopped"))
	component.metrics.SetSimulationNodeHealth("draining")
	var lifecycleErr error
	if component.controller != nil {
		for _, stamp := range component.controller.Stamps() {
			drainStarted := time.Now()
			drainErr := component.controller.Drain(ctx, stamp)
			component.metrics.ObserveSimulationControl("drain", simulationControlOutcome(drainErr), time.Since(drainStarted))
			component.metrics.ObserveSimulationDrain(simulationDrainOutcomeOrSuccess(drainErr))
			stopStarted := time.Now()
			stopErr := component.controller.Stop(ctx, stamp)
			component.metrics.ObserveSimulationControl("stop", simulationControlOutcome(stopErr), time.Since(stopStarted))
			lifecycleErr = errors.Join(
				lifecycleErr,
				drainErr,
				component.flushResults(ctx),
				stopErr,
			)
		}
		shutdownStarted := time.Now()
		shutdownErr := component.controller.Shutdown(ctx)
		component.metrics.ObserveSimulationControl("shutdown", simulationControlOutcome(shutdownErr), time.Since(shutdownStarted))
		lifecycleErr = errors.Join(lifecycleErr, shutdownErr)
	}
	if component.owner != nil {
		select {
		case <-component.owner.Done():
			component.metrics.ObserveSimulationProcessExit("expected")
		case <-ctx.Done():
			lifecycleErr = errors.Join(lifecycleErr, component.owner.Terminate(context.Background()))
			component.metrics.ObserveSimulationProcessExit("terminated")
		}
	}
	result := errors.Join(taskErr, lifecycleErr)
	component.metrics.SetSimulationInstances(0)
	component.metrics.SetSimulationNodeHealth("stopped")
	shutdownOutcome := "clean"
	if result != nil {
		shutdownOutcome = "failed"
		if errors.Is(result, context.DeadlineExceeded) {
			shutdownOutcome = "timeout"
		}
	}
	component.metrics.ObserveSimulationShutdown(shutdownOutcome)
	return result
}

// flushResults 同步执行 receipt-first decision，避免 drain 返回后 proposal 未落库。
func (component *simulationNodeComponent) flushResults(ctx context.Context) error {
	component.resultMutex.Lock()
	defer component.resultMutex.Unlock()
	component.metrics.SetSimulationControlQueue(component.inbox.Len())
	if component.inbox.Len() == 0 {
		return nil
	}
	if component.results == nil {
		return errors.New("simulation result coordinator is not bound")
	}
	var result error
	for {
		proposal, found := component.inbox.Peek()
		if !found {
			component.metrics.SetSimulationControlQueue(0)
			return result
		}
		processOutcome, processErr := component.results.ProcessWithOutcome(ctx, proposal)
		result = errors.Join(result, processErr)
		resultOutcome := string(processOutcome)
		if processErr != nil {
			resultOutcome = "failed"
		}
		component.metrics.ObserveSimulationResult(resultOutcome)
		component.metrics.SetSimulationControlQueue(component.inbox.Len())
		if result != nil {
			return result
		}
		if discardErr := component.inbox.Discard(proposal); discardErr != nil {
			return discardErr
		}
	}
}

// waitProcess 把 unexpected child exit 转为 terminal supervised failure。
func (component *simulationNodeComponent) waitProcess(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-component.owner.Done():
		component.metrics.SetSimulationNodeHealth("unhealthy")
		component.metrics.ObserveSimulationProcessExit("unexpected")
		if err := component.owner.Err(); err != nil {
			return err
		}
		return errors.New("simulation child exited unexpectedly")
	}
}

// runHealth 周期验证 exact node receipt；任一失败触发 root draining。
func (component *simulationNodeComponent) runHealth(ctx context.Context) error {
	timer := time.NewTimer(component.settings.HealthInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			probeContext, cancel := context.WithTimeout(ctx, component.settings.HealthTimeout)
			probeStarted := time.Now()
			err := component.controller.Probe(probeContext)
			cancel()
			component.metrics.ObserveSimulationControl("health", simulationControlOutcome(err), time.Since(probeStarted))
			if errors.Is(err, simulationcontrol.ErrProbeBusy) {
				timer.Reset(component.settings.HealthInterval)
				continue
			}
			if err != nil {
				component.metrics.SetSimulationNodeHealth("unhealthy")
				return err
			}
			component.metrics.SetSimulationNodeHealth("ready")
			timer.Reset(component.settings.HealthInterval)
		}
	}
}

// simulationControlOutcome 把内部错误归一为低基数 control 指标结果。
func simulationControlOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, simulationcontrol.ErrProbeBusy) {
		return "busy"
	}
	var sessionErr *simulationcontrol.SessionError
	if errors.As(err, &sessionErr) {
		switch sessionErr.Kind {
		case simulationcontrol.SessionErrorProtocol:
			return "protocol"
		case simulationcontrol.SessionErrorTransport, simulationcontrol.SessionErrorClosed:
			return "transport"
		case simulationcontrol.SessionErrorDeadline:
			return "deadline"
		}
	}
	return "failed"
}

// simulationDrainOutcome 把 drain error 映射为封闭结果。
func simulationDrainOutcome(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	return "failed"
}

// simulationDrainOutcomeOrSuccess 把 nil 映射为 drained。
func simulationDrainOutcomeOrSuccess(err error) string {
	if err == nil {
		return "drained"
	}
	return simulationDrainOutcome(err)
}

// simulationDiagnosticSink 只记录被 process adapter 清洗后的低敏行。
type simulationDiagnosticSink struct {
	// logger 绑定 simulation_node component。
	logger *slog.Logger
}

// ObserveSimulationDiagnostic 写入固定 operation，不包含 artifact path 或 nonce。
func (sink simulationDiagnosticSink) ObserveSimulationDiagnostic(message string) {
	sink.logger.Warn("simulation child diagnostic", "operation", "child_stderr", "message", message)
}

// resultClock 把 app Clock 适配为 receipt UTC clock。
type resultClock struct {
	// Clock 是 Composition Root 共享受信 clock。
	Clock
}

// Now 返回 UTC 微秒时间。
func (clock resultClock) Now() time.Time {
	return clock.Clock.Now().UTC().Truncate(time.Microsecond)
}

var (
	_ Component                        = (*simulationNodeComponent)(nil)
	_ placement.RuntimeController      = simulationRuntimeAdapter{}
	_ runtimeInventory                 = simulationRuntimeAdapter{}
	_ simulationprocess.DiagnosticSink = simulationDiagnosticSink{}
)
