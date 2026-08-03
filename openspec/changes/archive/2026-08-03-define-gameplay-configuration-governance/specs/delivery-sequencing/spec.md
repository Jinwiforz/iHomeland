## ADDED Requirements

### Requirement: PersonalWorld combat slice 必须先冻结玩法配置治理

B0.8 `deliver-personal-world-combat-slice` MUST 只在 B0.7 Unity gameplay runtime 与权威移动投影的定向开发验收通过，且 `define-gameplay-configuration-governance` 的 strict specs、机器可读 corpus、reference package、只读 validator、隔离失败回归、owner 文档和 closed validation plan 均完成并归档后开始。配置治理 MUST 先冻结 package identity、typed semantic ID、authority/presentation owner、单位/范围、config/nav/physics binding、instance 不可变与回滚语义；B0.8 才能创建 production package、C++/Go/Unity consumer、地图碰撞/导航、剑/扇子、怪物/Boss 和表现内容。治理完成只解锁 B0.8 提案，MUST NOT 声称内容实现、battle network 或产品 combat qualified。

#### Scenario: 直接在 B0.7 后实现内容
- **WHEN** proposal 在 gameplay configuration schema、identity、owner/parity、validator 或 instance replacement 语义任一未冻结时，直接把伤害/冷却/AI 数值写入 C++ 常量、Unity ScriptableObject 或 Scene/Prefab
- **THEN** 评审必须先完成独立配置治理 change，不得让代码或 Unity 资产反向成为跨 runtime 配置事实

#### Scenario: 配置治理完成后提出 B0.8
- **WHEN** governance corpus 与 validator 完整通过，主 specs/owner docs 已同步归档，B0.7 movement/Owner/Visitor 代表性验收仍无回归
- **THEN** B0.8 可以基于同一 contract 创建非 fixture production package并实现 Go selection、C++ authority loader和Unity presentation mapping

#### Scenario: 治理 baseline 冒充可玩内容
- **WHEN** reference package 只有 governance coverage 与纯数据 validation 证据，却被用于声明剑、扇子、怪物、Boss、地图 collision/navmesh 或双人 Boss 流程已完成
- **THEN** 完成门失败；这些能力仍必须由 B0.8 的 runtime、Unity assets、定向测试与真实 PC build 验收

#### Scenario: B0.8 定向绿色后自动执行最终资格
- **WHEN** production package与combat slice的直接影响 checks通过，但使用者未显式冻结最终 candidate
- **THEN** change可以完成普通开发验收，但不得自动运行或生成完整12场景、1/5/8 actor、安全/生命周期、连续verify、长时soak或finalize结论
