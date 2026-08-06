## ADDED Requirements

### Requirement: Networked SimulationInstance 必须加载并执行 production combat catalog

`SimulationNode` MUST 在ready前加载已验证production authority catalog、wire mapping与collision/navigation binding，并为每个SimulationInstance创建immutable typed catalog view。Current encounter MUST 在唯一simulation worker上确定生成player、少量ordinary monster与一只Boss；player BattleSession只能绑定既有actor slot，AI/projectile/effect/death entity必须受currentinstance hard capacity与deferred structural barrier约束。Flat-ground fixture/config、governance-only package或启动时静态projection MUST NOT充当production combat runtime。

#### Scenario: Production instance 正常启动
- **WHEN** child source、Go expected identity、model/profile/wire、nav/physics和capacity全部匹配
- **THEN** instance以确定seed与spawn plan建立player/monster/Boss current state，并在首个full snapshot发布真实archetype、weapon、health与phase projection

#### Scenario: Encounter peak 超过容量
- **WHEN** production package的player、AI、projectile、effect或query peak无法在current hard cap内保留完整lifecycle
- **THEN** node或instance在admission/activation前以稳定capacity reason拒绝，不隐藏spawn、不驱逐既有entity且不产生半扣Cost状态

### Requirement: Player 参战资格必须由 active BattleSession generation 驱动

预留 player actor slot MUST 只表达实例容量与稳定 actor identity。`BattleTransportRuntime` MUST 作为 authenticated BattleSession 创建、接管和终结的唯一 owner，把 exact instance、actor 与 session generation 生命周期提交给 `SimulationNode`；Simulation worker MUST 在每个 Tick barrier 冻结规范 active actor set，并让 Movement、Ability、AI target 和 Damage 使用同一份资格事实。未 active 的 player MUST 保留 current health/death 等权威状态，但 MUST 停止速度、忽略 intent 且不可成为 AI 或 damage target。Session 终结、断线或 successor takeover MUST NOT 回血、复活或重建 actor generation；旧 session 的迟到终结 MUST NOT 撤销更高 session generation。

#### Scenario: 实例在无人连接时持续推进
- **WHEN** production instance 已创建 player slots 和 AI，但尚无 actor 的 authenticated active BattleSession
- **THEN** simulation Tick 可以继续推进，所有 player 保持初始权威 health/position、速度为零，AI 不选择或攻击这些空 slot

#### Scenario: 同 actor 的 successor session 接管
- **WHEN** generation N+1 的 authenticated session 接管同一 actor，随后 generation N 的终结通知迟到
- **THEN** 下一 Tick 仍只把 N+1 视为 active，旧通知不能撤销 successor，且 actor 继续使用原有 health/death 与 entity generation

#### Scenario: 已死亡玩家断线后重连
- **WHEN** player 在 active session 中被权威伤害判定死亡，随后断线并以新 BattleSession generation 重连同一 instance
- **THEN** full baseline 仍投影该 actor 已死亡且输入关闭，不因 session activation 产生回血、复活或新 actor generation

### Requirement: 武器、Ability、AI、伤害与死亡必须接入 production 单写 pipeline

Current network input MUST 解析move、aim、jump、switch-weapon与primary-ability；switch MUST 原子撤销旧grant、应用新grant并按配置处理active ability，primary MUST 只激活current weapon授予的ability。Sword sweep、fan projectile、AI intent、hit、Effect、Attribute、Death、Boss phase、replication与structural change MUST 严格遵循冻结pipeline和整数/checked arithmetic。C++ MUST 发布单调reliable ability/lifecycle event及同Tick snapshot projection；客户端Collider、target、damage、health、phase、death或reward声明 MUST NOT 参与裁决。

#### Scenario: Sword sweep 命中重复 subshape
- **WHEN** current sword active Tick的规范sweep为同一enemy返回多个subshape hit
- **THEN** HitDetection按entity generation去重，只产生一次DamageIntent和一组规范target event，Attribute最多应用一次对应damage

#### Scenario: Fan projectile 命中 world 后终结
- **WHEN** fan activation在Tick T提交projectile且该projectile在后续Tick首次命中合法world或enemy collider
- **THEN** projectile不早于T+1移动，只对首次合法enemy hit产生damage，并在同Tick排入deferred destroy且不继续穿透造成未登记命中

#### Scenario: Boss phase 与死亡同 Tick 竞争
- **WHEN** damage在同一Tick既越过phase threshold又把Boss health降至零
- **THEN** Death提交唯一dead事实并阻止下一Tickphase/AI重入，event与snapshot以稳定顺序表达死亡而不先启动新phase

### Requirement: Combat projection 与 evidence 必须有界、低敏且不可结算

Replication MUST 从同一次committed Tick冻结entity archetype、equipped weapon、health/max health、phase/dead flags、transform、input acknowledgement及有界ability/lifecycle events。Unknown/unmapped ID、queue overflow、event expiry或partition failure MUST 产生稳定failure/backpressure/resync行为，不得改走其他transport。Replay/diagnostic evidence MAY 记录package/config/nav/physics/wire identity、seed、Tick、numeric semantic ID、capacity和稳定outcome；MUST NOT记录credential、PlayerID、完整package、奖励或把encounter-complete解释为settlement receipt。

#### Scenario: Reliable cue event 过期
- **WHEN** ability event在KCP sender application deadline前未能发送
- **THEN** event以稳定expiry终结且不回退到raw/WSS/TLS-TCP，后续snapshot仍使health/dead/phase持续事实收敛

#### Scenario: Boss defeat evidence 被提交
- **WHEN** runtime记录Boss首次死亡与encounter-complete摘要
- **THEN** evidence绑定exactinstance/config和Tick但不含玩家资料或reward，并且Go不得把该摘要当作资产、掉落或PersonalWorld mutation已提交证明
