package runner

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/correlation"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateway"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/manifest"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/measurement"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/protocolclient"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/workload"
	battlev1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/battle/v1"
	"github.com/jinwiforz/ihomeland/server/internal/testclient"
)

const (
	// diagnosticBodyBytes 是一次 Prometheus scrape 的 hard cap。
	diagnosticBodyBytes = 1 << 20
	// pollMaximumEvents 是 child 单次 bounded receive 的 contract ceiling。
	pollMaximumEvents = 32
	// pollWait 避免 40 Hz actor loop 被 receive 长时间占用。
	pollWait = time.Millisecond
	// transitionDrainBatches 限制 rebind 前最多排空的 32-event batch 数。
	transitionDrainBatches = 4
	// metricsRetryInterval 等待 periodic server sampler 发布完整窗口。
	metricsRetryInterval = 100 * time.Millisecond
	// resyncCadence 是 profile 登记的 2/s route rate 对应的单调时间间隔。
	resyncCadence = 500 * time.Millisecond
	// probeCadenceSteps 对应冻结的 4 Hz probe。
	probeCadenceSteps = uint64(workload.ProbeCadence / workload.InputCadence)
)

// FailureStage 是 runner 对外允许披露的低敏失败边界。
type FailureStage string

const (
	// FailureStageValidation 表示输入或 tracked contract 校验失败。
	FailureStageValidation FailureStage = "validation"
	// FailureStageGateway 表示 opaque fault gateway 创建或启动失败。
	FailureStageGateway FailureStage = "gateway"
	// FailureStageAdmission 表示公开 admission 链失败。
	FailureStageAdmission FailureStage = "admission"
	// FailureStageActorStart 表示独立协议客户端未完成 session 建立。
	FailureStageActorStart FailureStage = "actor-start"
	// FailureStageWarmup 表示 warmup workload 未完成。
	FailureStageWarmup FailureStage = "warmup"
	// FailureStageFaultActivation 表示 measurement 前无法启用 deterministic fault policy。
	FailureStageFaultActivation FailureStage = "fault-activation"
	// FailureStageFaultQuiesce 表示 measurement 后无法终止新的 fault ingress。
	FailureStageFaultQuiesce FailureStage = "fault-quiesce"
	// FailureStageStartMetrics 表示 measurement 起点样本不可用。
	FailureStageStartMetrics FailureStage = "start-metrics"
	// FailureStageRebind 表示 authenticated endpoint rebind 未提交。
	FailureStageRebind FailureStage = "rebind"
	// FailureStageMeasurement 表示 measurement workload 或 rekey 失败。
	FailureStageMeasurement FailureStage = "measurement"
	// FailureStageSecurityAttack 表示登记的独立攻击 source 未完成负例动作。
	FailureStageSecurityAttack FailureStage = "security-attack"
	// FailureStageSecurityEvidence 表示攻击计数、拒绝计数或放大边界不一致。
	FailureStageSecurityEvidence FailureStage = "security-evidence"
	// FailureStageWorkloadTransition 表示 tracked phase 无法切换。
	FailureStageWorkloadTransition FailureStage = "workload-transition"
	// FailureStageWorkloadSend 表示 typed input 无法完成 client round trip。
	FailureStageWorkloadSend FailureStage = "workload-send"
	// FailureStageWorkloadProbe 表示 typed probe 无法完成 client round trip。
	FailureStageWorkloadProbe FailureStage = "workload-probe"
	// FailureStageWorkloadResync 表示 typed resync 无法完成 client round trip。
	FailureStageWorkloadResync FailureStage = "workload-resync"
	// FailureStageWorkloadPoll 表示 client receive poll 失败。
	FailureStageWorkloadPoll FailureStage = "workload-poll"
	// FailureStageWorkloadReceipt 表示 client cumulative receipt 违反单调契约。
	FailureStageWorkloadReceipt FailureStage = "workload-receipt"
	// FailureStageClientProcess 表示独立 C++ client 写过诊断并异常终止。
	FailureStageClientProcess FailureStage = "client-process"
	// FailureStageEndMetrics 表示 measurement 终点样本不可用。
	FailureStageEndMetrics FailureStage = "end-metrics"
	// FailureStagePreCloseMetrics 表示无法建立 authenticated close 前的采样屏障。
	FailureStagePreCloseMetrics FailureStage = "pre-close-metrics"
	// FailureStageRebindEvidence 表示 rebind 的 client/control 双源证据不一致。
	FailureStageRebindEvidence FailureStage = "rebind-evidence"
	// FailureStageRebindClientEvidence 表示独立客户端未覆盖全部 rebind。
	FailureStageRebindClientEvidence FailureStage = "rebind-client-evidence"
	// FailureStageRebindControlEvidence 表示 C++ control 未覆盖全部 rebind。
	FailureStageRebindControlEvidence FailureStage = "rebind-control-evidence"
	// FailureStageRekeyEvidence 表示 rekey 的 client/control 双源证据不一致。
	FailureStageRekeyEvidence FailureStage = "rekey-evidence"
	// FailureStageClosureClientEvidence 表示客户端未确认全部 authenticated close。
	FailureStageClosureClientEvidence FailureStage = "closure-client-evidence"
	// FailureStageClosureReasonEvidence 表示 C++ 未记录完整正常 close reason。
	FailureStageClosureReasonEvidence FailureStage = "closure-reason-evidence"
	// FailureStageClosureRuntimeEvidence 表示关闭后仍有 active session 或 ticket。
	FailureStageClosureRuntimeEvidence FailureStage = "closure-runtime-evidence"
	// FailureStageSoakBudget 表示 soak resource budget 越界。
	FailureStageSoakBudget FailureStage = "soak-budget"
	// FailureStageCleanup 表示 owned resource 未在 deadline 内回收。
	FailureStageCleanup FailureStage = "cleanup"
	// FailureStageClientEvidence 表示 client measurement evidence 不完整。
	FailureStageClientEvidence FailureStage = "client-evidence"
	// FailureStageClientWindow 表示 client measurement 起止集合不一致。
	FailureStageClientWindow FailureStage = "client-window"
	// FailureStageClientCounters 表示发送或 reliable 累计值发生回退。
	FailureStageClientCounters FailureStage = "client-counters"
	// FailureStageClientSnapshot 表示 snapshot count/sequence 未在窗口内推进。
	FailureStageClientSnapshot FailureStage = "client-snapshot"
	// FailureStageClientTick 表示权威 Tick 未在窗口内推进。
	FailureStageClientTick FailureStage = "client-tick"
	// FailureStageClientBaseline 表示完整 baseline identity 不可用。
	FailureStageClientBaseline FailureStage = "client-baseline"
	// FailureStageClientInputAck 表示 snapshot 未发布非零连续输入确认。
	FailureStageClientInputAck FailureStage = "client-input-ack"
	// FailureStageClientSnapshotGap 表示 measurement 内没有可计算的 snapshot 间隔。
	FailureStageClientSnapshotGap FailureStage = "client-snapshot-gap"
	// FailureStageGatewayEvidence 表示 gateway metadata evidence 不完整。
	FailureStageGatewayEvidence FailureStage = "gateway-evidence"
	// FailureStageCrossCheck 表示四源 packet/tick 守恒不成立。
	FailureStageCrossCheck FailureStage = "cross-check"
	// FailureStageCrossCheckDirection 表示网关或客户端缺少双向有效流量。
	FailureStageCrossCheckDirection FailureStage = "cross-check-direction"
	// FailureStageCrossCheckProgress 表示 control packet counters 未在窗口内推进。
	FailureStageCrossCheckProgress FailureStage = "cross-check-progress"
	// FailureStageCrossCheckUplink 表示网关投递与 control ingress 不守恒。
	FailureStageCrossCheckUplink FailureStage = "cross-check-uplink"
	// FailureStageCrossCheckDownlink 表示 control egress 与网关终局不守恒。
	FailureStageCrossCheckDownlink FailureStage = "cross-check-downlink"
)

// StageError 保存内部 cause，但只向进程边界提供封闭阶段码。
type StageError struct {
	stage FailureStage
	cause error
}

// Error 避免把可能含 endpoint 或 child 诊断的 cause 写入外部日志。
func (failure *StageError) Error() string {
	return "battle qualification stage failed"
}

// Unwrap 保留进程内 errors.Is/errors.As 诊断能力。
func (failure *StageError) Unwrap() error {
	return failure.cause
}

// QualificationFailureCode 返回可安全写入资格编排日志的封闭阶段码。
func (failure *StageError) QualificationFailureCode() string {
	return string(failure.stage)
}

// newStageError 在最外层只包装一次，避免 cleanup join 覆盖原失败阶段。
func newStageError(stage FailureStage, cause error) error {
	if cause == nil {
		return nil
	}
	var existing interface {
		QualificationFailureCode() string
	}
	if errors.As(cause, &existing) {
		return cause
	}
	return &StageError{stage: stage, cause: cause}
}

// Config 是一个真实 fault scenario 的完整外部依赖集合。
type Config struct {
	// Definition 是通过 B0.6/B0.2 双 manifest 校验的 immutable scenario。
	Definition manifest.Definition
	// GatewayConfig 是 Definition 生成且绑定真实 backend/frontend 的配置。
	GatewayConfig gateway.SocketConfig
	// Runtime 只暴露公开 HTTPS/WSS/TLS_TCP admission。
	Runtime *testclient.ScenarioRuntime
	// ProtocolClientConfig 绑定 exact C++ binary identity。
	ProtocolClientConfig protocolclient.Config
	// DiagnosticURL 是 existing loopback diagnostic listener 的 `/metrics`。
	DiagnosticURL string
	// DiagnosticClient 为 scrape 提供有界 HTTP transport。
	DiagnosticClient *http.Client
	// Soak 非空时启用 tracked 30 分钟窗口与周期 rekey；普通 scenario 必须为空。
	Soak *manifest.SoakPolicy
	// Metrics 是 tracked metric catalog，不允许 runtime 复制预算常量。
	Metrics manifest.MetricCatalog
}

// Evidence 是单场景四源 measurement 的低敏规范化结果。
type Evidence struct {
	// SchemaVersion 是当前 run evidence 的 closed schema generation。
	SchemaVersion int `json:"schemaVersion"`
	// QualificationVersion 绑定 B0.6 corpus。
	QualificationVersion string `json:"qualificationVersion"`
	// EvidenceKind 区分单场景 evidence 与最终资格报告。
	EvidenceKind string `json:"evidenceKind"`
	// ScenarioID 是 B0.6 overlay identity。
	ScenarioID string `json:"scenarioId"`
	// SourceScenarioID 是只读 B0.2 source identity。
	SourceScenarioID string `json:"sourceScenarioId"`
	// WorkloadID 是本场景唯一的 tracked workload。
	WorkloadID string `json:"workloadId"`
	// ActorCount 是真实并发协议客户端数量。
	ActorCount uint8 `json:"actorCount"`
	// Phases 按实际执行顺序登记完整 coverage。
	Phases []string `json:"phases"`
	// Gateway 汇总 measurement window 内的 opaque packet metadata。
	Gateway GatewayEvidence `json:"gateway"`
	// Clients 汇总独立 C++ client 的累计差值。
	Clients []ClientEvidence `json:"clients"`
	// Metrics 保存 qualification control 与两进程 OS sample 窗口。
	Metrics measurement.Window `json:"metrics"`
	// Transitions 保存 client/control 两源共同确认的 lifecycle transition。
	Transitions TransitionEvidence `json:"transitions"`
	// Closure 证明全部 authenticated session 正常终结且服务端不再持有运行态。
	Closure ClosureEvidence `json:"closure"`
	// SoakInvariants 只在 soak evidence 中回显全部 tracked invariant coverage。
	SoakInvariants []string `json:"soakInvariants,omitempty"`
	// SoakSummary 只在 soak evidence 中保存固定大小的周期采样归约。
	SoakSummary *measurement.SoakSummary `json:"soakSummary,omitempty"`
	// Disposition 是本次 fail-closed 裁决。
	Disposition string `json:"disposition"`
}

// TransitionEvidence 是 measurement window 内 authenticated lifecycle 结果。
type TransitionEvidence struct {
	// ClientRebinds 是全部独立 child committed endpoint generation 数。
	ClientRebinds uint64 `json:"clientRebinds"`
	// ControlRebinds 是 C++ runtime qualification counter 的窗口差值。
	ControlRebinds uint64 `json:"controlRebinds"`
	// ClientRekeys 是全部独立 child committed rekey receipt 数。
	ClientRekeys uint64 `json:"clientRekeys"`
	// ControlRekeys 是 C++ runtime qualification counter 的窗口差值。
	ControlRekeys uint64 `json:"controlRekeys"`
}

// ClosureEvidence 是 measurement 结束后的有界会话终结证据。
type ClosureEvidence struct {
	// ClientClosedSessions 是独立客户端确认完成 authenticated close 的数量。
	ClientClosedSessions uint64 `json:"clientClosedSessions"`
	// ControlNormalCloseReasons 是 C++ runtime 观测到的正常关闭增量。
	ControlNormalCloseReasons uint64 `json:"controlNormalCloseReasons"`
	// ControlUnexpectedCloseReasons 是认证、超时、资源、生命周期、传输或内部关闭增量。
	ControlUnexpectedCloseReasons uint64 `json:"controlUnexpectedCloseReasons"`
	// RemainingActiveSessions 是关闭后 C++ runtime 的 active session gauge。
	RemainingActiveSessions uint64 `json:"remainingActiveSessions"`
	// RemainingInstalledTickets 是关闭后 C++ runtime 的 installed ticket gauge。
	RemainingInstalledTickets uint64 `json:"remainingInstalledTickets"`
}

// GatewayEvidence 是可与 control/client counters 交叉检查的聚合。
type GatewayEvidence struct {
	// Packets 是按方向、slot、公开 kind 与 disposition 稳定排序的计数。
	Packets []PacketEvidence `json:"packets"`
	// MaximumScheduledAgeUS 是 fault scheduler 的最大 due-receive 时间。
	MaximumScheduledAgeUS uint64 `json:"maximumScheduledAgeUs"`
	// MaximumDeliveryAgeUS 是成功 socket write 的最大 delivered-receive 时间。
	MaximumDeliveryAgeUS uint64 `json:"maximumDeliveryAgeUs"`
}

// PacketEvidence 汇总一类不含 payload/IP/session identity 的 datagram。
type PacketEvidence struct {
	// Direction 是 uplink 或 downlink。
	Direction string `json:"direction"`
	// ClientSlot 是 run-local 1..8 correlation。
	ClientSlot uint8 `json:"clientSlot"`
	// PacketKind 仅来自公开 header。
	PacketKind string `json:"packetKind"`
	// Disposition 是 packet copy 的唯一终局。
	Disposition string `json:"disposition"`
	// Count 是 measurement window 内 copy 数。
	Count uint64 `json:"count"`
	// Bytes 是对应 opaque datagram bytes 总和。
	Bytes uint64 `json:"bytes"`
}

// ClientEvidence 是一个独立 child 在 measurement window 内的累计差值。
type ClientEvidence struct {
	// ClientSlot 是 run-local actor slot。
	ClientSlot uint8 `json:"clientSlot"`
	// SentDatagrams 是 supervisor 收到的真实 socket send receipts。
	SentDatagrams uint64 `json:"sentDatagrams"`
	// SnapshotCount 是 child 独立认证并验证的 snapshot 数。
	SnapshotCount uint64 `json:"snapshotCount"`
	// ReliableCount 是 child 独立 KCP/application 验证的消息数。
	ReliableCount uint64 `json:"reliableCount"`
	// LatestServerTick 是 measurement 结束时最新权威 tick。
	LatestServerTick uint64 `json:"latestServerTick"`
	// LatestApplicationSequence 是最近接受的 raw/KCP application identity。
	LatestApplicationSequence uint64 `json:"latestApplicationSequence"`
	// LatestSnapshotSequence 是 raw baseline validator 的最新 snapshot identity。
	LatestSnapshotSequence uint64 `json:"latestSnapshotSequence"`
	// BaselineID 是 measurement 结束时可用的完整 baseline。
	BaselineID uint64 `json:"baselineId"`
	// LastProcessedInputTick 是 snapshot 显式发布的输入确认前沿。
	LastProcessedInputTick uint64 `json:"lastProcessedInputTick"`
	// MaximumSnapshotGapUS 是相邻完整 snapshot receipt 的最大本地单调间隔。
	MaximumSnapshotGapUS uint64 `json:"maximumSnapshotGapUs"`
	// MaximumBaselineRecoveryUS 是 resync request 到 successor baseline 的最大间隔。
	MaximumBaselineRecoveryUS uint64 `json:"maximumBaselineRecoveryUs"`
}

// actorOwner 独占一个 child、driver 与累计 receipt。
type actorOwner struct {
	slot       uint8
	supervisor *protocolclient.Supervisor
	driver     *workload.Driver
	// sessionActive 控制 cleanup 是否先尝试 authenticated close。
	sessionActive bool
	state         actorState
}

// actorState 只由 actor phase goroutine 修改，并在 phase join 后读取。
type actorState struct {
	sentDatagrams             uint64
	snapshotCount             uint64
	reliableCount             uint64
	latestServerTick          uint64
	latestApplicationSequence uint64
	latestSnapshotSequence    uint64
	baselineID                uint64
	lastProcessedInputTick    uint64
	maximumSnapshotGapUS      uint64
	maximumBaselineRecoveryUS uint64
	lastSnapshotAt            time.Time
	recoveryStartedAt         time.Time
	recoveryBaselineID        uint64
	baselineGapCount          uint64
	handledBaselineGapCount   uint64
	resyncSequence            uint64
	lastResyncAt              time.Time
	step                      uint64
}

// phaseRecoveryPolicy 区分真实 baseline gap 恢复与专用 KCP 重传负载。
type phaseRecoveryPolicy struct {
	// baselineGapDriven 只在 client decoder 观测到未知 baseline 后发起 resync。
	baselineGapDriven bool
	// periodicKCP 为专用重传场景按冻结 cadence 持续产生可靠消息。
	periodicKCP bool
}

// resyncTrigger 区分 fault seed、真实 gap recovery 与 KCP 压力消息的计时所有权。
type resyncTrigger uint8

const (
	// resyncTriggerNone 表示当前单调时刻不应提交可靠恢复消息。
	resyncTriggerNone resyncTrigger = iota
	// resyncTriggerGapSeed 只用于让 opaque gateway 建立确定性 baseline gap。
	resyncTriggerGapSeed
	// resyncTriggerGapRecovery 表示 decoder 已观测 gap，必须纳入恢复时长。
	resyncTriggerGapRecovery
	// resyncTriggerPeriodic 表示 KCP retransmit workload 的周期可靠消息。
	resyncTriggerPeriodic
)

// lifecycle 集中管理 admission、children、gateway 与 evidence drain。
type lifecycle struct {
	gateway       *gateway.Gateway
	admission     *testclient.BattleAdmission
	actors        []*actorOwner
	metadata      []gateway.Metadata
	metadataDone  chan struct{}
	cleanupBudget time.Duration
	cleanupOnce   sync.Once
	cleanupCtx    context.Context
	cleanupCancel context.CancelFunc
	closeOnce     sync.Once
	closeErr      error
}

// Run 执行 warmup、全部 phase、四源窗口校验与逆序 cleanup。
func Run(ctx context.Context, config Config) (_ Evidence, resultErr error) {
	stage := FailureStageValidation
	if err := config.validate(); err != nil {
		return Evidence{}, newStageError(stage, err)
	}
	owner := &lifecycle{
		metadataDone: make(chan struct{}),
	}
	warmupMilliseconds := config.Definition.ExecutionPolicy.WarmupMilliseconds
	measurementMilliseconds := config.Definition.ExecutionPolicy.MeasurementMilliseconds
	cleanupMilliseconds := config.Definition.ExecutionPolicy.CleanupMilliseconds
	var rekeyInterval time.Duration
	var minimumRekeys uint64
	if config.Soak != nil {
		warmupMilliseconds = config.Soak.WarmupMilliseconds
		measurementMilliseconds = config.Soak.DurationMilliseconds
		cleanupMilliseconds = config.Soak.CleanupMilliseconds
		rekeyInterval = time.Duration(
			config.Soak.RekeyIntervalMilliseconds,
		) * time.Millisecond
		minimumRekeys = config.Soak.MinimumObservedRekeys
	}
	owner.cleanupBudget = time.Duration(cleanupMilliseconds) * time.Millisecond
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, owner.Close())
			resultErr = newStageError(stage, resultErr)
			resultErr = captureScenarioFailureState(resultErr, owner.metadata)
		}
	}()
	stage = FailureStageGateway
	networkGateway, err := gateway.New(config.GatewayConfig)
	if err != nil {
		return Evidence{}, err
	}
	owner.gateway = networkGateway
	if err := networkGateway.Start(ctx); err != nil {
		return Evidence{}, err
	}
	go owner.collectMetadata()

	stage = FailureStageAdmission
	admission, err := testclient.PrepareBattleAdmission(ctx, config.Runtime, testclient.BattleAdmissionPlan{
		MembershipCount:  int(config.Definition.ActorCount),
		BattleActorCount: int(config.Definition.ActorCount),
	})
	if err != nil {
		return Evidence{}, err
	}
	owner.admission = admission
	stage = FailureStageActorStart
	if err := owner.startActors(ctx, config); err != nil {
		return Evidence{}, err
	}
	warmup := time.Duration(warmupMilliseconds) * time.Millisecond
	stage = FailureStageWarmup
	if err := runActorPhase(
		ctx,
		owner.actors,
		correlation.PhaseClean,
		warmup,
		phaseRecoveryPolicy{},
	); err != nil {
		return Evidence{}, err
	}
	stage = FailureStageFaultActivation
	if err := networkGateway.ActivateImpairments(ctx); err != nil {
		return Evidence{}, err
	}
	measurementStart, err := networkGateway.Elapsed()
	if err != nil {
		return Evidence{}, err
	}
	startClients := snapshotActors(owner.actors)
	resetActorMeasurements(owner.actors)
	stage = FailureStageStartMetrics
	// 第一份读取只建立 measurement cursor 之后的 sampler anchor；正式起点
	// 必须使用更高 sequence，不能接受 measurement 前缓存的 control snapshot。
	startMetricsAnchor, err := scrapeAfter(ctx, config, nil)
	if err != nil {
		return Evidence{}, err
	}
	startMetrics, err := scrapeAfter(ctx, config, &startMetricsAnchor)
	if err != nil {
		return Evidence{}, err
	}
	var clientRebinds uint64
	if len(config.Definition.Impairments) == 1 &&
		config.Definition.Impairments[0] == "clean" {
		stage = FailureStageRebind
		clientRebinds, err = runRebinds(ctx, networkGateway, owner.actors)
		if err != nil {
			return Evidence{}, err
		}
	}
	measurementDuration := time.Duration(measurementMilliseconds) * time.Millisecond
	stage = FailureStageMeasurement
	clientRekeys, soakSummary, err := runMeasurement(
		ctx,
		owner.actors,
		networkGateway,
		config,
		startMetrics,
		measurementDuration,
		rekeyInterval,
	)
	if err != nil {
		return Evidence{}, err
	}
	stage = FailureStageFaultQuiesce
	if err := networkGateway.QuiesceImpairments(ctx); err != nil {
		return Evidence{}, err
	}
	if err := awaitGatewayQuiescence(
		ctx,
		networkGateway,
		owner.actors,
	); err != nil {
		return Evidence{}, err
	}
	endClients := snapshotActors(owner.actors)
	stage = FailureStagePreCloseMetrics
	preCloseMetrics, err := scrapeAfter(ctx, config, &startMetrics)
	if err != nil {
		return Evidence{}, err
	}
	// Gateway end cursor 必须位于 fault queue 收敛和 control 结束采样屏障之后，
	// 避免把同一尾部 server egress 计入 control、却排除在 gateway terminal window 之外。
	measurementEnd, err := networkGateway.Elapsed()
	if err != nil {
		return Evidence{}, err
	}
	stage = FailureStageCleanup
	cleanupContext := owner.beginCleanup()
	clientClosedSessions, err := owner.closeSessions(cleanupContext)
	if err != nil {
		return Evidence{}, err
	}
	stage = FailureStageEndMetrics
	postCloseMetrics, err := scrapeAfter(cleanupContext, config, &preCloseMetrics)
	if err != nil {
		return Evidence{}, err
	}
	window := measurement.Window{Start: startMetrics, End: preCloseMetrics}
	if err := window.Validate(); err != nil {
		return Evidence{}, err
	}
	controlRekeys := window.End.Control["rekeys"] - window.Start.Control["rekeys"]
	controlRebinds := window.End.Control["rebinds"] - window.Start.Control["rebinds"]
	requiredRekeys := minimumRekeys * uint64(config.Definition.ActorCount)
	var requiredRebinds uint64
	if len(config.Definition.Impairments) == 1 &&
		config.Definition.Impairments[0] == "clean" {
		requiredRebinds = uint64(config.Definition.ActorCount)
	}
	stage = classifyRebindEvidence(
		clientRebinds,
		controlRebinds,
		requiredRebinds,
	)
	if err := validateRebindEvidence(
		clientRebinds,
		controlRebinds,
		requiredRebinds,
	); err != nil {
		return Evidence{}, err
	}
	if requiredRekeys > 0 {
		stage = FailureStageRekeyEvidence
		if err := validateRekeyEvidence(
			clientRekeys,
			controlRekeys,
			requiredRekeys,
		); err != nil {
			return Evidence{}, err
		}
	}
	if config.Soak != nil {
		stage = FailureStageSoakBudget
		workingSetBudget, err := config.Metrics.Metric(
			manifest.ProcessWorkingSetGrowthMetricID,
		)
		if err != nil {
			return Evidence{}, err
		}
		for _, role := range []string{"go-parent", "cpp-child"} {
			startWorkingSet := window.Start.Processes[role]["working-set-bytes"]
			endWorkingSet := window.End.Processes[role]["working-set-bytes"]
			if endWorkingSet > startWorkingSet &&
				endWorkingSet-startWorkingSet > workingSetBudget.Maximum {
				return Evidence{}, errors.New("battle qualification process working set grew beyond soak budget")
			}
		}
	}
	stage = FailureStageClosureReasonEvidence
	closure, err := newClosureEvidence(
		preCloseMetrics,
		postCloseMetrics,
		clientClosedSessions,
		uint64(config.Definition.ActorCount),
	)
	if err != nil {
		return Evidence{}, err
	}
	stage = FailureStageCleanup
	if err := owner.Close(); err != nil {
		return Evidence{}, err
	}
	stage = FailureStageClientEvidence
	clientEvidence, err := clientDeltas(startClients, endClients)
	if err != nil {
		return Evidence{}, err
	}
	if slices.Contains(config.Definition.Impairments, "baseline-gap") {
		recovered := false
		for _, client := range clientEvidence {
			recovered = recovered || client.MaximumBaselineRecoveryUS != 0
		}
		if !recovered {
			return Evidence{}, errors.New("battle client baseline recovery was not observed")
		}
	}
	stage = FailureStageGatewayEvidence
	gatewayEvidence, err := summarizeGateway(
		owner.metadata,
		measurementStart,
		measurementEnd,
	)
	if err != nil {
		return Evidence{}, err
	}
	stage = FailureStageCrossCheck
	if err := crossCheck(gatewayEvidence, clientEvidence, window); err != nil {
		return Evidence{}, err
	}
	phases := make([]string, len(config.Definition.Phases))
	for index, phase := range config.Definition.Phases {
		phases[index] = string(phase)
	}
	return Evidence{
		SchemaVersion:        1,
		QualificationVersion: "battle-network-qualification-v1",
		EvidenceKind:         evidenceKind(config.Soak),
		ScenarioID:           config.Definition.ScenarioID,
		SourceScenarioID:     config.Definition.SourceScenarioID,
		WorkloadID:           config.Definition.WorkloadID,
		ActorCount:           config.Definition.ActorCount,
		Phases:               phases,
		Gateway:              gatewayEvidence,
		Clients:              clientEvidence,
		Metrics:              window,
		Transitions: TransitionEvidence{
			ClientRebinds: clientRebinds, ControlRebinds: controlRebinds,
			ClientRekeys: clientRekeys, ControlRekeys: controlRekeys,
		},
		Closure:        closure,
		SoakInvariants: soakInvariants(config.Soak),
		SoakSummary:    soakSummary,
		Disposition:    "passed",
	}, nil
}

// awaitGatewayQuiescence 在停止新 workload 后持续推进已有 client UDP/KCP 状态。
//
// fault barrier 只拥有 gateway queue；actor poll 必须并发消费已送达 ACK 并产生必要
// retransmit，否则合法 inflight route 会因观察方暂停而越过自己的 deadline。
func awaitGatewayQuiescence(
	ctx context.Context,
	networkGateway *gateway.Gateway,
	actors []*actorOwner,
) error {
	return awaitQuiescenceWhileDraining(
		ctx,
		networkGateway.AwaitQuiescence,
		func(drainContext context.Context) error {
			for _, actor := range actors {
				receipt, err := actor.supervisor.PollSession(
					drainContext,
					protocolclient.Poll{
						ClientSlot:    actor.slot,
						MaximumEvents: pollMaximumEvents,
						Wait:          pollWait,
					},
				)
				if err != nil {
					return actor.operationFailure(
						FailureStageWorkloadPoll,
						err,
					)
				}
				if err := actor.observePollReceipt(receipt); err != nil {
					return err
				}
			}
			return nil
		},
	)
}

// awaitQuiescenceWhileDraining 组合 queue barrier 与 client drain，并在 barrier 后再读一轮。
func awaitQuiescenceWhileDraining(
	ctx context.Context,
	await func(context.Context) error,
	drain func(context.Context) error,
) error {
	drainContext, cancel := context.WithCancel(ctx)
	defer cancel()
	awaited := make(chan error, 1)
	go func() {
		awaited <- await(drainContext)
	}()
	for {
		select {
		case err := <-awaited:
			if err != nil {
				return err
			}
			return drain(drainContext)
		default:
		}
		if err := drain(drainContext); err != nil {
			cancel()
			<-awaited
			return err
		}
	}
}

// classifyRebindEvidence 将低敏双源缺口收敛到稳定阶段码，不披露运行态计数。
func classifyRebindEvidence(client, control, required uint64) FailureStage {
	switch {
	case client != required:
		return FailureStageRebindClientEvidence
	case control != required:
		return FailureStageRebindControlEvidence
	default:
		return FailureStageRebindEvidence
	}
}

// validateRebindEvidence 要求触发时两源均覆盖全部 actor，未触发时两源均为零。
func validateRebindEvidence(client, control, required uint64) error {
	switch {
	case client != required:
		return newStageError(
			FailureStageRebindClientEvidence,
			errors.New("battle qualification client rebind evidence is incomplete"),
		)
	case control != required:
		return newStageError(
			FailureStageRebindControlEvidence,
			errors.New("battle qualification control rebind evidence is incomplete"),
		)
	}
	return nil
}

// validateRekeyEvidence 只对 manifest 明确要求的 rekey 数量执行双源裁决。
func validateRekeyEvidence(client, control, required uint64) error {
	if required == 0 {
		return nil
	}
	if client < required || control < required {
		return errors.New("battle qualification rekey evidence is incomplete")
	}
	return nil
}

// newClosureEvidence 归约客户端与 control 双源关闭结果，并拒绝残留或异常原因。
func newClosureEvidence(
	start measurement.Snapshot,
	end measurement.Snapshot,
	clientClosedSessions uint64,
	expectedSessions uint64,
) (ClosureEvidence, error) {
	normal, err := counterDelta(start, end, "close-normal")
	if err != nil {
		return ClosureEvidence{}, err
	}
	var unexpected uint64
	for _, metric := range []string{
		"close-authentication",
		"close-timeout",
		"close-resource",
		"close-lifecycle",
		"close-transport",
		"close-internal",
	} {
		delta, deltaErr := counterDelta(start, end, metric)
		if deltaErr != nil {
			return ClosureEvidence{}, deltaErr
		}
		if unexpected > math.MaxUint64-delta {
			return ClosureEvidence{}, errors.New("battle qualification close counters overflowed")
		}
		unexpected += delta
	}
	evidence := ClosureEvidence{
		ClientClosedSessions:          clientClosedSessions,
		ControlNormalCloseReasons:     normal,
		ControlUnexpectedCloseReasons: unexpected,
		RemainingActiveSessions:       end.Control["active-session-count"],
		RemainingInstalledTickets:     end.Control["installed-ticket-count"],
	}
	switch {
	case expectedSessions == 0:
		return ClosureEvidence{}, newStageError(
			FailureStageValidation,
			errors.New("battle qualification expected close count is invalid"),
		)
	case evidence.ClientClosedSessions != expectedSessions:
		return ClosureEvidence{}, newStageError(
			FailureStageClosureClientEvidence,
			errors.New("battle qualification client session closure is incomplete"),
		)
	case evidence.ControlNormalCloseReasons < expectedSessions ||
		evidence.ControlUnexpectedCloseReasons != 0:
		return ClosureEvidence{}, newStageError(
			FailureStageClosureReasonEvidence,
			errors.New("battle qualification close reason evidence is incomplete"),
		)
	case
		evidence.RemainingActiveSessions != 0 ||
			evidence.RemainingInstalledTickets != 0:
		return ClosureEvidence{}, newStageError(
			FailureStageClosureRuntimeEvidence,
			errors.New("battle qualification closed session runtime remained active"),
		)
	}
	return evidence, nil
}

// counterDelta 读取单个累计 control counter 的有界差值。
func counterDelta(
	start measurement.Snapshot,
	end measurement.Snapshot,
	metric string,
) (uint64, error) {
	startValue, startOK := start.Control[metric]
	endValue, endOK := end.Control[metric]
	if !startOK || !endOK || endValue < startValue {
		return 0, errors.New("battle qualification close counter is invalid")
	}
	return endValue - startValue, nil
}

// soakInvariants 返回独立副本，普通 scenario 省略该字段。
func soakInvariants(policy *manifest.SoakPolicy) []string {
	if policy == nil {
		return nil
	}
	return slices.Clone(policy.RequiredInvariants)
}

// runRebinds 先提交 gateway NAT successor，再由 child 经 authenticated control lane 确认。
func runRebinds(
	ctx context.Context,
	networkGateway *gateway.Gateway,
	actors []*actorOwner,
) (uint64, error) {
	var count uint64
	for _, actor := range actors {
		if err := actor.drainForTransition(ctx); err != nil {
			return count, err
		}
		rotation, err := networkGateway.RotateMappingEndpoint(ctx, actor.slot)
		if err != nil {
			return count, err
		}
		event, err := actor.supervisor.Transition(
			ctx,
			protocolclient.NetworkTransition{
				ClientSlot:         actor.slot,
				Operation:          protocolclient.NetworkRebind,
				AdvertisedEndpoint: rotation.Endpoint,
			},
		)
		if err != nil {
			return count, err
		}
		if !event.Committed || event.Generation != rotation.Generation {
			return count, errors.New("battle qualification NAT rebind generation drifted")
		}
		count++
	}
	return count, nil
}

// drainForTransition 在 mapping rotation 前完整消费上一 phase 的有界 UDP output。
func (actor *actorOwner) drainForTransition(ctx context.Context) error {
	for range transitionDrainBatches {
		receipt, err := actor.supervisor.PollSession(
			ctx,
			protocolclient.Poll{
				ClientSlot:    actor.slot,
				MaximumEvents: pollMaximumEvents,
				Wait:          pollWait,
			},
		)
		if err != nil {
			return actor.operationFailure(
				FailureStageWorkloadPoll,
				err,
			)
		}
		if err := actor.observePollReceipt(receipt); err != nil {
			return err
		}
		if receipt.EventCount < pollMaximumEvents {
			return nil
		}
	}
	return newStageError(
		FailureStageRebind,
		errors.New("battle transition output drain exceeded hard limit"),
	)
}

// validate 在 listener、child 或 HTTP operation 前拒绝不完整依赖与非 loopback metrics。
func (config Config) validate() error {
	if config.Runtime == nil || config.Runtime.HTTP == nil ||
		config.Runtime.TLSConfig == nil ||
		config.DiagnosticClient == nil ||
		config.Definition.ActorCount == 0 ||
		config.GatewayConfig.ClientCount != config.Definition.ActorCount ||
		config.Definition.ExecutionPolicy.WarmupMilliseconds <= 0 ||
		config.Definition.ExecutionPolicy.MeasurementMilliseconds <= 0 ||
		config.Definition.ExecutionPolicy.CleanupMilliseconds <= 0 ||
		len(config.Definition.Phases) == 0 {
		return errors.New("battle qualification runner config is incomplete")
	}
	if _, err := config.Metrics.Metric(manifest.ProcessWorkingSetGrowthMetricID); err != nil {
		return err
	}
	if config.Soak != nil &&
		(config.Soak.DurationMilliseconds <= 0 ||
			config.Soak.WarmupMilliseconds <= 0 ||
			config.Soak.RekeyIntervalMilliseconds <= 0 ||
			config.Soak.MinimumObservedRekeys < 2 ||
			config.Soak.CleanupMilliseconds <= 0) {
		return errors.New("battle qualification soak policy is invalid")
	}
	parsed, err := url.Parse(config.DiagnosticURL)
	if err != nil || parsed.Scheme != "http" || parsed.Path != "/metrics" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("battle qualification diagnostic URL is invalid")
	}
	host, _, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return errors.New("battle qualification diagnostic authority is invalid")
	}
	address, err := netip.ParseAddr(host)
	if err != nil || !address.IsLoopback() {
		return errors.New("battle qualification diagnostic listener is not loopback")
	}
	return config.ProtocolClientConfig.Validate()
}

// evidenceKind 返回普通 fault 与 mandatory soak 的 closed evidence discriminator。
func evidenceKind(policy *manifest.SoakPolicy) string {
	if policy == nil {
		return "scenario-run-evidence"
	}
	return "soak-run-evidence"
}

// startActors 校验 advertised endpoint、登记 public ticket identity 并完成真实 handshake。
func (owner *lifecycle) startActors(ctx context.Context, config Config) error {
	endpoint, err := owner.gateway.FrontendEndpoint(1)
	if err != nil {
		return err
	}
	for _, participant := range owner.admission.Participants {
		actor, err := owner.startParticipant(ctx, config, endpoint, participant)
		if err != nil {
			return err
		}
		owner.actors = append(owner.actors, actor)
	}
	if len(owner.actors) != int(config.Definition.ActorCount) {
		return errors.New("battle admission actor count drifted")
	}
	return nil
}

// startParticipant 转移一个公开 BattleTicket 并建立唯一 child/session/driver owner。
func (owner *lifecycle) startParticipant(
	ctx context.Context,
	config Config,
	endpoint netip.AddrPort,
	participant testclient.BattleParticipant,
) (*actorOwner, error) {
	if participant.Slot <= 0 || participant.Slot > math.MaxUint8 ||
		participant.Ticket == nil {
		return nil, errors.New("battle admission participant is invalid")
	}
	ticketAddress, err := netip.ParseAddr(participant.Ticket.Endpoint.Host)
	if err != nil {
		return nil, errors.New("BattleTicket endpoint is not numeric")
	}
	ticketEndpoint := netip.AddrPortFrom(
		ticketAddress,
		participant.Ticket.Endpoint.Port,
	)
	if ticketEndpoint != endpoint {
		return nil, errors.New("BattleTicket advertised endpoint drifted from gateway")
	}
	credential, err := participant.Ticket.TakeCredential()
	if err != nil {
		return nil, err
	}
	if err := owner.gateway.RegisterTicket(
		ctx,
		uint8(participant.Slot),
		credential.TicketID,
	); err != nil {
		credential.Clear()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		credential.Clear()
		return nil, err
	}
	// Child 生命周期由 Supervisor 独占；此处 ctx 只约束启动 handshake。
	child, err := protocolclient.Start(ctx, config.ProtocolClientConfig)
	if err != nil {
		credential.Clear()
		return nil, err
	}
	actor := &actorOwner{
		slot: uint8(participant.Slot), supervisor: child, sessionActive: true,
	}
	sessionCredential := &protocolclient.SessionCredential{
		TicketID:     credential.TicketID,
		TicketSecret: credential.TicketSecret,
	}
	credential.Clear()
	if participant.Ticket.ExpiresAtMS <= 0 {
		sessionCredential.Clear()
		_ = child.Close(ctx)
		return nil, errors.New("BattleTicket expiry is invalid")
	}
	if _, err := child.StartSession(ctx, protocolclient.SessionStart{
		ClientSlot:            actor.slot,
		Endpoint:              endpoint,
		Credential:            sessionCredential,
		TicketExpiresAtUnixMS: uint64(participant.Ticket.ExpiresAtMS),
	}); err != nil {
		_ = child.Close(ctx)
		return nil, err
	}
	driver, err := workload.New(correlation.PhaseClean)
	if err != nil {
		_ = child.Close(ctx)
		return nil, err
	}
	actor.driver = driver
	return actor, nil
}

// collectMetadata 持续 drain gateway evidence，防止观察方反压改变被测系统。
func (owner *lifecycle) collectMetadata() {
	defer close(owner.metadataDone)
	for metadata := range owner.gateway.Evidence() {
		owner.metadata = append(owner.metadata, metadata)
	}
}

// Close 按 child session、child process、gateway、admission 的逆序在独立 deadline 内回收。
func (owner *lifecycle) Close() error {
	owner.closeOnce.Do(func() {
		cleanupContext := owner.beginCleanup()
		defer owner.cleanupCancel()
		var cleanupErrors []error
		if _, err := owner.closeSessions(cleanupContext); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
		for index := len(owner.actors) - 1; index >= 0; index-- {
			actor := owner.actors[index]
			if err := actor.supervisor.Close(cleanupContext); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		if owner.gateway != nil {
			if err := owner.gateway.Close(cleanupContext); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
			if owner.metadataDone != nil {
				select {
				case <-owner.metadataDone:
				case <-cleanupContext.Done():
					cleanupErrors = append(cleanupErrors, cleanupContext.Err())
				}
			}
		}
		if owner.admission != nil {
			if err := owner.admission.Close(); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		owner.closeErr = errors.Join(cleanupErrors...)
	})
	return owner.closeErr
}

// beginCleanup 创建一次独立于场景 deadline 的共享回收预算。
//
// authenticated close、关闭后采样与逆序资源释放必须消费同一个 deadline，
// 既不能复用已接近耗尽的 measurement context，也不能为每个阶段重置预算。
func (owner *lifecycle) beginCleanup() context.Context {
	owner.cleanupOnce.Do(func() {
		owner.cleanupCtx, owner.cleanupCancel = context.WithTimeout(
			context.Background(),
			owner.cleanupBudget,
		)
	})
	return owner.cleanupCtx
}

// closeSessions 按逆序完成 authenticated close，并让后续 cleanup 保持幂等。
func (owner *lifecycle) closeSessions(ctx context.Context) (uint64, error) {
	var closed uint64
	var closeErrors []error
	for index := len(owner.actors) - 1; index >= 0; index-- {
		actor := owner.actors[index]
		if !actor.sessionActive {
			continue
		}
		event, err := actor.supervisor.Transition(
			ctx,
			protocolclient.NetworkTransition{
				ClientSlot: actor.slot,
				Operation:  protocolclient.NetworkClose,
			},
		)
		if err != nil {
			if errors.Is(err, protocolclient.ErrClosed) {
				actor.sessionActive = false
				continue
			}
			closeErrors = append(closeErrors, err)
			continue
		}
		if !event.Committed || event.Generation != 0 {
			closeErrors = append(
				closeErrors,
				errors.New("battle qualification authenticated close was not committed"),
			)
			continue
		}
		actor.sessionActive = false
		closed++
	}
	return closed, errors.Join(closeErrors...)
}

// runMeasurement 驱动全部 phase，并在 soak 模式按 tracked cadence 完成 authenticated rekey。
func runMeasurement(
	ctx context.Context,
	actors []*actorOwner,
	networkGateway *gateway.Gateway,
	config Config,
	startMetrics measurement.Snapshot,
	duration time.Duration,
	rekeyInterval time.Duration,
) (uint64, *measurement.SoakSummary, error) {
	measurementContext, cancel := context.WithCancel(ctx)
	defer cancel()
	rekeyResults := make(chan struct {
		count uint64
		err   error
	}, 1)
	if rekeyInterval > 0 {
		go func() {
			count, err := runRekeys(measurementContext, actors, rekeyInterval)
			rekeyResults <- struct {
				count uint64
				err   error
			}{count: count, err: err}
		}()
	} else {
		rekeyResults <- struct {
			count uint64
			err   error
		}{}
	}
	sampleResults := make(chan struct {
		summary *measurement.SoakSummary
		err     error
	}, 1)
	if config.Soak != nil {
		go func() {
			summary, err := collectSoakSamples(
				measurementContext,
				config,
				startMetrics,
				duration,
			)
			sampleResults <- struct {
				summary *measurement.SoakSummary
				err     error
			}{summary: summary, err: err}
		}()
	} else {
		sampleResults <- struct {
			summary *measurement.SoakSummary
			err     error
		}{}
	}
	phaseErr := runMeasurementPhases(
		measurementContext,
		actors,
		networkGateway,
		config.Definition,
		duration,
	)
	cancel()
	rekeyResult := <-rekeyResults
	sampleResult := <-sampleResults
	return rekeyResult.count, sampleResult.summary, errors.Join(
		phaseErr,
		rekeyResult.err,
		sampleResult.err,
	)
}

// collectSoakSamples 按 manifest cadence 连续验证相邻四源 snapshot，并只保留有界 summary。
func collectSoakSamples(
	ctx context.Context,
	config Config,
	start measurement.Snapshot,
	duration time.Duration,
) (*measurement.SoakSummary, error) {
	interval := time.Duration(config.Soak.SampleIntervalMilliseconds) * time.Millisecond
	if interval <= 0 || duration <= interval {
		return nil, errors.New("battle qualification soak sample cadence is invalid")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	previous := start
	summary := &measurement.SoakSummary{}
	for {
		select {
		case <-ctx.Done():
			minimumSamples := uint64(duration/interval) - 1
			if summary.SampleCount < minimumSamples ||
				!summary.MonotonicCountersPassed {
				return nil, errors.New("battle qualification soak sample coverage is incomplete")
			}
			return summary, nil
		case <-ticker.C:
			current, err := scrapeAfter(ctx, config, &previous)
			if err != nil {
				if ctx.Err() != nil {
					continue
				}
				return nil, err
			}
			if err := summary.ObserveSoakSample(previous, current); err != nil {
				return nil, err
			}
			previous = current
		}
	}
}

// runRekeys 在 context 结束前串行推进全部 active session 的 key epoch。
func runRekeys(
	ctx context.Context,
	actors []*actorOwner,
	interval time.Duration,
) (uint64, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var count uint64
	for {
		select {
		case <-ctx.Done():
			return count, nil
		case <-ticker.C:
			for _, actor := range actors {
				event, err := actor.supervisor.Transition(
					ctx,
					protocolclient.NetworkTransition{
						ClientSlot: actor.slot,
						Operation:  protocolclient.NetworkRekey,
					},
				)
				if err != nil {
					return count, err
				}
				if !event.Committed || event.Generation == 0 {
					return count, errors.New("battle qualification rekey was not committed")
				}
				count++
			}
		}
	}
}

// runMeasurementPhases 平均分配 frozen window，并把余数交给最后一个 phase。
func runMeasurementPhases(
	ctx context.Context,
	actors []*actorOwner,
	networkGateway *gateway.Gateway,
	definition manifest.Definition,
	duration time.Duration,
) error {
	phases := definition.Phases
	perPhase := duration / time.Duration(len(phases))
	remaining := duration
	recovery := phaseRecoveryPolicy{
		baselineGapDriven: slices.Contains(
			definition.Impairments,
			"baseline-gap",
		),
		periodicKCP: slices.Contains(
			definition.Impairments,
			"kcp-retransmit",
		),
	}
	for index, phase := range phases {
		phaseDuration := perPhase
		if index == len(phases)-1 {
			phaseDuration = remaining
		}
		if phaseDuration <= 0 {
			return errors.New("battle qualification phase duration is invalid")
		}
		if index > 0 {
			seed, err := definition.FaultPatternSeed(phase)
			if err != nil {
				return err
			}
			if err := networkGateway.RestartImpairmentPattern(ctx, seed); err != nil {
				return err
			}
		}
		if err := runActorPhase(ctx, actors, phase, phaseDuration, recovery); err != nil {
			return err
		}
		remaining -= phaseDuration
	}
	return nil
}

// runActorPhase 让每个 child 拥有独立 cadence goroutine，首个失败取消全部 peers。
func runActorPhase(
	ctx context.Context,
	actors []*actorOwner,
	phase correlation.WorkloadPhase,
	duration time.Duration,
	recovery phaseRecoveryPolicy,
) error {
	phaseContext, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(duration, cancel)
	defer cancel()
	defer timer.Stop()
	results := make(chan error, len(actors))
	for _, actor := range actors {
		if err := actor.driver.Transition(phase); err != nil {
			return newStageError(FailureStageWorkloadTransition, err)
		}
		go func(current *actorOwner) {
			results <- current.runPhase(phaseContext, ctx, recovery)
		}(actor)
	}
	var result error
	for range actors {
		err := <-results
		if err != nil && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) && result == nil {
			result = err
			cancel()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return result
}

// runPhase 以 40 Hz 发送 typed intent，并在同一 owner 中串行 poll。
func (actor *actorOwner) runPhase(
	phaseContext context.Context,
	operationContext context.Context,
	recovery phaseRecoveryPolicy,
) error {
	ticker := time.NewTicker(workload.InputCadence)
	defer ticker.Stop()
	for {
		select {
		case <-phaseContext.Done():
			return phaseContext.Err()
		case <-ticker.C:
			actor.state.step++
			bundle, bundleErr := actor.driver.NextInput(
				actor.state.latestServerTick,
			)
			if bundleErr != nil {
				return newStageError(
					FailureStageWorkloadSend,
					bundleErr,
				)
			}
			if bundle != nil {
				commands := bundle.GetCommands()
				command := commands[len(commands)-1]
				valueA, valueB := commandValues(command)
				event, err := actor.supervisor.SendWorkload(operationContext, protocolclient.WorkloadCommand{
					ClientSlot:          actor.slot,
					Operation:           protocolclient.WorkloadInputBundle,
					CommandKind:         uint8(command.GetKind()),
					RepeatCount:         1,
					ApplicationSequence: command.GetCommandSequence(),
					ApplicationTick:     bundle.GetNewestInputTick(),
					ValueA:              valueA,
					ValueB:              valueB,
				})
				if err != nil {
					return actor.operationFailure(FailureStageWorkloadSend, err)
				}
				if event.ClientSlot != actor.slot ||
					event.Kind != protocolclient.WorkloadDatagramSent {
					return newStageError(
						FailureStageWorkloadReceipt,
						errors.New("battle workload send receipt drifted"),
					)
				}
				actor.state.sentDatagrams += uint64(event.Count)
			}
			if actor.state.step%probeCadenceSteps == 0 &&
				actor.state.latestSnapshotSequence != 0 {
				probe := actor.driver.NextProbe(workload.Snapshot{
					MappingGeneration:      1,
					LatestServerTick:       actor.state.latestServerTick,
					LatestSnapshotSequence: actor.state.latestSnapshotSequence,
					LastProcessedInputTick: actor.state.lastProcessedInputTick,
				}, uint64(actor.state.step)*uint64(workload.InputCadence/time.Microsecond))
				event, err := actor.supervisor.SendWorkload(operationContext, protocolclient.WorkloadCommand{
					ClientSlot:          actor.slot,
					Operation:           protocolclient.WorkloadProbe,
					RepeatCount:         1,
					ApplicationSequence: probe.GetProbeSequence(),
					ApplicationTick:     probe.GetLatestSnapshotSequence(),
				})
				if err != nil {
					return actor.operationFailure(FailureStageWorkloadProbe, err)
				}
				actor.state.sentDatagrams += uint64(event.Count)
			}
			receipt, err := actor.supervisor.PollSession(operationContext, protocolclient.Poll{
				ClientSlot: actor.slot, MaximumEvents: pollMaximumEvents, Wait: pollWait,
			})
			if err != nil {
				return actor.operationFailure(FailureStageWorkloadPoll, err)
			}
			if err := actor.observePollReceipt(receipt); err != nil {
				return err
			}
			if trigger := actor.nextResyncTrigger(
				recovery,
				time.Now(),
			); trigger != resyncTriggerNone {
				if err := actor.sendResync(operationContext, trigger); err != nil {
					return actor.operationFailure(FailureStageWorkloadResync, err)
				}
			}
		}
	}
}

// nextResyncTrigger 只允许登记原因产生 KCP 消息，并按单调时间共享 profile 的 2/s 限流。
func (actor *actorOwner) nextResyncTrigger(
	policy phaseRecoveryPolicy,
	observedAt time.Time,
) resyncTrigger {
	if actor.state.latestServerTick == 0 ||
		(!actor.state.lastResyncAt.IsZero() &&
			observedAt.Sub(actor.state.lastResyncAt) < resyncCadence) {
		return resyncTriggerNone
	}
	gapPending := policy.baselineGapDriven &&
		(!actor.state.recoveryStartedAt.IsZero() ||
			actor.state.baselineGapCount > actor.state.handledBaselineGapCount)
	gapSeedRequired := policy.baselineGapDriven &&
		actor.state.resyncSequence == 0
	periodicDue := policy.periodicKCP
	switch {
	case gapPending:
		return resyncTriggerGapRecovery
	case gapSeedRequired:
		return resyncTriggerGapSeed
	case periodicDue:
		return resyncTriggerPeriodic
	default:
		return resyncTriggerNone
	}
}

// sendResync 产生严格递增 application identity，并按触发原因提交计时与限流状态。
func (actor *actorOwner) sendResync(
	ctx context.Context,
	trigger resyncTrigger,
) error {
	if trigger == resyncTriggerNone {
		return errors.New("battle qualification resync trigger is missing")
	}
	if actor.state.resyncSequence == math.MaxUint64 {
		return errors.New("battle qualification resync sequence exhausted")
	}
	sentAt := time.Now()
	nextSequence := actor.state.resyncSequence + 1
	event, err := actor.supervisor.SendWorkload(ctx, protocolclient.WorkloadCommand{
		ClientSlot:          actor.slot,
		Operation:           protocolclient.WorkloadResyncRequest,
		RepeatCount:         1,
		ApplicationSequence: nextSequence,
		ApplicationTick:     actor.state.latestServerTick,
		ValueA: int32(
			battlev1.BattleResyncReason_BATTLE_RESYNC_REASON_DELTA_GAP,
		),
	})
	if err != nil {
		return err
	}
	if event.Kind != protocolclient.WorkloadDatagramSent &&
		event.Kind != protocolclient.WorkloadReliableQueued {
		return errors.New("battle qualification resync receipt drifted")
	}
	actor.state.resyncSequence = nextSequence
	actor.commitResyncTrigger(trigger, sentAt)
	actor.state.sentDatagrams += uint64(event.Count)
	return nil
}

// commitResyncTrigger 只在 client 已确认排队后提交限流与 recovery 测量状态。
func (actor *actorOwner) commitResyncTrigger(
	trigger resyncTrigger,
	sentAt time.Time,
) {
	if trigger == resyncTriggerGapRecovery &&
		actor.state.recoveryStartedAt.IsZero() {
		actor.state.recoveryStartedAt = sentAt
		actor.state.recoveryBaselineID = actor.state.baselineID
	}
	actor.state.lastResyncAt = sentAt
	actor.state.handledBaselineGapCount = actor.state.baselineGapCount
}

// observePollReceipt 单点维护 actor cumulative state 与 measurement high-watermark。
func (actor *actorOwner) observePollReceipt(
	receipt protocolclient.PollReceipt,
) error {
	if receipt.ClientSlot != actor.slot ||
		receipt.SnapshotCount < actor.state.snapshotCount ||
		receipt.ReliableCount < actor.state.reliableCount ||
		receipt.BaselineGapCount < actor.state.baselineGapCount ||
		receipt.LatestServerTick < actor.state.latestServerTick ||
		receipt.LatestSnapshotSequence < actor.state.latestSnapshotSequence ||
		receipt.LastProcessedInputTick < actor.state.lastProcessedInputTick {
		return newStageError(
			FailureStageWorkloadReceipt,
			errors.New("battle client cumulative receipt regressed"),
		)
	}
	previousBaselineID := actor.state.baselineID
	previousBaselineGapCount := actor.state.baselineGapCount
	if receipt.SnapshotCount > actor.state.snapshotCount {
		now := time.Now()
		if !actor.state.lastSnapshotAt.IsZero() {
			gap := uint64(
				now.Sub(actor.state.lastSnapshotAt) /
					time.Microsecond,
			)
			if gap > actor.state.maximumSnapshotGapUS {
				actor.state.maximumSnapshotGapUS = gap
			}
		}
		actor.state.lastSnapshotAt = now
	}
	if !actor.state.recoveryStartedAt.IsZero() &&
		receipt.BaselineID != 0 &&
		receipt.BaselineID != actor.state.recoveryBaselineID {
		elapsed := uint64(
			time.Since(actor.state.recoveryStartedAt) /
				time.Microsecond,
		)
		if elapsed > actor.state.maximumBaselineRecoveryUS {
			actor.state.maximumBaselineRecoveryUS = elapsed
		}
		actor.state.recoveryStartedAt = time.Time{}
		actor.state.recoveryBaselineID = 0
		actor.state.handledBaselineGapCount = receipt.BaselineGapCount
	} else if actor.state.recoveryStartedAt.IsZero() &&
		receipt.BaselineGapCount > previousBaselineGapCount &&
		receipt.BaselineID != 0 &&
		receipt.BaselineID != previousBaselineID {
		// PollReceipt 是一个累计终态。如果同一批事件既报告 gap 增长又建立了
		// successor baseline，该 gap 已在 client 内恢复，不能在 poll 返回后
		// 再启动一次等待“下一个 baseline”的过期 recovery 测量。
		actor.state.handledBaselineGapCount = receipt.BaselineGapCount
	}
	actor.state.snapshotCount = receipt.SnapshotCount
	actor.state.reliableCount = receipt.ReliableCount
	actor.state.latestServerTick = receipt.LatestServerTick
	actor.state.latestApplicationSequence =
		receipt.LatestApplicationSequence
	actor.state.latestSnapshotSequence =
		receipt.LatestSnapshotSequence
	actor.state.baselineID = receipt.BaselineID
	actor.state.lastProcessedInputTick =
		receipt.LastProcessedInputTick
	actor.state.baselineGapCount = receipt.BaselineGapCount
	return nil
}

// operationFailure 使用 child stderr 的有无区分协议操作失败与独立进程异常。
//
// stderr 内容始终由 supervisor 丢弃；这里只发布一个封闭、低敏分类。
func (actor *actorOwner) operationFailure(stage FailureStage, cause error) error {
	var classified interface {
		QualificationFailureCode() string
	}
	if errors.As(cause, &classified) {
		return cause
	}
	if actor.supervisor.HadStderr() {
		return newStageError(FailureStageClientProcess, cause)
	}
	return newStageError(stage, cause)
}

// commandValues 映射 protobuf typed intent，不允许调用方写入权威字段。
func commandValues(command *battlev1.BattleInputCommand) (int32, int32) {
	switch command.GetKind() {
	case battlev1.BattleInputKind_BATTLE_INPUT_KIND_MOVE:
		return command.GetMoveXMilli(), command.GetMoveYMilli()
	case battlev1.BattleInputKind_BATTLE_INPUT_KIND_AIM:
		return command.GetAimYawMillidegrees(), command.GetAimPitchMillidegrees()
	case battlev1.BattleInputKind_BATTLE_INPUT_KIND_INTERACT:
		return int32(command.GetInteractionSlot()), 0
	default:
		return 0, 0
	}
}

// scrapeAfter 等待 periodic sampler 的完整 sample；minimum 非空时必须严格更新全部 source。
func scrapeAfter(
	ctx context.Context,
	config Config,
	minimum *measurement.Snapshot,
) (measurement.Snapshot, error) {
	ticker := time.NewTicker(metricsRetryInterval)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.DiagnosticURL, nil)
		if err != nil {
			return measurement.Snapshot{}, err
		}
		response, err := config.DiagnosticClient.Do(request)
		if err == nil && response.StatusCode == http.StatusOK {
			snapshot, parseErr := measurement.ParsePrometheus(
				http.MaxBytesReader(nil, response.Body, diagnosticBodyBytes),
			)
			_ = response.Body.Close()
			if parseErr == nil && newerMetricsSnapshot(snapshot, minimum) {
				return snapshot, nil
			}
		} else if response != nil {
			_ = response.Body.Close()
		}
		select {
		case <-ctx.Done():
			return measurement.Snapshot{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// newerMetricsSnapshot 比较 control 与两个 process owner 的独立 sample sequence。
func newerMetricsSnapshot(
	candidate measurement.Snapshot,
	minimum *measurement.Snapshot,
) bool {
	if minimum == nil {
		return true
	}
	if candidate.Control["sample-sequence"] <= minimum.Control["sample-sequence"] {
		return false
	}
	for _, role := range []string{"go-parent", "cpp-child"} {
		if candidate.Processes[role]["sequence"] <=
			minimum.Processes[role]["sequence"] {
			return false
		}
	}
	return true
}

// snapshotActors 在所有 phase goroutine join 后复制累计 client state。
func snapshotActors(actors []*actorOwner) []actorState {
	states := make([]actorState, len(actors))
	for index, actor := range actors {
		states[index] = actor.state
	}
	return states
}

// resetActorMeasurements 在 warmup 结束后保留协议累计值但重置窗口 high-watermark。
func resetActorMeasurements(actors []*actorOwner) {
	now := time.Now()
	for _, actor := range actors {
		actor.state.maximumSnapshotGapUS = 0
		actor.state.maximumBaselineRecoveryUS = 0
		actor.state.lastSnapshotAt = now
		actor.state.recoveryStartedAt = time.Time{}
		actor.state.recoveryBaselineID = 0
	}
}

// clientDeltas 计算 measurement window 差值并拒绝无 snapshot 或 Tick 停滞。
func clientDeltas(start, end []actorState) ([]ClientEvidence, error) {
	if len(start) == 0 || len(start) != len(end) {
		return nil, newStageError(
			FailureStageClientWindow,
			errors.New("battle client measurement window is incomplete"),
		)
	}
	result := make([]ClientEvidence, len(start))
	for index := range start {
		if end[index].sentDatagrams < start[index].sentDatagrams ||
			end[index].reliableCount < start[index].reliableCount {
			return nil, newStageError(
				FailureStageClientCounters,
				errors.New("battle client cumulative counters regressed"),
			)
		}
		if end[index].snapshotCount <= start[index].snapshotCount {
			return nil, newStageError(
				FailureStageClientSnapshot,
				errors.New("battle client snapshot count did not advance"),
			)
		}
		if end[index].latestServerTick <= start[index].latestServerTick {
			return nil, newStageError(
				FailureStageClientTick,
				errors.New("battle client server tick did not advance"),
			)
		}
		result[index] = ClientEvidence{
			ClientSlot:                uint8(index + 1),
			SentDatagrams:             end[index].sentDatagrams - start[index].sentDatagrams,
			SnapshotCount:             end[index].snapshotCount - start[index].snapshotCount,
			ReliableCount:             end[index].reliableCount - start[index].reliableCount,
			LatestServerTick:          end[index].latestServerTick,
			LatestApplicationSequence: end[index].latestApplicationSequence,
			LatestSnapshotSequence:    end[index].latestSnapshotSequence,
			BaselineID:                end[index].baselineID,
			LastProcessedInputTick:    end[index].lastProcessedInputTick,
			MaximumSnapshotGapUS:      end[index].maximumSnapshotGapUS,
			MaximumBaselineRecoveryUS: end[index].maximumBaselineRecoveryUS,
		}
		if result[index].LatestSnapshotSequence == 0 {
			return nil, newStageError(
				FailureStageClientSnapshot,
				errors.New("battle client snapshot identity is unavailable"),
			)
		}
		if result[index].BaselineID == 0 {
			return nil, newStageError(
				FailureStageClientBaseline,
				errors.New("battle client baseline identity is unavailable"),
			)
		}
		if result[index].LastProcessedInputTick == 0 {
			return nil, newStageError(
				FailureStageClientInputAck,
				errors.New("battle client input acknowledgement is unavailable"),
			)
		}
		if result[index].MaximumSnapshotGapUS == 0 {
			return nil, newStageError(
				FailureStageClientSnapshotGap,
				errors.New("battle client snapshot gap is unavailable"),
			)
		}
	}
	return result, nil
}

// summarizeGateway 聚合 measurement 边界内的 packet terminal evidence。
func summarizeGateway(
	metadata []gateway.Metadata,
	start time.Duration,
	end time.Duration,
) (GatewayEvidence, error) {
	type key struct {
		direction   gateway.Direction
		clientSlot  uint8
		packetKind  gateway.PublicPacketKind
		disposition gateway.Disposition
	}
	aggregates := make(map[key]PacketEvidence)
	var maximumScheduledAge uint64
	var maximumDeliveryAge uint64
	var err error
	for _, item := range metadata {
		if item.ReceivedAt < start || item.ReceivedAt >= end {
			continue
		}
		if item.LengthBytes <= 0 || item.DueAt < item.ReceivedAt {
			return GatewayEvidence{}, errors.New("gateway evidence is invalid")
		}
		if item.Disposition == gateway.DispositionDelivered {
			if item.DeliveredAt < item.ReceivedAt {
				return GatewayEvidence{}, errors.New("gateway delivery evidence is invalid")
			}
			deliveryAge := uint64(
				(item.DeliveredAt - item.ReceivedAt) /
					time.Microsecond,
			)
			if deliveryAge > maximumDeliveryAge {
				maximumDeliveryAge = deliveryAge
			}
		} else if item.DeliveredAt != 0 {
			return GatewayEvidence{}, errors.New("gateway terminal evidence has delivery time")
		}
		currentKey := key{
			direction: item.Direction, clientSlot: item.ClientSlot,
			packetKind: item.PacketKind, disposition: item.Disposition,
		}
		aggregate := aggregates[currentKey]
		aggregate.Direction = item.Direction.String()
		aggregate.ClientSlot = item.ClientSlot
		aggregate.PacketKind = item.PacketKind.String()
		aggregate.Disposition = item.Disposition.String()
		aggregate.Count, err = addEvidenceCounter(
			aggregate.Count,
			1,
		)
		if err != nil {
			return GatewayEvidence{}, err
		}
		aggregate.Bytes, err = addEvidenceCounter(
			aggregate.Bytes,
			uint64(item.LengthBytes),
		)
		if err != nil {
			return GatewayEvidence{}, err
		}
		aggregates[currentKey] = aggregate
		age := uint64((item.DueAt - item.ReceivedAt) / time.Microsecond)
		if age > maximumScheduledAge {
			maximumScheduledAge = age
		}
	}
	result := GatewayEvidence{
		Packets:               make([]PacketEvidence, 0, len(aggregates)),
		MaximumScheduledAgeUS: maximumScheduledAge,
		MaximumDeliveryAgeUS:  maximumDeliveryAge,
	}
	for _, aggregate := range aggregates {
		result.Packets = append(result.Packets, aggregate)
	}
	slices.SortFunc(result.Packets, func(left, right PacketEvidence) int {
		leftKey := fmt.Sprintf(
			"%s/%02d/%s/%s",
			left.Direction, left.ClientSlot, left.PacketKind, left.Disposition,
		)
		rightKey := fmt.Sprintf(
			"%s/%02d/%s/%s",
			right.Direction, right.ClientSlot, right.PacketKind, right.Disposition,
		)
		if leftKey < rightKey {
			return -1
		}
		if leftKey > rightKey {
			return 1
		}
		return 0
	})
	if len(result.Packets) == 0 {
		return GatewayEvidence{}, errors.New("gateway measurement evidence is empty")
	}
	return result, nil
}

// addEvidenceCounter 拒绝不可信观测累计值发生静默回绕。
func addEvidenceCounter(left, right uint64) (uint64, error) {
	if left > math.MaxUint64-right {
		return 0, errors.New("battle qualification evidence counter overflowed")
	}
	return left + right, nil
}

// crossCheck 证明四源在同一窗口均观测到合法双向流量与真实 simulation 推进。
func crossCheck(
	gatewayEvidence GatewayEvidence,
	clients []ClientEvidence,
	window measurement.Window,
) error {
	var deliveredUplink, deliveredDownlink, observedDownlink uint64
	var err error
	for _, packet := range gatewayEvidence.Packets {
		switch packet.Direction {
		case gateway.DirectionUplink.String():
			if packet.Disposition == gateway.DispositionDelivered.String() {
				deliveredUplink, err = addEvidenceCounter(
					deliveredUplink,
					packet.Count,
				)
				if err != nil {
					return err
				}
			}
		case gateway.DirectionDownlink.String():
			observedDownlink, err = addEvidenceCounter(
				observedDownlink,
				packet.Count,
			)
			if err != nil {
				return err
			}
			if packet.Disposition == gateway.DispositionDelivered.String() {
				deliveredDownlink, err = addEvidenceCounter(
					deliveredDownlink,
					packet.Count,
				)
				if err != nil {
					return err
				}
			}
		}
	}
	if deliveredUplink == 0 || deliveredDownlink == 0 ||
		len(clients) == 0 {
		return newStageError(
			FailureStageCrossCheckDirection,
			errors.New("battle qualification gateway direction coverage is incomplete"),
		)
	}
	control := window.End.Control
	startControl := window.Start.Control
	ingressDelta := control["raw-ingress-packets"] + control["kcp-ingress-packets"] -
		startControl["raw-ingress-packets"] - startControl["kcp-ingress-packets"]
	egressDelta := control["raw-egress-packets"] + control["kcp-egress-packets"] -
		startControl["raw-egress-packets"] - startControl["kcp-egress-packets"]
	if ingressDelta == 0 || egressDelta == 0 {
		return newStageError(
			FailureStageCrossCheckProgress,
			errors.New("battle qualification control packet counters did not advance"),
		)
	}
	if deliveredUplink < ingressDelta {
		return newStageError(
			FailureStageCrossCheckUplink,
			errors.New("battle qualification uplink packet conservation failed"),
		)
	}
	if observedDownlink < egressDelta {
		return newStageError(
			FailureStageCrossCheckDownlink,
			errors.New("battle qualification downlink packet conservation failed"),
		)
	}
	return nil
}
