# client-battle-runtime Specification

## Purpose
TBD - created by archiving change implement-unity-gameplay-runtime. Update Purpose after archive.
## Requirements
### Requirement: Unity battle runtime 必须只在 current 契约与 target 上启动

客户端 MUST 在启动 `BattleNetworkClient` 前校验当前 Session epoch、World target generation、Assignment identity、`battle-model-v1`、`battle-network-profile-v2`、`battle-wire-v1`、numeric route registry 和 generated `battle/v1` binding；任一 identity、route、size、lane、expiry 或 acknowledgement policy 漂移 MUST 在请求 BattleTicket 或创建 UDP socket 前 fail closed。Battle runtime MUST 只作为既有 App Scope/Scene Scope DAG 中的 feature 接入，不得建立第二个 AppRoot、Composition、Session、WorldAdmission、connection recovery、UI Router 或 Scene owner。

#### Scenario: Client binding 落后于 current profile

- **WHEN** Unity build 仍绑定旧 profile、旧 `3006/3007` 500 ms expiry、缺少 snapshot acknowledgement field 或与 current wire digest 不一致
- **THEN** 构建/启动契约门失败，客户端不请求 BattleTicket、不创建 UDP socket，也不把消息回退到 WSS 或 TLS-TCP

#### Scenario: Current world target 尚未提交

- **WHEN** Session 已认证但 `WorldAdmissionCoordinator` 尚未提交 OwnWorld/Visiting target，或 target 正在 safe-return、replacement 或 recovery
- **THEN** battle activation 保持 inactive，不根据 Scene、HTTP payload 或缓存 identity 猜测 assignment 和 VisitSession 资格

### Requirement: BattleNetworkClient 必须封闭 BattleTicket 与安全握手

Infrastructure `BattleNetworkClient` MUST 以 current Session/target selector 和稳定 idempotency identity 调用既有 HTTPS `issueBattleTicket`，并在同一 infrastructure-owned attempt 内完成 ClientHello/Retry/ClientAuth/ServerAccept。Raw ticket secret MUST 从有界 UTF-8 response 直接解码到可清零 buffer，只能用于公开 derivation domain、transcript proof 和 session key schedule；它 MUST NOT 进入 Application contract、snapshot、exception、日志、metrics、View State、Unity serialization 或磁盘。Client MUST 精确验证 wire suite、endpoint、target revision、expiry、cookie transcript、ServerAccept AEAD、binding fingerprint、battle session generation、key/endpoint generation、actor slot 和 role；成功或任意失败后 MUST 清零 ticket、ephemeral、proof 和临时 key material。

#### Scenario: BattleTicket response 丢失

- **WHEN** ticket 已在 C++ 安装但 HTTPS response 丢失或本地读取超时，current Session/target 仍相同
- **THEN** 同一 connect attempt 只可用原 idempotency identity 有界重试并消费服务端 byte-equivalent credential，不生成第二个 actor 占用或把 commit-unknown 当作新 ticket

#### Scenario: ServerAccept 认证成功

- **WHEN** current ticket 经 advertised UDP endpoint 完成 cookie retry 与 authenticated accept
- **THEN** `BattleNetworkClient` 原子发布不含 secret 的 connection snapshot，并把 opaque binding fingerprint、battle generation、actor slot、role 与 current Session/target lease 一起冻结

#### Scenario: Ticket 或 accept 漂移

- **WHEN** response suite、role、target kind/revision、expiry、accept binding、generation 或 actor slot 与 closed contract/attempt 不一致
- **THEN** attempt 关闭 socket并清零全部 secret，不发布 connected snapshot、不尝试明文、旧 ticket、TLS-TCP 或其他 endpoint fallback

### Requirement: Unity battle transport 必须精确实现冻结 UDP、AEAD、replay 与 KCP

客户端 MUST 使用一个由 `BattleNetworkClient` 独占的 connected UDP socket 承载 raw、KCP 和 transport-control；managed adapter MUST 独占 socket、wire、generation、send/receive queue 和 lifecycle，第三方 crypto/KCP primitive MUST 隔离在窄 native port 后。每个 datagram MUST 遵守 1200-byte MTU、48-byte secure header、16-byte AEAD tag、方向隔离 key、严格单调 packet sequence、256-packet replay window、current/previous key epoch、authenticated rebind 和 close 语义。KCP MUST 使用 10 ms update、window 64、fast resend 2、RTO 30–200 ms、dead-link 10、1000-byte ceiling、64-message queue 和 route-owned 500/2250 ms sender expiry；raw snapshot MUST 不进入 KCP。

#### Scenario: Duplicate 或旧 epoch packet

- **WHEN** current socket 收到 AEAD 合法但 packet sequence 已接受、过旧、future-jump 超限或 previous epoch 超过 3 秒的 datagram
- **THEN** packet 在 route/protobuf/application 前被丢弃或使当前 battle generation terminal，且不能推进 replica、KCP、acknowledgement 或 UI

#### Scenario: KCP queue 达到上限

- **WHEN** reliable event/resync sender 或 receiver queue 达到 64 条、route deadline 到期或 dead-link 成立
- **THEN** transport 以稳定 backpressure/expiry/terminal cause 收敛，不动态扩容、不改走 raw/WSS/TLS-TCP，也不阻塞 AppLifetime 停止

#### Scenario: Authenticated endpoint rebind

- **WHEN** current battle session 的本地网络路径改变且旧 session key仍可发起登记 rebind
- **THEN** client 完成 challenge/confirm 后只推进 endpoint generation，保持 packet sequence、replay、KCP conversation 和 key epoch；失败则关闭 current generation并交给唯一 recovery owner

### Requirement: Battle lifecycle 必须由 Session、target 与 battle generation 共同 fencing

`BattleNetworkClient` MUST 唯一拥有 battle connection generation；`ClientConnectionRecoveryCoordinator` MUST 继续唯一拥有 automatic/manual recovery intent；`GameplayReplica`、`GameplayPrediction` 和 `GameplayInterpolation` MUST 分别唯一拥有权威 replica、本地 input/predicted history 和远端 sample buffer。所有 mutation/publish MUST 同时匹配 current Session epoch、World target generation、battle generation、opaque binding fingerprint 和 Scene lease；safe-return、assignment replacement、target revision change、Session invalidation、battle hard reset 或 App shutdown MUST 先关闭 input gate，再使旧 generation、queue、history、replica 和 Scene subscription 不可提交。

#### Scenario: 旧 socket 回调晚于 successor

- **WHEN** reconnect 已建立更高 battle generation，而 predecessor 的合法 snapshot、KCP event、send completion 或 terminal callback 随后到达主线程
- **THEN** generation gate 丢弃 predecessor 结果，不覆盖 successor replica、不重复触发 recovery，也不销毁 successor Scene/HUD

#### Scenario: Assignment replacement

- **WHEN** current PersonalWorld assignment 被 successor 替换
- **THEN** 客户端先退役旧 battle generation 与全部预测/插值状态，再由既有 world recovery 提交 successor target后请求新 BattleTicket；旧 endpoint、binding fingerprint 和 input history不能恢复

### Requirement: GameplayPrediction 必须有界产生语义输入与预测状态

Scene input host MUST 只把 Input System action 映射为封闭 semantic intent；纯 C# `GameplayPrediction` MUST 以受信 monotonic clock 按 25 ms input step、50 ms simulation mapping、bundle depth 3、redundancy 2、continuous hold 4 Tick 和 16-Tick history 生成 `BattleInputBundle`、`InputHistory` 与 `PredictedStateHistory`。Current generation 的首个 InputTick MUST 以已原子应用的 full baseline `ServerTick` 为锚，并按冻结 `input-early-window-ticks=2` 映射到 `ServerTick + 2`；它 MUST NOT 从1追赶已运行的 instance timeline或继承 predecessor input frontier。Client MUST 只预测模型允许的本地移动、跳跃和表现前置状态，MUST NOT 发送或裁决最终 transform、target identity、hit、damage、effect、death、reward、AI 或 settlement。队列过载、长 frame pause、clock drift 或 history 溢出 MUST 丢弃/重置有界预测并请求权威收敛，禁止 catch-up burst。

#### Scenario: Full baseline 激活已运行的 instance timeline

- **WHEN** successor generation 首次应用非零 `ServerTick=T` 的完整 baseline
- **THEN** 首个 InputTick 精确映射到 `SimulationTick=T+2`，后续 InputTick 单调推进且不会因从1追赶而持续落在已提交 Tick 之后

#### Scenario: Frame rate 低于 input cadence

- **WHEN** Unity 主线程一次迟到跨过多个 25 ms input boundary
- **THEN** input scheduler 只按 hard cap 生成允许数量的当前/冗余 bundle并记录稳定 overload cause，不在单帧无界补发历史 command

#### Scenario: 玩家持续移动后丢包

- **WHEN** current actor 连续移动且一个或两个 raw input bundle 丢失
- **THEN** bundle depth/redundancy 与 continuous hold 在冻结窗口内维持输入语义，预测历史保持有界且服务端仍是最终 position authority

#### Scenario: 输入试图提交权威事实

- **WHEN** Scene、View、Animator 或 UI 尝试提交 Transform、entity target、命中、伤害、health、cooldown 或 reward
- **THEN** Application 输入 API 不提供该字段或 command，架构/contract tests 拒绝任何旁路

### Requirement: Snapshot acknowledgement 必须只推进 current actor 的预测确认

Client codec MUST 在 generated Protobuf mapping 前验证 message `3002/3003` field 7 显式存在，并只在完整 partition set 通过 current generation、sequence、baseline 和 entity validation 后应用 `last_processed_input_tick`。Acknowledgement MUST 单调；正常连续路径不得超过当前 generation 已实际发送的最大 InputTick，显式 0 表示尚未终结 InputTick 1。只有 `GameplayPrediction` 已因长帧或有界历史过载显式进入 lost-continuity、清空旧预测且关闭 input gate 时，下一份 current authoritative snapshot MAY 把服务器已按 gap policy 终结的更高 input frontier收为 continuity anchor；该例外不得适用于普通 snapshot、predecessor generation或仍保留未确认输入的状态。`GameplayPrediction` MUST 从权威 local actor state恢复确认基点、删除已确认历史并按原顺序重演未确认输入；位置误差超过80 mm或角度误差超过2000 millidegree时 MUST 更新事实状态，阈值内只可采用有界表现平滑。

#### Scenario: Full snapshot 确认部分输入

- **WHEN** current generation 已发送 InputTick 1 至 8，完整 snapshot 将 acknowledgement 从 3 推进到 6
- **THEN** prediction 只删除 4 至 6 的确认历史，以同一权威 actor state 重演 7 至 8，不确认 future tick或其他 generation

#### Scenario: Acknowledgement 回退或越过已发送前沿

- **WHEN** snapshot field 7 缺失、低于已提交 acknowledgement 或高于已发送最大 InputTick
- **THEN** 除显式 lost-continuity re-anchor例外外，snapshot不进入replica/prediction，当前连接按protocol defect fail closed并启动受控恢复

#### Scenario: 长帧后服务器已终结 input gap

- **WHEN** client因超过per-frame catch-up上限进入lost-continuity并清空未确认历史，而下一份current authoritative snapshot的acknowledgement高于旧last-sent frontier
- **THEN** prediction仅以该snapshot建立新的continuity anchor并从其ServerTick之后恢复有界输入，不把合法gap expiry误判为protocol terminal，也不补发长帧期间的catch-up burst

#### Scenario: 本地预测超过容差

- **WHEN** current actor 权威 transform 与预测状态的位置或角度误差超过 profile 阈值
- **THEN** 纯 C# state 从权威点重演未确认输入并发布校正后的 presentation state，Actor View、Camera 与 Animator不得回写权威 snapshot

### Requirement: GameplayReplica 必须原子组装 snapshot 并使用唯一 resync route

`GameplayReplica` MUST 只在同一 battle generation、server Tick、snapshot sequence、baseline ID、partition count 和 acknowledgement 完全一致且分区唯一完整时原子发布 full/delta snapshot。Full baseline、delta fanout、baseline age、entity ordering/state mask、entity generation、unknown field/enum/bit、size 和 collection count MUST 受 closed contract 与 profile 硬限制。Delta 缺失 baseline、sequence gap、baseline 超过 40 Tick/10 次 fanout或 entity generation gap MUST 不得部分应用，并 MUST 通过 single-flight KCP message `3006` 请求 resync；`3007` 只控制下一 baseline调度/限流，不得携带或替代 raw full snapshot。

#### Scenario: Full snapshot 分区乱序到达

- **WHEN** 同一 full baseline 的合法分区乱序、重复并在 deadline 内最终齐全
- **THEN** assembler 去重并只在完整集合校验后提交一次 replica，任何中间分区都不改变 Actor/HUD/prediction

#### Scenario: Delta 引用未知 baseline

- **WHEN** current delta 引用客户端未提交或已过期 baseline
- **THEN** delta 被拒绝，客户端按稳定 reason 发送至多一个 current resync request并等待 raw full baseline，不把 delta 直接套用到当前 state

#### Scenario: Entity lifecycle generation 发生缺口

- **WHEN** KCP `3005` 的 spawn/despawn event 与 replica 中 entity generation 不连续或同 generation 冲突
- **THEN** event 不创建/销毁 View，replica 进入受控 resync；旧 entity generation不能覆盖后续复用 identity

### Requirement: Local actor 与远端 actor 必须使用不同表现收敛

客户端 MUST 只在 authenticated ServerAccept 后按 wire v1 actor mapping 把 local entity identity 解析为 one-based actor slot（`entity_id = actor_slot + 1`），并以 current binding/generation 验证该 entity 存在；payload、Scene 或 View不得选择 local actor。Local Actor View MUST 消费 prediction/reconciliation 的 presentation transform，并在 Unity render timeline 以有界 frame-rate-independent smoothing 收敛 40 Hz prediction与权威 correction；Camera MUST 跟随该平滑后的 presentation target，不得直接逐次跟随离散 prediction sample。远端玩家、怪物、Boss 与投射物 MUST 由 `GameplayInterpolation` 使用 100 ms render delay 的有界 samples 插值，并最多外推 150 ms，超过窗口后冻结/降级而不是继续猜测。两条路径都 MUST 保留服务器 snapshot 为权威事实。

#### Scenario: 本地 prediction cadence 低于 render cadence

- **WHEN** local actor transform以40 Hz更新而Unity以更高或波动的render cadence绘制，期间还收到阈值内或阈值外权威校正
- **THEN** Actor View按实际frame delta有界收敛且Camera跟随同一平滑target，不把每个prediction/correction sample直接硬写成可见位置或yaw跳变

#### Scenario: Actor slot 与 snapshot 不匹配

- **WHEN** authenticated accept 的 actor slot 无法解析到 current full snapshot 的对应 entity，或 lifecycle 将该 generation 非法替换
- **THEN** input gate 保持关闭并请求 resync/受控重连，不选择第一个 entity、PlayerID payload或 Scene object 作为 local actor

#### Scenario: 远端 snapshot 抖动

- **WHEN** 远端 actor 的两个合法 sample 在 profile 允许的 jitter 下到达
- **THEN** interpolation 在 100 ms render timeline 输出平滑状态，不把每个 raw snapshot 直接跳变写入 Transform

#### Scenario: 远端 sample 中断

- **WHEN** 最新远端 sample 已超过 150 ms extrapolation window
- **THEN** View 停止外推并显示有界 stale/degraded state，不能继续移动、生成命中或修改 replica

### Requirement: Scene Input、Actor、HUD 与 Camera Host 必须保持表现边界

PersonalWorldScene MUST 由唯一 Scene Scope context 创建/绑定 Input System host、Actor View registry、scene-bound uGUI battle HUD 和 `CinemachineCameraHost`。Input host MUST 受既有 Router/input/focus generation 控制；Actor/HUD/Camera MUST 只消费不可变 `ActorViewState`、`HudViewState`、`GameplayCue` 和封闭 `CameraIntent`。UI Toolkit MUST 继续承载页面/overlay，uGUI MUST 只承载技能槽、准星、生命/Boss/世界血条等 scene-bound HUD；两者不得创建第二个 active screen、input action owner 或 battle command入口。Cinemachine 3.1.7 MUST 只映射 `Exploration`、`MeleeCombat`、`RangedAim`、`Cinematic` intent 和 presentation target，不能决定服务器 aim、锁定、ability、hit 或 gameplay state。

#### Scenario: UI Toolkit overlay 打开

- **WHEN** existing Router 打开会捕获输入的菜单或 modal
- **THEN** input/focus owner 禁用 gameplay action提交并正确切换 cursor/focus，uGUI HUD 不私自切 action map且不会重复发送 command

#### Scenario: Camera 进入 RangedAim

- **WHEN** current gameplay presentation 发布 `RangedAim` intent
- **THEN** Camera Host 切换登记的 Cinemachine rig并跟随 presentation target，但发送到服务器的量化 aim intent仍由 Input/Application 边界产生

#### Scenario: 高频 correction 与 terminal failure

- **WHEN** authority correction连续发生或battle generation以closed failure终结
- **THEN** HUD缓存相同TMP文本、不以逐帧correction提示闪烁制造mesh重建，并在Unavailable/Retrying状态只显示低敏failure token；未引入CJK TMP font asset的current baseline必须使用现有font可覆盖的closed Basic Latin文案、不显示missing-glyph方框，且battle overlay不得遮挡existing World UI

#### Scenario: PersonalWorldScene 卸载

- **WHEN** Scene generation 被替换或卸载
- **THEN** Actor、HUD、Camera、Input host、订阅和 Unity object 随 SceneLifetime 恰好释放一次，App Scope battle owner 保留或关闭只由 current world/battle lifecycle决定

### Requirement: Battle 恢复与 safe-return 必须服从唯一 connection recovery

Battle-only transient disconnect MUST 由既有 `ClientConnectionRecoveryCoordinator` 在 current Session/target lease 内执行 single-flight、45 秒总 deadline 的新 ticket/新 battle generation恢复；它 MUST NOT 复用已消费 ticket、traffic key、packet sequence、prediction history 或旧 baseline。同一 simulation instance、PlayerID 与 role 的 successor MUST 复用稳定 actor slot，并在安装新 binding 时撤销 predecessor binding；不同actor不得借successor覆盖已有slot或绕过8 actor hard cap。WSS/TLS-TCP/world recovery 开始时 MUST 先取消 battle-only plan，关闭旧 battle generation，并只在 successor world target提交后重新激活。Battle terminal failure MUST 不伪造 VisitSession leave、safe-return、assignment replacement或 Session invalidation；这些事实仍只由既有权威 owner提交。

#### Scenario: UDP 断线但 world target 仍 current

- **WHEN** current battle generation 因 timeout、dead-link或临时 socket错误终止，而 Session、VisitSession 和 World target仍有效
- **THEN** recovery owner 关闭旧输入/replica，使用新 idempotency identity签发并消费新 ticket，以同一actor slot建立successor generation并撤销predecessor binding，等待 full baseline后恢复输入且actor容量不增加

#### Scenario: Safe-return 与 battle reconnect 并发

- **WHEN** Visitor battle reconnect 尚未完成时 TLS-TCP 发布 current safe-return
- **THEN** safe-return 的 world target generation 使 reconnect lease 失效，旧/新 battle attempt均停止，Scene/UI按既有 ReturningOwnWorld 流程收敛且不会再次进入 Visitor target

#### Scenario: Battle reconnect 超时

- **WHEN** 45 秒总 deadline 内无法建立 current target 的新 session并提交 full baseline
- **THEN** recovery 进入稳定可重试 failure、input保持关闭且旧资源清空，玩家 world membership不被客户端猜测改变

### Requirement: Battle runtime 必须有硬资源上限与低敏观测

Client MUST 对 UDP receive/send、main-thread dispatch、KCP、snapshot assembly、replica entity、input/predicted history、interpolation sample、cue 和 View binding 使用启动时验证的硬上限与明确 backpressure策略；默认 actor capacity MUST 不超过 8，session queue MUST 不超过 256，KCP queue MUST 不超过 64。日志/metrics MUST 只使用稳定 component、operation、generation、outcome、reason、count、bytes、duration 和低基数 route/lane；MUST NOT 记录 ticket、key、nonce、cookie、binding fingerprint、raw endpoint、PlayerID、payload、server error text 或 entity集合。

#### Scenario: Main-thread dispatcher 背压

- **WHEN** receive pump 已通过安全校验但 dispatcher 或 snapshot queue 达到硬上限
- **THEN** current battle generation 以稳定 backpressure cause 终止或按登记 replace-latest策略丢弃，不在网络线程直接修改 gameplay owner或 Unity object

#### Scenario: Secret 扫描与 failure evidence

- **WHEN** 执行客户端 battle 日志、exception、diagnostic snapshot、test evidence 和 Player artifact扫描
- **THEN** 不出现 ticket secret、traffic/rekey key、cookie、nonce、binding fingerprint、raw datagram 或 credential-bearing HTTP body

### Requirement: B0.7 必须通过分层与真实 Player 定向验收

仓库 MUST 提供一个客户端 battle runtime 验证入口，覆盖版本/asmdef/owner/secret/asset gates、wire/crypto/KCP parity、pure C# input/prediction/reconciliation/interpolation/replica tests、Unity EditMode/PlayMode、Windows Development/Release build smoke，以及真实 Go parent + C++ child + Unity Development Player 的 Owner/Visitor movement、jump、loss/reconnect、assignment replacement、safe-return 和 Scene teardown 定向场景。普通 change 验证 MUST 通过 closed `validation.json` 和 `quality.ps1 impact/check-change` 调用已登记检查；完整 12-scenario、1/5/8 actor、安全/lifecycle、连续 verify、30 分钟 soak 与 finalize MUST 只在用户显式冻结 current clean HEAD 时执行。

#### Scenario: Owner 与 Visitor 连接真实 simulation

- **WHEN** 两个 Development Player 通过公开 HTTPS/TLS-TCP/UDP 接入同一 current PersonalWorld
- **THEN** Owner 与 Visitor 都经各自 BattleTicket 建立独立 battle generation，移动/跳跃由 C++ snapshot确认，远端 View插值呈现且不存在 payload identity覆盖

#### Scenario: 定向开发验收通过

- **WHEN** 本 change 的 contract、parity、EditMode/PlayMode、build smoke和声明的真实 Player代表性场景全部通过
- **THEN** B0.7 可以完成开发验收并解锁 B0.8 提案，但报告不得声称完整 battle network 或产品 combat qualified

#### Scenario: 未获授权的最终资格

- **WHEN** B0.7 接近完成、准备归档或旧 battle report 已 stale，但用户没有显式调用 `quality.ps1 qualify -Candidate <current-clean-HEAD>`
- **THEN** 工具不运行完整矩阵、连续 verify、长时 soak或 finalize，不生成最终 qualified结论
