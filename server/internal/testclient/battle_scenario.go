package testclient

import (
	"context"
	"errors"
	"fmt"

	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
)

const (
	// maximumVisitMembership 是当前 VisitSession compatibility 上限。
	maximumVisitMembership = 33
	// maximumQualifiedBattleActors 是 B0.3/B0.5 已资格的 actor hard cap。
	maximumQualifiedBattleActors = 8
	// overflowBattleActorCount 是必须稳定拒绝的首个 actor 数。
	overflowBattleActorCount = maximumQualifiedBattleActors + 1
	// battleCapacityErrorCode 是第九个 BattleTicket 的冻结公开错误码。
	battleCapacityErrorCode = 3001
	// ownerBattleSlot 是 workload 中唯一 Owner 的一基 slot。
	ownerBattleSlot = 1
	// visitSnapshotRequestMessageID 是公开 registry 的 VisitSession snapshot request。
	visitSnapshotRequestMessageID = 2119
)

// BattleAdmissionPlan 定义通过公开网络建立的 battle membership 与 ticket 集合。
type BattleAdmissionPlan struct {
	// MembershipCount 是必须先完成 VisitSession JOIN 的总人数；一人时只建 own world。
	MembershipCount int
	// BattleActorCount 是要申请 BattleTicket 的前 N 人，可取 1..9。
	BattleActorCount int
	// ResponseLossSlot 是使用同 key 重放首次响应的一基 actor slot；零表示不注入。
	ResponseLossSlot int
}

// BattleParticipant 是不暴露 PlayerID、session 或 endpoint 的 run-local actor 投影。
type BattleParticipant struct {
	// Slot 是本次 workload 内从 1 开始的稳定 actor slot。
	Slot int
	// Label 是随机低敏 actor correlation。
	Label string
	// Role 是 OWNER 或 VISITOR。
	Role string
	// Ticket 拥有该 actor 尚未转移的 BattleTicket credential。
	Ticket *BattleTicket
}

// BattleAdmission 是全部公开 admission 资源的唯一 cleanup owner。
type BattleAdmission struct {
	// Participants 只包含成功取得 BattleTicket 的最多八个 actor。
	Participants []BattleParticipant
	// MembershipCount 是 ticket admission 后仍存在的 VisitSession 人数。
	MembershipCount int
	// VisitRevision 是兼容性断言后的权威 revision；solo workload 为零。
	VisitRevision uint64
	// CapacityRejected 表示第九个 actor 收到冻结 capacity error。
	CapacityRejected bool
	// scenario 拥有账号 credential、WSS/TCP 与未转移 BattleTicket。
	scenario *ScenarioContext
}

// Close 取消全部公开 operation，并按逆序关闭 ticket、TCP、WSS 与 credential。
func (admission *BattleAdmission) Close() error {
	if admission == nil || admission.scenario == nil {
		return nil
	}
	err := admission.scenario.Close()
	admission.scenario = nil
	return err
}

// PrepareBattleAdmission 只经 HTTPS/WSS/TLS_TCP 建立 actor membership 并签发 BattleTicket。
//
// 当 BattleActorCount 为九时，前八个 ticket 必须成功，第九个必须被 capacity gate
// 拒绝；拒绝后会重新读取 VisitSession snapshot，证明 membership/revision 未被修改。
func PrepareBattleAdmission(ctx context.Context, runtime *ScenarioRuntime, plan BattleAdmissionPlan) (*BattleAdmission, error) {
	if err := plan.validate(); err != nil {
		return nil, err
	}
	if runtime == nil || runtime.HTTP == nil {
		return nil, errors.New("battle admission runtime is incomplete")
	}
	if plan.MembershipCount == 1 {
		return prepareSoloBattleAdmission(ctx, runtime, plan)
	}
	return prepareVisitBattleAdmission(ctx, runtime, plan)
}

// validate 拒绝隐式 workload、超过 compatibility 上限与无目标 response-loss。
func (plan BattleAdmissionPlan) validate() error {
	if plan.MembershipCount < 1 || plan.MembershipCount > maximumVisitMembership ||
		plan.BattleActorCount < 1 || plan.BattleActorCount > overflowBattleActorCount ||
		plan.BattleActorCount > plan.MembershipCount ||
		plan.ResponseLossSlot < 0 ||
		plan.ResponseLossSlot > maximumQualifiedBattleActors ||
		plan.ResponseLossSlot > plan.BattleActorCount {
		return errors.New("battle admission plan is invalid")
	}
	return nil
}

// prepareSoloBattleAdmission 建立独立 register/login/bootstrap/own-world BattleTicket 路径。
func prepareSoloBattleAdmission(ctx context.Context, runtime *ScenarioRuntime, plan BattleAdmissionPlan) (_ *BattleAdmission, resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, scenario.Close())
		}
	}()
	account, err := registerAndLoginScenarioActor(scenario, runtime)
	if err != nil {
		return nil, err
	}
	bootstrap, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil {
		return nil, err
	}
	account.actor.PlayerID = bootstrap.World.OwnerPlayerID
	ticket, err := issueBattleTicketForSlot(
		ctx, runtime.HTTP, account, ownerBattleSlot, plan.ResponseLossSlot,
		BattleTicketRequest{Kind: "OWN_WORLD"},
	)
	if err != nil {
		return nil, err
	}
	if err := scenario.Track(ticket); err != nil {
		_ = ticket.Close()
		return nil, err
	}
	return &BattleAdmission{
		Participants: []BattleParticipant{{
			Slot: ownerBattleSlot, Label: account.actor.Label, Role: "OWNER", Ticket: ticket,
		}},
		MembershipCount: 1,
		scenario:        scenario,
	}, nil
}

// prepareVisitBattleAdmission 建立完整 invite/accept/JOIN membership 后按 slot 签发 ticket。
func prepareVisitBattleAdmission(ctx context.Context, runtime *ScenarioRuntime, plan BattleAdmissionPlan) (_ *BattleAdmission, resultErr error) {
	fixture, err := setupJoinedVisitWithRegistrar(ctx, runtime, registerAndLoginScenarioActor)
	if fixture == nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, fixture.scenario.Close())
		}
	}()
	if err != nil {
		return nil, err
	}
	accounts := make([]accountCredential, 0, plan.MembershipCount)
	accounts = append(accounts, fixture.owner, fixture.visitor)
	for len(accounts) < plan.MembershipCount {
		additional, joinErr := fixture.joinAdditionalVisitor(ctx)
		if joinErr != nil {
			return nil, joinErr
		}
		accounts = append(accounts, additional.account)
	}
	admission := &BattleAdmission{
		Participants:    make([]BattleParticipant, 0, min(plan.BattleActorCount, maximumQualifiedBattleActors)),
		MembershipCount: plan.MembershipCount,
		VisitRevision:   fixture.revision,
		scenario:        fixture.scenario,
	}
	for index := 0; index < plan.BattleActorCount; index++ {
		slot := index + 1
		request := BattleTicketRequest{Kind: "VISIT_WORLD", VisitSessionID: fixture.visitSessionID}
		if slot == ownerBattleSlot {
			request = BattleTicketRequest{Kind: "OWN_WORLD"}
		}
		ticket, issueErr := issueBattleTicketForSlot(
			ctx, runtime.HTTP, accounts[index], slot, plan.ResponseLossSlot, request,
		)
		if slot == overflowBattleActorCount {
			var publicError PublicError
			if !errors.As(issueErr, &publicError) ||
				publicError.Code != battleCapacityErrorCode {
				return nil, errors.New("ninth battle actor was not rejected by capacity gate")
			}
			admission.CapacityRejected = true
			break
		}
		if issueErr != nil {
			return nil, issueErr
		}
		if err := fixture.scenario.Track(ticket); err != nil {
			_ = ticket.Close()
			return nil, err
		}
		admission.Participants = append(admission.Participants, BattleParticipant{
			Slot: slot, Label: accounts[index].actor.Label,
			Role: ticket.Role, Ticket: ticket,
		})
	}
	if plan.BattleActorCount == overflowBattleActorCount {
		if err := verifyVisitMembershipPreserved(ctx, fixture, plan.MembershipCount, admission.VisitRevision); err != nil {
			return nil, err
		}
	}
	return admission, nil
}

// issueBattleTicketForSlot 使用独立随机 key，且只在指定 slot 丢弃并重放首次响应。
func issueBattleTicketForSlot(
	ctx context.Context,
	client *HTTPClient,
	account accountCredential,
	slot int,
	responseLossSlot int,
	request BattleTicketRequest,
) (*BattleTicket, error) {
	key, err := NewIdentity("battle", 16)
	if err != nil {
		return nil, err
	}
	if slot == responseLossSlot {
		return client.IssueBattleTicketAfterResponseLoss(
			ctx, account.actor.AccessToken, key, request,
		)
	}
	return client.IssueBattleTicket(ctx, account.actor.AccessToken, key, request)
}

// verifyVisitMembershipPreserved 证明 battle capacity failure 未改写 VisitSession。
func verifyVisitMembershipPreserved(
	ctx context.Context,
	fixture *joinedVisitFixture,
	expectedMembership int,
	expectedRevision uint64,
) error {
	message, err := fixture.ownerTCP.Request(
		ctx, visitSnapshotRequestMessageID,
		visitv1.VisitSnapshotRequest_builder{}.Build(),
	)
	if err != nil {
		return err
	}
	response, ok := message.Payload.(*visitv1.VisitSnapshotResponse)
	if !ok || response.GetSnapshot() == nil {
		return errors.New("battle capacity compatibility snapshot is incomplete")
	}
	snapshot := response.GetSnapshot()
	actualMembership := len(snapshot.GetVisitors()) + 1
	if actualMembership != expectedMembership ||
		snapshot.GetRevision() != expectedRevision {
		return fmt.Errorf(
			"battle capacity changed VisitSession: membership=%d revision=%d",
			actualMembership, snapshot.GetRevision(),
		)
	}
	return nil
}
