package battleticket

import storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"

const (
	// issueDefinitionName 是 BattleTicket issuance replay key 的 registry identity。
	issueDefinitionName = "battleticket_issue"
	// redisSchemaVersion 固定当前 Hash 字段布局。
	redisSchemaVersion uint16 = 1
	// maximumIssueBytes 约束完整非秘密 binding 与 digest 累计 field/value bytes。
	maximumIssueBytes = 8 * 1024
)

// Definitions 返回 battleticket owner 的不可变 Redis 治理 metadata。
func Definitions() []storageredis.Definition {
	return []storageredis.Definition{{
		Name:            issueDefinitionName,
		Owner:           "battleticket",
		Kind:            "issue",
		Purpose:         "BattleTicket签发、安装前幂等与响应丢失重放",
		TTLPolicy:       storageredis.TTLRequired,
		SchemaVersion:   redisSchemaVersion,
		MaxEncodedBytes: maximumIssueBytes,
		Recovery:        "进程重启只读取Redis仍保留的合法digest/handle；flush后旧资格不恢复",
		Cleanup:         "ticket业务到期加有界replay retention后自然删除",
		Failure:         "unknown/corrupt/oversized Hash、缺失TTL或binding/digest矛盾全部fail closed",
		MetricsName:     "battleticket_issue",
	}}
}
