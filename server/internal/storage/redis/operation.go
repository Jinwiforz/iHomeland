package redis

import (
	"errors"

	redisclient "github.com/redis/go-redis/v9"
)

// OperationKind 区分只读查询、单一 built-in command 与 owner-defined 复合 mutation。
type OperationKind string

const (
	// OperationReadOnly 表示可由 owner 使用有界策略重试的无副作用查询。
	OperationReadOnly OperationKind = "read_only"
	// OperationAtomicCommand 表示单个 Redis built-in command 完成的原子状态变更。
	OperationAtomicCommand OperationKind = "atomic_command"
	// OperationAtomicMutation 表示由 owner-defined Lua/transaction 完成的复合状态变更。
	// Redis 不回滚 script/transaction 中运行时错误前已完成的写入，因此 generic error 不能证明未应用。
	OperationAtomicMutation OperationKind = "atomic_mutation"
)

// CommandOutcome 描述 Redis command/script 失败时可证明的 mutation 边界。
type CommandOutcome string

const (
	// CommandReadFailed 表示只读查询失败，不涉及 commit 状态。
	CommandReadFailed CommandOutcome = "read_failed"
	// CommandNotApplied 表示单个 built-in command 被 Redis 明确拒绝，状态未应用。
	CommandNotApplied CommandOutcome = "not_applied"
	// CommandCommitUnknown 表示 mutation 发出后无法证明最终状态，禁止盲目重放。
	CommandCommitUnknown CommandOutcome = "commit_unknown"
)

// ClassifyCommandError 根据 operation kind 保守分类非 nil 失败，不决定 owner 是否重试。
//
// 只有单个 built-in command 的 Redis server rejection 能收窄为 not-applied。Lua/transaction
// 即使返回 server error 也可能已写入部分状态，必须保持 commit-unknown，直到 owner-specific
// outcome parser 使用稳定 identity 解析结果。
func ClassifyCommandError(kind OperationKind, err error) CommandOutcome {
	if kind == OperationReadOnly {
		return CommandReadFailed
	}
	if kind == OperationAtomicCommand {
		var redisError redisclient.Error
		if errors.As(err, &redisError) {
			return CommandNotApplied
		}
	}
	return CommandCommitUnknown
}
