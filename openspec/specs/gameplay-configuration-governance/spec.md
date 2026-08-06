# Gameplay Configuration Governance 规格

## Purpose

定义 PersonalWorld combat 配置 package 的身份、schema、typed semantic ID、authority/presentation owner、单位/范围、instance 不可变、回滚与纯数据验证契约。

## Requirements

### Requirement: Gameplay configuration package 必须具有闭合且可重算的 source identity

仓库 MUST 在 `shared/contracts/fixtures/battle/gameplay-config/` 维护 versioned closed schema、manifest、registries、reference packages 与 coverage；manifest MUST 双向登记全部 source document 的规范相对路径、document kind、format/package version 和 raw-file SHA-256。Package identity MUST 由版本化 domain、稳定路径顺序、document kind 与登记文件 digest 确定重算，并 MUST 绑定兼容的 battle model/profile identity；manifest、未登记文件、unknown field、路径逃逸、非 UTF-8/LF 或摘要漂移 MUST fail closed。`ConfigIdentity`、`NavigationIdentity` 与 `PhysicsIdentity` MUST 保持三个独立 identity，不得用一个占位 digest 替代其余 binding。

#### Scenario: 完整 reference package 通过身份校验
- **WHEN** manifest、schema、registry、authority、presentation 与 bindings 文件完整登记且 raw bytes、model/profile compatibility 和三个 binding identity 均匹配
- **THEN** validator 以稳定算法产生唯一 lowercase SHA-256 package identity，连续运行结果一致且不修改 corpus

#### Scenario: 未登记文件或 digest 漂移
- **WHEN** package 目录新增未登记 JSON、登记路径缺失、文件 bytes 改变但 manifest digest 未更新，或路径尝试逃逸 corpus root
- **THEN** validation 失败并报告稳定 document/path 分类，该 package 不得成为 `ConfigIdentity` source 或 B0.8 进入 evidence

#### Scenario: Nav 与 physics identity 被合并
- **WHEN** package 只登记 config digest，却缺少独立 navigation/physics binding，或用同一魔法值冒充三个 source identity
- **THEN** compatibility validation fail closed，工具不得推测 map/navmesh/physics material 与 authority config 相容

### Requirement: 配置目录必须使用 typed stable ID 与闭合引用图

Gameplay config MUST 为 actor archetype、weapon、ability grant/phase、projectile、effect/attribute、AI/Boss phase、encounter、collision/navigation layer、cue 与 presentation resource 使用 registry 登记的 typed semantic ID。ID MUST 是稳定 lowercase kebab-case namespace，在 package 内唯一并绑定不可变 kind；同一 major lineage 中已退役 ID MUST NOT 被不同语义复用。每个引用 MUST 声明 expected target kind 并解析到当前 package 或显式允许的 registry；dangling、cross-kind、duplicate、非法自引用或未登记 cycle MUST fail closed。

#### Scenario: Weapon grant 引用完整
- **WHEN** sword weapon 引用已登记 grant，grant 引用允许的 ability，ability 再引用已登记 projectile/effect/cue 且引用方向符合 registry
- **THEN** validator 接受该闭合子图，并按 semantic ID 的规范顺序生成相同引用摘要

#### Scenario: 引用错误类别
- **WHEN** ability 的 effect reference 指向 actor archetype、已退役 ID、缺失 ID 或 presentation-only resource
- **THEN** validator 以稳定 reference/kind reason 拒绝，不通过字符串相似度、加载顺序或隐式默认对象恢复

#### Scenario: 配置图形成脚本式递归
- **WHEN** ability/effect/encounter 通过直接或间接引用形成 v1 未登记的递归执行 cycle
- **THEN** validation fail closed，不能把 JSON 引用图扩张成未设计、无容量边界的脚本 VM

### Requirement: Authority 与 presentation 配置必须保持单向语义映射

Authority document MUST 是 weapon grant、Ability phase/Cost/Cooldown、projectile、hit、Effect/Attribute/Damage/Death、AI/Boss phase、encounter 与 collision/navigation policy 的唯一配置事实，并只由服务器权威 consumer 使用。Presentation document MUST 只把 authority 发布的 actor/ability/effect/cue semantic ID 映射为 display、Animator、VFX、Audio、HUD、camera 或 prediction hint；它 MUST NOT 定义或覆盖权威数值、命中、伤害、AI、死亡、spawn 或 lifetime。Authority document MUST NOT 包含 Unity asset path/GUID、Prefab、Animator、VFX、Audio、UI 或 Unity type，客户端 payload/asset MUST NOT 覆盖 authority config。

#### Scenario: Unity 为 authority cue 提供表现
- **WHEN** presentation catalog 为已登记 sword-hit cue 映射 animation、VFX 与 Audio resource key
- **THEN** parity gate 接受该单向映射，Unity 只能在收到合法 authority event/state 后播放表现，不能据 cue 产生命中或伤害事实

#### Scenario: Presentation 重复冷却或伤害
- **WHEN** presentation document 增加 damage、cooldown、Boss threshold、projectile lifetime 或 AI decision 字段
- **THEN** closed schema/owner gate 拒绝该字段，客户端不得以本地值预测或提交权威结论

#### Scenario: Authority 引用 Unity GUID
- **WHEN** authority catalog 或 bindings 把 Unity GUID、AssetDatabase path、Prefab 或 Animator state 当作 C++ gameplay reference
- **THEN** validation 失败；authority 只能发布稳定 semantic key，Unity 资源所有权保持在 presentation package

### Requirement: 权威数值必须具有单位、范围、舍入和溢出契约

每个 authority numeric field MUST 由 registry 声明 integer/scaled-integer type、真实单位、允许范围、零值/可空语义、舍入与 overflow policy，并与 battle simulation model 的 Tick、movement、Ability、Effect、Attribute、AI、history 和 capacity owner 一致。时间、距离、速度、角度、比例、Attribute 与容量 MUST NOT 使用无单位数字、NaN/Infinity、locale value 或消费端隐藏默认值。Validator MUST 对可静态判定的 scaled arithmetic、modifier、projectile travel/lifetime、Boss threshold、capacity reservation 与 encounter peak 使用 checked calculation；可能溢出、违反单调/容量约束或无法满足完整 lifecycle reservation 的 package MUST 在 instance start 前被拒绝。

#### Scenario: 合法 scaled attribute 配置
- **WHEN** attribute、modifier、damage 与 clamp 使用登记 scale、toward-zero rounding 和 signed 64-bit checked intermediates，且最坏组合仍在范围内
- **THEN** validator 接受并输出稳定 range disposition，后续 C++ consumer 必须使用同一单位与舍入契约

#### Scenario: Damage 中间值溢出
- **WHEN** base damage、modifier 或 stack 上限的合法组合可能使 checked intermediate 超出登记 signed 64-bit 范围
- **THEN** package validation 以 range/overflow 失败，不允许 C++ 依赖 wraparound、float、clamp-after-overflow 或 runtime 随机拒绝继续启动

#### Scenario: Encounter 超过 hard capacity
- **WHEN** encounter 的 actor/projectile/effect/query peak 高于当前 model/profile 登记 hard cap，或不能在 activation 前保留完整 lifecycle capacity
- **THEN** compatibility gate 拒绝该 package，不通过降低 actor 数、隐藏 spawn 或驱逐既有 entity 伪造通过

### Requirement: B0.8 最小产品角色必须由显式 coverage 证明

Governance registry MUST 定义 B0.8 production package 的最小 role coverage：Owner/Visitor player archetype、sword、fan、各武器授予的 primary ability、ordinary monster、Boss、至少一个严格有序的 Boss phase progression、PersonalWorld encounter、基础 world collision/navigation binding，以及对应 actor/ability/effect/cue presentation mapping。Coverage MUST 证明每个 role 的 owner、typed ID、必需引用与 authority/presentation mapping 齐全，但 MUST NOT 把未测量的最终平衡数值、资源品质或内容数量标记为 qualified。

#### Scenario: 最小角色完整
- **WHEN** package 为全部 required role 提供唯一 semantic object、闭合 authority reference、必要 presentation mapping 和 map/nav/physics binding
- **THEN** coverage gate 可标记 governance-complete，但不得据此声明剑/扇子/怪物/Boss 已实现或产品 combat qualified

#### Scenario: Boss 只有 presentation
- **WHEN** presentation 有 Boss model/HUD 映射，但 authority package 缺少 Boss archetype、AI、phase 或 encounter reference
- **THEN** required role coverage 失败，B0.8 不能用美术资产替代服务器权威 gameplay 配置

#### Scenario: Reference package 被用于 production
- **WHEN** `fixture/` namespace 或 `qualification_state: governance-only` 的 reference package 被 Go Composition Root、C++ child 或 Unity Player 选择
- **THEN** scope/consumer gate 必须拒绝；reference package 只证明 schema 表达与 validator 行为

### Requirement: 配置必须在 SimulationInstance 启动前验证并在实例内不可变

未来 production consumer MUST 由 Go owner 在启动公开 listener/child 前选择完整允许 package 并验证 config/nav/physics identity；C++ MUST 在创建 SimulationInstance 前独立验证本地 source 与 start binding。Instance identity、deterministic seed、BattleTicket/session binding、result proposal 和 replay evidence MUST 绑定 exact config identity。Active SimulationInstance 的 authority config MUST 不可变；v1 MUST NOT 支持文件 watcher、partial merge、actor-specific version、隐式 fallback 或 hot reload。任何 package 变化 MUST 通过新的 assignment generation/fence 与新 SimulationInstance 完整 replacement，旧 generation input/result/config 不得复活。

#### Scenario: Exact package 启动 instance
- **WHEN** Go 选择的完整 package 与 C++ 本地 source、model/profile、ConfigIdentity、NavigationIdentity 和 PhysicsIdentity 全部匹配
- **THEN** C++ 可以创建绑定该 identity 的新 SimulationInstance，并在 seed、ticket、result/evidence 中保留同一不可变身份

#### Scenario: Active instance 的文件发生变化
- **WHEN** active instance 对应 package 任一文件被替换、局部 merge 或 watcher 检测到新值
- **THEN** runtime 不更新当前 simulation state；该 source 不得用于新 start，直到完整 package 重验并通过更高 assignment generation 建立 successor

#### Scenario: 回滚到上一 package
- **WHEN**当前 production package 需要回滚
- **THEN** Go 只能选择上一份完整、仍可验证的 package并以新 generation replacement，不能改写旧 digest、让部分 actor降级或在原 timeline内恢复旧规则

### Requirement: 客户端配置不兼容必须 fail closed 且不得改写 world membership

Unity build MUST 只消费 presentation package 与共享 semantic registry，并通过 build-time parity 证明所需 authority actor/ability/effect/cue 均有兼容 mapping。客户端 MUST NOT 选择服务器 authority package、从 presentation 推导权威结果或通过 payload 覆盖 config binding。若 current build 缺少 mapping 或 package compatibility 不成立，battle feature MUST 明确 unavailable/terminal 或遵循已登记恢复路径；它 MUST NOT 伪造 logout、leave、safe-return、assignment replacement 或 PersonalWorld/VisitSession state。任何新的客户端 config projection MUST 先通过独立协议 change 冻结，不得由本 capability 预造字段或 message ID。

#### Scenario: 客户端缺少 cue mapping
- **WHEN** current Unity build 无法解析服务器可发布的 required cue/actor semantic ID
- **THEN** build/parity gate 失败或 battle activation fail closed，客户端不猜测资源、不本地计算伤害，也不改变已由 Go/TCP owner确认的 world membership

#### Scenario: 客户端尝试覆盖 config identity
- **WHEN** input、snapshot consumer、Scene asset 或 presentation config尝试提交另一 ConfigIdentity或修改authority参数
- **THEN**边界拒绝该覆盖；current target/ticket/SimulationInstance binding仍只由Go/C++ authority决定

### Requirement: Gameplay config validator 必须确定、只读且不实现第二套 gameplay runtime

项目 MUST 提供 `tools/gameplay-config/gameplay-config.ps1 validate` 唯一验证入口和隔离失败回归，覆盖 JSON schema、manifest 双向完整、canonical digest、typed ID/reference/cycle、unit/range/checked arithmetic、required role coverage、authority/presentation allowlist、semantic parity、fixture/production classification、路径与敏感字段。Validator MUST 不导入 Go/C++/Unity gameplay implementation，不启动 listener、网络、Docker、CMake、Unity 或 gameplay evaluator，不自动修复 source，不依赖本机绝对路径；连续运行 MUST 输出相同低敏结果且保持 corpus 和工作区不变。

#### Scenario: 连续验证 current corpus
- **WHEN** 在相同 source 上连续两次执行唯一 validate 入口
- **THEN** 两次稳定 result/digest 完全一致，不创建或修改 tracked source、generated code、cache、listener 或外部进程

#### Scenario: 隔离失败回归
- **WHEN** tests 在临时副本分别注入 unknown field、digest 漂移、duplicate/cross-kind ID、dangling/cycle、非法单位/范围、overflow、coverage 缺口、owner 字段越界、敏感字段和 production 误用 fixture
- **THEN** 每个 case 以登记稳定 reason 失败，原 source corpus 保持不变且其他 failure 不被误报为通过原因

#### Scenario: Validator 调用 gameplay engine
- **WHEN** validator 或 tests 导入 simulation gameplay source、启动 C++/Go/Unity 或执行 Tick/AI/damage 来判定 package
- **THEN** architecture gate 失败；纯数据治理不能成为第二套 gameplay evaluator 或普通 change 的最终资格入口

### Requirement: 配置诊断与 evidence 必须保持低敏且可审计

Validator 与未来 consumer MUST 至少区分 schema、digest、reference、compatibility、range/overflow、binding 和 presentation-parity failure。诊断 MAY 记录 document kind、stable semantic ID、corpus-relative path、expected/actual digest 摘要和 stable reason；MUST NOT 记录本机绝对路径、完整 package dump、credential、PlayerID/个人资料、奖励/资产事实、Unity Library path 或 secret。Replay/result/qualification evidence MUST 绑定 package/config/nav/physics identity 和完整性摘要，不复制 authority/presentation 全文。

#### Scenario: Package digest 不匹配
- **WHEN** consumer 发现 authority document digest 与 manifest 不一致
- **THEN** 它记录稳定 digest failure、相对 document identity 和低敏摘要后拒绝使用，不回显文件全文、本机路径或任何 credential

#### Scenario: Evidence 需要关联配置
- **WHEN** B0.8 system test、result proposal 或未来产品资格关联 gameplay 配置
- **THEN** evidence 保存 exact package/config/nav/physics identity 与 schema version，不复制全部平衡数值或客户端资源目录

### Requirement: B0.8 production package 必须与 governance-only corpus 隔离并可由三端消费

项目 MUST 在非fixture路径维护`personal-world-combat-v1` production package，并使用`gameplay-config-format-v1`的closed manifest、authority、presentation、bindings和wire mapping。Production package MUST 使用非`fixture/` semantic namespace、明确`production` classification，并覆盖Owner/Visitor player、sword、fan、ability grant、ordinary monster、Boss/phase、encounter、collision/navigation及全部required presentation mapping。Go selector、C++ authority loader与Unity build-time presentation validator MUST 消费同一source identity；governance-only reference package MUST 继续被所有production consumer拒绝。

#### Scenario: 三端选择同一 production package
- **WHEN** Go配置选择`personal-world-combat-v1`且package、model/profile/wire、navigation、physics和presentation mapping全部匹配
- **THEN** Go可冻结选择，C++可在node ready前加载authority，Unity build parity可解析presentation，并且三端报告同一ConfigIdentity及对应binding摘要

#### Scenario: Runtime 选择 governance reference
- **WHEN** Go、C++或Unity Player尝试加载`qualification_state: governance-only`或`fixture/` namespace package
- **THEN** consumer在listener/ticket/instance或battle feature激活前fail closed，不以reference数值、资源或identity继续运行

### Requirement: Production semantic ID 与 numeric wire ID 必须稳定且双向完整

Production package MUST 为所有上wire的actor archetype、weapon、ability和projectile semantic ID登记唯一非零`uint32` wire ID；projectile ID作为其entity lifecycle/full-state archetype identity，但MUST保持projectile semantic kind。Mapping MUST 双向完整、按kind隔离、在package major lineage内稳定，退役numeric或semantic ID MUST NOT 被另一语义复用。C++ MUST 只从该mapping编码，Unity MUST 只通过同一mapping解析，Go MUST 把mapping source纳入ConfigIdentity或显式兼容binding；字符串hash、加载顺序、enum ordinal或消费端硬编码 MUST NOT 生成production ID。

#### Scenario: Production mapping 完整
- **WHEN** package required role及其runtime可spawn/emit的actor、weapon、ability和projectile全部具有唯一typed numeric mapping
- **THEN** validator与三端parity接受该package，重复运行得到相同mapping digest与semantic-to-wire表

#### Scenario: Numeric ID 冲突或缺失
- **WHEN** sword/fan共享numeric ID、同一semantic ID在两个kind中复用、Boss archetype缺少mapping或已退役ID被重新分配
- **THEN** package和consumer validation fail closed，C++不得发送该ID，Unity不得用fallback Prefab或默认ability猜测

### Requirement: Production package replacement 与回滚必须创建新 instance generation

Go MUST 是production package选择与回滚owner；C++ MUST 在node ready前重算本地source，并在SimulationInstance start前验证exact Config/Navigation/Physics identity。Active instance MUST 持有immutable typed catalog；source bytes、mapping、nav/physics asset或presentation compatibility变化 MUST NOT partial merge或hot reload。切换到successor或predecessor package MUST 通过完整重验和更高assignment generation建立新SimulationInstance，旧input、ticket、result、event与evidence不得跨identity复活。

#### Scenario: Active package 文件漂移
- **WHEN** active instance对应authority、wire mapping或nav/physics source在磁盘发生变化
- **THEN** current instance不吸收变化，该source不得启动successor直到完整重验，且现有timeline不会混用两个catalog

#### Scenario: 回滚到已知 package
- **WHEN** operator把Go selector从current package回滚到一份仍可验证的predecessor
- **THEN** Go以更高assignment generation替换实例，C++创建绑定predecessor exact identity的新timeline，不能改写旧digest或在原Boss战中间切换规则
