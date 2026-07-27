package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/processmetrics"
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
	SetBattleQualificationControlMetric(string, uint64)
	SetBattleQualificationProcessMetric(string, string, uint64)
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
	// goProcessSampler 只在显式 qualification mode 读取 parent OS counters。
	goProcessSampler *processmetrics.Sampler
	// childProcessSampler 只在显式 qualification mode 读取 exact child OS counters。
	childProcessSampler *processmetrics.Sampler
	// resultMutex 保护 coordinator bind 与同步消费。
	resultMutex sync.Mutex
	// results 在 public placement store 构造后绑定。
	results *simulationcontrol.ResultCoordinator
	// targets 只为 current active/lease-valid/healthy/ready binding 返回内部 target。
	targets *simulationcontrol.TargetResolver
}

// newSimulationNodeComponent 解析配置并生成不可复活 node identities，不启动进程。
func newSimulationNodeComponent(settings config.SimulationControl, battle config.BattleUDPPolicy, mysql *storagemysql.Component, tasks *TaskOwner, clock Clock, ids IDGenerator, metrics simulationNodeObserver, logger *slog.Logger) (*simulationNodeComponent, error) {
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
	var qualificationRunID simulationcontrol.QualificationRunID
	if settings.QualificationMode {
		qualificationRunID, err = simulationcontrol.NewQualificationRunID(
			settings.QualificationRunID,
		)
		if err != nil {
			return nil, err
		}
	}
	bindHost, bindPortText, err := net.SplitHostPort(battle.BindAddress)
	if err != nil {
		return nil, err
	}
	bindPort, err := strconv.ParseUint(bindPortText, 10, 16)
	if err != nil || bindPort == 0 || battle.Advertised.Port <= 0 || battle.Advertised.Port > 65535 {
		return nil, errors.New("battle UDP production endpoint is invalid")
	}
	listenerDigest := sha256.Sum256([]byte("ihomeland/battle-listener/v1|" + nodeID.String() + "|" + battle.WireIdentity))
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
			QualificationMode:  settings.QualificationMode,
			QualificationRunID: qualificationRunID,
		},
		processConfig: simulationprocess.Config{
			BinaryPath:                 settings.BinaryPath,
			BinarySHA256:               binaryDigest,
			QualificationReceiptPath:   settings.QualificationReceiptPath,
			QualificationReceiptSHA256: receiptDigest,
			RequestTimeout:             settings.RequestTimeout,
			ShutdownTimeout:            settings.ShutdownTimeout,
			StderrLineLimit:            settings.StderrLineBytes,
			BattleUDPEnabled:           true,
			BattleUDPBindHost:          bindHost,
			BattleUDPBindPort:          uint16(bindPort),
			BattleUDPAdvertisedHost:    battle.Advertised.Host,
			BattleUDPAdvertisedPort:    uint16(battle.Advertised.Port),
			BattleListenerIdentity:     hex.EncodeToString(listenerDigest[:16]),
			QualificationMode:          settings.QualificationMode,
			QualificationRunID:         qualificationRunID,
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
	if component.settings.QualificationMode {
		goSampler, samplerErr := processmetrics.New(os.Getpid())
		if samplerErr != nil {
			_ = controller.Shutdown(context.Background())
			_ = owner.Terminate(context.Background())
			return errors.New("start qualification Go process sampler")
		}
		childSampler, samplerErr := processmetrics.New(owner.ProcessID())
		if samplerErr != nil {
			_ = goSampler.Close()
			_ = controller.Shutdown(context.Background())
			_ = owner.Terminate(context.Background())
			return errors.New("start qualification child process sampler")
		}
		component.goProcessSampler = goSampler
		component.childProcessSampler = childSampler
	}
	if err := component.tasks.Go("process_wait", component.waitProcess); err != nil {
		_ = component.closeQualificationSamplers()
		_ = controller.Shutdown(context.Background())
		_ = owner.Terminate(context.Background())
		return err
	}
	if err := component.tasks.Go("health", component.runHealth); err != nil {
		_ = component.tasks.Stop(context.Background(), errors.New("simulation health task startup failed"))
		_ = component.closeQualificationSamplers()
		_ = controller.Shutdown(context.Background())
		_ = owner.Terminate(context.Background())
		return err
	}
	if component.settings.QualificationMode {
		if err := component.tasks.Go(
			"qualification_sampling",
			component.runQualificationSampling,
		); err != nil {
			_ = component.tasks.Stop(
				context.Background(),
				errors.New("simulation qualification sampling startup failed"),
			)
			_ = component.closeQualificationSamplers()
			_ = controller.Shutdown(context.Background())
			_ = owner.Terminate(context.Background())
			return err
		}
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

// ControlSession 返回 exact child 的私有 control session；未 ready 时返回 nil。
func (component *simulationNodeComponent) ControlSession() *simulationcontrol.Session {
	if component == nil || component.owner == nil {
		return nil
	}
	return component.owner.Session()
}

// SimulationNodeID 返回本次不可复活 child identity。
func (component *simulationNodeComponent) SimulationNodeID() simulationcontrol.SimulationNodeID {
	if component == nil {
		var empty simulationcontrol.SimulationNodeID
		return empty
	}
	return component.controllerConfig.NodeID
}

// Stop 先取消监督任务，再 drain/result/stop instances，最后 shutdown child。
func (component *simulationNodeComponent) Stop(ctx context.Context) error {
	if component == nil || ctx == nil {
		return errors.New("simulation node stop context is invalid")
	}
	taskErr := component.tasks.Stop(ctx, errors.New("simulation node component stopped"))
	samplerErr := component.closeQualificationSamplers()
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
	result := errors.Join(taskErr, samplerErr, lifecycleErr)
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

// runQualificationSampling 以配置 cadence 采集 exact control 与 Go/C++ OS counters。
func (component *simulationNodeComponent) runQualificationSampling(ctx context.Context) error {
	if component == nil || !component.settings.QualificationMode ||
		component.controller == nil ||
		component.goProcessSampler == nil ||
		component.childProcessSampler == nil {
		return errors.New("qualification sampler is not initialized")
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			if err := component.sampleQualificationWindow(ctx); err != nil {
				return err
			}
			timer.Reset(component.settings.QualificationSampleInterval)
		}
	}
}

// sampleQualificationWindow 发布一轮 process samples，并从稳定排序的 current instances
// 选择一个读取 control。公开 admission 会先启动参与者各自的 PersonalWorld，因此零个或
// 多个实例都是合法生命周期状态；node-global transport counters 不依赖所选实例。
func (component *simulationNodeComponent) sampleQualificationWindow(ctx context.Context) error {
	goSample, err := component.goProcessSampler.Sample()
	if err != nil {
		return errors.New("sample qualification Go process")
	}
	childSample, err := component.childProcessSampler.Sample()
	if err != nil {
		return errors.New("sample qualification child process")
	}
	component.observeProcessSample("go-parent", goSample)
	component.observeProcessSample("cpp-child", childSample)

	stamps := component.controller.Stamps()
	if len(stamps) == 0 {
		return nil
	}
	snapshotContext, cancel := context.WithTimeout(
		ctx,
		min(component.settings.RequestTimeout, component.settings.QualificationSampleInterval),
	)
	defer cancel()
	snapshot, err := component.controller.QualificationSnapshot(
		snapshotContext,
		component.controllerConfig.QualificationRunID,
		stamps[0],
	)
	if err != nil {
		return fmt.Errorf("sample qualification control snapshot: %w", err)
	}
	component.observeQualificationSnapshot(snapshot)
	return nil
}

// observeProcessSample 将 OS sample 映射到固定、无 identity 的 metric 集合。
func (component *simulationNodeComponent) observeProcessSample(
	role string,
	sample processmetrics.Sample,
) {
	values := map[string]uint64{
		"sequence":          sample.Sequence,
		"monotonic-time-us": sample.MonotonicTimeUS,
		"cpu-time-ns":       sample.CPUTimeNS,
		"working-set-bytes": sample.WorkingSetBytes,
		"handle-count":      uint64(sample.HandleCount),
		"thread-count":      uint64(sample.ThreadCount),
	}
	for metric, value := range values {
		component.metrics.SetBattleQualificationProcessMetric(role, metric, value)
	}
}

// observeQualificationSnapshot 将 private receipt 映射为 closed diagnostic metric。
func (component *simulationNodeComponent) observeQualificationSnapshot(
	snapshot simulationcontrol.BattleQualificationSnapshot,
) {
	values := map[string]uint64{
		"sample-sequence":              snapshot.SampleSequence,
		"committed-tick":               snapshot.CommittedTick,
		"node-count":                   snapshot.NodeCount,
		"running-instance-count":       snapshot.RunningInstanceCount,
		"active-session-count":         snapshot.ActiveSessionCount,
		"installed-ticket-count":       snapshot.InstalledTicketCount,
		"raw-ingress-bytes":            snapshot.Metrics.RawIngressBytes,
		"raw-ingress-packets":          snapshot.Metrics.RawIngressPackets,
		"raw-egress-bytes":             snapshot.Metrics.RawEgressBytes,
		"raw-egress-packets":           snapshot.Metrics.RawEgressPackets,
		"kcp-ingress-bytes":            snapshot.Metrics.KCPIngressBytes,
		"kcp-ingress-packets":          snapshot.Metrics.KCPIngressPackets,
		"kcp-egress-bytes":             snapshot.Metrics.KCPEgressBytes,
		"kcp-egress-packets":           snapshot.Metrics.KCPEgressPackets,
		"dropped-packets":              snapshot.Metrics.DroppedPackets,
		"rejected-packets":             snapshot.Metrics.RejectedPackets,
		"expired-messages":             snapshot.Metrics.ExpiredMessages,
		"kcp-retransmits":              snapshot.Metrics.KCPRetransmits,
		"ingress-queue-high-watermark": snapshot.Metrics.IngressQueueHighWatermark,
		"egress-queue-high-watermark":  snapshot.Metrics.EgressQueueHighWatermark,
		"kcp-queue-high-watermark":     snapshot.Metrics.KCPQueueHighWatermark,
		"maximum-tick-duration-ns":     snapshot.Metrics.MaximumTickDurationNS,
		"tick-debt-high-watermark":     snapshot.Metrics.TickDebtHighWatermark,
		"instance-memory-bytes":        snapshot.Metrics.InstanceMemoryBytes,
		"history-memory-bytes":         snapshot.Metrics.HistoryMemoryBytes,
		"rebinds":                      snapshot.Metrics.Rebinds,
		"rekeys":                       snapshot.Metrics.Rekeys,
		"close-normal":                 snapshot.Metrics.CloseNormal,
		"close-authentication":         snapshot.Metrics.CloseAuthentication,
		"close-timeout":                snapshot.Metrics.CloseTimeout,
		"close-resource":               snapshot.Metrics.CloseResource,
		"close-lifecycle":              snapshot.Metrics.CloseLifecycle,
		"close-transport":              snapshot.Metrics.CloseTransport,
		"close-internal":               snapshot.Metrics.CloseInternal,
	}
	for metric, value := range values {
		component.metrics.SetBattleQualificationControlMetric(metric, value)
	}
}

// closeQualificationSamplers 逆序释放只读 process handles；重复调用安全。
func (component *simulationNodeComponent) closeQualificationSamplers() error {
	if component == nil {
		return nil
	}
	childErr := component.childProcessSampler.Close()
	goErr := component.goProcessSampler.Close()
	component.childProcessSampler = nil
	component.goProcessSampler = nil
	return errors.Join(childErr, goErr)
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
