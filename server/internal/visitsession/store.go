package visitsession

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// Clock 为 VisitSession application 提供可替换的受信绝对时间源。
type Clock interface {
	// Now 返回当前绝对时间；实现必须允许并发调用，application 会规范为 UTC 微秒。
	Now() time.Time
}

// IDGenerator 创建不可由客户端预测或选择的 VisitSession entity identity 材料。
type IDGenerator interface {
	// NewID 并发安全地返回不可预测安全 ASCII 材料；package 负责添加 namespace 并校验。
	NewID() (string, error)
}

// OwnedWorldOutcome 表达按认证 Owner 解析 active PersonalWorld 的稳定结果。
type OwnedWorldOutcome uint8

const (
	// OwnedWorldOutcomeUnspecified 表示 reader 未遵守契约。
	OwnedWorldOutcomeUnspecified OwnedWorldOutcome = iota
	// OwnedWorldOutcomeFound 表示返回完整 active world snapshot。
	OwnedWorldOutcomeFound
	// OwnedWorldOutcomeNotFound 表示 Owner 没有 active PersonalWorld。
	OwnedWorldOutcomeNotFound
)

// String 返回 reader contract 测试与低基数诊断使用的稳定名称。
func (outcome OwnedWorldOutcome) String() string {
	if outcome == OwnedWorldOutcomeFound {
		return "found"
	}
	if outcome == OwnedWorldOutcomeNotFound {
		return "not_found"
	}
	return "unspecified"
}

// OwnedWorldReader 只解析认证 actor 当前拥有的 active PersonalWorld。
type OwnedWorldReader interface {
	// ResolveOwnedWorld 返回 immutable Owner 与 lifecycle 均完整的持久 snapshot。
	// Found 必须携带 ownerID 匹配的完整 snapshot；NotFound 必须携带零值 snapshot；
	// dependency error 必须携带零值 snapshot 与 Unspecified outcome。
	ResolveOwnedWorld(ctx context.Context, ownerID account.PlayerID) (personalworld.Snapshot, OwnedWorldOutcome, error)
}

// AssignmentOutcome 表达 current placement 读取的稳定结果。
type AssignmentOutcome uint8

const (
	// AssignmentOutcomeUnspecified 表示 reader 未遵守契约。
	AssignmentOutcomeUnspecified AssignmentOutcome = iota
	// AssignmentOutcomeFound 表示返回完整 current assignment snapshot。
	AssignmentOutcomeFound
	// AssignmentOutcomeNotFound 表示 world 没有 current assignment。
	AssignmentOutcomeNotFound
)

// String 返回 placement reader contract 测试使用的稳定名称。
func (outcome AssignmentOutcome) String() string {
	if outcome == AssignmentOutcomeFound {
		return "found"
	}
	if outcome == AssignmentOutcomeNotFound {
		return "not_found"
	}
	return "unspecified"
}

// CurrentAssignmentReader 读取 placement owner 的 current assignment，不启动 runtime。
type CurrentAssignmentReader interface {
	// ResolveCurrent 返回 world 在 observedAt 的完整 snapshot；observedAt 是 UTC 微秒绝对时间。
	// Found 必须携带目标 world 的完整 snapshot，调用方仍会验证 active phase 与 lease；
	// NotFound 必须携带零值 snapshot；dependency error 必须携带零值 snapshot 与 Unspecified outcome。
	ResolveCurrent(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (placement.AssignmentSnapshot, AssignmentOutcome, error)
}

// CommandFingerprint 是 store 识别同 CommandID 同语义的固定 SHA-256 摘要。
type CommandFingerprint struct {
	// digest 保存规范稳定 command 字段的 SHA-256 摘要。
	digest [sha256.Size]byte
}

// NewCommandFingerprint 从 adapter replay record 的完整摘要恢复受校验值。
func NewCommandFingerprint(digest [sha256.Size]byte) (CommandFingerprint, error) {
	if digest == [sha256.Size]byte{} {
		return CommandFingerprint{}, errors.New("visit command fingerprint is empty")
	}
	return CommandFingerprint{digest: digest}, nil
}

// Digest 返回 store 索引使用的固定大小副本；不得写入普通日志或 metrics label。
func (fingerprint CommandFingerprint) Digest() [sha256.Size]byte { return fingerprint.digest }

// Valid 报告 fingerprint 是否来自规范 command 字段。
func (fingerprint CommandFingerprint) Valid() bool { return fingerprint.digest != [sha256.Size]byte{} }

// Equal 使用固定大小值比较完整摘要。
func (fingerprint CommandFingerprint) Equal(other CommandFingerprint) bool {
	return fingerprint.digest == other.digest
}

// String 防止默认格式化扩散 command 关联摘要。
func (CommandFingerprint) String() string { return "[REDACTED_VISIT_COMMAND_FINGERPRINT]" }

// GoString 防止 `%#v` 展开摘要 bytes。
func (CommandFingerprint) GoString() string { return "[REDACTED_VISIT_COMMAND_FINGERPRINT]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (CommandFingerprint) LogValue() slog.Value {
	return slog.StringValue("[REDACTED_VISIT_COMMAND_FINGERPRINT]")
}

// fingerprintCommand 使用长度前缀编码稳定字段，避免文本拼接歧义。
func fingerprintCommand(operation Operation, fields ...string) CommandFingerprint {
	hasher := sha256.New()
	writeFingerprintUint64(hasher, uint64(operation))
	for _, field := range fields {
		writeFingerprintString(hasher, field)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return CommandFingerprint{digest: digest}
}

// writeFingerprintUint64 以固定大端编码写入非歧义整数。
func writeFingerprintUint64(hasher hash.Hash, value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	_, _ = hasher.Write(buffer[:])
}

// writeFingerprintString 先写长度再写 bytes，字段内容无需 delimiter escaping。
func writeFingerprintString(hasher hash.Hash, value string) {
	writeFingerprintUint64(hasher, uint64(len(value)))
	_, _ = hasher.Write([]byte(value))
}

// CreateOutcome 表达 world active index 原子创建的确定性或不确定结果。
type CreateOutcome uint8

const (
	// CreateOutcomeUnspecified 表示 adapter 未遵守 store contract。
	CreateOutcomeUnspecified CreateOutcome = iota
	// CreateOutcomeCreated 表示 candidate 与 command result 已原子提交。
	CreateOutcomeCreated
	// CreateOutcomeExisting 表示 world 已有 active VisitSession，本次未创建。
	CreateOutcomeExisting
	// CreateOutcomeReplay 表示相同 command 已提交并返回首次完整结果。
	CreateOutcomeReplay
	// CreateOutcomeIdempotencyConflict 表示 CommandID 已绑定其他 fingerprint。
	CreateOutcomeIdempotencyConflict
	// CreateOutcomeNotCommitted 表示依赖失败且可确认没有提交。
	CreateOutcomeNotCommitted
	// CreateOutcomeCommitUnknown 表示无法确认 create 是否提交。
	CreateOutcomeCommitUnknown
)

// String 返回 create adapter、指标与测试使用的稳定低基数名称。
func (outcome CreateOutcome) String() string {
	switch outcome {
	case CreateOutcomeCreated:
		return "created"
	case CreateOutcomeExisting:
		return "existing"
	case CreateOutcomeReplay:
		return "replay"
	case CreateOutcomeIdempotencyConflict:
		return "idempotency_conflict"
	case CreateOutcomeNotCommitted:
		return "not_committed"
	case CreateOutcomeCommitUnknown:
		return "commit_unknown"
	default:
		return "unspecified"
	}
}

// ResolveOutcome 表达 active index 或 VisitSessionID 读取的稳定结果。
type ResolveOutcome uint8

const (
	// ResolveOutcomeUnspecified 表示 adapter 未返回有效读取决议。
	ResolveOutcomeUnspecified ResolveOutcome = iota
	// ResolveOutcomeFound 表示返回完整规范 snapshot。
	ResolveOutcomeFound
	// ResolveOutcomeNotFound 表示目标不存在或已从 active index 移除。
	ResolveOutcomeNotFound
)

// String 返回 read adapter 与 contract test 使用的稳定低基数名称。
func (outcome ResolveOutcome) String() string {
	if outcome == ResolveOutcomeFound {
		return "found"
	}
	if outcome == ResolveOutcomeNotFound {
		return "not_found"
	}
	return "unspecified"
}

// MutationOutcome 表达 transition CAS、幂等与提交确定性决议。
type MutationOutcome uint8

const (
	// MutationOutcomeUnspecified 表示 adapter 未遵守 store contract。
	MutationOutcomeUnspecified MutationOutcome = iota
	// MutationOutcomeApplied 表示 target snapshot 与完整 result 已提交。
	MutationOutcomeApplied
	// MutationOutcomeReplay 表示相同 command 返回首次完整 result。
	MutationOutcomeReplay
	// MutationOutcomeNotFound 表示 VisitSession 不存在且没有提交。
	MutationOutcomeNotFound
	// MutationOutcomeRevisionConflict 表示 expected revision 已过期。
	MutationOutcomeRevisionConflict
	// MutationOutcomeIdempotencyConflict 表示 CommandID 已绑定其他 fingerprint。
	MutationOutcomeIdempotencyConflict
	// MutationOutcomeCapacityConflict 表示 adapter 原子边界发现 capacity 已满。
	MutationOutcomeCapacityConflict
	// MutationOutcomeStale 表示 identity、binding、generation 或 deadline 条件已过期。
	MutationOutcomeStale
	// MutationOutcomeInvalidState 表示 current snapshot 不允许 transition。
	MutationOutcomeInvalidState
	// MutationOutcomeNotCommitted 表示依赖失败且可确认没有提交。
	MutationOutcomeNotCommitted
	// MutationOutcomeCommitUnknown 表示无法确认 transition 是否提交。
	MutationOutcomeCommitUnknown
)

// String 返回 transition adapter、指标与测试使用的稳定低基数名称。
func (outcome MutationOutcome) String() string {
	switch outcome {
	case MutationOutcomeApplied:
		return "applied"
	case MutationOutcomeReplay:
		return "replay"
	case MutationOutcomeNotFound:
		return "not_found"
	case MutationOutcomeRevisionConflict:
		return "revision_conflict"
	case MutationOutcomeIdempotencyConflict:
		return "idempotency_conflict"
	case MutationOutcomeCapacityConflict:
		return "capacity_conflict"
	case MutationOutcomeStale:
		return "stale"
	case MutationOutcomeInvalidState:
		return "invalid_state"
	case MutationOutcomeNotCommitted:
		return "not_committed"
	case MutationOutcomeCommitUnknown:
		return "commit_unknown"
	default:
		return "unspecified"
	}
}

// CreateRecord 是 active world index create 的完整候选与幂等条件。
type CreateRecord struct {
	// commandID 是 store 必须先行决议的有界重试 identity。
	commandID CommandID
	// fingerprint 绑定稳定 actor/world/assignment/policy 字段，不包含重试 observedAt。
	fingerprint CommandFingerprint
	// candidate 是仅在没有 active index 时允许提交的初始 snapshot。
	candidate Snapshot
}

// NewCreateRecord 校验 application 生成的完整 create 候选。
func NewCreateRecord(commandID CommandID, fingerprint CommandFingerprint, candidate Snapshot) (CreateRecord, error) {
	if !commandID.Valid() || !fingerprint.Valid() || !candidate.Valid() || candidate.Revision() != InitialRevision || candidate.Lifecycle() != LifecycleOpen {
		return CreateRecord{}, errors.New("visit create record is incomplete")
	}
	return CreateRecord{commandID: commandID, fingerprint: fingerprint, candidate: candidate}, nil
}

// CommandID 返回 store 幂等索引 identity。
func (record CreateRecord) CommandID() CommandID { return record.commandID }

// Fingerprint 返回稳定 command 摘要。
func (record CreateRecord) Fingerprint() CommandFingerprint { return record.fingerprint }

// Candidate 返回初始 snapshot 值副本。
func (record CreateRecord) Candidate() Snapshot { return record.candidate }

// String 防止默认格式化展开 create command 与 candidate identity。
func (CreateRecord) String() string { return "[REDACTED_VISIT_CREATE_RECORD]" }

// GoString 防止 `%#v` 展开 create record 私有字段。
func (CreateRecord) GoString() string { return "[REDACTED_VISIT_CREATE_RECORD]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (CreateRecord) LogValue() slog.Value { return slog.StringValue("[REDACTED_VISIT_CREATE_RECORD]") }

// CreateResult 是 created、existing 或 replay 必须返回的完整结果。
type CreateResult struct {
	// snapshot 是 active index 解析到的完整 VisitSession snapshot。
	snapshot Snapshot
	// commandID 只在 created/replay 结果绑定本次 command；existing 可为零。
	commandID CommandID
	// fingerprint 只在 created/replay 结果绑定本次语义；existing 可为零。
	fingerprint CommandFingerprint
}

// NewCreateResult 构造 store 返回的完整 create/replay 结果。
func NewCreateResult(snapshot Snapshot, commandID CommandID, fingerprint CommandFingerprint) (CreateResult, error) {
	if !snapshot.Valid() || !commandID.Valid() || !fingerprint.Valid() {
		return CreateResult{}, errors.New("visit create result is incomplete")
	}
	return CreateResult{snapshot: snapshot, commandID: commandID, fingerprint: fingerprint}, nil
}

// NewExistingResult 构造不绑定当前 command 的 existing active result。
func NewExistingResult(snapshot Snapshot) (CreateResult, error) {
	if !snapshot.Valid() || snapshot.Lifecycle() == LifecycleClosed {
		return CreateResult{}, errors.New("existing visit result is invalid")
	}
	return CreateResult{snapshot: snapshot}, nil
}

// Snapshot 返回 active VisitSession snapshot。
func (result CreateResult) Snapshot() Snapshot { return result.snapshot }

// CommandID 返回 created/replay command identity；existing 返回零值。
func (result CreateResult) CommandID() CommandID { return result.commandID }

// Fingerprint 返回 created/replay fingerprint；existing 返回零值。
func (result CreateResult) Fingerprint() CommandFingerprint { return result.fingerprint }

// String 防止默认格式化展开 active session 与 command identity。
func (CreateResult) String() string { return "[REDACTED_VISIT_CREATE_RESULT]" }

// GoString 防止 `%#v` 展开 create result 私有字段。
func (CreateResult) GoString() string { return "[REDACTED_VISIT_CREATE_RESULT]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (CreateResult) LogValue() slog.Value { return slog.StringValue("[REDACTED_VISIT_CREATE_RESULT]") }

// MutationResult 是 store 必须与 target snapshot 原子保存并完整 replay 的 operation 结果。
type MutationResult struct {
	// operation 标识结果 payload 的解释规则。
	operation Operation
	// snapshot 是 mutation 后的完整规范状态。
	snapshot Snapshot
	// commandID 与 fingerprint 固定 replay identity 和语义。
	commandID CommandID
	// fingerprint 防止相同 CommandID 用于其他目标或 binding。
	fingerprint CommandFingerprint
	// invite 只由 create-invite 结果返回。
	invite InviteSnapshot
	// admission 只由 accept 结果返回，且不是 credential。
	admission AdmissionIntent
	// membership 只由 join/reconnect 等 membership projection 结果返回。
	membership MembershipSnapshot
	// retiredInvites 保存本次 transition 从 pending 退役的稳定排序邀请集合。
	retiredInvites []InviteSnapshot
	// directives 保存单成员或批量 safe-return 的稳定排序副本。
	directives []SafeReturnDirective
}

// NewMutationResult 构造 application 提交与 store replay 共用的完整结果。
func NewMutationResult(operation Operation, snapshot Snapshot, commandID CommandID, fingerprint CommandFingerprint, invite InviteSnapshot, admission AdmissionIntent, membership MembershipSnapshot, directives []SafeReturnDirective) (MutationResult, error) {
	return NewMutationResultWithRetiredInvites(operation, snapshot, commandID, fingerprint, invite, admission, membership, nil, directives)
}

// NewMutationResultWithRetiredInvites 构造同时保存邀请退役事实的完整 mutation result。
func NewMutationResultWithRetiredInvites(operation Operation, snapshot Snapshot, commandID CommandID, fingerprint CommandFingerprint, invite InviteSnapshot, admission AdmissionIntent, membership MembershipSnapshot, retiredInvites []InviteSnapshot, directives []SafeReturnDirective) (MutationResult, error) {
	result := MutationResult{operation: operation, snapshot: snapshot, commandID: commandID, fingerprint: fingerprint, invite: invite, admission: admission, membership: membership, retiredInvites: append([]InviteSnapshot(nil), retiredInvites...), directives: append([]SafeReturnDirective(nil), directives...)}
	if err := result.validate(); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

// Operation 返回结果对应的稳定 operation。
func (result MutationResult) Operation() Operation { return result.operation }

// Snapshot 返回 mutation 后完整规范 snapshot。
func (result MutationResult) Snapshot() Snapshot { return result.snapshot }

// CommandID 返回 replay identity。
func (result MutationResult) CommandID() CommandID { return result.commandID }

// Fingerprint 返回稳定语义摘要。
func (result MutationResult) Fingerprint() CommandFingerprint { return result.fingerprint }

// Invite 返回 create-invite projection；其他 operation 返回零值。
func (result MutationResult) Invite() InviteSnapshot { return result.invite }

// AdmissionIntent 返回 accept 的非凭据投影；其他 operation 返回零值。
func (result MutationResult) AdmissionIntent() AdmissionIntent { return result.admission }

// Membership 返回 join/reconnect projection；其他 operation 返回零值。
func (result MutationResult) Membership() MembershipSnapshot { return result.membership }

// RetiredInvites 返回本次 transition 从 pending 退役的稳定排序邀请副本。
func (result MutationResult) RetiredInvites() []InviteSnapshot {
	return append([]InviteSnapshot(nil), result.retiredInvites...)
}

// Directives 返回稳定副本，调用方修改结果不会改变 replay record。
func (result MutationResult) Directives() []SafeReturnDirective {
	return append([]SafeReturnDirective(nil), result.directives...)
}

// String 防止默认格式化展开 command、membership 与 safe-return identity。
func (MutationResult) String() string { return "[REDACTED_VISIT_MUTATION_RESULT]" }

// GoString 防止 `%#v` 展开完整 replay result。
func (MutationResult) GoString() string { return "[REDACTED_VISIT_MUTATION_RESULT]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (MutationResult) LogValue() slog.Value {
	return slog.StringValue("[REDACTED_VISIT_MUTATION_RESULT]")
}

// validate 拒绝 operation 与可选 payload 矛盾或 directive 不属于 target session。
func (result MutationResult) validate() error {
	if result.operation == OperationUnspecified || !result.snapshot.Valid() || !result.commandID.Valid() || !result.fingerprint.Valid() {
		return errors.New("visit mutation result is incomplete")
	}
	for _, directive := range result.directives {
		if !directive.Valid() || directive.visitSessionID != result.snapshot.ID() {
			return errors.New("visit mutation result contains invalid directive")
		}
	}
	if len(result.retiredInvites) > maximumPendingInvites {
		return errors.New("visit mutation result contains too many retired invites")
	}
	for index, retired := range result.retiredInvites {
		if !retired.Valid() || retired.State() != InviteStatePending || snapshotContainsPendingInvite(result.snapshot, retired.ID()) {
			return errors.New("visit mutation result contains invalid retired invite")
		}
		if index > 0 && result.retiredInvites[index-1].ID().Value() >= retired.ID().Value() {
			return errors.New("visit mutation retired invites are not unique and sorted")
		}
	}
	for index := range result.directives {
		if result.directives[index].visitorID == result.snapshot.OwnerID() {
			return errors.New("visit mutation result directs immutable owner")
		}
		if index > 0 && result.directives[index-1].visitorID.String() >= result.directives[index].visitorID.String() {
			return errors.New("visit mutation directives are not unique and sorted")
		}
		for _, membership := range result.snapshot.memberships {
			if membership.visitorID == result.directives[index].visitorID {
				return errors.New("visit mutation directive retains target membership")
			}
		}
	}
	switch result.operation {
	case OperationCreateInvite:
		if !result.invite.Valid() || !snapshotContainsInvite(result.snapshot, result.invite) || result.admission.Valid() || result.membership.Valid() || len(result.directives) != 0 {
			return errors.New("create invite result is incomplete")
		}
	case OperationAcceptInvite:
		if !result.admission.Valid() || result.admission.visitSessionID != result.snapshot.ID() || !result.admission.assignment.Equal(result.snapshot.Assignment()) || !snapshotContainsReservation(result.snapshot, result.admission) || result.invite.Valid() || result.membership.Valid() || len(result.directives) != 0 {
			return errors.New("accept result is incomplete")
		}
	case OperationJoin, OperationVisitorReconnect:
		if !result.membership.Valid() || !snapshotContainsMembership(result.snapshot, result.membership) || result.invite.Valid() || result.admission.Valid() || len(result.directives) != 0 {
			return errors.New("membership result is incomplete")
		}
	case OperationLeave, OperationKick, OperationExpireVisitorReconnect:
		if result.invite.Valid() || result.admission.Valid() || result.membership.Valid() || len(result.directives) != 1 {
			return errors.New("single safe return result is incomplete")
		}
	default:
		if result.invite.Valid() || result.admission.Valid() || result.membership.Valid() {
			return errors.New("visit mutation result contains unexpected payload")
		}
	}
	return nil
}

// snapshotContainsPendingInvite 判断 target snapshot 是否仍保留指定 pending identity。
func snapshotContainsPendingInvite(snapshot Snapshot, inviteID InviteID) bool {
	for _, invite := range snapshot.invites {
		if invite.ID() == inviteID && invite.State() == InviteStatePending {
			return true
		}
	}
	return false
}

// snapshotContainsInvite 验证 result projection 精确存在于 target snapshot。
func snapshotContainsInvite(snapshot Snapshot, expected InviteSnapshot) bool {
	for _, invite := range snapshot.invites {
		if invite == expected {
			return true
		}
	}
	return false
}

// snapshotContainsReservation 验证 AdmissionIntent 与 target reserved membership 全部 lineage/deadline 一致。
func snapshotContainsReservation(snapshot Snapshot, intent AdmissionIntent) bool {
	for _, membership := range snapshot.memberships {
		if membership.visitorID == intent.visitorID && membership.state == MembershipStateReserved && membership.sessionID == intent.sessionID && membership.epoch == intent.epoch && membership.reservationExpiresAt.Equal(intent.expiresAt) {
			return true
		}
	}
	return false
}

// snapshotContainsMembership 验证 operation projection 与 target snapshot 精确一致。
func snapshotContainsMembership(snapshot Snapshot, expected MembershipSnapshot) bool {
	for _, membership := range snapshot.memberships {
		if membership == expected {
			return true
		}
	}
	return false
}

// TransitionRecord 是 store 在单一线性化点决议 replay、revision 与 target 的 CAS 请求。
type TransitionRecord struct {
	// operation 标识 mutation 与 result schema。
	operation Operation
	// visitSessionID 是 active aggregate identity。
	visitSessionID VisitSessionID
	// expectedRevision 是调用方读取并由 store 原子比较的正版本。
	expectedRevision Revision
	// commandID 与 fingerprint 必须在 revision 比较前决议 replay/conflict。
	commandID CommandID
	// fingerprint 绑定稳定 actor/target/binding/deadline，不包含 observedAt。
	fingerprint CommandFingerprint
	// result 在 expected revision 匹配时是完整 target；revision 已知不匹配时可为零。
	result MutationResult
}

// NewTransitionRecord 构造可提交的完整 target record。
func NewTransitionRecord(operation Operation, visitSessionID VisitSessionID, expectedRevision Revision, commandID CommandID, fingerprint CommandFingerprint, result MutationResult) (TransitionRecord, error) {
	record := TransitionRecord{operation: operation, visitSessionID: visitSessionID, expectedRevision: expectedRevision, commandID: commandID, fingerprint: fingerprint, result: result}
	if !record.valid() || !result.snapshot.Valid() || result.operation != operation || result.commandID != commandID || !result.fingerprint.Equal(fingerprint) || result.snapshot.ID() != visitSessionID || result.snapshot.Revision() != expectedRevision+1 {
		return TransitionRecord{}, errors.New("visit transition record is inconsistent")
	}
	return record, nil
}

// NewConflictProbe 构造只允许 store 返回 replay/conflict/not-found 的无 target record。
func NewConflictProbe(operation Operation, visitSessionID VisitSessionID, expectedRevision Revision, commandID CommandID, fingerprint CommandFingerprint) (TransitionRecord, error) {
	record := TransitionRecord{operation: operation, visitSessionID: visitSessionID, expectedRevision: expectedRevision, commandID: commandID, fingerprint: fingerprint}
	if !record.valid() {
		return TransitionRecord{}, errors.New("visit transition probe is incomplete")
	}
	return record, nil
}

// Operation 返回 mutation 类型。
func (record TransitionRecord) Operation() Operation { return record.operation }

// VisitSessionID 返回目标 aggregate identity。
func (record TransitionRecord) VisitSessionID() VisitSessionID { return record.visitSessionID }

// ExpectedRevision 返回 CAS 条件版本。
func (record TransitionRecord) ExpectedRevision() Revision { return record.expectedRevision }

// CommandID 返回 store 必须先行决议的重试 identity。
func (record TransitionRecord) CommandID() CommandID { return record.commandID }

// Fingerprint 返回相同 CommandID 必须精确匹配的语义摘要。
func (record TransitionRecord) Fingerprint() CommandFingerprint { return record.fingerprint }

// Result 返回 expected revision 匹配时的完整 target；probe 返回零值。
func (record TransitionRecord) Result() MutationResult { return record.result }

// HasResult 报告 record 是否携带允许 applied 的完整 target。
func (record TransitionRecord) HasResult() bool { return record.result.snapshot.Valid() }

// String 防止默认格式化展开 CAS、command 与 target snapshot。
func (TransitionRecord) String() string { return "[REDACTED_VISIT_TRANSITION_RECORD]" }

// GoString 防止 `%#v` 展开 transition 私有字段。
func (TransitionRecord) GoString() string { return "[REDACTED_VISIT_TRANSITION_RECORD]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (TransitionRecord) LogValue() slog.Value {
	return slog.StringValue("[REDACTED_VISIT_TRANSITION_RECORD]")
}

// valid 校验 replay/CAS 共有的 identity 条件。
func (record TransitionRecord) valid() bool {
	return record.operation != OperationUnspecified && record.visitSessionID.Valid() && record.expectedRevision.Valid() && record.commandID.Valid() && record.fingerprint.Valid()
}

// VisitSessionStore 是未来 Redis adapter 必须实现的最小原子运行态契约。
//
// Store 必须在每次 create/transition 的单一线性化点先决议 CommandID replay/conflict，再
// 比较 active index 与 revision。CommitUnknown 不能返回伪成功 result，调用方也不能换新
// CommandID 自动补写。成功、replay 与 found outcome 必须携带完整结果且 error 为 nil；
// 确定性冲突与 not-found 必须携带零值结果且 error 为 nil；NotCommitted/CommitUnknown
// 必须携带零值结果，可通过 error 保留 dependency cause。Unspecified 永远不是合法结果。
// 实现必须允许并发调用且不得持有 application callback。
type VisitSessionStore interface {
	// Create 原子维护每个 PersonalWorld 至多一个 active session，并保存完整 replay result。
	// Created/Replay 返回绑定当前 command 的完整结果；Existing 返回 command/fingerprint
	// 为零的 active snapshot；其他 outcome 不得返回部分 candidate 或 replay result。ctx 取消
	// 只有在实现能证明未创建时才能映射为 NotCommitted，否则必须返回 CommitUnknown。
	Create(ctx context.Context, record CreateRecord) (CreateResult, CreateOutcome, error)
	// ResolveActive 按 world active index 返回未关闭 snapshot；Found 返回完整 snapshot，
	// NotFound 返回零值 snapshot，dependency error 返回零值 snapshot 与 Unspecified outcome。
	ResolveActive(ctx context.Context, worldID personalworld.PersonalWorldID) (Snapshot, ResolveOutcome, error)
	// FindByID 返回指定 VisitSession snapshot，包括尚未清理的 terminal snapshot；读取结果
	// 与 error 的组合规则和 ResolveActive 相同。
	FindByID(ctx context.Context, visitSessionID VisitSessionID) (Snapshot, ResolveOutcome, error)
	// Commit 原子决议 replay、expected revision、target snapshot 与完整 result/directives。
	// Applied/Replay 返回首次完整结果；所有冲突、NotCommitted 与 CommitUnknown 返回零值结果。
	// ctx 取消只有在实现能证明未提交时才能映射为 NotCommitted，否则必须返回 CommitUnknown。
	Commit(ctx context.Context, record TransitionRecord) (MutationResult, MutationOutcome, error)
}
