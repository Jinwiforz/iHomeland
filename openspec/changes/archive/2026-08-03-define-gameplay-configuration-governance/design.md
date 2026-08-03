## Context

B0.1 已冻结纯 simulation model，B0.2-B0.6 已交付 profile、C++ core、Go/C++ control、安全 battle transport 与 network qualification tooling，B0.7 已把 Unity gameplay runtime 和权威移动投影接入真实服务。当前控制面已有三个相互独立的启动绑定：`ConfigIdentity`、`NavigationIdentity` 与 `PhysicsIdentity`；其中 `ConfigIdentity` 仍指向 `control-baseline-v1` 的 Tick/inbox/debt 配置，不能描述 B0.8 的武器、Ability、Effect、AI、Encounter 与客户端表现引用。模型 cases 中的 `battle-config-v1` 也只是一项 fixture identity，不是 production content package。

B0.8 将同时影响 Go 对 runtime 配置的选择、C++ 权威模拟、Unity 表现资源以及地图/nav/physics 资产。若没有先行契约，最容易出现四类双 owner：C++ 与 Unity 各保存一套伤害/冷却数值，Unity asset GUID 泄漏到服务端配置，Go/C++ 对 `ConfigIdentity` 的计算不一致，以及运行中局部更新导致同一 SimulationInstance 内存在多个规则版本。本 change 只建立机器可读治理与验证门，不实现这些 runtime consumer。

## Goals / Non-Goals

**Goals:**

- 建立可由 Go、C++、Unity 和工具共同引用、但不依赖任何运行时实现的 gameplay configuration package 契约。
- 明确 package、authority catalog、presentation catalog、map/nav/physics binding 的身份、所有权、引用、单位、范围和兼容规则。
- 为剑、扇子、普通怪物、Boss 与 PersonalWorld encounter 冻结最小配置类别和 coverage，而不提前确定最终平衡数值或资源。
- 复用现有 `ConfigIdentity`/`NavigationIdentity`/`PhysicsIdentity` 接缝，规定 instance 启动、replacement、回滚和 evidence 语义。
- 提供只读、确定、可隔离失败的 validator，使 B0.8 在写 runtime 前能依赖稳定 source contract。

**Non-Goals:**

- 不实现 C++ config loader、Go config selector、Unity presentation loader 或内容 runtime。
- 不制作或提交地图、navmesh、physics material、模型、动画、VFX、Audio、Prefab、ScriptableObject、AssetBundle 或 Addressables catalog。
- 不冻结最终伤害、冷却、血量、Boss phase threshold、spawn 数量或掉落数值。
- 不新增/修改 HTTP、Protobuf、Go/C++ control frame、battle wire、message ID、lane、listener、数据库或 Redis key。
- 不提供 runtime hot reload、跨版本 live migration、脚本 VM、行为树编辑器、远程配置、奖励或结算。
- 不执行完整 battle network/product qualification。

## Decisions

### 1. 以独立 gameplay-config corpus 表达跨 runtime 契约

source contract 固定在 `shared/contracts/fixtures/battle/gameplay-config/`，由 manifest 双向登记 schema、registries、reference packages 和 coverage。该目录只保存 canonical UTF-8/LF JSON 与说明文档；不保存生成代码、二进制资产、Unity `.meta`、运行日志或资格 evidence。

首个 format identity 为 `gameplay-config-format-v1`。manifest 自身不包含自身摘要；`config package identity` 由版本化 domain separator 加 manifest 登记文件的规范相对路径、document kind 和原始文件 SHA-256 按字节排序后计算。工具输出 lowercase SHA-256。这样既避免 JSON object 顺序影响，又能把完整 package 精确绑定到现有 `ConfigIdentity`。

备选方案是把配置直接放入 `simulation/` 或 Unity `Resources`。否决原因是任一 runtime 会成为事实 owner，另一端只能复制或反向解析实现资产，无法在实现前审计 parity。

### 2. 一个 package 分为 authority、presentation 和 external binding 三个闭合部分

每个 reference package 包含：

- `package.json`：package/version、model/profile compatibility、authority/presentation document、required role coverage 和独立 nav/physics binding 摘要；
- `authority.json`：C++ 独占的 actor archetype、weapon、ability grant/phase、projectile、effect/attribute、AI/Boss phase、encounter、collision/navigation policy 与权威 cue semantic key；
- `presentation.json`：Unity 独占的 display、Animator state、VFX、Audio、HUD、camera/prediction hint 与资源逻辑 key；
- `bindings.json`：map content identity、`NavigationIdentity`、`PhysicsIdentity` 及它们与 collision/navigation layer registry 的兼容关系，只保存摘要和逻辑 identity，不保存二进制资源。

共享 semantic ID 只用于把 presentation 映射到已发生的权威 entity/ability/effect/cue；presentation 不得重复伤害、Cost、Cooldown、命中体、AI、Boss threshold 或 authoritative lifetime。Authority 不得引用 Unity path、GUID、Prefab、Animator、Audio 或 VFX 类型。Validator 按字段 allowlist 和引用方向检查该边界。

备选方案是单一巨大 JSON。否决原因是它会让 C++ 必须理解 Unity 字段，或让 Unity 得到可被误用为本地裁决的权威配置，同时任何表现资源变更都会不必要地改变 authority digest。

### 3. stable ID 采用 typed namespace，引用图必须闭合且有向无环

ID 使用 lowercase kebab-case，并带由 registry 固定的类别前缀，例如 `actor/`、`weapon/`、`ability/`、`effect/`、`ai/`、`encounter/`、`cue/`、`resource/`。同一 ID 在整个 package 内唯一；删除的 ID 在同一 major format/package lineage 内不得重用于不同语义。引用必须声明预期 target kind，validator 拒绝 dangling、cross-kind 和非法 cycle。

只允许显式登记的有向关系，例如 weapon → grant → ability、ability → projectile/effect/cue、archetype → AI/attributes、encounter → archetype。递归 ability、effect 和 encounter composition 在 v1 禁止，避免配置图成为未设计的脚本语言。Boss phase 按严格单调 threshold 顺序引用既有 ability/AI policy，不形成反向依赖。

备选方案是使用递增整数或 Unity GUID 作为跨端 ID。整数难以审计且易在不同 catalog 中碰撞；Unity GUID 属于资产数据库实现细节，不能成为服务端语义事实。

### 4. schema 冻结单位、范围和推导规则，不冻结最终平衡值

registry 为每个 authority field 指定数据类型、真实单位、闭区间、零值/可空语义、rounding、overflow policy 和 model owner。时间使用 Tick 或显式整数毫秒，距离/速度使用整数项目单位，角度使用 millidegrees，Attribute/Effect 继续使用模型冻结的 signed 64-bit scaled integer；禁止无单位数字、NaN/Infinity、locale number 和隐藏默认值。

Validator 对能静态证明的引用与算术执行 checked range analysis，例如 base value + modifiers、scaled multiplication、projectile lifetime × speed、Boss threshold 顺序、capacity reservation 和 encounter peak。它只验证配置安全与 model contract，不运行 Tick pipeline、Jolt、Detour、AI 或 damage evaluator。最终 B0.8 数值仍必须由 C++ tests 与真实 Player 验收。

备选方案是把当前 C++ 常量直接认定为配置。否决原因是常量没有显式单位、版本或跨端 identity，也无法在 instance 启动前统一拒绝非法组合。

### 5. required role coverage 冻结产品切片边界而不预填内容

governance registry 固定 B0.8 package 必须覆盖的 role：Owner/Visitor player archetype、sword、fan、各武器授予的 primary ability、普通怪物、Boss、至少一个 Boss phase progression、PersonalWorld encounter、基础 world collision/navigation binding 以及对应 actor/ability/effect/cue presentation mapping。Coverage 只证明类别、引用和 owner 齐全，不规定最终数量与平衡值。

Reference package 使用专用 `fixture/` namespace 证明 schema 能表达完整关系，明确标记 `qualification_state: governance-only`，不得被 production Composition Root、C++ child 或 Unity Player 加载。B0.8 必须创建非 fixture 的 production package 并通过同一 validator。

备选方案是本 change 直接提交最终 `personal-world-combat-v1` 数值与资源引用。否决原因是 B0.7 目前只有平地移动，地图 collision、Ability runtime、AI 和表现资产尚未实现；在消费实现和测量前冻结最终内容会把未经验证的猜测伪装成稳定契约。

### 6. 配置选择由 Go owner 发起，SimulationInstance 内不可变

B0.8 的 production consumer 必须由 Go Composition Root 在启动 listener/child 前加载并验证允许的 package，得到 `ConfigIdentity`；start command 继续传递既有 config/nav/physics digests，不增加 control 字段。C++ 在创建 SimulationInstance 前独立校验本地 package 与三个 expected identity，并把它们纳入 instance identity、seed、result/evidence 和 ticket binding。任一文件或 binding 漂移均拒绝 start。

首版不支持 hot reload。配置变更只能部署完整新 package，并通过新的 assignment generation/fence 创建新的 SimulationInstance；旧 instance drain 后才能退役。禁止对 active instance 局部 merge、就地替换 catalog 或让不同 actor 使用不同 authority version。Rollback 选择上一份完整且仍可验证的 package，同样通过 replacement，而不是重写旧 digest。

Unity presentation package 由 build/release 绑定并通过 semantic parity gate；客户端不得选择服务器 authority config，也不得通过 payload 覆盖 `ConfigIdentity`。若当前 build 缺少所需 semantic mapping，battle feature 明确 unavailable/fail closed，world/visit membership 不被客户端伪造回滚。是否需要向客户端公开新的 config projection，必须由 B0.8 基于实际缺口提出独立协议变更；本 change 不预分配字段。

备选方案是文件 watcher 热更新。否决原因是会破坏 determinism、replay、ticket binding 与 result evidence，且首个内容切片没有不停服更新需求。

### 7. validator 只验证治理契约并保持确定只读

`tools/gameplay-config/gameplay-config.ps1 validate` 是唯一公共实现入口，检查 schema、manifest 双向完整、路径逃逸、digest、typed ID/reference、cycle、unit/range、checked static arithmetic、required role coverage、authority/presentation allowlist、semantic parity、fixture/production classification 和敏感字段。`gameplay-config.tests.ps1` 只在隔离临时副本注入 failure cases，不修改 source corpus。

连续两次 validation 必须产生相同的稳定低敏摘要并保持 Git 工作区不变。工具不得导入 C++/Go/Unity gameplay 实现，不启动 listener、Docker、Unity、CMake 或网络，也不得自动修复 manifest/digest。`tools/quality/catalog.json` 新增 incremental check `gameplay-config-validate`，当前 change 的 closed `validation.json` 只选择直接影响 checks；不自动升级为完整产品资格。

备选方案是让 C++ loader 兼任唯一 validator。否决原因是治理 change 会被迫提前实现 runtime，且 Unity/Go 无法在编译和 review 阶段独立验证 source package。

### 8. 错误与 evidence 只暴露稳定分类和低敏 identity

Validator 和未来 runtime 至少区分 schema、digest、reference、compatibility、range/overflow、binding 和 presentation-parity failure。诊断可记录 document kind、稳定 ID、相对路径、expected/actual digest 摘要和 reason code；不得记录本机绝对路径、完整配置 dump、凭据、玩家资料、奖励/资产事实或 Unity Library 路径。Replay/result evidence 只绑定 package/config/nav/physics identity，不复制整个 package。

备选方案是把解析异常原文直接进入日志。否决原因是异常可能携带绝对路径或整段配置，既不稳定也扩大低敏 evidence 范围。

## Risks / Trade-offs

- [治理 schema 过早限制内容表达] → v1 只覆盖 B0.8 已明确的固定 pipeline 和最小角色/武器/AI/encounter；新增组合语义必须独立 OpenSpec 升级 format，不在 JSON 中藏脚本表达式。
- [authority 与 presentation 分包增加发布协调] → 通过共享 semantic registry、manifest parity 和 build-time validation 保持一致；不以复制权威数值换取便利。
- [reference package 可能被误当 production 内容] → 使用保留的 `fixture/` namespace、`governance-only` classification 和 scope gate；production loader 必须拒绝该 classification。
- [无 hot reload 增加配置迭代成本] → 首个切片优先保证 determinism、回滚和 evidence；使用完整 instance replacement 支持可审计迭代，真实不停服需求成立后再提案。
- [静态 range analysis 不能证明全部 runtime 安全] → validator 只承担可判定边界；B0.8 仍必须增加 C++ unit/contract/integration、sanitizer、Unity tests 与真实 Player 定向验收。
- [未来需要客户端知道 exact authority config] → 当前保持 wire 不变；若 B0.8 证明 release binding 不足，先提出兼容协议 change，不在本治理 change 预造字段。

## Migration Plan

1. 提交 governance corpus、reference package、validator 与质量 check；现有 runtime 不消费它，部署行为不变。
2. strict 验证并同步主 specs 后归档本 change，B0.8 获得提案进入证据。
3. B0.8 创建实际 production package，并分别实现 Go selection、C++ pre-start load 与 Unity presentation mapping；在切换 Composition Root 前验证现有 world/visit 与 battle movement 回归。
4. 首次启用 production package 时以新 `ConfigIdentity` 和 assignment generation 重建 SimulationInstance；旧 config 的实例有界 drain，不做 live mutation。
5. 回滚时恢复上一份完整 package/build binding并再次 replacement；若 package validation 失败，保持 battle unavailable，现有 PersonalWorld/VisitSession owner 不被改写。

## Open Questions

无。本 change 不决定 B0.8 的最终平衡数值、资源系统或是否需要新增客户端 config projection；这些必须以实际 implementation gap 和独立验收边界提出。
