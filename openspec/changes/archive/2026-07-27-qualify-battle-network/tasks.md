## 1. 冻结 B0.6 资格契约

- [x] 1.1 建立 `shared/contracts/fixtures/battle/qualification/` 的 closed schemas、manifest、README 与 qualification version，登记所有文件、owner、排序和低敏边界。
- [x] 1.2 建立 upstream binding，绑定 B0.1 model、B0.2 profile/fault report、B0.3 binary receipt、B0.4 control、B0.5 wire/registry/config/report 与精确 dependency/toolchain digest。
- [x] 1.3 建立 fault execution overlay，将 B0.2 的 12 个 scenarios、required impairment/workload/phase、seed、cadence、MTU、queue逐项映射为显式 warmup、measurement、deadline 和 cleanup 预算。
- [x] 1.4 建立 1/5/8 actor workload 与第 9 actor/33 人 compatibility 契约，冻结 movement、boss-burst、reliable event、baseline/resync 和输入 cadence，不新增 gameplay 规则。
- [x] 1.5 建立 metric catalog 与 reproducibility tolerance，逐项登记 value/unit/source/method/window/budget/worst-case/disposition，使用 gateway 实际投递时长度量 delivery age，并覆盖带宽、client cadence 诊断、recovery、KCP amplification、queue、CPU/Tick debt 和 memory。
- [x] 1.6 建立 security、NAT/lifecycle 与 30 分钟 soak manifest，覆盖攻击速率硬上限、rebind/rekey、pause/resume、reconnect、assignment/session/process replacement 和至少两次 rekey。
- [x] 1.7 实现 corpus validator 与只读连续校验，拒绝未知/未登记文件、coverage 缺口、隐藏默认值、预算放宽、secret/绝对路径和 source 改写。

## 2. 增加 qualification-only control metrics

- [x] 2.1 扩展 simulation-control schema、message inventory 与 canonical golden，新增 `battle_qualification_snapshot_request/receipt`、run identity、sample sequence 和 closed low-sensitive metric fields。
- [x] 2.2 在 C++ transport/simulation owner 中聚合 raw/KCP bytes/packets、drop/reject/expiry/retransmit、queue high-watermark、Tick duration/debt、rebind/rekey 和 close reason，不复制 gameplay 最终事实。
- [x] 2.3 在 C++ control server 实现只读 snapshot，校验 qualification mode、bootstrap nonce、frame sequence、run/node/instance identity，并保证读取不清零、不暂停或修改 runtime。
- [x] 2.4 在 Go simulation-control session 增加 typed snapshot request/receipt、deadline、correlation 和低敏投影，拒绝旧 node/run、unknown field、乱序和超限 frame。
- [x] 2.5 扩展严格配置与 process bootstrap，使 qualification mode/run identity 只能由资格入口显式启用，production/default 配置稳定拒绝 snapshot request。
- [x] 2.6 增加 Go/C++ contract、unit、negative、concurrency 和 secret tests，验证连续读取只推进 sample sequence，错误身份不泄漏 counters，且不创建 listener/文件管理面。

## 3. 建立独立 C++ battle 协议客户端

- [x] 3.1 新增 `ihomeland-battle-protocol-client` test executable 与 CMake target，只链接锁定的 crypto/KCP/Protobuf primitives 和 generated contract，并以 architecture gate 禁止链接 production transport/simulation/gameplay adapters。
- [x] 3.2 冻结资格客户端 stdin/stdout closed frame、64 KiB 上限、sequence/deadline/secret/redaction 规则，使 ticket secret 只经继承 stdin 进入且 stdout 只返回低敏事件。
- [x] 3.3 按 canonical fixture 独立实现 ClientHello/Retry/ClientAuth/ServerAccept、ticket transcript proof、X25519/HKDF/ChaCha20-Poly1305 和握手 retry/expiry。
- [x] 3.4 独立实现 48-byte secure header、direction key/nonce、packet sequence、256-packet replay window、AEAD open/seal 与 wrong direction/epoch/sequence 拒绝。
- [x] 3.5 实现 raw lane encode/decode、input bundle/probe、full/delta snapshot、baseline identity、LastProcessedInputTick 和 bounded resync state，不接受客户端权威 transform/hit/damage/reward。
- [x] 3.6 基于锁定 KCP primitive 实现独立 client adapter，精确使用 10 ms update、window 64、fast resend 2、RTO 30–200 ms、1000-byte ceiling、queue 64，以及 registry 驱动的 500 ms reliable event/lifecycle 与 2250 ms resync sender expiry。
- [x] 3.7 实现 authenticated endpoint rebind、endpoint generation、10 分钟/`2^20` packet rekey、3 秒 previous epoch overlap 与稳定 close state。
- [x] 3.8 增加 wire/crypto/KCP canonical parity、malformed/expiry/replay/rebind/rekey negative、stdin/stdout secret 和 production-adapter link-scope tests。

## 4. 实现 opaque UDP fault gateway

- [x] 4.1 在 Go qualification tool 中实现每客户端独立 frontend/upstream UDP mapping、固定 buffer、单 owner reader/writer、global/per-client hard limits 和有界关闭。
- [x] 4.2 实现固定 PRNG 与稳定 `(dueTime, direction, clientSlot, receiveSequence, copyIndex)` scheduler，分别处理 uplink/downlink latency、jitter、loss、burst、duplicate 与 reorder。
- [x] 4.3 实现方向独立 bandwidth token bucket、pause/resume、MTU/oversize disposition、queue pressure 和 deadline expiry，所有参数只读取 manifest。
- [x] 4.4 实现 NAT mapping rotation 与旧 upstream 有界迟到投递，验证真实 rebind cookie/confirm 后只有 successor endpoint 可用。
- [x] 4.5 生成不含 payload/secret/IP 的 packet metadata evidence，记录长度、公开 packet kind、规则 disposition、单调时间和 correlation 摘要，并与发送/接收计数守恒。
- [x] 4.6 增加确定性 scheduler、双向 coverage、bandwidth arithmetic、NAT/rebind、unsupported environment、资源上限、opaque payload 和 cleanup tests。

## 5. 建立 Go 黑盒 harness 与真实业务入口

- [x] 5.1 新增 `server/cmd/battlequalificationtool` 和 import allowlist，禁止导入 production battle transport、simulation gameplay 或 application service 实现。
- [x] 5.2 实现真实 HTTPS register/login/world bootstrap/VisitSession invite-accept-join 与 BattleTicket issuance client，覆盖 Owner、Visitor、幂等 response-loss 和 8/9 actor。
- [x] 5.3 实现 C++ protocol client supervisor，经继承 stdin 交付一次性 credential、验证 exact binary/build identity、解析低敏事件并在 deadline/异常退出时 fail closed。
- [x] 5.4 实现 gateway、client slot、BattleSession generation、AssignmentStamp/SimulationInstance 摘要与 workload phase 的 run-local correlation，不保存 PlayerID、remote endpoint 或完整 binding。
- [x] 5.5 实现 20 Hz/40 Hz 冻结 cadence 的 movement/combat/boss-burst/idle/input-gap workload driver，以及 committed Tick 驱动的 10 Hz snapshot、周期 full baseline、resync/reliable event 验收断言。
- [x] 5.6 实现 qualification control snapshot 与 OS process CPU time、working set、handle/thread 的定期采样，并与 gateway/client 证据按 sample window 交叉核对。
- [x] 5.7 实现 Visitor reconnect/leave、Owner grace、Session invalidation、assignment replacement、child crash/restart、Go restart、drain/shutdown 的受控触发与 successor identity 解析。
- [x] 5.8 增加 harness unit、contract、race/fuzz、deadline、response-loss、redaction、stale callback、partial startup rollback 和 cleanup tests。

## 6. 提供 fault、容量、安全与长时资格能力

- [x] 6.1 用一个 `baseline-gap` 真实代表场景证明 fault runner 可工作，并以 contract/failure regression 验证完整 12-scenario manifest、调度与最终 `qualify` 编排；不在 tooling change 内重复执行 clean、loss/reorder、disconnect-drain 等完整矩阵。
- [x] 6.2 用五人默认协作真实代表场景验证容量采样链，并以 contract test 验证 1/8 actor workload 与第 9 actor 拒绝登记完整；完整容量曲线留给显式最终资格。
- [x] 6.3 实现 security-availability runner，使 spoof/cookie-less/forgery/replay/tamper/wrong lane/sequence/oversize/KCP expiry/rebind hijack/old epoch/flood 与合法五人流量并发。
- [x] 6.4 用一个合法 endpoint rebind 真实代表场景验证 lifecycle runner，并以 contract/failure regression 验证 Visitor reconnect、process replacement 与完整 lifecycle dispatch 登记；完整 lifecycle matrix 留给显式最终资格。
- [x] 6.5 提供 30 分钟五人 soak action、跨两次 rekey 的 manifest、指标与 cleanup 判定，但不要求在 tooling change 完成时实际运行。
- [x] 6.6 通过 catalog validator、failure regression 与代表性 evidence 验证 bandwidth、delivery age、cadence、recovery、KCP amplification、queue、CPU/Tick debt、memory 和 availability 的 source/method/budget/disposition 均可裁决；完整逐场景测量留给显式最终资格。

## 7. 建立唯一资格入口与报告

- [x] 7.1 实现 `tools/battle-qualification/battle-qualification.ps1` 的 `validate`、`diagnose`、`verify`、`soak`、`finalize` 动作、全局/阶段/cleanup deadline 和隔离 `.local/battle-qualification/<run-id>/` ownership。
- [x] 7.2 在 `verify` 中按 entry gate → dependency/build → storage/Go/C++ → matrix → regression → cleanup 顺序执行，首个失败停止资格但仍完成独立 cleanup。
- [x] 7.3 实现 run evidence schema 与规范化聚合，绑定 source/binary/toolchain/dependency/model/profile/control/wire/config/environment/fault/workload identity 和四源 measurement digest。
- [x] 7.4 实现两次 verify reproducibility gate，比较完整 coverage、disposition、metric tolerance 和 cleanup，以 min/max/worst-case 保留两份 run digest而不平均掩盖失败。
- [x] 7.5 实现 `finalize` completeness gate；missing/failed/skipped/stale/unsupported/unclassified、identity 不同或 cleanup failure 任一存在时只生成 not-qualified。
- [x] 7.6 提供按需生成低敏 `battle-network-qualified-windows-x64-controlled` report 与 B0.2 只读 overlay 的能力；tooling change 不要求当前工作区实际生成 qualified report。
- [x] 7.7 实现 secret/cache/scope scanner，拒绝 raw ticket、proof/traffic/cookie key、credential、PlayerID、payload dump、IP/绝对路径、tracked run output、Unity runtime 和第二 listener。

## 8. Failure regression 与分层验证

- [x] 8.1 使用临时 corpus 副本验证上游 digest、manifest/file registry、scenario/phase/impairment coverage、seed、direction 和 actor workload 漂移全部 fail closed。
- [x] 8.2 验证降低 loss/jitter/duration、提高 budget/tolerance、删安全开销、改变 lane/KCP/MTU 或把 8 actor 降为 5 actor 均不能生成通过报告。
- [x] 8.3 验证 unsupported 伪装 pass、metrics/source 伪造、旧 run 复用、diagnose evidence 混入、cleanup 伪造与 secret 注入均被 finalize 拒绝。
- [x] 8.4 验证 production mode qualification snapshot、client 链接 production adapter、gateway 解密/改写 payload、额外 listener/port 和 Unity gameplay runtime 文件触发 scope gate。
- [x] 8.5 执行 qualification corpus、protocol client、gateway、scope/security owner checks，并只运行 validation plan 登记的 fault/capacity/security/lifecycle 四个代表性 real child/socket 场景；全量 race/fuzz/ASan 由显式最终资格运行。

## 9. Tooling、定向验证与文档收口

- [x] 9.1 通过统一 `check-change` 运行 profile、wire、control、qualification validators 与本 change strict，不重跑历史 B0.3/B0.4/B0.5 完整资格。
- [x] 9.2 通过统一 validation plan 运行 Go/C++/protocol-client/gateway 的直接影响面 checks，以及生成物、注释、secret 与 cache gates；不得为勾选本任务扩张到 server/client 完整资格。
- [x] 9.3 以 closed orchestration contract 验证统一 `qualify` 对 clean frozen candidate 编排两次 verify、mandatory soak 与 finalize，并实际验证 dirty worktree 稳定拒绝；本 change 不执行完整最终资格。
- [x] 9.4 更新 `docs/architecture.md`、`docs/gameplay-simulation-architecture.md`、`docs/network-transport-architecture.md`、`docs/network-port-allocation.md`、`docs/protocol-compatibility.md`、`docs/file-structure.md`、`docs/technology-versions.md` 和 `docs/roadmap.md`，明确 controlled scope、owner、资格结果和 B0.7 门。
- [x] 9.5 更新 qualification/runbook 文档，说明环境探测、单场景诊断、完整 verify/soak、evidence 保留、cleanup、失败定位、回滚和禁止扩大结论。
- [x] 9.6 执行格式化、静态/architecture/scope/comment、定向 tests 与 `openspec validate qualify-battle-network --strict`，逐项审计 tooling requirement/scenario 并核对主 specs 已同步。
