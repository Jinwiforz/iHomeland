## ADDED Requirements

### Requirement: Battle wire 必须兼容表达武器切换与完整 content projection

`battle-wire-v1` MUST 在现有message `3000-3007`与唯一lane内兼容增加无target payload的`SWITCH_WEAPON` input kind，并在full entity state显式携带nonzero `archetype_id`、受类型约束的`equipped_weapon_id`与positive `max_health_milli`。`health_milli` MUST 不大于max health；archetype/max health在entity generation内不可变，equipped weapon MAY 通过登记delta field与唯一state-mask bit完整替换。Lifecycle spawn外层archetype MUST 与initial state一致；unknown enum、unknown state-mask/flag、zero/unmapped ID、presence/range或cross-field mismatch MUST 在application前fail closed。

#### Scenario: Full baseline 包含 current player 与 Boss
- **WHEN** server编码current generation的player、ordinary monster与Boss full state
- **THEN** 每个entity携带可映射archetype和合法health/max-health，只有player携带登记equipped weapon，所有partition共享相同Tick/baseline/ack并在1200-byte预算内

#### Scenario: Weapon delta 缺少 state mask
- **WHEN**delta携带equipped weapon但未设置对应mask，设置mask却缺字段，或引用unmapped weapon ID
- **THEN**C++/Go fixture/C# closed decoder以同一稳定wire reason拒绝，replica与HUD不部分更新

#### Scenario: Switch input 夹带 target
- **WHEN**`SWITCH_WEAPON` command的move、aim、interaction slot或其他不允许字段非零
- **THEN**normalization在进入simulation inbox前拒绝该command，不能让payload选择weapon、target或actor identity

### Requirement: Content wire mapping 与 profile budget 必须跨三端冻结

Battle wire source MUST 登记production actor/weapon/ability/projectile numeric ID的kind、semantic identity、范围、retired policy和mapping digest，并把新增字段的presence、state mask、canonical bytes、malformed corpus、encoded maximum与partition projection纳入exact wire/profile binding。Go、C++与C# generated descriptor/codec/fixture MUST 对同一source保持parity；旧wire identity、旧max-size projection或消费端硬编码 MUST 在ticket/connection前fail closed。Compatible字段扩展 MUST NOT 改变message ID、direction、raw/KCP lane、AEAD/replay、rate、expiry或resync语义。

#### Scenario: 三端 canonical content packet
- **WHEN**canonical fixture编码包含Boss archetype、fan weapon、ability event与max health的full/lifecycle/event集合
- **THEN**Go、C++和C#产生相同bytes/descriptor interpretation并解析到相同semantic mapping，且size/profile validator接受其budget

#### Scenario: 旧客户端缺少新增字段 binding
- **WHEN**Unity build仍使用扩展前wire digest或decoder，不理解content字段/state mask
- **THEN**startup compatibility在请求BattleTicket或创建UDP socket前失败，不允许忽略unknown field后加入current combat instance
