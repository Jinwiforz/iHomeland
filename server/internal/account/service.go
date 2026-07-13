package account

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// authenticationCommandPlaceholder 是 register/login command 所有默认输出唯一允许的文本。
const authenticationCommandPlaceholder = "[REDACTED_AUTH_COMMAND]"

// RegisterCommand 封装客户端允许提交的原始注册字段，并阻止默认格式化泄漏 password。
//
// 字段保持私有，adapter 必须通过 NewRegisterCommand 创建该值。构造过程不提前规范化或
// 校验输入，确保 Register 仍是 validation error、调用顺序和敏感值生命周期的唯一 owner。
type RegisterCommand struct {
	// username 是登录 key 的原始输入，由 Register 规范化且不会写入错误。
	username string
	// password 是未经变换的 plaintext，只在 Register 当前调用内交给 CredentialHasher。
	password string
	// displayName 是公开展示输入，由 Register 执行 Unicode 安全规范化。
	displayName string
}

// NewRegisterCommand 保存 adapter 已完成 decode/size 检查的原始字段，不执行领域校验。
func NewRegisterCommand(username string, password string, displayName string) RegisterCommand {
	return RegisterCommand{username: username, password: password, displayName: displayName}
}

// String 返回固定占位符，避免 command 的默认格式化暴露 plaintext 或可枚举 username。
func (RegisterCommand) String() string { return authenticationCommandPlaceholder }

// GoString 防止 `%#v` 展开 RegisterCommand 私有字段。
func (RegisterCommand) GoString() string { return authenticationCommandPlaceholder }

// LogValue 让 slog 只记录固定 command 占位符，不展开任何认证输入。
func (RegisterCommand) LogValue() slog.Value {
	return slog.StringValue(authenticationCommandPlaceholder)
}

// LoginCommand 封装建立新 session 所需的原始凭据，并阻止默认格式化泄漏。
type LoginCommand struct {
	// username 是待规范化的登录 key，不得在认证失败日志中作为 label。
	username string
	// password 是未经变换的 plaintext，只在 Login 当前调用内交给 CredentialHasher。
	password string
}

// NewLoginCommand 保存 adapter 已完成 decode/size 检查的原始字段，不执行领域校验。
func NewLoginCommand(username string, password string) LoginCommand {
	return LoginCommand{username: username, password: password}
}

// String 返回固定占位符，使 unknown username 与真实账号输入具有相同默认输出。
func (LoginCommand) String() string { return authenticationCommandPlaceholder }

// GoString 防止 `%#v` 展开 LoginCommand 私有字段。
func (LoginCommand) GoString() string { return authenticationCommandPlaceholder }

// LogValue 让 slog 只记录固定 command 占位符，不展开 username 或 plaintext。
func (LoginCommand) LogValue() slog.Value { return slog.StringValue(authenticationCommandPlaceholder) }

// AuthResult 汇总安全账号投影与 Session Core 的签发结果。
type AuthResult struct {
	// Account 不包含 username、player ID、status 或 credential hash。
	Account AccountSummary
	// Session 是 Session Core 唯一生成的 session/token 结果。
	Session session.SessionResult
}

// Service 编排 transport-independent register/login，不拥有 listener 或存储连接。
//
// Service 构造后保持只读，可以在其 repository、hasher、session issuer、clock 与 ID generator
// 均满足并发契约时并发调用。它不缓存 account、password 或 session，不承担 rate limit；
// transport adapter 仍须对登录和注册实施独立的请求预算与枚举防护。
type Service struct {
	// repository 保存账号持久事实并提供认证快照。
	repository AccountRepository
	// hasher 是 plaintext password 唯一允许进入的安全边界。
	hasher CredentialHasher
	// sessions 将已验证 repository 身份交给 Session Core。
	sessions SessionIssuer
	// clock 为注册固定唯一 created time 快照。
	clock Clock
	// ids 为 account/player identity 提供共享 CSPRNG 材料。
	ids IDGenerator
	// dummyHash 保证 unknown username 仍走与真实账号相同的 Verify 方法。
	dummyHash CredentialHash
}

// NewService 校验全部安全依赖和 dummy hash，防止运行时绕过验证路径。
//
// 构造函数只能验证 dummyHash 非零且满足通用编码边界，无法证明其算法和成本。Composition Root
// 必须从 production hasher 的同一配置生成或加载 dummy hash；使用低成本占位值会重新引入
// unknown username timing side channel。构造失败返回固定 validation error，不回显依赖内容。
func NewService(repository AccountRepository, hasher CredentialHasher, sessions SessionIssuer, clock Clock, ids IDGenerator, dummyHash CredentialHash) (*Service, error) {
	if repository == nil || hasher == nil || sessions == nil || clock == nil || ids == nil || !dummyHash.Valid() {
		return nil, newError(ErrorKindValidation, OperationConstruct, CommitPhaseNone, errors.New("account service dependencies are incomplete"))
	}
	return &Service{repository: repository, hasher: hasher, sessions: sessions, clock: clock, ids: ids, dummyHash: dummyHash}, nil
}

// Register 验证输入、提交持久账号，再为 repository 身份创建 session。
//
// 账号提交与 session 创建不使用分布式事务。session 失败时返回 account_created，
// 保留已创建账号供后续 Login 恢复，绝不执行补偿删除或返回未提交 token。
// ctx 取消只限制当前调用等待：repository 返回 account_commit_unknown 时调用方不得立即
// 重试为新身份或宣称账号不存在；重复 register 由 canonical 唯一约束收敛为 conflict。
// Validation、conflict、dependency 和部分成功均通过 ErrorKind/CommitPhase 稳定表达。
func (service *Service) Register(ctx context.Context, command RegisterCommand) (AuthResult, error) {
	username, err := NewUsername(command.username)
	if err != nil {
		return AuthResult{}, newError(ErrorKindValidation, OperationRegister, CommitPhaseNone, nil)
	}
	displayName, err := NewDisplayName(command.displayName)
	if err != nil {
		return AuthResult{}, newError(ErrorKindValidation, OperationRegister, CommitPhaseNone, nil)
	}
	password, err := NewRegisterPassword(command.password)
	if err != nil {
		return AuthResult{}, newError(ErrorKindValidation, OperationRegister, CommitPhaseNone, nil)
	}
	hash, err := service.hasher.Hash(ctx, password)
	if err != nil || !hash.Valid() {
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationRegister, CommitPhaseNone, err)
	}
	accountID, err := service.newAccountID()
	if err != nil {
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationRegister, CommitPhaseNone, err)
	}
	playerID, err := service.newPlayerID()
	if err != nil {
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationRegister, CommitPhaseNone, err)
	}
	account, err := NewAccount(accountID, playerID, username, displayName, StatusActive, service.clock.Now())
	if err != nil {
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationRegister, CommitPhaseNone, err)
	}
	outcome, repositoryErr := service.repository.Create(ctx, CreateRecord{Account: account, Credential: hash})
	if repositoryErr != nil {
		phase := CommitPhaseNone
		if outcome == CreateOutcomeCommitUnknown {
			phase = CommitPhaseUnknown
		}
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationRegister, phase, repositoryErr)
	}
	switch outcome {
	case CreateOutcomeCreated:
		// 后续 session 失败不能改变已经确认的持久事实。
	case CreateOutcomeUsernameConflict:
		return AuthResult{}, newError(ErrorKindUsernameConflict, OperationRegister, CommitPhaseNone, nil)
	case CreateOutcomeCommitUnknown:
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationRegister, CommitPhaseUnknown, nil)
	default:
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationRegister, CommitPhaseNone, errors.New("repository returned invalid create outcome"))
	}
	issued, err := service.issueSession(ctx, account)
	if err != nil {
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationRegister, CommitPhaseAccountCreated, err)
	}
	return AuthResult{Account: account.Summary(), Session: issued}, nil
}

// Login 验证 canonical username 与原始 password，并为 active account 创建新 session。
//
// Unknown username、wrong password 与 inactive status 使用相同外部错误。Unknown username
// 仍调用同一个 Verify 方法和注入的同成本 dummy hash，以降低明显 timing 差异。该措施不保证
// 绝对恒定时间，transport adapter 仍必须执行统一响应、rate limit 和并发预算。每次成功调用
// 创建独立 session；ctx 取消或依赖错误不应被 adapter 映射为 invalid credentials。
func (service *Service) Login(ctx context.Context, command LoginCommand) (AuthResult, error) {
	username, err := NewUsername(command.username)
	if err != nil {
		return AuthResult{}, newError(ErrorKindValidation, OperationLogin, CommitPhaseNone, nil)
	}
	password, err := NewLoginPassword(command.password)
	if err != nil {
		return AuthResult{}, newError(ErrorKindValidation, OperationLogin, CommitPhaseNone, nil)
	}
	record, outcome, repositoryErr := service.repository.FindForAuthentication(ctx, username)
	if repositoryErr != nil {
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationLogin, CommitPhaseNone, repositoryErr)
	}
	if outcome == FindOutcomeNotFound {
		if _, verifyErr := service.hasher.Verify(ctx, service.dummyHash, password); verifyErr != nil {
			return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationLogin, CommitPhaseNone, verifyErr)
		}
		return AuthResult{}, invalidCredentialsError()
	}
	if outcome != FindOutcomeFound || !record.Account.Valid() || !record.Credential.Valid() || record.Account.Username() != username {
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationLogin, CommitPhaseNone, errors.New("repository returned malformed authentication record"))
	}
	matched, verifyErr := service.hasher.Verify(ctx, record.Credential, password)
	if verifyErr != nil {
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationLogin, CommitPhaseNone, verifyErr)
	}
	if !matched || record.Account.Status() != StatusActive {
		return AuthResult{}, invalidCredentialsError()
	}
	issued, err := service.issueSession(ctx, record.Account)
	if err != nil {
		return AuthResult{}, newError(ErrorKindDependencyUnavailable, OperationLogin, CommitPhaseNone, err)
	}
	return AuthResult{Account: record.Account.Summary(), Session: issued}, nil
}

// newAccountID 添加 account namespace，并把无效 generator 输出视为依赖故障。
func (service *Service) newAccountID() (AccountID, error) {
	material, err := service.ids.NewID()
	if err != nil {
		return AccountID{}, err
	}
	return NewAccountID(accountIDPrefix + material)
}

// newPlayerID 添加 player namespace，并与账号 ID 使用独立随机材料。
func (service *Service) newPlayerID() (PlayerID, error) {
	material, err := service.ids.NewID()
	if err != nil {
		return PlayerID{}, err
	}
	return NewPlayerID(playerIDPrefix + material)
}

// issueSession 只从 repository Account 构造 Principal，拒绝 payload 身份注入。
func (service *Service) issueSession(ctx context.Context, account Account) (session.SessionResult, error) {
	principal, err := session.NewPrincipal(account.ID().String(), account.PlayerID().String())
	if err != nil {
		return session.SessionResult{}, err
	}
	return service.sessions.CreateSession(ctx, principal)
}

// invalidCredentialsError 统一所有可枚举认证失败的 kind、消息和 phase。
func invalidCredentialsError() error {
	return newError(ErrorKindInvalidCredentials, OperationLogin, CommitPhaseNone, nil)
}
