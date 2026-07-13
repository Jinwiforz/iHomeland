package personalworld

import (
	"context"
	"errors"

	"github.com/jinwiforz/ihomeland/server/internal/account"
)

// Service 编排 PersonalWorld primary 创建和 Owner-only lifecycle mutation。
//
// Service 不持有 socket、placement、storage transaction 或 mutable cache。所有持久并发
// 决议由 PersonalWorldRepository 原子完成，application 负责验证不可信 adapter result。
// Service 自身没有调用期可变状态；当注入的 repository、Clock 与 IDGenerator 满足各自
// 并发契约时，同一个实例可以被多个请求并发调用。
type Service struct {
	// repository 持有 owner unique、revision 与 idempotency 原子边界。
	repository PersonalWorldRepository
	// clock 为新 world 选择单次 UTC created time snapshot。
	clock Clock
	// ids 为每个候选 world 生成不可预测 identity 材料。
	ids IDGenerator
}

// NewService 校验并保留 PersonalWorld application 的全部必需依赖。
//
// 构造不会启动 goroutine 或执行 I/O；依赖的关闭与生命周期仍由 Composition Root 持有。
func NewService(repository PersonalWorldRepository, clock Clock, ids IDGenerator) (*Service, error) {
	if repository == nil || clock == nil || ids == nil {
		return nil, newError(ErrorKindValidation, OperationConstruct, CommitPhaseNone, errors.New("personal world service dependencies are incomplete"))
	}
	return &Service{repository: repository, clock: clock, ids: ids}, nil
}

// EnsurePrimaryResult 是 primary world ensure 的安全 application 结果。
type EnsurePrimaryResult struct {
	// world 是 repository 已提交或已经存在的唯一 primary world。
	world PersonalWorld
	// created 只表示本次调用明确完成了新 transaction。
	created bool
}

// World 返回 owner 的完整 PersonalWorld aggregate 值副本。
func (result EnsurePrimaryResult) World() PersonalWorld { return result.world }

// Created 报告本次调用是否明确创建了 primary world。
func (result EnsurePrimaryResult) Created() bool { return result.created }

// Valid 报告 result 是否包含合法 PersonalWorld。
func (result EnsurePrimaryResult) Valid() bool { return result.world.Valid() }

// EnsurePrimaryWorld 原子创建或解析 owner 唯一的 primary PersonalWorld。
//
// ownerID 必须来自账号或认证边界，不能从客户端 payload 自行构造。重复或并发调用由
// repository owner unique key 收敛到同一事实；候选 ID 只有在 created outcome 后才成为
// 持久事实。ctx 取消只停止调用方等待，不能证明 repository transaction 未提交；发生
// commit unknown 时不会补偿写入或声称未提交，调用方可再次 ensure 让 owner 唯一事实收敛。
func (service *Service) EnsurePrimaryWorld(ctx context.Context, ownerID account.PlayerID) (EnsurePrimaryResult, error) {
	if ctx == nil || !ownerID.Valid() {
		return EnsurePrimaryResult{}, newError(ErrorKindValidation, OperationEnsurePrimary, CommitPhaseNone, nil)
	}
	worldID, err := service.newPersonalWorldID()
	if err != nil {
		return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, CommitPhaseNone, err)
	}
	candidate, err := NewPersonalWorld(worldID, ownerID, service.clock.Now())
	if err != nil {
		return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, CommitPhaseNone, err)
	}
	snapshot, outcome, repositoryErr := service.repository.EnsurePrimary(ctx, candidate.Snapshot())
	if repositoryErr != nil {
		return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, phaseForEnsureOutcome(outcome), repositoryErr)
	}
	switch outcome {
	case EnsureOutcomeCreated:
		world, hydrateErr := validateEnsuredWorld(snapshot, ownerID)
		if hydrateErr != nil || !snapshot.Equal(candidate.Snapshot()) {
			return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, CommitPhaseCommitted, hydrateErr)
		}
		return EnsurePrimaryResult{world: world, created: true}, nil
	case EnsureOutcomeExisting:
		world, hydrateErr := validateEnsuredWorld(snapshot, ownerID)
		if hydrateErr != nil {
			return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, CommitPhaseNone, hydrateErr)
		}
		return EnsurePrimaryResult{world: world, created: false}, nil
	case EnsureOutcomeCommitUnknown:
		if !snapshot.empty() {
			return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, CommitPhaseUnknown, errors.New("repository returned snapshot for commit unknown"))
		}
		return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, CommitPhaseUnknown, nil)
	case EnsureOutcomeNotCommitted:
		if !snapshot.empty() {
			return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, CommitPhaseNone, errors.New("repository returned snapshot for not committed"))
		}
		return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, CommitPhaseNone, nil)
	default:
		return EnsurePrimaryResult{}, newError(ErrorKindDependencyUnavailable, OperationEnsurePrimary, CommitPhaseNone, errors.New("repository returned invalid ensure outcome"))
	}
}

// ArchiveWorld 以 Owner 身份、expected revision 和 idempotency identity 归档世界。
//
// command.actorID 必须来自可信认证上下文。Service 先读取 immutable owner facts 并完成授权，
// 防止非 Owner 通过 key 冲突探测幂等记录；随后把 idempotency replay/conflict、revision 与
// lifecycle 的最终顺序交给单个 repository transaction。这样首次提交后响应丢失的重试会
// 返回原结果，不会因当前世界已经 archived 而被误判为新的 invalid-state 请求。ctx 取消
// 同样不能证明 mutation 未提交，恢复动作必须依据返回的 CommitPhase 并复用原 key。
func (service *Service) ArchiveWorld(ctx context.Context, command ArchiveCommand) (PersonalWorld, error) {
	if ctx == nil || !command.Valid() {
		return PersonalWorld{}, newError(ErrorKindValidation, OperationArchive, CommitPhaseNone, nil)
	}
	snapshot, outcome, repositoryErr := service.repository.FindByID(ctx, command.WorldID())
	if repositoryErr != nil {
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseNone, repositoryErr)
	}
	switch outcome {
	case FindOutcomeNotFound:
		if !snapshot.empty() {
			return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseNone, errors.New("repository returned snapshot for not found"))
		}
		return PersonalWorld{}, newError(ErrorKindNotFound, OperationArchive, CommitPhaseNone, nil)
	case FindOutcomeFound:
		// 继续执行严格 hydration 与 owner authorization。
	default:
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseNone, errors.New("repository returned invalid find outcome"))
	}
	current, err := HydratePersonalWorld(snapshot)
	if err != nil || current.ID() != command.WorldID() {
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseNone, err)
	}
	if current.OwnerID() != command.ActorID() {
		return PersonalWorld{}, newError(ErrorKindForbidden, OperationArchive, CommitPhaseNone, nil)
	}
	record, err := NewArchiveRecord(command, current.Snapshot())
	if err != nil {
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseNone, err)
	}
	// 正常首次提交路径必须与 aggregate transition 得到完全相同的 target；replay/stale
	// 路径由 repository 先查询 idempotency record，再决定 revision 或 lifecycle outcome。
	if current.Lifecycle() == LifecycleActive && current.Revision() == command.ExpectedRevision() {
		transitioned, transitionErr := current.archive()
		if transitionErr != nil || !transitioned.Snapshot().Equal(record.Target()) {
			return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseNone, transitionErr)
		}
	}
	result, mutationOutcome, mutationErr := service.repository.CommitArchive(ctx, record)
	if mutationErr != nil {
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, phaseForMutationOutcome(mutationOutcome), mutationErr)
	}
	switch mutationOutcome {
	case MutationOutcomeApplied, MutationOutcomeReplay:
		return validateMutationResult(result, record, mutationOutcome)
	case MutationOutcomeNotFound:
		if !result.empty() {
			return malformedNonSuccessMutationResult()
		}
		return PersonalWorld{}, newError(ErrorKindNotFound, OperationArchive, CommitPhaseNone, nil)
	case MutationOutcomeRevisionConflict:
		if !result.empty() {
			return malformedNonSuccessMutationResult()
		}
		return PersonalWorld{}, newError(ErrorKindRevisionConflict, OperationArchive, CommitPhaseNone, nil)
	case MutationOutcomeIdempotencyConflict:
		if !result.empty() {
			return malformedNonSuccessMutationResult()
		}
		return PersonalWorld{}, newError(ErrorKindIdempotencyConflict, OperationArchive, CommitPhaseNone, nil)
	case MutationOutcomeInvalidState:
		if !result.empty() {
			return malformedNonSuccessMutationResult()
		}
		return PersonalWorld{}, newError(ErrorKindInvalidState, OperationArchive, CommitPhaseNone, nil)
	case MutationOutcomeCommitUnknown:
		if !result.empty() {
			return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseUnknown, errors.New("repository returned result for commit unknown"))
		}
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseUnknown, nil)
	case MutationOutcomeNotCommitted:
		if !result.empty() {
			return malformedNonSuccessMutationResult()
		}
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseNone, nil)
	default:
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseNone, errors.New("repository returned invalid mutation outcome"))
	}
}

// malformedNonSuccessMutationResult 返回 repository outcome/result 组合违约错误。
func malformedNonSuccessMutationResult() (PersonalWorld, error) {
	return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseNone, errors.New("repository returned result for non-success mutation outcome"))
}

// newPersonalWorldID 添加 PersonalWorld namespace，并把 generator 违约视为依赖故障。
func (service *Service) newPersonalWorldID() (PersonalWorldID, error) {
	material, err := service.ids.NewID()
	if err != nil {
		return PersonalWorldID{}, err
	}
	return NewPersonalWorldID(personalWorldIDPrefix + material)
}

// validateEnsuredWorld 拒绝 repository 返回其他 owner 或 malformed snapshot。
func validateEnsuredWorld(snapshot Snapshot, ownerID account.PlayerID) (PersonalWorld, error) {
	world, err := HydratePersonalWorld(snapshot)
	if err != nil {
		return PersonalWorld{}, err
	}
	if world.OwnerID() != ownerID {
		return PersonalWorld{}, errors.New("repository returned world for different owner")
	}
	return world, nil
}

// validateMutationResult 确认 applied/replay result 与本次规范 command 完全一致。
func validateMutationResult(result MutationResult, record ArchiveRecord, outcome MutationOutcome) (PersonalWorld, error) {
	if !result.Valid() || !result.Fingerprint().Equal(record.Fingerprint()) || !result.World().Equal(record.Target()) {
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseCommitted, errors.New("repository returned malformed mutation result"))
	}
	world, err := HydratePersonalWorld(result.World())
	if err != nil {
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseCommitted, err)
	}
	if outcome != MutationOutcomeApplied && outcome != MutationOutcomeReplay {
		return PersonalWorld{}, newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseCommitted, errors.New("mutation result has invalid outcome"))
	}
	return world, nil
}

// phaseForEnsureOutcome 把 repository error 与已知 create 提交状态对齐。
func phaseForEnsureOutcome(outcome EnsureOutcome) CommitPhase {
	switch outcome {
	case EnsureOutcomeCreated:
		return CommitPhaseCommitted
	case EnsureOutcomeCommitUnknown:
		return CommitPhaseUnknown
	default:
		return CommitPhaseNone
	}
}

// phaseForMutationOutcome 把 repository error 与已知 mutation 提交状态对齐。
func phaseForMutationOutcome(outcome MutationOutcome) CommitPhase {
	switch outcome {
	case MutationOutcomeApplied, MutationOutcomeReplay:
		return CommitPhaseCommitted
	case MutationOutcomeCommitUnknown:
		return CommitPhaseUnknown
	default:
		return CommitPhaseNone
	}
}
