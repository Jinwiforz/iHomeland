package worldentry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// WorldEnsurer 是PersonalWorld owner提供的primary ensure窄端口。
type WorldEnsurer interface {
	// EnsurePrimary 创建或解析actor唯一primary world并返回完整持久snapshot。
	EnsurePrimary(ctx context.Context, ownerID account.PlayerID) (personalworld.Snapshot, error)
}

// PersonalWorldServiceAdapter 把既有PersonalWorld service缩窄为world-entry所需端口。
type PersonalWorldServiceAdapter struct {
	// service 保留PersonalWorld owner，不复制repository或状态。
	service *personalworld.Service
}

// NewPersonalWorldServiceAdapter 校验并包装既有领域owner。
func NewPersonalWorldServiceAdapter(service *personalworld.Service) (*PersonalWorldServiceAdapter, error) {
	if service == nil {
		return nil, errors.New("personal world service is unavailable")
	}
	return &PersonalWorldServiceAdapter{service: service}, nil
}

// EnsurePrimary 委托owner并只返回world-entry需要的持久snapshot。
func (adapter *PersonalWorldServiceAdapter) EnsurePrimary(ctx context.Context, ownerID account.PlayerID) (personalworld.Snapshot, error) {
	if adapter == nil || adapter.service == nil {
		return personalworld.Snapshot{}, errors.New("personal world service is unavailable")
	}
	result, err := adapter.service.EnsurePrimaryWorld(ctx, ownerID)
	if err != nil {
		return personalworld.Snapshot{}, err
	}
	if !result.Valid() || result.World().OwnerID() != ownerID {
		return personalworld.Snapshot{}, errors.New("personal world service returned invalid result")
	}
	return result.World().Snapshot(), nil
}

// AssignmentReader 是placement owner提供的current只读端口。
type AssignmentReader interface {
	// Resolve 返回observedAt时刻的current assignment事实。
	Resolve(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error)
}

// VisitSessionOwner 是HTTP world-entry所需的两个窄VisitSession用例。
type VisitSessionOwner interface {
	// AcceptInviteFromHTTP 由领域owner计算deadline并原子提交reservation。
	AcceptInviteFromHTTP(ctx context.Context, authenticated session.AuthenticatedSession, visitID visitsession.VisitSessionID, inviteID visitsession.InviteID, expected visitsession.Revision, commandID visitsession.CommandID) (visitsession.MutationResult, error)
	// ResolveAdmissionEligibility 只读解析当前membership用途与deadline。
	ResolveAdmissionEligibility(ctx context.Context, authenticated session.AuthenticatedSession, visitID visitsession.VisitSessionID) (visitsession.AdmissionEligibility, error)
}

// AdmissionIssuer 是WorldAdmission owner提供的幂等签发窄端口。
type AdmissionIssuer interface {
	// Issue 验证current assignment并原子创建或重放credential。
	Issue(ctx context.Context, issueID worldadmission.IssueID, binding worldadmission.Binding) (worldadmission.IssueResult, error)
}

// Clock 为binding issuedAt和placement观察提供同一UTC时间来源。
type Clock interface {
	// Now 返回当前绝对时间。
	Now() time.Time
}

// Service 只编排bootstrap、accept与admission签发，不拥有任何领域状态。
type Service struct {
	// worlds 提供持久primary world事实。
	worlds WorldEnsurer
	// assignments 提供current assignment与lease。
	assignments AssignmentReader
	// visits 提供reservation与membership资格。
	visits VisitSessionOwner
	// admissions 是opaque credential唯一owner。
	admissions AdmissionIssuer
	// endpoints 提供不可由客户端覆盖的TLS/TCP地址。
	endpoints session.EndpointProvider
	// clock 与admission issuer共享受信时间来源。
	clock Clock
	// admissionLifetime 是own-world credential配置上限。
	admissionLifetime time.Duration
}

// NewService 校验world-entry全部真实owner端口与短期签发策略。
func NewService(worlds WorldEnsurer, assignments AssignmentReader, visits VisitSessionOwner, admissions AdmissionIssuer, endpoints session.EndpointProvider, clock Clock, admissionLifetime time.Duration) (*Service, error) {
	if worlds == nil || assignments == nil || visits == nil || admissions == nil || endpoints == nil || clock == nil || admissionLifetime <= 0 || admissionLifetime > 5*time.Minute {
		return nil, errors.New("world entry dependencies are incomplete")
	}
	return &Service{worlds: worlds, assignments: assignments, visits: visits, admissions: admissions, endpoints: endpoints, clock: clock, admissionLifetime: admissionLifetime}, nil
}

// BootstrapOwnWorld 幂等确保primary world并返回可选client-safe current assignment。
func (service *Service) BootstrapOwnWorld(ctx context.Context, authenticated session.AuthenticatedSession) (BootstrapResult, error) {
	if service == nil || ctx == nil || !authenticated.Valid() {
		return BootstrapResult{}, operationError(ErrorCodeValidation, nil)
	}
	playerID, err := account.NewPlayerID(authenticated.AuthContext().Principal().PlayerID())
	if err != nil {
		return BootstrapResult{}, operationError(ErrorCodeDependencyDefect, err)
	}
	world, err := service.worlds.EnsurePrimary(ctx, playerID)
	if err != nil || !world.Valid() || world.OwnerID() != playerID || world.Lifecycle() != personalworld.LifecycleActive {
		return BootstrapResult{}, dependencyError(err)
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	assignment, outcome, err := service.assignments.Resolve(ctx, world.ID(), now)
	if err != nil {
		return BootstrapResult{}, dependencyError(err)
	}
	result := BootstrapResult{World: world}
	switch outcome {
	case placement.ResolveOutcomeNotFound:
		if assignment.Valid() {
			return BootstrapResult{}, operationError(ErrorCodeDependencyDefect, nil)
		}
	case placement.ResolveOutcomeFound:
		if !assignment.Valid() || assignment.WorldID() != world.ID() {
			return BootstrapResult{}, operationError(ErrorCodeDependencyDefect, nil)
		}
		if assignment.ValidAt(now) && assignment.Phase() == placement.PhaseActive {
			projection, projectionErr := service.projectAssignment(ctx, assignment, world.ID(), now)
			if projectionErr != nil {
				return BootstrapResult{}, projectionErr
			}
			result.Assignment = projection
		}
	default:
		return BootstrapResult{}, operationError(ErrorCodeDependencyDefect, nil)
	}
	if !result.Valid() {
		return BootstrapResult{}, operationError(ErrorCodeDependencyDefect, nil)
	}
	return result, nil
}

// AcceptVisitInvite 派生actor/session/operation分域CommandID并委托VisitSession owner。
func (service *Service) AcceptVisitInvite(ctx context.Context, authenticated session.AuthenticatedSession, visitID visitsession.VisitSessionID, inviteID visitsession.InviteID, expected visitsession.Revision, idempotencyKey string) (ReservationResult, error) {
	if service == nil || ctx == nil || !authenticated.Valid() || !visitID.Valid() || !inviteID.Valid() || !expected.Valid() || validateIdempotencyKey(idempotencyKey) != nil {
		return ReservationResult{}, operationError(ErrorCodeValidation, nil)
	}
	identity := deriveIdentity("accept-visit-invite", authenticated.AuthContext(), idempotencyKey)
	commandID, err := visitsession.NewCommandID("vcmd_" + identity)
	if err != nil {
		return ReservationResult{}, operationError(ErrorCodeDependencyDefect, err)
	}
	mutation, err := service.visits.AcceptInviteFromHTTP(ctx, authenticated, visitID, inviteID, expected, commandID)
	if err != nil {
		return ReservationResult{}, err
	}
	intent := mutation.AdmissionIntent()
	result := ReservationResult{VisitSessionID: mutation.Snapshot().ID(), Revision: mutation.Snapshot().Revision(), ExpiresAt: intent.ExpiresAt()}
	if !result.Valid() || result.VisitSessionID != visitID || !intent.Valid() {
		return ReservationResult{}, operationError(ErrorCodeDependencyDefect, nil)
	}
	return result, nil
}

// IssueWorldAdmission 从权威owner事实派生binding，payload不能覆盖actor、role、purpose或endpoint。
func (service *Service) IssueWorldAdmission(ctx context.Context, authenticated session.AuthenticatedSession, target AdmissionTarget, idempotencyKey string) (AdmissionResult, error) {
	if service == nil || ctx == nil || !authenticated.Valid() || !target.Valid() || validateIdempotencyKey(idempotencyKey) != nil {
		return AdmissionResult{}, operationError(ErrorCodeValidation, nil)
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	auth := authenticated.AuthContext()
	playerID, err := account.NewPlayerID(auth.Principal().PlayerID())
	if err != nil {
		return AdmissionResult{}, operationError(ErrorCodeDependencyDefect, err)
	}
	endpoint, err := service.endpoints.EndpointFor(ctx, session.ChannelTLSTCP)
	if err != nil || !endpoint.Valid() || endpoint.Channel() != session.ChannelTLSTCP {
		return AdmissionResult{}, dependencyError(err)
	}
	var binding worldadmission.Binding
	if target.Kind == AdmissionTargetOwnWorld {
		world, ensureErr := service.worlds.EnsurePrimary(ctx, playerID)
		if ensureErr != nil || !world.Valid() || world.OwnerID() != playerID || world.Lifecycle() != personalworld.LifecycleActive {
			return AdmissionResult{}, dependencyError(ensureErr)
		}
		assignment, resolveOutcome, resolveErr := service.assignments.Resolve(ctx, world.ID(), now)
		if resolveErr != nil {
			return AdmissionResult{}, dependencyError(resolveErr)
		}
		if resolveOutcome == placement.ResolveOutcomeNotFound {
			return AdmissionResult{}, operationError(ErrorCodeWorldNotReady, nil)
		}
		if resolveOutcome != placement.ResolveOutcomeFound || !assignment.Valid() || assignment.WorldID() != world.ID() {
			return AdmissionResult{}, operationError(ErrorCodeDependencyDefect, nil)
		}
		if !assignment.ValidAt(now) || assignment.Phase() != placement.PhaseActive {
			return AdmissionResult{}, operationError(ErrorCodeWorldNotReady, nil)
		}
		deadline := earliest(now.Add(service.admissionLifetime), authenticated.Deadline(), assignment.Lease().ExpiresAt())
		binding, err = worldadmission.NewBinding(playerID, auth.SessionID(), auth.Epoch(), worldadmission.RoleOwner, world.ID(), visitsession.VisitSessionID{}, worldadmission.PurposeOwnWorld, assignment.Stamp(), endpoint, now, deadline)
	} else {
		eligibility, eligibilityErr := service.visits.ResolveAdmissionEligibility(ctx, authenticated, target.VisitSessionID)
		if eligibilityErr != nil {
			return AdmissionResult{}, eligibilityErr
		}
		if !eligibility.Valid() {
			return AdmissionResult{}, operationError(ErrorCodeDependencyDefect, nil)
		}
		intent := eligibility.Intent()
		purpose := worldadmission.PurposeJoin
		if eligibility.Purpose() == visitsession.AdmissionPurposeReconnect {
			purpose = worldadmission.PurposeReconnect
		}
		deadline := earliest(now.Add(service.admissionLifetime), intent.ExpiresAt(), authenticated.Deadline())
		binding, err = worldadmission.NewBinding(playerID, auth.SessionID(), auth.Epoch(), worldadmission.RoleVisitor, intent.Assignment().WorldID(), target.VisitSessionID, purpose, intent.Assignment(), endpoint, now, deadline)
	}
	if err != nil {
		return AdmissionResult{}, operationError(ErrorCodeDependencyDefect, err)
	}
	issueID, err := worldadmission.NewIssueID("wiss_" + deriveIdentity("issue-world-admission", auth, idempotencyKey))
	if err != nil {
		return AdmissionResult{}, operationError(ErrorCodeDependencyDefect, err)
	}
	issued, err := service.admissions.Issue(ctx, issueID, binding)
	if err != nil {
		return AdmissionResult{}, err
	}
	committed := issued.Binding()
	if !issued.Valid() || !admissionReplayMatches(committed, binding) {
		return AdmissionResult{}, operationError(ErrorCodeDependencyDefect, nil)
	}
	result := AdmissionResult{Credential: issued.Credential(), Endpoint: committed.Endpoint(), Role: committed.Role(), Purpose: committed.Purpose(), ExpiresAt: committed.ExpiresAt()}
	if !result.Valid() {
		return AdmissionResult{}, operationError(ErrorCodeDependencyDefect, nil)
	}
	return result, nil
}

// admissionReplayMatches 接受权威事实相同且不延长资格的首次签发binding。
func admissionReplayMatches(committed worldadmission.Binding, candidate worldadmission.Binding) bool {
	return committed.Valid() && candidate.Valid() && committed.PlayerID() == candidate.PlayerID() &&
		committed.SessionID() == candidate.SessionID() && committed.Epoch() == candidate.Epoch() &&
		committed.Role() == candidate.Role() && committed.WorldID() == candidate.WorldID() &&
		committed.VisitSessionID() == candidate.VisitSessionID() && committed.Purpose() == candidate.Purpose() &&
		committed.Assignment().Equal(candidate.Assignment()) && committed.Endpoint().Equal(candidate.Endpoint()) &&
		!committed.IssuedAt().After(candidate.IssuedAt()) && !committed.ExpiresAt().After(candidate.ExpiresAt())
}

// projectAssignment 严格隐藏node与fencing token，只公开协议允许字段。
func (service *Service) projectAssignment(ctx context.Context, assignment placement.AssignmentSnapshot, worldID personalworld.PersonalWorldID, observedAt time.Time) (AssignmentProjection, error) {
	if !assignment.ValidAt(observedAt) || assignment.Phase() != placement.PhaseActive || assignment.WorldID() != worldID {
		return AssignmentProjection{}, operationError(ErrorCodeWorldNotReady, nil)
	}
	endpoint, err := service.endpoints.EndpointFor(ctx, session.ChannelTLSTCP)
	if err != nil {
		return AssignmentProjection{}, dependencyError(err)
	}
	projection := AssignmentProjection{WorldID: worldID, InstanceID: assignment.InstanceID().String(), Endpoint: endpoint, Generation: uint64(assignment.Generation()), LeaseExpiresAt: assignment.Lease().ExpiresAt()}
	if !projection.Valid() {
		return AssignmentProjection{}, operationError(ErrorCodeDependencyDefect, nil)
	}
	return projection, nil
}

// deriveIdentity 用固定domain separator隔离actor、session lineage与operation。
func deriveIdentity(operation string, auth session.AuthContext, key string) string {
	digest := sha256.New()
	for _, field := range []string{"ihomeland/world-entry/idempotency/v1", operation, auth.Principal().PlayerID(), auth.SessionID().String(), strconv.FormatUint(uint64(auth.Epoch()), 10), key} {
		digest.Write([]byte{byte(len(field) >> 8), byte(len(field))})
		digest.Write([]byte(field))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// earliest 返回完整绝对deadline集合中的最早值。
func earliest(values ...time.Time) time.Time {
	var result time.Time
	for _, value := range values {
		if !value.IsZero() && (result.IsZero() || value.Before(result)) {
			result = value
		}
	}
	return result
}

// dependencyError 保留非空cause供集中错误映射检查类型，但不拼接敏感内容。
func dependencyError(cause error) error {
	if cause == nil {
		return operationError(ErrorCodeDependencyDefect, nil)
	}
	return operationError(ErrorCodeDependency, cause)
}
