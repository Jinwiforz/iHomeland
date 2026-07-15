package worldadmission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// semanticManifest 映射共享 admission corpus 的运行时开关与稳定场景集合。
type semanticManifest struct {
	// RuntimeImplemented 声明 production runtime 已经覆盖 corpus。
	RuntimeImplemented bool `json:"runtimeImplemented"`
	// Cases 保持 generator 定义的稳定顺序。
	Cases []semanticCase `json:"cases"`
}

// semanticCase 保存不泄漏 credential 布局的单条跨 owner 验收语义。
type semanticCase struct {
	// Name 是子测试使用的稳定场景名。
	Name string `json:"name"`
	// MembershipState 标识VisitSession二次校验状态。
	MembershipState string `json:"membershipState"`
	// Purpose 是JOIN或RECONNECT。
	Purpose string `json:"purpose"`
	// Condition 选择runtime拒绝分支。
	Condition string `json:"condition"`
	// ExpectedOutcome 是ACCEPT或REJECT。
	ExpectedOutcome string `json:"expectedOutcome"`
	// ExpectedErrorCode 引用共享stable error registry。
	ExpectedErrorCode uint32 `json:"expectedErrorCode"`
}

// TestProductionRuntimeCoversSemanticCorpus 将版本化 corpus 逐项映射到 production issuer/verifier 决议。
func TestProductionRuntimeCoversSemanticCorpus(t *testing.T) {
	path := filepath.Join("..", "..", "..", "shared", "contracts", "fixtures", "admission", "semantic.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest semanticManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.RuntimeImplemented || len(manifest.Cases) == 0 {
		t.Fatal("semantic runtime manifest is not active")
	}
	for _, testCase := range manifest.Cases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) { executeSemanticCase(t, testCase) })
	}
}

// executeSemanticCase 使用真实 Service 与 VisitSession aggregate 组合执行一条 corpus 条件。
func executeSemanticCase(t *testing.T, testCase semanticCase) {
	t.Helper()
	fixture := newAdmissionFixture(t)
	purpose := PurposeJoin
	if testCase.Purpose == "RECONNECT" {
		purpose = PurposeReconnect
	}
	binding := fixture.binding
	if purpose == PurposeReconnect {
		binding, _ = NewBinding(fixture.playerID, fixture.sessionID, session.InitialEpoch, RoleVisitor, fixture.worldID, fixture.visitID, PurposeReconnect, fixture.stamp, fixture.endpoint, fixture.now, fixture.binding.ExpiresAt())
	}
	issued, err := fixture.service.Issue(context.Background(), mustIssueID(t, "semantic_"+testCase.Name), binding)
	if err != nil {
		t.Fatal(err)
	}
	consumeID := mustConsumeID(t, "consume_"+testCase.Name)
	endpoint := fixture.endpoint
	verifyPurpose := purpose
	switch testCase.Condition {
	case "MATCHING_BINDING":
		// 使用完整匹配输入。
	case "PURPOSE_MISMATCH":
		if verifyPurpose == PurposeJoin {
			verifyPurpose = PurposeReconnect
		} else {
			verifyPurpose = PurposeJoin
		}
	case "EXPIRED":
		fixture.clock.now = binding.ExpiresAt()
	case "CREDENTIAL_REPLAYED":
		if _, err := fixture.service.Verify(context.Background(), issued.Credential(), consumeID, fixture.auth, endpoint, verifyPurpose); err != nil {
			t.Fatal(err)
		}
		consumeID = mustConsumeID(t, "consume_replayed_"+testCase.Name)
	case "STALE_SESSION_EPOCH":
		binding, _ = NewBinding(fixture.playerID, fixture.sessionID, session.Epoch(2), RoleVisitor, fixture.worldID, fixture.visitID, purpose, fixture.stamp, fixture.endpoint, fixture.now, fixture.binding.ExpiresAt())
		issued, err = fixture.service.Issue(context.Background(), mustIssueID(t, "semantic_epoch_"+testCase.Name), binding)
		if err != nil {
			t.Fatal(err)
		}
	case "STALE_ASSIGNMENT":
		fixture.placements.snapshot = successorAssignment(t, fixture)
	case "WRONG_ENDPOINT":
		endpoint, _ = session.NewEndpoint(session.ChannelTLSTCP, "wrong.example.invalid", 4433)
	case "WRONG_CHANNEL":
		wssEndpoint, _ := session.NewEndpoint(session.ChannelWSS, "control.example.invalid", 4434)
		wssAuth := newControlAuth(t, fixture.now, fixture.playerID, fixture.sessionID, wssEndpoint)
		qualification, verifyErr := fixture.service.Verify(context.Background(), issued.Credential(), consumeID, wssAuth, wssEndpoint, verifyPurpose)
		assertSemanticDecision(t, testCase, qualification.Valid(), verifyErr)
		return
	case "MEMBERSHIP_MISSING":
		// verifier 只恢复资格；缺失 membership 必须由 VisitSession owner 稳定拒绝。
		qualification, verifyErr := fixture.service.Verify(context.Background(), issued.Credential(), consumeID, fixture.auth, endpoint, verifyPurpose)
		if verifyErr != nil || !qualification.Valid() {
			t.Fatalf("verifier failed before VisitSession membership gate: %v", verifyErr)
		}
		assertVisitSessionDecision(t, fixture, testCase, qualification)
		return
	default:
		t.Fatalf("unmapped semantic condition %s", testCase.Condition)
	}
	qualification, verifyErr := fixture.service.Verify(context.Background(), issued.Credential(), consumeID, fixture.auth, endpoint, verifyPurpose)
	assertSemanticDecision(t, testCase, qualification.Valid(), verifyErr)
	if testCase.ExpectedOutcome == "ACCEPT" {
		assertVisitSessionDecision(t, fixture, testCase, qualification)
	}
}

// assertVisitSessionDecision 真实执行 Join 或 VisitorReconnect，并核对 membership owner 的稳定结果。
func assertVisitSessionDecision(t *testing.T, fixture *admissionFixture, testCase semanticCase, qualification Qualification) {
	t.Helper()
	visitQualification, err := qualification.VisitSessionQualification()
	if err != nil {
		t.Fatal(err)
	}
	visitor, err := visitsession.ActorFromAuth(fixture.auth)
	if err != nil {
		t.Fatal(err)
	}
	ownerID, _ := account.NewPlayerID("ply_admissionOwner")
	ownerSessionID, _ := session.NewSessionID("ses_admissionOwner")
	ownerConnectionID, _ := visitsession.NewConnectionBindingID("vbind_admissionOwner")
	ownerBinding, err := visitsession.HydrateAuthBinding(ownerID, ownerSessionID, session.InitialEpoch, ownerConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	capacity, _ := visitsession.NewCapacity(1)
	createdAt := fixture.now.Add(-time.Minute)
	expiresAt := fixture.placements.snapshot.Lease().ExpiresAt()
	var invites []visitsession.InviteSnapshot
	var memberships []visitsession.MembershipSnapshot
	if testCase.MembershipState != "NONE" {
		inviteID, _ := visitsession.NewInviteID("vinv_admissionSemantic")
		invite, inviteErr := visitsession.NewInviteSnapshot(inviteID, fixture.playerID, visitsession.InviteStateAccepted, visitsession.InitialRevision, qualification.Binding().ExpiresAt())
		if inviteErr != nil {
			t.Fatal(inviteErr)
		}
		invites = []visitsession.InviteSnapshot{invite}
		state := visitsession.MembershipStateReserved
		bindingID := visitsession.ConnectionBindingID{}
		reservationExpiresAt := qualification.Binding().ExpiresAt()
		var reconnectGeneration uint64
		var reconnectExpiresAt time.Time
		if testCase.MembershipState == "RECONNECTING" {
			state = visitsession.MembershipStateReconnecting
			bindingID, _ = visitsession.NewConnectionBindingID("vbind_admissionPrevious")
			reservationExpiresAt = time.Time{}
			reconnectGeneration = 1
			reconnectExpiresAt = qualification.Binding().ExpiresAt()
		}
		membership, membershipErr := visitsession.NewMembershipSnapshot(fixture.playerID, inviteID, state, fixture.sessionID, session.InitialEpoch, bindingID, reservationExpiresAt, reconnectGeneration, reconnectExpiresAt)
		if membershipErr != nil {
			t.Fatal(membershipErr)
		}
		memberships = []visitsession.MembershipSnapshot{membership}
	}
	snapshot, err := visitsession.NewSnapshot(fixture.visitID, ownerID, fixture.worldID, fixture.stamp, visitsession.LifecycleOpen, visitsession.InitialRevision, capacity, createdAt, expiresAt, ownerBinding, 0, time.Time{}, invites, memberships)
	if err != nil {
		t.Fatal(err)
	}
	visit, err := visitsession.HydrateVisitSession(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	newBindingID, _ := visitsession.NewConnectionBindingID("vbind_admissionCurrent")
	if testCase.Purpose == "RECONNECT" {
		_, _, err = visit.VisitorReconnect(visitor, visitQualification, newBindingID, fixture.placements.snapshot, fixture.now)
	} else {
		_, _, err = visit.Join(visitor, visitQualification, newBindingID, fixture.placements.snapshot, fixture.now)
	}
	if testCase.ExpectedOutcome == "ACCEPT" {
		if err != nil {
			t.Fatalf("VisitSession expected accept: %v", err)
		}
		return
	}
	if testCase.ExpectedErrorCode != 2107 || !visitsession.IsErrorCode(err, visitsession.ErrorCodeNotFound) {
		t.Fatalf("VisitSession error=%v expected public code=%d", err, testCase.ExpectedErrorCode)
	}
}

// assertSemanticDecision 对照共享 outcome/error code 检查 production 决议，避免 corpus 与实现静默漂移。
func assertSemanticDecision(t *testing.T, testCase semanticCase, accepted bool, err error) {
	t.Helper()
	if testCase.ExpectedOutcome == "ACCEPT" {
		if err != nil || !accepted {
			t.Fatalf("expected accept: accepted=%v err=%v", accepted, err)
		}
		return
	}
	if err == nil || accepted {
		t.Fatalf("expected reject: accepted=%v err=%v", accepted, err)
	}
	expected := map[uint32]ErrorCode{2002: ErrorCodeStaleAssignment, 2003: ErrorCodeInvalid, 2004: ErrorCodeExpired, 2005: ErrorCodeReplayed}[testCase.ExpectedErrorCode]
	if !IsErrorCode(err, expected) {
		t.Fatalf("error=%v expected code=%v", err, expected)
	}
}

// newControlAuth 通过真实 session ticket consume 构造 WSS AuthContext，用于证明错误 channel fail closed。
func newControlAuth(t *testing.T, now time.Time, playerID account.PlayerID, sessionID session.SessionID, endpoint session.Endpoint) session.AuthContext {
	t.Helper()
	principal, _ := session.NewPrincipal("acc_admissionFixture", playerID.String())
	scopes, _ := session.NewScopeSet(session.ScopeControl)
	store := sessionTestStore{snapshot: session.AuthSnapshot{Principal: principal, SessionID: sessionID, Epoch: session.InitialEpoch, Scopes: scopes}}
	policy, _ := session.NewPolicy(time.Second, time.Minute, time.Hour, time.Hour)
	service, err := session.NewService(store, sessionEndpointProvider{endpoint}, sessionInvalidator{}, &fixedClock{now}, sessionIDGenerator{}, sessionSecretGenerator{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	nonce, _ := session.ParseTicketNonce([]byte{2, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	auth, err := service.ConsumeTicket(context.Background(), nonce, session.ChannelWSS, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}
