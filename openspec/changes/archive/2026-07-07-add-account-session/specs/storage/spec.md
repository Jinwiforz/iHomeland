## ADDED Requirements

### Requirement: Storage 必须提供账号会话持久化边界
MUST:服务端账号会话 runtime 必须通过真实 MySQL `PlayerProfileRepository` 和真实 Redis `AccountSessionCache` 访问账号资料与 session token；fake/in-memory storage 只能用于测试，不得作为 server runtime 默认依赖。

#### Scenario: account service 查询玩家资料
- **WHEN** account service 需要根据账号名查询第一阶段玩家资料
- **THEN** 它必须通过 storage repository 接口访问 MySQL 持久事实

#### Scenario: account service 创建玩家资料
- **WHEN** account service 注册新账号
- **THEN** 它必须通过 MySQL repository 创建玩家资料，并将同一账号名唯一索引冲突映射为稳定冲突错误

#### Scenario: account service 写入 session token
- **WHEN** account service 登录成功并签发 session token
- **THEN** 它必须通过 Redis cache 写入短期 session，并设置与 session 过期时间一致的 TTL

#### Scenario: runtime wiring 尝试使用 fake storage
- **WHEN** server runtime 组装账号 service 依赖
- **THEN** 它不得创建或注入 `storage.NewFakeStore()` 作为账号资料或账号 session 的 runtime 依赖

### Requirement: 第一阶段 MySQL 必须保存最小玩家基础资料
MUST:第一阶段 MySQL 必须保存账号会话所需的最小玩家基础资料和密码哈希，但不得扩展为完整账号安全、社交、背包或经济系统。

#### Scenario: 保存玩家基础资料
- **WHEN** 玩家首次注册合法账号
- **THEN** MySQL 必须保存 `player_id`、账号名、密码哈希、显示名、创建时间和更新时间等最小字段

#### Scenario: 账号名不存在
- **WHEN** account service 通过账号名读取玩家资料且 MySQL 中无记录
- **THEN** repository 必须返回稳定 not found 错误，不得创建隐式账号

#### Scenario: 需求要求完整账号安全
- **WHEN** 新需求需要密码找回、多因素认证或第三方登录
- **THEN** 必须创建单独 OpenSpec change，不得混入账号会话持久化边界

### Requirement: Redis 必须保存短期账号 session
MUST:Redis 必须保存短期账号 session token，并为每个 key 定义 owner、TTL、value、重建来源和清理触发。

#### Scenario: 写入账号 session key
- **WHEN** 登录成功签发 session token
- **THEN** Redis 写入 owner 为 `account` 的 session key，TTL 与会话过期时间一致

#### Scenario: 读取账号 session key
- **WHEN** 客户端提交有效 session token 恢复会话
- **THEN** Redis cache 必须解码 session value 并返回 player id、账号名、签发时间、过期时间和连接信息

#### Scenario: Redis session 丢失
- **WHEN** Redis 中账号 session key 丢失或过期
- **THEN** 玩家基础资料仍保存在 MySQL，客户端必须重新登录或重新获取有效 session
