## ADDED Requirements

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
