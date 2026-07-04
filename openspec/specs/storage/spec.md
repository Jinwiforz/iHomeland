# Storage 规格

## Purpose

定义 MySQL 持久事实、Redis 短期运行态、存储接口、key 规则和恢复边界的长期行为契约，确保数据职责不混淆。

## Requirements

### Requirement: MySQL 是持久事实来源
MUST:需要跨进程、跨 Redis 丢失后仍然存在的数据必须存储在 MySQL 或后续明确的持久化系统中。

#### Scenario: Redis 数据丢失
- **WHEN** Redis 中的运行态数据被清空
- **THEN** 玩家进度、房间摘要和对局摘要等持久数据仍可从 MySQL 恢复

### Requirement: Storage 必须提供房间大厅持久化边界
MUST:服务端必须通过 storage interface 为房间大厅提供持久化和运行态缓存边界，业务状态机不得直接依赖 Redis 或 MySQL 客户端。

#### Scenario: room service 保存房间摘要
- **WHEN** room service 需要记录房间摘要
- **THEN** 它必须通过 storage repository 接口写入，而不是直接调用 MySQL client

#### Scenario: room 状态机单元测试
- **WHEN** 测试房间状态机和业务规则
- **THEN** 测试必须可以使用 fake 或 in-memory storage adapter，不依赖真实 Redis/MySQL

### Requirement: MySQL schema 变更必须通过迁移管理
MUST:所有 MySQL schema 变更必须以迁移文件表达，并记录兼容策略和回滚路径。

#### Scenario: 新增房间摘要表
- **WHEN** 服务端需要保存房间摘要
- **THEN** 必须新增迁移文件创建表，并说明字段语义、owner 和回滚方式

#### Scenario: 修改已发布字段
- **WHEN** 需要修改已发布 MySQL 字段语义或约束
- **THEN** 必须提供兼容读写策略，不得直接破坏旧数据读取

### Requirement: 第一阶段 MySQL 只保存最小持久事实
MUST:第一阶段 MySQL 只能保存房间大厅需要的最小持久事实或摘要，不得扩展为完整账号、背包、经济、战绩系统。

#### Scenario: 保存房间摘要
- **WHEN** 房间创建、关闭或需要恢复摘要信息
- **THEN** MySQL 可以保存 room id、房间名称、房主、成员数量、状态和时间戳等摘要字段

#### Scenario: 需求要求完整经济系统
- **WHEN** 新需求需要背包、经济或完整战绩
- **THEN** 必须创建单独 OpenSpec change，不得混入 persistence boundaries

### Requirement: Redis 只保存短期运行态数据
MUST:Redis 必须用于 session、presence、room index、reconnect token、queue、lock、rate limit 等短期运行态数据。

#### Scenario: 新增 Redis key
- **WHEN** 新增 Redis key
- **THEN** 文档必须记录 owner、用途、TTL 或重建路径

### Requirement: Redis key 必须有 owner、TTL 或重建路径
MUST:所有新增 Redis key 必须记录 owner、用途、TTL、value 结构、重建来源和清理触发条件。

#### Scenario: 新增重连 token key
- **WHEN** 服务端新增 `room:reconnect` Redis key
- **THEN** 文档必须记录 owner 为 `room`、较短 TTL、value 结构、重建来源和过期清理策略

#### Scenario: 新增 room index key
- **WHEN** 服务端新增 `room:index` Redis key
- **THEN** 文档必须记录它可以从 room service 或 MySQL 房间摘要重建

### Requirement: Redis 不得成为持久事实来源
MUST:Redis 只能保存 session、presence、room index、reconnect token、lock、rate limit 等短期运行态数据。

#### Scenario: Redis 数据丢失
- **WHEN** Redis 中的 session、presence 或 room index 被清空
- **THEN** 服务端必须能从连接状态、room service 或 MySQL 摘要重建可恢复状态，不能丢失持久事实

#### Scenario: 保存玩家进度
- **WHEN** 需要保存跨进程和跨 Redis 丢失后仍需存在的玩家进度
- **THEN** 数据必须写入 MySQL 或后续明确的持久化系统，不能只写 Redis

### Requirement: 可重试写入必须幂等
MUST:可能被重试的持久化写入必须具备幂等保护。

#### Scenario: 对局摘要重复提交
- **WHEN** 对局摘要提交因瞬时错误被重试
- **THEN** 存储层只产生一份有效记录

#### Scenario: 房间摘要重复写入
- **WHEN** 保存房间摘要因瞬时错误被重试
- **THEN** storage 层必须通过稳定 room id 或幂等键保证最终只有一份有效摘要

#### Scenario: 重连 token 重复写入
- **WHEN** 同一成员断线事件被重复处理
- **THEN** Redis reconnect token 写入必须覆盖或刷新同一 key，而不是生成多个互相冲突的资格

### Requirement: 本地验证必须区分依赖可达和业务可恢复
MUST:本地验证必须能检查 MySQL/Redis 是否可连接，但业务恢复能力必须通过 repository/cache 测试或明确的集成测试验证。

#### Scenario: 运行 readyz
- **WHEN** `/readyz` 检查 MySQL 和 Redis 可连接
- **THEN** 它只表示依赖可达，不等同于业务持久化恢复已通过

#### Scenario: 验证 storage adapter
- **WHEN** 开发者运行 storage 相关测试
- **THEN** 测试必须覆盖 key 构造、TTL、幂等写入和基础 repository 行为
