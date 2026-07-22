//go:build storage_integration

package worldadmission

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	domain "github.com/jinwiforz/ihomeland/server/internal/worldadmission"
	redisclient "github.com/redis/go-redis/v9"
)

// integrationObserver 丢弃不含identity的固定存储结果。
type integrationObserver struct{}

// RecordStorageOperation 丢弃集成测试不需要断言的低基数观测结果。
func (integrationObserver) RecordStorageOperation(string, string, string) {}

// TestStoreIssueConsumeAndReplay 覆盖真实Redis幂等签发、单次消费与精确response-loss重放语义。
func TestStoreIssueConsumeAndReplay(t *testing.T) {
	ctx := context.Background()
	client := integrationClient(t)
	defer client.Close()
	registry, err := storageredis.NewRegistry(Definitions())
	if err != nil {
		t.Fatal(err)
	}
	environment := "wa" + strconv.FormatInt(time.Now().UnixNano(), 10)
	keyspace, err := storageredis.NewKeyspace(environment, registry)
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(client, keyspace, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	binding, auth, endpoint := integrationBinding(t, now)
	issueID, _ := domain.NewIssueID("issue_integration")
	fingerprint := integrationDigest(t, "fingerprint")
	credential, _ := domain.ParseCredential("wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	record := domain.IssueRecord{IssueID: issueID, Fingerprint: fingerprint, CredentialDigest: credential.Digest(), Binding: binding, PhysicalExpiresAt: now.Add(2 * time.Minute)}
	if outcome, err := store.Issue(ctx, record, now); err != nil || outcome != domain.IssueOutcomeCreated {
		t.Fatalf("issue outcome=%v err=%v", outcome, err)
	}
	resolved, resolveOutcome, resolveErr := store.ResolveIssue(ctx, issueID)
	if resolveErr != nil || resolveOutcome != domain.IssueResolveOutcomeFound || !resolved.Valid() || !resolved.Binding.Equal(binding) || !resolved.Fingerprint.Equal(fingerprint) || !resolved.CredentialDigest.Equal(credential.Digest()) || resolved.Consumed {
		t.Fatalf("resolve issue outcome=%v snapshot=%#v err=%v", resolveOutcome, resolved, resolveErr)
	}
	if outcome, err := store.Issue(ctx, record, now); err != nil || outcome != domain.IssueOutcomeReplay {
		t.Fatalf("issue replay outcome=%v err=%v", outcome, err)
	}
	consumeID, _ := domain.NewConsumeID("consume_integration")
	consumeFingerprint := integrationDigest(t, "consume-fingerprint")
	request := domain.ConsumeRequest{CredentialDigest: credential.Digest(), ConsumeID: consumeID, Fingerprint: consumeFingerprint, Auth: auth, Endpoint: endpoint, Purpose: domain.PurposeJoin, ObservedAt: now}
	consumed, outcome, err := store.Consume(ctx, request)
	if err != nil || outcome != domain.ConsumeOutcomeApplied || !consumed.Equal(binding) {
		t.Fatalf("consume outcome=%v binding=%#v err=%v", outcome, consumed, err)
	}
	consumed, outcome, err = store.Consume(ctx, request)
	if err != nil || outcome != domain.ConsumeOutcomeReplay || !consumed.Equal(binding) {
		t.Fatalf("consume replay outcome=%v err=%v", outcome, err)
	}
	otherID, _ := domain.NewConsumeID("consume_other")
	request.ConsumeID = otherID
	request.Fingerprint = integrationDigest(t, "other-fingerprint")
	if consumed, outcome, err = store.Consume(ctx, request); err != nil || outcome != domain.ConsumeOutcomeReplayed || consumed.Valid() {
		t.Fatalf("replayed outcome=%v binding=%#v err=%v", outcome, consumed, err)
	}
	if outcome, err := store.Issue(ctx, record, now); err != nil || outcome != domain.IssueOutcomeConsumed {
		t.Fatalf("consumed issue replay outcome=%v err=%v", outcome, err)
	}
}

// TestStoreFlushAndCorruptionFailClosed 验证key丢失与损坏Hash不会恢复或覆盖旧资格。
func TestStoreFlushAndCorruptionFailClosed(t *testing.T) {
	ctx := context.Background()
	client := integrationClient(t)
	defer client.Close()
	registry, _ := storageredis.NewRegistry(Definitions())
	keyspace, _ := storageredis.NewKeyspace("wafail"+strconv.FormatInt(time.Now().UnixNano(), 10), registry)
	store, _ := New(client, keyspace, integrationObserver{})
	now := time.Now().UTC().Truncate(time.Microsecond)
	binding, auth, endpoint := integrationBinding(t, now)
	credential, _ := domain.ParseCredential("wad1_AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE")
	consumeID, _ := domain.NewConsumeID("consume_missing")
	request := domain.ConsumeRequest{CredentialDigest: credential.Digest(), ConsumeID: consumeID, Fingerprint: integrationDigest(t, "missing"), Auth: auth, Endpoint: endpoint, Purpose: domain.PurposeJoin, ObservedAt: now}
	if _, outcome, err := store.Consume(ctx, request); err != nil || outcome != domain.ConsumeOutcomeNotFound {
		t.Fatalf("missing outcome=%v err=%v", outcome, err)
	}
	key, _ := keyspace.Build(credentialDefinitionName, credential.Digest().Hex())
	if err := client.HSet(ctx, key.Value(), "v", "999").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Expire(ctx, key.Value(), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, outcome, err := store.Consume(ctx, request); err == nil || outcome != domain.ConsumeOutcomeUnspecified {
		t.Fatalf("corrupt outcome=%v err=%v", outcome, err)
	}
	_ = binding
}

// TestStoreOversizedHashesFailClosed 验证 registry 编码预算由真实 Lua 读取路径强制执行。
func TestStoreOversizedHashesFailClosed(t *testing.T) {
	ctx := context.Background()
	client := integrationClient(t)
	defer client.Close()
	registry, _ := storageredis.NewRegistry(Definitions())
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("issue", func(t *testing.T) {
		keyspace, _ := storageredis.NewKeyspace("waoversizedissue"+strconv.FormatInt(time.Now().UnixNano(), 10), registry)
		store, _ := New(client, keyspace, integrationObserver{})
		binding, _, _ := integrationBinding(t, now)
		issueID, _ := domain.NewIssueID("issue_oversized")
		credential, _ := domain.ParseCredential("wad1_AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI")
		record := domain.IssueRecord{IssueID: issueID, Fingerprint: integrationDigest(t, "oversized-issue"), CredentialDigest: credential.Digest(), Binding: binding, PhysicalExpiresAt: now.Add(2 * time.Minute)}
		if outcome, err := store.Issue(ctx, record, now); err != nil || outcome != domain.IssueOutcomeCreated {
			t.Fatalf("setup outcome=%v err=%v", outcome, err)
		}
		key, _ := keyspace.Build(issueDefinitionName, storageredis.DigestIdentity([]byte(issueID.Value())))
		if err := client.HSet(ctx, key.Value(), "fingerprint", strings.Repeat("a", maximumIssueBytes)).Err(); err != nil {
			t.Fatal(err)
		}
		if outcome, err := store.Issue(ctx, record, now); err == nil || outcome != domain.IssueOutcomeUnspecified {
			t.Fatalf("oversized issue outcome=%v err=%v", outcome, err)
		}
	})

	t.Run("credential", func(t *testing.T) {
		keyspace, _ := storageredis.NewKeyspace("waoversizedcredential"+strconv.FormatInt(time.Now().UnixNano(), 10), registry)
		store, _ := New(client, keyspace, integrationObserver{})
		binding, auth, endpoint := integrationBinding(t, now)
		issueID, _ := domain.NewIssueID("credential_oversized")
		credential, _ := domain.ParseCredential("wad1_AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM")
		record := domain.IssueRecord{IssueID: issueID, Fingerprint: integrationDigest(t, "oversized-credential"), CredentialDigest: credential.Digest(), Binding: binding, PhysicalExpiresAt: now.Add(2 * time.Minute)}
		if outcome, err := store.Issue(ctx, record, now); err != nil || outcome != domain.IssueOutcomeCreated {
			t.Fatalf("setup outcome=%v err=%v", outcome, err)
		}
		key, _ := keyspace.Build(credentialDefinitionName, credential.Digest().Hex())
		if err := client.HSet(ctx, key.Value(), "host", strings.Repeat("a", maximumCredentialBytes)).Err(); err != nil {
			t.Fatal(err)
		}
		consumeID, _ := domain.NewConsumeID("consume_oversized")
		request := domain.ConsumeRequest{CredentialDigest: credential.Digest(), ConsumeID: consumeID, Fingerprint: integrationDigest(t, "oversized-consume"), Auth: auth, Endpoint: endpoint, Purpose: domain.PurposeJoin, ObservedAt: now}
		if binding, outcome, err := store.Consume(ctx, request); err == nil || outcome != domain.ConsumeOutcomeUnspecified || binding.Valid() {
			t.Fatalf("oversized credential outcome=%v binding=%#v err=%v", outcome, binding, err)
		}
	})
}

// integrationClient 使用 storage harness 注入的临时 Redis，并由测试注册 cleanup 关闭借用连接。
func integrationClient(t *testing.T) *redisclient.Client {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	client := redisclient.NewClient(&redisclient.Options{Addr: os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), Password: string(password), DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxRetries: -1})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	return client
}

// integrationDigest 从稳定测试输入构造完整 SHA-256 摘要。
func integrationDigest(t *testing.T, value string) domain.Digest {
	t.Helper()
	raw := sha256.Sum256([]byte(value))
	digest, err := domain.NewDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// integrationBinding 构造同一组 binding、AuthContext 与 endpoint，供真实 Lua 往返比较。
func integrationBinding(t *testing.T, now time.Time) (domain.Binding, session.AuthContext, session.Endpoint) {
	t.Helper()
	playerID, _ := account.NewPlayerID("ply_admissionIntegration")
	sessionID, _ := session.NewSessionID("ses_admissionIntegration")
	worldID, _ := personalworld.NewPersonalWorldID("pworld_admissionIntegration")
	visitID, _ := visitsession.NewVisitSessionID("vses_admissionIntegration")
	instanceID, _ := placement.NewWorldInstanceID("winst_admissionIntegration")
	nodeID, _ := placement.NewRuntimeNodeID("rnode_admissionIntegration")
	generation, _ := placement.NewAssignmentGeneration(1)
	fence, _ := placement.NewFencingToken(1)
	stamp, _ := placement.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	endpoint, _ := session.NewEndpoint(session.ChannelTLSTCP, "game.example.invalid", 4433)
	binding, err := domain.NewBinding(playerID, sessionID, session.InitialEpoch, domain.RoleVisitor, worldID, visitID, domain.PurposeJoin, visitsession.InitialRevision, stamp, endpoint, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	auth := integrationAuth(t, now, playerID, sessionID, endpoint)
	return binding, auth, endpoint
}

// integrationSessionStore 只为真实session service返回ticket消费事实。
type integrationSessionStore struct {
	// snapshot 是权威session identity与gameplay scope。
	snapshot session.AuthSnapshot
}

// Create 标记本集成 fixture 不会创建 session。
func (store integrationSessionStore) Create(context.Context, session.SessionBundle) (session.StoreOutcome, error) {
	return session.StoreOutcomeUnspecified, errors.New("unused")
}

// ResolveAccess 标记本集成 fixture 不会解析 access credential。
func (store integrationSessionStore) ResolveAccess(context.Context, session.Digest, time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.StoreOutcomeUnspecified, errors.New("unused")
}

// RotateRefresh 标记本集成 fixture 不会轮换 refresh credential。
func (store integrationSessionStore) RotateRefresh(context.Context, session.Rotation) (session.AuthSnapshot, session.Invalidation, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.Invalidation{}, session.StoreOutcomeUnspecified, errors.New("unused")
}

// IssueTicket 标记 AuthContext 已通过预置 ticket 路径构造，不在此处签发。
func (store integrationSessionStore) IssueTicket(context.Context, session.TicketRecord, time.Time) (session.StoreOutcome, error) {
	return session.StoreOutcomeUnspecified, errors.New("unused")
}

// ConsumeTicket 返回预置的权威 session snapshot。
func (store integrationSessionStore) ConsumeTicket(context.Context, session.Digest, session.Channel, session.Endpoint, time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	return store.snapshot, session.StoreOutcomeApplied, nil
}

// InvalidateSession 标记本集成 fixture 不触发 session 失效。
func (store integrationSessionStore) InvalidateSession(context.Context, session.SessionID, session.InvalidationReason) (session.Invalidation, session.StoreOutcome, error) {
	return session.Invalidation{}, session.StoreOutcomeUnspecified, errors.New("unused")
}

// InvalidatePrincipal 标记本集成 fixture 不触发 principal 失效。
func (store integrationSessionStore) InvalidatePrincipal(context.Context, session.Principal, session.InvalidationReason) ([]session.Invalidation, error) {
	return nil, errors.New("unused")
}

// integrationEndpoint 返回测试listener identity。
type integrationEndpoint struct {
	// value 是唯一受信endpoint。
	value session.Endpoint
}

// EndpointFor 返回 storage integration 使用的唯一受信 TLS/TCP endpoint。
func (provider integrationEndpoint) EndpointFor(context.Context, session.Channel) (session.Endpoint, error) {
	return provider.value, nil
}

// integrationInvalidator 满足未触发的连接失效边界。
type integrationInvalidator struct{}

// Invalidate 满足本用例不会触发的连接失效边界。
func (integrationInvalidator) Invalidate(context.Context, session.Invalidation) error { return nil }

// integrationClock 固定单次测试的绝对时间。
type integrationClock struct {
	// value 是session service读取的受信时刻。
	value time.Time
}

// Now 返回测试固定的绝对时间。
func (clock integrationClock) Now() time.Time { return clock.value }

// integrationIDs 满足本用例不会调用的session ID边界。
type integrationIDs struct{}

// NewID 返回不会用于持久 identity 的固定测试值。
func (integrationIDs) NewID() (string, error) { return "integration", nil }

// integrationSecrets 满足本用例不会生成新credential的熵边界。
type integrationSecrets struct{}

// Fill 用确定性字节填充仅供测试构造的 ticket nonce。
func (integrationSecrets) Fill(buffer []byte) error {
	for index := range buffer {
		buffer[index] = byte(index + 1)
	}
	return nil
}

// integrationAuth 通过真实 session service 消费 ticket，生成不可直接构造的 gameplay AuthContext。
func integrationAuth(t *testing.T, now time.Time, playerID account.PlayerID, sessionID session.SessionID, endpoint session.Endpoint) session.AuthContext {
	t.Helper()
	principal, _ := session.NewPrincipal("acc_admissionIntegration", playerID.String())
	scopes, _ := session.NewScopeSet(session.ScopeGameplay)
	store := integrationSessionStore{snapshot: session.AuthSnapshot{Principal: principal, SessionID: sessionID, Epoch: session.InitialEpoch, Scopes: scopes}}
	policy, _ := session.NewPolicy(time.Second, time.Minute, time.Hour, time.Hour)
	service, err := session.NewService(store, integrationEndpoint{endpoint}, integrationInvalidator{}, integrationClock{now}, integrationIDs{}, integrationSecrets{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	nonce, _ := session.ParseTicketNonce([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	auth, err := service.ConsumeTicket(context.Background(), nonce, session.ChannelTLSTCP, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}
