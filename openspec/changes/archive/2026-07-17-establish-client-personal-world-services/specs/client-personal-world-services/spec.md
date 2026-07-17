## ADDED Requirements

### Requirement: PersonalWorld、VisitSession 与访问流程必须由独立 App Scope owner 管理

客户端 MUST 由纯 C# `PersonalWorldService`、`VisitSessionService` 与 `WorldAdmissionCoordinator` 分别拥有 PersonalWorld/WorldInstance 投影、VisitSession/invite 投影和当前访问转换。Services MUST 由 `AppComposition` 显式创建和注入，只能向消费者发布不可变、无 credential 的只读 snapshot；MonoBehaviour、Scene、Prefab、ScriptableObject、UI、generated message 与网络 channel MUST NOT 保存或替代第二份最终业务事实。

#### Scenario: 页面与场景尚未创建

- **WHEN** App Scope 已初始化且没有 UI、Scene adapter 或业务 flow 请求
- **THEN** Services 可提供空的只读状态但不查询 world、不签发 ticket/admission、不建立 socket，也不依赖 Unity 序列化资产

#### Scenario: 后续页面重新打开

- **WHEN** presentation consumer 在网络 response/PUSH 已更新 Service 后重新订阅
- **THEN** consumer 从 Service 当前不可变 snapshot 派生展示，而不是要求 channel 重放 payload 或从旧 view 恢复状态

### Requirement: World 与 Visit 投影必须按 identity 和 revision 完整替换

客户端 MUST 在提交 response/PUSH 前完整验证 PersonalWorldID、WorldOwnerID、VisitSessionID、assignment 绑定、封闭 enum、集合上限与 deadline，并把 generated message 复制为 application-owned immutable projection。每个 aggregate MUST 保存最高已提交 revision：低 revision MUST 丢弃；同 revision 且语义等价 MUST 幂等忽略；同 revision 但内容不同 MUST 视为协议冲突并使当前 target fail closed。缺失 assignment MUST 清除旧 assignment，旧 endpoint 或 WorldInstanceID MUST NOT 被沿用。

#### Scenario: 低 revision PUSH 晚到

- **WHEN** Service 已保存 revision 12 的 world 或 visit snapshot，随后收到同 identity 的 revision 11 PUSH
- **THEN** 当前 snapshot、assignment、role 与访问模式保持不变，subscriber 不观察倒退

#### Scenario: 同 revision 内容冲突

- **WHEN** response 与 PUSH 对同一 aggregate 提交相同 revision 但不同 owner、membership、lifecycle 或 assignment
- **THEN** Service 不选择最后到达者覆盖，当前 target 的 mutation gate 被关闭并进入受控重新解析

#### Scenario: Assignment 被撤销

- **WHEN** 权威完整 world snapshot 对当前 PersonalWorld 不再携带 assignment
- **THEN** 客户端原子清除旧 WorldInstanceID、endpoint 与 generation，并保留撤销 tombstone；只有更高 generation 才能建立新实例

### Requirement: World target 转换必须由显式有界状态机线性化

`WorldAdmissionCoordinator` MUST 只允许 `Inactive`、`ResolvingOwnWorld`、`OwnWorld`、`JoiningVisit`、`Visiting`、`ReturningOwnWorld` 与 `Stopped` 的登记转换。每次转换 MUST 取得单一 intent owner 并递增 `targetGeneration`；异步完成在提交前 MUST 同时匹配 current session generation、target generation、目标 identity 与当前状态。并发 target 转换 MUST 有界拒绝，客户端 MUST NOT 隐式取消、自动重发或以新 identity 猜测已发送 mutation 的结果。

#### Scenario: 完成 own-world 解析

- **WHEN** 有效 session 显式请求进入自己的世界且 bootstrap、own-world admission、gameplay connect 与完整 world snapshot 依次成功
- **THEN** coordinator 只使用服务端返回的 primary world 和 current assignment 进入 `OwnWorld`，不接受 caller 指定 Owner、WorldInstance 或 endpoint

#### Scenario: 旧 join 在返回流程后完成

- **WHEN** Visitor join 已发出但 safe-return 或新的 target generation 先使状态进入 `ReturningOwnWorld`
- **THEN** 迟到 join 结果只能完成旧等待方，不能恢复 `Visiting`、重新开放 mutation 或覆盖新的 own-world 投影

#### Scenario: 返回自己的世界失败

- **WHEN** 旧 Visitor target 已关闭但 own-world bootstrap、admission 或 connect 失败
- **THEN** 状态保持 `ReturningOwnWorld` 并保留低敏失败与权威 destination 信息，旧 Visitor target 不得恢复为可写

### Requirement: Invite accept 与 Visitor join 必须保持单一 intent 和凭据边界

客户端 MUST 只接受当前目标 Visitor 的未过期定向 invite，并使用 invite 的 VisitSessionID、InviteID 与正 expected revision 调用强类型 `acceptVisitInvite`。同一 join intent MUST 分别生成并稳定保存 accept 与 admission idempotency key，随后只以 reservation 对应 VisitSession 签发 JOIN admission。Admission credential MUST 只在 Session owner/gameplay channel 内单次交付，coordinator、Service、UI、Scene、日志和 snapshot MUST NOT 读取它；join 首帧 MUST 由 gameplay channel 内部绑定同一 credential 和 expected revision。

#### Scenario: 接受合法邀请并加入

- **WHEN** 当前 actor 接受未过期 invite，服务端返回匹配 reservation，visit admission、JOIN connect 和 join command 均成功
- **THEN** 客户端提交权威 VisitSession/world snapshot 并进入 `Visiting`，role 只能来自已验证 admission/flow binding，不能由 caller 或 payload 自报

#### Scenario: Accept 结果未知

- **WHEN** accept 请求因 caller cancel、timeout 或 transport failure 没有可判定结果
- **THEN** 客户端不以新 idempotency key 自动重试、不直接签发 join admission，并返回可收敛的 commit-unknown 结果

#### Scenario: Reservation 与 invite identity 不一致

- **WHEN** accept response 的 VisitSessionID、revision 或 expiry 不满足当前 intent 和公开合同
- **THEN** 客户端拒绝响应且不关闭 own-world 连接、不签发 admission 或进入 `JoiningVisit` 的后续阶段

### Requirement: VisitSession command 必须服从权威角色、revision 与首次结果

`VisitSessionService` MUST 只在 current own-world target、受信 role binding 与完整 snapshot 一致时开放 open、create/revoke invite、kick 和 close；只在 current Visitor role/target binding 与完整 snapshot 一致时开放 leave。Join/reconnect MUST 只由 coordinator 调用。每个 mutation MUST 携带 Service 当前 highest revision 作为 expected revision，并通过同一完整 snapshot gate 提交 response/PUSH；revision conflict 或 commit-unknown MUST NOT 被自动重试。

#### Scenario: Visitor 尝试 Owner command

- **WHEN** 当前角色为 Visitor 但 caller 请求创建邀请、移除其他 Visitor 或关闭 VisitSession
- **THEN** Service 在写入 gameplay writer 前返回稳定 policy failure，snapshot 和 revision 保持不变

#### Scenario: Owner command 成功

- **WHEN** Owner 以 current revision 创建 invite、kick Visitor 或关闭 VisitSession 且服务端返回完整首次结果
- **THEN** Service 原子应用更高 revision snapshot，并仅按结果中的权威 safe-return 目标收敛相关状态

#### Scenario: Revision conflict 后收敛

- **WHEN** mutation 返回稳定 revision conflict
- **THEN** Service 不修改原 command 或自动重发，只允许执行一次只读 snapshot 查询以取得新的权威投影

### Requirement: Control hints 与 safe-return 必须按通道权威边界收敛

客户端 MUST 将 `VisitInvitePush` 保存到按 identity 去重、按 expiry 清理且有硬上限的 invite inbox。`WorldAssignmentChangedPush`、`VisitOwnerAvailabilityPush` 与 `VisitClosedNoticePush` MUST 只作为匹配 identity/revision 的刷新或 terminal hint，不得代替 TLS/TCP 完整 snapshot、world admission 或 active gameplay safe-return。匹配 current Visitor target 的 `VisitSafeReturnPush` MUST 在 subscriber 执行前停止旧 target mutation，并驱动 `ReturningOwnWorld`；caller 不得替换 reason、preferred 或 fallback destination。

#### Scenario: Invite inbox 达到上限

- **WHEN** 未过期且不同 identity 的 invite 已达到硬上限又收到新 invite
- **THEN** Service 不无界增长集合，保留既有有效投影并返回稳定 overflow 结果，不关闭 WSS 或记录 payload

#### Scenario: WSS closed notice 早于 TCP safe-return

- **WHEN** active gameplay connection 仍存在且先收到同 VisitSession 的 control closed notice
- **THEN** Service 记录 terminal hint 并请求收敛，但不伪造 safe-return directive 或让 UI 自行选择返回目标

#### Scenario: TCP safe-return 到达

- **WHEN** current target 收到 identity 匹配的合法 `VisitSafeReturnPush`
- **THEN** 旧 target 立即拒绝新 mutation，coordinator 关闭旧 connection 并按权威 destination 进入返回流程，重复 directive 保持幂等

### Requirement: Services 生命周期与通知必须有界且默认无网络副作用

Services MUST 在初始化时只验证依赖、登记既有 channel 的强类型 subscriber 并发布本地初始状态；所有网络动作 MUST 由显式 flow 或 command 触发。状态提交 MUST 在线程安全临界区完成且在锁外通知 subscriber；网络 receive callback MUST 继续经既有有界主线程 dispatcher。App 停止 MUST 先撤销 flow、拒绝新 command 并解除 subscriber，再按既有逆序关闭 channel/session/http；迟到 callback MUST 被 stopped/session/target generation gate 丢弃。

#### Scenario: App 在 join 期间关闭

- **WHEN** AppLifetime 进入停止且存在 accept、admission、connect 或 join 等待
- **THEN** 新 flow 立即被拒绝，等待有界完成或取消，subscriber 解除且迟到结果不能提交 world/visit 状态

#### Scenario: Subscriber 抛出异常

- **WHEN** 一个 presentation subscriber 处理不可变 snapshot 时抛出
- **THEN** 已提交 Service 事实不回滚，其他生命周期资源仍可受控停止且 credential 不会进入异常文本

### Requirement: Personal world Services 必须分层验收且不依赖表现资产

实现 MUST 用纯 C# EditMode tests 覆盖十个 HTTP operation、projection validation、低/同 revision、identity 冲突、invite 上限、角色 gate、状态转换、并发 intent、caller cancel、commit-unknown、session invalidation、safe-return 和 reverse shutdown。Tests MUST 使用可控 HTTP/channel/clock/identity seam，不依赖真实账号、公共 listener、Unity Services、Scene、Prefab 或 UI。既有 protocol parity、PlayMode 与 Windows Development build MUST 继续通过，默认启动 MUST 不产生 world/visit 网络副作用。

#### Scenario: 执行状态机竞态测试

- **WHEN** tests 控制 response、control PUSH、gameplay PUSH、cancel 和 shutdown 的到达顺序
- **THEN** 每个 intent 恰好完成一次，最高 revision 不倒退，旧 target 不复活且测试可重复无外部网络依赖

#### Scenario: 启动空 BootstrapScene 构建

- **WHEN** Windows Development build 只初始化 App Scope 后正常关闭
- **THEN** Player.log 不包含 credential、未观察异常或自动 world/visit 连接，且 runtime/protocol 依赖完整
