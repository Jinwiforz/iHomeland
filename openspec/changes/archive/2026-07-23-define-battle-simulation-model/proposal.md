## Why

`define-authoritative-gameplay-architecture` 已冻结服务器权威边界和后续交付顺序，但 Tick 映射、输入命令、移动/战斗/AI 状态迁移、历史查询、过载和确定性仍只有原则，无法为 network profile 提供可测状态量，也无法让后续 C++ core 依据同一套无网络用例验收。现在需要先把首个 PersonalWorld gameplay 的纯模拟模型冻结为机器可读基线，避免网络参数、物理 adapter 和内容实现反向定义玩法语义。

## What Changes

- 冻结 `SimulationTick`、`InputTick`、输入映射 epoch、确认边界和固定系统阶段的离散时间语义；精确 tick rate、窗口和带宽数值仍由后续 network profile 通过测量确定。
- 定义首期输入命令及其唯一 mutation owner，包括移动、视角/瞄准、跳跃边沿、武器切换和 ability activation；客户端不得提交最终 Transform、命中、伤害、冷却或奖励。
- 定义角色胶囊运动、grounded/jump、物理查询 port、碰撞结果和稳定处理顺序，使纯模型测试可以注入记录化 physics trace，而不提前引入 Jolt。
- 定义剑、扇子、武器授予、Ability phase、Cost/Cooldown、Effect、Attribute、Damage、Death、respawn/移除边界，以及 GameplayCue 与权威事实的分离。
- 定义普通怪物和 Boss 的最小服务端 AI 状态、目标选择、导航请求、攻击选择、Boss phase 和随机流所有权，不引入脚本 VM、行为树编辑器或 ActivityInstance。
- 定义历史帧最小投影、延迟补偿 clamp、当前 Tick 结算、replay evidence、稳定排序/随机数和可测试确定性边界。
- 定义 actor、command、spawn、history、deferred command、tick debt 的容量类别及过载优先级；本 change 只冻结参数名称、单位、测量点和失败语义，不猜测最终预算值。
- 新增 `shared/contracts/fixtures/battle/model/` 下的版本化 schema、manifest、正向/拒绝/确定性 cases 与预期摘要，作为后续 C++ 无网络 simulation harness 和 network profile 的共同输入。
- 更新 gameplay 架构、路线图、文件结构与相关 owner 文档；不创建 C++ 工程，不引入 Asio/Jolt/Recast/Detour/KCP，不新增 Protobuf/message id，不开放 listener，也不修改 Go/Unity v1 行为。

## Capabilities

### New Capabilities

- `battle-simulation-model`: 定义首个 PersonalWorld gameplay 的离散 Tick、输入、移动/跳跃、物理 port、Ability/Effect/Damage/Death、AI、历史帧、过载、确定性和纯模型 fixture 契约。

### Modified Capabilities

- `delivery-sequencing`: 将已通过的权威 gameplay 架构作为本模型的进入证据，并把 strict 验证的模型、fixtures 与预算假设规定为 network profile 和 C++ core 的前置门。

## Impact

- 影响 `docs/gameplay-simulation-architecture.md`、`docs/roadmap.md`、`docs/file-structure.md`、必要的工程/协议说明，以及 `openspec/specs/` 中的长期行为。
- 新增可提交的纯数据模型 fixtures 与校验入口；fixtures 不含 wire layout、网络 lane、凭据、玩家个人数据、第三方类型或生成代码。
- Go 的 PersonalWorld、WorldInstance、VisitSession、placement、admission 与 settlement owner 保持不变；模拟结果只能形成待 Go 重验的 proposal，不产生新的持久化 owner。
- 后续 `define-battle-network-profile` 必须消费本 change 冻结的 command/state/capacity 维度来测量 tick、snapshot、历史和队列预算；`implement-game-simulation-core` 必须以同一 corpus 验证离线行为。
- 自动化将校验 schema/manifest/case 完整性、稳定 ID、单位、状态摘要、正负场景覆盖、无网络/第三方依赖和文档一致性。完成门槛是全部 artifacts/tasks 完成、fixture 校验与 `openspec validate --strict` 通过，并形成可回滚的独立提交。
- 回滚点为本 change 开始前、`define-authoritative-gameplay-architecture` 已归档且 Go/Unity v1 资格保持通过的可运行提交。
