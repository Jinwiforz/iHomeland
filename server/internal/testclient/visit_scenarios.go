package testclient

import (
	"context"
	"errors"
	"time"

	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"google.golang.org/protobuf/proto"
)

// joinedVisitFixture 保存一个完全通过公开网络建立的双 actor VisitSession。
type joinedVisitFixture struct {
	// scenario 拥有全部 actor 与连接 cleanup。
	scenario *ScenarioContext
	// runtime 提供公开 HTTP endpoint 与本次临时 TLS trust。
	runtime *ScenarioRuntime
	// owner 是 VisitSession immutable Owner。
	owner accountCredential
	// visitor 是 invite 唯一目标和 joined member。
	visitor accountCredential
	// ownerTCP 是绑定 OWN_WORLD admission 的 gameplay connection。
	ownerTCP *TCPClient
	// visitorTCP 是绑定 JOIN admission 的 gameplay connection。
	visitorTCP *TCPClient
	// visitorWSS 接收定向 invite/close control push。
	visitorWSS *WSSClient
	// visitSessionID 是公开 aggregate identity。
	visitSessionID string
	// revision 是最后一次已观察提交的公开 aggregate revision。
	revision uint64
}

// runVisitOpenInviteJoin 验证 OPEN、定向 invite、WSS push、HTTPS accept 与 JOIN 收敛。
func runVisitOpenInviteJoin(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	fixture, err := setupJoinedVisit(ctx, runtime)
	if fixture != nil {
		defer closeScenario(fixture.scenario, &resultErr)
	}
	if err != nil {
		return err
	}
	response, err := fixture.ownerTCP.Request(ctx, 2119, visitv1.VisitSnapshotRequest_builder{}.Build())
	if err != nil {
		return err
	}
	snapshot, ok := response.Payload.(*visitv1.VisitSnapshotResponse)
	if !ok || snapshot.GetSnapshot() == nil || snapshot.GetSnapshot().GetVisitSessionId() != fixture.visitSessionID || snapshot.GetSnapshot().GetRevision() != fixture.revision {
		return errors.New("Owner VisitSession snapshot did not converge after JOIN")
	}
	return nil
}

// runVisitLeave 验证 Visitor LEAVE response、单目标 safe-return push 与 revision 单调。
func runVisitLeave(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	fixture, err := setupJoinedVisit(ctx, runtime)
	if fixture != nil {
		defer closeScenario(fixture.scenario, &resultErr)
	}
	if err != nil {
		return err
	}
	response, err := fixture.visitorTCP.Command(ctx, 2111, visitv1.VisitLeaveCommand_builder{ExpectedRevision: proto.Uint64(fixture.revision)}.Build())
	if err != nil {
		return err
	}
	left, ok := response.Payload.(*visitv1.VisitLeaveResponse)
	if !ok || left.GetResult() == nil || left.GetResult().GetSnapshot() == nil || len(left.GetResult().GetSafeReturns()) != 1 {
		return errors.New("Visitor LEAVE response omitted authoritative result")
	}
	after := left.GetResult().GetSnapshot().GetRevision()
	if err := requireRevisionIncrease(fixture.revision, after); err != nil {
		return err
	}
	return expectVisitorSafeReturn(ctx, fixture, visitv1.SafeReturnReason_SAFE_RETURN_REASON_VOLUNTARY_LEAVE)
}

// runVisitKick 验证只有 Owner 可精确 KICK 目标 Visitor 并触发 safe-return。
func runVisitKick(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	fixture, err := setupJoinedVisit(ctx, runtime)
	if fixture != nil {
		defer closeScenario(fixture.scenario, &resultErr)
	}
	if err != nil {
		return err
	}
	command := visitv1.VisitKickCommand_builder{TargetVisitorId: proto.String(fixture.visitor.actor.PlayerID), ExpectedRevision: proto.Uint64(fixture.revision)}.Build()
	response, err := fixture.ownerTCP.Command(ctx, 2113, command)
	if err != nil {
		return err
	}
	kicked, ok := response.Payload.(*visitv1.VisitKickResponse)
	if !ok || kicked.GetResult() == nil || kicked.GetResult().GetSnapshot() == nil || len(kicked.GetResult().GetSafeReturns()) != 1 {
		return errors.New("Owner KICK response omitted authoritative result")
	}
	if err := requireRevisionIncrease(fixture.revision, kicked.GetResult().GetSnapshot().GetRevision()); err != nil {
		return err
	}
	return expectVisitorSafeReturn(ctx, fixture, visitv1.SafeReturnReason_SAFE_RETURN_REASON_KICKED)
}

// runVisitClose 验证 Owner CLOSE 形成 terminal snapshot 并只关闭目标 Visitor gameplay。
func runVisitClose(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	fixture, err := setupJoinedVisit(ctx, runtime)
	if fixture != nil {
		defer closeScenario(fixture.scenario, &resultErr)
	}
	if err != nil {
		return err
	}
	response, err := fixture.ownerTCP.Command(ctx, 2117, visitv1.VisitCloseCommand_builder{ExpectedRevision: proto.Uint64(fixture.revision)}.Build())
	if err != nil {
		return err
	}
	closed, ok := response.Payload.(*visitv1.VisitCloseResponse)
	if !ok || closed.GetResult() == nil || closed.GetResult().GetSnapshot() == nil || closed.GetResult().GetSnapshot().GetLifecycle() != visitv1.VisitLifecycle_VISIT_LIFECYCLE_CLOSED || len(closed.GetResult().GetSafeReturns()) != 1 {
		return errors.New("Owner CLOSE response omitted terminal result")
	}
	if err := expectVisitorSafeReturn(ctx, fixture, visitv1.SafeReturnReason_SAFE_RETURN_REASON_OWNER_CLOSED); err != nil {
		return err
	}
	if _, err := fixture.ownerTCP.Request(ctx, 2000, newWorldSnapshotRequest()); err != nil {
		return errors.New("Owner own-world connection was affected by VisitSession close")
	}
	return nil
}

// runVisitCloseMultiple 验证两个 Visitor 同时在线时 CLOSE 生成稳定双目标 safe-return。
func runVisitCloseMultiple(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	fixture, err := setupJoinedVisit(ctx, runtime)
	if fixture != nil {
		defer closeScenario(fixture.scenario, &resultErr)
	}
	if err != nil {
		return err
	}
	additional, err := fixture.joinAdditionalVisitor(ctx)
	if err != nil {
		return err
	}
	response, err := fixture.ownerTCP.Command(ctx, 2117, visitv1.VisitCloseCommand_builder{ExpectedRevision: proto.Uint64(fixture.revision)}.Build())
	if err != nil {
		return err
	}
	closed, ok := response.Payload.(*visitv1.VisitCloseResponse)
	if !ok || closed.GetResult() == nil || closed.GetResult().GetSnapshot() == nil || closed.GetResult().GetSnapshot().GetLifecycle() != visitv1.VisitLifecycle_VISIT_LIFECYCLE_CLOSED || len(closed.GetResult().GetSafeReturns()) != 2 {
		return errors.New("multi-Visitor CLOSE response omitted deterministic safe-return set")
	}
	if err := expectVisitorSafeReturn(ctx, fixture, visitv1.SafeReturnReason_SAFE_RETURN_REASON_OWNER_CLOSED); err != nil {
		return err
	}
	return expectSafeReturnFor(ctx, additional.tcp, fixture.visitSessionID, additional.account.actor.PlayerID, visitv1.SafeReturnReason_SAFE_RETURN_REASON_OWNER_CLOSED)
}

// additionalVisitor 保存复用同一 VisitSession 加入的第二个独立 actor。
type additionalVisitor struct {
	// account 是第二个 Visitor 的独立 session/credential owner。
	account accountCredential
	// tcp 是第二个 Visitor 的 JOIN gameplay connection。
	tcp *TCPClient
	// wss 是第二个 Visitor 的定向 control connection。
	wss *WSSClient
}

// joinAdditionalVisitor 复用 Owner current revision，以完整 invite/accept/JOIN 路径加入第二个 Visitor。
func (fixture *joinedVisitFixture) joinAdditionalVisitor(ctx context.Context) (additionalVisitor, error) {
	var additional additionalVisitor
	var err error
	additional.account, err = registerScenarioActor(fixture.scenario, fixture.runtime)
	if err != nil {
		return additional, err
	}
	bootstrap, err := worldBootstrapEventually(ctx, fixture.runtime.HTTP, additional.account.actor.AccessToken)
	if err != nil {
		return additional, err
	}
	additional.account.actor.PlayerID = bootstrap.World.OwnerPlayerID
	additional.wss, err = dialScenarioWSS(ctx, fixture.runtime, additional.account.actor)
	if err != nil {
		return additional, err
	}
	if err := fixture.scenario.Track(additional.wss); err != nil {
		return additional, err
	}
	create := visitv1.VisitCreateInviteCommand_builder{
		TargetVisitorId: proto.String(additional.account.actor.PlayerID), InviteLifetimeMs: proto.Uint32(20_000), ExpectedRevision: proto.Uint64(fixture.revision),
	}.Build()
	createdMessage, err := fixture.ownerTCP.Command(ctx, 2105, create)
	if err != nil {
		return additional, err
	}
	created, ok := createdMessage.Payload.(*visitv1.VisitCreateInviteResponse)
	if !ok || created.GetInvite() == nil || created.GetResult() == nil || created.GetResult().GetSnapshot() == nil {
		return additional, errors.New("additional Visitor invite response is incomplete")
	}
	fixture.revision = created.GetResult().GetSnapshot().GetRevision()
	pushContext, cancelPush := scenarioDeadline(ctx, 5*time.Second)
	_, err = waitMessage(pushContext, additional.wss.Messages(), 2100)
	cancelPush()
	if err != nil {
		return additional, err
	}
	acceptKey, err := NewIdentity("accept", 16)
	if err != nil {
		return additional, err
	}
	accepted, err := fixture.runtime.HTTP.AcceptVisitInvite(ctx, additional.account.actor.AccessToken, fixture.visitSessionID, created.GetInvite().GetInviteId(), acceptKey, AcceptVisitInviteRequest{ExpectedRevision: fixture.revision})
	if err != nil {
		return additional, err
	}
	fixture.revision = accepted.Reservation.Revision
	ticketResponse, err := fixture.runtime.HTTP.IssueTicket(ctx, additional.account.actor.AccessToken, TicketRequest{Channel: "TLS_TCP"})
	if err != nil {
		return additional, err
	}
	admissionKey, err := NewIdentity("admission", 16)
	if err != nil {
		return additional, err
	}
	admissionResponse, err := fixture.runtime.HTTP.IssueWorldAdmission(ctx, additional.account.actor.AccessToken, admissionKey, WorldAdmissionRequest{Kind: "VISIT_WORLD", VisitSessionID: fixture.visitSessionID})
	if err != nil {
		return additional, err
	}
	ticket, admission, err := newCredentialPair(ticketResponse.Ticket, admissionResponse.Credential)
	ticketResponse.Ticket = ""
	admissionResponse.Credential = ""
	if err != nil {
		return additional, err
	}
	additional.tcp, err = DialTCP(ctx, admissionResponse.Endpoint, ticket, admission, "JOIN", fixture.runtime.TLSConfig, fixture.runtime.Bootstrap.Limits.RealtimeFrameBytes)
	if err != nil {
		return additional, err
	}
	if err := fixture.scenario.Track(additional.tcp); err != nil {
		return additional, err
	}
	credential, err := additional.tcp.TakeAdmission()
	if err != nil {
		return additional, err
	}
	joinedMessage, err := additional.tcp.Command(ctx, 2109, visitv1.VisitJoinCommand_builder{AdmissionCredential: proto.String(credential), ExpectedRevision: proto.Uint64(fixture.revision)}.Build())
	if err != nil {
		return additional, err
	}
	joined, ok := joinedMessage.Payload.(*visitv1.VisitJoinResponse)
	if !ok || joined.GetResult() == nil || joined.GetResult().GetSnapshot() == nil || membershipState(joined.GetResult().GetSnapshot(), additional.account.actor.PlayerID) != visitv1.VisitMembershipState_VISIT_MEMBERSHIP_STATE_JOINED {
		return additional, errors.New("additional Visitor JOIN response is incomplete")
	}
	fixture.revision = joined.GetResult().GetSnapshot().GetRevision()
	return additional, nil
}

// setupJoinedVisit 只通过 HTTPS/WSS/TLS_TCP 建立一个 joined Visitor membership。
func setupJoinedVisit(ctx context.Context, runtime *ScenarioRuntime) (*joinedVisitFixture, error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return nil, err
	}
	fixture := &joinedVisitFixture{scenario: scenario, runtime: runtime}
	fixture.owner, err = registerScenarioActor(scenario, runtime)
	if err != nil {
		return fixture, err
	}
	fixture.visitor, err = registerScenarioActor(scenario, runtime)
	if err != nil {
		return fixture, err
	}
	ownerBootstrap, err := worldBootstrapEventually(ctx, runtime.HTTP, fixture.owner.actor.AccessToken)
	if err != nil {
		return fixture, err
	}
	visitorBootstrap, err := worldBootstrapEventually(ctx, runtime.HTTP, fixture.visitor.actor.AccessToken)
	if err != nil {
		return fixture, err
	}
	fixture.owner.actor.PlayerID = ownerBootstrap.World.OwnerPlayerID
	fixture.visitor.actor.PlayerID = visitorBootstrap.World.OwnerPlayerID
	fixture.visitorWSS, err = dialScenarioWSS(ctx, runtime, fixture.visitor.actor)
	if err != nil {
		return fixture, err
	}
	if err := scenario.Track(fixture.visitorWSS); err != nil {
		return fixture, err
	}
	fixture.ownerTCP, err = dialOwnWorldTCP(ctx, runtime, fixture.owner.actor)
	if err != nil {
		return fixture, err
	}
	if err := scenario.Track(fixture.ownerTCP); err != nil {
		return fixture, err
	}
	openMessage, err := fixture.ownerTCP.Command(ctx, 2103, visitv1.VisitOpenCommand_builder{}.Build())
	if err != nil {
		return fixture, err
	}
	opened, ok := openMessage.Payload.(*visitv1.VisitOpenResponse)
	if !ok || opened.GetSnapshot() == nil || opened.GetSnapshot().GetVisitSessionId() == "" {
		return fixture, errors.New("VisitSession OPEN response is incomplete")
	}
	fixture.visitSessionID = opened.GetSnapshot().GetVisitSessionId()
	fixture.revision = opened.GetSnapshot().GetRevision()
	createCommand := visitv1.VisitCreateInviteCommand_builder{
		TargetVisitorId: proto.String(fixture.visitor.actor.PlayerID), InviteLifetimeMs: proto.Uint32(20_000), ExpectedRevision: proto.Uint64(fixture.revision),
	}.Build()
	createMessage, err := fixture.ownerTCP.Command(ctx, 2105, createCommand)
	if err != nil {
		return fixture, err
	}
	created, ok := createMessage.Payload.(*visitv1.VisitCreateInviteResponse)
	if !ok || created.GetInvite() == nil || created.GetResult() == nil || created.GetResult().GetSnapshot() == nil || created.GetInvite().GetTargetVisitorId() != fixture.visitor.actor.PlayerID {
		return fixture, errors.New("VisitSession CREATE_INVITE response is incomplete")
	}
	if err := requireRevisionIncrease(fixture.revision, created.GetResult().GetSnapshot().GetRevision()); err != nil {
		return fixture, err
	}
	fixture.revision = created.GetResult().GetSnapshot().GetRevision()
	pushContext, cancelPush := scenarioDeadline(ctx, 5*time.Second)
	inviteMessage, err := waitMessage(pushContext, fixture.visitorWSS.Messages(), 2100)
	cancelPush()
	if err != nil {
		return fixture, err
	}
	invitePush, ok := inviteMessage.Payload.(*visitv1.VisitInvitePush)
	if !ok || invitePush.GetInvite() == nil || invitePush.GetInvite().GetInviteId() != created.GetInvite().GetInviteId() || invitePush.GetInvite().GetTargetVisitorId() != fixture.visitor.actor.PlayerID {
		return fixture, errors.New("target Visitor invite push drifted")
	}
	idempotencyKey, err := NewIdentity("accept", 16)
	if err != nil {
		return fixture, err
	}
	accepted, err := runtime.HTTP.AcceptVisitInvite(ctx, fixture.visitor.actor.AccessToken, fixture.visitSessionID, created.GetInvite().GetInviteId(), idempotencyKey, AcceptVisitInviteRequest{ExpectedRevision: fixture.revision})
	if err != nil {
		return fixture, err
	}
	if err := requireRevisionIncrease(fixture.revision, accepted.Reservation.Revision); err != nil {
		return fixture, err
	}
	fixture.revision = accepted.Reservation.Revision
	ticketResponse, err := runtime.HTTP.IssueTicket(ctx, fixture.visitor.actor.AccessToken, TicketRequest{Channel: "TLS_TCP"})
	if err != nil {
		return fixture, err
	}
	admissionKey, err := NewIdentity("admission", 16)
	if err != nil {
		return fixture, err
	}
	admissionResponse, err := runtime.HTTP.IssueWorldAdmission(ctx, fixture.visitor.actor.AccessToken, admissionKey, WorldAdmissionRequest{Kind: "VISIT_WORLD", VisitSessionID: fixture.visitSessionID})
	if err != nil {
		return fixture, err
	}
	if admissionResponse.Purpose != "JOIN" || !sameEndpoint(ticketResponse.Endpoint, admissionResponse.Endpoint) {
		return fixture, errors.New("Visitor JOIN ticket/admission binding drifted")
	}
	ticket, admission, err := newCredentialPair(ticketResponse.Ticket, admissionResponse.Credential)
	ticketResponse.Ticket = ""
	admissionResponse.Credential = ""
	if err != nil {
		return fixture, err
	}
	fixture.visitorTCP, err = DialTCP(ctx, admissionResponse.Endpoint, ticket, admission, "JOIN", runtime.TLSConfig, runtime.Bootstrap.Limits.RealtimeFrameBytes)
	if err != nil {
		return fixture, err
	}
	if err := scenario.Track(fixture.visitorTCP); err != nil {
		return fixture, err
	}
	credential, err := fixture.visitorTCP.TakeAdmission()
	if err != nil {
		return fixture, err
	}
	joinCommand := visitv1.VisitJoinCommand_builder{AdmissionCredential: proto.String(credential), ExpectedRevision: proto.Uint64(fixture.revision)}.Build()
	joinMessage, err := fixture.visitorTCP.Command(ctx, 2109, joinCommand)
	if err != nil {
		return fixture, err
	}
	joined, ok := joinMessage.Payload.(*visitv1.VisitJoinResponse)
	if !ok || joined.GetResult() == nil || joined.GetResult().GetSnapshot() == nil || joined.GetResult().GetSnapshot().GetVisitSessionId() != fixture.visitSessionID {
		return fixture, errors.New("Visitor JOIN response is incomplete")
	}
	if err := requireRevisionIncrease(fixture.revision, joined.GetResult().GetSnapshot().GetRevision()); err != nil {
		return fixture, err
	}
	fixture.revision = joined.GetResult().GetSnapshot().GetRevision()
	return fixture, nil
}

// expectVisitorSafeReturn 验证 safe-return 精确目标与不可由客户端改写的公开原因。
func expectVisitorSafeReturn(ctx context.Context, fixture *joinedVisitFixture, reason visitv1.SafeReturnReason) error {
	return expectSafeReturnFor(ctx, fixture.visitorTCP, fixture.visitSessionID, fixture.visitor.actor.PlayerID, reason)
}

// expectSafeReturnFor 验证指定 gameplay connection 收到精确目标和原因。
func expectSafeReturnFor(ctx context.Context, client *TCPClient, visitSessionID, visitorID string, reason visitv1.SafeReturnReason) error {
	pushContext, cancelPush := scenarioDeadline(ctx, 5*time.Second)
	defer cancelPush()
	message, err := waitMessage(pushContext, client.Pushes(), 2122)
	if err != nil {
		return err
	}
	push, ok := message.Payload.(*visitv1.VisitSafeReturnPush)
	if !ok || push.GetDirective() == nil || push.GetDirective().GetVisitSessionId() != visitSessionID || push.GetDirective().GetVisitorId() != visitorID || push.GetDirective().GetReason() != reason {
		return errors.New("Visitor safe-return target or reason drifted")
	}
	return nil
}

// newWorldSnapshotRequest 避免 VisitSession 场景导入任何服务端 world helper。
func newWorldSnapshotRequest() *worldv1.WorldSnapshotRequest {
	return worldv1.WorldSnapshotRequest_builder{}.Build()
}
