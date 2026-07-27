package simulationcontrol

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// ErrProbeBusy 表示 lifecycle request 正在占用唯一 control turn；本轮 idle probe 应跳过而非判定 node 失效。
var ErrProbeBusy = errors.New("simulation node probe skipped while control is busy")

// ControlSession 是 controller 唯一需要的私有 request/receipt 端口。
type ControlSession interface {
	// Call 执行一个 serialized request turn。
	Call(context.Context, RequestID, string, any, string) (json.RawMessage, error)
	// Send 执行无需 receipt 的单向 frame。
	Send(context.Context, RequestID, string, any) error
	// Close 终止 session pipes。
	Close() error
	// Done 在 terminal failure 后关闭。
	Done() <-chan struct{}
	// Err 返回 terminal failure。
	Err() error
}

// ControllerConfig 固定一个 Go-owned C++ child 的完整 registration。
type ControllerConfig struct {
	// NodeID 是本 child incarnation identity。
	NodeID SimulationNodeID
	// RuntimeNodeID 必须匹配 placement target node。
	RuntimeNodeID placement.RuntimeNodeID
	// Build 绑定 B0.3 qualification。
	Build BuildBinding
	// Capacity 是 Go 配置的 hard upper bounds。
	Capacity NodeCapacity
	// ConfigIdentity 绑定 control-baseline-v1 runtime config。
	ConfigIdentity Digest
	// NavigationIdentity 绑定 nav asset/config。
	NavigationIdentity Digest
	// PhysicsIdentity 绑定 physics adapter/config。
	PhysicsIdentity Digest
	// DrainDeadline 是传给 child 的 bounded drain budget。
	DrainDeadline time.Duration
	// StopDeadline 是传给 child 的 bounded stop budget。
	StopDeadline time.Duration
	// QualificationRunID 非空时绑定唯一允许读取 snapshot 的资格 run。
	QualificationRunID QualificationRunID
	// QualificationMode 必须由资格 Composition Root 显式启用。
	QualificationMode bool
}

// Validate 拒绝 incomplete、超资格或未绑定 deadline 的 controller 配置。
func (config ControllerConfig) Validate() error {
	if !config.NodeID.Valid() || !config.RuntimeNodeID.Valid() ||
		config.Build.Validate() != nil || config.Capacity.Validate() != nil ||
		!config.ConfigIdentity.Valid() || !config.NavigationIdentity.Valid() ||
		!config.PhysicsIdentity.Valid() || config.DrainDeadline <= 0 ||
		config.StopDeadline <= 0 ||
		config.QualificationMode !=
			(config.QualificationRunID != "") ||
		(config.QualificationMode &&
			!config.QualificationRunID.Valid()) {
		return errors.New("simulation controller config is invalid")
	}
	return nil
}

// instanceBinding 保存 exact placement 到 C++ runtime 的私有映射。
type instanceBinding struct {
	// stamp 是 placement current owner 提供的完整 identity。
	stamp placement.AssignmentStamp
	// fingerprint 是跨进程比较的 canonical stamp digest。
	fingerprint Digest
	// instanceID 是 C++ ready 返回的不可复活 identity。
	instanceID SimulationInstanceID
	// startRequestID 是响应丢失时复用的幂等 identity。
	startRequestID RequestID
	// mappingGeneration 绑定 input timeline。
	mappingGeneration uint64
	// seed 是由 exact assignment 与 config identity 派生的稳定非零 deterministic root。
	seed uint64
	// revision 使 replacement 后的 target cache 失效。
	revision uint64
	// ready 只在 exact ready receipt 后为真。
	ready bool
	// drained 在 target 不再可用于 input 后为真。
	drained bool
}

// Controller 把 placement lifecycle 映射到一个已 hello 的 SimulationNode。
type Controller struct {
	// session 是唯一 control pipe owner。
	session ControlSession
	// config 是 hello 后不可变 registration。
	config ControllerConfig
	// mutex 保护 bindings、pending start identity 与 revision。
	mutex sync.Mutex
	// operationMutex 防止同一 node lifecycle receipt 交错修改 registry。
	operationMutex sync.Mutex
	// bindings 按 WorldInstanceID 保存 exact runtime。
	bindings map[string]instanceBinding
	// pendingStarts 在 response loss 后保留稳定 request identity。
	pendingStarts map[string]instanceBinding
	// stoppedBindings 保存当前 node incarnation 已观察的 exact stop tombstone。
	stoppedBindings map[string]placement.AssignmentStamp
	// revision 是 target invalidation high-watermark。
	revision uint64
	// healthy 一旦 session terminal 或 shutdown 就不可恢复。
	healthy bool
	// qualificationSampleSequence 只在资格 snapshot 成功后单调推进。
	qualificationSampleSequence uint64
}

// BootstrapController 完成 nonce hello/build/capacity receipt 后才发布 controller。
func BootstrapController(ctx context.Context, session ControlSession, config ControllerConfig) (*Controller, error) {
	if ctx == nil || session == nil || config.Validate() != nil {
		return nil, errors.New("simulation controller bootstrap input is invalid")
	}
	requestID, err := newRequestID("hello")
	if err != nil {
		return nil, err
	}
	payload, err := session.Call(
		ctx,
		requestID,
		"node.hello.challenge",
		struct {
			ActorCapacity           int    `json:"actorCapacity"`
			ExpectedBuildIdentity   string `json:"expectedBuildIdentity"`
			ExpectedModelManifest   string `json:"expectedModelManifest"`
			ExpectedProfileManifest string `json:"expectedProfileManifest"`
			InstanceCapacity        int    `json:"instanceCapacity"`
			RuntimeNodeID           string `json:"runtimeNodeId"`
			SimulationNodeID        string `json:"simulationNodeId"`
		}{
			ActorCapacity:           config.Capacity.Actors,
			ExpectedBuildIdentity:   config.Build.BuildIdentity.String(),
			ExpectedModelManifest:   config.Build.ModelManifest.String(),
			ExpectedProfileManifest: config.Build.ProfileManifest.String(),
			InstanceCapacity:        config.Capacity.Instances,
			RuntimeNodeID:           config.RuntimeNodeID.String(),
			SimulationNodeID:        config.NodeID.String(),
		},
		"node.hello.receipt",
	)
	if err != nil {
		return nil, err
	}
	var receipt struct {
		ActorCapacity         int    `json:"actorCapacity"`
		BuildIdentity         string `json:"buildIdentity"`
		InstanceCapacity      int    `json:"instanceCapacity"`
		ModelManifest         string `json:"modelManifest"`
		PlatformQualification string `json:"platformQualification"`
		ProfileManifest       string `json:"profileManifest"`
		RuntimeNodeID         string `json:"runtimeNodeId"`
		SimulationNodeID      string `json:"simulationNodeId"`
	}
	if err := decodeClosedPayload(payload, &receipt); err != nil {
		return nil, err
	}
	if receipt.ActorCapacity < 1 || receipt.ActorCapacity > config.Capacity.Actors ||
		receipt.InstanceCapacity < 1 || receipt.InstanceCapacity > config.Capacity.Instances ||
		receipt.BuildIdentity != config.Build.BuildIdentity.String() ||
		receipt.ModelManifest != config.Build.ModelManifest.String() ||
		receipt.ProfileManifest != config.Build.ProfileManifest.String() ||
		receipt.PlatformQualification != config.Build.PlatformQualification ||
		receipt.RuntimeNodeID != config.RuntimeNodeID.String() ||
		receipt.SimulationNodeID != config.NodeID.String() {
		return nil, errors.New("simulation hello receipt binding drifted")
	}
	config.Capacity = NodeCapacity{
		Instances: receipt.InstanceCapacity,
		Actors:    receipt.ActorCapacity,
	}
	return &Controller{
		session:         session,
		config:          config,
		bindings:        make(map[string]instanceBinding, config.Capacity.Instances),
		pendingStarts:   make(map[string]instanceBinding, config.Capacity.Instances),
		stoppedBindings: make(map[string]placement.AssignmentStamp),
		revision:        1,
		healthy:         true,
	}, nil
}

// Start 等待 exact C++ ready receipt，但不发布 placement active。
func (controller *Controller) Start(ctx context.Context, snapshot placement.AssignmentSnapshot) error {
	if controller == nil || ctx == nil || !snapshot.Valid() ||
		snapshot.Phase() != placement.PhaseStarting ||
		snapshot.NodeID() != controller.config.RuntimeNodeID {
		return errors.New("simulation runtime start input is invalid")
	}
	controller.operationMutex.Lock()
	defer controller.operationMutex.Unlock()
	if err := controller.requireHealthy(); err != nil {
		return err
	}
	key := snapshot.InstanceID().String()
	fingerprint := assignmentFingerprint(snapshot.Stamp())
	controller.mutex.Lock()
	if _, retired := controller.stoppedBindings[key]; retired {
		controller.mutex.Unlock()
		return errors.New("WorldInstanceID was already stopped by this simulation node")
	}
	if existing, ok := controller.bindings[key]; ok {
		controller.mutex.Unlock()
		if existing.stamp.Equal(snapshot.Stamp()) && existing.fingerprint == fingerprint && existing.ready {
			return nil
		}
		return errors.New("WorldInstanceID was reused with another simulation binding")
	}
	pending, ok := controller.pendingStarts[key]
	if ok && (!pending.stamp.Equal(snapshot.Stamp()) || pending.fingerprint != fingerprint) {
		controller.mutex.Unlock()
		return errors.New("pending simulation start binding conflicts")
	}
	if !ok {
		if len(controller.bindings)+len(controller.pendingStarts) >= controller.config.Capacity.Instances {
			controller.mutex.Unlock()
			return errors.New("simulation node instance capacity is exhausted")
		}
		requestID, err := newRequestID("start")
		if err != nil {
			controller.mutex.Unlock()
			return err
		}
		pending = instanceBinding{
			stamp:             snapshot.Stamp(),
			fingerprint:       fingerprint,
			startRequestID:    requestID,
			mappingGeneration: snapshot.Generation().Uint64(),
			seed:              simulationSeed(fingerprint, controller.config.ConfigIdentity),
		}
		controller.pendingStarts[key] = pending
	}
	controller.mutex.Unlock()

	payload, err := controller.session.Call(
		ctx,
		pending.startRequestID,
		"instance.start",
		struct {
			ActorCapacity int `json:"actorCapacity"`
			Assignment    struct {
				AssignmentFingerprint string `json:"assignmentFingerprint"`
				FencingToken          string `json:"fencingToken"`
				Generation            string `json:"generation"`
				PersonalWorldID       string `json:"personalWorldId"`
				RuntimeNodeID         string `json:"runtimeNodeId"`
				WorldInstanceID       string `json:"worldInstanceId"`
			} `json:"assignment"`
			ConfigIdentity     string `json:"configIdentity"`
			MappingGeneration  string `json:"mappingGeneration"`
			NavigationIdentity string `json:"navigationIdentity"`
			PhysicsIdentity    string `json:"physicsIdentity"`
			Seed               string `json:"seed"`
			StartRequestID     string `json:"startRequestId"`
		}{
			ActorCapacity: controller.config.Capacity.Actors,
			Assignment: struct {
				AssignmentFingerprint string `json:"assignmentFingerprint"`
				FencingToken          string `json:"fencingToken"`
				Generation            string `json:"generation"`
				PersonalWorldID       string `json:"personalWorldId"`
				RuntimeNodeID         string `json:"runtimeNodeId"`
				WorldInstanceID       string `json:"worldInstanceId"`
			}{
				AssignmentFingerprint: fingerprint.String(),
				FencingToken:          strconv.FormatUint(snapshot.FencingToken().Uint64(), 10),
				Generation:            strconv.FormatUint(snapshot.Generation().Uint64(), 10),
				PersonalWorldID:       snapshot.WorldID().String(),
				RuntimeNodeID:         snapshot.NodeID().String(),
				WorldInstanceID:       snapshot.InstanceID().String(),
			},
			ConfigIdentity:     controller.config.ConfigIdentity.String(),
			MappingGeneration:  strconv.FormatUint(pending.mappingGeneration, 10),
			NavigationIdentity: controller.config.NavigationIdentity.String(),
			PhysicsIdentity:    controller.config.PhysicsIdentity.String(),
			Seed:               strconv.FormatUint(pending.seed, 10),
			StartRequestID:     pending.startRequestID.String(),
		},
		"instance.ready",
	)
	if err != nil {
		return err
	}
	var receipt struct {
		AssignmentFingerprint string `json:"assignmentFingerprint"`
		MappingGeneration     string `json:"mappingGeneration"`
		Replayed              bool   `json:"replayed"`
		Seed                  string `json:"seed"`
		SimulationInstanceID  string `json:"simulationInstanceId"`
		StartRequestID        string `json:"startRequestId"`
	}
	if err := decodeClosedPayload(payload, &receipt); err != nil {
		return err
	}
	mappingGeneration, err := parseCanonicalUint64(receipt.MappingGeneration)
	if err != nil {
		return err
	}
	seed, err := parseCanonicalUint64(receipt.Seed)
	if err != nil {
		return err
	}
	instanceID, err := NewSimulationInstanceID(receipt.SimulationInstanceID)
	if err != nil {
		return err
	}
	if receipt.AssignmentFingerprint != fingerprint.String() ||
		receipt.StartRequestID != pending.startRequestID.String() ||
		mappingGeneration != pending.mappingGeneration ||
		seed != pending.seed {
		return errors.New("simulation ready receipt binding drifted")
	}
	controller.mutex.Lock()
	controller.revision++
	pending.instanceID = instanceID
	pending.revision = controller.revision
	pending.ready = true
	controller.bindings[key] = pending
	delete(controller.pendingStarts, key)
	controller.mutex.Unlock()
	return nil
}

// Drain 关闭 exact runtime input 并等待 bounded drained receipt。
func (controller *Controller) Drain(ctx context.Context, stamp placement.AssignmentStamp) error {
	if controller == nil || ctx == nil || !stamp.Valid() {
		return errors.New("simulation runtime drain input is invalid")
	}
	controller.operationMutex.Lock()
	defer controller.operationMutex.Unlock()
	binding, err := controller.exactBinding(stamp)
	if err != nil {
		return err
	}
	if binding.drained {
		return nil
	}
	requestID, err := newRequestID("drain")
	if err != nil {
		return err
	}
	payload, err := controller.session.Call(
		ctx,
		requestID,
		"instance.drain",
		struct {
			AssignmentFingerprint string `json:"assignmentFingerprint"`
			DeadlineMS            string `json:"deadlineMs"`
			WorldInstanceID       string `json:"worldInstanceId"`
		}{
			AssignmentFingerprint: binding.fingerprint.String(),
			DeadlineMS:            strconv.FormatInt(controller.config.DrainDeadline.Milliseconds(), 10),
			WorldInstanceID:       stamp.InstanceID().String(),
		},
		"instance.drained",
	)
	if err != nil {
		return err
	}
	if err := validateStatusReceipt(payload, binding, "drained"); err != nil {
		return err
	}
	controller.mutex.Lock()
	current := controller.bindings[stamp.InstanceID().String()]
	if !current.stamp.Equal(stamp) {
		controller.mutex.Unlock()
		return errors.New("simulation drain successor race")
	}
	controller.revision++
	current.drained = true
	current.ready = false
	current.revision = controller.revision
	controller.bindings[stamp.InstanceID().String()] = current
	controller.mutex.Unlock()
	return nil
}

// Status 查询 exact runtime 的 C++ lifecycle 投影。
func (controller *Controller) Status(ctx context.Context, stamp placement.AssignmentStamp) (string, uint64, error) {
	if controller == nil || ctx == nil || !stamp.Valid() {
		return "", 0, errors.New("simulation runtime status input is invalid")
	}
	controller.operationMutex.Lock()
	defer controller.operationMutex.Unlock()
	binding, err := controller.exactBinding(stamp)
	if err != nil {
		return "", 0, err
	}
	requestID, err := newRequestID("status")
	if err != nil {
		return "", 0, err
	}
	payload, err := controller.session.Call(
		ctx,
		requestID,
		"instance.status.query",
		struct {
			AssignmentFingerprint string `json:"assignmentFingerprint"`
			WorldInstanceID       string `json:"worldInstanceId"`
		}{
			AssignmentFingerprint: binding.fingerprint.String(),
			WorldInstanceID:       stamp.InstanceID().String(),
		},
		"instance.status.receipt",
	)
	if err != nil {
		return "", 0, err
	}
	state, tick, err := parseStatusReceipt(payload, binding)
	return state, tick, err
}

// Stop 清理 exact runtime；missing 仅在本 controller 已观察 stopped 后幂等成功。
func (controller *Controller) Stop(ctx context.Context, stamp placement.AssignmentStamp) error {
	if controller == nil || ctx == nil || !stamp.Valid() {
		return errors.New("simulation runtime stop input is invalid")
	}
	controller.operationMutex.Lock()
	defer controller.operationMutex.Unlock()
	binding, err := controller.exactBinding(stamp)
	if err != nil {
		controller.mutex.Lock()
		_, pending := controller.pendingStarts[stamp.InstanceID().String()]
		stopped, stoppedBefore := controller.stoppedBindings[stamp.InstanceID().String()]
		controller.mutex.Unlock()
		if !pending && stoppedBefore && stopped.Equal(stamp) &&
			strings.Contains(err.Error(), "missing") {
			return nil
		}
		return err
	}
	requestID, err := newRequestID("stop")
	if err != nil {
		return err
	}
	payload, err := controller.session.Call(
		ctx,
		requestID,
		"instance.stop",
		struct {
			AssignmentFingerprint string `json:"assignmentFingerprint"`
			DeadlineMS            string `json:"deadlineMs"`
			WorldInstanceID       string `json:"worldInstanceId"`
		}{
			AssignmentFingerprint: binding.fingerprint.String(),
			DeadlineMS:            strconv.FormatInt(controller.config.StopDeadline.Milliseconds(), 10),
			WorldInstanceID:       stamp.InstanceID().String(),
		},
		"instance.stopped",
	)
	if err != nil {
		return err
	}
	var receipt struct {
		AssignmentFingerprint string `json:"assignmentFingerprint"`
		State                 string `json:"state"`
		WorldInstanceID       string `json:"worldInstanceId"`
	}
	if err := decodeClosedPayload(payload, &receipt); err != nil {
		return err
	}
	if receipt.AssignmentFingerprint != binding.fingerprint.String() ||
		receipt.State != "stopped" ||
		receipt.WorldInstanceID != stamp.InstanceID().String() {
		return errors.New("simulation stopped receipt binding drifted")
	}
	controller.mutex.Lock()
	current, exists := controller.bindings[stamp.InstanceID().String()]
	if exists && current.stamp.Equal(stamp) {
		delete(controller.bindings, stamp.InstanceID().String())
		controller.stoppedBindings[stamp.InstanceID().String()] = stamp
		controller.revision++
	}
	controller.mutex.Unlock()
	return nil
}

// Shutdown 只在所有 instance 已清理后停止 child node 并关闭 control session。
func (controller *Controller) Shutdown(ctx context.Context) error {
	if controller == nil || ctx == nil {
		return errors.New("simulation node shutdown input is invalid")
	}
	controller.operationMutex.Lock()
	defer controller.operationMutex.Unlock()
	controller.mutex.Lock()
	if len(controller.bindings) != 0 || len(controller.pendingStarts) != 0 {
		controller.mutex.Unlock()
		return errors.New("simulation node shutdown still has instance bindings")
	}
	if controller.healthy {
		controller.healthy = false
		controller.revision++
	}
	controller.mutex.Unlock()
	requestID, err := newRequestID("shutdown")
	if err != nil {
		return err
	}
	payload, err := controller.session.Call(
		ctx,
		requestID,
		"node.shutdown",
		struct {
			DeadlineMS string `json:"deadlineMs"`
		}{
			DeadlineMS: strconv.FormatInt(controller.config.StopDeadline.Milliseconds(), 10),
		},
		"node.stopped",
	)
	if err != nil {
		_ = controller.session.Close()
		return err
	}
	var receipt struct {
		PendingResults   int    `json:"pendingResults"`
		RunningInstances int    `json:"runningInstances"`
		SimulationNodeID string `json:"simulationNodeId"`
		State            string `json:"state"`
	}
	if err := decodeClosedPayload(payload, &receipt); err != nil {
		_ = controller.session.Close()
		return err
	}
	if receipt.PendingResults != 0 || receipt.RunningInstances != 0 ||
		receipt.SimulationNodeID != controller.config.NodeID.String() ||
		receipt.State != "stopped" {
		_ = controller.session.Close()
		return errors.New("simulation node stopped receipt binding drifted")
	}
	return controller.session.Close()
}

// AcknowledgeResult 在持久裁决完成后向 child 发送 terminal disposition。
func (controller *Controller) AcknowledgeResult(ctx context.Context, proposal ResultProposal, disposition string) error {
	if controller == nil || ctx == nil || proposal.Validate() != nil ||
		disposition != "committed" && disposition != "rejected" && disposition != "replayed" {
		return errors.New("simulation result acknowledgement input is invalid")
	}
	controller.operationMutex.Lock()
	defer controller.operationMutex.Unlock()
	if err := controller.requireHealthy(); err != nil {
		return err
	}
	requestID, err := newRequestID("result")
	if err != nil {
		return err
	}
	return controller.session.Send(
		ctx,
		requestID,
		"result.ack",
		struct {
			Disposition         string `json:"disposition"`
			ProposalFingerprint string `json:"proposalFingerprint"`
			ResultID            string `json:"resultId"`
		}{
			Disposition:         disposition,
			ProposalFingerprint: proposal.ProposalFingerprint.String(),
			ResultID:            proposal.ResultID,
		},
	)
}

// Probe 验证 node incarnation、capacity、runtime node 与 health receipt。
func (controller *Controller) Probe(ctx context.Context) error {
	if controller == nil || ctx == nil {
		return errors.New("simulation node probe input is invalid")
	}
	if !controller.operationMutex.TryLock() {
		return ErrProbeBusy
	}
	defer controller.operationMutex.Unlock()
	if err := controller.requireHealthy(); err != nil {
		return err
	}
	requestID, err := newRequestID("health")
	if err != nil {
		return err
	}
	payload, err := controller.session.Call(
		ctx,
		requestID,
		"node.health.query",
		struct {
			SimulationNodeID string `json:"simulationNodeId"`
		}{SimulationNodeID: controller.config.NodeID.String()},
		"node.health.receipt",
	)
	if err != nil {
		return err
	}
	var receipt struct {
		ActorCapacity    int    `json:"actorCapacity"`
		Healthy          bool   `json:"healthy"`
		InstanceCapacity int    `json:"instanceCapacity"`
		RunningInstances int    `json:"runningInstances"`
		RuntimeNodeID    string `json:"runtimeNodeId"`
		SimulationNodeID string `json:"simulationNodeId"`
	}
	if err := decodeClosedPayload(payload, &receipt); err != nil {
		return err
	}
	controller.mutex.Lock()
	expectedRunning := len(controller.bindings)
	controller.mutex.Unlock()
	if !receipt.Healthy ||
		receipt.ActorCapacity != controller.config.Capacity.Actors ||
		receipt.InstanceCapacity != controller.config.Capacity.Instances ||
		receipt.RunningInstances != expectedRunning ||
		receipt.RuntimeNodeID != controller.config.RuntimeNodeID.String() ||
		receipt.SimulationNodeID != controller.config.NodeID.String() {
		return errors.New("simulation node health receipt binding drifted")
	}
	return nil
}

// ResolveTarget 只为 exact ready、healthy、未 drain binding 返回内部 target。
func (controller *Controller) ResolveTarget(stamp placement.AssignmentStamp) (SimulationTarget, bool) {
	if controller == nil || !stamp.Valid() {
		return SimulationTarget{}, false
	}
	if !controller.Healthy() {
		return SimulationTarget{}, false
	}
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	binding, ok := controller.bindings[stamp.InstanceID().String()]
	if !ok || !controller.healthy || !binding.ready || binding.drained ||
		!binding.stamp.Equal(stamp) {
		return SimulationTarget{}, false
	}
	target := SimulationTarget{
		RuntimeNodeID:         controller.config.RuntimeNodeID,
		NodeID:                controller.config.NodeID,
		InstanceID:            binding.instanceID,
		AssignmentFingerprint: binding.fingerprint,
		MappingGeneration:     binding.mappingGeneration,
		ModelManifest:         controller.config.Build.ModelManifest,
		ProfileManifest:       controller.config.Build.ProfileManifest,
		ConfigIdentity:        controller.config.ConfigIdentity,
		ActorCapacity:         controller.config.Capacity.Actors,
		Revision:              binding.revision,
	}
	return target, target.Validate() == nil
}

// ResolveProposalBinding 将 result 的 fingerprint/instance 解析回 exact placement stamp。
func (controller *Controller) ResolveProposalBinding(proposal ResultProposal) (placement.AssignmentStamp, bool) {
	if controller == nil || proposal.Validate() != nil {
		return placement.AssignmentStamp{}, false
	}
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	for _, binding := range controller.bindings {
		if binding.fingerprint == proposal.AssignmentFingerprint &&
			binding.instanceID == proposal.InstanceID &&
			proposal.Kind == LifecycleSummaryKind &&
			proposal.PayloadDigest == lifecyclePayloadDigest(binding, proposal.TickEnd) &&
			proposal.EvidenceDigest == controller.lifecycleEvidenceDigest(binding, proposal.TickEnd) {
			return binding.stamp, true
		}
	}
	return placement.AssignmentStamp{}, false
}

// Contains 报告 exact stamp 是否由本 controller 登记。
func (controller *Controller) Contains(stamp placement.AssignmentStamp) bool {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	binding, ok := controller.bindings[stamp.InstanceID().String()]
	return ok && binding.stamp.Equal(stamp)
}

// StampsForWorld 返回指定 PersonalWorld 的 node-local exact binding 副本。
func (controller *Controller) StampsForWorld(worldID personalworld.PersonalWorldID) []placement.AssignmentStamp {
	if controller == nil || !worldID.Valid() {
		return nil
	}
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	result := make([]placement.AssignmentStamp, 0, 1)
	for _, binding := range controller.bindings {
		if binding.stamp.WorldID() == worldID {
			result = append(result, binding.stamp)
		}
	}
	return result
}

// Stamps 返回当前 node 全部 exact runtime bindings。
func (controller *Controller) Stamps() []placement.AssignmentStamp {
	if controller == nil {
		return nil
	}
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	result := make([]placement.AssignmentStamp, 0, len(controller.bindings))
	for _, binding := range controller.bindings {
		result = append(result, binding.stamp)
	}
	sort.Slice(result, func(first int, second int) bool {
		return result[first].InstanceID().String() < result[second].InstanceID().String()
	})
	return result
}

// Healthy 报告 node 是否可用于新 start/target。
func (controller *Controller) Healthy() bool {
	if controller == nil {
		return false
	}
	select {
	case <-controller.session.Done():
		controller.mutex.Lock()
		if controller.healthy {
			controller.healthy = false
			controller.revision++
		}
		controller.mutex.Unlock()
	default:
	}
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	return controller.healthy
}

// exactBinding 返回完整 stamp 对应的 binding 副本。
func (controller *Controller) exactBinding(stamp placement.AssignmentStamp) (instanceBinding, error) {
	if err := controller.requireHealthy(); err != nil {
		return instanceBinding{}, err
	}
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	binding, ok := controller.bindings[stamp.InstanceID().String()]
	if !ok {
		return instanceBinding{}, errors.New("simulation runtime binding is missing")
	}
	if !binding.stamp.Equal(stamp) {
		return instanceBinding{}, errors.New("simulation runtime binding is stale")
	}
	return binding, nil
}

// requireHealthy 把 terminal session 转为不可恢复 controller state。
func (controller *Controller) requireHealthy() error {
	if !controller.Healthy() {
		return errors.New("simulation node is unhealthy")
	}
	return nil
}

// validateStatusReceipt 验证 drain/status receipt 完整绑定。
func validateStatusReceipt(payload json.RawMessage, binding instanceBinding, state string) error {
	actualState, _, err := parseStatusReceipt(payload, binding)
	if err != nil {
		return err
	}
	if actualState != state {
		return errors.New("simulation status receipt state drifted")
	}
	return nil
}

// parseStatusReceipt 验证 status binding 并返回 state/Tick。
func parseStatusReceipt(payload json.RawMessage, binding instanceBinding) (string, uint64, error) {
	var receipt struct {
		AssignmentFingerprint string `json:"assignmentFingerprint"`
		CommittedTick         string `json:"committedTick"`
		SimulationInstanceID  string `json:"simulationInstanceId"`
		State                 string `json:"state"`
	}
	if err := decodeClosedPayload(payload, &receipt); err != nil {
		return "", 0, err
	}
	tick, err := parseCanonicalUint64AllowZero(receipt.CommittedTick)
	if err != nil {
		return "", 0, err
	}
	if receipt.AssignmentFingerprint != binding.fingerprint.String() ||
		receipt.SimulationInstanceID != binding.instanceID.String() ||
		receipt.State != "running" && receipt.State != "drained" {
		return "", 0, errors.New("simulation status receipt binding drifted")
	}
	return receipt.State, tick, nil
}

// assignmentFingerprint 绑定 placement stamp 的全部 identity。
func assignmentFingerprint(stamp placement.AssignmentStamp) Digest {
	value := strings.Join(
		[]string{
			stamp.WorldID().String(),
			stamp.InstanceID().String(),
			stamp.NodeID().String(),
			strconv.FormatUint(stamp.Generation().Uint64(), 10),
			strconv.FormatUint(stamp.FencingToken().Uint64(), 10),
		},
		"\x00",
	)
	sum := sha256.Sum256([]byte(value))
	return Digest(hex.EncodeToString(sum[:]))
}

// simulationSeed 从 assignment/config binding 派生可重放的非零 deterministic root seed。
func simulationSeed(fingerprint Digest, configIdentity Digest) uint64 {
	sum := sha256.Sum256([]byte("simulation-seed-v1\x00" + fingerprint.String() + "\x00" + configIdentity.String()))
	seed := binary.BigEndian.Uint64(sum[:8])
	if seed == 0 {
		return 1
	}
	return seed
}

// lifecyclePayloadDigest 重算当前已登记 lifecycle summary 的低敏 payload identity。
func lifecyclePayloadDigest(binding instanceBinding, tickEnd uint64) Digest {
	return textDigest(binding.instanceID.String() + "|" + strconv.FormatUint(tickEnd, 10))
}

// lifecycleEvidenceDigest 绑定产生 summary 的完整 node/start 资格输入。
func (controller *Controller) lifecycleEvidenceDigest(binding instanceBinding, tickEnd uint64) Digest {
	return textDigest(strings.Join(
		[]string{
			controller.config.Build.BuildIdentity.String(),
			controller.config.Build.ModelManifest.String(),
			controller.config.Build.ProfileManifest.String(),
			controller.config.ConfigIdentity.String(),
			controller.config.NavigationIdentity.String(),
			controller.config.PhysicsIdentity.String(),
			strconv.FormatUint(binding.mappingGeneration, 10),
			strconv.FormatUint(binding.seed, 10),
			strconv.Itoa(controller.config.Capacity.Actors),
			strconv.FormatUint(tickEnd, 10),
		},
		"|",
	))
}

// textDigest 对双方已冻结的 UTF-8 material 生成规范小写 SHA-256。
func textDigest(value string) Digest {
	sum := sha256.Sum256([]byte(value))
	return Digest(hex.EncodeToString(sum[:]))
}

// newRequestID 使用 CSPRNG 创建不含业务 identity 的 request ID。
func newRequestID(kind string) (RequestID, error) {
	if kind == "" || len(kind) > 12 {
		return "", errors.New("control request kind is invalid")
	}
	var material [16]byte
	if _, err := rand.Read(material[:]); err != nil {
		return "", fmt.Errorf("generate control request identity: %w", err)
	}
	return NewRequestID("sctl_" + kind + "_" + hex.EncodeToString(material[:]))
}

var _ placement.RuntimeController = (*Controller)(nil)
