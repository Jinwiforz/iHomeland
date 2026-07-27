## Context

B0.1/B0.2 已冻结 20 Hz simulation、40 Hz input、10 Hz snapshot、1200-byte datagram、raw/KCP 唯一 lane、KCP 参数、12 个 fault scenarios、默认 5 actor、8 actor hard qualification cap 与带宽/CPU/memory/queue 预算。B0.3/B0.4 已交付可资格的 Windows x64 C++ core 和 Go/C++ 私有 control，B0.5 又完成 BattleTicket、真实 C++ UDP listener、cookie/X25519/HKDF/AEAD、replay、rebind、rekey、raw/KCP 与 loopback implementation qualification。

当前缺口不是再实现一套 transport，而是提供可按需证明 exact implementation 在受控真实网络故障、NAT mapping 变化、长时运行、攻击流量和 1/5/8 actor 负载下仍符合 B0.2 的工具。B0.6 tooling 是后续功能开发的 development-readiness 基础；完整矩阵报告是用户显式最终验收的消费者。运维、网络、服务端模拟和客户端协议 owner 都需要能从同一低敏 evidence 定位 lane、session generation、assignment 和 Tick，而不能依赖 raw secret、payload dump 或不可重放的人工结论。

## Goals / Non-Goals

**Goals:**

- 建立唯一、版本化、fail-closed 的 B0.6 资格入口和 evidence contract。
- 用独立黑盒协议客户端驱动真实 Go parent、B0.5 C++ child、Redis ticket 与 UDP/KCP wire。
- 提供对 B0.2 fault matrix、NAT/rebind、lifecycle、安全攻击、30 分钟 soak 和 1/5/8 actor 容量给出机器可读结论的能力；tooling change 只运行代表性场景。
- 在不解密 fault gateway 流量的前提下，交叉关联 gateway、client、C++ control metrics 与 OS process 测量。
- 维持 model/profile/wire corpus 只读，并让任何 identity、coverage、预算或 cleanup 漂移阻止资格。

**Non-Goals:**

- 不实现 Unity gameplay runtime、客户端预测/校正/插值、Actor View、Cinemachine、HUD 或产品内容。
- 不修改 gameplay model、numeric route、wire layout、crypto suite、MTU、KCP 参数、BattleTicket 或现有 production listener。
- 不声明 Linux、Unity IL2CPP、真实公网运营商、云跨区或移动网络已经 qualified。
- 不新增公网管理端口、gRPC、远程 Go/C++ control、packet capture 服务或持久数据库 schema。
- 不把 33 人 VisitSession compatibility 转换为 33 actor battle capacity，也不修改现有 membership。

## Decisions

### 1. 使用独立 qualification corpus，以上游 digest 建立只读 overlay

新增 `shared/contracts/fixtures/battle/qualification/`，包含 closed schemas、manifest、upstream binding、fault execution overlay、workloads、security corpus、environment contract、report schema 和 failure-regression cases。它引用并验证 B0.1 model、B0.2 profile、B0.4 control、B0.5 wire 与 qualification report 的完整 digest，不复制或改写这些 source。

实际 stdout/stderr、进程采样、packet metadata 和阶段 evidence 写入 ignored `.local/battle-qualification/<run-id>/`。Tracked corpus 只保存低敏规则和示例，不保存一次运行的 endpoint、PID、凭据、payload 或绝对路径。

选择独立 overlay 而不是扩写 `network-profile/reports/qualification.json`，因为 B0.2 是确定性逻辑模型，B0.6 是平台相关真实实现测量；混写会让逻辑 profile 与 Windows 实现 evidence 互相污染。

### 2. 由一个 PowerShell owner 编排，Go harness 与独立 C++ 协议客户端分工

`tools/battle-qualification/battle-qualification.ps1` 是 battle 资格内部 owner，由项目级 `tools/quality/quality.ps1` 统一公开；其动作固定为：

- `validate`：只读校验 schema、双向 registry、upstream identity、环境能力与 secret/cache policy；
- `diagnose`：只运行一个闭合场景，输出不得进入 finalize；
- `verify`：恢复依赖、构建 exact binaries、启动隔离 storage/Go/C++、执行 mandatory 自动矩阵并清理；
- `soak`：执行 30 分钟五人 lifecycle/rebind/rekey/queue/memory 运行；
- `finalize`：只消费同一 identity 下连续两次 verify、soak、回归与 strict evidence。

新增 `server/cmd/battlequalificationtool` 作为黑盒 harness。它只能通过公开 HTTPS、受控 qualification control 和子进程 stdio contract 驱动系统，不得导入 production battle transport、simulation 或 application service 实现；它负责账号/world/visit/BattleTicket 流程、workload 编排、UDP gateway、进程采样和 evidence 聚合。

新增只用于测试的 `ihomeland-battle-protocol-client` C++ executable，通过有界 stdin/stdout frame 接受一次性 ticket binding 并返回低敏事件。它只链接锁定的 crypto/KCP/Protobuf primitives和 generated contract，不链接或包含 production `authenticated_handshake`、`secure_datagram`、`battle_session`、`authenticated_multiplexer` 或 simulation gameplay 实现；客户端按 canonical wire fixture 独立实现 handshake、raw/KCP、rebind、rekey 和 close，architecture test 对 link/import graph 做白名单检查。

选择 Go harness 加独立 C++ client，而不是在本 tooling change 中提前创建 Unity `BattleNetworkClient`；同时复用已锁定的底层 crypto/KCP dependency，避免为了资格引入第二套未经治理的实现依赖。禁止复用 production transport adapter 可减少 client/server 共用同一封装错误。现有 C# fixture parity 继续作为 B0.5/B0.6 regression，而不承担 runtime qualification。

### 3. 通过 advertised endpoint 前置 user-mode UDP gateway 注入双向故障

C++ listener 绑定隔离 backend loopback endpoint；BattleTicket 的 advertised endpoint 指向资格工具拥有的 gateway。Gateway 为每个逻辑客户端创建独立 upstream socket，因此 C++ 仍观察到独立 remote mapping；gateway 将 packet 视为 opaque bytes，只读取 UDP 长度和 B0.5 明文 secure header 中允许用于低敏统计的 version/packet-kind/session摘要字段，不解密或改写 payload。

每个方向使用 manifest 指定的固定 PRNG、单调时钟和稳定 `(dueTime, direction, clientSlot, receiveSequence, copyIndex)` 排序，执行 latency、jitter、loss、burst、duplicate、reorder、bandwidth、pause 和 MTU disposition。切换某个 client 的 upstream socket建模 NAT mapping 变化，并经真实 rebind cookie/confirm 收敛；旧 socket 保留有界时间以投递迟到包并验证旧 endpoint 拒绝。

Gateway 创建 successor 后先保持 pending。客户端 uplink 的公开 `Control` kind 不能证明 C++ backend 已接受 rebind，因此不得触发 mapping 提交；只有 connected successor socket 观察到 backend 返回的 `Control` 响应后，唯一 event loop 才能原子提升 successor，并从该时刻开始 predecessor 的有界迟到窗口。独立协议客户端的 transition event 同时冻结 operation 与阶段化失败码，编排器必须拒绝跨 operation 组合，避免用底层异常文本或反复试跑定位 rebind、rekey、close 与 old-epoch 失败。

Measurement 结束后只关闭新的 fault scheduling，已经进入 scheduler 的 packet copy 仍按原始 due time、lifetime 与 disposition 自然终结。Gateway event loop 在 pending copy 归零后完成 quiescence barrier，runner 才建立 control/gateway 结束游标；禁止固定 sleep、清空队列或提前改写 packet 终局。随后 authenticated close、关闭后 control 采样和逆序资源释放共同消费 manifest 的一个独立 cleanup deadline，不继承 scenario deadline，也不为子阶段重新计时。只有 `CloseAcknowledged` 已成功进入 production listener output queue，C++ runtime 才登记 normal close；enqueue 失败必须使用非 normal 稳定原因终结。

Close control 仍是 UDP，因此单次 request/ack 不能成为 cleanup 成功假设。独立客户端在同一冻结 deadline 内按固定 receive slice 重放 exact sealed `CloseRequest`；服务端在销毁 session 前排队三个独立认证的 `CloseAcknowledged` copy。重试不得重置 cleanup deadline，任一 output enqueue 失败仍 fail closed。该策略只为 terminal control 提供有界冗余，不改变 fault measurement window，也不把 close 引入 KCP 或第二 listener。

该拓扑利用 B0.5 已冻结的 bind/advertised 分离，不需要 WinDivert/WFP、管理员权限或 production hook。代价是结论只适用于 `controlled-local-fault-gateway` environment class；真实公网/运营商实验室仍是独立部署证据。

### 4. 将 profile conformance、容量、安全和 lifecycle 分成不可替代的 gate

资格 manifest 包含四类 gate：

1. `profile-fault`：完整映射 B0.2 的 12 个 scenarios 和所有 required impairment/workload/phase；默认五人逐项运行，profile 的短 pattern 在显式 warmup 后重复到登记 measurement window，不改变其参数与 seed stream。
2. `capacity-curve`：1/5/8 actor 在 clean、movement-heavy、boss-burst、KCP retransmit 与 queue pressure 下测量；第 9 actor 只验证 admission gate，33 人只验证 VisitSession compatibility 不被修改。
3. `security-availability`：伪造、重放、tamper、amplification、malformed flood、rebind hijack、old epoch 与资源耗尽同合法五人 workload 并发。
4. `lifecycle-soak`：Visitor reconnect/leave、Owner grace、assignment replacement、Session invalidation、child/Go restart、pause/resume、rebind、至少两次 rekey 与 30 分钟资源稳定性。

四类结果分别记录但总体使用 AND 语义。选择分层 gate 是为了区分“默认玩法在弱网失败”“8 人容量失败”“安全攻击影响可用性”和“生命周期泄漏”，同时防止只展示 clean happy path。

### 5. 使用四源测量并以稳定 correlation key 合并

- Gateway 记录方向、client slot、packet kind、长度、scheduled/delivered/dropped/duplicated disposition 和单调时间。
- 黑盒 client 记录 handshake/rebind/rekey、InputTick、ServerTick、snapshot sequence、baseline/resync、KCP application completion、close reason 与 workload phase。
- C++ child 经私有 control 返回 qualification-only 低敏累计和 high-watermark，包括 raw/KCP bytes/packets、retransmit、drop/reject/expiry、queue、Tick duration/debt、rebind/rekey 与稳定 close reason。
- Orchestrator 采样 Go/C++ process CPU time、working set、handle/thread count，并绑定 binary/process incarnation。

合并键只使用 run-local client slot、BattleSession 摘要、session/endpoint generation、AssignmentStamp fingerprint 摘要、SimulationInstance 摘要和 Tick；禁止 PlayerID、remote IP、ticket、key 或 payload。每个指标在 report schema 中同时登记 value、unit、method、budget、sample window 和 disposition。

选择受控 control snapshot 而不是解析日志，是因为日志不是稳定 API，也不能保证 queue/KCP/Tick 指标的原子一致性。Snapshot 只在显式 qualification mode 可用、只读且不清零，production request 必须拒绝；不新增 listener 或文件轮询。

### 6. 真实测量允许数值波动，但结论与覆盖必须可复现

每次 run 产生独立 evidence digest。连续两次 `verify` 不要求 CPU、调度延迟等原始数值逐字节相同，但必须满足：

- source/environment/fault/workload identity 完全相同；
- mandatory scenario 与 packet disposition coverage 完整；
- 每个 disposition 相同；
- 测量均在预算内，并且两次差异不超过 manifest 登记的 metric-specific reproducibility tolerance；
- cleanup outcome 相同且没有遗留进程、listener、container 或可复用 credential。

`finalize` 对两个 run 的规范化摘要稳定排序，保留各自 evidence digest 和 min/max/worst-case，不以平均值掩盖单次超限。选择容差判定而不是要求报告字节相同，是因为真实 OS scheduling、CPU 和 socket timing 本身非确定；要求数值完全一致会诱使工具伪造或舍弃真实测量。

Client v1 regression 使用单一 `ClientContractIdentity` owner 计算其实际消费的 proto、HTTP/registry、admission/realtime/client-qualification/battle-wire fixture、simulation-control runtime baseline、Unity package 与 Editor version 身份。B0.3/B0.4/B0.6 的服务端资格报告、network profile/model corpus 和可推导 binding 不属于 Unity client contract，禁止把这些证据纳入 digest 形成循环依赖；client qualification 生成端与 B0.4 消费端必须调用同一 owner，不能复制 path list 或各自解释范围。

### 7. 资格结论严格限定为 Windows x64 controlled environment

最终 label 固定为 `battle-network-qualified-windows-x64-controlled`，并显式记录 `publicInternetQualified=false`、OS/build、CPU class、loopback gateway topology、timer resolution 与权限能力。任何 mandatory gate 为 missing、failed、skipped、stale、unsupported、unclassified，或 cleanup 失败，整体均为 not-qualified。

该 label 只表示显式最终候选在 Windows x64 controlled environment 的 battle network 资格。B0.7 仍需验证 Unity/IL2CPP crypto、socket、prediction 与表现；未来 Linux 或公网资格必须用新 environment contract 和独立 evidence，不能重命名或扩大本报告。B0.6 tooling 与代表性场景通过可以解锁后续功能开发，但不得伪装成该 label。

### 8. 失败回归必须证明 runner 不能自我放宽

Failure regression 使用临时 corpus 副本依次破坏 upstream digest、删 scenario、降低 impairment/duration、交换 uplink/downlink、改变 actor 数、提高预算、跳过 unsupported、伪造 metrics、泄漏 secret、复用旧 run、标记 cleanup pass 和注入 Unity runtime 文件。每种 mutation 必须被稳定 gate 拒绝且不得改写 tracked source。

这比只测试 happy-path schema 更重要，因为资格工具本身是发布门 owner；如果 runner 能静默缩小矩阵，真实 transport 的绿色测试没有治理意义。

## Risks / Trade-offs

- [User-mode gateway 不等同真实公网/NAT 设备] → 在 label 和 environment contract 中明确 controlled scope；保留后续公网/运营商实验室 change，不把本结论外推。
- [C++ 黑盒 client 与未来 Unity 实现不同] → B0.6 只冻结 wire/network envelope，继续运行 C# parity；B0.7 必须重新验证 Unity/IL2CPP provider、socket 和 gameplay replica。
- [Qualification control snapshot 扩大内部协议] → 只允许显式 qualification mode、current run/node/sequence，只返回 closed low-sensitive counters；production 稳定拒绝并由 scope tests 保证无 listener。
- [长矩阵与 30 分钟 soak 增加本地/CI 时间] → 开发期只运行 change 影响面和代表性 `diagnose`；完整 `verify`/`soak` 仅由用户显式最终资格触发，触发后不允许缩减 mandatory coverage。
- [OS scheduling 造成测量噪声] → 登记 warmup、sample window、CPU class 和 metric-specific tolerance；使用两次 run 的 worst-case，任一预算超限都失败。
- [加密流量难以从 gateway 判断 application semantics] → Gateway 只负责 packet/byte/fault evidence，client 和 C++ snapshot 分别证明 application completion 与内部 queue/KCP/Tick；三方计数不一致即失败。
- [攻击 flood 可能影响开发机] → Manifest 对 sender、rate、duration 和总 bytes 设置硬上限，run directory、process、port 与 container 全部有 owner 和独立 cleanup deadline。
- [Qualification 发现冻结 profile 不可达] → B0.6 保持 not-qualified并输出最小低敏反例；不得在本 change 内自动放宽，后续以独立 OpenSpec 重新评审 profile/wire。

## Migration Plan

1. 先落地 qualification corpus、schemas、upstream binding、环境探测和 failure regression，不启动 runtime。
2. 增加 qualification-only control snapshot及 Go/C++ golden/negative tests，确认 production mode 拒绝且 B0.4/B0.5 regression 不漂移。
3. 实现 Go black-box harness、opaque UDP gateway 与不链接 production transport 的独立 C++ protocol client，以单 actor clean、cookie/handshake、raw/KCP、rebind/rekey 逐层验证。
4. 接入真实 storage、Go Composition Root 与 C++ child，以 clean、baseline-gap、loss/reorder、1/5 actor、安全和 lifecycle 代表性场景证明各 runner、metric 与 cleanup 能力。
5. 验证项目级统一入口能拒绝 dirty candidate 并编排两次完整 verify、30 分钟 soak、mandatory capability suites 与 finalize；本 tooling change 不实际执行昂贵最终资格。
6. 同步主 specs 和路线图后归档；tooling 与代表性 development readiness 允许后续功能开发，完整 qualified label 只在用户显式最终验收时生成。

回滚时删除未归档 qualification tooling/control extension，停止所有 qualification-owned process/gateway/container 并清理 ignored run directories；不迁移 MySQL/Redis，不回滚或复用 production message ID。若 control frame 已进入共享基线，则保留其 ID 为 reserved/production-disabled，不能让旧 binary 将其解释为其他命令。

## Open Questions

无阻塞问题。真实公网/运营商拓扑、Linux runner、Unity IL2CPP 客户端和生产放量阈值明确留给后续独立 change；它们不改变本次 Windows x64 controlled B0.6 的实现与验收边界。
