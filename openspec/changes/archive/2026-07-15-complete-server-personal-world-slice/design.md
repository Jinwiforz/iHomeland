## Context

当前 production Composition Root 已构造 Account、Session、PersonalWorld、VisitSession、WorldAdmission、HTTP、WSS 与 TLS/TCP，但 Placement 只以 `PlacementStore` 只读接入：`BootstrapOwnWorld` 不启动实例，`placement.Service` 与 `RuntimeController` 未构造，lease 无续约 owner。TCP command 已能调用 VisitSession core，却没有把 committed result 统一转换为 WSS/TCP push，也没有把连接断开转换为 Owner/Visitor lifecycle command。VisitSession 的 invite、reservation、grace 与 session absolute deadline 因此只存在于 snapshot，尚无 production semantic cleanup owner。

本 change 位于路线图 S7，是 Q0 `qualify-server-v1` 的前置条件。它必须复用既有领域 owner、storage adapter 和冻结协议完成单进程服务端竖切，同时保持 MySQL 持久事实、Redis 可恢复运行态、进程内连接/WorldInstance 运行态三者边界。当前部署模型只有一个服务端进程承载 PersonalWorld；跨节点调度与迁移协调不在本 change 内。

## Goals / Non-Goals

**Goals:**

- 让 own-world bootstrap 幂等获得本进程可承载的 current active assignment，并可继续签发 admission、建立 TLS/TCP 连接。
- 让 VisitSession 的所有既有 command、断线恢复、deadline、assignment invalidation 与 safe-return 在 production graph 中形成闭环。
- 只从已提交或 replay 证明的领域结果派生网络副作用，保持精确 target、低敏观测和失败隔离。
- 用有界任务、队列、deadline、启动回滚和逆序关闭管理全部新增长生命周期资源。
- 用 production Composition Root、真实 MySQL/Redis 与临时 TLS 验收 own-world / visit-world 竖切及恢复边界。

**Non-Goals:**

- 不新增或修改 OpenAPI path、Protobuf field、message ID、channel registry、listener 或 storage schema。
- 不实现世界玩法 mutation、地图模拟、奖励/结算、ActivityInstance、Room、Party、匹配、战斗或 UDP/KCP。
- 不实现多进程 placement scheduler、跨节点 runtime RPC、服务发现、负载均衡或无缝迁移。
- 不交付 Q0 独立 Go 协议资格客户端，也不开始 Unity runtime、Services 或 UI。
- 不引入通用 event bus、通用 scheduler、持久 outbox、第二套 session/presence owner 或 memory storage fallback。

## Decisions

### 1. 由窄的 PersonalWorld slice coordinator 统一跨 owner 编排

在 `internal/app` 增加一个只服务本竖切的 coordinator。它通过消费侧窄接口调用 `personalworld.Service`、`placement.Service`、`visitsession.Service`、WSS registry 与 TCP publisher，并分别实现 `worldentry` 和 TCP application 所需端口。领域事实仍只能由原 owner 提交；coordinator 只负责调用顺序、任务登记、结果投影与 transport side effect。

HTTP accept、TCP Visit command、连接 lifecycle 和 semantic deadline 都必须经过同一 result handling 路径，以免不同 adapter 分别实现 push、timer 或 replay 语义。已有 application 类型改为依赖窄接口，不把 socket、generated message 或 transport registry 反向传入领域包。

**替代方案：**直接在每个 handler 后追加 push/timer。该方案会重复 commit-unknown、replay、deadline 和 delivery 处理，并使 HTTP 与 TCP 行为漂移，因此不采用。通用 event bus 会隐藏 owner 与失败语义，也不采用。

### 2. WorldInstance 是由 Placement contract 驱动的进程内受控 runtime

新增进程内 runtime owner，实现既有 `placement.RuntimeController`：以完整 AssignmentStamp/WorldInstanceID 幂等登记逻辑 runtime，只有登记成功且本进程具备 ready gameplay transport 时 `Start` 才返回 ready；`Stop` 只移除精确 predecessor，旧 stamp 不能停止 successor。registry 有配置硬上限，不保存 PersonalWorld 内容、VisitSession、socket 或持久事实。

每次进程启动生成新的不可预测 RuntimeNodeID，使 Redis 中上一次进程的 assignment 不会被误认成本进程仍在承载。当前单进程部署下，bootstrap 遇到非本进程 current assignment 时，以完整 expected stamp 调用 Placement replacement，取得更高 generation/fence 后再开放；未来多节点部署必须以独立 change 引入调度所有权，不能复用这条“单进程 predecessor 即 orphan”的判断。

coordinator 为本进程每个 active assignment 登记 lease renewal。续约在 lease 的安全分数点触发，短暂 dependency failure 只在绝对 expiry 前有界重试；stale、missing、replacement 或到期会停止精确 runtime、拒绝新 mutation，并触发相应 VisitSession assignment invalidation。每次 gameplay operation 仍重新验证 current full stamp；后台续约不替代 point-in-time qualification。

**替代方案：**只用 Redis active assignment 代表 runtime ready。进程重启后 Redis 可能仍存而实际 runtime 已消失，会产生假 ready，因此不采用。把 WorldInstance 写入 MySQL会把可失效运行态升级为持久事实，也不采用。

### 3. Bootstrap 使用 Placement command，而非可选只读 projection

`worldentry.Service` 的 assignment 依赖由只读 `AssignmentReader` 收窄/扩展为 own-world activation 端口。`BootstrapOwnWorld` 在确保 active PersonalWorld 后调用 coordinator 的 `EnsureOwnWorldActive`，只有得到本进程 runtime ready 且 store 已确认 current active 的 assignment 才返回成功。dependency、capacity、commit-unknown 或 deadline 失败返回既有稳定公开错误，不返回“world 成功但 assignment 缺失”的半就绪结果。

并发 bootstrap 复用 Placement 原子决议；响应丢失后的再次 bootstrap 解析 current assignment，不创建第二个 runtime。bootstrap 不签发 ticket/admission，客户端仍按冻结 operation 单独请求 credential。

**替代方案：**保留 optional assignment 让客户端轮询。它无法完成服务端竖切，也把 runtime lifecycle 暴露为客户端时序，因此不采用。

### 4. TCP registry 只报告受信连接 lifecycle，不拥有 VisitSession

TCP connection 在 handshake 完成后形成不可变 lifecycle view：ConnectionID、AuthContext、admission purpose、PersonalWorldID、可选 VisitSessionID、完整 AssignmentStamp。registry 在连接建立与最终移除处调用窄 lifecycle sink；sink 不允许 payload identity 覆盖该 view。transport 定义低基数 close class，至少区分 peer/error/invalidation 与 application-return/draining，且不得把 backend error 文本传入领域层。

coordinator 只对仍匹配当前 snapshot binding 的连接提交 disconnect。Owner connection 新建时：没有 active VisitSession 则等待 `VisitOpen`；处于 matching owner grace 时用新 binding 恢复；active session 仍绑定另一条实际存活连接时拒绝抢占；snapshot 绑定的旧连接已不在 registry 时先以稳定 system command 建立 grace，再用新 binding 恢复。Visitor 必须继续使用冻结 `VISIT_RECONNECT_COMMAND` 和新 admission，不由连接事件自动提升资格。

主动 leave、kick/close 后的 safe-return、已经进入 returning 的连接以及 process draining 不再提交第二次 disconnect；stale callback 由 binding/revision/generation 条件成为 no-op。Session invalidation 可作为真实连接丢失进入 grace，但绝不恢复旧 epoch 授权。

**替代方案：**让 registry 直接调用 VisitSession。该做法会使 transport 拥有业务状态且难以区分 committed mutation 与 close callback，违反分层，因此不采用。

### 5. 单一有界 deadline owner 执行语义 expiry 与 lazy reconciliation

coordinator 使用一个受监督 worker 和有界 priority queue，而不是每个 invite/member 启动 goroutine 或 `time.Timer`。entry key 由 VisitSessionID、deadline kind、目标 identity/binding/generation 组成；新的 snapshot 替换同一语义 key，旧 revision callback 即使出队也必须携带 expected revision、绝对 deadline 与 generation/binding 调用 domain command，由 store 原子拒绝 stale。

每个 committed/replayed snapshot 都重新登记其中仍有效的 session、invite、reservation、Owner grace 与 Visitor reconnect deadline；每个本地 assignment 同样登记 renew/expiry reconciliation。启动时按 `maxInstances × (68 + visitCapacity)` 验证队列下限：每个 world 最多包含 2 个 assignment、1 个 session、1 个 Owner grace、64 个 pending invite 和 `visitCapacity` 个 reservation/reconnect deadline；运行中不得无界增长。到期命令使用从稳定 task identity 派生的 CommandID，重试不得换 identity 或延长原 deadline。

不增加 Redis global index，也不执行生产 `SCAN`。进程重启后，通过 own-world bootstrap、accept/admission、TCP connect、Visit command 与 snapshot read 对所触及的 active assignment/VisitSession 做 lazy reconciliation 并恢复任务。没有活连接且从未再次触及的 Redis 运行态由 physical TTL 最终删除；它不能产生持久事实或授权。Redis key 丢失时不得从连接/payload重建 VisitSession，旧连接 fail closed。

**替代方案：**每个实体一个 timer 简单但无法证明 goroutine 与 shutdown 上界；Redis keyspace scan 会扩大 adapter ownership、引入高成本全局发现且当前没有受控索引，因此均不采用。

### 6. 事实先提交，副作用按确定性映射尽力投递

coordinator 只处理 `created/applied/replay` 且结构完整的结果；not-committed 不投递，commit-unknown 不猜测成功。对已提交结果执行固定映射：

- invite 创建向目标 Visitor 的 WSS 连接投递 `VISIT_INVITE_PUSH`；
- Owner grace/恢复向受影响 Visitor 投递 `VISIT_OWNER_AVAILABILITY_PUSH`；
- VisitSession revision 变化向当前精确 gameplay bindings 投递完整 `VISIT_SNAPSHOT_PUSH`；
- terminal close 向可确定参与者投递 `VISIT_CLOSED_NOTICE_PUSH`，并对每条领域 `SafeReturnDirective` 仅向对应 VisitSessionID + VisitorID 的当前 TCP binding 投递 `VISIT_SAFE_RETURN_PUSH`；
- assignment replacement/revoke 向相关玩家投递 `WORLD_ASSIGNMENT_CHANGED_PUSH`，先由 VisitSession owner 提交 assignment invalidation，再处理其 safe-return directives。

TCP publisher 增加精确 composite target/connection transition 能力；不能用按 VisitSession 广播再让 payload 过滤。safe-return 入队前原子把目标连接标为 returning，使它不再接受旧 target mutation；WSS close notice只是控制面收敛，不能替代 gameplay safe-return。

网络 side effect 不与 Redis mutation 建立伪事务。无在线目标、slow consumer 或连接竞态记录稳定 delivery outcome，但不能把已提交 command 改报失败。相同 revision/effect/target 在进程内有界去重；进程重启或 command replay 后允许重新投递完整 replacement snapshot，客户端以 revision/target 收敛，不能依赖 exactly-once。若 VisitSession 事实已因 Redis flush 不存在，系统只关闭无法再证明资格的连接，不伪造 safe-return directive。

**替代方案：**为短生命周期 Redis 运行态新增 MySQL outbox 会制造跨 owner 持久事实和清理问题；把 socket 发送纳入 Lua/transaction 不可实现。因此本阶段采用 commit-first、replacement projection 与可重试 replay。

### 7. 生命周期、readiness 与观测由 Composition Root 统一管理

构造顺序为 endpoint/codec/inert registry → storage-backed domain owner → runtime/deadline/effect coordinator 与 publisher → handler/listener。registry 在构造和 supervise 后仍不接受连接，只有 storage 可用、后台 owner 已受监督且两个公开 listener 已启动后才进入 ready。任一构造、supervise、bind 或初始 reconciliation 失败，按成功栈逆序停止并清空本地 runtime/task，不写 memory fallback。

draining 先拒绝新 bootstrap/handshake/command，停止产生新 deadline/effect，处理已确认 assignment/VisitSession 的失效边界，再在共享 budget 内关闭 WSS/TCP、排空 HTTP，最后释放 Redis/MySQL。所有 worker、queue、runtime 与 connection 都有明确上限和 context；不得出现裸 goroutine。

metrics 使用 operation、deadline kind、delivery kind、stable outcome、close class 等低基数 label；日志只允许随机 ConnectionID和受控 identity 摘要，不记录 credential、payload、完整 assignment、invite、IP 或 backend 文本。readiness 不能因单个离线 push 失败而波动，但后台 owner 终止或无法维持本地 assignment 安全边界时必须撤销。

## Risks / Trade-offs

- [单进程 orphan 判断未来不适用于多节点] → 用每进程 RuntimeNodeID 和明确单进程 policy 隔离；引入多节点前必须用独立 OpenSpec 替换该 policy。
- [commit 后 push 失败导致客户端短时旧视图] → 只推完整 replacement snapshot、保留 command replay、在后续 read/connect 时 reconciliation，并对 safe-return 精确关闭旧 target。
- [进程重启不扫描 Redis，离线 session 的语义 expiry 可能晚于 deadline] → 任何再次使用先按绝对时间 lazy expire；未触及且无连接的 key 只占用有界 TTL，不产生授权或持久事实。
- [deadline queue 饱和使已提交事实无人调度] → 启动时按可达业务上界校验容量、按语义 key 原位替换，并在不变量破坏时撤销 readiness而不是丢任务。
- [disconnect、reconnect 与 close 并发] → 所有 system command 携带完整 binding/generation/revision/deadline，store 决定唯一线性化结果；stale callback 不补写。
- [lease 暂时续约失败造成可用性下降] → 只在绝对 lease 内有界重试；无法证明 current 时优先停止 runtime 和连接，不能用可用性换取 stale writer。

## Migration Plan

1. 先增加配置与启动校验、process runtime、deadline/effect coordinator 及纯 Go tests，不改变公开 listener。
2. 接入 Placement service 与 bootstrap activation，再接入 TCP lifecycle 和 VisitSession result effects。
3. 扩展 storage integration harness，验证现有 schema/key/protocol 无迁移即可运行。
4. 部署时滚动重启单进程服务；新进程以新 RuntimeNodeID replacement 旧 assignment，旧 admission 因 full stamp 变化失效。
5. 回滚到旧版本时停止新进程即可；MySQL/Redis schema 未变化。旧版本仍会只读 current assignment，不应在本 change 已对外验收后作为长期运行版本。

## Open Questions

无。多节点 runtime placement、持久通知和独立资格客户端均已明确留给后续 change，不阻塞本设计。
