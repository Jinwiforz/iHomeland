# Battle Network Qualification 规格

## Purpose

定义 B0.6 真实进程、安全 wire、弱网、容量、生命周期、安全与长时运行资格门，以及低敏可审计 evidence 和 cleanup 边界。

## Requirements

### Requirement: B0.6 必须只接受完整且无漂移的 B0.5 进入证据

资格入口 MUST 在启动任何 Go/C++ 进程或开放测试 UDP 路径前验证 current source、Windows x64 toolchain/dependency、model、network profile、simulation control、battle wire/registry/config、B0.3/B0.4 receipt 与 B0.5 `secure-transport-qualified-windows-x64` report identity。Manifest、digest、qualification label、mandatory gate 或 source corpus 任一缺失、陈旧、漂移或被改写时 MUST fail closed；诊断运行 MUST NOT 产生资格结论。

#### Scenario: B0.5 identity 完整匹配

- **WHEN** 当前源码、binary、dependency、model/profile/control/wire/config 与已归档 B0.5 evidence 全部匹配
- **THEN** B0.6 可以创建隔离 run directory 并开始真实进程资格矩阵，但仍不预先声明 qualified

#### Scenario: 使用旧 C++ binary

- **WHEN** qualification manifest 绑定的 C++ binary digest 与当前 B0.5 report 或实际 child 不一致
- **THEN** 入口在发放 BattleTicket 和启动 fault matrix 前失败，旧报告不得复用

### Requirement: Fault manifest 与注入路径必须确定、双向且可重放

本 capability MUST 提供 versioned closed-schema fault manifest，完整绑定 B0.2 的 seed、12 个 scenario、required impairment/workload/phase、cadence、MTU、queue 和预算。真实网络注入 MUST 分别控制 uplink/downlink latency、jitter、loss、burst、reorder、duplicate、bandwidth、pause/resume、MTU 与 endpoint mapping，并记录规范化 PRNG、单调时钟、packet metadata 和规则 disposition；不得解密、重写或按 payload 内容选择性处理 secure datagram。每个 mandatory scenario MUST 有显式 warmup、measurement duration、deadline 和 cleanup budget，禁止隐藏默认值、人工跳过或运行时降低 impairment。

#### Scenario: 重放同一 fault case

- **WHEN** 两次运行使用相同 source identity、fault manifest、seed、workload 和 environment class
- **THEN** 注入规则、方向、packet disposition 顺序和场景分类可重放，测量值允许在登记容差内波动但 qualification disposition 必须一致

#### Scenario: Fault gateway 无法控制 downlink

- **WHEN** 当前环境只能对 client-to-server 流量注入故障，或权限/端口映射使任一 mandatory impairment 无法执行
- **THEN** 对应 gate 标记 unsupported 且整体 not-qualified，不把单向或 clean run 解释为完整网络资格

### Requirement: 资格必须驱动真实安全 wire 与独立黑盒协议客户端

Mandatory run MUST 启动真实 Go Composition Root、精确 B0.5 C++ child、真实 BattleTicket/Redis 状态和单 C++ UDP listener，并由不导入服务端 transport/gameplay 内部实现的独立协议客户端完成 cookie、X25519/HKDF/AEAD handshake、raw/KCP route、input、snapshot、baseline/resync、rebind、rekey 与 close。Fault gateway MUST 作为受控 NAT/impairment 边界使用 advertised endpoint 与每客户端独立 upstream mapping，但 MUST NOT 创建第二个 simulation listener、绕过 ticket consume、替换 crypto 或调用 C++ 内部对象。

Authenticated UDP close MUST 在同一 cleanup deadline 内使用有界 request retry 与 server acknowledgement redundancy；重试 MUST NOT 重置或延长 deadline。只有至少一个 `CloseAcknowledged` 通过客户端认证且 production listener 已成功排队全部登记 acknowledgement copy 时才可记录 normal close，任一失败 MUST fail closed 并继续逆序资源回收。

#### Scenario: 五人默认协作矩阵

- **WHEN** 1 Owner + 4 Visitor 经真实 HTTPS admission 建立各自 BattleSession 并运行全部 mandatory profile fault scenarios
- **THEN** 所有 input、snapshot、reliable event、resync 与 close 都经过登记的 secure raw/KCP wire，报告可关联 lane/session generation/assignment/tick 且无客户端权威 gameplay 字段

#### Scenario: 测试直接调用 BattleSession

- **WHEN** runner 绕过 UDP socket、握手或 ticket consume，直接构造 C++ BattleSession、SimulationInstance input 或 replication queue
- **THEN** architecture/registry gate 失败，该结果只能作为单元诊断而不能计入 B0.6

### Requirement: 默认矩阵与容量曲线必须分别满足冻结预算

资格 MUST 对 1、5、8 actor 运行版本化 workload，并分别测量 uplink/downlink bytes、packet loss、gateway actual delivery age、客户端 snapshot cadence、baseline recovery、KCP retransmit amplification、ingress/egress/KCP high-watermark、expired/rejected count、CPU per Tick、Tick debt、process working set、instance/history memory 和 close reason。Delivery age MUST 使用 fault gateway 成功 socket write 的 `DeliveredAt - ReceivedAt`；intentional loss、burst loss 和相邻客户端 snapshot receipt gap MUST NOT 伪装为单 packet delivery age。1 Owner + 4 Visitor 的全部 mandatory fault matrix MUST 在 B0.2 的 per-player/per-instance bandwidth、2.5 ms/Tick、64 MiB instance、8 MiB history、256-item node/session queue、64-message KCP queue和 expiry 预算内通过；8 actor MUST 运行登记的 clean、movement-heavy、boss-burst、KCP retransmit 与 queue pressure 容量集并在同一 hard limits 内通过。第 9 个 actor MUST 被 battle admission 拒绝而不改变 33 人 VisitSession 兼容事实。

#### Scenario: 默认五人矩阵超出预算

- **WHEN** 任一 mandatory 五人场景超过冻结带宽、CPU、memory、queue、expiry、recovery 或 Tick debt 阈值
- **THEN** B0.6 整体 not-qualified，工具不得通过降低 actor 数、impairment、message rate、安全开销或 scenario duration 生成通过报告

#### Scenario: 八人容量失败但五人通过

- **WHEN** 默认五人全部通过而任一 mandatory 八人容量场景超出 hard limit
- **THEN** 报告分别保留五人结果与八人失败，整体不解锁 B0.7，且不得修改 VisitSession capacity 或静默把 battle qualified maximum 降为五人

#### Scenario: 第九个 actor 申请 BattleTicket

- **WHEN** 八个 actor 已 installed 或 active 且第九个合法 Visitor 申请 battle admission
- **THEN** 请求以稳定 capacity reason 拒绝，既有八个 BattleSession 与 VisitSession membership/revision 保持不变

#### Scenario: Intentional loss 扩大客户端收包间隔

- **WHEN** fault case 丢弃连续 snapshot，使客户端相邻完整 snapshot receipt gap 超过 150 ms，但所有实际投递 packet 均在 gateway delivery budget 内
- **THEN** delivery-age 指标继续以实际投递 packet 的 gateway age 裁决，客户端 gap 只进入 cadence/baseline recovery 诊断，不能伪造 delivery-age 失败或通过

### Requirement: 生命周期、NAT 与长时运行必须保持 current binding

Mandatory lifecycle matrix MUST 覆盖合法 endpoint rebind、伪造 rebind、短时 pause/resume、network loss 后重新 admission、Visitor reconnect/leave、Owner grace、assignment replacement、Session epoch 失效、child crash/restart、Go restart、drain/shutdown 和至少 30 分钟的 5-actor soak。Soak MUST 跨越至少两次 10 分钟 rekey deadline，并持续校验唯一 current endpoint/session/target/mapping generation、单调 packet/Input/Server Tick、bounded queue/memory 与无 nonce reuse；旧 ticket、key epoch、endpoint、assignment、target 或 actor binding MUST 不可复活。

#### Scenario: NAT mapping 在 active session 中变化

- **WHEN** fault gateway 为持有 current key 的客户端切换 upstream mapping，客户端完成 cookie 与 authenticated rebind confirm
- **THEN** session 递增 endpoint generation 并继续原 replay/KCP/actor timeline，旧 mapping 后续 packet 全部拒绝

#### Scenario: Assignment replacement 与旧流量并发

- **WHEN** current assignment 被 successor 替换且 gateway 继续投递 predecessor 的延迟、重排或重复 packet
- **THEN** predecessor 不再推进 simulation、snapshot 或 result，报告能以 assignment/session/tick 关联稳定拒绝原因

#### Scenario: Soak 出现无界增长

- **WHEN** 30 分钟运行中 process memory、queue、pending handshake、KCP state 或 replay state 超过登记 hard limit或无法在 cleanup deadline 内回收
- **THEN** soak gate 失败并保留低敏资源证据，不能用进程退出掩盖泄漏

### Requirement: 安全负例与攻击流量不得突破 B0.5 边界

资格 MUST 通过真实 fault path 重放 versioned 安全 negative corpus，覆盖 spoofed source、无 cookie amplification、ticket/proof forgery、ticket replay、ciphertext/AAD tamper、duplicate/too-old/future sequence、wrong direction/lane、malformed/oversize flood、KCP expiry、rebind hijack、old epoch 和 rate/queue exhaustion。攻击流量 MUST 与合法五人 workload 并发验证 availability；未认证流量不得创建 BattleSession/KCP/actor queue，已认证恶意流量不得覆盖身份、assignment、Tick、伤害、死亡、奖励或持久事实。

#### Scenario: Malformed flood 与合法玩家并发

- **WHEN** 多 remote identity 按 manifest 预算发送 malformed、cookie-less、replay 和 oversize datagram，同时五个合法 actor 继续 gameplay
- **THEN** 攻击流量在对应安全/资源边界被拒绝，合法 workload 仍满足登记 availability budget，响应总量不违反抗放大限制

#### Scenario: Negative corpus 缺少 mandatory attack

- **WHEN** report 未执行任一登记攻击类别，或 runner 把失败/unsupported 标记为 skipped 后继续
- **THEN** completeness gate 失败且整体 not-qualified

### Requirement: 资格证据必须低敏、完整、可审计且清理干净

项目的统一质量入口 MUST 将 battle qualification 的 `validate`、定向 `diagnose`、完整
`verify`、长时 `soak` 与 `finalize` 作为内部 owner actions 暴露。Change-level
`impact`、`check-change` 与 `diagnose` MUST NOT 产生资格结论；只有用户对 clean frozen
candidate 显式调用最终 `qualify`，才可连续消费同一 identity 下两次完整 verify、
mandatory soak、安全矩阵、当前 mandatory capability regression 和 OpenSpec strict
evidence，生成 `battle-network-qualified-windows-x64-controlled` 报告。报告 MUST 绑定
全部 source/binary/config/environment/fault/workload digest，逐场景记录 measured value、
unit、budget、disposition、run evidence digest、cleanup 和 scope；missing、failed、
skipped、stale、unsupported、unclassified 或 cleanup failure 任一存在时 MUST
not-qualified。Tracked corpus 与报告不得包含 raw ticket、proof/traffic key、cookie
secret、完整 credential、玩家资料、payload dump 或本机绝对路径。

Qualification tooling change MAY 用 validators、failure regression、scope/security tests
和代表性真实场景证明工具可用，而不为变化中的开发工作区生成 qualified report；这不得把
未运行的完整矩阵标记为通过，也不得降低未来最终资格的 mandatory coverage。

#### Scenario: 连续运行结论不一致

- **WHEN** 显式最终资格的两次完整 verify identity 相同但任一 mandatory scenario disposition 不同、测量超出可重复容差或 cleanup 不一致
- **THEN** finalize 拒绝生成 qualified report，并要求保留两个低敏 run digest 供定位

#### Scenario: 全部门禁通过

- **WHEN** 用户显式冻结的 candidate 通过两次 verify、mandatory soak、安全负例、当前 mandatory regression、strict validation 与 cleanup
- **THEN** 报告可声明 `battle-network-qualified-windows-x64-controlled`，但不声明 Linux、公网运营商、未包含的 Unity runtime 或产品内容已 qualified

#### Scenario: Qualification tooling 已完成但未执行最终资格

- **WHEN** corpus、gateway、独立协议客户端、runner、failure regression、代表性真实场景和统一显式入口均通过，而用户尚未请求完整最终资格
- **THEN** tooling change 可以完成且报告保持未生成或 not-qualified，完整矩阵、连续 verify 与 soak 继续作为可随时执行的最终动作
