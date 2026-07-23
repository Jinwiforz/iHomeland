## 1. 建立 network profile 源契约

- [x] 1.1 在 `shared/contracts/fixtures/battle/network-profile/README.md` 定义 owner、format/profile version、逻辑 message kind、单位、canonical JSON/SHA-256、资格分类、低敏规则和后续 C++/transport/Unity consumer 边界。
- [x] 1.2 创建严格 `schema.json` 与 `manifest.json`，约束 model binding、profile 参数、message inventory、fault matrix、case/report 结构、稳定排序、双向登记和 digest，拒绝 unknown field、非法单位、溢出与本机绝对路径。
- [x] 1.3 创建 `model-binding.json`，登记并重算 `battle-model-v1` manifest、assumptions、全部 case digest 与 required coverage，确保 model 漂移或现有 model validator 失败时 fail closed。
- [x] 1.4 创建 `profile.json`，为 cadence、window、MTU、baseline、KCP、capacity、CPU/memory/history/queue/bandwidth 参数登记值、单位、workload、evidence 与 `target_budget`、`profile_qualified`、`implementation_required` 状态。

## 2. 冻结消息 lane、MTU 与网络矩阵

- [x] 2.1 创建 `message-inventory.json`，为 input bundle、full/delta snapshot、probe、entity lifecycle、重要 ability event 与 resync 登记 owner、direction、唯一 raw/KCP lane、QoS、size/rate、expiry、Tick/sequence、幂等、baseline 和 recovery policy。
- [x] 2.2 创建 checked MTU/payload budget，显式预留 IP/UDP、future secure session、AEAD tag 与 lane header 开销，并为超限 bundle/delta 登记 bounded split 或稳定拒绝，禁止 IP 分片和 snapshot 转 KCP。
- [x] 2.3 创建 `fault-matrix.json`，以固定 seed 覆盖 clean、上下行 latency/jitter、loss/burst、reorder、duplicate、baseline gap、KCP retransmit、MTU、queue saturation、slow consumer 与 disconnect/drain。
- [x] 2.4 为 solo Owner、默认 1 Owner + 4 Visitor 和 1 Owner + 32 Visitor compatibility 的全部 workload phases 建立 cadence、baseline、reliable、MTU、overload 与 negative cases，并登记 mandatory coverage。

## 3. 实现确定性 profile validator 与 simulator

- [x] 3.1 新增 `tools/battle-network-profile/battle-network-profile.ps1` 的非交互 `validate` 入口，先调用 battle model gate，再验证 schema/manifest/binding、lane 唯一性、coverage、budget arithmetic、qualification completeness、canonical LF/UTF-8 与 SHA-256。
- [x] 3.2 实现 `simulate` 的整数离散事件内核，按固定 PRNG 和 `(delivery_time, direction, lane, sequence, copy_index)` 排序处理 delay/loss/reorder/duplicate/burst/queue，且不执行 gameplay、真实 socket 或 wall clock 逻辑。
- [x] 3.3 实现 cadence/window candidate evaluator，验证固定 `dt`、InputTick mapping/gap/expiry/hold、bundle redundancy、history 与 drift/reset，并按规范目标和参数 tuple 产生稳定 selected/rejected 结果。
- [x] 3.4 实现 full/delta baseline 与 resync evaluator，覆盖 snapshot age、旧 sequence 丢弃、baseline fan-out/maximum age、恢复 deadline、interpolation/extrapolation 和 correction tolerance。
- [x] 3.5 实现有限 KCP/ARQ profile evaluator，验证 deadline delivery、ordered blocking、duplicate suppression、retransmit amplification 与 queue watermark，同时把真实 KCP adapter parity 保持为 `implementation_required`。
- [x] 3.6 实现 per-player/per-instance bytes、queue、history/memory 与 CPU target budget 聚合，分别输出默认/兼容容量结论和显式 `capacity_gate_required`，不得修改当前 Go VisitSession admission。

## 4. 生成并冻结 profile evidence

- [x] 4.1 运行完整候选和 fault matrix，生成低敏 canonical `reports/qualification.json`，包含 source/tool/seed digest、selected/rejected candidates、threshold、metric distributions、coverage 与未完成 implementation qualification。
- [x] 4.2 确认默认 5 actors 的全部 mandatory cases 在冻结 budgets 内通过；若不能通过则保持 B0.2 `not-qualified`，不得通过降低现有默认容量完成 task。
- [x] 4.3 记录 33 actors compatibility 结果、evaluated/qualified maximum 和 per-player/per-instance budget；当低于 VisitSession 配置上限时登记后续 battle admission capacity gate owner 与 `required-not-implemented`。
- [x] 4.4 连续两次执行 validate/simulate，证明 corpus 不被改写且 events、metrics、candidate selection、qualification 状态与 canonical digest 一致。

## 5. 建立失败回归与安全门

- [x] 5.1 新增 `tools/battle-network-profile/battle-network-profile.tests.ps1`，在隔离临时副本覆盖 model/case digest 漂移、manifest 悬空/漏登记、duplicate ID、unknown field、非法单位、overflow、missing coverage 与 stale report。
- [x] 5.2 增加 lane/MTU/KCP 负向回归，覆盖 snapshot 走 KCP、logical kind 多 lane/跨 transport 双写、payload 超限、隐式 IP 分片、过期可靠消息进入 simulation、参数 parity 漂移与 queue 无界。
- [x] 5.3 增加资格/安全负向回归，拒绝把 synthetic CPU/socket/KCP 标为 implementation-qualified，并拒绝 raw ticket、AEAD key、真实玩家资料、numeric production message ID、UDP 端口、generated wire code 与本机路径。
- [x] 5.4 证明工具无 listener、Docker、Go server、Unity、C++ runtime 和第三方安装依赖，所有手写 PowerShell 注释符合中文注释规范，失败输出稳定且不自动修复 source。

## 6. 同步 owner 文档与完成阶段门

- [x] 6.1 更新 `docs/gameplay-simulation-architecture.md` 与 `docs/network-transport-architecture.md`，同步冻结 cadence/window/baseline/lane/MTU/KCP/capacity 决策、profile 链接、implementation-required 项和后续 parity owner。
- [x] 6.2 更新 `docs/roadmap.md`、`docs/file-structure.md`、`docs/protocol-compatibility.md`、`docs/network-port-allocation.md` 与必要工程说明，登记 B0.2 evidence、B0.3 精确进入条件，并保持 wire/listener/端口/安全 transport/Unity gameplay 门关闭。
- [x] 6.3 运行 battle model/profile validator、全部失败回归和受影响文档/格式检查，将 `battle-network-profile` 与 `delivery-sequencing` delta 同步到主 specs并执行 OpenSpec strict。
- [x] 6.4 运行 `git diff --check`、generated/cache/secret/本机文件扫描和受影响质量门，记录可回滚到已归档 B0.1 的 completion evidence；提交保持单一 OpenSpec 意图并带 `OpenSpec: define-battle-network-profile` footer。
