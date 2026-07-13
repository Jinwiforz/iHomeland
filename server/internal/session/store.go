package session

import (
	"context"
	"time"
)

// Clock 为 expiry 判断提供可替换时间源，避免领域测试依赖 wall clock。
//
// 同一次安全操作必须只读取一次 Now 并把该快照传给 store，避免跨步骤读取时钟
// 造成 expiry 边界不一致。生产 wall clock 可能被系统校时，调用方不能把它当作
// elapsed-time 计时器；duration 与 deadline 应继续使用 Go 的 duration/context 语义。
type Clock interface {
	// Now 返回当前绝对时间；跨端或持久化 adapter 负责转换为 Unix epoch，测试实现由用例显式推进。
	Now() time.Time
}

// IDGenerator 创建不可由客户端选择的 session identifier。
type IDGenerator interface {
	// NewID 返回不可预测的安全 ASCII 材料；session package 负责添加所属实体前缀并校验。
	NewID() (string, error)
}

// StoreOutcome 表达原子存储操作的安全结果，不泄漏记录内部结构。
type StoreOutcome uint8

const (
	// StoreOutcomeUnspecified 表示 adapter 未返回有效结果。
	StoreOutcomeUnspecified StoreOutcome = iota
	// StoreOutcomeApplied 表示原子操作已经提交。
	StoreOutcomeApplied
	// StoreOutcomeNotFound 合并未知与不可再用记录，避免身份枚举。
	StoreOutcomeNotFound
	// StoreOutcomeExpired 表示记录在操作时已经到期。
	StoreOutcomeExpired
	// StoreOutcomeReplayed 表示一次性凭据已经被消费。
	StoreOutcomeReplayed
	// StoreOutcomeEpochMismatch 表示凭据绑定了旧 session epoch。
	StoreOutcomeEpochMismatch
	// StoreOutcomeInvalidated 表示 session 已永久失效。
	StoreOutcomeInvalidated
	// StoreOutcomeConflict 表示 identifier 或原子前置条件冲突。
	StoreOutcomeConflict
)

// SessionRecord 是 store 保存的最小权威身份事实。
type SessionRecord struct {
	// ID 是 server-generated session identity。
	ID SessionID
	// Principal 是上游账号域已经验证的身份。
	Principal Principal
	// Epoch 是所有 channel 共享的撤销屏障。
	Epoch Epoch
	// Status 决定 session 是否仍可认证。
	Status Status
	// ExpiresAt 是绝对时间，也是 session 与 replay tombstone 的寿命上限。
	ExpiresAt time.Time
}

// TokenRecord 只保存 credential digest 与绑定元数据。
type TokenRecord struct {
	// Digest 是不可恢复原始 secret 的 lookup key。
	Digest Digest
	// SessionID 关联唯一身份 lineage。
	SessionID SessionID
	// Epoch 必须与当前 session epoch 相同。
	Epoch Epoch
	// ExpiresAt 是 token 的绝对失效时间，达到该时刻即视为过期。
	ExpiresAt time.Time
}

// SessionBundle 让创建操作原子写入 session 与首对 token。
type SessionBundle struct {
	// Session 是新的 epoch 1 权威记录。
	Session SessionRecord
	// Access 是首枚短期访问资格。
	Access TokenRecord
	// Refresh 是首枚可轮换资格。
	Refresh TokenRecord
}

// Rotation 描述 refresh compare-and-consume 成功后必须同事务提交的新 token pair。
type Rotation struct {
	// PresentedRefresh 是调用方提交的旧 refresh digest。
	PresentedRefresh Digest
	// Access 是替换上一枚 access 的新记录。
	Access TokenRecord
	// Refresh 是替换旧 refresh 的新记录。
	Refresh TokenRecord
	// Now 是调用方读取一次后传入的绝对时间快照，固定本次原子操作的 expiry 边界。
	Now time.Time
}

// TicketRecord 是一次性 realtime admission 的完整服务端绑定。
type TicketRecord struct {
	// Digest 是唯一可用于消费 lookup 的 ticket nonce 摘要。
	Digest Digest
	// SessionID 与 Epoch 绑定当前认证 lineage。
	SessionID SessionID
	// Epoch 与当前 session epoch 不一致时必须拒绝。
	Epoch Epoch
	// Channel 是 ticket 唯一允许进入的 listener 类型。
	Channel Channel
	// Endpoint 是受信 provider 选择的 listener identity。
	Endpoint Endpoint
	// Scopes 是 policy 固定授予且不可扩展的 capability。
	Scopes ScopeSet
	// ExpiresAt 是绝对时间，限制 ticket 暴露窗口。
	ExpiresAt time.Time
}

// AuthSnapshot 是 store 在同一原子读取中确认的当前身份事实。
type AuthSnapshot struct {
	// Principal 来自当前 session record，而不是 token payload。
	Principal Principal
	// SessionID 与 Epoch 表示认证时读取到的权威版本。
	SessionID SessionID
	// Epoch 是当前有效撤销屏障。
	Epoch Epoch
	// Scopes 仅在 ticket 消费结果中非空。
	Scopes ScopeSet
	// AccessExpiresAt 是 refresh 成功后 store 实际提交的新 access expiry。
	AccessExpiresAt time.Time
	// RefreshExpiresAt 是受 session 总寿命约束后的新 refresh expiry。
	RefreshExpiresAt time.Time
}

// InvalidationReason 是连接边界可安全记录和幂等处理的失效原因。
type InvalidationReason uint8

const (
	// InvalidationReasonUnspecified 禁止发布没有安全语义的通知。
	InvalidationReasonUnspecified InvalidationReason = iota
	// InvalidationReasonLogout 表示当前 session 主动退出。
	InvalidationReasonLogout
	// InvalidationReasonForcedLogout 表示受信管理边界撤销单一 session。
	InvalidationReasonForcedLogout
	// InvalidationReasonPrincipalBan 表示账号域要求撤销 principal 的现有 sessions。
	InvalidationReasonPrincipalBan
	// InvalidationReasonRefreshReplay 表示 refresh tombstone 被再次提交。
	InvalidationReasonRefreshReplay
)

// Invalidation 是 store 已提交后通知连接边界的幂等事实。
type Invalidation struct {
	// SessionID 标识需要关闭旧连接的 lineage。
	SessionID SessionID
	// Epoch 是提交后的新屏障，旧连接必须小于该值。
	Epoch Epoch
	// Reason 是低基数且不含敏感输入的原因。
	Reason InvalidationReason
}

// SessionStore 只暴露 session 安全模型要求原子的操作。
//
// 实现不得把这些方法拆成外部可观察的通用 CRUD 序列。ctx 只限制调用方等待；
// 对远程 store 而言，deadline 与依赖错误不能被解释为“肯定没有提交”。Service
// 在错误时不会返回新 credential，adapter 必须使用单个事务或原子脚本，并使重试
// 收敛到 conflict、replay 或相同 invalidation，不能恢复已经撤销的资格。
type SessionStore interface {
	// Create 以 all-or-nothing 方式写入 session 与首对 token；重复 identifier/digest 返回 conflict。
	Create(ctx context.Context, bundle SessionBundle) (StoreOutcome, error)
	// ResolveAccess 在同一一致性读取中校验 access、session status、epoch 与 expiry，且不延长寿命。
	ResolveAccess(ctx context.Context, digest Digest, now time.Time) (AuthSnapshot, StoreOutcome, error)
	// RotateRefresh 原子消费旧 refresh、撤销旧 access 并写入新 token pair。
	//
	// 成功时实际 expiry 必须截断到 session expiry 并通过 snapshot 返回；已消费 digest
	// 必须保留 tombstone 并返回 replay invalidation，不能重新签发或恢复上一枚 access。
	RotateRefresh(ctx context.Context, rotation Rotation) (AuthSnapshot, Invalidation, StoreOutcome, error)
	// IssueTicket 在当前 session/epoch 仍有效时保存 ticket；校验失败不得留下可消费记录。
	IssueTicket(ctx context.Context, record TicketRecord, now time.Time) (StoreOutcome, error)
	// ConsumeTicket 原子校验全部绑定并至多消费一次。
	//
	// channel、endpoint、expiry 或 epoch 校验失败时不得标记 consumed；只有完整成功的
	// listener 可以取得 AuthSnapshot，随后重复提交必须稳定返回 replay。
	ConsumeTicket(ctx context.Context, digest Digest, channel Channel, endpoint Endpoint, now time.Time) (AuthSnapshot, StoreOutcome, error)
	// InvalidateSession 原子递增 epoch 并撤销旧资格；重复调用返回同一 invalidation 而不重复递增。
	InvalidateSession(ctx context.Context, id SessionID, reason InvalidationReason) (Invalidation, StoreOutcome, error)
	// InvalidatePrincipal 以 all-or-nothing 方式撤销 principal 当前拥有的全部 sessions。
	//
	// 依赖错误可能发生在原子提交之后，重试必须返回各 session 已存在的同一 invalidation，
	// 不能再次递增 epoch，也不能只撤销部分 sessions。
	InvalidatePrincipal(ctx context.Context, principal Principal, reason InvalidationReason) ([]Invalidation, error)
}

// EndpointProvider 返回运行环境已经验证的 realtime listener identity。
type EndpointProvider interface {
	// EndpointFor 返回唯一 channel 的受信 endpoint，不接受客户端 host 或 port；失败时不得回退默认地址。
	EndpointFor(ctx context.Context, channel Channel) (Endpoint, error)
}

// ConnectionInvalidator 接收已经提交的失效事实，不持有或返回 socket。
type ConnectionInvalidator interface {
	// Invalidate 幂等通知连接 registry 关闭低于指定 epoch 的连接。
	//
	// 调用发生在 store 提交之后；返回错误只表示连接清理仍需重试，不能要求调用方
	// 回滚 epoch。实现不得在重试时关闭等于或高于 invalidation.Epoch 的新连接。
	Invalidate(ctx context.Context, invalidation Invalidation) error
}
