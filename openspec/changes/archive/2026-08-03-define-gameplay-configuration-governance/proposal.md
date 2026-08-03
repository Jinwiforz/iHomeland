## Why

B0.7 已完成 Unity gameplay runtime 与权威移动投影，B0.8 现在唯一缺少的进入证据是可跨 Go、C++ 与 Unity 审计的玩法配置治理；当前 `battle-config-v1` 仍只是模型 fixture identity，`control-baseline-v1` 也不能表达剑、扇子、怪物、Boss、地图及表现资源的完整版本与引用关系。若直接实现内容，数值、语义 ID、资源引用、nav/physics identity 和回滚边界会散落到代码与 Unity 资产中，无法证明同一 SimulationInstance 与客户端表现消费的是兼容内容。

## What Changes

- 建立版本化、closed-schema 的 gameplay configuration package 契约，冻结 manifest、canonical serialization/SHA-256、稳定 semantic ID、显式单位、引用图、兼容策略和 owner；它与既有 battle model/profile digest、`ConfigIdentity`、`NavigationIdentity`、`PhysicsIdentity` 分别绑定但不混淆所有权。
- 冻结首个 PersonalWorld combat slice 所需的最小配置目录：actor archetype、weapon、ability grant/phase、projectile、effect/attribute、AI/Boss phase、spawn encounter、collision/navigation layer、map binding 与 presentation cue/resource key；只定义 schema、必需 ID/coverage 和安全范围，不在本 change 交付最终数值、美术或场景内容。
- 规定 C++ authority package 与 Unity presentation package 的共享 semantic ID/parity 边界：权威数值、AI、命中、伤害和死亡只由 C++ 消费，Unity 只消费显示、动画、VFX、Audio、HUD 和预测提示；Unity asset path/GUID 不得进入 C++ authority 配置。
- 规定配置在 instance start 前完成完整校验并按 config identity 对单个 SimulationInstance 不可变；禁止 runtime partial merge、隐式默认值和首期 hot reload。配置漂移、引用缺失、单位/范围非法、算术溢出风险、nav/physics binding 不匹配或客户端 parity 缺失均 fail closed。
- 增加纯数据 validator 与隔离失败回归，验证 schema/manifest 双向完整、canonical digest、ID/引用、cycle、单位/范围、model capability coverage、authority/presentation 字段隔离、跨包 semantic parity、敏感字段和只读性；不启动 Go/C++/Unity、listener、Docker 或 gameplay evaluator。
- 更新 gameplay 架构、文件结构、路线图和质量目录，明确该 change 只解锁 B0.8 提案，不开放新协议、存储、端口、结算或发布资格。
- 不实现剑、扇子、怪物、Boss、地图 collision/navmesh、动画/VFX/Audio/HUD、内容加载 runtime、AssetBundle/Addressables、奖励/资产/结算、hot reload、脚本 VM、行为树编辑器或完整产品资格；这些属于 B0.8 或后续独立 change。

## Capabilities

### New Capabilities

- `gameplay-configuration-governance`: 定义 PersonalWorld combat 配置 package 的身份、schema、所有权、稳定 ID、引用/单位/范围、authority/presentation 分离、instance 不可变性、回滚和纯数据验证契约。

### Modified Capabilities

- `delivery-sequencing`: 将玩法配置治理的 strict specs、机器可读 corpus 与 validator 证据规定为 B0.8 `deliver-personal-world-combat-slice` 的进入门，并保持内容实现与最终产品资格边界关闭。

## Impact

- **契约与工具：**新增 `shared/contracts/fixtures/battle/gameplay-config/` 的 schema、manifest、authority/presentation catalog 与负向 corpus，以及 `tools/gameplay-config/` 的只读 validator 和失败回归；不会生成运行时代码或二进制资产。
- **现有运行边界：**复用 B0.4 已存在的 `ConfigIdentity`、`NavigationIdentity`、`PhysicsIdentity` 和 B0.5 ticket binding，不修改 Go/C++ control frame、HTTPS、Protobuf、battle numeric message、UDP/KCP lane、MySQL/Redis schema 或 listener。
- **后续消费者：**B0.8 必须以本 corpus 为 source contract，实现 Go 选择/绑定、C++ 启动前加载和 Unity presentation mapping；不得从 fixture validator 派生第二套 gameplay engine。
- **安全与回滚：**配置不含 credential、玩家资料、奖励/资产事实或本机绝对路径；回滚到 B0.7 与权威移动投影均已归档的当前可运行提交时，移除新 corpus/validator 即可，现有 world/visit 与 battle movement runtime 行为不变。
