## ADDED Requirements

### Requirement: Personal World 必须按核心风险顺序交付
项目 MUST 先交付 PersonalWorld identity/revision，再交付 WorldInstance placement/lease 与持久化，随后交付 VisitSession，最后冻结 world/visit protocol、transport admission 和 Go 客户端资格验收。ActivityInstance、Room 与 Party MUST 等待 own-world 和 visit-world 服务端 v1 完成。

#### Scenario: Room 被提议为个人世界前置
- **WHEN** proposal 要求玩家先创建 Room 或 Party 才能进入自己的 PersonalWorld 或接受 VisitSession
- **THEN** 评审必须拒绝该依赖，保持 world presence、visit membership 与活动准备相互独立

#### Scenario: Unity 提前实现个人世界
- **WHEN** world/visit protocol、placement、storage、admission 或恢复场景尚未由 Go 测试客户端验收
- **THEN** Unity 不得实现对应运行时 Service、Scene adapter 或 UI 页面

### Requirement: 每个玩家必须拥有独立且服务端权威的 Personal World
每个已创建玩家角色 MUST 拥有一个 primary PersonalWorld，使用稳定 `PersonalWorldID` 与不可变 `WorldOwnerID = PlayerID` 标识。PersonalWorld MUST 默认只允许 Owner 进入，MUST NOT 与其他玩家世界自动合并。Owner 客户端 MUST NOT 充当权威网络主机。

#### Scenario: 玩家正常进入游戏
- **WHEN** 已认证玩家进入开放世界且没有接受其他世界的有效 admission
- **THEN** 服务端加载或启动该玩家自己的 PersonalWorld，返回由服务端托管的 WorldInstance，且不会把玩家自动放入陌生玩家世界

#### Scenario: Owner 更换设备或登录地域
- **WHEN** Owner 使用其他设备或从其他物理部署位置登录
- **THEN** 系统继续解析同一 PlayerID、PersonalWorldID 与 WorldOwnerID，只允许 placement 改变运行位置

### Requirement: PersonalWorld 持久事实必须与 WorldInstance 运行态分离
PersonalWorldID、WorldOwnerID、持久 revision 与世界子系统事实 MUST 可跨进程恢复；WorldInstanceID、部署位置 assignment、lease 和 connection presence MUST 被视为可变运行态。同一 PersonalWorld MUST 最多存在一个对 gameplay 开放的 active writable WorldInstance，并使用 lease/fencing 或等价机制拒绝陈旧实例提交。

#### Scenario: 同一世界并发启动
- **WHEN** 两个进程因重试或故障恢复同时尝试启动同一 PersonalWorld 的 writable instance
- **THEN** 最多一个实例获得当前写入资格，另一个不得签发 admission 或提交世界 mutation

#### Scenario: WorldInstance 重建
- **WHEN** 当前运行进程故障且 placement 在其他部署位置重建 WorldInstance
- **THEN** PersonalWorldID、WorldOwnerID 和已提交 revision 保持不变，旧实例的 lease 不能继续写入

### Requirement: VisitSession 必须是访客进入个人世界的唯一资格 owner
Visitor MUST 通过服务端创建的 invite、eligibility validation 和一次性 admission 加入 Owner 的当前 WorldInstance。VisitSession MUST 绑定 immutable Owner、PersonalWorldID、Visitor PlayerID、session epoch、capacity、revision 与 expiry；payload 中的 owner/world/instance/player 字段 MUST NOT 覆盖 AuthContext 和服务端目录结果。

#### Scenario: Visitor 接受合法邀请
- **WHEN** Visitor 在邀请有效期内通过容量、玩法阶段、版本和安全策略校验
- **THEN** 服务端解析 Owner 当前 WorldInstance，签发短期一次性 admission，并以 Visitor role 建立 VisitSession membership

#### Scenario: Visitor 重放 admission
- **WHEN** 已成功消费或已经过期的 admission 被再次提交
- **THEN** 服务端拒绝连接且不恢复旧 membership、不增加容量计数，也不降级为普通 session token 认证

#### Scenario: 客户端伪造 Owner 世界
- **WHEN** 客户端在邀请或连接 payload 中提交其他 PersonalWorldID、WorldInstanceID 或 Owner PlayerID
- **THEN** 服务端忽略或拒绝该声明，只使用 AuthContext、VisitSession 和 placement 的权威绑定

### Requirement: Owner 与 Visitor 权限必须显式且不可继承
WorldOwnerID MUST 在 PersonalWorld 生命周期内保持不变。Visitor MUST NOT 继承 Owner 权限、转让世界、修改世界配置、邀请额外 Visitor、推进 Owner 关键任务、消费不可恢复唯一资源或提交奖励最终事实，除非后续交互规格对具体 command 显式授权。Owner role MUST NOT 因连接顺序、seat、延迟或断线自动转移。

#### Scenario: Visitor 尝试执行 Owner command
- **WHEN** Visitor 提交仅允许 Owner 的世界配置或关键任务 command
- **THEN** application 根据 AuthContext 与 VisitSession role 返回 forbidden，PersonalWorld revision 和状态保持不变

#### Scenario: Owner 断线但 Visitor 在线
- **WHEN** Owner connection 进入 reconnecting 而一个或多个 Visitor 仍在线
- **THEN** WorldOwnerID 保持不变，Visitor 不会成为 Owner；需要 Owner 在线确认的 mutation 被暂停或拒绝

### Requirement: Owner 断线与离开必须有界关闭访问
Owner 断线后 VisitSession MUST 进入有绝对 deadline 的 reconnect grace，旧 connection callback 与 timer MUST 通过 binding/deadline 校验保持幂等。Owner 在 deadline 前恢复时可以继续同一世界访问；Owner 主动关闭或 grace 到期时，VisitSession MUST 关闭全部 Visitor admission，并为 Visitor 提供返回自己 PersonalWorld 或安全入口的明确结果。

#### Scenario: Owner 在 grace 内重连
- **WHEN** Owner 使用新 connection binding 在匹配 deadline 前恢复 session
- **THEN** WorldInstance 和 Visitor membership 可以继续，旧 disconnect/timer 不得关闭新连接或转移 Owner

#### Scenario: Owner grace 到期
- **WHEN** Owner 未在 reconnect deadline 前恢复且 VisitSession 仍有 Visitor
- **THEN** VisitSession 以明确 owner-unavailable 原因关闭，Visitor 被移出该 WorldInstance 并获得返回流程，PersonalWorld 持久事实不被删除

### Requirement: Player、World、Visit 与 Activity 状态必须按 owner 分离
PlayerState、PersonalWorldState、VisitSessionState 与 ActivityInstanceState MUST 分别由明确 owner 管理。每个 Visitor 可执行交互 MUST 登记 actor role、mutation owner、settlement owner、idempotency、revision/transaction 边界和失败语义。Visitor 奖励 MUST 写入 Visitor 自己的持久 player ledger；世界 mutation MUST 写入 Owner PersonalWorld；任何 adapter MUST NOT 通过双写或顺序调用伪造原子成功。

#### Scenario: Visitor 获得个人奖励
- **WHEN** Visitor 在 Owner 世界完成一个明确允许发放个人奖励的交互
- **THEN** 奖励以幂等 command 写入 Visitor 自己的 player ledger，Owner 世界只提交规格要求的世界事实，响应丢失重试不会重复发奖

#### Scenario: Redis 运行态丢失
- **WHEN** VisitSession、presence 或 WorldInstance assignment 因 Redis 清空而丢失
- **THEN** 当前访问可以安全结束并要求重新 admission，但已提交 PersonalWorldState、PlayerState、资产和奖励不会丢失或回滚

### Requirement: Personal World 访问必须由服务端 placement 承载
Owner 进入自己的世界时，placement MUST 根据受信部署位置健康、容量、网络与合规输入选择服务端 WorldInstance。Visitor MUST 加入 Owner 当前 assignment，而不是创建同名世界副本或自行选择 endpoint。部署位置与 WorldInstance MUST NOT 进入 PlayerID 或 PersonalWorldID；旧 endpoint/instance 值 MUST NOT 恢复已经失效的 admission。

#### Scenario: 跨地域 Visitor 加入
- **WHEN** Visitor 与 Owner 位于不同物理地域且当前路径满足联机策略
- **THEN** Visitor 被连接到 Owner 的权威 WorldInstance，双方身份和世界 owner 不变，实际部署位置与延迟可以被观测

#### Scenario: Visitor 使用旧 InstanceID 重连
- **WHEN** Owner 世界已经迁移或重建，而 Visitor 提交旧 endpoint 或 WorldInstanceID
- **THEN** 服务端根据当前 assignment 和 VisitSession 重新解析；旧值不能绕过 admission、epoch 或 lease 校验

### Requirement: Party、Room、VisitSession 与 ActivityInstance 必须独立
VisitSession MUST 能在没有 Party 或 Room 的情况下支持邀请访问。Party MUST 只表达跨场景持续队伍；Room MUST 只表达自定义活动准备；ActivityInstance MUST 只表达活动运行与 admission。任一 membership index MUST 使用明确 scope，MUST NOT 以裸 PlayerID 在所有 Party、Room、Visit 和 World presence 中实施全局互斥。

#### Scenario: 好友直接访问个人世界
- **WHEN** Owner 邀请好友进入自己的 PersonalWorld 且双方没有 Party 或 Room
- **THEN** 系统只创建 VisitSession membership，不创建虚假 Party、Room 或 ActivityInstance

#### Scenario: Party 从个人世界进入副本
- **WHEN** 未来 Party 成员正在 Owner 世界联机并请求进入副本
- **THEN** VisitSession、Party、Room/ready 与 ActivityInstance 按各自 owner 迁移或并存，任何 aggregate 不接管其他 aggregate 的最终事实
