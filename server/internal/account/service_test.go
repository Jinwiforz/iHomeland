package account

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// TestRegisterOrderAndSafeResult 验证注册固定顺序、canonical 持久化和安全结果投影。
func TestRegisterOrderAndSafeResult(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	result, err := fixture.service.Register(context.Background(), NewRegisterCommand("Player.One", "  StrongPass12  ", "  Cafe\u0301  Player "))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if !result.Account.Valid() || !result.Session.SessionID.Valid() {
		t.Fatal("Register() returned incomplete result")
	}
	if got := fixture.events.snapshot(); fmt.Sprint(got) != fmt.Sprint([]string{"hash", "id", "id", "clock", "create", "session"}) {
		t.Fatalf("operation order = %v", got)
	}
	record, outcome, err := fixture.repository.FindForAuthentication(context.Background(), mustUsername(t, "player.one"))
	if err != nil || outcome != FindOutcomeFound {
		t.Fatalf("FindForAuthentication() = %v, %v", outcome, err)
	}
	if record.Account.Username().String() != "player.one" || record.Account.DisplayName().String() != "Café Player" {
		t.Fatalf("stored account was not normalized: username=%q display=%q", record.Account.Username(), record.Account.DisplayName())
	}
	if fixture.hasher.lastHashed != "  StrongPass12  " {
		t.Fatal("register password was modified before hashing")
	}
}

// TestRegisterConflictAndConcurrentUniqueness 验证重复与并发 case variants 最多提交一个账号。
func TestRegisterConflictAndConcurrentUniqueness(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	commands := []RegisterCommand{
		NewRegisterCommand("Race.User", "StrongPassword1", "First"),
		NewRegisterCommand("race.user", "StrongPassword2", "Second"),
	}
	var successes atomic.Int32
	var conflicts atomic.Int32
	var wait sync.WaitGroup
	for _, command := range commands {
		command := command
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := fixture.service.Register(context.Background(), command)
			switch ErrorKindOf(err) {
			case ErrorKindUnspecified:
				successes.Add(1)
			case ErrorKindUsernameConflict:
				conflicts.Add(1)
			default:
				t.Errorf("Register() error kind = %s", ErrorKindOf(err))
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 || conflicts.Load() != 1 || fixture.repository.count() != 1 {
		t.Fatalf("success=%d conflict=%d records=%d", successes.Load(), conflicts.Load(), fixture.repository.count())
	}
	if _, err := fixture.service.Register(context.Background(), commands[0]); ErrorKindOf(err) != ErrorKindUsernameConflict {
		t.Fatalf("duplicate register kind = %s", ErrorKindOf(err))
	}
}

// TestRegisterSessionFailureKeepsAccount 验证账号提交后 session 失败不会伪造回滚，并可由 login 恢复。
func TestRegisterSessionFailureKeepsAccount(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.sessions.failNext(errors.New("session unavailable"))
	const username = "recover.user"
	const password = "StrongPassword1"
	command := NewRegisterCommand(username, password, "Recover")
	if _, err := fixture.service.Register(context.Background(), command); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseAccountCreated {
		t.Fatalf("Register() error = %v, phase = %s", err, CommitPhaseOf(err))
	}
	if fixture.repository.count() != 1 {
		t.Fatal("account was removed after session failure")
	}
	if _, err := fixture.service.Register(context.Background(), command); ErrorKindOf(err) != ErrorKindUsernameConflict {
		t.Fatalf("retry register kind = %s", ErrorKindOf(err))
	}
	result, err := fixture.service.Login(context.Background(), NewLoginCommand(username, password))
	if err != nil || !result.Session.SessionID.Valid() {
		t.Fatalf("Login() recovery = %v, %v", result, err)
	}
}

// TestRegisterCommitUnknownPreservesUncertainty 验证 repository 模糊提交不会被谎报为回滚或成功。
func TestRegisterCommitUnknownPreservesUncertainty(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.repository.createOutcome = CreateOutcomeCommitUnknown
	fixture.repository.createErr = context.DeadlineExceeded
	_, err := fixture.service.Register(context.Background(), NewRegisterCommand("unknown.commit", "StrongPassword1", "Unknown"))
	if ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseUnknown {
		t.Fatalf("Register() error = %v, phase = %s", err, CommitPhaseOf(err))
	}
	if fixture.sessions.count() != 0 {
		t.Fatal("session was issued after unknown account commit")
	}
}

// TestLoginEnumerationBoundary 验证 unknown、wrong password 与 inactive 使用同一错误并执行 dummy Verify。
func TestLoginEnumerationBoundary(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	register := NewRegisterCommand("known.user", "StrongPassword1", "Known")
	if _, err := fixture.service.Register(context.Background(), register); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	tests := []struct {
		// name 标识可枚举认证失败的来源。
		name string
		// username 是当前 login 输入。
		username string
		// password 是当前 login 原始密码。
		password string
		// prepare 可在调用前改变 repository 账号状态。
		prepare func()
	}{
		{name: "unknown", username: "missing.user", password: "StrongPassword1"},
		{name: "wrong", username: "known.user", password: "WrongPassword1"},
		{name: "inactive", username: "known.user", password: "StrongPassword1", prepare: func() { fixture.repository.setStatus(t, mustUsername(t, "known.user"), StatusInactive) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.prepare != nil {
				test.prepare()
			}
			beforeSessions := fixture.sessions.count()
			beforeVerify := fixture.hasher.verifyCount()
			_, err := fixture.service.Login(context.Background(), NewLoginCommand(test.username, test.password))
			if ErrorKindOf(err) != ErrorKindInvalidCredentials || CommitPhaseOf(err) != CommitPhaseNone {
				t.Fatalf("Login() error = %v", err)
			}
			if fixture.hasher.verifyCount() != beforeVerify+1 || fixture.sessions.count() != beforeSessions {
				t.Fatal("enumeration failure did not use exactly one verify and zero sessions")
			}
		})
	}
	if fixture.hasher.lastVerifiedHash() != mustCredentialHash(t, "StrongPassword1").Encoded() {
		t.Fatal("inactive account did not verify real credential before status rejection")
	}
}

// TestLoginCreatesIndependentSessions 验证每次成功登录均委托 Session Core 创建独立 session。
func TestLoginCreatesIndependentSessions(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	const username = "multi.session"
	const password = "StrongPassword1"
	command := NewRegisterCommand(username, password, "Multi")
	if _, err := fixture.service.Register(context.Background(), command); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	first, err := fixture.service.Login(context.Background(), NewLoginCommand(username, password))
	if err != nil {
		t.Fatalf("first Login() error = %v", err)
	}
	second, err := fixture.service.Login(context.Background(), NewLoginCommand(username, password))
	if err != nil {
		t.Fatalf("second Login() error = %v", err)
	}
	if first.Session.SessionID == second.Session.SessionID || fixture.sessions.count() != 3 {
		t.Fatal("successful logins reused or invalidated an existing session")
	}
}

// TestServiceGuardrails 覆盖无效依赖、malformed repository 和依赖失败的 fail-closed 行为。
func TestServiceGuardrails(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	if _, err := NewService(nil, fixture.hasher, fixture.sessions, fixture.clock, fixture.ids, fixture.dummyHash); ErrorKindOf(err) != ErrorKindValidation {
		t.Fatalf("NewService() kind = %s", ErrorKindOf(err))
	}
	if _, err := NewService(fixture.repository, fixture.hasher, fixture.sessions, fixture.clock, fixture.ids, CredentialHash{}); ErrorKindOf(err) != ErrorKindValidation {
		t.Fatalf("NewService() accepted zero dummy hash: %v", err)
	}
	fixture.repository.findRecord = AuthenticationRecord{}
	fixture.repository.findOutcome = FindOutcomeFound
	if _, err := fixture.service.Login(context.Background(), NewLoginCommand("malformed.user", "Password1234")); ErrorKindOf(err) != ErrorKindDependencyUnavailable {
		t.Fatalf("malformed record kind = %s", ErrorKindOf(err))
	}
	fixture.repository.findErr = errors.New("storage unavailable")
	if _, err := fixture.service.Login(context.Background(), NewLoginCommand("malformed.user", "Password1234")); ErrorKindOf(err) != ErrorKindDependencyUnavailable {
		t.Fatalf("repository error kind = %s", ErrorKindOf(err))
	}
}

// TestRegisterDependencyFailures 验证提交前故障不创建账号，context 取消也不会被解释为冲突。
func TestRegisterDependencyFailures(t *testing.T) {
	t.Parallel()
	command := NewRegisterCommand("failure.user", "StrongPassword1", "Failure")
	tests := []struct {
		// name 标识发生故障的注册阶段。
		name string
		// prepare 注入故障并返回当前调用使用的 context。
		prepare func(*serviceFixture) context.Context
	}{
		{name: "hash", prepare: func(fixture *serviceFixture) context.Context {
			fixture.hasher.hashErr = errors.New("hash unavailable")
			return context.Background()
		}},
		{name: "repository", prepare: func(fixture *serviceFixture) context.Context {
			fixture.repository.createOutcome = CreateOutcomeNotCommitted
			fixture.repository.createErr = errors.New("repository unavailable")
			return context.Background()
		}},
		{name: "cancelled", prepare: func(_ *serviceFixture) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			ctx := test.prepare(fixture)
			if _, err := fixture.service.Register(ctx, command); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseNone {
				t.Fatalf("Register() error = %v", err)
			}
			if fixture.repository.count() != 0 || fixture.sessions.count() != 0 {
				t.Fatal("pre-commit failure created account or session")
			}
		})
	}
	fixture := newServiceFixture(t)
	failingIDs := &failingIDGenerator{err: errors.New("entropy unavailable")}
	service, err := NewService(fixture.repository, fixture.hasher, fixture.sessions, fixture.clock, failingIDs, fixture.dummyHash)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Register(context.Background(), command); ErrorKindOf(err) != ErrorKindDependencyUnavailable || fixture.repository.count() != 0 {
		t.Fatalf("ID failure = %v, records=%d", err, fixture.repository.count())
	}
}

// TestLoginDependencyFailures 验证 verify 与 session 依赖错误 fail closed，不伪装成 invalid credentials。
func TestLoginDependencyFailures(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	const username = "login.failure"
	const password = "StrongPassword1"
	command := NewRegisterCommand(username, password, "Failure")
	if _, err := fixture.service.Register(context.Background(), command); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	fixture.hasher.verifyErr = errors.New("verify unavailable")
	if _, err := fixture.service.Login(context.Background(), NewLoginCommand(username, password)); ErrorKindOf(err) != ErrorKindDependencyUnavailable {
		t.Fatalf("verify failure kind = %s", ErrorKindOf(err))
	}
	fixture.hasher.verifyErr = nil
	fixture.sessions.failNext(errors.New("session unavailable"))
	if _, err := fixture.service.Login(context.Background(), NewLoginCommand(username, password)); ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseNone {
		t.Fatalf("session failure = %v", err)
	}
}

// TestConcurrentLoginCreatesIndependentSessions 验证并发成功登录不会共享 session identity。
func TestConcurrentLoginCreatesIndependentSessions(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	const username = "parallel.login"
	const password = "StrongPassword1"
	command := NewRegisterCommand(username, password, "Parallel")
	if _, err := fixture.service.Register(context.Background(), command); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	// 八个并发调用足以让 race detector 覆盖 fake 锁边界，又不依赖 sleep 或调度时序判定成功。
	const loginCount = 8
	ids := make(chan string, loginCount)
	var wait sync.WaitGroup
	for range loginCount {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := fixture.service.Login(context.Background(), NewLoginCommand(username, password))
			if err != nil {
				t.Errorf("Login() error = %v", err)
				return
			}
			ids <- result.Session.SessionID.String()
		}()
	}
	wait.Wait()
	close(ids)
	unique := make(map[string]struct{}, loginCount)
	for id := range ids {
		unique[id] = struct{}{}
	}
	if len(unique) != loginCount || fixture.sessions.count() != loginCount+1 {
		t.Fatalf("unique=%d issued=%d", len(unique), fixture.sessions.count())
	}
}

// eventLog 以互斥锁保存跨 fake 的调用顺序，避免并发测试产生数据竞争。
type eventLog struct {
	// mutex 保护 values。
	mutex sync.Mutex
	// values 保存低基数测试事件。
	values []string
}

// add 追加单个事件。
func (log *eventLog) add(value string) {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	log.values = append(log.values, value)
}

// snapshot 返回调用顺序副本。
func (log *eventLog) snapshot() []string {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	return append([]string(nil), log.values...)
}

// fakeClock 为所有注册提供固定绝对时间。
type fakeClock struct {
	// now 是测试显式选择的时间快照。
	now time.Time
	// events 记录 clock 在 ID 之后、repository 之前读取。
	events *eventLog
}

// Now 返回固定时间并记录调用。
func (clock *fakeClock) Now() time.Time {
	clock.events.add("clock")
	return clock.now
}

// fakeIDGenerator 生成并发安全且满足 ASCII identifier 约束的确定性材料。
type fakeIDGenerator struct {
	// next 保证每次调用返回不同材料。
	next atomic.Uint64
	// events 记录 identity 生成顺序。
	events *eventLog
}

// failingIDGenerator 模拟 Composition Root CSPRNG 无法提供身份材料。
type failingIDGenerator struct {
	// err 是每次 NewID 返回的稳定测试故障。
	err error
}

// NewID 返回熵源故障且不生成可用材料。
func (generator *failingIDGenerator) NewID() (string, error) { return "", generator.err }

// NewID 返回固定宽度字母数字材料。
func (generator *fakeIDGenerator) NewID() (string, error) {
	generator.events.add("id")
	return fmt.Sprintf("id%016d", generator.next.Add(1)), nil
}

// fakeHasher 使用 SHA-256 提供确定性测试边界，不代表 production password algorithm。
type fakeHasher struct {
	// mutex 保护观测字段。
	mutex sync.Mutex
	// events 记录 hash/verify 顺序。
	events *eventLog
	// lastHashed 保存测试断言所需原始输入。
	lastHashed string
	// verified 保存每次 Verify 使用的 encoded hash。
	verified []string
	// hashErr 模拟注册 hashing 依赖失败。
	hashErr error
	// verifyErr 模拟登录 verification 依赖失败。
	verifyErr error
}

// Hash 对原始测试密码生成自描述 fake hash。
func (hasher *fakeHasher) Hash(ctx context.Context, password RegisterPassword) (CredentialHash, error) {
	hasher.events.add("hash")
	hasher.mutex.Lock()
	hasher.lastHashed = password.Value()
	err := hasher.hashErr
	hasher.mutex.Unlock()
	if err != nil {
		return CredentialHash{}, err
	}
	if err := ctx.Err(); err != nil {
		return CredentialHash{}, err
	}
	return fakeCredentialHash(password.Value())
}

// Verify 使用相同 fake 路径比较 hash，并记录 dummy/real 调用。
func (hasher *fakeHasher) Verify(ctx context.Context, hash CredentialHash, password LoginPassword) (bool, error) {
	hasher.events.add("verify")
	hasher.mutex.Lock()
	hasher.verified = append(hasher.verified, hash.Encoded())
	err := hasher.verifyErr
	hasher.mutex.Unlock()
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	expected, err := fakeCredentialHash(password.Value())
	return err == nil && expected.Encoded() == hash.Encoded(), err
}

// verifyCount 返回已经执行的 Verify 次数。
func (hasher *fakeHasher) verifyCount() int {
	hasher.mutex.Lock()
	defer hasher.mutex.Unlock()
	return len(hasher.verified)
}

// lastVerifiedHash 返回最近 Verify 使用的 hash。
func (hasher *fakeHasher) lastVerifiedHash() string {
	hasher.mutex.Lock()
	defer hasher.mutex.Unlock()
	return hasher.verified[len(hasher.verified)-1]
}

// fakeCredentialHash 为测试密码生成不包含 plaintext 的确定性表示。
func fakeCredentialHash(value string) (CredentialHash, error) {
	digest := sha256.Sum256([]byte(value))
	return NewCredentialHash("$fake$sha256$" + hex.EncodeToString(digest[:]))
}

// referenceRepository 以 mutex 提供 canonical username 的线性化测试语义。
type referenceRepository struct {
	// mutex 保护 records 和可注入结果。
	mutex sync.Mutex
	// events 记录 repository 调用顺序。
	events *eventLog
	// records 保存完整认证快照，不模拟通用 CRUD。
	records map[string]AuthenticationRecord
	// createOutcome 非零时覆盖正常创建结果。
	createOutcome CreateOutcome
	// createErr 模拟已知或不确定依赖故障。
	createErr error
	// findRecord 非零测试值覆盖 map lookup。
	findRecord AuthenticationRecord
	// findOutcome 非零时覆盖 map lookup 结果。
	findOutcome FindOutcome
	// findErr 模拟读取依赖失败。
	findErr error
}

// Create 原子校验并提交 account/player/credential 测试事实。
func (repository *referenceRepository) Create(ctx context.Context, record CreateRecord) (CreateOutcome, error) {
	repository.events.add("create")
	if err := ctx.Err(); err != nil {
		return CreateOutcomeNotCommitted, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.createOutcome != CreateOutcomeUnspecified || repository.createErr != nil {
		return repository.createOutcome, repository.createErr
	}
	if !record.Account.Valid() || !record.Credential.Valid() {
		return CreateOutcomeNotCommitted, errors.New("incomplete create record")
	}
	key := record.Account.Username().String()
	if _, exists := repository.records[key]; exists {
		return CreateOutcomeUsernameConflict, nil
	}
	repository.records[key] = AuthenticationRecord{Account: record.Account, Credential: record.Credential}
	return CreateOutcomeCreated, nil
}

// FindForAuthentication 返回 canonical username 的完整测试快照。
func (repository *referenceRepository) FindForAuthentication(ctx context.Context, username Username) (AuthenticationRecord, FindOutcome, error) {
	if err := ctx.Err(); err != nil {
		return AuthenticationRecord{}, FindOutcomeUnspecified, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.findErr != nil || repository.findOutcome != FindOutcomeUnspecified {
		return repository.findRecord, repository.findOutcome, repository.findErr
	}
	record, exists := repository.records[username.String()]
	if !exists {
		return AuthenticationRecord{}, FindOutcomeNotFound, nil
	}
	return record, FindOutcomeFound, nil
}

// count 返回原子创建成功的 canonical username 数量。
func (repository *referenceRepository) count() int {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return len(repository.records)
}

// setStatus 用新 Account 替换认证快照，模拟管理边界已提交的 inactive 事实。
func (repository *referenceRepository) setStatus(t testing.TB, username Username, status Status) {
	t.Helper()
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	record := repository.records[username.String()]
	account, err := NewAccount(record.Account.ID(), record.Account.PlayerID(), record.Account.Username(), record.Account.DisplayName(), status, record.Account.CreatedAt())
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	record.Account = account
	repository.records[username.String()] = record
}

// fakeSessionIssuer 为每次成功调用创建独立 session identity。
type fakeSessionIssuer struct {
	// mutex 保护 nextError。
	mutex sync.Mutex
	// events 记录 session 必须位于 repository 提交之后。
	events *eventLog
	// issued 保存成功创建的 session 数量。
	issued atomic.Uint64
	// nextError 只让下一次调用失败。
	nextError error
}

// CreateSession 返回独立 SessionResult，失败时不返回伪 token。
func (issuer *fakeSessionIssuer) CreateSession(_ context.Context, principal session.Principal) (session.SessionResult, error) {
	issuer.events.add("session")
	if !principal.Valid() {
		return session.SessionResult{}, errors.New("invalid principal")
	}
	issuer.mutex.Lock()
	err := issuer.nextError
	issuer.nextError = nil
	issuer.mutex.Unlock()
	if err != nil {
		return session.SessionResult{}, err
	}
	sequence := issuer.issued.Add(1)
	id, idErr := session.NewSessionID(fmt.Sprintf("ses_test%016d", sequence))
	if idErr != nil {
		return session.SessionResult{}, idErr
	}
	return session.SessionResult{SessionID: id, Epoch: session.InitialEpoch, ExpiresAt: time.Unix(1_700_003_600, 0)}, nil
}

// failNext 配置下一次 session 创建失败。
func (issuer *fakeSessionIssuer) failNext(err error) {
	issuer.mutex.Lock()
	defer issuer.mutex.Unlock()
	issuer.nextError = err
}

// count 返回成功签发的 session 数量。
func (issuer *fakeSessionIssuer) count() uint64 { return issuer.issued.Load() }

// serviceFixture 汇总每个测试独占的 account service 和可观测依赖。
type serviceFixture struct {
	// service 是被测应用服务。
	service *Service
	// repository 是并发安全 reference adapter。
	repository *referenceRepository
	// hasher 是仅测试使用的确定性 credential boundary。
	hasher *fakeHasher
	// sessions 记录独立 session 签发。
	sessions *fakeSessionIssuer
	// clock 提供固定 created time。
	clock *fakeClock
	// ids 提供唯一测试 identity。
	ids *fakeIDGenerator
	// dummyHash 与 fake hasher 使用同一算法路径。
	dummyHash CredentialHash
	// events 聚合跨依赖调用顺序。
	events *eventLog
}

// newServiceFixture 构造不依赖 listener、MySQL 或 Redis 的账号测试环境。
func newServiceFixture(t testing.TB) *serviceFixture {
	t.Helper()
	events := new(eventLog)
	repository := &referenceRepository{events: events, records: make(map[string]AuthenticationRecord)}
	hasher := &fakeHasher{events: events}
	sessions := &fakeSessionIssuer{events: events}
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0), events: events}
	ids := &fakeIDGenerator{events: events}
	dummyHash, err := fakeCredentialHash("dummy-password-with-same-cost")
	if err != nil {
		t.Fatalf("fakeCredentialHash() error = %v", err)
	}
	service, err := NewService(repository, hasher, sessions, clock, ids, dummyHash)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return &serviceFixture{service: service, repository: repository, hasher: hasher, sessions: sessions, clock: clock, ids: ids, dummyHash: dummyHash, events: events}
}

// mustCredentialHash 返回 fake hasher 对输入生成的 hash，供测试比较路径而非 plaintext。
func mustCredentialHash(t testing.TB, value string) CredentialHash {
	t.Helper()
	hash, err := fakeCredentialHash(value)
	if err != nil {
		t.Fatalf("fakeCredentialHash() error = %v", err)
	}
	return hash
}

// mustUsername 构造测试 canonical username。
func mustUsername(t testing.TB, value string) Username {
	t.Helper()
	username, err := NewUsername(value)
	if err != nil {
		t.Fatalf("NewUsername() error = %v", err)
	}
	return username
}

// 编译期断言确保测试 adapter 始终完整实现消费侧接口。
var (
	_ Clock             = (*fakeClock)(nil)
	_ IDGenerator       = (*fakeIDGenerator)(nil)
	_ CredentialHasher  = (*fakeHasher)(nil)
	_ AccountRepository = (*referenceRepository)(nil)
	_ SessionIssuer     = (*fakeSessionIssuer)(nil)
	_ IDGenerator       = (*failingIDGenerator)(nil)
)
