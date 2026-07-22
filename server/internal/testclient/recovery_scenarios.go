package testclient

import (
	"context"
	"errors"
	"time"

	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"google.golang.org/protobuf/proto"
)

// runAuthorizationNegativeMatrix 验证 Visitor 不能执行 Owner command，低 revision 拒绝且连接仍可读取。
func runAuthorizationNegativeMatrix(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	fixture, err := setupJoinedVisit(ctx, runtime)
	if fixture != nil {
		defer closeScenario(fixture.scenario, &resultErr)
	}
	if err != nil {
		return err
	}
	unauthorized := visitv1.VisitKickCommand_builder{
		TargetVisitorId: proto.String(fixture.owner.actor.PlayerID), ExpectedRevision: proto.Uint64(fixture.revision),
	}.Build()
	_, err = fixture.visitorTCP.Command(ctx, 2113, unauthorized)
	var publicError RealtimePublicError
	if !errors.As(err, &publicError) {
		return errors.New("Visitor Owner-command attempt did not return a correlated public error")
	}
	stale := visitv1.VisitCreateInviteCommand_builder{
		TargetVisitorId: proto.String(fixture.visitor.actor.PlayerID), InviteLifetimeMs: proto.Uint32(20_000), ExpectedRevision: proto.Uint64(fixture.revision - 1),
	}.Build()
	_, err = fixture.ownerTCP.Command(ctx, 2105, stale)
	if !errors.As(err, &publicError) {
		return errors.New("stale VisitSession revision did not return a correlated public error")
	}
	if _, err := fixture.visitorTCP.Request(ctx, 2119, visitv1.VisitSnapshotRequest_builder{}.Build()); err != nil {
		return errors.New("negative command expanded into connection failure")
	}
	return nil
}

// runCredentialRejectionMatrix 验证 ticket/admission replay 与 wrong purpose 都 fail closed。
func runCredentialRejectionMatrix(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	if _, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken); err != nil {
		return err
	}
	wssTicket, err := runtime.HTTP.IssueTicket(ctx, account.actor.AccessToken, TicketRequest{Channel: "WSS"})
	if err != nil {
		return err
	}
	firstTicket, err := NewSecret(wssTicket.Ticket)
	if err != nil {
		return err
	}
	replayedTicket, err := NewSecret(wssTicket.Ticket)
	wssTicket.Ticket = ""
	if err != nil {
		firstTicket.Clear()
		return err
	}
	first, err := DialWSS(ctx, wssTicket.Endpoint, firstTicket, runtime.TLSConfig, runtime.Bootstrap.Limits.RealtimeFrameBytes)
	if err != nil {
		replayedTicket.Clear()
		return err
	}
	if err := scenario.Track(first); err != nil {
		return err
	}
	if replayed, replayErr := DialWSS(ctx, wssTicket.Endpoint, replayedTicket, runtime.TLSConfig, runtime.Bootstrap.Limits.RealtimeFrameBytes); replayErr == nil {
		_ = replayed.Close()
		return errors.New("consumed WSS ticket was replayed")
	}

	admissionKey, err := NewIdentity("admission", 16)
	if err != nil {
		return err
	}
	admissionResponse, err := runtime.HTTP.IssueWorldAdmission(ctx, account.actor.AccessToken, admissionKey, WorldAdmissionRequest{Kind: "OWN_WORLD"})
	if err != nil {
		return err
	}
	tcpTicket, err := runtime.HTTP.IssueTicket(ctx, account.actor.AccessToken, TicketRequest{Channel: "TLS_TCP"})
	if err != nil {
		return err
	}
	ticketSecret, admissionSecret, err := newCredentialPair(tcpTicket.Ticket, admissionResponse.Credential)
	tcpTicket.Ticket = ""
	admissionResponse.Credential = ""
	if err != nil {
		return err
	}
	wrongPurpose, err := DialTCP(ctx, admissionResponse.Endpoint, ticketSecret, admissionSecret, "JOIN", runtime.TLSConfig, runtime.Bootstrap.Limits.RealtimeFrameBytes)
	if err == nil {
		probeContext, cancelProbe := scenarioDeadline(ctx, 2*time.Second)
		_, probeErr := wrongPurpose.Request(probeContext, 2000, new(worldv1.WorldSnapshotRequest))
		cancelProbe()
		_ = wrongPurpose.Close()
		if probeErr == nil {
			return errors.New("OWN_WORLD admission was accepted with JOIN purpose")
		}
	}
	return nil
}

// runVisitDisconnectReconnect 验证 Visitor unexpected close、公开 revision 收敛与显式 RECONNECT。
func runVisitDisconnectReconnect(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	fixture, err := setupJoinedVisit(ctx, runtime)
	if fixture != nil {
		defer closeScenario(fixture.scenario, &resultErr)
	}
	if err != nil {
		return err
	}
	if err := fixture.visitorTCP.Close(); err != nil {
		return err
	}
	_, err = pollVisitSnapshot(ctx, fixture.ownerTCP, func(snapshot *visitv1.VisitSessionSnapshot) bool {
		return membershipState(snapshot, fixture.visitor.actor.PlayerID) == visitv1.VisitMembershipState_VISIT_MEMBERSHIP_STATE_RECONNECTING
	})
	if err != nil {
		return err
	}
	ticketResponse, err := runtime.HTTP.IssueTicket(ctx, fixture.visitor.actor.AccessToken, TicketRequest{Channel: "TLS_TCP"})
	if err != nil {
		return err
	}
	admissionKey, err := NewIdentity("admission", 16)
	if err != nil {
		return err
	}
	admissionResponse, err := runtime.HTTP.IssueWorldAdmission(ctx, fixture.visitor.actor.AccessToken, admissionKey, WorldAdmissionRequest{Kind: "VISIT_WORLD", VisitSessionID: fixture.visitSessionID})
	if err != nil || admissionResponse.Purpose != "RECONNECT" {
		return errors.New("reconnecting Visitor did not receive RECONNECT admission")
	}
	ticket, admission, err := newCredentialPair(ticketResponse.Ticket, admissionResponse.Credential)
	ticketResponse.Ticket = ""
	admissionResponse.Credential = ""
	if err != nil {
		return err
	}
	reconnected, err := DialTCP(ctx, admissionResponse.Endpoint, ticket, admission, "RECONNECT", runtime.TLSConfig, runtime.Bootstrap.Limits.RealtimeFrameBytes)
	if err != nil {
		return err
	}
	if err := fixture.scenario.Track(reconnected); err != nil {
		return err
	}
	credential, err := reconnected.TakeAdmission()
	if err != nil {
		return err
	}
	command := visitv1.VisitReconnectCommand_builder{AdmissionCredential: proto.String(credential), ExpectedRevision: proto.Uint64(admissionResponse.VisitRevision)}.Build()
	message, err := reconnected.Command(ctx, 2115, command)
	if err != nil {
		return err
	}
	response, ok := message.Payload.(*visitv1.VisitReconnectResponse)
	if !ok || response.GetResult() == nil || response.GetResult().GetSnapshot() == nil || membershipState(response.GetResult().GetSnapshot(), fixture.visitor.actor.PlayerID) != visitv1.VisitMembershipState_VISIT_MEMBERSHIP_STATE_JOINED {
		return errors.New("Visitor RECONNECT did not restore joined membership")
	}
	if err := fixture.ownerTCP.Close(); err != nil {
		return err
	}
	recoveredOwner, err := dialOwnWorldTCP(ctx, runtime, fixture.owner.actor)
	if err != nil {
		return err
	}
	if err := fixture.scenario.Track(recoveredOwner); err != nil {
		return err
	}
	ownerSnapshot, err := recoveredOwner.Request(ctx, 2119, visitv1.VisitSnapshotRequest_builder{}.Build())
	if err != nil {
		return err
	}
	ownerResponse, ok := ownerSnapshot.Payload.(*visitv1.VisitSnapshotResponse)
	if !ok || ownerResponse.GetSnapshot() == nil || ownerResponse.GetSnapshot().GetLifecycle() != visitv1.VisitLifecycle_VISIT_LIFECYCLE_OPEN {
		return errors.New("Owner grace recovery did not restore OPEN VisitSession")
	}
	return nil
}

// runVisitGraceExpiry 验证 Visitor grace 等于即失效并从 active membership 移除。
func runVisitGraceExpiry(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	fixture, err := setupJoinedVisit(ctx, runtime)
	if fixture != nil {
		defer closeScenario(fixture.scenario, &resultErr)
	}
	if err != nil {
		return err
	}
	if err := fixture.visitorTCP.Close(); err != nil {
		return err
	}
	_, err = pollVisitSnapshot(ctx, fixture.ownerTCP, func(snapshot *visitv1.VisitSessionSnapshot) bool {
		return membershipState(snapshot, fixture.visitor.actor.PlayerID) == visitv1.VisitMembershipState_VISIT_MEMBERSHIP_STATE_UNSPECIFIED
	})
	return err
}

// pollVisitSnapshot 有界查询公开 snapshot，避免固定 sleep 猜测异步 lifecycle 边界。
func pollVisitSnapshot(ctx context.Context, owner *TCPClient, predicate func(*visitv1.VisitSessionSnapshot) bool) (*visitv1.VisitSessionSnapshot, error) {
	// 250ms 足以观察 2s 资格 grace，又不会把 visit_read 的 per-connection 预算变成测试噪声。
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		message, err := owner.Request(ctx, 2119, visitv1.VisitSnapshotRequest_builder{}.Build())
		if err != nil {
			var publicError RealtimePublicError
			if !errors.As(err, &publicError) || !publicError.Retryable {
				return nil, err
			}
			select {
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
		}
		response, ok := message.Payload.(*visitv1.VisitSnapshotResponse)
		if !ok || response.GetSnapshot() == nil {
			return nil, errors.New("VisitSession snapshot poll returned incomplete payload")
		}
		if predicate(response.GetSnapshot()) {
			return response.GetSnapshot(), nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
}

// membershipState 返回目标 Visitor 的公开 membership 状态，不存在时返回 UNSPECIFIED。
func membershipState(snapshot *visitv1.VisitSessionSnapshot, visitorID string) visitv1.VisitMembershipState {
	if snapshot == nil {
		return visitv1.VisitMembershipState_VISIT_MEMBERSHIP_STATE_UNSPECIFIED
	}
	for _, visitor := range snapshot.GetVisitors() {
		if visitor.GetPlayerId() == visitorID {
			return visitor.GetState()
		}
	}
	return visitv1.VisitMembershipState_VISIT_MEMBERSHIP_STATE_UNSPECIFIED
}
