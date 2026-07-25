# Server Session 规格

## Purpose

定义服务端统一 session、opaque token、一次性 connection ticket、AuthContext 与全通道 epoch 失效行为。

## Requirements

### Requirement: Session 必须是所有通道的唯一身份事实
服务端 MUST 使用服务端生成的 session id、从 1 开始且单调递增的 session epoch、经过上游验证的 principal、状态和 expiry 表达认证 session。Access token、refresh token、connection ticket 与 connection auth context MUST 绑定同一 session id/epoch；任何 payload 字段不得覆盖该身份。

#### Scenario: 创建新的认证 session
- **WHEN** 上游账号 application service 提交经过验证的 principal 创建 session
- **THEN** session core 生成新的 session id、epoch 1 和有界 expiry，并返回不包含存储实现或网络对象的结果

#### Scenario: Payload 声明不同操作者
- **WHEN** application service 收到 AuthContext 与 payload 中不一致的 account、player 或 session 标识
- **THEN** 授权身份仍只来自 AuthContext，payload 标识不能扩大权限或替换 actor

### Requirement: Opaque token 必须高熵、可轮换且不可从存储恢复
Access token 与 refresh token MUST 使用 CSPRNG 生成的带类型 opaque secret，持久边界 MUST 只保存固定长度 digest 和必要元数据。Access 验证 MUST 同时检查 token expiry、session 状态和当前 epoch；refresh MUST 通过单一原子操作消费旧 token 并生成新 pair，且轮换后的 token expiry MUST NOT 超过原 session expiry。

#### Scenario: Access token 已过期
- **WHEN** access token digest 存在但当前时间达到或超过其 expiry
- **THEN** 验证返回稳定 unauthenticated/expired 结果，不创建 AuthContext 且不延长 token 生命周期

#### Scenario: 两个请求并发刷新同一 token
- **WHEN** 两个 refresh 操作并发提交同一未消费 refresh token
- **THEN** 最多一个操作成功撤销上一枚 access token并写入新 token pair，另一个被识别为 replay 且任何旧 token 不会恢复有效

#### Scenario: 凭据进入日志或格式化
- **WHEN** token/ticket 被错误地作为日志属性、错误参数或格式化值传入
- **THEN** session core 只暴露脱敏摘要或固定占位符，输出不得包含可用于认证的原始 secret

### Requirement: Refresh replay 必须撤销对应 session lineage
Session store MUST 为已轮换 refresh digest 保留有界 tombstone，至少覆盖该 session 的剩余有效期。已消费 refresh token 再次出现时，session core MUST 原子递增 session epoch、撤销旧 token/ticket，并产生可通知连接边界的安全失效结果。

#### Scenario: 已轮换 refresh token 被重放
- **WHEN** 客户端提交能够关联到现有 session 的 consumed refresh digest
- **THEN** session epoch 单调递增，旧 access、refresh、ticket 和 connection context 全部失去授权，响应不泄漏 session 或账号是否存在

### Requirement: Connection ticket 必须短期、单通道且只能消费一次
Connection ticket MUST 绑定 session id/epoch、受信 endpoint、唯一 target channel、由服务端 policy 授予的 scopes、16-byte one-time nonce、issued time 和绝对 expiry。只有 HTTPS access AuthContext 可以签发 ticket；签发方 MUST NOT 接受客户端提供的 scope、host 或 port。WSS ticket MUST 只授予 control scope，TLS/TCP ticket MUST 只授予 gameplay scope；gameplay scope 只允许建立通用可靠业务连接，不能替代具体 admission 或业务授权。Session core MUST 返回不依赖 generated type 的完整领域投影供 adapter 转换既有协议；消费方 MUST 使用 nonce digest 原子 compare-and-consume，并校验当前 session epoch、listener channel 与 endpoint identity。

#### Scenario: 为 WSS 签发 ticket
- **WHEN** 有效 AuthContext 请求 WSS ticket
- **THEN** 服务端从受信 endpoint provider 选择 WSS endpoint，只授予 control scope，并返回不包含其他 channel 权限的短期 ticket

#### Scenario: 为 TLS/TCP 签发 ticket
- **WHEN** 有效 HTTPS AuthContext 请求 TLS/TCP ticket
- **THEN** 服务端从受信 endpoint provider 选择 TLS/TCP endpoint，只授予 gameplay scope，后续 world/visit command 仍需有效 admission 与 application authorization

#### Scenario: Realtime context 请求再次签发 ticket
- **WHEN** WSS 或 TLS/TCP AuthContext 请求新的 connection ticket
- **THEN** session core 返回 forbidden，不能通过已有 realtime capability 扩展或转移连接资格

#### Scenario: Ticket 在错误通道消费
- **WHEN** 绑定 WSS 的 ticket 被提交到 TLS/TCP listener
- **THEN** 消费失败且 ticket 不被转换为其他凭据、scope 不被扩大、业务 dispatcher 不被调用

#### Scenario: Ticket 被并发消费
- **WHEN** 两个连接并发消费同一合法且未过期 ticket
- **THEN** 最多一个连接获得 AuthContext，另一个收到稳定 replay/unauthenticated 结果

#### Scenario: Ticket 绑定旧 epoch
- **WHEN** ticket 签发后 session epoch 已经递增
- **THEN** 即使 ticket 尚未到期且未消费也必须被拒绝

### Requirement: AuthContext 必须只读且与 transport 实现解耦
认证成功 MUST 产生纯 Go AuthContext，包含 principal、session id/epoch、channel 与规范化 scopes，但不得包含 raw token/ticket、HTTP request、socket、connection implementation、可变 map 或 generated protocol type。AuthContext 构造入口 MUST NOT 暴露给其他 package；Scopes MUST 去重并严格匹配 channel policy。Gameplay scope MUST NOT 被解释为任何具体世界、访问或活动 mutation 权限。

#### Scenario: Application service 执行权限检查
- **WHEN** application service 判断 control capability 或 gameplay connection capability
- **THEN** 它只查询 AuthContext scope；执行 world/visit mutation 时还必须校验 admission、actor role 和领域 policy

### Requirement: Session invalidation 必须先提交权威 epoch 再通知连接
Logout、forced logout、ban 输入和安全失效 MUST 通过 store 原子递增 epoch并撤销旧 token/ticket，随后调用 transport-independent ConnectionInvalidator 发布 session id、新 epoch 与安全 reason。通知失败 MUST NOT 回滚 epoch 或恢复旧凭据。

#### Scenario: 用户正常 logout
- **WHEN** 当前 session 请求 logout
- **THEN** session core 只从当前 AuthContext 取得 session id，epoch 递增且旧 token/ticket 立即失效，连接失效通知使用新 epoch 幂等发布

#### Scenario: Principal 被 ban
- **WHEN** 上游账号域提交经过授权的 principal ban 失效请求
- **THEN** session core 撤销该 principal 的全部现有 sessions并通知各自新 epoch，但不自行决定该 principal 未来能否创建 session

#### Scenario: 连接失效通知失败
- **WHEN** epoch 已提交但 ConnectionInvalidator 返回依赖错误
- **THEN** application 返回可诊断失败并允许幂等重试通知，所有后续认证仍拒绝旧 epoch

### Requirement: Session core 必须独立于 listener 与真实存储验收
Session domain/application MUST 只依赖消费侧定义的 SessionStore、EndpointProvider、ConnectionInvalidator、clock、ID generator 和 secret generator 接口，并复用 Composition Root 已有的生产 clock 与 ID generator。实现与测试 MUST NOT 启动 listener、连接 MySQL/Redis、依赖 generated protocol type 或把测试 store 注册到正式 Composition Root。

#### Scenario: 独立运行 session 安全测试
- **WHEN** 测试 token expiry、refresh replay、ticket wrong-channel、并发消费和 epoch invalidation
- **THEN** 测试只使用 fake clock、确定性 generator、并发测试 store 与 fake invalidator，并能通过 race detector 验证

#### Scenario: 正式服务端启动
- **WHEN** production Redis store、connection registry 或公开 auth/transport adapter 尚未全部接线并通过对应验收
- **THEN** Composition Root 不注入测试 memory session store、不开放相关业务 listener，也不宣称 session API 已可用

### Requirement: Account session 失效必须撤销 BattleTicket 与 BattleSession

BattleTicket 和 BattleSession MUST 绑定唯一 Account SessionID/epoch 且不得构造第二份身份事实。Logout、forced logout、ban、refresh replay 或其他 epoch 递增 MUST 先提交 Session owner 的权威失效，再通知 BattleTicket/BattleSession invalidator 撤销该 lineage 的 installed ticket、proof key 和 active session；通知失败 MUST 不得回滚 epoch 或恢复旧 battle 资格。UDP payload、ticket selector、endpoint rebind 或 actor 字段 MUST 不能覆盖 AuthContext 派生的 PlayerID、role 或 epoch。

#### Scenario: Active battle 中 logout

- **WHEN** HTTPS AuthContext 对 active BattleSession 所属账号执行 logout
- **THEN** SessionStore 先原子推进 epoch 并撤销旧 token/ticket，随后 C++ 停止旧 lineage packet dispatch；旧 BattleTicket、key 或 rebind 不能继续使用

#### Scenario: Revoke 通知暂时失败

- **WHEN** epoch 已提交但 child control revoke 超时或 child 不可达
- **THEN** Go 保持 session 无效、撤销 target/readiness 并受控关闭，不把通知失败解释为旧 BattleSession 仍获授权

### Requirement: BattleTicket 必须与 ConnectionTicket 和 WorldAdmission 不可互换

Session policy MUST 明确 WSS ConnectionTicket 只授予 CONTROL、TLS/TCP ConnectionTicket 只授予 GAMEPLAY，而 BattleTicket 只允许在绑定 UDP endpoint 上建立 exact SimulationTarget 的 BATTLE session。WorldAdmission 继续只授权 TLS/TCP PersonalWorld/VisitSession target。任一 credential 提交到错误 listener/channel、错误 endpoint、错误 assignment 或错误 session epoch MUST fail closed 且不得转换 scope 或消费为另一凭据。

#### Scenario: TLS/TCP ticket 提交到 battle UDP

- **WHEN** 客户端把有效 GAMEPLAY ConnectionTicket 或 WorldAdmission 编码进 battle handshake
- **THEN** UDP listener 在建立 session 前拒绝且不把它转换为 BattleTicket、不泄漏 credential detail
