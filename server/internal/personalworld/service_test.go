package personalworld

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
)

// TestEnsurePrimaryWorldConcurrent 验证 owner unique 边界在并发下只提交一个世界。
func TestEnsurePrimaryWorldConcurrent(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, "ensure-race")
	const workers = 24
	results := make(chan EnsurePrimaryResult, workers)
	errorsChannel := make(chan error, workers)
	var waitGroup sync.WaitGroup
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := fixture.service.EnsurePrimaryWorld(context.Background(), fixture.owner)
			results <- result
			errorsChannel <- err
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("EnsurePrimaryWorld() error = %v", err)
		}
	}
	var worldID PersonalWorldID
	created := 0
	for result := range results {
		if !result.Valid() || result.World().OwnerID() != fixture.owner {
			t.Fatalf("invalid ensure result: %+v", result)
		}
		if result.Created() {
			created++
		}
		if !worldID.Valid() {
			worldID = result.World().ID()
		}
		if result.World().ID() != worldID {
			t.Fatalf("different primary world: %s != %s", result.World().ID(), worldID)
		}
	}
	if created != 1 || fixture.repository.worldCount() != 1 {
		t.Fatalf("created=%d worlds=%d", created, fixture.repository.worldCount())
	}
}

// TestEnsurePrimaryWorldCommitUnknownConverges 验证未知提交通过 owner unique key 安全收敛。
func TestEnsurePrimaryWorldCommitUnknownConverges(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, "ensure-unknown")
	fixture.repository.setEnsureCommitUnknown()
	if result, err := fixture.service.EnsurePrimaryWorld(context.Background(), fixture.owner); result.Valid() || ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseUnknown {
		t.Fatalf("first result=%+v kind=%v phase=%v err=%v", result, ErrorKindOf(err), CommitPhaseOf(err), err)
	}
	result, err := fixture.service.EnsurePrimaryWorld(context.Background(), fixture.owner)
	if err != nil || !result.Valid() || result.Created() || fixture.repository.worldCount() != 1 {
		t.Fatalf("retry result=%+v worlds=%d err=%v", result, fixture.repository.worldCount(), err)
	}
}

// TestEnsurePrimaryWorldRejectsInvalidDependencies 覆盖 generator、context 和 repository 违约。
func TestEnsurePrimaryWorldRejectsInvalidDependencies(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, "ensure-invalid")
	if result, err := fixture.service.EnsurePrimaryWorld(context.Background(), mustPlayerIDZero()); result.Valid() || ErrorKindOf(err) != ErrorKindValidation {
		t.Fatalf("zero owner result=%+v err=%v", result, err)
	}
	invalidService, err := NewService(fixture.repository, fixture.clock, invalidIDGenerator{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := invalidService.EnsurePrimaryWorld(context.Background(), fixture.owner); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseNone {
		t.Fatalf("invalid generator err=%v", err)
	}
	cause := errors.New("entropy unavailable with sensitive material")
	failingService, err := NewService(fixture.repository, fixture.clock, failingIDGenerator{err: cause})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := failingService.EnsurePrimaryWorld(context.Background(), fixture.owner); !errors.Is(err, cause) || strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("dependency cause handling err=%v", err)
	}

	createdFixture := newServiceFixture(t, "ensure-malformed-created")
	createdFixture.repository.setEnsureOverride(Snapshot{}, EnsureOutcomeCreated, nil)
	if _, err := createdFixture.service.EnsurePrimaryWorld(context.Background(), createdFixture.owner); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseCommitted {
		t.Fatalf("malformed created err=%v", err)
	}
	committedCause := errors.New("post-commit dependency failure")
	committedFixture := newServiceFixture(t, "ensure-committed-error")
	committedFixture.repository.setEnsureOverride(Snapshot{}, EnsureOutcomeCreated, committedCause)
	if _, err := committedFixture.service.EnsurePrimaryWorld(context.Background(), committedFixture.owner); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseCommitted || !errors.Is(err, committedCause) {
		t.Fatalf("committed dependency err=%v", err)
	}

	existingFixture := newServiceFixture(t, "ensure-wrong-owner")
	otherWorld := mustWorld(t, "wrongowner", mustPlayerID(t, "other-owner"))
	existingFixture.repository.setEnsureOverride(otherWorld.Snapshot(), EnsureOutcomeExisting, nil)
	if _, err := existingFixture.service.EnsurePrimaryWorld(context.Background(), existingFixture.owner); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseNone {
		t.Fatalf("wrong owner err=%v", err)
	}

	cancelledFixture := newServiceFixture(t, "ensure-cancelled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cancelledFixture.service.EnsurePrimaryWorld(ctx, cancelledFixture.owner); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseNone || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err=%v", err)
	}

	collisionFixture := newServiceFixture(t, "ensure-collision")
	collisionFixture.ensure(t)
	collisionService, err := NewService(collisionFixture.repository, collisionFixture.clock, &fakeIDGenerator{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := collisionService.EnsurePrimaryWorld(context.Background(), mustPlayerID(t, "collision-owner")); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseNone || collisionFixture.repository.worldCount() != 1 {
		t.Fatalf("identifier collision worlds=%d err=%v", collisionFixture.repository.worldCount(), err)
	}
}

// TestArchiveWorldSuccessReplayAndAuthorization 验证 Owner archive、replay 与授权优先级。
func TestArchiveWorldSuccessReplayAndAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, "archive")
	ensured := fixture.ensure(t)
	key := mustIdempotencyKey(t, "archive:success:0001")
	command, err := NewArchiveCommand(fixture.owner, ensured.World().ID(), ensured.World().Revision(), key)
	if err != nil {
		t.Fatalf("NewArchiveCommand() error = %v", err)
	}
	archived, err := fixture.service.ArchiveWorld(context.Background(), command)
	if err != nil || archived.Lifecycle() != LifecycleArchived || archived.Revision() != InitialRevision+1 || archived.OwnerID() != fixture.owner {
		t.Fatalf("archived=%+v err=%v", archived.Snapshot(), err)
	}
	replayed, err := fixture.service.ArchiveWorld(context.Background(), command)
	if err != nil || !replayed.Snapshot().Equal(archived.Snapshot()) || fixture.repository.mutationCount() != 1 {
		t.Fatalf("replayed=%+v mutations=%d err=%v", replayed.Snapshot(), fixture.repository.mutationCount(), err)
	}

	otherActor := mustPlayerID(t, "visitor")
	unauthorized := mustArchiveCommand(t, otherActor, archived.ID(), InitialRevision, mustIdempotencyKey(t, "archive:visitor:0001"))
	if _, err := fixture.service.ArchiveWorld(context.Background(), unauthorized); ErrorKindOf(err) != ErrorKindForbidden {
		t.Fatalf("unauthorized err=%v", err)
	}

	differentCommand := mustArchiveCommand(t, fixture.owner, archived.ID(), archived.Revision(), key)
	if _, err := fixture.service.ArchiveWorld(context.Background(), differentCommand); ErrorKindOf(err) != ErrorKindIdempotencyConflict {
		t.Fatalf("same key different command err=%v", err)
	}

	newCommand := mustArchiveCommand(t, fixture.owner, archived.ID(), archived.Revision(), mustIdempotencyKey(t, "archive:new-key:0001"))
	if _, err := fixture.service.ArchiveWorld(context.Background(), newCommand); ErrorKindOf(err) != ErrorKindInvalidState {
		t.Fatalf("terminal mutation err=%v", err)
	}

	notFound := mustArchiveCommand(t, fixture.owner, mustWorldID(t, "missing"), InitialRevision, mustIdempotencyKey(t, "archive:missing:001"))
	if _, err := fixture.service.ArchiveWorld(context.Background(), notFound); ErrorKindOf(err) != ErrorKindNotFound {
		t.Fatalf("not found err=%v", err)
	}
}

// TestArchiveWorldRevisionCompetition 验证不同 key 竞争同一 revision 时最多一个提交。
func TestArchiveWorldRevisionCompetition(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, "archive-race")
	world := fixture.ensure(t).World()
	commands := make([]ArchiveCommand, 2)
	commands[0] = mustArchiveCommand(t, fixture.owner, world.ID(), world.Revision(), mustIdempotencyKey(t, "archive:race:0001"))
	commands[1] = mustArchiveCommand(t, fixture.owner, world.ID(), world.Revision(), mustIdempotencyKey(t, "archive:race:0002"))
	results := make(chan PersonalWorld, len(commands))
	errorsChannel := make(chan error, len(commands))
	var waitGroup sync.WaitGroup
	for _, command := range commands {
		waitGroup.Add(1)
		go func(command ArchiveCommand) {
			defer waitGroup.Done()
			result, err := fixture.service.ArchiveWorld(context.Background(), command)
			results <- result
			errorsChannel <- err
		}(command)
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)
	succeeded := 0
	conflicted := 0
	for result := range results {
		if result.Valid() {
			succeeded++
		}
	}
	for err := range errorsChannel {
		if ErrorKindOf(err) == ErrorKindRevisionConflict {
			conflicted++
		} else if err != nil {
			t.Fatalf("unexpected error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 || fixture.repository.mutationCount() != 1 {
		t.Fatalf("succeeded=%d conflicted=%d mutations=%d", succeeded, conflicted, fixture.repository.mutationCount())
	}
}

// TestArchiveWorldCommitUnknownCanReplay 验证提交后响应丢失不会重复增加 revision。
func TestArchiveWorldCommitUnknownCanReplay(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, "archive-unknown")
	world := fixture.ensure(t).World()
	command := mustArchiveCommand(t, fixture.owner, world.ID(), world.Revision(), mustIdempotencyKey(t, "archive:unknown:001"))
	fixture.repository.setMutationCommitUnknown()
	if result, err := fixture.service.ArchiveWorld(context.Background(), command); result.Valid() || ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseUnknown {
		t.Fatalf("first result=%+v err=%v", result, err)
	}
	replayed, err := fixture.service.ArchiveWorld(context.Background(), command)
	if err != nil || replayed.Revision() != InitialRevision+1 || fixture.repository.mutationCount() != 1 {
		t.Fatalf("replay=%+v mutations=%d err=%v", replayed.Snapshot(), fixture.repository.mutationCount(), err)
	}
}

// TestArchiveWorldScopesIdempotencyByOwner 验证相同文本 key 不会在不同 Owner 之间互相阻塞。
func TestArchiveWorldScopesIdempotencyByOwner(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, "archive-owner-scope")
	first := fixture.ensure(t).World()
	secondOwner := mustPlayerID(t, "second-owner")
	secondResult, err := fixture.service.EnsurePrimaryWorld(context.Background(), secondOwner)
	if err != nil || !secondResult.Valid() || !secondResult.Created() {
		t.Fatalf("second ensure result=%+v err=%v", secondResult, err)
	}
	key := mustIdempotencyKey(t, "archive:shared-owner-key")
	firstCommand := mustArchiveCommand(t, fixture.owner, first.ID(), first.Revision(), key)
	secondCommand := mustArchiveCommand(t, secondOwner, secondResult.World().ID(), secondResult.World().Revision(), key)
	firstArchived, firstErr := fixture.service.ArchiveWorld(context.Background(), firstCommand)
	secondArchived, secondErr := fixture.service.ArchiveWorld(context.Background(), secondCommand)
	if firstErr != nil || secondErr != nil || firstArchived.Lifecycle() != LifecycleArchived || secondArchived.Lifecycle() != LifecycleArchived || fixture.repository.mutationCount() != 2 {
		t.Fatalf("first=%v second=%v mutations=%d firstErr=%v secondErr=%v", firstArchived.Valid(), secondArchived.Valid(), fixture.repository.mutationCount(), firstErr, secondErr)
	}
}

// TestArchiveWorldValidatesRepositoryResults 覆盖 find/mutation outcome 与 payload 的矛盾组合。
func TestArchiveWorldValidatesRepositoryResults(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, "archive-result")
	world := fixture.ensure(t).World()
	command := mustArchiveCommand(t, fixture.owner, world.ID(), world.Revision(), mustIdempotencyKey(t, "archive:result:001"))

	fixture.repository.setFindOverride(Snapshot{}, FindOutcomeFound, nil)
	if _, err := fixture.service.ArchiveWorld(context.Background(), command); ErrorKindOf(err) != ErrorKindDependencyUnavailable {
		t.Fatalf("malformed found err=%v", err)
	}
	fixture.repository.clearFindOverride()
	fixture.repository.setFindOverride(world.Snapshot(), FindOutcomeNotFound, nil)
	if _, err := fixture.service.ArchiveWorld(context.Background(), command); ErrorKindOf(err) != ErrorKindDependencyUnavailable {
		t.Fatalf("not found with snapshot err=%v", err)
	}
	fixture.repository.clearFindOverride()

	fixture.repository.setMutationOverride(MutationResult{}, MutationOutcomeApplied, nil)
	if _, err := fixture.service.ArchiveWorld(context.Background(), command); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseCommitted {
		t.Fatalf("applied with zero result err=%v", err)
	}
	validTarget := mustSnapshot(t, world.ID(), world.OwnerID(), LifecycleArchived, world.Revision()+1, world.CreatedAt())
	validResult := mustMutationResult(t, validTarget, fingerprintArchiveCommand(world.ID(), world.OwnerID().String(), world.Revision(), LifecycleArchived))
	fixture.repository.setMutationOverride(validResult, MutationOutcomeRevisionConflict, nil)
	if _, err := fixture.service.ArchiveWorld(context.Background(), command); ErrorKindOf(err) != ErrorKindDependencyUnavailable {
		t.Fatalf("conflict with result err=%v", err)
	}

	cause := errors.New("repository failed with archive:result:001")
	fixture.repository.setMutationOverride(MutationResult{}, MutationOutcomeCommitUnknown, cause)
	if _, err := fixture.service.ArchiveWorld(context.Background(), command); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseUnknown || !errors.Is(err, cause) || strings.Contains(err.Error(), "archive:result:001") {
		t.Fatalf("commit unknown err=%v", err)
	}
	committedCause := errors.New("archive committed before dependency failure")
	fixture.repository.setMutationOverride(MutationResult{}, MutationOutcomeApplied, committedCause)
	if _, err := fixture.service.ArchiveWorld(context.Background(), command); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseCommitted || !errors.Is(err, committedCause) {
		t.Fatalf("committed dependency err=%v", err)
	}

	fixture.repository.setMutationOverride(MutationResult{}, MutationOutcomeUnspecified, nil)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.service.ArchiveWorld(cancelled, command); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseNone || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err=%v", err)
	}
}

// TestArchiveWorldMapsStableBusinessOutcomes 验证 repository 决议不会被误映射为 dependency。
func TestArchiveWorldMapsStableBusinessOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		outcome MutationOutcome
		kind    ErrorKind
		phase   CommitPhase
	}{
		{name: "not found", outcome: MutationOutcomeNotFound, kind: ErrorKindNotFound},
		{name: "revision", outcome: MutationOutcomeRevisionConflict, kind: ErrorKindRevisionConflict},
		{name: "idempotency", outcome: MutationOutcomeIdempotencyConflict, kind: ErrorKindIdempotencyConflict},
		{name: "state", outcome: MutationOutcomeInvalidState, kind: ErrorKindInvalidState},
		{name: "not committed", outcome: MutationOutcomeNotCommitted, kind: ErrorKindDependencyUnavailable},
		{name: "unknown", outcome: MutationOutcomeCommitUnknown, kind: ErrorKindDependencyUnavailable, phase: CommitPhaseUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t, "map-"+strings.ReplaceAll(test.name, " ", ""))
			world := fixture.ensure(t).World()
			command := mustArchiveCommand(t, fixture.owner, world.ID(), world.Revision(), mustIdempotencyKey(t, "archive:map:"+fmt.Sprintf("%04d", test.outcome)))
			fixture.repository.setMutationOverride(MutationResult{}, test.outcome, nil)
			if _, err := fixture.service.ArchiveWorld(context.Background(), command); ErrorKindOf(err) != test.kind || CommitPhaseOf(err) != test.phase {
				t.Fatalf("kind=%v phase=%v err=%v", ErrorKindOf(err), CommitPhaseOf(err), err)
			}
		})
	}
}

// TestServiceConstructionAndErrorSafety 保护 nil dependency、stable metadata 与 cause 脱敏。
func TestServiceConstructionAndErrorSafety(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, "construct")
	constructors := []func() (*Service, error){
		func() (*Service, error) { return NewService(nil, fixture.clock, fixture.ids) },
		func() (*Service, error) { return NewService(fixture.repository, nil, fixture.ids) },
		func() (*Service, error) { return NewService(fixture.repository, fixture.clock, nil) },
	}
	for _, construct := range constructors {
		service, err := construct()
		if service != nil || ErrorKindOf(err) != ErrorKindValidation {
			t.Fatalf("service=%v err=%v", service, err)
		}
	}
	cause := errors.New("repository snapshot includes private world facts")
	err := newError(ErrorKindDependencyUnavailable, OperationArchive, CommitPhaseUnknown, cause)
	var failure *Error
	if !errors.As(err, &failure) || !errors.Is(err, cause) || failure.Kind() != ErrorKindDependencyUnavailable || failure.Operation() != OperationArchive || failure.Phase() != CommitPhaseUnknown {
		t.Fatalf("error metadata = %#v", failure)
	}
	if strings.Contains(err.Error(), cause.Error()) || strings.Contains(err.Error(), "private world") {
		t.Fatalf("error leaked cause: %v", err)
	}
}

// TestErrorMetadataStableNames 固定 adapter、指标与恢复逻辑依赖的低基数错误名称。
func TestErrorMetadataStableNames(t *testing.T) {
	t.Parallel()
	kinds := []struct {
		value ErrorKind
		name  string
	}{
		{value: ErrorKindUnspecified, name: "unspecified"},
		{value: ErrorKindValidation, name: "validation"},
		{value: ErrorKindNotFound, name: "not_found"},
		{value: ErrorKindForbidden, name: "forbidden"},
		{value: ErrorKindRevisionConflict, name: "revision_conflict"},
		{value: ErrorKindIdempotencyConflict, name: "idempotency_conflict"},
		{value: ErrorKindInvalidState, name: "invalid_state"},
		{value: ErrorKindDependencyUnavailable, name: "dependency_unavailable"},
		{value: ErrorKind(255), name: "unspecified"},
	}
	for _, kind := range kinds {
		if kind.value.String() != kind.name {
			t.Fatalf("ErrorKind(%d) = %q", kind.value, kind.value.String())
		}
	}
	operations := []struct {
		value Operation
		name  string
	}{
		{value: OperationUnspecified, name: "unspecified"},
		{value: OperationConstruct, name: "construct"},
		{value: OperationEnsurePrimary, name: "ensure_primary"},
		{value: OperationArchive, name: "archive"},
		{value: Operation(255), name: "unspecified"},
	}
	for _, operation := range operations {
		if operation.value.String() != operation.name {
			t.Fatalf("Operation(%d) = %q", operation.value, operation.value.String())
		}
	}
	phases := []struct {
		value CommitPhase
		name  string
	}{
		{value: CommitPhaseNone, name: "none"},
		{value: CommitPhaseUnknown, name: "world_commit_unknown"},
		{value: CommitPhaseCommitted, name: "world_committed"},
		{value: CommitPhase(255), name: "none"},
	}
	for _, phase := range phases {
		if phase.value.String() != phase.name {
			t.Fatalf("CommitPhase(%d) = %q", phase.value, phase.value.String())
		}
	}
	foreign := errors.New("foreign error")
	if ErrorKindOf(foreign) != ErrorKindUnspecified || CommitPhaseOf(foreign) != CommitPhaseNone {
		t.Fatal("foreign error received PersonalWorld metadata")
	}
}

// serviceFixture 组合单个测试用例需要的 deterministic application dependencies。
type serviceFixture struct {
	// owner 是所有正常用例使用的 immutable WorldOwnerID。
	owner account.PlayerID
	// repository 提供并发安全的 owner/revision/idempotency reference semantics。
	repository *referenceRepository
	// clock 返回固定 UTC 创建时间。
	clock *fixedClock
	// ids 为并发 ensure 生成不同候选 ID。
	ids *fakeIDGenerator
	// service 是待测 PersonalWorld application。
	service *Service
}

// newServiceFixture 创建不接触 listener 或真实 storage 的完整测试边界。
func newServiceFixture(t testing.TB, suffix string) *serviceFixture {
	t.Helper()
	owner := mustPlayerID(t, suffix)
	repository := newReferenceRepository()
	clock := &fixedClock{now: time.Unix(1_750_000_000, 0).UTC()}
	ids := &fakeIDGenerator{}
	service, err := NewService(repository, clock, ids)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return &serviceFixture{owner: owner, repository: repository, clock: clock, ids: ids, service: service}
}

// ensure 创建 fixture owner 的 primary world 并要求本次明确提交。
func (fixture *serviceFixture) ensure(t testing.TB) EnsurePrimaryResult {
	t.Helper()
	result, err := fixture.service.EnsurePrimaryWorld(context.Background(), fixture.owner)
	if err != nil || !result.Valid() || !result.Created() {
		t.Fatalf("EnsurePrimaryWorld() result=%+v err=%v", result, err)
	}
	return result
}

// fixedClock 为所有创建操作提供不可变绝对时间。
type fixedClock struct {
	// now 是测试显式选择的 UTC snapshot。
	now time.Time
}

// Now 返回固定时间值副本，可被多个 goroutine 安全读取。
func (clock *fixedClock) Now() time.Time { return clock.now }

// fakeIDGenerator 以 atomic sequence 生成并发安全的字母数字材料。
type fakeIDGenerator struct {
	// next 确保每次调用使用不同候选 identity。
	next atomic.Uint64
}

// NewID 返回满足 PersonalWorldID 字符约束的固定宽度材料。
func (generator *fakeIDGenerator) NewID() (string, error) {
	return fmt.Sprintf("world%016d", generator.next.Add(1)), nil
}

// failingIDGenerator 模拟 Composition Root CSPRNG 失败。
type failingIDGenerator struct {
	// err 是每次 NewID 返回的稳定依赖故障。
	err error
}

// NewID 返回依赖故障且不产生可用 identity。
func (generator failingIDGenerator) NewID() (string, error) { return "", generator.err }

// invalidIDGenerator 模拟 generator 返回不安全材料但没有报告错误。
type invalidIDGenerator struct{}

// NewID 返回会被 PersonalWorldID constructor 拒绝的材料。
func (invalidIDGenerator) NewID() (string, error) { return "bad-id", nil }

// idempotencyEntry 保存 archive transaction 已提交的 command/result 对。
type idempotencyEntry struct {
	// fingerprint 决定相同 key 是 replay 还是 conflict。
	fingerprint CommandFingerprint
	// result 是 replay 必须返回的原始已提交 snapshot。
	result MutationResult
}

// idempotencyIndex 把客户端可重复的 key 限定在可信 Owner identity 内。
type idempotencyIndex struct {
	// actorID 是已通过 application Owner authorization 的 PlayerID。
	actorID account.PlayerID
	// key 是 repository 原子索引使用的原始 command identity。
	key string
}

// referenceRepository 以单 mutex 模拟 owner、revision 与 idempotency 的原子 transaction。
//
// 所有 map 和 failure injection 字段都由同一 mutex 保护，使并发测试能由 race detector 验证；
// 它只证明消费侧契约可实现，不模拟生产数据库隔离级别、连接池或持久恢复。
type referenceRepository struct {
	// mutex 保护全部 world、idempotency 和 failure injection 状态。
	mutex sync.Mutex
	// worldsByOwner 使用 immutable owner unique key 线性化 primary 创建。
	worldsByOwner map[string]Snapshot
	// ownersByWorld 让 PersonalWorldID 定位当前 snapshot 的 owner key。
	ownersByWorld map[string]string
	// idempotency 保存与 archive mutation 同事务提交的 replay result。
	idempotency map[idempotencyIndex]idempotencyEntry
	// ensureOverrideEnabled 让测试注入任意 ensure result 组合。
	ensureOverrideEnabled bool
	// ensureSnapshot 是覆盖路径返回的 snapshot。
	ensureSnapshot Snapshot
	// ensureOutcome 是覆盖路径返回的 create 决议。
	ensureOutcome EnsureOutcome
	// ensureErr 是覆盖路径返回的依赖故障。
	ensureErr error
	// ensureCommitUnknown 让下一次 ensure 提交后模拟响应丢失。
	ensureCommitUnknown bool
	// findOverrideEnabled 让测试注入任意 find result 组合。
	findOverrideEnabled bool
	// findSnapshot 是覆盖路径返回的 snapshot。
	findSnapshot Snapshot
	// findOutcome 是覆盖路径返回的读取决议。
	findOutcome FindOutcome
	// findErr 是覆盖路径返回的依赖故障。
	findErr error
	// mutationOverrideEnabled 让测试注入任意 mutation result 组合。
	mutationOverrideEnabled bool
	// mutationResult 是覆盖路径返回的已提交事实。
	mutationResult MutationResult
	// mutationOutcome 是覆盖路径返回的 transaction 决议。
	mutationOutcome MutationOutcome
	// mutationErr 是覆盖路径返回的依赖故障。
	mutationErr error
	// mutationCommitUnknown 让下一次 archive 提交后模拟响应丢失。
	mutationCommitUnknown bool
}

// newReferenceRepository 创建空且不会进入 production graph 的测试 repository。
func newReferenceRepository() *referenceRepository {
	return &referenceRepository{
		worldsByOwner: make(map[string]Snapshot),
		ownersByWorld: make(map[string]string),
		idempotency:   make(map[idempotencyIndex]idempotencyEntry),
	}
}

// EnsurePrimary 原子返回 owner 的既有 world 或提交候选 snapshot。
//
// ctx 在进入临界区前检查；一旦写入 map，后续注入的响应丢失只能返回 CommitUnknown，不能
// 回滚已提交事实。方法持锁期间不调用外部代码。
func (repository *referenceRepository) EnsurePrimary(ctx context.Context, candidate Snapshot) (Snapshot, EnsureOutcome, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, EnsureOutcomeNotCommitted, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.ensureOverrideEnabled {
		return repository.ensureSnapshot, repository.ensureOutcome, repository.ensureErr
	}
	if !candidate.Valid() {
		return Snapshot{}, EnsureOutcomeNotCommitted, errors.New("candidate snapshot is invalid")
	}
	ownerKey := candidate.OwnerID().String()
	if existing, found := repository.worldsByOwner[ownerKey]; found {
		return existing, EnsureOutcomeExisting, nil
	}
	if _, collision := repository.ownersByWorld[candidate.ID().String()]; collision {
		return Snapshot{}, EnsureOutcomeNotCommitted, errors.New("personal world identifier collision")
	}
	repository.worldsByOwner[ownerKey] = candidate
	repository.ownersByWorld[candidate.ID().String()] = ownerKey
	if repository.ensureCommitUnknown {
		repository.ensureCommitUnknown = false
		return Snapshot{}, EnsureOutcomeCommitUnknown, errors.New("ensure response lost after commit")
	}
	return candidate, EnsureOutcomeCreated, nil
}

// FindByID 返回 mutex 内读取的单一一致性 snapshot。
//
// Snapshot 是纯值，因此离开临界区后调用方不会持有 repository 内部可变引用。
func (repository *referenceRepository) FindByID(ctx context.Context, id PersonalWorldID) (Snapshot, FindOutcome, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, FindOutcomeUnspecified, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.findOverrideEnabled {
		return repository.findSnapshot, repository.findOutcome, repository.findErr
	}
	ownerKey, found := repository.ownersByWorld[id.String()]
	if !found {
		return Snapshot{}, FindOutcomeNotFound, nil
	}
	return repository.worldsByOwner[ownerKey], FindOutcomeFound, nil
}

// CommitArchive 先处理 idempotency，再原子比较 revision/lifecycle 并提交 target。
//
// 检查顺序刻意保证已提交 replay 优先于 terminal lifecycle；状态与 replay result 在同一锁内
// 写入，响应丢失发生在写入后，借此模拟数据库 commit 成功但调用方未收到响应的边界。
func (repository *referenceRepository) CommitArchive(ctx context.Context, record ArchiveRecord) (MutationResult, MutationOutcome, error) {
	if err := ctx.Err(); err != nil {
		return MutationResult{}, MutationOutcomeNotCommitted, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.mutationOverrideEnabled {
		return repository.mutationResult, repository.mutationOutcome, repository.mutationErr
	}
	if !record.Valid() {
		return MutationResult{}, MutationOutcomeNotCommitted, errors.New("archive record is invalid")
	}
	key := idempotencyIndex{actorID: record.Command().ActorID(), key: record.Command().IdempotencyKey().Value()}
	if existing, found := repository.idempotency[key]; found {
		if existing.fingerprint.Equal(record.Fingerprint()) {
			return existing.result, MutationOutcomeReplay, nil
		}
		return MutationResult{}, MutationOutcomeIdempotencyConflict, nil
	}
	ownerKey, found := repository.ownersByWorld[record.Command().WorldID().String()]
	if !found {
		return MutationResult{}, MutationOutcomeNotFound, nil
	}
	currentSnapshot := repository.worldsByOwner[ownerKey]
	if currentSnapshot.OwnerID() != record.Command().ActorID() {
		return MutationResult{}, MutationOutcomeNotCommitted, errors.New("archive actor does not own repository world")
	}
	if currentSnapshot.Revision() != record.Command().ExpectedRevision() {
		return MutationResult{}, MutationOutcomeRevisionConflict, nil
	}
	if currentSnapshot.Lifecycle() != LifecycleActive {
		return MutationResult{}, MutationOutcomeInvalidState, nil
	}
	current, err := HydratePersonalWorld(currentSnapshot)
	if err != nil {
		return MutationResult{}, MutationOutcomeNotCommitted, err
	}
	transitioned, err := current.archive()
	if err != nil || !transitioned.Snapshot().Equal(record.Target()) {
		return MutationResult{}, MutationOutcomeNotCommitted, errors.New("archive target violates domain transition")
	}
	result, err := NewMutationResult(record.Target(), record.Fingerprint())
	if err != nil {
		return MutationResult{}, MutationOutcomeNotCommitted, err
	}
	repository.worldsByOwner[ownerKey] = record.Target()
	repository.idempotency[key] = idempotencyEntry{fingerprint: record.Fingerprint(), result: result}
	if repository.mutationCommitUnknown {
		repository.mutationCommitUnknown = false
		return MutationResult{}, MutationOutcomeCommitUnknown, errors.New("archive response lost after commit")
	}
	return result, MutationOutcomeApplied, nil
}

// setEnsureOverride 配置后续 ensure 调用返回指定组合。
func (repository *referenceRepository) setEnsureOverride(snapshot Snapshot, outcome EnsureOutcome, err error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.ensureOverrideEnabled = true
	repository.ensureSnapshot = snapshot
	repository.ensureOutcome = outcome
	repository.ensureErr = err
}

// setEnsureCommitUnknown 配置下一次 ensure 在提交后丢失响应。
func (repository *referenceRepository) setEnsureCommitUnknown() {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.ensureCommitUnknown = true
}

// setFindOverride 配置后续 find 调用返回指定组合。
func (repository *referenceRepository) setFindOverride(snapshot Snapshot, outcome FindOutcome, err error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.findOverrideEnabled = true
	repository.findSnapshot = snapshot
	repository.findOutcome = outcome
	repository.findErr = err
}

// clearFindOverride 恢复 reference repository 正常读取路径。
func (repository *referenceRepository) clearFindOverride() {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.findOverrideEnabled = false
	repository.findSnapshot = Snapshot{}
	repository.findOutcome = FindOutcomeUnspecified
	repository.findErr = nil
}

// setMutationOverride 配置后续 archive 调用返回指定组合。
func (repository *referenceRepository) setMutationOverride(result MutationResult, outcome MutationOutcome, err error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.mutationOverrideEnabled = true
	repository.mutationResult = result
	repository.mutationOutcome = outcome
	repository.mutationErr = err
}

// setMutationCommitUnknown 配置下一次正常 archive 在提交后丢失响应。
func (repository *referenceRepository) setMutationCommitUnknown() {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.mutationCommitUnknown = true
}

// worldCount 返回 owner unique primary world 数量。
func (repository *referenceRepository) worldCount() int {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return len(repository.worldsByOwner)
}

// mutationCount 返回已经原子提交的唯一 idempotency result 数量。
func (repository *referenceRepository) mutationCount() int {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return len(repository.idempotency)
}

// mustPlayerIDZero 返回 account.PlayerID 的禁止零值，用于 validation 测试。
func mustPlayerIDZero() account.PlayerID { return account.PlayerID{} }
