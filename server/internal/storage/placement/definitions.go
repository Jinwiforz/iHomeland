package placement

import storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"

const (
	// assignmentDefinitionName 是 current assignment key 的 registry identity。
	assignmentDefinitionName = "placement_assignment"
	// transitionDefinitionName 是有界 mutation replay key 的 registry identity。
	transitionDefinitionName = "placement_transition"
	// redisSchemaVersion 固定当前 Hash field schema；未知版本必须 fail closed。
	redisSchemaVersion uint16 = 1
	// maximumAssignmentBytes 是 current Hash 所有 field/value 的累计预算。
	maximumAssignmentBytes = 2048
	// maximumTransitionBytes 是 replay Hash 所有 field/value 的累计预算。
	maximumTransitionBytes = 3072
)

// Definitions 返回 placement owner 的不可变 Redis registry metadata 值副本。
//
// 调用方在创建共享 Keyspace 前合并各 owner definitions；本 package 不创建第二个 registry
// 或 client。Assignment 与 replay 都必须有 TTL，Redis flush 后不恢复旧 lease。
func Definitions() []storageredis.Definition {
	return []storageredis.Definition{
		assignmentDefinition(),
		transitionDefinition(),
	}
}

// assignmentDefinition 返回 current assignment 的 registry metadata 值。
func assignmentDefinition() storageredis.Definition {
	return storageredis.Definition{
		Name: assignmentDefinitionName, Owner: "placement", Kind: "assignment",
		Purpose:   "个人世界当前实例放置",
		TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: maximumAssignmentBytes,
		Recovery:    "不恢复旧 assignment；新 candidate 必须从 MySQL allocation 获得更高 fence",
		Cleanup:     "lease absolute expiry 或 owner revoke/replace 删除",
		Failure:     "unknown/corrupt Hash 或缺失 TTL fail closed，禁止恢复写资格",
		MetricsName: "placement_assignment",
	}
}

// transitionDefinition 返回有界 mutation replay 的 registry metadata 值。
func transitionDefinition() storageredis.Definition {
	return storageredis.Definition{
		Name: transitionDefinitionName, Owner: "placement", Kind: "transition",
		Purpose:   "实例放置变更重放证据",
		TTLPolicy: storageredis.TTLRequired, SchemaVersion: redisSchemaVersion, MaxEncodedBytes: maximumTransitionBytes,
		Recovery:    "不恢复；证据丢失后既有 allocation 保持 commit-unknown",
		Cleanup:     "有界 retry window 到期自然删除",
		Failure:     "unknown/corrupt replay 或缺失 TTL fail closed，不猜测历史成功",
		MetricsName: "placement_transition",
	}
}
