package session

import storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"

const (
	// sessionRecordDefinitionName 是权威 session/epoch key 的 registry identity。
	sessionRecordDefinitionName = "session_record"
	// sessionAccessDefinitionName 是 access digest lookup key 的 registry identity。
	sessionAccessDefinitionName = "session_access"
	// sessionRefreshDefinitionName 是 active refresh/replay tombstone key 的 registry identity。
	sessionRefreshDefinitionName = "session_refresh"
	// sessionTicketDefinitionName 是一次性 connection ticket key 的 registry identity。
	sessionTicketDefinitionName = "session_ticket"
	// sessionPrincipalDefinitionName 是 principal sessions 失效索引的 registry identity。
	sessionPrincipalDefinitionName = "session_principal"
	// redisSchemaVersion 固定当前 Hash fields；unknown version 必须 fail closed。
	redisSchemaVersion uint16 = 1
	// maximumPrincipalSessions 限制单个 all-or-nothing invalidation script 的工作量。
	maximumPrincipalSessions = 64
)

// Definitions 返回 Session owner 的不可变 Redis registry metadata 值副本。
func Definitions() []storageredis.Definition {
	return []storageredis.Definition{
		{Name: sessionRecordDefinitionName, Owner: "session", Kind: "record", Purpose: "会话身份状态",
			TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: 2048,
			Recovery: "不恢复；丢失后全部旧凭据失效并要求重新登录", Cleanup: "会话绝对到期时间自然删除",
			Failure: "unknown/corrupt Hash 或缺失 TTL fail closed", MetricsName: "session_record"},
		{Name: sessionAccessDefinitionName, Owner: "session", Kind: "access", Purpose: "访问凭据摘要索引",
			TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: 1024,
			Recovery: "不恢复；丢失后 access 认证失败", Cleanup: "access 绝对到期或 refresh rotation 删除",
			Failure: "unknown/corrupt Hash 或缺失 TTL fail closed", MetricsName: "session_access"},
		{Name: sessionRefreshDefinitionName, Owner: "session", Kind: "refresh", Purpose: "刷新凭据与重放墓碑",
			TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: 2048,
			Recovery: "不恢复；丢失后 refresh 失败并要求重新登录", Cleanup: "active refresh 到期；consumed 墓碑保留到 session 到期",
			Failure: "unknown/corrupt Hash 或缺失 TTL fail closed", MetricsName: "session_refresh"},
		{Name: sessionTicketDefinitionName, Owner: "session", Kind: "ticket", Purpose: "一次性连接票据绑定",
			TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: 2048,
			Recovery: "不恢复；丢失后 ticket handshake 失败", Cleanup: "ticket 绝对到期时间自然删除",
			Failure: "unknown/corrupt Hash 或缺失 TTL fail closed", MetricsName: "session_ticket"},
		{Name: sessionPrincipalDefinitionName, Owner: "session", Kind: "principal", Purpose: "玩家会话失效索引",
			TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: 16384,
			Recovery: "不恢复；丢失后既有凭据仍由 session record 校验，principal revoke fail closed", Cleanup: "最晚 session 到期时间自然删除",
			Failure: "unknown/corrupt/oversized index fail closed，禁止部分撤销", MetricsName: "session_principal"},
	}
}
