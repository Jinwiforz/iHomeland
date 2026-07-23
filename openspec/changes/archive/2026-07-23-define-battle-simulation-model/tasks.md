## 1. 建立模型与 fixture 源契约

- [x] 1.1 在 `shared/contracts/fixtures/battle/model/README.md` 定义 model/format version、目录 owner、规范 ID、Tick/单位、canonical JSON/SHA-256、敏感信息禁止项，以及 B0.2/C++ harness 的消费边界。
- [x] 1.2 创建 `schema.json`，完整约束 initial state、mapping epoch、commands、physics/navigation trace、random streams、expected query/state/event/rejection/capacity outcome；拒绝 unknown field、非法单位、越界整数、重复或不稳定 identity。
- [x] 1.3 创建 `manifest.json`，为全部 case 登记稳定 ID、路径、category、requirements、positive/negative/determinism coverage、model/config version 和 canonical digest，并保证 manifest/case 双向完整。
- [x] 1.4 创建 `assumptions.json`，区分 `solo_owner`、默认 1 Owner + 4 Visitor、VisitSession 上限兼容性、movement/combat/Boss/disconnect workload，以及 `assumption`、`hard_contract`、`profile_output` 数值所有权和单位。

## 2. 冻结 Tick、输入与移动用例

- [x] 2.1 添加 Tick/Input cases，覆盖 `S0 -> S(T)`、整数 mapping、同 Tick 连续样本 fold、离散边沿、gap/expiry 后的 `LastProcessedInputTick`、duplicate 和 mapping/assignment generation reset。
- [x] 2.2 添加 command rejection cases，覆盖 payload 最终 Transform/命中/伤害越权、stale generation、invalid actor/state、未授予 ability、过期和规范排序不受网络到达顺序影响。
- [x] 2.3 添加 movement/jump cases，覆盖 grounded jump、空中重复跳跃、输入 clamp、acceleration/deceleration/gravity、slope/step/collision，以及相同 fraction 命中的 ColliderID/SubshapeID 稳定排序。
- [x] 2.4 为全部 movement cases 提供记录化 `PhysicsWorld` query/result、每 Tick expected transform/velocity/grounded projection 和 query digest，且不引用 Jolt 类型或真实 listener。

## 3. 冻结战斗、AI 与历史用例

- [x] 3.1 添加 weapon/ability cases，覆盖剑/扇子 grant 原子切换、phase、Tag、Cost、Cooldown、cancel/reject 与 prediction identity 的表现收敛边界。
- [x] 3.2 添加 sword/fan cases，覆盖剑 active Tick sweep 与 activation-target 去重、扇子 deferred projectile spawn/next-Tick movement/首次阻挡/expiry，以及 GameplayCue 不拥有伤害事实。
- [x] 3.3 添加 Effect/Attribute/Damage/Death cases，覆盖 scaled-integer 舍入、modifier 顺序、stack/refresh/expiry、immunity、overflow 配置拒绝、同 Tick 多伤害和唯一 death cause。
- [x] 3.4 添加普通怪物/Boss cases，覆盖 idle/acquire/chase/attack/recover/dead、threat/distance/ActorID tie-break、下一 Tick Boss phase 和 entity-local PRNG stream 隔离。
- [x] 3.5 添加 history cases，覆盖合法近战补偿、future clamp、过期/缺帧/旧 generation 拒绝、最小 history projection 和“当前 Tick 结算但不改写历史”。
- [x] 3.6 添加 overload cases，覆盖 player/projectile/effect/command/query/history/deferred capacity、连续 intent 收敛、离散命令稳定拒绝、evidence truncation 和 hard Tick debt drain request。

## 4. 实现纯数据校验门

- [x] 4.1 新增 `tools/battle-model/battle-model.ps1` 的非交互 `validate` 入口，验证 JSON schema、manifest 双向完整、ID/引用/单位/排序、coverage、canonical LF/UTF-8 与 SHA-256；所有手写 PowerShell 注释遵守中文注释规范。
- [x] 4.2 新增 `tools/battle-model/battle-model.tests.ps1`，以隔离临时副本覆盖漏登记/悬空 case、duplicate ID、unknown field、非法 Tick/单位、digest 漂移、缺失 requirement coverage 和敏感/wire 字段拒绝。
- [x] 4.3 验证工具保持只读且无 gameplay evaluator、网络、Docker、第三方 C++、生成代码和本机绝对路径依赖；连续两次 validate 必须产生相同低敏结果且不修改工作区。

## 5. 同步 owner 文档与阶段门

- [x] 5.1 更新 `docs/gameplay-simulation-architecture.md`，同步 Tick 后状态、mapping/ack、command vocabulary、mutation owner、运动/战斗/AI/history/overload/determinism 决策，并链接 model fixtures。
- [x] 5.2 更新 `docs/roadmap.md` 的 B0.1 状态、完成 evidence 和 B0.2 精确进入条件，保持 C++ core、Go/C++ control、UDP/KCP 与 Unity gameplay 门关闭。
- [x] 5.3 更新 `docs/file-structure.md`、`docs/network-transport-architecture.md`、`docs/network-port-allocation.md` 与必要工程说明，登记 fixture/validator owner、profile 参数消费者和“不新增 wire/listener/第三方依赖”边界，避免复制 owner 规则。

## 6. 完成验证与可回滚交付

- [x] 6.1 运行 battle model validator 及其失败回归，确认全部 cases/assumptions/schema/manifest 通过且 corpus 不含 credential、个人数据、message id 或 lane 分配。
- [x] 6.2 将 `battle-simulation-model` 与 `delivery-sequencing` delta 同步到主 specs，执行 `openspec validate --strict` 并确认没有 active change/artifact 漂移。
- [x] 6.3 运行文档链接/格式、`git diff --check`、generated/cache/secret 扫描和受影响质量门，记录 B0.1 completion evidence；提交保持单一 OpenSpec 意图并可回滚到权威 gameplay 架构基线。
