package placement

import (
	"context"
	"errors"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
)

// Service 编排 WorldInstance current assignment、runtime ready 与 lease/fencing 用例。
//
// Service 不持有 mutable registry、socket、endpoint 或 PersonalWorld 内容。所有 current
// assignment 并发由 PlacementStore 原子决议；runtime ready 不能自行发布 active。Service
// 自身没有调用期可变状态，当依赖满足并发契约时可由多个请求并发使用。
type Service struct {
	// store 持有 current assignment、lease 与 generation/fence high-watermark 原子边界。
	store PlacementStore
	// runtime 执行以 WorldInstanceID 幂等的启动和停止 side effect。
	runtime RuntimeController
	// clock 为每次条件操作提供单次受信 UTC 时间 snapshot。
	clock Clock
	// ids 为 EnsureActive 创建不可预测 WorldInstance identity 材料。
	ids IDGenerator
	// leaseTTL 是 assignment 初始和续租 deadline 的有界相对时长。
	leaseTTL time.Duration
}

// NewService 校验并保留 placement application 的全部必需依赖和 lease TTL。
//
// 构造不会启动 goroutine、runtime 或 I/O；依赖关闭、renew loop 和 reconciliation 生命周期
// 仍由后续 Composition Root owner 持有。
func NewService(store PlacementStore, runtime RuntimeController, clock Clock, ids IDGenerator, leaseTTL time.Duration) (*Service, error) {
	if store == nil || runtime == nil || clock == nil || ids == nil || leaseTTL < MinimumLeaseTTL || leaseTTL > MaximumLeaseTTL {
		return nil, newError(ErrorKindValidation, OperationConstruct, CommitPhaseNone, errors.New("placement service dependencies or lease TTL are invalid"))
	}
	return &Service{store: store, runtime: runtime, clock: clock, ids: ids, leaseTTL: leaseTTL}, nil
}

// EnsureActive 原子解析或启动 PersonalWorld 的唯一 current active WorldInstance。
//
// worldID 与 nodeID 必须来自受信 application/Composition Root 边界，不得由客户端 payload
// 选择。Service 先 resolve/acquire starting assignment，再等待 runtime ready，最后条件 activate；
// 并发调用由 store 返回 existing 或 in-progress，不通过进程锁线性化。ctx 取消不能证明 store
// transition 未提交，CommitUnknown 只会通过原 candidate identity resolve 收敛。
func (service *Service) EnsureActive(ctx context.Context, worldID personalworld.PersonalWorldID, nodeID RuntimeNodeID) (AssignmentResult, error) {
	if ctx == nil || !worldID.Valid() || !nodeID.Valid() {
		return AssignmentResult{}, newError(ErrorKindValidation, OperationEnsureActive, CommitPhaseNone, nil)
	}
	now, err := service.now(OperationEnsureActive)
	if err != nil {
		return AssignmentResult{}, err
	}
	current, found, err := service.resolveCurrent(ctx, worldID, now, OperationEnsureActive, CommitPhaseNone)
	if err != nil {
		return AssignmentResult{}, err
	}
	if found && current.ValidAt(now) {
		if current.Phase() == PhaseActive {
			return assignmentResult(current, ResultDispositionExisting, false, OperationEnsureActive, CommitPhaseNone)
		}
		return AssignmentResult{}, newError(ErrorKindInProgress, OperationEnsureActive, CommitPhaseNone, nil)
	}
	instanceID, err := service.newWorldInstanceID()
	if err != nil {
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsureActive, CommitPhaseNone, err)
	}
	candidate, err := service.newCandidate(worldID, instanceID, nodeID, now)
	if err != nil {
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsureActive, CommitPhaseNone, err)
	}
	request, err := NewAcquireRequest(candidate, now)
	if err != nil {
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsureActive, CommitPhaseNone, err)
	}
	snapshot, outcome, storeErr := service.store.Acquire(ctx, request)
	if storeErr != nil {
		if outcome == StoreOutcomeCommitUnknown && snapshot.empty() {
			return service.recoverAcquiredCandidate(ctx, candidate, OperationEnsureActive)
		}
		return AssignmentResult{}, storeFailure(OperationEnsureActive, outcome, storeErr)
	}
	switch outcome {
	case StoreOutcomeApplied:
		if err := validateCandidateSnapshot(snapshot, candidate, PhaseStarting, now); err != nil {
			return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsureActive, CommitPhaseCommitted, err)
		}
		return service.startAndActivate(ctx, snapshot, ResultDispositionStarted, OperationEnsureActive)
	case StoreOutcomeExisting:
		if !snapshot.ValidAt(now) || snapshot.WorldID() != worldID || snapshot.Phase() != PhaseActive {
			return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsureActive, CommitPhaseNone, errors.New("store returned malformed existing assignment"))
		}
		return assignmentResult(snapshot, ResultDispositionExisting, false, OperationEnsureActive, CommitPhaseNone)
	case StoreOutcomeReplay:
		if err := validateCandidateSnapshotAnyPhase(snapshot, candidate, now); err != nil {
			return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsureActive, CommitPhaseCommitted, err)
		}
		if snapshot.Phase() == PhaseActive {
			return assignmentResult(snapshot, ResultDispositionReplayed, false, OperationEnsureActive, CommitPhaseCommitted)
		}
		return service.startAndActivate(ctx, snapshot, ResultDispositionReplayed, OperationEnsureActive)
	case StoreOutcomeInProgress:
		if !snapshot.ValidAt(now) || snapshot.WorldID() != worldID || snapshot.Phase() != PhaseStarting {
			return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsureActive, CommitPhaseNone, errors.New("store returned malformed in-progress assignment"))
		}
		return AssignmentResult{}, newError(ErrorKindInProgress, OperationEnsureActive, CommitPhaseNone, nil)
	default:
		if !snapshot.empty() {
			return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsureActive, phaseForStoreOutcome(outcome), errors.New("store returned snapshot for failed acquire"))
		}
		return AssignmentResult{}, outcomeError(OperationEnsureActive, outcome, nil)
	}
}

// Renew 原子延长完整 current stamp 的 lease，generation 与 fencing token 保持不变。
//
// 等于或晚于旧 expiry 的 holder 已失效；store 必须返回 Expired 而不是复活。返回 snapshot
// 仍是 point-in-time 值，调用方不能把成功 renew 解释为永久写资格。
func (service *Service) Renew(ctx context.Context, stamp AssignmentStamp) (AssignmentSnapshot, error) {
	if ctx == nil || !stamp.Valid() {
		return AssignmentSnapshot{}, newError(ErrorKindValidation, OperationRenew, CommitPhaseNone, nil)
	}
	now, err := service.now(OperationRenew)
	if err != nil {
		return AssignmentSnapshot{}, err
	}
	request, err := NewRenewRequest(stamp, now, now.Add(service.leaseTTL))
	if err != nil {
		return AssignmentSnapshot{}, newError(ErrorKindDependencyUnavailable, OperationRenew, CommitPhaseNone, err)
	}
	snapshot, outcome, storeErr := service.store.Renew(ctx, request)
	if storeErr != nil {
		return AssignmentSnapshot{}, storeFailure(OperationRenew, outcome, storeErr)
	}
	switch outcome {
	case StoreOutcomeApplied, StoreOutcomeReplay:
		if !snapshot.ValidAt(now) || !snapshot.Stamp().Equal(stamp) || !snapshot.Lease().ExpiresAt().Equal(request.LeaseExpiresAt()) {
			return AssignmentSnapshot{}, newError(ErrorKindDependencyUnavailable, OperationRenew, CommitPhaseCommitted, errors.New("store returned malformed renewed assignment"))
		}
		return snapshot, nil
	default:
		if !snapshot.empty() {
			return AssignmentSnapshot{}, newError(ErrorKindDependencyUnavailable, OperationRenew, phaseForStoreOutcome(outcome), errors.New("store returned snapshot for failed renew"))
		}
		return AssignmentSnapshot{}, outcomeError(OperationRenew, outcome, nil)
	}
}

// QualifyWrite 对完整 current stamp 执行 point-in-time active/lease 校验。
//
// 返回 WriteFence 仍必须交给实际持久 mutation transaction 重新验证；本方法不接受部分
// identity，也不把连接、endpoint 或曾经成功的结果转换为永久授权。
func (service *Service) QualifyWrite(ctx context.Context, stamp AssignmentStamp) (WriteFence, error) {
	if ctx == nil || !stamp.Valid() {
		return WriteFence{}, newError(ErrorKindValidation, OperationQualifyWrite, CommitPhaseNone, nil)
	}
	now, err := service.now(OperationQualifyWrite)
	if err != nil {
		return WriteFence{}, err
	}
	request, err := NewStampRequest(stamp, now)
	if err != nil {
		return WriteFence{}, newError(ErrorKindDependencyUnavailable, OperationQualifyWrite, CommitPhaseNone, err)
	}
	fence, outcome, storeErr := service.store.QualifyWrite(ctx, request)
	if storeErr != nil {
		return WriteFence{}, storeFailure(OperationQualifyWrite, outcome, storeErr)
	}
	if outcome == StoreOutcomeApplied {
		if !fence.Valid() || !fence.Stamp().Equal(stamp) {
			return WriteFence{}, newError(ErrorKindDependencyUnavailable, OperationQualifyWrite, CommitPhaseNone, errors.New("store returned malformed write fence"))
		}
		return fence, nil
	}
	if !fence.empty() {
		return WriteFence{}, newError(ErrorKindDependencyUnavailable, OperationQualifyWrite, CommitPhaseNone, errors.New("store returned fence for failed qualification"))
	}
	return WriteFence{}, outcomeError(OperationQualifyWrite, outcome, nil)
}

// Sleep 原子撤销 expected current assignment 后再停止对应 runtime。
//
// Revoke 一旦 applied/replay，旧 fence 即永久失效；Stop 失败通过包含有效 LifecycleResult
// 的 cleanup error 表达，调用方不得补偿恢复 assignment。Stale expected stamp 只能返回
// conflict，不能停止或撤销 successor。
func (service *Service) Sleep(ctx context.Context, expected AssignmentStamp) (LifecycleResult, error) {
	if ctx == nil || !expected.Valid() {
		return LifecycleResult{}, newError(ErrorKindValidation, OperationSleep, CommitPhaseNone, nil)
	}
	now, err := service.now(OperationSleep)
	if err != nil {
		return LifecycleResult{}, err
	}
	request, err := NewStampRequest(expected, now)
	if err != nil {
		return LifecycleResult{}, newError(ErrorKindDependencyUnavailable, OperationSleep, CommitPhaseNone, err)
	}
	predecessor, outcome, storeErr := service.store.Revoke(ctx, request)
	if storeErr != nil {
		return LifecycleResult{}, storeFailure(OperationSleep, outcome, storeErr)
	}
	if outcome != StoreOutcomeApplied && outcome != StoreOutcomeReplay {
		if !predecessor.empty() {
			return LifecycleResult{}, newError(ErrorKindDependencyUnavailable, OperationSleep, phaseForStoreOutcome(outcome), errors.New("store returned predecessor for failed revoke"))
		}
		return LifecycleResult{}, outcomeError(OperationSleep, outcome, nil)
	}
	if !predecessor.Valid() || !predecessor.Stamp().Equal(expected) {
		return LifecycleResult{}, newError(ErrorKindDependencyUnavailable, OperationSleep, CommitPhaseCommitted, errors.New("store returned malformed revoked predecessor"))
	}
	stopErr := service.runtime.Stop(ctx, expected)
	result, resultErr := NewLifecycleResult(predecessor, true, stopErr != nil)
	if resultErr != nil {
		return LifecycleResult{}, newError(ErrorKindDependencyUnavailable, OperationSleep, CommitPhaseCommitted, resultErr)
	}
	if stopErr != nil {
		return result, newError(ErrorKindCleanupFailed, OperationSleep, CommitPhaseCommitted, stopErr)
	}
	return result, nil
}

// Replace 以 break-before-make 顺序重建或迁移 current WorldInstance。
//
// command.successorID 必须由调用方预生成并在 commit-unknown 重试时复用。Store 先原子撤销
// predecessor 并提交 starting successor；从该线性化点起旧 fence 失效。随后 Stop predecessor、
// Start successor 并 Activate。Stop 失败不恢复旧 fence，也不阻止安全 successor 变为 active，
// 但成功 result 会与 CleanupFailed error 一起返回以触发 reconciliation。
func (service *Service) Replace(ctx context.Context, command ReplaceCommand) (AssignmentResult, error) {
	if ctx == nil || !command.Valid() {
		return AssignmentResult{}, newError(ErrorKindValidation, OperationReplace, CommitPhaseNone, nil)
	}
	now, err := service.now(OperationReplace)
	if err != nil {
		return AssignmentResult{}, err
	}
	candidate, err := service.newCandidate(command.Expected().WorldID(), command.SuccessorID(), command.TargetNodeID(), now)
	if err != nil {
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationReplace, CommitPhaseNone, err)
	}
	request, err := NewReplaceRequest(command.Expected(), candidate, now)
	if err != nil {
		return AssignmentResult{}, newError(ErrorKindValidation, OperationReplace, CommitPhaseNone, err)
	}
	successor, outcome, storeErr := service.store.Replace(ctx, request)
	disposition := ResultDispositionStarted
	if storeErr != nil {
		if outcome != StoreOutcomeCommitUnknown || !successor.empty() {
			return AssignmentResult{}, storeFailure(OperationReplace, outcome, storeErr)
		}
		recovered, found, resolveErr := service.resolveCurrent(ctx, command.Expected().WorldID(), now, OperationReplace, CommitPhaseUnknown)
		if resolveErr != nil {
			return AssignmentResult{}, resolveErr
		}
		if !found || validateCandidateSnapshotAnyPhase(recovered, candidate, now) != nil {
			return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationReplace, CommitPhaseUnknown, storeErr)
		}
		successor = recovered
		disposition = ResultDispositionReplayed
	} else {
		switch outcome {
		case StoreOutcomeApplied:
			if err := validateCandidateSnapshot(successor, candidate, PhaseStarting, now); err != nil {
				return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationReplace, CommitPhaseCommitted, err)
			}
		case StoreOutcomeReplay:
			if err := validateReplacementSnapshot(successor, command, now); err != nil {
				return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationReplace, CommitPhaseCommitted, err)
			}
			disposition = ResultDispositionReplayed
		default:
			if !successor.empty() {
				return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, OperationReplace, phaseForStoreOutcome(outcome), errors.New("store returned successor for failed replace"))
			}
			return AssignmentResult{}, outcomeError(OperationReplace, outcome, nil)
		}
	}
	stopErr := service.runtime.Stop(ctx, command.Expected())
	var result AssignmentResult
	if successor.Phase() == PhaseActive {
		result, err = assignmentResult(successor, disposition, stopErr != nil, OperationReplace, CommitPhaseCommitted)
	} else {
		result, err = service.startAndActivate(ctx, successor, disposition, OperationReplace)
		if err == nil && stopErr != nil {
			result, err = assignmentResult(result.Assignment(), result.Disposition(), true, OperationReplace, CommitPhaseCommitted)
		}
	}
	if err != nil {
		if stopErr != nil {
			return result, newError(ErrorKindRuntimeUnavailable, OperationReplace, CommitPhaseCommitted, errors.Join(err, stopErr))
		}
		return result, err
	}
	if stopErr != nil {
		return result, newError(ErrorKindCleanupFailed, OperationReplace, CommitPhaseCommitted, stopErr)
	}
	return result, nil
}

// recoverAcquiredCandidate 在 acquire commit-unknown 后只接受精确原 candidate current 事实。
func (service *Service) recoverAcquiredCandidate(ctx context.Context, candidate AssignmentCandidate, operation Operation) (AssignmentResult, error) {
	now, err := service.now(operation)
	if err != nil {
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, operation, CommitPhaseUnknown, err)
	}
	snapshot, found, err := service.resolveCurrent(ctx, candidate.WorldID(), now, operation, CommitPhaseUnknown)
	if err != nil {
		return AssignmentResult{}, err
	}
	if !found || validateCandidateSnapshotAnyPhase(snapshot, candidate, now) != nil {
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, operation, CommitPhaseUnknown, errors.New("commit-unknown candidate could not be resolved"))
	}
	if snapshot.Phase() == PhaseActive {
		return assignmentResult(snapshot, ResultDispositionReplayed, false, operation, CommitPhaseCommitted)
	}
	return service.startAndActivate(ctx, snapshot, ResultDispositionReplayed, operation)
}

// startAndActivate 等待 runtime ready 后按完整 starting stamp 条件发布 active。
//
// Runtime ready 只允许尝试发布，不自行授予 write qualification。Activate commit unknown 时
// 只接受 Resolve 确认同一 stamp 已成为有效 active；明确未提交、stale、expired 或 conflict
// 时必须按旧完整 stamp 条件 revoke 并停止 runtime，不能影响 successor。
func (service *Service) startAndActivate(ctx context.Context, starting AssignmentSnapshot, disposition ResultDisposition, operation Operation) (AssignmentResult, error) {
	if !starting.Valid() || starting.Phase() != PhaseStarting {
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, operation, CommitPhaseCommitted, errors.New("starting assignment is invalid"))
	}
	if err := service.runtime.Start(ctx, starting); err != nil {
		cleanupErr, phase := service.cleanupStarting(ctx, starting, operation)
		return AssignmentResult{}, newError(ErrorKindRuntimeUnavailable, operation, phase, errors.Join(err, cleanupErr))
	}
	now, err := service.now(operation)
	if err != nil {
		cleanupErr, phase := service.cleanupStarting(ctx, starting, operation)
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, operation, phase, errors.Join(err, cleanupErr))
	}
	request, err := NewStampRequest(starting.Stamp(), now)
	if err != nil {
		cleanupErr, phase := service.cleanupStarting(ctx, starting, operation)
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, operation, phase, errors.Join(err, cleanupErr))
	}
	active, outcome, storeErr := service.store.Activate(ctx, request)
	if storeErr != nil {
		if outcome == StoreOutcomeCommitUnknown && active.empty() {
			resolved, found, resolveErr := service.resolveCurrent(ctx, starting.WorldID(), now, operation, CommitPhaseUnknown)
			if resolveErr == nil && found && resolved.Stamp().Equal(starting.Stamp()) && resolved.Phase() == PhaseActive && resolved.ValidAt(now) {
				return assignmentResult(resolved, ResultDispositionReplayed, false, operation, CommitPhaseCommitted)
			}
			if resolveErr != nil {
				return AssignmentResult{}, resolveErr
			}
			return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, operation, CommitPhaseUnknown, storeErr)
		}
		if outcome == StoreOutcomeNotCommitted {
			cleanupErr, phase := service.cleanupStarting(ctx, starting, operation)
			return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, operation, phase, errors.Join(storeErr, cleanupErr))
		}
		return AssignmentResult{}, storeFailure(operation, outcome, storeErr)
	}
	if outcome == StoreOutcomeApplied || outcome == StoreOutcomeReplay {
		if !active.ValidAt(now) || active.Phase() != PhaseActive || !active.Stamp().Equal(starting.Stamp()) || !active.CreatedAt().Equal(starting.CreatedAt()) {
			return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, operation, CommitPhaseCommitted, errors.New("store returned malformed active assignment"))
		}
		return assignmentResult(active, disposition, false, operation, CommitPhaseCommitted)
	}
	if !active.empty() {
		baseErr := newError(ErrorKindDependencyUnavailable, operation, phaseForStoreOutcome(outcome), errors.New("store returned assignment for failed activate"))
		cleanupErr, phase := service.cleanupStarting(ctx, starting, operation)
		if cleanupErr != nil {
			return AssignmentResult{}, newError(ErrorKindCleanupFailed, operation, phase, errors.Join(baseErr, cleanupErr))
		}
		return AssignmentResult{}, baseErr
	}
	baseErr := outcomeError(operation, outcome, nil)
	cleanupErr, phase := service.cleanupStarting(ctx, starting, operation)
	if cleanupErr != nil {
		return AssignmentResult{}, newError(ErrorKindCleanupFailed, operation, phase, errors.Join(baseErr, cleanupErr))
	}
	return AssignmentResult{}, baseErr
}

// cleanupStarting 在 candidate 未能发布 active 后条件撤销 starting assignment，再停止同一 instance。
//
// 补偿可能复用已经取消或超时的 ctx；revoke/stop 失败不会把 starting 转为可写状态。返回的
// CommitPhase 表达 revoke 的提交确定性，无法即时确认或清理时由 lease expiry 与 reconciliation
// 提供最终回收边界。Stop 始终使用旧完整 stamp，不能停止 successor。
func (service *Service) cleanupStarting(ctx context.Context, starting AssignmentSnapshot, operation Operation) (error, CommitPhase) {
	phase := CommitPhaseCommitted
	var revokeErr error
	now, nowErr := service.now(operation)
	if nowErr != nil {
		revokeErr = nowErr
	} else {
		request, requestErr := NewStampRequest(starting.Stamp(), now)
		if requestErr != nil {
			revokeErr = requestErr
		} else {
			predecessor, outcome, storeErr := service.store.Revoke(ctx, request)
			revokeErr = storeErr
			if outcome == StoreOutcomeCommitUnknown {
				phase = CommitPhaseUnknown
			}
			if revokeErr == nil && outcome != StoreOutcomeApplied && outcome != StoreOutcomeReplay && outcome != StoreOutcomeConflict && outcome != StoreOutcomeNotFound {
				revokeErr = errors.New("store returned invalid cleanup outcome")
			}
			if revokeErr == nil && (outcome == StoreOutcomeApplied || outcome == StoreOutcomeReplay) && (!predecessor.Valid() || !predecessor.Stamp().Equal(starting.Stamp())) {
				revokeErr = errors.New("store returned malformed cleanup predecessor")
			}
		}
	}
	stopErr := service.runtime.Stop(ctx, starting.Stamp())
	return errors.Join(revokeErr, stopErr), phase
}

// resolveCurrent 验证 resolve outcome/snapshot 组合并拒绝其他 world 或 malformed 值。
func (service *Service) resolveCurrent(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time, operation Operation, phase CommitPhase) (AssignmentSnapshot, bool, error) {
	snapshot, outcome, storeErr := service.store.Resolve(ctx, worldID, observedAt)
	if storeErr != nil {
		return AssignmentSnapshot{}, false, newError(ErrorKindDependencyUnavailable, operation, phase, storeErr)
	}
	switch outcome {
	case ResolveOutcomeFound:
		if !snapshot.Valid() || snapshot.WorldID() != worldID {
			return AssignmentSnapshot{}, false, newError(ErrorKindDependencyUnavailable, operation, phase, errors.New("store returned malformed resolved assignment"))
		}
		return snapshot, true, nil
	case ResolveOutcomeNotFound:
		if !snapshot.empty() {
			return AssignmentSnapshot{}, false, newError(ErrorKindDependencyUnavailable, operation, phase, errors.New("store returned assignment for not found"))
		}
		return AssignmentSnapshot{}, false, nil
	default:
		return AssignmentSnapshot{}, false, newError(ErrorKindDependencyUnavailable, operation, phase, errors.New("store returned invalid resolve outcome"))
	}
}

// now 读取并规范化一次受信 UTC absolute time，零值属于依赖违约。
func (service *Service) now(operation Operation) (time.Time, error) {
	now := service.clock.Now()
	if now.IsZero() {
		return time.Time{}, newError(ErrorKindDependencyUnavailable, operation, CommitPhaseNone, errors.New("clock returned zero time"))
	}
	return now.UTC(), nil
}

// newWorldInstanceID 添加 namespace，并把 generator 违约视为依赖故障。
func (service *Service) newWorldInstanceID() (WorldInstanceID, error) {
	material, err := service.ids.NewID()
	if err != nil {
		return WorldInstanceID{}, err
	}
	return NewWorldInstanceID(worldInstanceIDPrefix + material)
}

// newCandidate 用同一 time snapshot 构造 starting assignment 与初始 lease deadline。
func (service *Service) newCandidate(worldID personalworld.PersonalWorldID, instanceID WorldInstanceID, nodeID RuntimeNodeID, now time.Time) (AssignmentCandidate, error) {
	return NewAssignmentCandidate(worldID, instanceID, nodeID, now, now.Add(service.leaseTTL))
}

// validateCandidateSnapshot 确认 store success 与 application 生成的候选事实完全一致。
func validateCandidateSnapshot(snapshot AssignmentSnapshot, candidate AssignmentCandidate, phase Phase, observedAt time.Time) error {
	if !snapshot.ValidAt(observedAt) || snapshot.Phase() != phase {
		return errors.New("store returned candidate with invalid phase or lease")
	}
	if snapshot.WorldID() != candidate.WorldID() || snapshot.InstanceID() != candidate.InstanceID() || snapshot.NodeID() != candidate.NodeID() {
		return errors.New("store returned candidate with inconsistent identity")
	}
	if !snapshot.CreatedAt().Equal(candidate.CreatedAt()) || !snapshot.Lease().ExpiresAt().Equal(candidate.LeaseExpiresAt()) {
		return errors.New("store returned candidate with inconsistent time")
	}
	return nil
}

// validateCandidateSnapshotAnyPhase 接受 commit-unknown recovery 的 starting 或 active 精确 candidate。
func validateCandidateSnapshotAnyPhase(snapshot AssignmentSnapshot, candidate AssignmentCandidate, observedAt time.Time) error {
	if !snapshot.Phase().Valid() {
		return errors.New("resolved candidate phase is invalid")
	}
	return validateCandidateSnapshot(snapshot, candidate, snapshot.Phase(), observedAt)
}

// validateReplacementSnapshot 按外部重试可稳定复用的 command identity 验证 successor replay。
//
// 重试会重新读取 clock，因此不能要求首次提交的 created time/expiry 等于本次临时候选；store
// 返回值仍必须绑定同一 world、successor、target node，并严格推进 predecessor generation/fence。
func validateReplacementSnapshot(snapshot AssignmentSnapshot, command ReplaceCommand, observedAt time.Time) error {
	expected := command.Expected()
	if !snapshot.ValidAt(observedAt) || !snapshot.Phase().Valid() {
		return errors.New("store returned replay replacement with invalid phase or lease")
	}
	if snapshot.WorldID() != expected.WorldID() || snapshot.InstanceID() != command.SuccessorID() || snapshot.NodeID() != command.TargetNodeID() {
		return errors.New("store returned replacement inconsistent with replay identity")
	}
	if snapshot.Generation().Uint64() <= expected.Generation().Uint64() || snapshot.FencingToken().Uint64() <= expected.FencingToken().Uint64() {
		return errors.New("store returned replacement without advancing generation or fence")
	}
	return nil
}

// assignmentResult 验证 application success 仍为 active 且包含完整 disposition。
func assignmentResult(snapshot AssignmentSnapshot, disposition ResultDisposition, cleanupFailed bool, operation Operation, phase CommitPhase) (AssignmentResult, error) {
	result, err := NewAssignmentResult(snapshot, disposition, cleanupFailed)
	if err != nil {
		return AssignmentResult{}, newError(ErrorKindDependencyUnavailable, operation, phase, err)
	}
	return result, nil
}

// storeFailure 把 adapter error 与其声明的提交确定性对齐，并拒绝 success+error 伪组合。
func storeFailure(operation Operation, outcome StoreOutcome, cause error) error {
	switch outcome {
	case StoreOutcomeCommitUnknown:
		return newError(ErrorKindDependencyUnavailable, operation, CommitPhaseUnknown, cause)
	case StoreOutcomeNotCommitted:
		return newError(ErrorKindDependencyUnavailable, operation, CommitPhaseNone, cause)
	case StoreOutcomeApplied, StoreOutcomeReplay:
		phase := CommitPhaseCommitted
		if operation == OperationQualifyWrite {
			phase = CommitPhaseNone
		}
		return newError(ErrorKindDependencyUnavailable, operation, phase, errors.Join(errors.New("store returned success outcome with error"), cause))
	default:
		return newError(ErrorKindDependencyUnavailable, operation, CommitPhaseNone, errors.New("store returned error with invalid outcome"))
	}
}

// outcomeError 把 nil-error store 业务决议映射为稳定 application failure。
func outcomeError(operation Operation, outcome StoreOutcome, cause error) error {
	switch outcome {
	case StoreOutcomeNotFound:
		return newError(ErrorKindNotFound, operation, CommitPhaseNone, cause)
	case StoreOutcomeConflict:
		return newError(ErrorKindConflict, operation, CommitPhaseNone, cause)
	case StoreOutcomeInProgress:
		return newError(ErrorKindInProgress, operation, CommitPhaseNone, cause)
	case StoreOutcomeExpired:
		return newError(ErrorKindExpired, operation, CommitPhaseNone, cause)
	case StoreOutcomeNotCommitted:
		return newError(ErrorKindDependencyUnavailable, operation, CommitPhaseNone, errors.Join(errors.New("store returned not-committed outcome without error"), cause))
	case StoreOutcomeCommitUnknown:
		return newError(ErrorKindDependencyUnavailable, operation, CommitPhaseUnknown, errors.Join(errors.New("store returned commit-unknown outcome without error"), cause))
	default:
		return newError(ErrorKindDependencyUnavailable, operation, phaseForStoreOutcome(outcome), errors.New("store returned invalid outcome"))
	}
}

// phaseForStoreOutcome 映射 store outcome 可证明的 transition 提交阶段。
func phaseForStoreOutcome(outcome StoreOutcome) CommitPhase {
	switch outcome {
	case StoreOutcomeApplied, StoreOutcomeReplay:
		return CommitPhaseCommitted
	case StoreOutcomeCommitUnknown:
		return CommitPhaseUnknown
	default:
		return CommitPhaseNone
	}
}
