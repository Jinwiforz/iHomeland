# Server Personal World Slice 规格

## Purpose

定义 PersonalWorld 服务端竖切的 production runtime、权威跨模块编排、连接生命周期、语义 deadline、跨通道副作用、故障恢复及验收边界。

## Requirements

### Requirement: Production graph 必须承载唯一受控 WorldInstance runtime
Composition Root MUST 构造 production Placement service 与实现既有 RuntimeController contract 的进程内 WorldInstance owner。每次进程启动 MUST 使用新的受信 RuntimeNodeID；runtime MUST 以完整 AssignmentStamp 幂等启动和精确停止，并具有配置硬上限。只有 runtime ready 且 PlacementStore 已条件提交 current active 的 assignment MAY 对 gameplay 开放；Redis assignment、runtime registry、listener ready 三者中的任意单一事实 MUST NOT 被解释为完整可写资格。当前单进程 policy MAY 以完整 expected stamp replacement 不属于本进程的 predecessor，但跨节点调度 MUST NOT 由本 capability 猜测实现。

#### Scenario: 首次启动个人世界实例
- **WHEN** active PersonalWorld 没有 current assignment 且 runtime、storage 与 gameplay listener 均可用
- **THEN** Placement 先提交 starting assignment，runtime owner 对相同 stamp 报告 ready，再原子发布 active，并只登记一个本地 WorldInstance

#### Scenario: 进程重启但 Redis 保留旧 assignment
- **WHEN** 新进程使用新 RuntimeNodeID 读取到上一进程仍未物理过期的 current assignment
- **THEN** 单进程 coordinator 以完整 predecessor stamp replacement 为更高 generation/fence 的本地 runtime，旧 admission 与旧 writer 不能恢复资格

#### Scenario: Runtime capacity 已满
- **WHEN** bootstrap 需要启动新 WorldInstance 但本地 runtime 已达到配置硬上限
- **THEN** operation 返回稳定 dependency/capacity failure，不发布 active assignment、不创建无 owner goroutine且不使用 memory fallback

### Requirement: Own-world 与 visit-world 必须形成端到端权威编排
服务端 MUST 连接 Account、Session、PersonalWorld、Placement、VisitSession、WorldAdmission、HTTP、WSS 与 TLS/TCP 的既有公开契约。Own-world 路径 MUST 从认证 bootstrap 得到 active assignment，单独签发并原子消费绑定该 full stamp 的 admission，再建立唯一受信 gameplay target。Visit-world 路径 MUST 由 Owner 在 own-world connection 创建 VisitSession/invite，Visitor 经 HTTPS accept、短期 admission 与 TLS/TCP join/reconnect 进入相同 assignment；payload identity MUST NOT 替代 AuthContext、connection binding、membership 或 full stamp。Room、Party 与 ActivityInstance MUST NOT 成为该路径前置条件。

#### Scenario: 完成 own-world 连接
- **WHEN** 已认证 Player bootstrap 自己的 PersonalWorld、签发 own-world admission 并通过 TLS/TCP handshake 请求 world snapshot
- **THEN** 每一步都绑定同一 current full AssignmentStamp，返回该 Player 的世界与 assignment 投影，credential 只能消费一次

#### Scenario: 完成 Visitor 加入
- **WHEN** Owner 创建定向 invite，目标 Visitor 以正确 revision 接受并使用所得 membership 签发 admission 后提交 join
- **THEN** VisitSession 原子占用 capacity、绑定 Visitor 当前 connection并向 Owner/Visitor gameplay target 投影同一新 revision

#### Scenario: Payload 尝试切换世界
- **WHEN** 已认证 connection 的 command payload 携带其他 Player、PersonalWorld、VisitSession、instance 或 endpoint identity
- **THEN** application 只使用 handshake与registry中的受信 target并返回既有 validation/forbidden/stale error，不签发或迁移到 payload target

### Requirement: Placement lease 与 assignment loss 必须受监督并 fail closed
每个由本进程承载的 active assignment MUST 登记有界 lease renewal/reconciliation。Renew MUST 携带完整 current condition，并在原 lease absolute expiry 前使用固定策略调度；dependency failure MAY 在该边界内有界重试，但 MUST NOT 延长已失效 lease。Stale、missing、replacement、expiry 或无法维持 runtime safety MUST 停止精确 local runtime、拒绝旧 target 后续 mutation，并在存在权威 active VisitSession 时提交 assignment invalidation；dependency error 本身 MUST NOT 伪造 assignment-changed 事实。

#### Scenario: 正常续约 active assignment
- **WHEN** 本地 runtime 仍匹配 current full stamp 且 renew 在 lease 到期前原子提交
- **THEN** coordinator 更新同一 assignment 的 expiry 与下一次任务，不改变 generation/fence或创建第二个 runtime

#### Scenario: 旧 renew 晚于 replacement
- **WHEN** predecessor 的续约任务在 successor 已成为 current 后到达
- **THEN** store 以完整 stamp 拒绝 stale renew，predecessor runtime 被精确停止且 successor 不受影响

#### Scenario: Placement dependency 暂时失败
- **WHEN** coordinator 无法读取或续约 assignment但尚无 missing、expired或不同 stamp 的权威证据
- **THEN** 系统在原 lease 边界内有界重试并拒绝伪造 VisitSession close；超过安全边界后停止本地资格而不是继续 stale 写入

### Requirement: Connection lifecycle 必须只作用于精确当前 binding
TCP transport MUST 在成功认证后向 application 暴露不可变、受信的 connection lifecycle view，并在最终移除时报告低基数 close class；transport MUST NOT 直接拥有或修改 VisitSession。Coordinator MUST 只为匹配当前 AuthContext、ConnectionBindingID、VisitSession/PersonalWorld target 与 revision 的真实连接丢失提交 Owner/Visitor disconnect。Application-return、safe-return、已提交 leave/close 后的移除与 process draining MUST NOT 建立新的 grace；晚到 callback MUST 成为 stale no-op。

#### Scenario: Owner 意外断线后恢复
- **WHEN** 当前 Owner binding 因 peer/error/invalidation 移除并在 grace deadline 前以新有效 session lineage和connection重新进入
- **THEN** VisitSession 先以旧 binding进入 owner grace，再由 immutable Owner 更新为新 binding并恢复 open，旧 timer不能关闭恢复后的session

#### Scenario: Visitor 意外断线后恢复
- **WHEN** joined Visitor 当前 binding断开、取得新的 reconnect admission并在deadline前提交冻结的 reconnect command
- **THEN** membership 先以旧 binding进入 reconnecting，再只由新受信 binding恢复 joined且capacity不重复占用

#### Scenario: 旧 Owner connection 仍存活
- **WHEN** 第二条 own-world connection尝试取得仍由另一条active connection绑定的open VisitSession
- **THEN** coordinator拒绝抢占，不能直接替换Owner binding、制造grace或关闭第一条连接

#### Scenario: Safe-return 关闭触发移除 callback
- **WHEN** Visitor connection 已因 committed directive进入returning/closing并随后从registry移除
- **THEN** lifecycle sink不再提交VisitorDisconnect，membership与terminal reason不被第二次修改

### Requirement: Semantic deadline 必须由单一有界 owner 执行
Production MUST 使用一个受监督、可取消且有容量上限的 semantic deadline owner，覆盖 assignment renew/expiry、VisitSession expiry、invite expiry、reservation expiry、Owner grace 与 Visitor reconnect grace。实现 MUST 按 VisitSessionID、deadline kind、target identity、binding/generation 对任务去重或替换，不得为每个实体创建裸 goroutine/timer。到期 command MUST 复用由稳定 task identity 派生的 CommandID，并携带原 absolute deadline、expected revision 与所需 generation/binding；重试 MUST NOT 换 identity、延长 deadline 或覆盖较新状态。

#### Scenario: Invite deadline 到达
- **WHEN** matching invite、deadline与revision在absolute deadline到达时仍为current
- **THEN** deadline owner以稳定command提交一次expiry并登记所得新snapshot；相同task重试只得到replay

#### Scenario: 旧 grace task 晚到
- **WHEN** Owner/Visitor已恢复或进入更新generation后旧task出队
- **THEN** domain/store以revision、generation、binding或deadline拒绝stale callback，较新connection与snapshot保持不变

#### Scenario: 重复登记同一 snapshot
- **WHEN** command replay、read reconciliation或并发observer多次登记相同semantic deadlines
- **THEN** queue按稳定key收敛到同一组有界entry，不线性增加timer、goroutine或内存

### Requirement: Committed VisitSession 结果必须驱动精确跨通道副作用
Coordinator MUST 只从结构完整的 created/applied/replay 结果派生既有 WSS/TCP push；not-committed MUST 不投递，commit-unknown MUST 不猜测成功。Invite、Owner availability、terminal close、assignment change、Visit snapshot 与 safe-return MUST 映射到 registry 已允许的唯一 channel/message。`VISIT_SAFE_RETURN_PUSH` MUST 只投递给 directive 的 VisitSessionID + VisitorID 当前 gameplay binding，并在投递前使该 connection 停止旧 target mutation；WSS notice MUST NOT 替代 gameplay safe-return。网络投递失败 MUST NOT 回滚事实、伪造 command failure 或创建第二套 durable outbox。

#### Scenario: 创建定向 invite
- **WHEN** CreateInvite 已提交并包含目标 Visitor 与新 revision
- **THEN** 仅目标 Player 的在线 WSS connections收到 `VISIT_INVITE_PUSH`，其他Player与Owner gameplay connection不被当作invite target

#### Scenario: Owner grace 到期关闭会话
- **WHEN** matching owner grace expiry 已terminal提交并返回多个稳定排序的SafeReturnDirective
- **THEN** WSS收到控制面availability/close收敛，且每个仍匹配的Visitor gameplay connection各收到自己的`VISIT_SAFE_RETURN_PUSH`并停止旧world mutation

#### Scenario: Push 与 actor response 并发
- **WHEN** committed command response和由同一result派生的snapshot push进入同一TCP connection
- **THEN** connection writer分配唯一单调sequence并串行写frame，不交错socket写入或扩大target

#### Scenario: 目标已经离线
- **WHEN** 领域结果已提交但WSS/TCP registry没有matching current connection或队列拒绝slow consumer
- **THEN** coordinator记录稳定delivery outcome并保留权威result；command仍按已提交语义响应，后续read/reconnect从snapshot收敛

### Requirement: 恢复、关闭与验收必须保持运行态边界
进程重启后 MUST 通过 bootstrap、accept/admission、connection、command或snapshot read 对被触及的 assignment/VisitSession执行 lazy reconciliation；MUST NOT 对 Redis 执行无 owner 的全局 key scan。Redis flush/key missing MUST 使旧 admission、membership与connection fail closed，且 MUST NOT 从MySQL、payload或本地registry重建旧访问事实。Composition Root MUST 在后台owner和listeners均成功后才ready，部分失败必须逆序回滚；draining MUST 有界停止新入口、deadline/effect生产、runtime、realtime、HTTP与storage。验收 MUST 使用production graph、真实MySQL/Redis、临时TLS和Go wire harness覆盖完整竖切，但 MUST NOT把它声明为独立Go协议客户端资格或server v1最终资格。

#### Scenario: Redis flush 后旧 Visitor 继续发送 command
- **WHEN** VisitSession/placement/admission运行态已丢失但旧TCP connection仍尝试旧target mutation
- **THEN** 每次current qualification或reconciliation失败并关闭旧连接，不从PersonalWorld持久事实或payload补回membership、directive或assignment

#### Scenario: Listener 启动中途失败
- **WHEN** runtime/deadline owner已构造但WSS或TLS/TCP listener无法bind或监督任务异常退出
- **THEN** readiness保持false并按成功初始化栈释放所有task、runtime、registry与storage，不残留后台goroutine或active assignment假象

#### Scenario: 执行完整竖切 integration harness
- **WHEN** 开发者在隔离MySQL/Redis和临时TLS环境运行本change验收
- **THEN** harness覆盖own-world、visit-world、Owner/Visitor断线恢复、deadline、safe-return、stale admission、assignment replacement、Redis flush和process reconstruction，且全部资源有界清理、production graph不注入fake store

#### Scenario: 只完成本 change
- **WHEN** production服务端能够完成own-world/visit-world竖切但尚无Q0独立可复用Go资格客户端
- **THEN** 项目只声明`complete-server-personal-world-slice`完成，`qualify-server-v1`与Unity runtime仍保持未完成
