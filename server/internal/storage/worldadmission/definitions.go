package worldadmission

import storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"

const (
	// issueDefinitionName 是 issuance replay key 的 registry identity。
	issueDefinitionName = "worldadmission_issue"
	// credentialDefinitionName 是一次性 binding key 的 registry identity。
	credentialDefinitionName = "worldadmission_credential"
	// redisSchemaVersion 固定当前 Hash 字段布局。
	redisSchemaVersion uint16 = 2
	// maximumIssueBytes 约束 issue Hash 累计 field/value 字节。
	maximumIssueBytes = 1024
	// maximumCredentialBytes 约束完整 binding/tombstone Hash 累计 field/value 字节。
	maximumCredentialBytes = 8 * 1024
)

// Definitions 返回 worldadmission owner 的不可变 Redis 治理 metadata。
func Definitions() []storageredis.Definition {
	return []storageredis.Definition{
		{
			Name:            issueDefinitionName,
			Owner:           "worldadmission",
			Kind:            "issue",
			Purpose:         "世界准入签发重放",
			TTLPolicy:       storageredis.TTLRequired,
			SchemaVersion:   redisSchemaVersion,
			MaxEncodedBytes: maximumIssueBytes,
			Recovery:        "进程重启只读取 Redis 仍保留的合法值；丢失后使用新签发 identity",
			Cleanup:         "credential 到期加有界重放保留期后自然删除",
			Failure:         "unknown/corrupt/oversized Hash 或缺失 TTL fail closed",
			MetricsName:     "worldadmission_issue",
		},
		{
			Name:            credentialDefinitionName,
			Owner:           "worldadmission",
			Kind:            "credential",
			Purpose:         "世界准入一次性绑定",
			TTLPolicy:       storageredis.TTLRequired,
			SchemaVersion:   redisSchemaVersion,
			MaxEncodedBytes: maximumCredentialBytes,
			Recovery:        "进程重启只读取 Redis 仍保留的合法值；flush 后旧 credential 永久失效",
			Cleanup:         "业务到期后仅保留有界 consume 重放证据并自然删除",
			Failure:         "unknown/corrupt/oversized Hash 或 binding 矛盾 fail closed",
			MetricsName:     "worldadmission_credential",
		},
	}
}
