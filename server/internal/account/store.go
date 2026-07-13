package account

import (
	"context"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// Clock 为账号 created time 提供可替换的绝对时间源。
//
// 生产实现复用 Composition Root 的 SystemClock；package 不创建第二套 wall clock，确保
// account 与 session 在同一进程使用一致时间来源。
type Clock interface {
	// Now 返回当前绝对时间；一次注册只能读取一次并持久化同一快照。
	Now() time.Time
}

// IDGenerator 创建不可由客户端预测或选择的身份材料。
//
// 生产实现必须来自 Composition Root 的 CSPRNG。每个 account/player ID 分别调用一次，
// 禁止从 username、时间或数据库自增值推导公开身份。
type IDGenerator interface {
	// NewID 返回安全 ASCII 随机材料；account package 负责添加实体前缀。
	NewID() (string, error)
}

// CredentialHasher 隔离具体 password algorithm、参数和 hash 格式。
//
// 实现必须使用自描述 memory-hard hash 和随机 salt，并确保 dummy hash 与真实 hash 使用相同
// 算法及成本参数。方法可以并发调用；实现若持有可变状态必须自行同步。错误不得包含 password、
// 完整 hash、salt 或 pepper，context 取消只停止调用方等待，不能泄漏中间 credential。
type CredentialHasher interface {
	// Hash 为注册密码生成带随机 salt 的自描述 hash；失败时必须返回零值且不得持久化 plaintext。
	Hash(ctx context.Context, password RegisterPassword) (CredentialHash, error)
	// Verify 使用 hash 自描述参数验证原始登录密码；false 表示不匹配，error 只表示依赖或格式故障。
	Verify(ctx context.Context, hash CredentialHash, password LoginPassword) (bool, error)
}

// CreateOutcome 表达原子账号创建的确定性或明确不确定结果。
type CreateOutcome uint8

const (
	// CreateOutcomeUnspecified 表示 adapter 没有遵守 repository contract。
	CreateOutcomeUnspecified CreateOutcome = iota
	// CreateOutcomeCreated 表示 account、player 与 credential 已全部提交。
	CreateOutcomeCreated
	// CreateOutcomeUsernameConflict 表示 canonical username 已存在且没有新建记录。
	CreateOutcomeUsernameConflict
	// CreateOutcomeNotCommitted 表示依赖失败发生在确定没有提交的阶段。
	CreateOutcomeNotCommitted
	// CreateOutcomeCommitUnknown 表示调用方不能确认事务是否已经提交。
	CreateOutcomeCommitUnknown
)

// FindOutcome 表达认证读取是否找到完整记录。
type FindOutcome uint8

const (
	// FindOutcomeUnspecified 表示 adapter 没有返回有效结果。
	FindOutcomeUnspecified FindOutcome = iota
	// FindOutcomeFound 表示返回了完整认证记录。
	FindOutcomeFound
	// FindOutcomeNotFound 合并所有 username 不存在情况。
	FindOutcomeNotFound
)

// CreateRecord 是 repository 必须在同一事务提交的完整注册事实。
type CreateRecord struct {
	// Account 包含 account/player identity、canonical username、状态与创建时间。
	Account Account
	// Credential 是 hasher 生成的自描述 hash，绝不能替换为 plaintext。
	Credential CredentialHash
}

// AuthenticationRecord 是 login 验证所需且不对外公开的最小事实。
type AuthenticationRecord struct {
	// Account 提供服务端身份、状态与公开 summary 来源。
	Account Account
	// Credential 只交给 CredentialHasher.Verify。
	Credential CredentialHash
}

// AccountRepository 只暴露账号注册与认证真正需要的原子操作。
//
// 实现必须允许并发调用，并由数据库唯一约束线性化同一 canonical username。ctx deadline
// 只限制调用方等待，不能被解释为事务必定回滚；无法确认提交结果时必须返回
// CreateOutcomeCommitUnknown，使 application 不会谎报失败或执行跨存储补偿删除。
type AccountRepository interface {
	// Create 原子写入 account、player 与 credential；成功、唯一冲突和提交不确定性必须使用对应 outcome。
	Create(ctx context.Context, record CreateRecord) (CreateOutcome, error)
	// FindForAuthentication 按 canonical username 读取同一一致性快照；依赖错误不得伪装为 not found。
	FindForAuthentication(ctx context.Context, username Username) (AuthenticationRecord, FindOutcome, error)
}

// SessionIssuer 是 account application 对 Session Core 的最窄消费接口。
//
// 实际实现必须是 internal/session.Service；account 不包装 refresh、logout、ticket 或 token
// parsing。CreateSession 失败不改变已经提交的账号事实。
type SessionIssuer interface {
	// CreateSession 只接受 repository 身份构造的 Principal；错误时不得返回可用 token 或伪造成功结果。
	CreateSession(ctx context.Context, principal session.Principal) (session.SessionResult, error)
}
