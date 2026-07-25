# Go Simulation Control 规格

## Purpose

定义 Go Control & Data Plane 对本机 C++ SimulationNode 的私有进程控制、placement/target 绑定、ResultProposal 持久裁决、故障恢复和资格门。

## Requirements

### Requirement: Go 与 C++ 必须通过私有且有界的进程控制契约通信
Go Control & Data Plane MUST 是 `ihomeland-sim-server` 控制会话的唯一启动和监督 owner。控制通道 MUST 使用仅在父子进程间继承的匿名 stdin/stdout handles、受信 child process identity、每次启动唯一的 bootstrap nonce、单调 frame sequence、固定最大 frame 和 closed schema；stdout MUST 只承载控制 frame，stderr MUST 只承载低敏诊断。任意未知版本、未知字段、超限 frame、错误 nonce/sequence、额外 stdout 字节或 binary/build identity 漂移 MUST fail closed。控制通道 MUST NOT bind TCP/UDP、分配 production port、接收客户端连接或替代后续 battle transport。

#### Scenario: 受监督子进程完成 hello
- **WHEN** Go 启动经过资格验证的 `ihomeland-sim-server --control-stdio`，并发送包含本次 bootstrap nonce、协议版本和期望 digest 的 hello challenge
- **THEN** C++ 只通过继承 handles 返回匹配 nonce、递增 sequence、build/model/profile digest 与容量的 hello receipt，Go 验证 child process 和 receipt 后才登记 node

#### Scenario: stdout 混入非协议内容
- **WHEN** C++ 在 stdout 写入日志、未知 JSON、超限 frame 或 sequence 不连续的响应
- **THEN** Go 关闭控制会话、将 node 标记为 unhealthy 并触发受控失败，不从损坏流中猜测下一条消息边界

#### Scenario: proposal 尝试开放控制 listener
- **WHEN** control mode、配置或测试尝试 bind TCP/UDP、登记部署端口或接受非父进程 caller
- **THEN** architecture/scope gate 失败，内部控制不能成为未评审的网络服务或 gRPC 占位

### Requirement: SimulationNode 必须以健康、兼容和容量证据注册
每个受监督 C++ 进程 MUST 表示一个不可复活的 `SimulationNode` incarnation。Go MUST 生成并拥有本次 `SimulationNodeID` 与对应 placement `RuntimeNodeID`，并只在 hello 的 binary/build、model、profile、config schema、platform qualification 和 hard limit 全部匹配后登记。Registry MUST 保存有界 node 状态、最近健康证据、实例/actor capacity 使用量与 draining 状态；只有 `healthy` 且有容量的 node 才能成为 placement target。C++ 自报容量 MUST 只能缩小 Go 配置与冻结 profile 允许值，不能扩大 8-actor instance cap 或绕过 node instance hard limit。

#### Scenario: Digest 匹配且容量可用
- **WHEN** node hello 与冻结 qualification receipt、model/profile digest 和 Go 期望一致，且 node 尚有一个 instance slot
- **THEN** registry 将该 incarnation 标记为 healthy，并允许 selector 返回其受信 RuntimeNodeID

#### Scenario: C++ 自报更大 actor 容量
- **WHEN** node hello 声称单实例可承载超过 battle profile 已资格的 8 actors
- **THEN** Go 拒绝扩大资格上限并使该 hello 不可用于 admission；不得据此修改 VisitSession 的既有容量事实

#### Scenario: Node 进入 draining
- **WHEN** Go 开始关闭、node health 过期或 control task 返回异常
- **THEN** registry 原子停止新的 placement 选择，现有 instance 进入有界 drain/revoke/stop 或故障恢复，node 不得重新变回同一 incarnation 的 healthy

### Requirement: SimulationInstance 生命周期必须绑定完整 AssignmentStamp
每个 start MUST 绑定完整 `AssignmentStamp`、Go 生成的稳定 start request identity、C++ 生成的不可复活 `SimulationInstanceID`、mapping generation、冻结 model/profile/config/nav/physics identity、seed 和容量。C++ MUST 重算 AssignmentStamp fingerprint，并只把 WorldInstanceID + 全部 immutable start 字段完全相同的请求解释为幂等重放；同一 WorldInstanceID 与不同 stamp、旧 assignment generation、错误 node、seed/config/capacity 或其他 identity 漂移 MUST 拒绝。Go MUST 只在收到回显 mapping generation 与 seed 的 exact ready receipt 后允许 placement 从 `starting` transition 到 `active`，并 MUST 为 active stamp 提供 bounded drain、stop 和 status resolution。

#### Scenario: Start 响应丢失后重试
- **WHEN** C++ 已创建 instance 并返回 ready，但 Go 未收到响应而以相同 start request identity 和完整 stamp 重试
- **THEN** C++ 返回原 SimulationInstanceID 和等价 ready receipt，不创建第二个 simulation timeline

#### Scenario: 旧 generation 的 ready 迟到
- **WHEN** predecessor 的 ready receipt 在 placement 已建立更高 generation successor 后到达
- **THEN** Go 拒绝该 receipt，不能发布 predecessor active、创建 admission target 或覆盖 successor

#### Scenario: 同一 WorldInstanceID 绑定不同 stamp
- **WHEN** control request 复用既有 WorldInstanceID 但改变 RuntimeNodeID、generation、fencing token 或 PersonalWorldID
- **THEN** C++ 返回稳定 identity-conflict，Go 将其视为安全违约且不尝试用部分 identity 恢复

### Requirement: Simulation admission target 必须是内部且可撤销的资格投影
Go MUST 只为 current active、lease 有效、node healthy、instance ready 且完整 binding 一致的 runtime 暴露不可变内部 `SimulationTarget`。Target MUST 绑定完整 AssignmentStamp fingerprint、RuntimeNodeID、SimulationNodeID、SimulationInstanceID、mapping generation、model/profile/config identity 和已资格 actor capacity；它 MUST NOT 包含 battle credential、客户端可见 endpoint、UDP port、PlayerID 覆盖字段或永久写许可。Assignment replacement、lease expiry、node loss、drain 或 stop MUST 立即使旧 target 不可用于后续 battle admission。33-actor VisitSession compatibility MUST 保持既有 world/visit 行为，但 battle actor admission MUST 在超过 8-actor qualified cap 时 fail closed。

#### Scenario: Active runtime 解析 target
- **WHEN** placement、node registry 和 C++ status 对同一完整 active stamp 与 SimulationInstanceID 达成一致
- **THEN** resolver 返回 generation-bound SimulationTarget，供后续安全 transport change 消费但不签发 credential

#### Scenario: VisitSession 超过 battle 资格容量
- **WHEN** 既有 PersonalWorld/VisitSession 合法包含超过 8 个潜在 actor
- **THEN** world/visit v1 流程保持不变，但 SimulationTarget 的 battle admission capacity gate 拒绝额外 actor，不降低或篡改 VisitSession capacity

#### Scenario: Assignment 被替换
- **WHEN** target 对应 stamp 已被更高 generation successor 替换
- **THEN** 旧 target 解析失败，旧 SimulationInstanceID、node identity 或曾经成功的 target lookup 不能恢复准入资格

### Requirement: ResultProposal 必须由 Go owner 幂等裁决并持久确认
C++ MUST 通过 node-global、最多 256 entries 的内存 outbox 只提交已登记 result kind 的有界 `ResultProposal`，并绑定不可复用 ResultID、完整 AssignmentStamp、SimulationInstanceID、Tick range、payload digest、evidence digest 和低敏 payload。Drain MUST 在停止 worker 前取得 outbox 容量；容量满时 MUST 保持未 drained，释放容量后才能重试。Go MUST 在首次裁决前按全部 immutable 字段重算 proposal fingerprint，并验证 schema、kind owner、current active fence、instance binding、kind-specific Tick policy、大小、安全字段与对应 committer policy，再在同一 MySQL transaction 中提交 owner side effect（如有）和 immutable result receipt。Go MUST 按已登记 kind 重算 payload/evidence digest，lifecycle summary 的 evidence 必须绑定 build/model/profile/config/navigation/physics、mapping generation、seed、actor capacity 与 terminal Tick。Go inbox MUST 对 exact proposal replay 去重、拒绝相同 ResultID 的字段漂移，并在 receipt 与 ack 成功前保留消费所有权；C++ MUST 以有界 replay state 接受 exact 重复 ack。Ack MUST 只包含 `committed`、`rejected` 或 `replayed` 及稳定 reason；只有相同 ResultID + 完全相同 immutable proposal 的重试才能返回既有结论，任一字段漂移 MUST fail closed。Receipt committed MUST NOT 被解释为未登记奖励、资产或 PersonalWorld mutation 已结算。

#### Scenario: 首次合法 proposal 提交
- **WHEN** active instance 提交已登记 lifecycle result，完整 fence、Tick range 与 digest 均有效
- **THEN** Go 在 owner transaction 中写入唯一 receipt 后返回 committed，C++ 收到 ack 后才从有界 outbox 删除 proposal

#### Scenario: Ack 响应丢失
- **WHEN** Go 已提交 receipt 但 C++ 未收到 ack，并以相同 ResultID 与 fingerprint 重发
- **THEN** Go 不重复 side effect，返回 replayed 与原 disposition，C++ 可以安全释放该 outbox entry

#### Scenario: Stale instance 提交新结果
- **WHEN** predecessor 在 assignment replacement 后提交尚无 receipt 的 proposal
- **THEN** Go 以 stale-assignment 拒绝且持久记录一致 fingerprint 的终态，不让旧 C++ runtime 写入奖励、资产或世界事实

#### Scenario: ResultID 被不同 payload 复用
- **WHEN** 已存在 receipt 的 ResultID 携带不同 assignment、instance、Tick range、kind 或 digest
- **THEN** store 报告 identity conflict，Go 不覆盖既有 receipt、不调用 committer，并触发低敏安全诊断

### Requirement: 故障恢复与关闭必须保持 fence 和 owner 顺序
Unexpected C++ exit、control EOF、health deadline、protocol violation 或 supervised task failure MUST 使 node terminal unhealthy、停止新 target、撤销 Go readiness 并触发非零受控关闭；同一进程内 MUST NOT 静默生成新 node incarnation 继续服务。下一次 Go 启动 MUST 创建新的 SimulationNodeID/RuntimeNodeID，解析旧 placement，并以更高 assignment generation/fence 重建需要的 runtime。正常关闭 MUST 先停止公开输入与新 placement，再 drain instance、提交或终结有界 result、revoke assignment、stop instance/node，最后关闭 storage；deadline 到期 MUST 继续 fail closed 撤销写资格，不得为等待 C++ 无限延长 lease。

#### Scenario: C++ 进程异常退出
- **WHEN** child process 在承载 active instance 时异常退出
- **THEN** Go 将 node 与全部 target 标记为失效，启动受控关闭；旧 assignment 只能因 lease/revoke 失效，不能由进程内占位 runtime 假装继续承载

#### Scenario: Go 进程重启
- **WHEN** Go 关闭控制 handles 后重新启动并发现旧 node 的 current assignment
- **THEN** 旧 child 因 EOF 结束或被 shutdown deadline 终止，新进程使用新 node identity 和更高 generation/fence 完成 replacement，旧 control receipt 和 target 均不可复活

#### Scenario: Drain deadline 超时
- **WHEN** C++ 未在有界 deadline 内完成 drain 或 result flush
- **THEN** Go 记录稳定 incomplete outcome，仍推进 assignment revoke 与强制 stop；未持久 receipt 的 proposal 不得被假定 committed

### Requirement: Go/C++ control 必须通过跨语言进程资格门
本 capability MUST 提供 versioned closed schemas、manifest、Go/C++ golden fixtures、双向 encode/decode parity、架构扫描和真实子进程 harness。Qualification MUST 覆盖 own-world、visit-world、重复 start、stale assignment、node capacity、control corruption、response loss、duplicate result、C++ crash、Go restart、drain deadline 与逆序关闭，并 MUST 重跑现有 server v1、client v1 contract baseline 和 B0.3 C++ qualification。报告 MUST 绑定 Go/C++ build、model/profile/control schema digest 且保持低敏；任一 mandatory gate 失败时 B0.5 继续关闭。

#### Scenario: 完整 control qualification 通过
- **WHEN** 当前源码的 schema parity、Go unit/race/integration、C++ contract/ASan、真实进程 fault matrix、现有 v1 regression 和 OpenSpec strict 全部通过
- **THEN** B0.4 可声明 control-qualified，并仅解锁 `establish-secure-battle-transport`

#### Scenario: 只有 mock adapter 通过
- **WHEN** Go tests 只使用 fake RuntimeController，或 C++ tests 只运行进程内 lifecycle 而没有真实跨进程 harness
- **THEN** qualification 保持未完成，不能以单侧测试声明 SimulationNode、result replay 或 crash recovery 已验证

### Requirement: BattleTicket control 必须消费 current SimulationTarget 且不能阻塞 lifecycle

Go MUST在每次BattleTicket install/status/revoke前重新解析current `SimulationTarget`，并把exact target/actor capacity/binding通过现有继承stdio control发送到对应child。新增battle control frame MUST保持closed schema、bootstrap nonce、sequence、64 KiB frame与secret redaction；lifecycle/health/revoke MUST使用高优先级有界lane，ticket install/status MAY使用独立低优先级有界queue且不得饿死drain/stop或使health误判。C++ MUST只接受与本node/instance/current stamp一致的install，并在instance/node terminal时本地先撤销ticket/session。

#### Scenario: Target 在排队期间变为 stale

- **WHEN**BattleTicket install等待control lane时assignment被更高generation替换
- **THEN**Go在发送前或C++在接收时拒绝stale binding，不占用successor actor slot且HTTP不返回credential

#### Scenario: Ticket 安装突发

- **WHEN**多个合法actor并发请求ticket并填满低优先级control queue
- **THEN**额外install以稳定backpressure/capacity reason失败，health、revoke、drain和stop仍在deadline内执行

#### Scenario: Secret control frame 被诊断捕获

- **WHEN**proof key字段经过codec、日志、fixture、report或错误格式化路径
- **THEN**secret gate拒绝或清洗输出；只有受信继承pipe与C++有界secret memory可见原值

### Requirement: SimulationNode 生命周期必须包含 battle listener 与 session revoke

Go MUST只在exact child hello/control identity、battle listener bind/advertised identity和crypto/wire config全部验证后发布node ready。Node unhealthy、control EOF、listener failure、assignment revoke、instance drain/stop或Session invalidation MUST使相关BattleTicket/BattleSession target不可用，并通过同一supervised owner触发revoke与受控关闭。Battle listener MUST在instance/result/node/storage之前按逆序停止，且不得由C++静默rebind新端口或由Go进程内fallback替代。

#### Scenario: Listener 在 child hello 后绑定失败

- **WHEN**control child identity有效但UDP address冲突或listener config漂移
- **THEN**node不注册healthy target，Go逆序关闭child并保持non-ready，不退回无battle的占位node继续签发ticket

#### Scenario: Assignment drain

- **WHEN**Go开始drain承载activeBattleSession的exact assignment
- **THEN**先停止该target新ticket/packet ingress并revoke session，再有界drain transport和simulation result，最后撤销fence并stop instance
