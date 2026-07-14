package visitsession

import storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"

// VisitSession Redis definitions的稳定名称、schema版本与编码预算。
const (
	// activeDefinitionName 是 PersonalWorld 到唯一 active VisitSession 的索引定义。
	activeDefinitionName = "visitsession_active"
	// sessionDefinitionName 是完整 current 或 terminal snapshot 的定义。
	sessionDefinitionName = "visitsession_session"
	// commandDefinitionName 是首次 create/mutation 完整结果的重放定义。
	commandDefinitionName = "visitsession_command"
	// redisSchemaVersion 固定当前 Hash metadata 与 JSON payload schema。
	redisSchemaVersion uint16 = 1
	// maximumActiveBytes 约束 active Hash 的累计 field/value 字节。
	maximumActiveBytes = 1024
	// maximumSessionBytes 约束含metadata、最多96个邀请和32个membership的完整Hash。
	maximumSessionBytes = 128 * 1024
	// maximumCommandBytes 约束含metadata、snapshot与完整safe-return/replay result的Hash。
	maximumCommandBytes = 192 * 1024
)

// Definitions 返回 VisitSession owner 的不可变 Redis registry metadata 副本。
//
// 三类key共享session absolute expiry加replay retention的物理过期边界；physical TTL只清理
// 可失效运行态，不能替代application的deadline command或safe-return side effect。
func Definitions() []storageredis.Definition {
	return []storageredis.Definition{
		activeDefinition(),
		sessionDefinition(),
		commandDefinition(),
	}
}

// activeDefinition 返回 PersonalWorld 活动访客会话索引的治理元数据。
func activeDefinition() storageredis.Definition {
	return storageredis.Definition{
		Name: activeDefinitionName, Owner: "visitsession", Kind: "active", Purpose: "个人世界当前访客会话索引",
		TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: maximumActiveBytes,
		Recovery: "进程重启只读取Redis仍保留的合法值；丢失后旧访问资格失效", Cleanup: "terminal transition主动删除，否则session到期加重放保留期后自然删除",
		Failure: "unknown/corrupt/oversized Hash或缺失TTL fail closed，不覆盖潜在有效索引", MetricsName: "visitsession_active",
	}
}

// sessionDefinition 返回完整 current/terminal snapshot 的治理元数据。
func sessionDefinition() storageredis.Definition {
	return storageredis.Definition{
		Name: sessionDefinitionName, Owner: "visitsession", Kind: "session", Purpose: "访客会话完整运行快照",
		TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: maximumSessionBytes,
		Recovery: "进程重启只读取Redis仍保留的合法值；不从MySQL或memory补回", Cleanup: "session到期加重放保留期后自然删除",
		Failure: "unknown/corrupt/oversized Hash或payload fail closed", MetricsName: "visitsession_session",
	}
}

// commandDefinition 返回首次 create/mutation 完整结果的治理元数据。
func commandDefinition() storageredis.Definition {
	return storageredis.Definition{
		Name: commandDefinitionName, Owner: "visitsession", Kind: "command", Purpose: "访客会话命令完整重放结果",
		TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: maximumCommandBytes,
		Recovery: "进程重启只读取Redis仍保留的合法值；丢失后禁止猜测首次结果", Cleanup: "所属session到期加重放保留期后自然删除",
		Failure: "unknown/corrupt/oversized result或fingerprint不一致fail closed", MetricsName: "visitsession_command",
	}
}
