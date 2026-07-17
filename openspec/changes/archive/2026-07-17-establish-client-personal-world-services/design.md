## Context

客户端 C0/C1 已建立唯一 `AppBootstrap -> AppComposition -> AppRoot`、统一 Session owner、九个强类型 HTTPS operation、只接收 WSS control channel 与 TLS/TCP gameplay channel。网络层能够验证 credential、framing、route、sequence、correlation 和背压，但只交付 generated response/PUSH，不拥有 PersonalWorld、WorldInstance 或 VisitSession 最终状态。

服务端 v1 已冻结并通过 own-world、visit-world、断线恢复、陈旧 admission 与持久化恢复资格验收。客户端现在可以进入 C2，但 UI 与 Scene 尚无权自行保存 world/visit 事实或编排 admission；本 change 必须先建立纯 C# application owner。现有 HTTP capability 有意未包含 `acceptVisitInvite`，因此本 change 同时对该强类型边界做一次最小扩展。

实现继续服从以下约束：不修改 generated code，不提交可推导生成物，不引入第三方 DI/async/resource 框架，不让 MonoBehaviour、Scene、Prefab、ScriptableObject 或 UI 成为业务状态 owner，初始化不产生网络副作用。

## Goals / Non-Goals

**Goals:**

- 为 PersonalWorld、VisitSession 与当前 world target 建立唯一、可测试的 App Scope owner。
- 将 HTTPS bootstrap/accept/admission、WSS control hints 与 TLS/TCP response/PUSH 收敛为不可变客户端投影。
- 提供 own-world、join visit、visiting 与 safe-return 的显式状态机，并以 session/target generation、revision、cancellation 和 mutation gate 拒绝陈旧结果。
- 为 Owner 的 open/create/revoke/kick/close 与 Visitor 的 accept/join/leave/reconnect 提供窄的强类型 application 用例。
- 保持 credential、actor identity、role、assignment 与返回目标只能来自既有受信 owner 和服务端合同。

**Non-Goals:**

- 不实现登录页面、邀请页面、screen/layer 路由、UI Toolkit/uGUI、SceneContext world adapter、Prefab 或资源加载。
- 不实现通用 event bus、service locator、通用工作流引擎或第二套网络 router。
- 不实现跨进程 token 恢复、无限自动重试、后台离线队列、DLC/Addressables、UDP/KCP 或未登记 world interaction。
- 不改变服务端 wire contract、PersonalWorld/VisitSession 领域规则或 Unity 序列化资产。

## Decisions

### 1. 以三个窄 owner 分离事实投影与访问编排

`PersonalWorldService` 只拥有当前 actor 的 primary PersonalWorld、当前 gameplay target 的 world snapshot、assignment 与最高 revision。`VisitSessionService` 只拥有有界 invite inbox、当前 VisitSession 完整 snapshot、控制面 notice 与最高 revision。`WorldAdmissionCoordinator` 只拥有当前访问模式、target generation、进行中的 intent 和通道切换。

三者均为纯 C# App Scope 对象，由 `AppComposition` 显式构造和注入；不通过全局静态入口或 `AppRoot` 查询。服务向后续 presentation 暴露不可变 snapshot 与变更通知，不暴露 generated mutable message、access token、ticket、admission 或底层 channel。

备选方案是建立一个 `WorldManager` 同时保存所有状态并直接驱动 Scene。该方案会混合 aggregate owner、连接生命周期与表现生命周期，难以证明 stale callback 和 safe-return 不变量，因此不采用。

### 2. Generated message 只作为边界输入，提交前转换为不可变投影

Protobuf message 可变且由网络 codec 创建。Services 在提交前完整校验 identity、enum、数量、deadline、assignment/world 绑定和 actor role，再复制为 application-owned immutable model。任何 subscriber 都不能修改已提交状态，也不能长期持有 channel payload。

PersonalWorld 与 VisitSession 分别按自身 revision 比较：低 revision 丢弃；同 revision 且语义等价为幂等重放；同 revision 但内容冲突视为协议不变量破坏。冲突若涉及当前 target，coordinator 立即关闭 mutation gate、撤销该 target generation 并进入受控重新解析，不能选择“最后到达者覆盖”。Assignment generation 单独单调比较；完整快照缺失 assignment 是带 generation tombstone 的清除，只有更高 generation 才能建立新实例，不能保留或复活旧 endpoint。

### 3. Control PUSH 是收敛输入，不越权伪造完整 gameplay snapshot

`VisitInvitePush` 可以更新有界 invite inbox；过期 invite 按注入时钟惰性清理，集合达到上限时拒绝继续增长并报告稳定 overflow 结果。`WorldAssignmentChangedPush`、`VisitOwnerAvailabilityPush` 与 `VisitClosedNoticePush` 只记录匹配 identity/revision 的 control hint，并使相关完整投影标记为需要刷新；它们不签发 admission、不替代 gameplay snapshot，也不根据 payload 切换 actor role。

`VisitSafeReturnPush` 是 active Visitor target 的权威返回输入。Gameplay channel 已在回调前关闭旧 mutation gate；coordinator 再校验 target generation、VisitSessionID 与当前 actor，固定进入 `ReturningOwnWorld`。WSS close notice 不能在仍活跃的 gameplay connection 上伪装同一 safe-return 结果。

### 4. 一个 coordinator 线性化 world target 转换

状态固定为：

```text
Inactive
  -> ResolvingOwnWorld
  -> OwnWorld
  -> JoiningVisit
  -> Visiting
  -> ReturningOwnWorld
  -> OwnWorld
  -> Stopped
```

每次 target 转换递增本地 `targetGeneration`，并且最多有一个转换 intent。并发的 enter/join/leave/return 请求有界拒绝，不隐式取消或重放已发送的 mutation。每个异步完成在提交前同时比较 session generation、target generation、目标 identity 与当前状态；旧完成只能收敛其自身等待，不能恢复旧连接、旧 assignment 或旧 UI 模式。

Own-world 流程为：查询 bootstrap 并提交 primary world -> 签发 own-world admission -> 显式连接 gameplay -> 查询并提交 world snapshot -> `OwnWorld`。Visit 流程为：冻结当前 invite intent -> 使用稳定 accept idempotency key 接受邀请 -> 使用独立稳定 key 签发 visit admission -> 关闭旧 target connection -> 连接 JOIN target -> 由 gameplay channel 内部携带已消费 admission 发送 join -> 提交 VisitSession/world snapshot -> `Visiting`。

主动离开、terminal close、assignment loss 或 safe-return 都先禁止旧 target mutation 并关闭旧连接，再重新执行 own-world 解析。Owner unavailable 的 WSS hint 在 grace 期间只请求收敛，不能伪造 terminal return。返回失败不得回滚为 `Visiting`；coordinator 保持 `ReturningOwnWorld` 与低敏失败/服务端 destination 投影，允许后续显式重试或由 Scene/UI 进入安全入口。

### 5. Credential 与幂等 identity 始终留在最窄 owner

新增 `acceptVisitInvite` 作为第十个 `IClientHttpApi` 强类型 operation，只接受 `visitSessionId`、`inviteId`、正 `expectedRevision`、规范 idempotency key 与 Session owner 借出的当前 access token。Codec 必须严格验证 path escaping、body、200 response、reservation identity/revision/expiry 和既有稳定错误映射。

`SessionCoordinator` 增加 generation-bound accept wrapper；响应晚于 session 轮换时不得提交 reservation。Coordinator 为同一 accept/admission intent 生成一次 CSPRNG idempotency key 并保存到该 intent 完成，不因 timeout、caller cancel 或 transport unknown 自动换 key 重发。

JOIN/RECONNECT admission credential 继续只存在于 `ClientGameplayChannel`。Channel 增加窄的 pending-target join/reconnect 方法，在内部从已消费 admission 构造首个 command；PersonalWorld/VisitSession Services、日志和 subscriber 都不能读取 credential。相比把 credential 返回给 coordinator，此设计缩短 secret 生命周期并保持既有 single-use owner。

### 6. 业务 command 由 VisitSessionService 统一施加 role 与 revision 前置条件

Owner 用例只在 current own-world target、受信 admission/flow role 与完整 snapshot 一致时开放 `open/create invite/revoke/kick/close`；Visitor 用例只在受信 role、membership target 与完整 snapshot 一致时开放 `leave`，join/reconnect 仅由 coordinator 调用。每个 command 使用当前完整 snapshot 的 expected revision，response 中的完整首次结果通过同一 apply gate 提交，safe-return 只用于匹配目标的收敛提示，不能让本地 caller 改写 destination。

Service 不对 revision conflict 自动重试 mutation，因为旧 command 是否提交及新状态所需用户意图无法由 transport 层猜测。它返回稳定 application failure，并在可用时请求一次只读 snapshot 重新收敛。

### 7. 生命周期与通知保持有界且不建立隐式连接

Services 初始化只登记 channel subscriber、验证本地依赖并进入 `Inactive`；不会查询 world、签发 credential 或启动 WSS/TCP。显式业务 flow 才能联网。状态提交在线程安全临界区内完成，subscriber 快照在锁外通知；来自 network receive 的 PUSH 继续先经过既有 `MainThreadDispatcher`。停止顺序先撤销 flow 与 subscriber，再关闭 control/gameplay/session/http，迟到 callback 通过 stopped/target generation gate 丢弃。

不新增通用 `MessageRouter`、`EventBus` 或 `IServiceProvider`。只有后续 UI/Scene 确有多个稳定消费者时，才评估更高层 presentation adapter。

### 8. 验收以纯 C# 合同和确定性竞态测试为主

EditMode tests 使用 fake HTTP API、可控 channel、clock 和 identity source，覆盖十个 HTTP operation、snapshot 验证、revision 重放/冲突、invite 上限、角色 gate、每条状态迁移、并发 intent、迟到结果、session invalidation、safe-return 与逆序停止。既有 protocol parity、PlayMode 与 Windows Development build 继续回归；默认 BootstrapScene 仍不得自动联网。

## Risks / Trade-offs

- [三个 owner 与 coordinator 增加类型数量和学习成本] → 每个类型只拥有一类事实或转换，禁止空 interface、通用 manager 和 presentation 逻辑；通过短命名空间与状态图维持可读性。
- [HTTP accept 使既有九 operation 基线变化] → 同步修改 `client-http-bootstrap` requirement、operation catalog、codec fixture 与 baseline tests，仍不开放任意 path/body API。
- [Response、WSS 与 TCP PUSH 可能乱序] → 完整 snapshot 使用 revision/identity gate，control hint 不冒充完整 snapshot，同 revision 冲突 fail closed。
- [Caller cancel 后服务端可能已提交 mutation] → 不自动换 key 重发；保留 intent identity 并通过只读 snapshot 收敛，不向 UI 声称“未发生”。
- [返回 own-world 期间网络失败导致无 active world] → 旧 Visitor target 保持不可写，状态停留 `ReturningOwnWorld` 并提供显式重试/安全入口信息，不回退到陈旧访问。
- [Generated model 转换产生样板代码] → 只转换当前业务需要且必须不可变的合同，不建立通用 reflection mapper 或复制生成代码。

## Migration Plan

1. 扩展 HTTP operation catalog、model、codec、API 与 SessionCoordinator，先以 fixtures 验证 `acceptVisitInvite`。
2. 增加不可变 PersonalWorld/VisitSession/flow models 与独立 projection Services。
3. 为 gameplay channel 增加不泄漏 admission 的 join/reconnect 窄入口，再实现 coordinator 与 command façade。
4. 在 `AppComposition` 中显式接线并登记逆序生命周期，保持默认启动零网络副作用。
5. 完成 EditMode、protocol verify、PlayMode 与 Windows Development build 回归后再进入 UI routing change。

该 change 尚未被任何 UI/Scene 消费，回滚时可整体移除新 Services 与 composition wiring，并恢复九 operation 客户端 HTTP 基线；服务端合同无需回滚。

## Open Questions

无。UI 技术、SceneContext 映射与自动恢复策略分别留给后续已登记 change。
