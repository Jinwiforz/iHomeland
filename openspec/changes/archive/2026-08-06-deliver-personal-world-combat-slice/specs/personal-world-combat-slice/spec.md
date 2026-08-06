## ADDED Requirements

### Requirement: PersonalWorld combat slice 必须保持 actor、mutation 与 settlement owner 分离

Owner 与 Visitor MUST 只通过现有 PersonalWorld、WorldInstance、VisitSession、BattleTicket 与 current AssignmentStamp 进入同一 `SimulationInstance`。C++ SimulationInstance MUST 独占移动、武器、Ability、AI、命中、Effect、Attribute、伤害、死亡、Boss phase 与 encounter 暂态；Go MUST 继续独占账号、Session、PersonalWorld、VisitSession、assignment、safe-return 与任何未来 settlement；Unity MUST 只提交受限 intent并呈现只读 projection。Visitor MUST NOT 因参与 combat 继承 Owner 权限，Boss defeat MUST NOT 在本 capability 中创建奖励、资产、掉落、持久世界 mutation 或 ActivityInstance。

#### Scenario: Visitor 参与 Owner 世界 Boss 战
- **WHEN** Visitor 通过 current VisitSession 和 BattleTicket 加入 Owner 的 active SimulationInstance并攻击 Boss
- **THEN** C++ 以 Visitor 绑定 actor裁决实时战斗，VisitSession role与WorldOwnerID保持不变，且不会写入奖励、资产或PersonalWorld持久事实

#### Scenario: 客户端提交命中与奖励
- **WHEN** Unity payload、Scene Collider、Animator event或HUD尝试指定target、damage、death、reward或Owner identity
- **THEN** transport/application边界拒绝未登记权威字段，C++只按current actor与server state裁决，Go不产生任何settlement side effect

### Requirement: 首个 production encounter 必须形成剑、扇子、怪物与 Boss 的服务器权威闭环

Current production package MUST 提供一把近战剑、一把远程扇子、各自 grant 的 primary ability、少量普通怪物、一只具有严格有序 phase 的 Boss，以及基础 world collision/navigation binding。玩家 MUST 能在服务器接受的状态下切换剑/扇子；剑 MUST 通过权威 sweep 命中，扇子 MUST 通过下一 Tick 起移动的权威 projectile 命中；普通怪物与 Boss MUST 只以确定 AI intent移动和攻击。Damage、health、dead、phase与encounter-complete MUST 只在固定 Tick pipeline提交。

#### Scenario: Owner 使用剑后切换扇子
- **WHEN** alive Owner先以剑触发合法primary ability，再提交合法switch-weapon edge并以扇子触发primary ability
- **THEN** C++分别执行一次登记sword sweep与一次deferred fan projectile lifecycle，客户端只从权威weapon/snapshot/event收敛且不能选择命中目标

#### Scenario: Boss health 越过 phase threshold
- **WHEN** Owner或Visitor造成的权威damage使Boss health在Tick T首次越过下一登记threshold
- **THEN** Attribute与Death先完成Tick T提交，Boss在Tick T+1仅转换一次phase并由AIIntent产生新行为，Unity phase/HUD只消费该权威projection

#### Scenario: Boss 被协作击败
- **WHEN** Owner与Visitor在同一current SimulationInstance内把Boss health首次降至零
- **THEN** Death owner提交唯一Boss death与transient encounter-complete状态，双方最终snapshot收敛且不产生持久奖励或结算事实

### Requirement: Encounter lifecycle 必须绑定 current assignment 并可安全恢复或终结

Monster、Boss、projectile、ability/effect与encounter状态 MUST 绑定 exact AssignmentStamp、SimulationInstanceID、mapping generation、ConfigIdentity、NavigationIdentity和PhysicsIdentity。Battle-only reconnect MAY 在同一实例以新battle generation恢复current snapshot；assignment replacement MUST 创建新的SimulationInstance timeline和encounter state；safe-return、VisitSession close、Session invalidation与Scene teardown MUST 使旧input、event、View与HUD不能再提交。Redis/connection丢失 MUST NOT 使客户端或C++伪造持久恢复事实。

#### Scenario: Visitor 在 Boss 战中 battle-only reconnect
- **WHEN** Visitor UDP generation终止但Session、VisitSession和world target仍current，随后以new BattleTicket重连同一SimulationInstance
- **THEN** successor full baseline恢复current archetype、weapon、health、Boss phase和entity generations，旧prediction/cue/View回调不能覆盖successor

#### Scenario: Assignment 在战斗中 replacement
- **WHEN** Go以更高assignment generation替换正在运行的SimulationInstance
- **THEN** 旧encounter、projectile、input与event全部stale，新实例以绑定新package identity的独立S0启动，客户端不得把旧Boss进度或击杀状态复活到新timeline

#### Scenario: Visitor safe-return 与 ability event 并发
- **WHEN** current safe-return提交后旧battle generation的AEAD合法ability/lifecycle event迟到
- **THEN** generation/target gate丢弃迟到事件，Visitor按既有ReturningOwnWorld流程收敛且不会重新创建旧Boss、projectile或HUD状态

### Requirement: Combat slice 必须通过定向自动化和真实双 Player 可玩验收

仓库 MUST 维护 closed `validation.json`，只引用 quality catalog登记的incremental或targeted-expensive check，并提供production package/consumer parity、battle wire、C++离线确定性与真实runtime、Unity EditMode/PlayMode、Windows build smoke和真实Go parent + C++ child + Unity Development Player验证。代表性产品场景 MUST 从登录、进入Owner PersonalWorld、邀请/接受Visitor、双方移动/跳跃/切换武器/使用剑与扇子、击败Boss，覆盖一次battle reconnect、safe-return或assignment replacement及最终teardown。普通change验证 MUST NOT 自动执行或生成完整battle network qualification、连续verify、soak或finalize结论。

#### Scenario: 定向开发验收完成
- **WHEN** closed validation中的contract、package、C++、Unity、build与双Player场景在同一source identity上全部通过且cleanup成功
- **THEN** 本change可声明PersonalWorld combat slice development-ready，但不得声明完整battle network、产品combat或发布候选qualified

#### Scenario: 未获授权的最终资格
- **WHEN** 本change准备同步或归档但使用者没有显式调用`quality.ps1 qualify -Candidate <current-clean-HEAD>`
- **THEN** 工具只执行`impact/check-change`声明的定向影响面，不运行12场景矩阵、1/5/8 actor全量、安全/生命周期、连续verify、长时soak或finalize
