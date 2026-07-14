package visitsession

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// TestIdentifierNamespacesAndRedaction 保护四类 identity 不可混用且默认格式化不泄漏原值。
func TestIdentifierNamespacesAndRedaction(t *testing.T) {
	t.Parallel()
	constructors := []struct {
		name   string
		prefix string
		build  func(string) (any, error)
	}{
		{name: "visit session", prefix: visitSessionIDPrefix, build: func(value string) (any, error) { return NewVisitSessionID(value) }},
		{name: "invite", prefix: inviteIDPrefix, build: func(value string) (any, error) { return NewInviteID(value) }},
		{name: "command", prefix: commandIDPrefix, build: func(value string) (any, error) { return NewCommandID(value) }},
		{name: "binding", prefix: connectionBindingIDPrefix, build: func(value string) (any, error) { return NewConnectionBindingID(value) }},
	}
	for _, test := range constructors {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw := test.prefix + "Sensitive123"
			value, err := test.build(raw)
			if err != nil {
				t.Fatalf("construct identity: %v", err)
			}
			for _, formatted := range []string{fmt.Sprint(value), fmt.Sprintf("%#v", value)} {
				if strings.Contains(formatted, raw) || formatted != identityPlaceholder {
					t.Fatalf("unsafe default formatting: %q", formatted)
				}
			}
			for _, other := range constructors {
				if other.prefix == test.prefix {
					continue
				}
				if _, err := other.build(raw); err == nil {
					t.Fatalf("%s accepted %s namespace", other.name, test.name)
				}
			}
		})
	}
}

// TestPolicyAndDeadlineBoundaries 固定 capacity、duration 与等于即失效边界。
func TestPolicyAndDeadlineBoundaries(t *testing.T) {
	t.Parallel()
	validCapacity, _ := NewCapacity(32)
	if _, err := NewPolicy(validCapacity, 24*time.Hour, time.Hour, 2*time.Minute, 5*time.Minute, 2*time.Minute); err != nil {
		t.Fatalf("maximum policy should be valid: %v", err)
	}
	for _, value := range []uint8{0, 33, 255} {
		if _, err := NewCapacity(value); err == nil {
			t.Fatalf("capacity %d should fail", value)
		}
	}
	if _, err := NewPolicy(validCapacity, time.Minute-time.Microsecond, time.Second, time.Second, time.Second, time.Second); err == nil {
		t.Fatal("short session lifetime should fail")
	}
	deadline := time.Date(2026, 7, 14, 1, 2, 3, 456789999, time.FixedZone("offset", 8*60*60))
	canonical := canonicalTime(deadline)
	if canonical.Location() != time.UTC || canonical.Nanosecond()%int(time.Microsecond) != 0 {
		t.Fatalf("time was not canonicalized: %v", canonical)
	}
	if !expiredAt(canonical, canonical) {
		t.Fatal("deadline equality must be expired")
	}
}

// TestSnapshotHydrationSortsAndCopies 证明规范投影稳定排序且不会暴露内部 slice。
func TestSnapshotHydrationSortsAndCopies(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 2)
	visitorA := mustPlayerID(t, "ply_visitorA")
	visitorB := mustPlayerID(t, "ply_visitorB")
	inviteA := mustInviteID(t, "vinv_inviteA")
	inviteB := mustInviteID(t, "vinv_inviteB")
	invites := []InviteSnapshot{
		mustInvite(t, inviteB, visitorB, InviteStateAccepted, InitialRevision, fixture.createdAt.Add(time.Minute)),
		mustInvite(t, inviteA, visitorA, InviteStateAccepted, InitialRevision, fixture.createdAt.Add(time.Minute)),
	}
	members := []MembershipSnapshot{
		mustReserved(t, visitorB, inviteB, fixture.visitorB, fixture.createdAt.Add(30*time.Second)),
		mustReserved(t, visitorA, inviteA, fixture.visitorA, fixture.createdAt.Add(30*time.Second)),
	}
	snapshot, err := NewSnapshot(fixture.visitID, fixture.owner.playerID, fixture.worldID, fixture.assignment.Stamp(), LifecycleOpen, InitialRevision, fixture.capacity, fixture.createdAt, fixture.createdAt.Add(time.Hour), fixture.ownerBinding, 0, time.Time{}, invites, members)
	if err != nil {
		t.Fatalf("hydrate snapshot: %v", err)
	}
	if got := snapshot.Invites(); got[0].ID() != inviteA {
		t.Fatalf("invites are not sorted: %#v", got)
	}
	gotMembers := snapshot.Memberships()
	if gotMembers[0].VisitorID() != visitorA {
		t.Fatalf("memberships are not sorted: %#v", gotMembers)
	}
	gotMembers[0] = MembershipSnapshot{}
	if !snapshot.Memberships()[0].Valid() {
		t.Fatal("caller mutated snapshot membership")
	}
}

// TestSnapshotHydrationRejectsMalformedFacts 覆盖 revision、重复 identity、Owner-as-Visitor 与跨对象 deadline。
func TestSnapshotHydrationRejectsMalformedFacts(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 2)
	visitor := fixture.visitorA.playerID
	inviteID := mustInviteID(t, "vinv_badfacts")
	accepted := mustInvite(t, inviteID, visitor, InviteStateAccepted, InitialRevision, fixture.createdAt.Add(time.Minute))
	member := mustReserved(t, visitor, inviteID, fixture.visitorA, fixture.createdAt.Add(30*time.Second))
	ownerInviteID := mustInviteID(t, "vinv_ownerfacts")
	ownerInvite := mustInvite(t, ownerInviteID, fixture.owner.playerID, InviteStateAccepted, InitialRevision, fixture.createdAt.Add(time.Minute))
	ownerMember := mustReserved(t, fixture.owner.playerID, ownerInviteID, fixture.owner, fixture.createdAt.Add(30*time.Second))
	earlyReservation := mustReserved(t, visitor, inviteID, fixture.visitorA, fixture.createdAt.Add(-time.Second))
	lateReservation := mustReserved(t, visitor, inviteID, fixture.visitorA, fixture.createdAt.Add(2*time.Minute))
	tests := []struct {
		name     string
		revision Revision
		invites  []InviteSnapshot
		members  []MembershipSnapshot
	}{
		{name: "zero revision", revision: 0, invites: nil, members: nil},
		{name: "duplicate invite", revision: InitialRevision, invites: []InviteSnapshot{accepted, accepted}, members: []MembershipSnapshot{member}},
		{name: "duplicate member", revision: InitialRevision, invites: []InviteSnapshot{accepted}, members: []MembershipSnapshot{member, member}},
		{name: "owner as visitor", revision: InitialRevision, invites: []InviteSnapshot{ownerInvite}, members: []MembershipSnapshot{ownerMember}},
		{name: "reservation before creation", revision: InitialRevision, invites: []InviteSnapshot{accepted}, members: []MembershipSnapshot{earlyReservation}},
		{name: "reservation after invite", revision: InitialRevision, invites: []InviteSnapshot{accepted}, members: []MembershipSnapshot{lateReservation}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewSnapshot(fixture.visitID, fixture.owner.playerID, fixture.worldID, fixture.assignment.Stamp(), LifecycleOpen, test.revision, fixture.capacity, fixture.createdAt, fixture.createdAt.Add(time.Hour), fixture.ownerBinding, 0, time.Time{}, test.invites, test.members); err == nil {
				t.Fatal("malformed snapshot should fail")
			}
		})
	}
}

// TestSensitiveBusinessValuesRedactDefaultFormatting 保护 snapshot、record 与 application result 不展开身份或 assignment。
func TestSensitiveBusinessValuesRedactDefaultFormatting(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	visit, inviteID := invitedFixture(t, fixture, fixture.visitorA, "vinv_format")
	reserved, intent, err := visit.AcceptInvite(fixture.visitorA, inviteID, fixture.createdAt.Add(20*time.Second), fixture.assignment, fixture.createdAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	joined, member, err := reserved.Join(fixture.visitorA, JoinQualification{intent: intent}, mustBindingID(t, "vbind_format"), fixture.assignment, fixture.createdAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	directive, _ := NewSafeReturnDirective(joined.ID(), fixture.visitorA.playerID, SafeReturnReasonVoluntaryLeave)
	commandID := mustCommandID(t, "vcmd_format")
	fingerprint := fingerprintCommand(OperationJoin, "format")
	result, _ := NewMutationResult(OperationJoin, joined.Snapshot(), commandID, fingerprint, InviteSnapshot{}, AdmissionIntent{}, member, nil)
	record, _ := NewTransitionRecord(OperationJoin, joined.ID(), reserved.Revision(), commandID, fingerprint, result)
	createRecord, _ := NewCreateRecord(commandID, fingerprint, openFixture(t, fixture).Snapshot())
	values := []any{fixture.ownerBinding, visit.Snapshot().Invites()[0], member, visit, visit.Snapshot(), intent, JoinQualification{intent: intent}, directive, result, record, createRecord, OpenResult{snapshot: visit.Snapshot()}}
	for _, value := range values {
		for _, formatted := range []string{fmt.Sprint(value), fmt.Sprintf("%#v", value)} {
			for _, forbidden := range []string{"ply_", "ses_", "vses_", "vinv_", "vbind_", "winst_", "rnode_"} {
				if strings.Contains(formatted, forbidden) {
					t.Fatalf("%T leaked %q through %q", value, forbidden, formatted)
				}
			}
		}
	}
}

// FuzzVisitSessionIdentifiers 验证任意输入不会绕过 namespace、安全 ASCII 与长度边界。
func FuzzVisitSessionIdentifiers(f *testing.F) {
	for _, seed := range []string{"", "vses_a", "vinv_a", "vses_bad/value", "vses_中文", "vses_" + strings.Repeat("a", 123)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		id, err := NewVisitSessionID(value)
		if err != nil {
			return
		}
		if !id.Valid() || !strings.HasPrefix(id.Value(), visitSessionIDPrefix) || len(id.Value()) > maximumIdentifierBytes {
			t.Fatalf("accepted invalid identity %q", value)
		}
		for _, character := range id.Value()[len(visitSessionIDPrefix):] {
			if character > 127 {
				t.Fatalf("accepted non-ASCII identity %q", value)
			}
		}
	})
}

// aggregateFixture 提供纯 Go aggregate 测试共享的受信 owner、visitor 与 placement facts。
type aggregateFixture struct {
	// createdAt 是所有 deadline 与 placement lease 的 UTC 微秒基准。
	createdAt time.Time
	// visitID 是 aggregate 测试固定 identity。
	visitID VisitSessionID
	// worldID 是 assignment 与 VisitSession 共同绑定的 PersonalWorld。
	worldID personalworld.PersonalWorldID
	// assignment 是当前 active 且 lease 有效的受信 placement snapshot。
	assignment placement.AssignmentSnapshot
	// capacity 是当前测试选择的 Visitor 上限。
	capacity Capacity
	// policy 保存 fixture 共用的有界 duration。
	policy Policy
	// owner 是 immutable Owner 的受信认证投影。
	owner Actor
	// visitorA 是第一个独立 Visitor lineage。
	visitorA Actor
	// visitorB 是第二个独立 Visitor lineage。
	visitorB Actor
	// ownerBinding 是创建 aggregate 时的精确连接条件。
	ownerBinding AuthBinding
}

// newAggregateFixture 创建不依赖 listener、backend 或 session credential 的受控领域 fixture。
func newAggregateFixture(t *testing.T, capacityValue uint8) aggregateFixture {
	t.Helper()
	createdAt := time.Date(2026, 7, 14, 1, 0, 0, 123456789, time.UTC)
	worldID, _ := personalworld.NewPersonalWorldID("pworld_fixture")
	instanceID, _ := placement.NewWorldInstanceID("winst_fixture")
	nodeID, _ := placement.NewRuntimeNodeID("rnode_fixture")
	generation, _ := placement.NewAssignmentGeneration(1)
	fence, _ := placement.NewFencingToken(1)
	stamp, _ := placement.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	assignment, err := placement.NewAssignmentSnapshot(stamp, placement.PhaseActive, createdAt.Add(-time.Minute), createdAt.Add(2*time.Hour), createdAt)
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	capacity, _ := NewCapacity(capacityValue)
	policy, _ := NewPolicy(capacity, time.Hour, time.Minute, 30*time.Second, 30*time.Second, 30*time.Second)
	owner := testActor(t, "ply_owner", "ses_owner", 1)
	visitorA := testActor(t, "ply_visitorA", "ses_visitorA", 1)
	visitorB := testActor(t, "ply_visitorB", "ses_visitorB", 1)
	ownerBinding, _ := NewAuthBinding(owner, mustBindingID(t, "vbind_owner"))
	visitID, _ := NewVisitSessionID("vses_fixture")
	return aggregateFixture{createdAt: canonicalTime(createdAt), visitID: visitID, worldID: worldID, assignment: assignment, capacity: capacity, policy: policy, owner: owner, visitorA: visitorA, visitorB: visitorB, ownerBinding: ownerBinding}
}

// testActor 构造与 session owner 类型一致但仅限本 package 测试使用的受信 actor。
func testActor(t *testing.T, player, sessionValue string, epoch session.Epoch) Actor {
	t.Helper()
	playerID, err := account.NewPlayerID(player)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := session.NewSessionID(sessionValue)
	if err != nil {
		t.Fatal(err)
	}
	return Actor{playerID: playerID, sessionID: sessionID, epoch: epoch}
}

// mustPlayerID 构造测试 PlayerID。
func mustPlayerID(t *testing.T, value string) account.PlayerID {
	t.Helper()
	id, err := account.NewPlayerID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// mustInviteID 构造测试 InviteID。
func mustInviteID(t *testing.T, value string) InviteID {
	t.Helper()
	id, err := NewInviteID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// mustBindingID 构造测试 ConnectionBindingID。
func mustBindingID(t *testing.T, value string) ConnectionBindingID {
	t.Helper()
	id, err := NewConnectionBindingID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// mustInvite 构造测试 invite projection。
func mustInvite(t *testing.T, id InviteID, target account.PlayerID, state InviteState, revision Revision, deadline time.Time) InviteSnapshot {
	t.Helper()
	invite, err := NewInviteSnapshot(id, target, state, revision, deadline)
	if err != nil {
		t.Fatal(err)
	}
	return invite
}

// mustReserved 构造测试 reserved membership projection。
func mustReserved(t *testing.T, visitor account.PlayerID, invite InviteID, actor Actor, deadline time.Time) MembershipSnapshot {
	t.Helper()
	member, err := NewMembershipSnapshot(visitor, invite, MembershipStateReserved, actor.sessionID, actor.epoch, ConnectionBindingID{}, deadline, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return member
}
