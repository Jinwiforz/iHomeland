//go:build storage_integration

package battleticket

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	domain "github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	redisclient "github.com/redis/go-redis/v9"
)

// integrationObserver 丢弃不含 identity 的固定 storage result。
type integrationObserver struct{}

// RecordStorageOperation 满足 Observer 且不保留测试输入。
func (integrationObserver) RecordStorageOperation(string, string, string) {}

// TestStoreIssueReplayCorruptionAndFlush 使用真实 Redis 验证原子幂等、codec、损坏和 flush 语义。
func TestStoreIssueReplayCorruptionAndFlush(t *testing.T) {
	ctx := context.Background()
	client := integrationClient(t)
	defer client.Close()
	defer client.FlushDB(ctx)
	registry, err := storageredis.NewRegistry(Definitions())
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("bt"+strconv.FormatInt(time.Now().UnixNano(), 10), registry)
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(client, keyspace, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	deriver, err := domain.NewDeriver(bytes.Repeat([]byte{0x61}, 32))
	if err != nil {
		t.Fatal(err)
	}
	facts := integrationFacts(t, now)
	material, err := deriver.Derive(facts)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := domain.NewPolicy(30*time.Second, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	physicalExpiry, err := policy.PhysicalExpiresAt(material.Binding())
	if err != nil {
		t.Fatal(err)
	}
	record := domain.IssueRecord{
		Binding: material.Binding(), Fingerprint: material.Fingerprint(),
		SecretDigest: material.Secret().Digest(), ProofDigest: material.ProofKey().Digest(),
		PhysicalExpiresAt: physicalExpiry,
	}
	if outcome, err := store.Issue(ctx, record, now); err != nil || outcome != domain.IssueOutcomeCreated {
		t.Fatalf("issue outcome=%v err=%v", outcome, err)
	}
	key, err := store.issueKey(facts.IssueID)
	if err != nil {
		t.Fatal(err)
	}
	ttl, err := client.PTTL(ctx, key.Value()).Result()
	if err != nil || ttl <= 0 || ttl > 6*time.Minute {
		t.Fatalf("BattleTicket issue TTL=%s err=%v", ttl, err)
	}
	snapshot, outcome, err := store.Resolve(ctx, facts.IssueID)
	if err != nil || outcome != domain.ResolveOutcomeFound || !snapshot.Valid() ||
		!snapshot.Binding.Equal(material.Binding()) ||
		!snapshot.SecretDigest.Equal(material.Secret().Digest()) ||
		!snapshot.ProofDigest.Equal(material.ProofKey().Digest()) {
		t.Fatalf("resolve outcome=%v snapshot=%#v err=%v", outcome, snapshot, err)
	}
	replayed, err := deriver.Derive(snapshot.Binding.Facts())
	if err != nil || replayed.Secret().Value() != material.Secret().Value() ||
		replayed.ProofKey().Bytes() != material.ProofKey().Bytes() {
		t.Fatal("resolved binding did not reproduce first credential/proof material")
	}
	if outcome, err := store.Issue(ctx, record, now); err != nil || outcome != domain.IssueOutcomeReplay {
		t.Fatalf("issue replay outcome=%v err=%v", outcome, err)
	}
	changedFacts := facts
	changedFacts.TargetRevision++
	changedMaterial, err := deriver.Derive(changedFacts)
	if err != nil {
		t.Fatal(err)
	}
	changedRecord := domain.IssueRecord{
		Binding: changedMaterial.Binding(), Fingerprint: changedMaterial.Fingerprint(),
		SecretDigest: changedMaterial.Secret().Digest(), ProofDigest: changedMaterial.ProofKey().Digest(),
		PhysicalExpiresAt: physicalExpiry,
	}
	if outcome, err := store.Issue(ctx, changedRecord, now); err != nil || outcome != domain.IssueOutcomeIdempotencyConflict {
		t.Fatalf("target drift outcome=%v err=%v", outcome, err)
	}
	stored, err := client.HGetAll(ctx, key.Value()).Result()
	if err != nil {
		t.Fatal(err)
	}
	secretBytes := material.Secret().Bytes()
	proofBytes := material.ProofKey().Bytes()
	for field, value := range stored {
		if value == material.Secret().Value() || value == string(secretBytes[:]) ||
			value == string(proofBytes[:]) || strings.Contains(field, "proof_key") ||
			field == "ticket_secret" || field == "secret" {
			t.Fatalf("Redis stored raw BattleTicket secret material in field %s", field)
		}
	}
	if err := client.HSet(ctx, key.Value(), "v", "999").Err(); err != nil {
		t.Fatal(err)
	}
	if snapshot, outcome, err := store.Resolve(ctx, facts.IssueID); err == nil ||
		outcome != domain.ResolveOutcomeUnspecified || snapshot.Valid() {
		t.Fatalf("unknown schema outcome=%v snapshot=%#v err=%v", outcome, snapshot, err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if snapshot, outcome, err := store.Resolve(ctx, facts.IssueID); err != nil ||
		outcome != domain.ResolveOutcomeNotFound || snapshot.Valid() {
		t.Fatalf("flush recovery outcome=%v snapshot=%#v err=%v", outcome, snapshot, err)
	}
}

// integrationClient 借用 storage harness Redis 的独立 database，避免 flush 影响其他 packages。
func integrationClient(t *testing.T) *redisclient.Client {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	client := redisclient.NewClient(&redisclient.Options{
		Addr: os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), Password: string(password), DB: 15,
		DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxRetries: -1,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.FlushDB(context.Background()).Err() })
	return client
}

// integrationFacts 构造完整 Owner target，供真实 Redis codec 往返。
func integrationFacts(t *testing.T, issuedAt time.Time) domain.Facts {
	t.Helper()
	playerID, _ := account.NewPlayerID("ply_battleredis")
	sessionID, _ := session.NewSessionID("ses_battleredis")
	worldID, _ := personalworld.NewPersonalWorldID("pworld_battleredis")
	instanceID, _ := placement.NewWorldInstanceID("winst_battleredis")
	runtimeNodeID, _ := placement.NewRuntimeNodeID("rnode_battleredis")
	generation, _ := placement.NewAssignmentGeneration(3)
	fence, _ := placement.NewFencingToken(4)
	assignment, _ := placement.NewAssignmentStamp(worldID, instanceID, runtimeNodeID, generation, fence)
	assignmentFingerprint, _ := simulationcontrol.NewDigest(strings.Repeat("1", 64))
	simulationNodeID, _ := simulationcontrol.NewSimulationNodeID("snode_battleredis")
	simulationInstanceID, _ := simulationcontrol.NewSimulationInstanceID("sinst_abcdef0123456789abcdef0123456789")
	modelIdentity, _ := simulationcontrol.NewDigest(strings.Repeat("2", 64))
	profileIdentity, _ := simulationcontrol.NewDigest(strings.Repeat("3", 64))
	configIdentity, _ := simulationcontrol.NewDigest(strings.Repeat("4", 64))
	wireIdentity, _ := domain.ParseDigestHex(strings.Repeat("5", 64))
	slot, _ := domain.NewActorSlot(7)
	endpoint, _ := domain.NewEndpoint("battle.example.invalid", 58445)
	issueID, _ := domain.NewIssueID("issue_battle_redis_00000001")
	return domain.Facts{
		PlayerID: playerID, SessionID: sessionID, SessionEpoch: session.InitialEpoch, Role: domain.RoleOwner,
		WorldID: worldID, VisitSessionID: visitsession.VisitSessionID{}, Assignment: assignment,
		AssignmentFingerprint: assignmentFingerprint, RuntimeNodeID: runtimeNodeID,
		SimulationNodeID: simulationNodeID, SimulationInstanceID: simulationInstanceID,
		MappingGeneration: 3, TargetRevision: 7,
		ModelIdentity: modelIdentity, ProfileIdentity: profileIdentity, ConfigIdentity: configIdentity,
		WireIdentity: wireIdentity, ActorSlot: slot, Endpoint: endpoint, IssueID: issueID,
		IssuedAt: issuedAt, ExpiresAt: issuedAt.Add(30 * time.Second),
	}
}
