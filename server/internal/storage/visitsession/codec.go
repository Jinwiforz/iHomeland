package visitsession

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	domain "github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// maximumLuaExactInteger 是Lua number可精确表达的最大整数，保护时间与序列比较。
const maximumLuaExactInteger int64 = 1<<53 - 1

// assignmentDTO 是完整AssignmentStamp的稳定JSON字段投影。
type assignmentDTO struct {
	// WorldID绑定持久PersonalWorld。
	WorldID string `json:"world_id"`
	// InstanceID绑定不可复活的运行实例。
	InstanceID string `json:"instance_id"`
	// NodeID绑定受信运行节点。
	NodeID string `json:"node_id"`
	// Generation保存单调实例代际。
	Generation uint64 `json:"generation"`
	// FencingToken保存写入围栏序列。
	FencingToken uint64 `json:"fencing_token"`
}

// authBindingDTO 是Owner认证lineage与连接条件的存储投影。
type authBindingDTO struct {
	// PlayerID必须与snapshot immutable Owner一致。
	PlayerID string `json:"player_id"`
	// SessionID固定认证lineage。
	SessionID string `json:"session_id"`
	// Epoch拒绝旧token与callback。
	Epoch uint64 `json:"epoch"`
	// ConnectionID精确区分新旧连接绑定。
	ConnectionID string `json:"connection_id"`
}

// inviteDTO 保存定向邀请及其UTC微秒deadline。
type inviteDTO struct {
	// ID是aggregate内唯一邀请identity。
	ID string `json:"id"`
	// TargetID是唯一允许accept的Visitor。
	TargetID string `json:"target_id"`
	// State使用封闭pending/accepted枚举。
	State string `json:"state"`
	// CreatedRevision绑定首次创建结果版本。
	CreatedRevision uint64 `json:"created_revision"`
	// ExpiresUS是等于即失效的UTC Unix微秒。
	ExpiresUS int64 `json:"expires_us"`
}

// membershipDTO 保存一个Visitor的reservation或连接恢复条件。
type membershipDTO struct {
	// VisitorID是membership唯一玩家身份。
	VisitorID string `json:"visitor_id"`
	// InviteID保留accept来源，不授予连接权限。
	InviteID string `json:"invite_id"`
	// State使用reserved/joined/reconnecting封闭枚举。
	State string `json:"state"`
	// SessionID绑定accept时认证lineage。
	SessionID string `json:"session_id"`
	// Epoch是旧lineage失效屏障。
	Epoch uint64 `json:"epoch"`
	// BindingID仅joined/reconnecting非空。
	BindingID string `json:"binding_id"`
	// ReservationExpiresUS仅reserved为正UTC Unix微秒。
	ReservationExpiresUS int64 `json:"reservation_expires_us"`
	// ReconnectGeneration仅reconnecting为正。
	ReconnectGeneration uint64 `json:"reconnect_generation"`
	// ReconnectExpiresUS仅reconnecting为正UTC Unix微秒。
	ReconnectExpiresUS int64 `json:"reconnect_expires_us"`
}

// snapshotDTO 是VisitSession完整且有界的canonical JSON schema。
type snapshotDTO struct {
	// ID是aggregate identity。
	ID string `json:"id"`
	// OwnerID是不可转移世界主人。
	OwnerID string `json:"owner_id"`
	// WorldID是绑定的PersonalWorld。
	WorldID string `json:"world_id"`
	// Assignment保存创建时完整current stamp。
	Assignment assignmentDTO `json:"assignment"`
	// Lifecycle使用open/owner_grace/closed封闭枚举。
	Lifecycle string `json:"lifecycle"`
	// Revision是已提交CAS版本。
	Revision uint64 `json:"revision"`
	// Capacity是1至32人的Visitor上限。
	Capacity uint8 `json:"capacity"`
	// CreatedUS是创建时间(UTC Unix微秒)。
	CreatedUS int64 `json:"created_us"`
	// ExpiresUS是会话到期时间(UTC Unix微秒)。
	ExpiresUS int64 `json:"expires_us"`
	// OwnerBinding保存当前或grace前最后连接条件。
	OwnerBinding authBindingDTO `json:"owner_binding"`
	// OwnerGraceGeneration仅owner_grace为正。
	OwnerGraceGeneration uint64 `json:"owner_grace_generation"`
	// OwnerGraceExpiresUS仅owner_grace为正UTC Unix微秒。
	OwnerGraceExpiresUS int64 `json:"owner_grace_expires_us"`
	// Invites按InviteID稳定排序。
	Invites []inviteDTO `json:"invites"`
	// Memberships按VisitorID稳定排序。
	Memberships []membershipDTO `json:"memberships"`
}

// immutableFactsDTO 只包含任何transition都不得替换的aggregate事实。
type immutableFactsDTO struct {
	// ID固定aggregate identity。
	ID string `json:"id"`
	// OwnerID固定不可转移主人。
	OwnerID string `json:"owner_id"`
	// WorldID固定PersonalWorld。
	WorldID string `json:"world_id"`
	// Assignment固定创建时完整placement stamp。
	Assignment assignmentDTO `json:"assignment"`
	// Capacity固定Visitor上限。
	Capacity uint8 `json:"capacity"`
	// CreatedUS固定创建时间(UTC Unix微秒)。
	CreatedUS int64 `json:"created_us"`
	// ExpiresUS固定会话到期时间(UTC Unix微秒)。
	ExpiresUS int64 `json:"expires_us"`
}

// admissionDTO 保存accept首次结果中的非凭据AdmissionIntent。
type admissionDTO struct {
	// VisitSessionID绑定reservation aggregate。
	VisitSessionID string `json:"visit_session_id"`
	// VisitorID绑定accept actor。
	VisitorID string `json:"visitor_id"`
	// SessionID绑定认证lineage。
	SessionID string `json:"session_id"`
	// Epoch拒绝旧session恢复。
	Epoch uint64 `json:"epoch"`
	// Assignment绑定accept时current stamp。
	Assignment assignmentDTO `json:"assignment"`
	// ExpiresUS是reservation到期时间(UTC Unix微秒)。
	ExpiresUS int64 `json:"expires_us"`
}

// directiveDTO 保存尚未执行网络side effect的确定性安全返回意图。
type directiveDTO struct {
	// VisitSessionID绑定产生directive的aggregate。
	VisitSessionID string `json:"visit_session_id"`
	// VisitorID是必须迁离旧assignment的玩家。
	VisitorID string `json:"visitor_id"`
	// Reason是客户端不可改写的封闭原因。
	Reason string `json:"reason"`
	// Revision绑定产生directive的已提交aggregate版本。
	Revision uint64 `json:"revision"`
}

// createResultDTO 保存Open首次完整result。
type createResultDTO struct {
	// Snapshot是首次选中的active aggregate。
	Snapshot snapshotDTO `json:"snapshot"`
	// CommandID绑定全局重试identity。
	CommandID string `json:"command_id"`
	// Fingerprint绑定稳定Open语义。
	Fingerprint string `json:"fingerprint"`
}

// mutationResultDTO 保存transition首次完整projection与directives。
type mutationResultDTO struct {
	// Operation决定可选result payload形状。
	Operation string `json:"operation"`
	// Snapshot是mutation后的完整target。
	Snapshot snapshotDTO `json:"snapshot"`
	// CommandID绑定全局重试identity。
	CommandID string `json:"command_id"`
	// Fingerprint绑定首次请求语义。
	Fingerprint string `json:"fingerprint"`
	// Invite仅create-invite存在。
	Invite *inviteDTO `json:"invite,omitempty"`
	// Admission仅accept存在且不是credential。
	Admission *admissionDTO `json:"admission,omitempty"`
	// Membership仅join/reconnect存在。
	Membership *membershipDTO `json:"membership,omitempty"`
	// RetiredInvites 保存本次 transition 从 pending 退役的稳定排序邀请。
	RetiredInvites []inviteDTO `json:"retired_invites,omitempty"`
	// Directives按VisitorID稳定排序。
	Directives []directiveDTO `json:"directives"`
}

// encodeSnapshot 将完整领域投影编码为稳定字段顺序的JSON对象。
func encodeSnapshot(snapshot domain.Snapshot) (string, error) {
	if !snapshot.Valid() {
		return "", errors.New("visit session snapshot is invalid")
	}
	encoded, err := json.Marshal(snapshotToDTO(snapshot))
	if err != nil || len(encoded) > maximumSessionBytes {
		return "", errors.New("visit session snapshot exceeds codec boundary")
	}
	return string(encoded), nil
}

// decodeSnapshot 要求payload已经是当前codec产生的canonical JSON。
func decodeSnapshot(payload string) (domain.Snapshot, error) {
	if payload == "" || len(payload) > maximumSessionBytes {
		return domain.Snapshot{}, errors.New("visit session snapshot payload is empty or oversized")
	}
	var dto snapshotDTO
	if err := decodeCanonicalJSON(payload, &dto); err != nil {
		return domain.Snapshot{}, err
	}
	return snapshotFromDTO(dto)
}

// snapshotFacts 生成Lua CAS比较使用的immutable facts SHA-256十六进制值。
//
// 摘要只用于证明target没有替换ID、Owner、World、assignment、capacity或创建/到期时间；
// 它不是带密钥MAC、credential或授权证据，不得用于提升Redis payload的可信级别。
func snapshotFacts(snapshot domain.Snapshot) (string, error) {
	if !snapshot.Valid() {
		return "", errors.New("visit session snapshot is invalid")
	}
	encoded, err := json.Marshal(immutableFactsDTO{
		ID:         snapshot.ID().Value(),
		OwnerID:    snapshot.OwnerID().String(),
		WorldID:    snapshot.WorldID().String(),
		Assignment: assignmentToDTO(snapshot.Assignment()),
		Capacity:   snapshot.Capacity().Uint8(),
		CreatedUS:  snapshot.CreatedAt().UTC().Truncate(time.Microsecond).UnixMicro(),
		ExpiresUS:  snapshot.ExpiresAt().UTC().Truncate(time.Microsecond).UnixMicro(),
	})
	if err != nil {
		return "", errors.New("visit immutable facts encoding failed")
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// encodeCreateResult 保存首次Open完整结果，不编码existing临时投影。
func encodeCreateResult(result domain.CreateResult) (string, error) {
	if !result.Snapshot().Valid() || !result.CommandID().Valid() || !result.Fingerprint().Valid() {
		return "", errors.New("visit create result is invalid")
	}
	dto := createResultDTO{Snapshot: snapshotToDTO(result.Snapshot()), CommandID: result.CommandID().Value(), Fingerprint: encodeFingerprint(result.Fingerprint())}
	return marshalCommandDTO(dto)
}

// decodeCreateResult 恢复首次Open result并重新执行领域交叉校验。
func decodeCreateResult(payload string) (domain.CreateResult, error) {
	var dto createResultDTO
	if err := decodeCommandDTO(payload, &dto); err != nil {
		return domain.CreateResult{}, err
	}
	snapshot, err := snapshotFromDTO(dto.Snapshot)
	if err != nil {
		return domain.CreateResult{}, err
	}
	commandID, err := domain.NewCommandID(dto.CommandID)
	if err != nil {
		return domain.CreateResult{}, err
	}
	fingerprint, err := decodeFingerprint(dto.Fingerprint)
	if err != nil {
		return domain.CreateResult{}, err
	}
	return domain.NewCreateResult(snapshot, commandID, fingerprint)
}

// encodeMutationResult 保存首次transition的全部projection与safe-return结果。
func encodeMutationResult(result domain.MutationResult) (string, error) {
	if !result.Snapshot().Valid() || !result.CommandID().Valid() || !result.Fingerprint().Valid() || result.Operation() == domain.OperationUnspecified {
		return "", errors.New("visit mutation result is invalid")
	}
	dto := mutationResultDTO{
		Operation: result.Operation().String(), Snapshot: snapshotToDTO(result.Snapshot()), CommandID: result.CommandID().Value(),
		Fingerprint: encodeFingerprint(result.Fingerprint()), Directives: make([]directiveDTO, 0, len(result.Directives())),
	}
	if value := result.Invite(); value.Valid() {
		item := inviteToDTO(value)
		dto.Invite = &item
	}
	if value := result.AdmissionIntent(); value.Valid() {
		item := admissionToDTO(value)
		dto.Admission = &item
	}
	if value := result.Membership(); value.Valid() {
		item := membershipToDTO(value)
		dto.Membership = &item
	}
	for _, retired := range result.RetiredInvites() {
		dto.RetiredInvites = append(dto.RetiredInvites, inviteToDTO(retired))
	}
	for _, directive := range result.Directives() {
		dto.Directives = append(dto.Directives, directiveDTO{VisitSessionID: directive.VisitSessionID().Value(), VisitorID: directive.VisitorID().String(), Reason: directive.Reason().String(), Revision: directive.Revision().Uint64()})
	}
	return marshalCommandDTO(dto)
}

// decodeMutationResult 恢复完整result并由NewMutationResult验证operation专属形状。
func decodeMutationResult(payload string) (domain.MutationResult, error) {
	var dto mutationResultDTO
	if err := decodeCommandDTO(payload, &dto); err != nil {
		return domain.MutationResult{}, err
	}
	operation, err := parseOperation(dto.Operation)
	if err != nil {
		return domain.MutationResult{}, err
	}
	snapshot, err := snapshotFromDTO(dto.Snapshot)
	if err != nil {
		return domain.MutationResult{}, err
	}
	commandID, err := domain.NewCommandID(dto.CommandID)
	if err != nil {
		return domain.MutationResult{}, err
	}
	fingerprint, err := decodeFingerprint(dto.Fingerprint)
	if err != nil {
		return domain.MutationResult{}, err
	}
	var invite domain.InviteSnapshot
	if dto.Invite != nil {
		invite, err = inviteFromDTO(*dto.Invite)
	}
	var admission domain.AdmissionIntent
	if err == nil && dto.Admission != nil {
		admission, err = admissionFromDTO(*dto.Admission)
	}
	var membership domain.MembershipSnapshot
	if err == nil && dto.Membership != nil {
		membership, err = membershipFromDTO(*dto.Membership)
	}
	retiredInvites := make([]domain.InviteSnapshot, 0, len(dto.RetiredInvites))
	for _, value := range dto.RetiredInvites {
		if err != nil {
			break
		}
		var retired domain.InviteSnapshot
		retired, err = inviteFromDTO(value)
		retiredInvites = append(retiredInvites, retired)
	}
	directives := make([]domain.SafeReturnDirective, 0, len(dto.Directives))
	for _, value := range dto.Directives {
		if err != nil {
			break
		}
		var directive domain.SafeReturnDirective
		directive, err = directiveFromDTO(value)
		directives = append(directives, directive)
	}
	if err != nil {
		return domain.MutationResult{}, err
	}
	return domain.NewMutationResultWithRetiredInvites(operation, snapshot, commandID, fingerprint, invite, admission, membership, retiredInvites, directives)
}

// snapshotToDTO 从已经过领域验证的snapshot生成稳定字段副本。
func snapshotToDTO(snapshot domain.Snapshot) snapshotDTO {
	invites := make([]inviteDTO, 0, len(snapshot.Invites()))
	for _, invite := range snapshot.Invites() {
		invites = append(invites, inviteToDTO(invite))
	}
	memberships := make([]membershipDTO, 0, len(snapshot.Memberships()))
	for _, membership := range snapshot.Memberships() {
		memberships = append(memberships, membershipToDTO(membership))
	}
	return snapshotDTO{
		ID: snapshot.ID().Value(), OwnerID: snapshot.OwnerID().String(), WorldID: snapshot.WorldID().String(), Assignment: assignmentToDTO(snapshot.Assignment()),
		Lifecycle: snapshot.Lifecycle().String(), Revision: snapshot.Revision().Uint64(), Capacity: uint8(snapshot.Capacity()), CreatedUS: snapshot.CreatedAt().UnixMicro(), ExpiresUS: snapshot.ExpiresAt().UnixMicro(),
		OwnerBinding: bindingToDTO(snapshot.OwnerBinding()), OwnerGraceGeneration: snapshot.OwnerGraceGeneration(), OwnerGraceExpiresUS: optionalUnixMicro(snapshot.OwnerGraceExpiresAt()), Invites: invites, Memberships: memberships,
	}
}

// snapshotFromDTO 逐层构造并最终执行NewSnapshot完整交叉校验。
func snapshotFromDTO(dto snapshotDTO) (domain.Snapshot, error) {
	id, err := domain.NewVisitSessionID(dto.ID)
	owner, ownerErr := account.NewPlayerID(dto.OwnerID)
	world, worldErr := personalworld.NewPersonalWorldID(dto.WorldID)
	assignment, assignmentErr := assignmentFromDTO(dto.Assignment)
	lifecycle, lifecycleErr := parseLifecycle(dto.Lifecycle)
	revision, revisionErr := domain.NewRevision(dto.Revision)
	capacity, capacityErr := domain.NewCapacity(dto.Capacity)
	created, createdErr := requiredTime(dto.CreatedUS)
	expires, expiresErr := requiredTime(dto.ExpiresUS)
	binding, bindingErr := bindingFromDTO(dto.OwnerBinding)
	grace, graceErr := optionalTime(dto.OwnerGraceExpiresUS)
	if errors.Join(err, ownerErr, worldErr, assignmentErr, lifecycleErr, revisionErr, capacityErr, createdErr, expiresErr, bindingErr, graceErr) != nil {
		return domain.Snapshot{}, errors.New("visit session snapshot scalar field is invalid")
	}
	invites := make([]domain.InviteSnapshot, 0, len(dto.Invites))
	for _, value := range dto.Invites {
		invite, parseErr := inviteFromDTO(value)
		if parseErr != nil {
			return domain.Snapshot{}, parseErr
		}
		invites = append(invites, invite)
	}
	memberships := make([]domain.MembershipSnapshot, 0, len(dto.Memberships))
	for _, value := range dto.Memberships {
		membership, parseErr := membershipFromDTO(value)
		if parseErr != nil {
			return domain.Snapshot{}, parseErr
		}
		memberships = append(memberships, membership)
	}
	return domain.NewSnapshot(id, owner, world, assignment, lifecycle, revision, capacity, created, expires, binding, dto.OwnerGraceGeneration, grace, invites, memberships)
}

// assignmentToDTO 保留stale判断需要的全部stamp字段。
func assignmentToDTO(value placement.AssignmentStamp) assignmentDTO {
	return assignmentDTO{WorldID: value.WorldID().String(), InstanceID: value.InstanceID().String(), NodeID: value.NodeID().String(), Generation: value.Generation().Uint64(), FencingToken: value.FencingToken().Uint64()}
}

// assignmentFromDTO 拒绝缺失world、node、generation或fence的旧资格。
func assignmentFromDTO(dto assignmentDTO) (placement.AssignmentStamp, error) {
	world, worldErr := personalworld.NewPersonalWorldID(dto.WorldID)
	instance, instanceErr := placement.NewWorldInstanceID(dto.InstanceID)
	node, nodeErr := placement.NewRuntimeNodeID(dto.NodeID)
	generation, generationErr := placement.NewAssignmentGeneration(dto.Generation)
	fence, fenceErr := placement.NewFencingToken(dto.FencingToken)
	if errors.Join(worldErr, instanceErr, nodeErr, generationErr, fenceErr) != nil {
		return placement.AssignmentStamp{}, errors.New("visit assignment is invalid")
	}
	return placement.NewAssignmentStamp(world, instance, node, generation, fence)
}

// bindingToDTO 保存旧callback拒绝所需的完整认证与连接条件。
func bindingToDTO(value domain.AuthBinding) authBindingDTO {
	actor := value.Actor()
	return authBindingDTO{PlayerID: actor.PlayerID().String(), SessionID: actor.SessionID().String(), Epoch: uint64(actor.Epoch()), ConnectionID: value.ConnectionID().Value()}
}

// bindingFromDTO 只恢复snapshot条件事实，不创建AuthContext。
func bindingFromDTO(dto authBindingDTO) (domain.AuthBinding, error) {
	player, playerErr := account.NewPlayerID(dto.PlayerID)
	sessionID, sessionErr := session.NewSessionID(dto.SessionID)
	epoch := session.Epoch(dto.Epoch)
	connection, connectionErr := domain.NewConnectionBindingID(dto.ConnectionID)
	if errors.Join(playerErr, sessionErr, connectionErr) != nil || !epoch.Valid() {
		return domain.AuthBinding{}, errors.New("visit auth binding is invalid")
	}
	return domain.HydrateAuthBinding(player, sessionID, epoch, connection)
}

// inviteToDTO 编码一个有界定向邀请。
func inviteToDTO(value domain.InviteSnapshot) inviteDTO {
	return inviteDTO{ID: value.ID().Value(), TargetID: value.TargetID().String(), State: value.State().String(), CreatedRevision: value.CreatedRevision().Uint64(), ExpiresUS: value.ExpiresAt().UnixMicro()}
}

// inviteFromDTO 通过领域constructor恢复邀请事实。
func inviteFromDTO(dto inviteDTO) (domain.InviteSnapshot, error) {
	id, idErr := domain.NewInviteID(dto.ID)
	target, targetErr := account.NewPlayerID(dto.TargetID)
	state, stateErr := parseInviteState(dto.State)
	revision, revisionErr := domain.NewRevision(dto.CreatedRevision)
	expires, expiresErr := requiredTime(dto.ExpiresUS)
	if errors.Join(idErr, targetErr, stateErr, revisionErr, expiresErr) != nil {
		return domain.InviteSnapshot{}, errors.New("visit invite is invalid")
	}
	return domain.NewInviteSnapshot(id, target, state, revision, expires)
}

// membershipToDTO 按state保留reservation或reconnect专属字段。
func membershipToDTO(value domain.MembershipSnapshot) membershipDTO {
	return membershipDTO{VisitorID: value.VisitorID().String(), InviteID: value.InviteID().Value(), State: value.State().String(), SessionID: value.SessionID().String(), Epoch: uint64(value.Epoch()), BindingID: value.BindingID().Value(), ReservationExpiresUS: optionalUnixMicro(value.ReservationExpiresAt()), ReconnectGeneration: value.ReconnectGeneration(), ReconnectExpiresUS: optionalUnixMicro(value.ReconnectExpiresAt())}
}

// membershipFromDTO 拒绝state与binding/deadline字段组合矛盾。
func membershipFromDTO(dto membershipDTO) (domain.MembershipSnapshot, error) {
	visitor, visitorErr := account.NewPlayerID(dto.VisitorID)
	invite, inviteErr := domain.NewInviteID(dto.InviteID)
	state, stateErr := parseMembershipState(dto.State)
	sessionID, sessionErr := session.NewSessionID(dto.SessionID)
	epoch := session.Epoch(dto.Epoch)
	var binding domain.ConnectionBindingID
	var bindingErr error
	if dto.BindingID != "" {
		binding, bindingErr = domain.NewConnectionBindingID(dto.BindingID)
	}
	reservation, reservationErr := optionalTime(dto.ReservationExpiresUS)
	reconnect, reconnectErr := optionalTime(dto.ReconnectExpiresUS)
	if errors.Join(visitorErr, inviteErr, stateErr, sessionErr, bindingErr, reservationErr, reconnectErr) != nil || !epoch.Valid() {
		return domain.MembershipSnapshot{}, errors.New("visit membership is invalid")
	}
	return domain.NewMembershipSnapshot(visitor, invite, state, sessionID, epoch, binding, reservation, dto.ReconnectGeneration, reconnect)
}

// admissionToDTO 编码非凭据reservation投影，不添加nonce或endpoint。
func admissionToDTO(value domain.AdmissionIntent) admissionDTO {
	return admissionDTO{VisitSessionID: value.VisitSessionID().Value(), VisitorID: value.VisitorID().String(), SessionID: value.SessionID().String(), Epoch: uint64(value.Epoch()), Assignment: assignmentToDTO(value.Assignment()), ExpiresUS: value.ExpiresAt().UnixMicro()}
}

// admissionFromDTO 恢复非凭据投影，不能创建JoinQualification。
func admissionFromDTO(dto admissionDTO) (domain.AdmissionIntent, error) {
	visitID, visitErr := domain.NewVisitSessionID(dto.VisitSessionID)
	visitor, visitorErr := account.NewPlayerID(dto.VisitorID)
	sessionID, sessionErr := session.NewSessionID(dto.SessionID)
	epoch := session.Epoch(dto.Epoch)
	assignment, assignmentErr := assignmentFromDTO(dto.Assignment)
	expires, expiresErr := requiredTime(dto.ExpiresUS)
	if errors.Join(visitErr, visitorErr, sessionErr, assignmentErr, expiresErr) != nil || !epoch.Valid() {
		return domain.AdmissionIntent{}, errors.New("visit admission intent is invalid")
	}
	return domain.HydrateAdmissionIntent(visitID, visitor, sessionID, epoch, assignment, expires)
}

// directiveFromDTO 只允许领域登记的封闭reason与固定返回目标。
func directiveFromDTO(dto directiveDTO) (domain.SafeReturnDirective, error) {
	visitID, visitErr := domain.NewVisitSessionID(dto.VisitSessionID)
	visitor, visitorErr := account.NewPlayerID(dto.VisitorID)
	reason, reasonErr := parseSafeReturnReason(dto.Reason)
	revision, revisionErr := domain.NewRevision(dto.Revision)
	if errors.Join(visitErr, visitorErr, reasonErr, revisionErr) != nil {
		return domain.SafeReturnDirective{}, errors.New("visit safe return directive is invalid")
	}
	return domain.NewSafeReturnDirective(visitID, visitor, reason, revision)
}

// marshalCommandDTO 对完整result实施独立encoded budget。
func marshalCommandDTO(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maximumCommandBytes {
		return "", errors.New("visit command result exceeds codec boundary")
	}
	return string(encoded), nil
}

// decodeCommandDTO 在解释result kind前拒绝空值和超预算输入。
func decodeCommandDTO(payload string, target any) error {
	if payload == "" || len(payload) > maximumCommandBytes {
		return errors.New("visit command payload is empty or oversized")
	}
	return decodeCanonicalJSON(payload, target)
}

// decodeCanonicalJSON 拒绝unknown field、尾随数据与任何非规范重编码。
func decodeCanonicalJSON(payload string, target any) error {
	decoder := json.NewDecoder(bytes.NewReader([]byte(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("visit JSON payload is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("visit JSON payload has trailing data")
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(canonical, []byte(payload)) {
		return errors.New("visit JSON payload is not canonical")
	}
	return nil
}

// validateHashEncoding 按 Redis Hash 实际 field name/value 累计字节数执行 definition 预算。
//
// fields 必须是交替的 name/value；这使 metadata 与 JSON payload 共享同一硬上限，
// 避免只校验 payload 而让实际 Hash 超出 registry 声明。
func validateHashEncoding(definition storageredis.Definition, fields ...string) error {
	if len(fields) == 0 || len(fields)%2 != 0 {
		return errors.New("visit redis hash encoding is incomplete")
	}
	encodedBytes := 0
	for _, field := range fields {
		encodedBytes += len(field)
	}
	return storageredis.ValidateEncodedSize(definition, redisSchemaVersion, encodedBytes)
}

// encodeFingerprint 返回固定32字节摘要的小写hex存储表达。
func encodeFingerprint(value domain.CommandFingerprint) string {
	digest := value.Digest()
	return hex.EncodeToString(digest[:])
}

// decodeFingerprint 拒绝错误长度、大小写或零摘要。
func decodeFingerprint(value string) (domain.CommandFingerprint, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || value != hex.EncodeToString(decoded) {
		return domain.CommandFingerprint{}, errors.New("visit command fingerprint is invalid")
	}
	var digest [sha256.Size]byte
	copy(digest[:], decoded)
	return domain.NewCommandFingerprint(digest)
}

// requiredTime 恢复Lua精确整数范围内的正UTC Unix微秒。
func requiredTime(value int64) (time.Time, error) {
	if value <= 0 || value > maximumLuaExactInteger {
		return time.Time{}, errors.New("visit timestamp is outside supported UTC microsecond range")
	}
	return time.UnixMicro(value).UTC(), nil
}

// optionalTime 保留状态专属deadline使用的严格零值。
func optionalTime(value int64) (time.Time, error) {
	if value == 0 {
		return time.Time{}, nil
	}
	return requiredTime(value)
}

// optionalUnixMicro 移除单调分量并把零时间编码为0。
func optionalUnixMicro(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().Truncate(time.Microsecond).UnixMicro()
}

// canonicalUint 生成不经过Lua double的十进制无符号整数。
func canonicalUint(value uint64) string { return strconv.FormatUint(value, 10) }

// canonicalTime 将有效UTC时间编码为Lua可精确比较的Unix微秒十进制文本。
func canonicalTime(value time.Time) (string, error) {
	microseconds := value.UTC().Truncate(time.Microsecond).UnixMicro()
	if value.IsZero() || microseconds <= 0 || microseconds > maximumLuaExactInteger {
		return "", errors.New("visit timestamp is outside supported Redis range")
	}
	return strconv.FormatInt(microseconds, 10), nil
}

// expiryMilliseconds 向后取整，确保Redis物理TTL不会早于领域微秒deadline。
func expiryMilliseconds(value time.Time) (string, error) {
	microseconds := value.UTC().Truncate(time.Microsecond).UnixMicro()
	if value.IsZero() || microseconds <= 0 || microseconds > maximumLuaExactInteger {
		return "", errors.New("visit expiry is outside supported Redis range")
	}
	milliseconds := microseconds / 1000
	if microseconds%1000 != 0 {
		milliseconds++
	}
	return strconv.FormatInt(milliseconds, 10), nil
}

// parseLifecycle 只接受领域封闭生命周期名称。
func parseLifecycle(value string) (domain.Lifecycle, error) {
	switch value {
	case "open":
		return domain.LifecycleOpen, nil
	case "owner_grace":
		return domain.LifecycleOwnerGrace, nil
	case "closed":
		return domain.LifecycleClosed, nil
	default:
		return domain.LifecycleUnspecified, errors.New("visit lifecycle is unknown")
	}
}

// parseInviteState 只接受pending或accepted。
func parseInviteState(value string) (domain.InviteState, error) {
	switch value {
	case "pending":
		return domain.InviteStatePending, nil
	case "accepted":
		return domain.InviteStateAccepted, nil
	default:
		return domain.InviteStateUnspecified, errors.New("visit invite state is unknown")
	}
}

// parseMembershipState 只接受三个已实现membership阶段。
func parseMembershipState(value string) (domain.MembershipState, error) {
	switch value {
	case "reserved":
		return domain.MembershipStateReserved, nil
	case "joined":
		return domain.MembershipStateJoined, nil
	case "reconnecting":
		return domain.MembershipStateReconnecting, nil
	default:
		return domain.MembershipStateUnspecified, errors.New("visit membership state is unknown")
	}
}

// parseOperation 拒绝未登记或unspecified mutation。
func parseOperation(value string) (domain.Operation, error) {
	for operation := domain.OperationOpen; operation <= domain.OperationDependencyLost; operation++ {
		if operation.String() == value {
			return operation, nil
		}
	}
	return domain.OperationUnspecified, errors.New("visit operation is unknown")
}

// parseSafeReturnReason 拒绝transport自造的同义reason。
func parseSafeReturnReason(value string) (domain.SafeReturnReason, error) {
	for reason := domain.SafeReturnReasonVoluntaryLeave; reason <= domain.SafeReturnReasonDependencyLost; reason++ {
		if reason.String() == value {
			return reason, nil
		}
	}
	return domain.SafeReturnReasonUnspecified, errors.New("visit safe return reason is unknown")
}

// validateSessionMetadata 防止合法payload被装入矛盾Hash metadata。
func validateSessionMetadata(snapshot domain.Snapshot, id string, world string, revision string, lifecycle string, expiresUS string, facts string) error {
	payload, err := encodeSnapshot(snapshot)
	if err != nil {
		return err
	}
	if err := validateHashEncoding(sessionDefinition(), "v", strconv.FormatUint(uint64(redisSchemaVersion), 10), "visit_id", id, "world", world, "revision", revision, "lifecycle", lifecycle, "expires_us", expiresUS, "facts", facts, "payload", payload); err != nil {
		return err
	}
	if snapshot.ID().Value() != id || snapshot.WorldID().String() != world || canonicalUint(snapshot.Revision().Uint64()) != revision || snapshot.Lifecycle().String() != lifecycle {
		return errors.New("visit session metadata contradicts payload")
	}
	expires, err := canonicalTime(snapshot.ExpiresAt())
	if err != nil || expires != expiresUS {
		return errors.New("visit session expiry contradicts payload")
	}
	expectedFacts, err := snapshotFacts(snapshot)
	if err != nil || expectedFacts != facts {
		return errors.New("visit session immutable facts contradict payload")
	}
	return nil
}

// validateCommandMetadata 防止replay key把完整result绑定到其他command或aggregate。
func validateCommandMetadata(kind string, fingerprint string, visitID string, world string, revision string, expiresUS string, payload string) error {
	if err := validateHashEncoding(commandDefinition(), "v", strconv.FormatUint(uint64(redisSchemaVersion), 10), "kind", kind, "fingerprint", fingerprint, "visit_id", visitID, "world", world, "revision", revision, "expires_us", expiresUS, "payload", payload); err != nil {
		return err
	}
	switch kind {
	case "create":
		result, err := decodeCreateResult(payload)
		resultExpiry, expiryErr := canonicalTime(result.Snapshot().ExpiresAt())
		if err != nil || expiryErr != nil || encodeFingerprint(result.Fingerprint()) != fingerprint || result.Snapshot().ID().Value() != visitID || result.Snapshot().WorldID().String() != world || canonicalUint(result.Snapshot().Revision().Uint64()) != revision || resultExpiry != expiresUS {
			return errors.New("visit create replay metadata contradicts payload")
		}
	case "mutation":
		result, err := decodeMutationResult(payload)
		resultExpiry, expiryErr := canonicalTime(result.Snapshot().ExpiresAt())
		if err != nil || expiryErr != nil || encodeFingerprint(result.Fingerprint()) != fingerprint || result.Snapshot().ID().Value() != visitID || result.Snapshot().WorldID().String() != world || canonicalUint(result.Snapshot().Revision().Uint64()) != revision || resultExpiry != expiresUS {
			return errors.New("visit mutation replay metadata contradicts payload")
		}
	default:
		return errors.New("visit command kind is unknown")
	}
	return nil
}
