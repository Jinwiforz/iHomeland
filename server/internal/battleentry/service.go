package battleentry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// WorldEnsurer 是 BattleTicket own-world role 所需的 PersonalWorld owner 窄端口。
type WorldEnsurer interface {
	// EnsurePrimary 返回 actor 唯一 primary world 的持久事实。
	EnsurePrimary(ctx context.Context, ownerID account.PlayerID) (personalworld.Snapshot, error)
}

// AssignmentReader 是 current active placement 事实的只读窄端口。
type AssignmentReader interface {
	// Resolve 返回 observedAt 时刻的 current assignment。
	Resolve(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error)
}

// VisitRoleOwner 是 Visitor battle role 所需的 VisitSession policy 窄端口。
type VisitRoleOwner interface {
	// ResolveBattleAuthority 返回当前 actor、lineage、assignment 与最早 deadline。
	ResolveBattleAuthority(ctx context.Context, authenticated session.AuthenticatedSession, visitID visitsession.VisitSessionID) (VisitAuthority, error)
}

// VisitSessionServiceAdapter 把 VisitSession owner 的 admission eligibility 收窄为 battle 只读投影。
type VisitSessionServiceAdapter struct {
	service *visitsession.Service
}

// NewVisitSessionServiceAdapter 校验并包装既有 VisitSession owner。
func NewVisitSessionServiceAdapter(service *visitsession.Service) (*VisitSessionServiceAdapter, error) {
	if service == nil {
		return nil, errors.New("visit session service is unavailable")
	}
	return &VisitSessionServiceAdapter{service: service}, nil
}

// ResolveBattleAuthority 委托领域 owner，且不复制 membership 或 aggregate 状态。
func (adapter *VisitSessionServiceAdapter) ResolveBattleAuthority(ctx context.Context, authenticated session.AuthenticatedSession, visitID visitsession.VisitSessionID) (VisitAuthority, error) {
	if adapter == nil || adapter.service == nil {
		return VisitAuthority{}, errors.New("visit session service is unavailable")
	}
	eligibility, err := adapter.service.ResolveAdmissionEligibility(ctx, authenticated, visitID)
	if err != nil {
		return VisitAuthority{}, err
	}
	authority := VisitAuthority{Intent: eligibility.Intent(), Revision: eligibility.Revision()}
	if !eligibility.Valid() || !authority.Valid() {
		return VisitAuthority{}, errors.New("visit session service returned malformed eligibility")
	}
	return authority, nil
}

// SimulationTargetResolver 解析完整 assignment stamp 对应的 current exact child target。
type SimulationTargetResolver interface {
	// ResolveSimulationTarget 不返回 endpoint 或 credential。
	ResolveSimulationTarget(ctx context.Context, stamp placement.AssignmentStamp) (simulationcontrol.SimulationTarget, bool, error)
}

// BattleEndpointProvider 返回只来自受信配置与 listener readiness 的 advertised UDP 地址。
type BattleEndpointProvider interface {
	// EndpointFor 返回 exact target 唯一允许发布的 endpoint。
	EndpointFor(ctx context.Context, target simulationcontrol.SimulationTarget) (battleticket.Endpoint, error)
}

// ActorCapacityOwner 原子拥有 exact instance 的 installed+active actor slots。
//
// 当前 adapter 可以由 fake 实现；后续 control change 必须把该端口接到 C++ hard-cap owner。
type ActorCapacityOwner interface {
	// Reserve 为相同 IssueID 与相同 target 幂等返回同一 slot，第 9 个 actor 必须拒绝。
	Reserve(ctx context.Context, request ReservationRequest) (Reservation, error)
	// Release 精确撤销尚未公开成功的 reservation，不能影响 successor target。
	Release(ctx context.Context, reservation Reservation) error
}

// TicketIssuer 是 digest/handle-only issuance owner 的窄端口。
type TicketIssuer interface {
	// Issue 创建或精确重放 deterministic material；commit-unknown 不得返回 material。
	Issue(ctx context.Context, facts battleticket.Facts, observedAt time.Time) (battleticket.IssueResult, error)
}

// ChildTicketRegistry 是 private control 上 exact child ticket registry 的窄端口。
type ChildTicketRegistry interface {
	// Install 幂等安装 proof key 与完整 binding，并原子确认 actor slot。
	Install(ctx context.Context, target simulationcontrol.SimulationTarget, material battleticket.Material) (ChildTicketReceipt, error)
	// Status 在 install 响应丢失后查询 exact ticket/binding。
	Status(ctx context.Context, target simulationcontrol.SimulationTarget, binding battleticket.Binding) (ChildTicketReceipt, error)
	// Revoke 只撤销 exact target 上的 exact ticket/binding，不能命中 successor。
	Revoke(ctx context.Context, target simulationcontrol.SimulationTarget, binding battleticket.Binding) (ChildTicketReceipt, error)
}

// Clock 为 role、placement、target 与 issuance 使用同一绝对时间快照。
type Clock interface {
	// Now 返回受信绝对时间。
	Now() time.Time
}

// Service 编排 BattleTicket target admission，不拥有任何领域或 runtime 状态。
type Service struct {
	worlds         WorldEnsurer
	assignments    AssignmentReader
	visits         VisitRoleOwner
	targets        SimulationTargetResolver
	endpoints      BattleEndpointProvider
	capacity       ActorCapacityOwner
	issuer         TicketIssuer
	child          ChildTicketRegistry
	clock          Clock
	ticketLifetime time.Duration
	wireIdentity   battleticket.Digest
}

// NewService 校验 BattleTicket admission 全部真实 owner 与冻结配置。
func NewService(
	worlds WorldEnsurer,
	assignments AssignmentReader,
	visits VisitRoleOwner,
	targets SimulationTargetResolver,
	endpoints BattleEndpointProvider,
	capacity ActorCapacityOwner,
	issuer TicketIssuer,
	child ChildTicketRegistry,
	clock Clock,
	ticketLifetime time.Duration,
	wireIdentity battleticket.Digest,
) (*Service, error) {
	if worlds == nil || assignments == nil || visits == nil || targets == nil || endpoints == nil ||
		capacity == nil || issuer == nil || clock == nil || ticketLifetime <= 0 ||
		child == nil ||
		ticketLifetime > 2*time.Minute || !wireIdentity.Valid() {
		return nil, errors.New("battle entry dependencies are incomplete")
	}
	return &Service{
		worlds: worlds, assignments: assignments, visits: visits, targets: targets,
		endpoints: endpoints, capacity: capacity, issuer: issuer, clock: clock,
		child: child, ticketLifetime: ticketLifetime, wireIdentity: wireIdentity,
	}, nil
}

// Issue 从权威 owner 解析 role、current target、trusted endpoint 与 actor slot 后签发。
//
// Capacity reservation 后会再次读取 current assignment 与 SimulationTarget，避免 replacement
// 窗口把旧 child slot 变成可返回 credential。任一失败都会精确 release 且返回零 Result。
func (service *Service) Issue(ctx context.Context, authenticated session.AuthenticatedSession, selector Target, idempotencyKey string) (Result, error) {
	if service == nil || ctx == nil || !authenticated.Valid() || !selector.Valid() ||
		validateIdempotencyKey(idempotencyKey) != nil {
		return Result{}, admissionError("validate", battleticket.ErrorCodeInvalidArgument, nil)
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	if now.IsZero() || !now.Before(authenticated.Deadline()) {
		return Result{}, admissionError("authorize", battleticket.ErrorCodeExpired, nil)
	}
	auth := authenticated.AuthContext()
	playerID, err := account.NewPlayerID(auth.Principal().PlayerID())
	if err != nil {
		return Result{}, admissionError("authorize", battleticket.ErrorCodeDependencyDefect, err)
	}
	issueID, err := battleticket.NewIssueID("biss_" + deriveIdentity(auth, idempotencyKey))
	if err != nil {
		return Result{}, admissionError("authorize", battleticket.ErrorCodeDependencyDefect, err)
	}
	authority, err := service.resolveAuthority(ctx, authenticated, selector, playerID, now)
	if err != nil {
		return Result{}, err
	}
	target, err := service.resolveTarget(ctx, authority.assignment.Stamp())
	if err != nil {
		return Result{}, err
	}
	endpoint, err := service.endpoints.EndpointFor(ctx, target)
	if err != nil {
		return Result{}, admissionError("endpoint", battleticket.ErrorCodeTargetNotReady, err)
	}
	if !endpoint.Valid() {
		return Result{}, admissionError("endpoint", battleticket.ErrorCodeDependencyDefect, nil)
	}
	reservation, err := service.capacity.Reserve(ctx, ReservationRequest{
		IssueID: issueID, PlayerID: playerID, Role: authority.role, Target: target,
	})
	if err != nil {
		return Result{}, capacityError(err)
	}
	if !reservation.Valid() || reservation.IssueID() != issueID ||
		!sameTarget(reservation.Target(), target) {
		service.release(ctx, reservation)
		return Result{}, admissionError("capacity", battleticket.ErrorCodeDependencyDefect, nil)
	}

	current, err := service.resolveCurrent(ctx, authority.worldID, now)
	if err != nil || !current.Stamp().Equal(authority.assignment.Stamp()) {
		service.release(ctx, reservation)
		if err != nil && battleticket.ErrorCodeOf(err) == battleticket.ErrorCodeDependency {
			return Result{}, err
		}
		return Result{}, admissionError("revalidate", battleticket.ErrorCodeTargetStale, err)
	}
	revalidatedTarget, err := service.resolveTarget(ctx, current.Stamp())
	if err != nil || !sameTarget(revalidatedTarget, target) {
		service.release(ctx, reservation)
		return Result{}, admissionError("revalidate", battleticket.ErrorCodeTargetStale, err)
	}
	revalidatedEndpoint, err := service.endpoints.EndpointFor(ctx, revalidatedTarget)
	if err != nil || !revalidatedEndpoint.Valid() || !revalidatedEndpoint.Equal(endpoint) {
		service.release(ctx, reservation)
		return Result{}, admissionError("revalidate", battleticket.ErrorCodeTargetStale, err)
	}
	deadline := earliest(now.Add(service.ticketLifetime), authenticated.Deadline(),
		current.Lease().ExpiresAt(), authority.deadline)
	if !deadline.After(now) {
		service.release(ctx, reservation)
		return Result{}, admissionError("authorize", battleticket.ErrorCodeExpired, nil)
	}
	facts := battleticket.Facts{
		PlayerID: playerID, SessionID: auth.SessionID(), SessionEpoch: auth.Epoch(),
		Role: authority.role, WorldID: authority.worldID, VisitSessionID: authority.visitSessionID,
		Assignment: current.Stamp(), AssignmentFingerprint: target.AssignmentFingerprint,
		RuntimeNodeID: target.RuntimeNodeID, SimulationNodeID: target.NodeID,
		SimulationInstanceID: target.InstanceID, MappingGeneration: target.MappingGeneration,
		TargetRevision: target.Revision, ModelIdentity: target.ModelManifest,
		ProfileIdentity: target.ProfileManifest, ConfigIdentity: target.ConfigIdentity,
		WireIdentity: service.wireIdentity, ActorSlot: reservation.Slot(), Endpoint: endpoint,
		IssueID: issueID, IssuedAt: now, ExpiresAt: deadline,
	}
	issued, err := service.issuer.Issue(ctx, facts, now)
	if err != nil {
		service.release(ctx, reservation)
		return Result{}, err
	}
	material := issued.Material()
	if !issued.Valid() || !material.Binding().Facts().ActorSlot.Valid() ||
		!material.Binding().Facts().Assignment.Equal(current.Stamp()) ||
		!material.Binding().Facts().Endpoint.Equal(endpoint) {
		service.release(ctx, reservation)
		return Result{}, admissionError("issue", battleticket.ErrorCodeDependencyDefect, nil)
	}
	installed, err := service.installOrResolve(ctx, target, material)
	if err != nil {
		service.compensate(target, material.Binding(), reservation)
		return Result{}, err
	}
	if !installed.MatchesInstalled(target, material) {
		service.compensate(target, material.Binding(), reservation)
		return Result{}, admissionError("install", battleticket.ErrorCodeDependencyDefect, nil)
	}
	if err := ctx.Err(); err != nil {
		service.compensate(target, material.Binding(), reservation)
		return Result{}, admissionError("install", battleticket.ErrorCodeDependency, err)
	}
	postInstallCurrent, err := service.resolveCurrent(ctx, authority.worldID, service.clock.Now().UTC().Truncate(time.Microsecond))
	if err != nil || !postInstallCurrent.Stamp().Equal(current.Stamp()) {
		service.compensate(target, material.Binding(), reservation)
		return Result{}, admissionError("install_revalidate", battleticket.ErrorCodeTargetStale, err)
	}
	postInstallTarget, err := service.resolveTarget(ctx, postInstallCurrent.Stamp())
	if err != nil || !sameTarget(postInstallTarget, target) {
		service.compensate(target, material.Binding(), reservation)
		return Result{}, admissionError("install_revalidate", battleticket.ErrorCodeTargetStale, err)
	}
	committed := material.Binding().Facts()
	result := Result{
		TicketID: material.Binding().TicketID(), TicketSecret: material.Secret(),
		Endpoint: committed.Endpoint, WireSuite: battleticket.CurrentWireSuite(),
		Role: committed.Role, TargetKind: selector.Kind, TargetRevision: committed.TargetRevision,
		ExpiresAt: committed.ExpiresAt,
	}
	if !result.Valid() {
		service.release(ctx, reservation)
		return Result{}, admissionError("project", battleticket.ErrorCodeDependencyDefect, nil)
	}
	return result, nil
}

// installOrResolve 把 response-loss 收敛为 exact status；只有 Installed 才允许继续公开。
func (service *Service) installOrResolve(ctx context.Context, target simulationcontrol.SimulationTarget, material battleticket.Material) (ChildTicketReceipt, error) {
	receipt, installErr := service.child.Install(ctx, target, material)
	if installErr == nil {
		return receipt, nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	status, statusErr := service.child.Status(cleanupCtx, target, material.Binding())
	if statusErr != nil {
		return ChildTicketReceipt{}, admissionError("install_status", battleticket.ErrorCodeCommitUnknown, errors.Join(installErr, statusErr))
	}
	if !status.MatchesInstalled(target, material) {
		return ChildTicketReceipt{}, admissionError("install_status", battleticket.ErrorCodeCommitUnknown, installErr)
	}
	return status, nil
}

// compensate 在独立有界 context 中 exact revoke child，并释放相同 target reservation。
//
// Revoke 或 release 失败不能恢复 credential，也不能把 cleanup 扩大到 successor。
func (service *Service) compensate(target simulationcontrol.SimulationTarget, binding battleticket.Binding, reservation Reservation) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if binding.Valid() {
		_, _ = service.child.Revoke(cleanupCtx, target, binding)
	}
	service.release(cleanupCtx, reservation)
}

type authority struct {
	role           battleticket.Role
	worldID        personalworld.PersonalWorldID
	visitSessionID visitsession.VisitSessionID
	assignment     placement.AssignmentSnapshot
	deadline       time.Time
}

func (service *Service) resolveAuthority(ctx context.Context, authenticated session.AuthenticatedSession, selector Target, playerID account.PlayerID, now time.Time) (authority, error) {
	if selector.Kind == TargetKindOwnWorld {
		world, err := service.worlds.EnsurePrimary(ctx, playerID)
		if err != nil {
			return authority{}, admissionError("world", battleticket.ErrorCodeDependency, err)
		}
		if !world.Valid() || world.OwnerID() != playerID ||
			world.Lifecycle() != personalworld.LifecycleActive {
			return authority{}, admissionError("world", battleticket.ErrorCodeTargetNotReady, nil)
		}
		current, err := service.resolveCurrent(ctx, world.ID(), now)
		if err != nil {
			return authority{}, err
		}
		return authority{role: battleticket.RoleOwner, worldID: world.ID(), assignment: current}, nil
	}
	visitAuthority, err := service.visits.ResolveBattleAuthority(ctx, authenticated, selector.VisitSessionID)
	if err != nil {
		return authority{}, admissionError("visit", battleticket.ErrorCodeTargetNotReady, err)
	}
	if !visitAuthority.Valid() {
		return authority{}, admissionError("visit", battleticket.ErrorCodeDependencyDefect, nil)
	}
	intent := visitAuthority.Intent
	auth := authenticated.AuthContext()
	if intent.VisitSessionID() != selector.VisitSessionID || intent.VisitorID() != playerID ||
		intent.SessionID() != auth.SessionID() || intent.Epoch() != auth.Epoch() ||
		!now.Before(intent.ExpiresAt()) {
		return authority{}, admissionError("visit", battleticket.ErrorCodeTargetStale, nil)
	}
	current, err := service.resolveCurrent(ctx, intent.Assignment().WorldID(), now)
	if err != nil {
		return authority{}, err
	}
	if !current.Stamp().Equal(intent.Assignment()) {
		return authority{}, admissionError("visit", battleticket.ErrorCodeTargetStale, nil)
	}
	return authority{
		role: battleticket.RoleVisitor, worldID: intent.Assignment().WorldID(),
		visitSessionID: selector.VisitSessionID, assignment: current,
		deadline: intent.ExpiresAt(),
	}, nil
}

func (service *Service) resolveCurrent(ctx context.Context, worldID personalworld.PersonalWorldID, now time.Time) (placement.AssignmentSnapshot, error) {
	current, outcome, err := service.assignments.Resolve(ctx, worldID, now)
	if err != nil {
		return placement.AssignmentSnapshot{}, admissionError("assignment", battleticket.ErrorCodeDependency, err)
	}
	if outcome == placement.ResolveOutcomeNotFound {
		return placement.AssignmentSnapshot{}, admissionError("assignment", battleticket.ErrorCodeTargetNotReady, nil)
	}
	if outcome != placement.ResolveOutcomeFound || !current.Valid() ||
		current.WorldID() != worldID {
		return placement.AssignmentSnapshot{}, admissionError("assignment", battleticket.ErrorCodeDependencyDefect, nil)
	}
	if current.Phase() != placement.PhaseActive || !current.ValidAt(now) {
		return placement.AssignmentSnapshot{}, admissionError("assignment", battleticket.ErrorCodeTargetNotReady, nil)
	}
	return current, nil
}

func (service *Service) resolveTarget(ctx context.Context, stamp placement.AssignmentStamp) (simulationcontrol.SimulationTarget, error) {
	target, found, err := service.targets.ResolveSimulationTarget(ctx, stamp)
	if err != nil {
		return simulationcontrol.SimulationTarget{}, admissionError("target", battleticket.ErrorCodeDependency, err)
	}
	if !found {
		return simulationcontrol.SimulationTarget{}, admissionError("target", battleticket.ErrorCodeTargetNotReady, nil)
	}
	if target.Validate() != nil || target.RuntimeNodeID != stamp.NodeID() ||
		target.ActorCapacity != simulationcontrol.QualifiedActorCapacity {
		return simulationcontrol.SimulationTarget{}, admissionError("target", battleticket.ErrorCodeDependencyDefect, nil)
	}
	return target, nil
}

func (service *Service) release(ctx context.Context, reservation Reservation) {
	if reservation.Valid() {
		_ = service.capacity.Release(ctx, reservation)
	}
}

func sameTarget(left simulationcontrol.SimulationTarget, right simulationcontrol.SimulationTarget) bool {
	return left == right
}

func deriveIdentity(auth session.AuthContext, key string) string {
	digest := sha256.New()
	for _, field := range []string{
		"ihomeland/battle-entry/idempotency/v1", "issue-battle-ticket",
		auth.Principal().PlayerID(), auth.SessionID().String(),
		strconv.FormatUint(uint64(auth.Epoch()), 10), key,
	} {
		_, _ = digest.Write([]byte{byte(len(field) >> 8), byte(len(field))})
		_, _ = digest.Write([]byte(field))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func validateIdempotencyKey(value string) error {
	if len(value) < 16 || len(value) > 128 {
		return errors.New("idempotency key length is invalid")
	}
	for _, character := range []byte(value) {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == ':' || character == '-') {
			return errors.New("idempotency key contains unsafe characters")
		}
	}
	return nil
}

func earliest(values ...time.Time) time.Time {
	var result time.Time
	for _, value := range values {
		if !value.IsZero() && (result.IsZero() || value.Before(result)) {
			result = value
		}
	}
	return result.UTC().Truncate(time.Microsecond)
}

func admissionError(operation string, code battleticket.ErrorCode, cause error) error {
	return battleticket.NewAdmissionError(operation, code, cause)
}

func capacityError(err error) error {
	code := battleticket.ErrorCodeOf(err)
	if code != battleticket.ErrorCodeUnspecified {
		return err
	}
	return admissionError("capacity", battleticket.ErrorCodeDependency, err)
}
