// Package storage 组合跨owner共享的基础设施治理定义，不拥有client或业务service。
package storage

import (
	storageplacement "github.com/jinwiforz/ihomeland/server/internal/storage/placement"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	storagesession "github.com/jinwiforz/ihomeland/server/internal/storage/session"
	storagevisit "github.com/jinwiforz/ihomeland/server/internal/storage/visitsession"
)

// RedisDefinitions 合并全部已实现owner的不可变definition值副本。
//
// 该函数只治理key metadata；typed codec、Lua、TTL与恢复仍由各消费adapter独占。
func RedisDefinitions() []storageredis.Definition {
	sessionDefinitions := storagesession.Definitions()
	placementDefinitions := storageplacement.Definitions()
	visitDefinitions := storagevisit.Definitions()
	definitions := make([]storageredis.Definition, 0, len(sessionDefinitions)+len(placementDefinitions)+len(visitDefinitions))
	definitions = append(definitions, sessionDefinitions...)
	definitions = append(definitions, placementDefinitions...)
	definitions = append(definitions, visitDefinitions...)
	return definitions
}

// NewRedisKeyspace 为已验证environment构造全部owner共享的安全key builder。
//
// 构造不连接Redis；重复definition、缺失治理字段或非法environment会在任何业务写入前失败。
func NewRedisKeyspace(environment string) (*storageredis.Keyspace, error) {
	registry, err := storageredis.NewRegistry(RedisDefinitions())
	if err != nil {
		return nil, err
	}
	return storageredis.NewKeyspace(environment, registry)
}
