package visitsession

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	domain "github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// testFixture 提供每个测试独占identity的最小有效VisitSession聚合。
type testFixture struct {
	// now是全部fixture deadline的UTC微秒时间锚点。
	now time.Time
	// snapshot是尚无Visitor的open基线。
	snapshot domain.Snapshot
}

// newTestFixture 构造带完整owner认证绑定与placement stamp的领域基线。
func newTestFixture(t testing.TB, suffix string) testFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	visitID, _ := domain.NewVisitSessionID("vses_" + suffix)
	ownerID, _ := account.NewPlayerID("ply_owner" + suffix)
	worldID, _ := personalworld.NewPersonalWorldID("pworld_" + suffix)
	instanceID, _ := placement.NewWorldInstanceID("winst_" + suffix)
	nodeID, _ := placement.NewRuntimeNodeID("rnode_" + suffix)
	generation, _ := placement.NewAssignmentGeneration(1)
	fence, _ := placement.NewFencingToken(1)
	assignment, err := placement.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, _ := session.NewSessionID("ses_owner" + suffix)
	bindingID, _ := domain.NewConnectionBindingID("vbind_owner" + suffix)
	binding, err := domain.HydrateAuthBinding(ownerID, sessionID, session.InitialEpoch, bindingID)
	if err != nil {
		t.Fatal(err)
	}
	capacity, _ := domain.NewCapacity(2)
	snapshot, err := domain.NewSnapshot(visitID, ownerID, worldID, assignment, domain.LifecycleOpen, domain.InitialRevision, capacity, now, now.Add(time.Hour), binding, 0, time.Time{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return testFixture{now: now, snapshot: snapshot}
}

// testFingerprint 从稳定语义文本生成不含凭据的测试指纹。
func testFingerprint(t testing.TB, value string) domain.CommandFingerprint {
	t.Helper()
	digest := sha256.Sum256([]byte(value))
	fingerprint, err := domain.NewCommandFingerprint(digest)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

// testCreateRecord 构造Open首次命令记录。
func testCreateRecord(t testing.TB, snapshot domain.Snapshot, suffix string) domain.CreateRecord {
	t.Helper()
	commandID, _ := domain.NewCommandID("vcmd_open" + suffix)
	record, err := domain.NewCreateRecord(commandID, testFingerprint(t, "open:"+suffix), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// testCloseRecord 构造删除active index但保留terminal replay的关闭记录。
func testCloseRecord(t testing.TB, snapshot domain.Snapshot, suffix string) domain.TransitionRecord {
	t.Helper()
	revision, _ := domain.NewRevision(snapshot.Revision().Uint64() + 1)
	target, err := domain.NewSnapshot(snapshot.ID(), snapshot.OwnerID(), snapshot.WorldID(), snapshot.Assignment(), domain.LifecycleClosed, revision, snapshot.Capacity(), snapshot.CreatedAt(), snapshot.ExpiresAt(), snapshot.OwnerBinding(), 0, time.Time{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	commandID, _ := domain.NewCommandID("vcmd_close" + suffix)
	fingerprint := testFingerprint(t, "close:"+suffix)
	result, err := domain.NewMutationResult(domain.OperationClose, target, commandID, fingerprint, domain.InviteSnapshot{}, domain.AdmissionIntent{}, domain.MembershipSnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := domain.NewTransitionRecord(domain.OperationClose, snapshot.ID(), snapshot.Revision(), commandID, fingerprint, result)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// testAcceptRecord 构造占用一个Visitor容量的邀请接受记录。
func testAcceptRecord(t testing.TB, snapshot domain.Snapshot, suffix string) domain.TransitionRecord {
	t.Helper()
	visitor, _ := account.NewPlayerID("ply_visitor" + suffix)
	inviteID, _ := domain.NewInviteID("vinv_" + suffix)
	targetRevision, _ := domain.NewRevision(snapshot.Revision().Uint64() + 1)
	invite, _ := domain.NewInviteSnapshot(inviteID, visitor, domain.InviteStateAccepted, targetRevision, snapshot.CreatedAt().Add(30*time.Minute))
	visitorSession, _ := session.NewSessionID("ses_visitor" + suffix)
	reservation := snapshot.CreatedAt().Add(10 * time.Minute)
	membership, _ := domain.NewMembershipSnapshot(visitor, inviteID, domain.MembershipStateReserved, visitorSession, session.InitialEpoch, domain.ConnectionBindingID{}, reservation, 0, time.Time{})
	target, err := domain.NewSnapshot(snapshot.ID(), snapshot.OwnerID(), snapshot.WorldID(), snapshot.Assignment(), domain.LifecycleOpen, targetRevision, snapshot.Capacity(), snapshot.CreatedAt(), snapshot.ExpiresAt(), snapshot.OwnerBinding(), 0, time.Time{}, []domain.InviteSnapshot{invite}, []domain.MembershipSnapshot{membership})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := domain.HydrateAdmissionIntent(snapshot.ID(), visitor, visitorSession, session.InitialEpoch, snapshot.Assignment(), reservation)
	if err != nil {
		t.Fatal(err)
	}
	commandID, _ := domain.NewCommandID("vcmd_accept" + suffix)
	fingerprint := testFingerprint(t, "accept:"+suffix)
	result, err := domain.NewMutationResult(domain.OperationAcceptInvite, target, commandID, fingerprint, domain.InviteSnapshot{}, intent, domain.MembershipSnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := domain.NewTransitionRecord(domain.OperationAcceptInvite, snapshot.ID(), snapshot.Revision(), commandID, fingerprint, result)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
