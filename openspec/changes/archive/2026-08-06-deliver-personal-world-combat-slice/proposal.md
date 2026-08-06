## Why

B0.7 已完成 Unity battle runtime、服务器权威移动/跳跃投影及 Owner/Visitor 代表性联机验收，`define-gameplay-configuration-governance` 也已归档，B0.8 的进入条件已经满足；但当前 PersonalWorld 仍只有平地移动基线，没有可由 production package 驱动的武器、敌人、Boss 与双人协作战斗闭环。现在需要把既有 C++ simulation、Go control、secure UDP/KCP 和 Unity 表现边界连接成首个可重复 PC 可玩切片，同时继续保持伤害、死亡和世界成员资格的权威所有权。

## What Changes

- 交付第一份非 fixture 的 `gameplay-config-format-v1` production package，覆盖 Owner/Visitor player、近战剑、远程扇子、武器 grant、普通怪物、Boss phase、PersonalWorld encounter、基础 collision/navigation binding 及 Unity presentation mapping；Go 是 package 选择 owner，C++ 在 SimulationInstance 启动前独立校验并在实例内保持不可变。
- 在现有 C++ 单写 Tick pipeline 中把武器切换、primary ability、剑 sweep、扇子 projectile、Effect/Attribute、伤害、死亡、普通怪物 AI、Boss phase 和 encounter lifecycle 接入真实 production runtime；客户端 input、Transform、Collider、Animator 或 cue 不能提交命中、伤害、血量或死亡事实。
- 兼容扩展 battle wire v1：为 `BattleInputKind` 增加无 payload identity 的武器切换 intent，并为 snapshot 增加 full-state 所需的 archetype、equipped weapon 与 max-health 投影；继续复用 `BattleAbilityReliableEvent` 与 `BattleEntityLifecycle`，建立 production semantic ID 到冻结 numeric ID 的确定映射和跨 C++/C#/fixture parity。该扩展不新增 message ID、不改变 raw/KCP allowed lane，也不回退到 WSS/TLS-TCP。
- 扩展 Unity 纯 C# replica/projector 与 Scene Scope hosts，呈现玩家武器、怪物/Boss、projectile、GameplayCue、动画、VFX、Audio、生命/技能/Boss HUD 和 melee/ranged camera intent；Scene/Prefab/ScriptableObject 只拥有表现资源，唯一 App/Scene/UI/Input owner 保持不变。
- 交付基础 PersonalWorld 场景 collision/navigation 内容及 encounter spawn/teardown，使 Owner 可直接游玩，Visitor 可通过既有 VisitSession 加入并协作击败 Boss；战斗断线、assignment replacement、safe-return 与 Scene teardown 继续服从既有 generation fencing 和 recovery owner。
- 增加无网络 simulation fixtures、production package/consumer parity、C++ unit/integration、battle contract、Unity EditMode/PlayMode、Windows build smoke，以及真实 Go parent + C++ child + 两个 Unity Development Player 的定向端到端验收；普通 change 只运行 closed `validation.json` 声明的影响面，不自动运行完整 battle 网络矩阵、连续 verify、soak 或 finalize。
- 明确不交付奖励、资产、掉落、持久战斗进度或结算事实，不引入 ActivityInstance、Room、Party、匹配、完整经济、脚本 VM、通用行为树编辑器、Addressables、DOTS/ECS 或跨平台发布资格。

## Capabilities

### New Capabilities

- `personal-world-combat-slice`: 定义 production gameplay package 驱动的 PersonalWorld 剑/扇子、普通怪物、Boss、Owner/Visitor 协作、服务器权威战斗、Unity 表现与可重复 PC 竖切验收。

### Modified Capabilities

- `gameplay-configuration-governance`: 将 governance-only reference contract 扩展为 production package 的选择、三端消费、actor/weapon/ability/projectile numeric wire mapping、instance binding、replacement/rollback 与 consumer parity 要求。
- `game-simulation-core`: 将已冻结的 Ability/Effect/Attribute/AI/Death 模型要求落实为 networked production SimulationInstance 的 encounter lifecycle、可靠事件和权威 projection 契约。
- `secure-battle-transport`: 兼容扩展 battle input 与 entity projection，冻结 content numeric ID、字段 presence/range、state mask、size/partition 和三端 wire parity。
- `client-battle-runtime`: 增加 production content semantic mapping、weapon/ability intent、怪物/Boss/projectile View、GameplayCue 与战斗 HUD/Camera 的运行行为和 teardown 要求。

## Impact

- **上游与 owner：**依赖已归档的 B0.7、权威移动投影和 gameplay configuration governance。Go Composition Root/Simulation control 拥有 production package 选择与 instance binding；C++ `SimulationInstance` 独占战斗 mutation、AI、伤害和死亡；Unity Application/Scene Scope 只拥有 input intent、replica 与表现；PersonalWorld/VisitSession membership、safe-return 及未来 settlement 继续由既有 Go owner 持有。
- **代码与内容：**影响 `shared/contracts/fixtures/battle/gameplay-config/`、battle semantic/numeric fixtures、`server/` simulation control wiring、`simulation/` gameplay/adapters/transport projection、`client/Assets/App/` 的纯 C# runtime、PersonalWorld Scene/Prefab/ScriptableObject/表现资产、质量工具与 owner 文档。
- **协议、存储与安全：**兼容扩展 battle proto/closed wire fixtures，但复用 message `3000-3007`、既有 secure UDP/KCP 和唯一 allowed lane；更新字段、state mask、size/profile binding、registry、fixtures 与三端生成基线，未知值和旧 binding fail closed。MySQL/Redis schema、账号/资产/奖励事实、HTTPS/WSS/TLS-TCP world/visit 契约和公开端口不变；UDP 仍不携带账号凭据、资产、奖励或结算事实。
- **验收与完成门槛：**production package 与三端 binding fail-closed，C++ 离线和真实 runtime 能确定执行剑、扇子、怪物/Boss 与死亡闭环，Unity 能在 current generation 中一致呈现并安全恢复/销毁；从登录、进入 Owner PersonalWorld、邀请 Visitor 到双人击败 Boss 的 Windows Development Player 场景可重复通过，closed `validation.json`、`quality.ps1 impact/check-change` 与 OpenSpec strict 全部通过。
- **回滚：**回滚到 `define-gameplay-configuration-governance`、B0.7 与权威移动投影均已归档的当前可运行提交；移除 production package、combat runtime wiring 与表现内容，恢复 movement-only PersonalWorld，不复用已发布 semantic/numeric identity，也不改变既有 world/visit membership。
