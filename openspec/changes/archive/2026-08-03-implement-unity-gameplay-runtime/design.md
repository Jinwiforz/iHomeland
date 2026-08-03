## Context

B0.1-B0.6 已冻结并实现服务器权威 simulation、`battle-network-profile-v2`、Go/C++ child control、安全 BattleTicket/UDP/KCP 和可重复 network qualification tooling。当前 production C++ runtime 已提供公开 HTTPS `/v1/battle/tickets`、cookie + PSK-authenticated ephemeral X25519 handshake、ChaCha20-Poly1305 secure datagram、raw/KCP route `3000-3007`、input acknowledgement、resync、rebind/rekey/close 与 8 actor hard cap；独立 C++ client 已证明代表性 development-readiness。Unity 只存在 cross-language wire golden test，没有 production battle transport、replica、prediction 或 Scene 表现。

客户端已经完成模块化，编译期 DAG 固定为：

```text
Foundation
   ↓
Application
   ↓ ↘
Infrastructure  Presentation
          ↘     ↙
            Runtime
```

`SessionCoordinator`、`WorldAdmissionCoordinator`、`ClientConnectionRecoveryCoordinator`、`ClientUiRouter` 和 `ClientWorldSceneTransitionHost` 已分别拥有 Session、target、恢复、route 和 Scene generation。本 change 必须扩展这些稳定边界，不能用第二个 network/UI/Scene manager 绕开它们。

协议/profile 固定参数包括 25 ms input step、50 ms simulation Tick、10 Hz snapshot、16-Tick history、input bundle depth 3/redundancy 2、80 mm/2000 millidegree correction、100 ms interpolation、150 ms maximum extrapolation、1200-byte datagram、256 session queue 与 64 KCP message queue。B0.7 默认只做定向开发验收，不执行需要用户显式授权的完整最终资格。

当前目标平台仍是 Windows x64，Unity Editor 锁定为 `6000.5.2f1`。Apply 阶段的 current Editor 实测证明 Cinemachine 3.1.5 仍调用已移除的 `GetInstanceID()`，产生不可忽略的 `CS0619`；官方 3.1.6 changelog 明确把 InstanceID API 转换为 EntityID，`release/3.1` 在 2026-07-29 的最新非 preview 稳定版本为 3.1.7。因此本 change 固定使用 `com.unity.cinemachine` 3.1.7，并通过 current Editor 的 package resolve、Scene/Prefab serialization 和 Development/Release build 回归证明实际兼容；不得修改 `Library/PackageCache` 或使用 preview 规避兼容错误。

## Goals / Non-Goals

**Goals:**

- 在 Unity production object graph 中建立安全、可停止、可恢复的 `BattleNetworkClient`，byte-exact 消费现有 BattleTicket/wire/profile。
- 建立纯 C# replica、input/prediction history、reconciliation、remote interpolation 和不可变 presentation state。
- 把 battle connection/replica 与 current Session、World target、assignment 和 Scene generation 组合 fencing。
- 以现有 Input System、uGUI/UI Toolkit、SceneLifetime 和 Cinemachine 实现最小 Actor/HUD/Camera runtime。
- 通过真实 Go parent、C++ child 和 Unity Player 验证 Owner/Visitor movement、jump、loss/reconnect、replacement 与 teardown。

**Non-Goals:**

- 不新增或改变 battle numeric message、公开 HTTP schema、MySQL/Redis schema和服务端 simulation rule。
- 不交付剑、扇子、怪物、Boss、正式 VFX/动画/美术、玩法配置 catalog、奖励或结算；这些属于 B0.8。
- 不建立完整客户端 ECS/GAS、客户端权威物理/命中/AI、TCP fallback、第二个 Router 或全局 event bus。
- 不扩展到 Linux/macOS、移动平台、多个远程 SimulationNode、ActivityInstance、Room、Party、匹配、观战或回放。
- 不自动运行或生成完整 battle network/product qualification 结论。

## Decisions

### 1. Battle feature 由五个唯一 owner 组成并接入现有恢复 owner

对象图固定为：

```text
World target + Session
  -> ClientConnectionRecoveryCoordinator
      -> ClientBattleRuntimeCoordinator
          -> IClientBattleNetwork
               -> Infrastructure BattleNetworkClient
          -> GameplayPrediction
          -> GameplayReplica
          -> GameplayInterpolation
          -> GameplayPresentationProjector
```

- `BattleNetworkClient` 唯一拥有 UDP socket、handshake/session generation、wire/crypto/KCP、send/receive queues 与 terminal publish。
- `ClientBattleRuntimeCoordinator` 只编排 current Session/target lease、activation/deactivation 和三个 gameplay owner 的原子 generation replacement；它不保存第二份 transport/replica state。
- `GameplayPrediction` 唯一拥有 local input、acknowledgement 与 predicted history。
- `GameplayReplica` 唯一拥有 authoritative baseline/entity/lifecycle/event state。
- `GameplayInterpolation` 唯一拥有 remote samples/render timeline。
- `GameplayPresentationProjector` 是无状态纯函数，只派生 Actor/HUD/Cue/Camera View State。
- 既有 `ClientConnectionRecoveryCoordinator` 继续是唯一 recovery intent owner。Battle-only recovery 是其具名 sub-flow；world/channel recovery 开始时取消 battle sub-flow。

这些 owner 登记到 `client/Architecture/owner-registry.json`，Application/Presentation 保持无 Unity、无 generated protocol。`BattleNetworkClient` 的 generated mapping 和 native primitive只存在于 Infrastructure；Actor/HUD/Camera/Input host 只存在于 Runtime/Scene Scope。

备选方案是让 `PersonalWorldSceneContext` 创建 socket、prediction 和 retry。否决原因是 Scene replacement 会制造 connection/replica 双 owner，并使 safe-return、Session invalidation 和迟到 callback 无法统一 fencing。

### 2. BattleTicket issuance 与握手合并为一个 Infrastructure attempt

Application 只提交：

```text
BattleConnectIntent(
  session_generation,
  world_target_generation,
  OWN_WORLD | VISIT_WORLD,
  visit_session_id?)
```

Infrastructure attempt 创建稳定 idempotency identity，通过现有 HTTP transport请求 `/v1/battle/tickets`，验证 client-safe projection并立即完成 UDP handshake。Raw `ticketSecret` 不映射为普通 Application model：专用 `ClientBattleTicketCodec` 使用 `Utf8JsonReader` 从有界 response bytes 直接把 base64url secret 解码到 pinned/可清零 byte buffer，响应 buffer、proof、ephemeral private key、HKDF intermediates 和 handshake plaintext在成功/失败 `finally` 中清零。Application 只收到不含 endpoint raw text/secret 的 connection result。

HTTP response loss只在同一 attempt、同一 Session/target lease和原 idempotency identity下有界重试；未知是否提交不能生成另一 identity。Ticket 到期、target变化或 cancellation 后只关闭并重新由 recovery owner建立新 attempt。

备选方案是扩展通用 `ClientHttpContractMapper` 返回 `string ticketSecret`。否决原因是 immutable string 会扩散凭据生命周期并可能进入普通结果、异常或 snapshot。

### 3. C# 管理协议与 socket，窄 Windows native plugin 只提供 crypto/KCP primitive

Managed Infrastructure 实现：

- connected `System.Net.Sockets.Socket` 与单 receive pump/serialized send；
- handshake、secure header、nonce/replay/epoch、route envelope、partition metadata和 transport-control state machine；
- generated Protobuf validation/mapping、route policy、queue/backpressure和 main-thread dispatch；
- battle connection generation、rebind/rekey/close 与 lifecycle。

项目生成 `ihomeland_client_battle_native.dll`，只导出 versioned C ABI：

- X25519、HMAC-SHA-256/HKDF、ChaCha20-Poly1305、CSPRNG、constant-time compare和 secure zero；
- KCP context create/input/update/send/receive/release 与固定 allocator/error codes。

Native plugin 复用 `versions.yaml` 已锁定的 libsodium 1.0.22 与 KCP 2.1.1 source/checksum/license，不拥有 socket、ticket、session、route、queue、gameplay state或 Unity object。构建产物进入忽略的统一 generated native plugin目录，由客户端 build/test入口在 Unity 编译/Player build前按 exact source identity重建；不得提交本机 DLL/cache或从系统 PATH 回退。C ABI、C++ production adapter、独立协议客户端和 C# fixtures进行 byte/behavior parity。

备选方案包括复制第三方 C# KCP/crypto 源码、使用未锁定 NuGet wrapper或把完整 C++ transport暴露给 Unity。前两者增加新的供应链和算法漂移，后者把 session/socket owner跨语言隐藏并削弱 C# lifecycle测试，因此否决。

### 4. Battle generation 以五重 lease 提交，world membership 与 battle availability 分离

每次 activation冻结：

```text
SessionGeneration + SessionEpoch
WorldTargetGeneration + target kind/VisitSession
BattleConnectionGeneration + opaque binding fingerprint
GameplayStateGeneration
SceneGeneration (仅表现提交)
```

所有 receive、send completion、prediction Tick、recovery completion与 Scene callback提交时重新比较对应 lease。World target提交后可以开始 battle activation；Scene 可先显示 loading/disabled-input状态，只有 authenticated accept + full baseline + Scene binding都提交后开启 input。Battle-only failure只进入 battle unavailable/retrying state，不伪造 leave、safe-return、assignment replacement或 logout；玩家仍由既有 TCP/VisitSession owner拥有 world membership。

safe-return/target replacement/Session invalidation先关闭 input gate和 battle generation，再由原 owner执行 world/Scene状态迁移。App shutdown顺序是 input/Scene subscriptions → battle runtime/gameplay owners → UDP/native resources →既有 world/channels/session/storage，且每项受 AppLifetime deadline约束。

备选方案是把 battle connect塞入 `WorldAdmissionCoordinator.EnterOwnWorld/JoinVisit` 的同一事务并令失败回滚 world membership。否决原因是 battle transport availability不是 PersonalWorld/VisitSession authority，客户端不能用 UDP失败伪造服务器业务回滚。

### 5. Input scheduler 与 prediction 使用纯 C# 固定步长、有界 catch-up

Scene Input host在主线程读取已有 Input System action map，只投影 `Move/Aim/Jump/PrimaryAbility/SecondaryAbility/Interact` semantic sample。`GameplayPrediction` 是 App tickable，使用 monotonic accumulator按25 ms生成 InputTick，并按冻结 mapping归入50 ms SimulationTick；每帧 catch-up有硬上限，超过即记录 `input-clock-overrun`、清空未确认历史、关闭input gate并进入显式lost-continuity，禁止一次Update无界补发。下一份current authoritative snapshot可在该状态下建立新的continuity anchor：服务器可能已按gap expiry终结本地从未发送的InputTick，因此此处允许ack高于旧last-sent frontier；普通连续路径仍严格拒绝future acknowledgement。

每个 successor generation 的首个 InputTick 不能重新从1追赶已经运行的 instance timeline；`GameplayPrediction` 以原子应用的 full baseline `ServerTick` 为锚，使用 `input-early-window-ticks=2` 计算首个 InputTick，使其映射到 `ServerTick + 2`。该值来自 current network profile，既不能越过服务端提前窗口，也不能复用 predecessor generation 的 input frontier。

Current Input System 1.19 Editor 对 `Button` Action 不再显示独立 Control Type，并可把
`expectedControlType` 序列化为空；`InputActionType.Button` 本身只解析 `ButtonControl`。
因此客户端与静态门对 Move/Aim 继续要求显式 `Value/Vector2`，对
Jump/Primary/Secondary/Interact 要求 `Button` Action Type，并接受空或显式
`Button` expected control type；任何非 Button Action Type 或其他显式 control type
仍 fail closed。

每个 bundle只包含当前与最近两个 InputTick，并按两份冗余策略发送；连续输入最多由服务端冻结的4 Tick hold语义承接。`InputHistory` 与 `PredictedStateHistory`最多保留16 Tick；输入发送成功后才推进 `lastSentInputTick`。Prediction只实现模型允许的量化 move/jump/aim视觉前置和简单 kinematic，不复制 Jolt、不执行 hit/AI/damage。Unity Physics/Animator root motion只影响表现，不进入预测事实。

完整 snapshot partition set中的显式 `last_processed_input_tick` 是唯一确认来源。Codec在 Protobuf mapping前扫描 field 7 wire presence；ack回退、普通连续路径越过sent frontier或partition不一致为protocol terminal。显式lost-continuity re-anchor没有未确认历史，只采用current authority state与server finalized frontier并从后续Tick恢复输入。确认后以同一authoritative local actor state重放剩余输入；超过80 mm/2度立即重建predicted fact，阈值内和阈值外的可见Transform均由Scene presentation smoothing在render timeline收敛。

### 6. Replica 先原子组装，再校验 baseline/lifecycle 并 single-flight resync

Receive pump完成 AEAD/replay/route/Protobuf structural validation后，把不可变 packet交给有界 main-thread dispatcher。`GameplayReplica` 按 `(battle generation, message ID, server Tick, snapshot sequence, baseline ID)` 建立有界 partition assembler；重复分区幂等，任何字段/ack冲突终结集合。只有分区齐全、entity排序/唯一/state mask/unknown field/size均合法时才原子发布。

Full snapshot替换 current baseline；delta只可基于已提交且不超过40 Tick/10 fanout的 baseline。缺失 baseline、snapshot gap、entity generation gap或 lifecycle expiry只触发一个 current resync intent。`3006/3007` 继续走 KCP 2250 ms route；scheduled response只等待后续 raw full snapshot，rate-limited遵守 `retry_after_ms`，不得从 KCP/TCP获取 full snapshot。

Wire v1 的 authenticated accept公开 `actor_slot`，C++ current runtime把 player actor identity固定为 one-based slot。客户端只在 accept成功后解析 `local_entity_id = actor_slot + 1`，并要求 full snapshot存在匹配 entity；该关系由 C++/独立 client/Unity system test共同锁定。同一 simulation instance、PlayerID 与 role 的 battle-only successor使用新 ticket、binding fingerprint与battle generation，但Go admission复用原 actor slot，C++安装 successor 时原子撤销 predecessor binding；不同PlayerID、role或slot组合仍按冲突拒绝，不能借重连增加actor容量。若未来 actor identity不能继续遵守该兼容规则，必须先提出 wire version change，不能由 B0.7 payload或Scene猜测。

### Authority movement projection 前置已由独立 change 收口

`implement-authoritative-battle-movement-projection` 已冻结完整 snapshot transform 与
`state_flags` registry：低四位保持 phase，bit 4 是 authority grounded，bit 31 是 dead；
bit 0 不得重解释。C++ runtime 把 current move/aim/jump 接入每 instance movement/physics
commit，在同一 Tick 发布全部 actor state 与 acknowledgement；C# activation 和
reconciliation 直接消费 bit 4，并由三端 parity gate 与负向测试锁定。

Current physics 是 server-only Y=0 有界平地，只用于闭合基础 movement/jump read path。
正式地图碰撞仍属于后续独立 change。该前置完成不替代本 change 剩余的 Unity
EditMode/PlayMode、Scene 接线和真实 Development Player Owner/Visitor 验收。

### 7. Local prediction 与 remote interpolation 分开投影

`GameplayInterpolation` 为每个 remote entity保留有界、按 server Tick排序的 samples，render timeline固定落后100 ms；正常区间插值 transform，最多外推150 ms，之后冻结并标记 degraded。Local actor不进入 remote buffer，其 `ActorViewState`来自reconciliation后的predicted state；Scene Actor registry以实际render frame delta对local presentation target做有界指数收敛，Camera host只跟随该平滑后的Actor Transform。Current平地prediction可在world Y=0把下落candidate钳制为grounded visual state，但该结果不进入authority snapshot或grounded事实。Authoritative health/tag/ability/event仍来自replica。

Presentation projector从值快照派生：

```text
ActorViewState
HudViewState
GameplayCue
CameraIntent
BattleAvailabilityViewState
```

Projector不访问 clock、network、Unity object或可变全局。GameplayCue按 `(battle generation, event identity)` 去重并有硬上限；迟到 cue不能重放旧 ability或镜头 impulse。

### 8. Scene Scope 复用现有输入、Router 与双 UI

`PersonalWorldSceneContext` 增加一个封闭 battle binding bundle：

- `ClientGameplayInputHost`：读取 Input System并服从 existing route/input/focus lease；
- `ClientActorViewRegistry`：按 entity/generation绑定 generic B0.7 Actor prefab，不保存 replica；
- `ClientBattleHudHost`：scene-bound uGUI，显示 health、connection/correction与最小 crosshair；
- `CinemachineCameraHost`：只消费 `CameraIntent` 和 presentation target。

B0.7 只提供可验收的 generic actor/HUD/camera baseline，不创建武器/怪物/Boss正式内容 catalog。UI Toolkit继续负责 Login、Visit、Settings和 overlay；overlay capture input时由唯一 `ClientUiRouter`/input-focus host切换 action map/cursor，HUD不自行仲裁。

Current UI Toolkit 登录页继续使用系统字体 fallback 显示中文；scene-bound Battle HUD则明确绑定现有 Liberation Sans SDF。用户选择在尚未引入有授权的CJK TMP font asset时让Battle HUD使用closed Basic Latin availability/health文案，避免missing-glyph方框且不新增Scene、Prefab或`.meta`变更。该选择不把英文文案固化为长期本地化方案；正式多语言与共享fallback font catalog必须由后续UI资产change统一收口。

`com.unity.cinemachine` 3.1.7写入 `versions.yaml`、`manifest.json` 和 lockfile，owner为 Unity camera host，rollback为移除 package/CameraHost并恢复原 PersonalWorldScene camera。Scene/Prefab serialization、缺失脚本、探索跟随、RangedAim、impulse、overlay focus、Scene unload和 Development/Release build都进入回归门。

### 9. 普通 change validation 与最终资格保持分层

实现时新增并登记：

- `client-battle-runtime-validate`：版本/contract/profile/wire/asmdef/owner/secret/native source/asset静态门和纯 C# tests；
- `client-battle-runtime-unity`：current Editor 的定向 EditMode/PlayMode fixture 与序列化接线检查；
- `client-battle-runtime-targeted`：Windows Development/Release build smoke、native plugin 打包/加载，以及真实 Go parent + C++ child + Unity Development Player 的 Owner/Visitor clean、loss/reconnect、assignment replacement/safe-return与 teardown代表性场景。

`validation.json` 只引用 catalog 已登记的 incremental/targeted-expensive IDs；使用 `quality.ps1 impact/check-change` 运行。真实 Player harness分配独立 credential、endpoint、storage、PID、evidence和cleanup owner，只终止精确持有的进程。B0.6 的完整 fault/capacity/security/lifecycle、连续两次 verify、30分钟soak与finalize保留在 `final-product-qualification`，没有用户显式 `quality.ps1 qualify -Candidate <current-clean-HEAD>` 时不运行。

本 change 不修改 `.proto`、message ID 或 generated source，`client-battle-runtime-validate` 已按 manifest digest 和 field/route registry 校验 current proto consumer，Player build又会从现有 ignored projection实编译。因此定向计划不调用会重建整个 Unity generated目录的 `proto-verify`；协议源变化必须由独立直接影响面重新加入该检查。

## Risks / Trade-offs

- [Unity native plugin 增加构建与部署复杂度] → 只导出窄 C ABI、复用现有锁定依赖、统一入口重建，并用 ABI/parity/Player build gate阻止系统 DLL或架构漂移。
- [Managed UDP lifecycle与 native KCP context跨边界泄漏] → `BattleNetworkClient`保持唯一 owner，native handle只由 generation-scoped disposable wrapper持有，Stop/failure tests核对零残留 handle/task/socket。
- [HTTP JSON secret进入托管不可清零对象] → BattleTicket使用专用 UTF-8 streaming codec和可清零 buffer，不经过通用 string mapper；raw body在finally清零并纳入 heap/log/artifact secret scan。
- [Unity Update无法稳定达到25/10 ms cadence] → input/prediction使用有界主线程 accumulator，KCP使用独立 cancellable monotonic pump；两者都禁止 catch-up burst并报告低基数 overrun。
- [本地 kinematic与Jolt碰撞不一致造成明显校正] → B0.7只预测冻结的最小移动/跳跃子集，以80 mm/2度门和presentation smoothing收敛；更精细环境预测必须由后续有测量证据的独立 change提出。
- [actor slot 到 local entity 的 v1映射成为兼容负担] → 通过跨端 contract/system test明确锁定；若模型需要非一一映射，先升级 wire而不是在客户端启发式选择。
- [Battle-only失败与world recovery交错] → 复用唯一 recovery owner和五重 lease；safe-return/Session/target authority优先，迟到 battle completion无法回写。
- [通用 PersonalWorldScene 被临时 B0.7 asset污染] → generic Actor/HUD只承担 runtime验收，正式内容 catalog/Prefab由B0.8替换；不在本 change创建空武器/怪物/Boss manager。
- [Battle HUD 与 UI Toolkit 当前语言不一致] → 无CJK TMP资产阶段只在Scene HUD使用closed Basic Latin文案并以PlayMode锁定无missing glyph；正式本地化时以独立共享字体/文案资产change统一替换。
- [Unity minor release 把 package 使用的 API 升级为编译错误] → 以 current Editor 实编译为兼容源事实，锁定已完成 EntityID 转换的 3.1.7；禁止修改 package cache、降级 Editor 或选择 preview 绕过。

## Migration Plan

1. 记录 current clean commit、client-v1 build/contract baseline、B0.6 current profile/wire/native source identity和现有 Scene/owner snapshot。
2. 锁定 Cinemachine 3.1.7 与 client native adapter metadata；建立 native plugin统一 build、ABI、wire/crypto/KCP parity和失败回归，但不激活产品 feature。
3. 扩展 HTTP battle operation的专用 secret codec、Application ports、owner registry和 Composition bundle；以 fake HTTP/socket验证 ticket response-loss、secret cleanup和generation gate。
4. 实现 managed UDP handshake/secure session/raw/KCP/control lifecycle，先对真实 C++ child执行无Scene contract/integration tests。
5. 实现 pure C# input/prediction/ack、snapshot assembler/replica/resync、interpolation/projector，并以冻结 fixtures和故障序列验收。
6. 接入 existing recovery、AppLifetime、PersonalWorldScene Input/Actor/HUD/Cinemachine Hosts；保留每一步 Scene/Prefab `.meta` 和可运行 build。
7. 增加 quality catalog/validation checks、Development/Release build smoke及真实双 Player定向 harness；更新 architecture/UI/integration/file-structure/roadmap/version文档。
8. 运行 `quality.ps1 impact/check-change`、OpenSpec strict和 `git diff --check`。只有定向开发门全部通过才完成 B0.7；不生成最终资格。

回滚时先停止客户端 BattleTicket请求与 UDP activation，恢复 change开始前的 Unity object graph/package manifest/Scene/Prefab，并删除由统一入口重建的 ignored native/generated产物。服务端 B0.6 secure transport、已占用 message IDs和 fixtures保留，不回滚或复用；没有 MySQL/Redis迁移需要逆向执行。每个迁移阶段必须形成可运行提交，失败时回滚到最近绿色阶段。

## Open Questions

无阻塞问题。Cinemachine正式 rig参数、角色/武器/怪物内容 catalog、精细地形预测、完整 battle product qualification和非 Windows平台均由 B0.8 或后续独立 change在当前 runtime evidence上提出。
