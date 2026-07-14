package visitsession

import (
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	domain "github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// TestDefinitions 固定三类key的owner、TTL、schema和大小治理边界。
func TestDefinitions(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != 3 {
		t.Fatalf("definition count = %d", len(definitions))
	}
	registry, err := storageredis.NewRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("test", registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.Owner != "visitsession" || definition.TTLPolicy != storageredis.TTLRequired || definition.SchemaVersion != 1 || definition.MaxEncodedBytes <= 0 {
			t.Fatalf("invalid definition: %+v", definition)
		}
		key, buildErr := keyspace.Build(definition.Name, "vses_fixture")
		if buildErr != nil || strings.Contains(key.String(), "vses_fixture") || !strings.HasPrefix(key.Value(), "ih:test:visitsession:") {
			t.Fatalf("key boundary failed: key=%s err=%v", key, buildErr)
		}
	}
}

// TestSnapshotAndResultCodecRoundTrip 验证完整投影经canonical payload往返不丢失。
func TestSnapshotAndResultCodecRoundTrip(t *testing.T) {
	fixture := newTestFixture(t, "codec")
	payload, err := encodeSnapshot(fixture.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeSnapshot(payload)
	if err != nil || !restored.Equal(fixture.snapshot) {
		t.Fatalf("snapshot round trip failed: %v", err)
	}
	createRecord := testCreateRecord(t, fixture.snapshot, "codec")
	createResult, _ := domainCreateResult(createRecord)
	createPayload, err := encodeCreateResult(createResult)
	if err != nil {
		t.Fatal(err)
	}
	restoredCreate, err := decodeCreateResult(createPayload)
	if err != nil || !restoredCreate.Snapshot().Equal(fixture.snapshot) || restoredCreate.CommandID() != createRecord.CommandID() {
		t.Fatalf("create round trip failed: %v", err)
	}
	closeRecord := testCloseRecord(t, fixture.snapshot, "codec")
	mutationPayload, err := encodeMutationResult(closeRecord.Result())
	if err != nil {
		t.Fatal(err)
	}
	restoredMutation, err := decodeMutationResult(mutationPayload)
	if err != nil || !restoredMutation.Snapshot().Equal(closeRecord.Result().Snapshot()) || restoredMutation.Operation() != closeRecord.Operation() {
		t.Fatalf("mutation round trip failed: %v", err)
	}
	acceptResult := testAcceptResult(t, fixture)
	acceptPayload, err := encodeMutationResult(acceptResult)
	if err != nil {
		t.Fatal(err)
	}
	restoredAccept, err := decodeMutationResult(acceptPayload)
	if err != nil || !restoredAccept.AdmissionIntent().Valid() || !restoredAccept.Snapshot().Equal(acceptResult.Snapshot()) {
		t.Fatalf("admission round trip failed: %v", err)
	}
	for _, projection := range testProjectionResults(t, fixture) {
		encoded, encodeErr := encodeMutationResult(projection)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		restored, decodeErr := decodeMutationResult(encoded)
		if decodeErr != nil || restored.Operation() != projection.Operation() || !restored.Snapshot().Equal(projection.Snapshot()) || restored.Invite().Valid() != projection.Invite().Valid() || restored.Membership().Valid() != projection.Membership().Valid() {
			t.Fatalf("projection %s round trip failed: %v", projection.Operation(), decodeErr)
		}
	}
	ownerGraceRevision, _ := domain.NewRevision(2)
	ownerGrace, err := domain.NewSnapshot(fixture.snapshot.ID(), fixture.snapshot.OwnerID(), fixture.snapshot.WorldID(), fixture.snapshot.Assignment(), domain.LifecycleOwnerGrace, ownerGraceRevision, fixture.snapshot.Capacity(), fixture.snapshot.CreatedAt(), fixture.snapshot.ExpiresAt(), fixture.snapshot.OwnerBinding(), 1, fixture.now.Add(time.Minute), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerGracePayload, _ := encodeSnapshot(ownerGrace)
	restoredOwnerGrace, err := decodeSnapshot(ownerGracePayload)
	if err != nil || !restoredOwnerGrace.Equal(ownerGrace) {
		t.Fatalf("owner grace round trip failed: %v", err)
	}
	directive, _ := domain.NewSafeReturnDirective(fixture.snapshot.ID(), acceptResult.AdmissionIntent().VisitorID(), domain.SafeReturnReasonOwnerClosed)
	commandID, _ := domain.NewCommandID("vcmd_directiveCodec")
	fingerprint := testFingerprint(t, "directive")
	directiveResult, err := domain.NewMutationResult(domain.OperationClose, closeRecord.Result().Snapshot(), commandID, fingerprint, domain.InviteSnapshot{}, domain.AdmissionIntent{}, domain.MembershipSnapshot{}, []domain.SafeReturnDirective{directive})
	if err != nil {
		t.Fatal(err)
	}
	directivePayload, _ := encodeMutationResult(directiveResult)
	restoredDirective, err := decodeMutationResult(directivePayload)
	if err != nil || len(restoredDirective.Directives()) != 1 || restoredDirective.Directives()[0].Reason() != domain.SafeReturnReasonOwnerClosed {
		t.Fatalf("directive round trip failed: %v", err)
	}
}

// TestCodecRejectsNonCanonicalAndContradictoryPayload 固定unknown、额外field和metadata矛盾拒绝。
func TestCodecRejectsNonCanonicalAndContradictoryPayload(t *testing.T) {
	fixture := newTestFixture(t, "reject")
	payload, err := encodeSnapshot(fixture.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{
		" " + payload,
		strings.Replace(payload, `"lifecycle":"open"`, `"lifecycle":"unknown"`, 1),
		strings.TrimSuffix(payload, "}") + `,"extra":1}`,
		strings.Repeat("x", maximumSessionBytes+1),
	} {
		if snapshot, decodeErr := decodeSnapshot(malformed); decodeErr == nil || snapshot.Valid() {
			t.Fatalf("malformed payload accepted: %.40q", malformed)
		}
	}
	facts, _ := snapshotFacts(fixture.snapshot)
	if err := validateSessionMetadata(fixture.snapshot, "vses_other", fixture.snapshot.WorldID().String(), "1", "open", strconvTime(fixture.snapshot.ExpiresAt()), facts); err == nil {
		t.Fatal("contradictory metadata accepted")
	}
	if err := validateHashEncoding(activeDefinition(), "v", "1", "visit_id", strings.Repeat("x", maximumActiveBytes), "world", fixture.snapshot.WorldID().String(), "expires_us", strconvTime(fixture.snapshot.ExpiresAt())); err == nil {
		t.Fatal("oversized hash metadata accepted")
	}
	if result, err := decodeCreateResult(strings.Repeat("x", maximumCommandBytes+1)); err == nil || result.Snapshot().Valid() {
		t.Fatal("oversized command result accepted")
	}
	mutationPayload, _ := encodeMutationResult(testCloseRecord(t, fixture.snapshot, "wrongkind").Result())
	if result, err := decodeCreateResult(mutationPayload); err == nil || result.Snapshot().Valid() {
		t.Fatal("mutation payload accepted as create result")
	}
}

// TestExpiryMillisecondsRoundsUp 证明物理TTL不会在领域微秒deadline之前删除。
func TestExpiryMillisecondsRoundsUp(t *testing.T) {
	value := time.UnixMicro(1_700_000_000_000_001)
	encoded, err := expiryMilliseconds(value)
	if err != nil || encoded != "1700000000001" {
		t.Fatalf("expiry milliseconds = %q, %v", encoded, err)
	}
}

// FuzzDecodeSnapshot 验证任意payload不会绕过canonical与领域hydration边界。
func FuzzDecodeSnapshot(f *testing.F) {
	fixture := newTestFixture(f, "fuzz")
	payload, _ := encodeSnapshot(fixture.snapshot)
	f.Add(payload)
	f.Add("")
	f.Add(`{"id":"vses_fuzz"}`)
	f.Fuzz(func(t *testing.T, value string) {
		snapshot, err := decodeSnapshot(value)
		if err == nil && !snapshot.Valid() {
			t.Fatal("codec returned invalid snapshot without error")
		}
	})
}

// FuzzDecodeCommandResults 验证任意首次结果不会绕过kind、大小与领域交叉绑定。
func FuzzDecodeCommandResults(f *testing.F) {
	fixture := newTestFixture(f, "commandfuzz")
	createRecord := testCreateRecord(f, fixture.snapshot, "commandfuzz")
	createResult, _ := domainCreateResult(createRecord)
	createPayload, _ := encodeCreateResult(createResult)
	mutationPayload, _ := encodeMutationResult(testCloseRecord(f, fixture.snapshot, "commandfuzz").Result())
	f.Add("create", createPayload)
	f.Add("mutation", mutationPayload)
	f.Add("create", "")
	f.Fuzz(func(t *testing.T, kind string, payload string) {
		switch kind {
		case "create":
			result, err := decodeCreateResult(payload)
			if err == nil && (!result.Snapshot().Valid() || !result.CommandID().Valid() || !result.Fingerprint().Valid()) {
				t.Fatal("create codec returned invalid result without error")
			}
		case "mutation":
			result, err := decodeMutationResult(payload)
			if err == nil && (!result.Snapshot().Valid() || result.Operation() == domain.OperationUnspecified || !result.CommandID().Valid() || !result.Fingerprint().Valid()) {
				t.Fatal("mutation codec returned invalid result without error")
			}
		}
	})
}

// domainCreateResult 从CreateRecord恢复codec测试所需首次结果。
func domainCreateResult(record domain.CreateRecord) (domain.CreateResult, error) {
	return domain.NewCreateResult(record.Candidate(), record.CommandID(), record.Fingerprint())
}

// strconvTime 将有效领域时间转换为canonical UTC Unix微秒文本。
func strconvTime(value time.Time) string {
	encoded, _ := canonicalTime(value)
	return encoded
}

// testAcceptResult 构造同时携带reservation与AdmissionIntent的完整首次结果。
func testAcceptResult(t testing.TB, fixture testFixture) domain.MutationResult {
	t.Helper()
	visitor, _ := account.NewPlayerID("ply_visitorcodec")
	inviteID, _ := domain.NewInviteID("vinv_codec")
	revision, _ := domain.NewRevision(2)
	invite, _ := domain.NewInviteSnapshot(inviteID, visitor, domain.InviteStateAccepted, revision, fixture.now.Add(30*time.Minute))
	visitorSession, _ := session.NewSessionID("ses_visitorcodec")
	reservation := fixture.now.Add(10 * time.Minute)
	membership, _ := domain.NewMembershipSnapshot(visitor, inviteID, domain.MembershipStateReserved, visitorSession, session.InitialEpoch, domain.ConnectionBindingID{}, reservation, 0, time.Time{})
	target, err := domain.NewSnapshot(fixture.snapshot.ID(), fixture.snapshot.OwnerID(), fixture.snapshot.WorldID(), fixture.snapshot.Assignment(), domain.LifecycleOpen, revision, fixture.snapshot.Capacity(), fixture.snapshot.CreatedAt(), fixture.snapshot.ExpiresAt(), fixture.snapshot.OwnerBinding(), 0, time.Time{}, []domain.InviteSnapshot{invite}, []domain.MembershipSnapshot{membership})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := domain.HydrateAdmissionIntent(fixture.snapshot.ID(), visitor, visitorSession, session.InitialEpoch, fixture.snapshot.Assignment(), reservation)
	if err != nil {
		t.Fatal(err)
	}
	commandID, _ := domain.NewCommandID("vcmd_acceptCodec")
	result, err := domain.NewMutationResult(domain.OperationAcceptInvite, target, commandID, testFingerprint(t, "accept"), domain.InviteSnapshot{}, intent, domain.MembershipSnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// testProjectionResults 构造invite、joined与reconnecting可选projection的完整结果。
func testProjectionResults(t testing.TB, fixture testFixture) []domain.MutationResult {
	t.Helper()
	visitor, _ := account.NewPlayerID("ply_projection")
	inviteID, _ := domain.NewInviteID("vinv_projection")
	revision, _ := domain.NewRevision(2)
	visitorSession, _ := session.NewSessionID("ses_projection")
	bindingID, _ := domain.NewConnectionBindingID("vbind_projection")

	pending, _ := domain.NewInviteSnapshot(inviteID, visitor, domain.InviteStatePending, revision, fixture.now.Add(30*time.Minute))
	pendingTarget, err := domain.NewSnapshot(fixture.snapshot.ID(), fixture.snapshot.OwnerID(), fixture.snapshot.WorldID(), fixture.snapshot.Assignment(), domain.LifecycleOpen, revision, fixture.snapshot.Capacity(), fixture.snapshot.CreatedAt(), fixture.snapshot.ExpiresAt(), fixture.snapshot.OwnerBinding(), 0, time.Time{}, []domain.InviteSnapshot{pending}, nil)
	if err != nil {
		t.Fatal(err)
	}
	inviteCommand, _ := domain.NewCommandID("vcmd_projectionInvite")
	inviteFingerprint := testFingerprint(t, "projection-invite")
	inviteResult, err := domain.NewMutationResult(domain.OperationCreateInvite, pendingTarget, inviteCommand, inviteFingerprint, pending, domain.AdmissionIntent{}, domain.MembershipSnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	accepted, _ := domain.NewInviteSnapshot(inviteID, visitor, domain.InviteStateAccepted, revision, fixture.now.Add(30*time.Minute))
	joined, _ := domain.NewMembershipSnapshot(visitor, inviteID, domain.MembershipStateJoined, visitorSession, session.InitialEpoch, bindingID, time.Time{}, 0, time.Time{})
	joinedTarget, err := domain.NewSnapshot(fixture.snapshot.ID(), fixture.snapshot.OwnerID(), fixture.snapshot.WorldID(), fixture.snapshot.Assignment(), domain.LifecycleOpen, revision, fixture.snapshot.Capacity(), fixture.snapshot.CreatedAt(), fixture.snapshot.ExpiresAt(), fixture.snapshot.OwnerBinding(), 0, time.Time{}, []domain.InviteSnapshot{accepted}, []domain.MembershipSnapshot{joined})
	if err != nil {
		t.Fatal(err)
	}
	joinCommand, _ := domain.NewCommandID("vcmd_projectionJoin")
	joinFingerprint := testFingerprint(t, "projection-join")
	joinResult, err := domain.NewMutationResult(domain.OperationJoin, joinedTarget, joinCommand, joinFingerprint, domain.InviteSnapshot{}, domain.AdmissionIntent{}, joined, nil)
	if err != nil {
		t.Fatal(err)
	}

	reconnecting, _ := domain.NewMembershipSnapshot(visitor, inviteID, domain.MembershipStateReconnecting, visitorSession, session.InitialEpoch, bindingID, time.Time{}, 1, fixture.now.Add(time.Minute))
	reconnectingTarget, err := domain.NewSnapshot(fixture.snapshot.ID(), fixture.snapshot.OwnerID(), fixture.snapshot.WorldID(), fixture.snapshot.Assignment(), domain.LifecycleOpen, revision, fixture.snapshot.Capacity(), fixture.snapshot.CreatedAt(), fixture.snapshot.ExpiresAt(), fixture.snapshot.OwnerBinding(), 0, time.Time{}, []domain.InviteSnapshot{accepted}, []domain.MembershipSnapshot{reconnecting})
	if err != nil {
		t.Fatal(err)
	}
	reconnectCommand, _ := domain.NewCommandID("vcmd_projectionReconnect")
	reconnectFingerprint := testFingerprint(t, "projection-reconnect")
	reconnectResult, err := domain.NewMutationResult(domain.OperationVisitorReconnect, reconnectingTarget, reconnectCommand, reconnectFingerprint, domain.InviteSnapshot{}, domain.AdmissionIntent{}, reconnecting, nil)
	if err != nil {
		t.Fatal(err)
	}
	return []domain.MutationResult{inviteResult, joinResult, reconnectResult}
}
