## 1. 冻结 B0.7 进入基线与依赖

- [x] 1.1 建立 client battle runtime source manifest，绑定 current clean commit、Unity `6000.5.2f1`、client-v1 contract/build baseline、`battle-model-v1`、`battle-network-profile-v2`、`battle-wire-v1`、route registry、generated battle Protobuf 与 B0.6 development-readiness identity。
- [x] 1.2 实现只读 entry validator，拒绝旧 profile、`3006/3007` 500 ms expiry、缺失 snapshot acknowledgement、route/lane/size 漂移和未满足 B0.6 代表性 readiness。
- [x] 1.3 在 `versions.yaml`、Unity `manifest.json` 与 `packages-lock.json` 锁定 `com.unity.cinemachine` 3.1.7，登记 source、camera-host owner、3.1.5 `CS0619`/3.1.6+ EntityID 兼容证据与 rollback，并验证 current Editor 可重复 resolve。
- [x] 1.4 为 client native adapter 登记 libsodium 1.0.22、KCP 2.1.1 的现有 source/checksum/license consumer、C ABI owner、Windows x64平台和回滚，不新增系统/NuGet fallback。
- [x] 1.5 记录现有 BootstrapScene、PersonalWorldScene、产品 Prefab/UXML/USS、asmdef DAG、owner registry、EditMode/PlayMode 与 Development/Release build characterization，补足会被 B0.7 触及的回归断言。

## 2. 建立 client battle native primitive

- [x] 2.1 新增 `ihomeland_client_battle_native` CMake target 与 versioned C ABI，只导出 X25519、HMAC/HKDF、ChaCha20-Poly1305、CSPRNG、constant-time compare、secure zero 和 KCP context primitive。
- [x] 2.2 增加 architecture/link gate，证明 native plugin 不拥有或链接 production socket、ticket/session、battle route、simulation/gameplay adapter、Unity object或持久存储。
- [x] 2.3 扩展统一客户端构建入口，按 exact locked dependency重建 Windows x64 plugin并复制到 ignored generated Unity plugin目录，禁止 PATH、用户级 cache和 tracked binary fallback。
- [x] 2.4 在 Infrastructure 实现 C# interop、generation-scoped safe handle、固定 buffer/error mapping与幂等 release，保证 partial initialization和 shutdown清零/释放全部 native resource。
- [x] 2.5 增加 native ABI、RFC vector、wire crypto、KCP参数/expiry、malformed input、secret zeroization与连续 rebuild digest parity tests。

## 3. 接入 BattleTicket HTTP 与封闭 connect attempt

- [x] 3.1 在 Application 增加不含 credential/generated/Unity 类型的 battle target intent、connection snapshot、failure、network port和不可变 lifecycle contract。
- [x] 3.2 在 HTTP operation catalog/API 增加现有 `issueBattleTicket` consumer，严格编码 OWN_WORLD/VISIT_WORLD selector、5秒deadline、2048-byte body与 required Idempotency-Key。
- [x] 3.3 实现专用 `ClientBattleTicketCodec`，用有界 UTF-8 streaming parser验证 closed response并把 base64url secret直接解码到可清零 buffer，不经过通用 string result。
- [x] 3.4 实现 infrastructure-owned connect attempt，使 HTTP issuance、response-loss同 identity重试、ticket expiry、UDP handshake与secret cleanup共享一个 Session/target/cancellation lease。
- [x] 3.5 增加 own/visit、unknown field/enum、wrong suite/endpoint/role/target revision、response loss、commit-unknown、expiry/cancel和 raw body/heap/log secret regression tests。

## 4. 实现 managed BattleNetworkClient

- [x] 4.1 实现单 connected UDP socket、单 receive pump、serialized send、current battle generation、AppLifetime initialize/rollback/stop和无 Unity副作用的 connection owner。
- [x] 4.2 实现 ClientHello/Retry/ClientAuth/ServerAccept codec/state machine，验证 cookie/transcript/AEAD/binding fingerprint/session-key-endpoint generation/actor slot/role和 exact accept replay。
- [x] 4.3 实现48-byte secure header、direction key/nonce、64-bit send sequence、256-packet receive replay window、AEAD seal/open、future-jump/epoch/sequence terminal与secret rollover。
- [x] 4.4 实现 raw route envelope与 generated battle Protobuf adapter，按 registry校验 direction、message ID、payload/partition/sequence/expiry/unknown field并禁止 TLS-TCP/WSS fallback。
- [x] 4.5 实现 KCP C# owner/wrapper与10 ms cancellable pump，精确执行 window 64、fast resend 2、RTO 30–200 ms、dead-link 10、1000-byte ceiling、queue 64和500/2250 ms route-owned sender expiry。
- [x] 4.6 实现 authenticated rebind、10分钟/`2^20` packet rekey、3秒 previous epoch overlap、close/ack和失败到唯一 terminal callback的状态机。
- [x] 4.7 实现 send/receive/dispatcher/session 256-item hard limits、replace-latest raw snapshot策略、KCP/backpressure终态和低敏 observer snapshot。
- [x] 4.8 增加 fake socket与真实 C++ child contract tests，覆盖handshake loss/replay/tamper、wrong direction/epoch/nonce、MTU、raw/KCP、rebind/rekey、close、backpressure、stop竞态和零 resource leak。

## 5. 实现纯 C# input、prediction 与 acknowledgement

- [x] 5.1 从 current model/profile生成或严格读取 immutable client battle policy，校验25/50 ms cadence、bundle depth 3/redundancy 2、hold 4 Tick、16-Tick history和80 mm/2000 millidegree阈值。
- [x] 5.2 实现封闭 semantic input model、25 ms monotonic accumulator、50 ms mapping、有界 per-frame catch-up和 `BattleInputBundle` encoder，禁止权威 transform/hit/damage/reward字段。
- [x] 5.3 实现 `InputHistory`、`PredictedStateHistory` 与 local move/jump/aim最小 kinematic prediction，所有集合有硬上限且 clock overrun不产生catch-up burst。
- [x] 5.4 在 snapshot codec前验证field 7显式 presence，并实现 current generation 的 sent/acknowledged frontier、0语义、回退/future拒绝和已确认history pruning。
- [x] 5.5 实现以authoritative local actor state为基点的未确认输入重演、阈值外hard correction和阈值内presentation smoothing候选。
- [x] 5.6 增加pure C# cadence、loss/redundancy、continuous hold、pause/overrun、ack loss/reorder/duplicate、generation reset、history overflow和correction tests。

## 6. 实现 snapshot replica、resync 与 interpolation

- [x] 6.1 实现按battle generation/Tick/sequence/baseline/partition/ack绑定的有界 full/delta assembler，重复幂等且只在完整集合校验后原子发布。
- [x] 6.2 实现 `GameplayReplica` 的full baseline、delta state mask、entity排序/唯一、unknown enum/bit、lifecycle generation和reliable ability event projection。
- [x] 6.3 锁定 wire v1 `local_entity_id = actor_slot + 1` 映射，并在accept/full snapshot/lifecycle跨端测试中拒绝缺失、漂移或Scene/payload启发式 local actor。
- [x] 6.4 实现40-Tick/10-fanout baseline边界、snapshot/entity gap分类和single-flight `3006/3007` resync flow，scheduled只等待raw full、rate-limited遵守retry_after且不跨lane fallback。
- [x] 6.5 实现 `GameplayInterpolation` 的每entity有界sample、100 ms render delay、150 ms maximum extrapolation、stale freeze和generation teardown。
- [x] 6.6 实现纯 `GameplayPresentationProjector`，从replica/prediction/interpolation派生不可变 `ActorViewState`、`HudViewState`、`GameplayCue`、`CameraIntent` 与availability state。
- [x] 6.7 增加partition loss/reorder/duplicate/conflict、missing/expired baseline、resync限流、entity generation gap、local/remote分流、jitter/extrapolation和cue去重tests。
- [x] 6.8 通过独立 OpenSpec change 或使用者明确授权的当前 scope 扩展，补齐 C++ runtime input 到 committed Movement/Physics projection，并冻结 snapshot yaw、velocity、grounded 与 `state_flags` registry；完成前不得把纯 C# prediction 当作真实移动/跳跃验收。

## 7. 接入 Composition、恢复与生命周期

- [x] 7.1 实现 `ClientBattleRuntimeCoordinator`，以Session/target/battle/gameplay generation编排activation、full-baseline ready、input gate、deactivation和原子owner replacement。
- [x] 7.2 扩展 `ClientConnectionRecoveryCoordinator` 的battle-only single-flight flow，复用45秒总deadline；world/WSS/TLS-TCP recovery、safe-return与Session invalidation必须抢占battle flow。
- [x] 7.3 在 Infrastructure/World/Runtime Composition bundles中显式装配native primitive、HTTP battle codec、BattleNetworkClient、gameplay owners与projector，不向feature暴露完整 `AppCompositionResult`或service locator。
- [x] 7.4 更新 `client/Architecture/owner-registry.json` 与architecture validator，登记battle connection、battle runtime、replica、prediction、interpolation的唯一state/command/snapshot/module/test入口。
- [x] 7.5 把battle participants纳入 AppLifetime 初始化回滚和逆序停止，确保input/Scene订阅先退役、socket/native context随后释放且既有world/session owner不被battle failure伪造。
- [x] 7.6 增加activation/stop、old callback、battle-only reconnect、world recovery抢占、safe-return并发、assignment replacement、Session invalidation和partial composition rollback tests。

## 8. 接入 PersonalWorldScene、输入、Actor、HUD 与 Cinemachine

- [x] 8.1 扩展现有 Input Actions与runtime binding，建立Move/Aim/Jump/Primary/Secondary/Interact semantic input host，并服从唯一Router/input/focus generation。
- [x] 8.2 实现Scene Scope `ClientActorViewRegistry` 与generic B0.7 Actor prefab，按entity/generation绑定只读presentation state并明确区分local prediction与remote interpolation。
- [x] 8.3 实现scene-bound uGUI battle HUD/World indicators，只读取immutable Hud/Actor state并复用既有UI Toolkit页面、modal、EventSystem和active screen owner。
- [x] 8.4 实现 `CinemachineCameraHost` 与3.1.7 rig/prefab，映射Exploration/MeleeCombat/RangedAim/Cinematic intent、blend/impulse和deterministic fallback，不参与aim/hit/gameplay authority。
- [x] 8.5 扩展 `PersonalWorldSceneContext` 的battle binding/lease/unbind，确保Scene teardown释放Input/Actor/HUD/Camera/订阅且不自行close或复活App Scope battle事实。
- [x] 8.6 更新PersonalWorldScene、BootstrapScene及必要Prefab/UXML/USS接线并保留`.meta`，验证无missing script、重复Camera/EventSystem/Router/HUD owner或generated script序列化引用。
- [x] 8.7 增加EditMode/PlayMode覆盖overlay focus/action map、Actor spawn/despawn、local/remote transform、HUD/cue、Camera intent/impulse、Scene replacement和销毁后迟到回写。

## 9. 建立定向质量门与真实 Player 验收

- [x] 9.1 实现 `tools/client-battle-runtime/` 唯一入口与closed manifest，聚合entry、version、profile/wire/proto、native ABI、asmdef/owner、secret、asset、pure C#和Unity检查。
- [x] 9.2 在 `tools/quality/catalog.json` 登记 `client-battle-runtime-validate`、`client-battle-runtime-unity` 与 `client-battle-runtime-targeted`，并把 `validation.json` 更新为只引用current直接影响面检查。
- [x] 9.3 建立真实 Go parent + C++ child + Unity Development Player harness，隔离每次credential、endpoint、storage、PID、evidence和cleanup，并只终止精确持有的进程。
- [x] 9.4 实现双Player Owner/Visitor clean场景，验证各自BattleTicket、movement/jump input acknowledgement、local correction、remote interpolation、当前active actor identity集合与8-actor hard cap边界。
- [x] 9.5 实现代表性loss/reconnect、assignment replacement/safe-return与Scene teardown场景，验证successor generation、旧callback拒绝、稳定失败和零残留socket/native/Unity owner。
- [x] 9.6 实现Windows Development/Release build smoke、native plugin打包/加载、Release diagnostic剔除、secret/cache/tracked generated artifact扫描和cleanup failure gate。
- [x] 9.7 通过 `quality.ps1 impact -Change implement-unity-gameplay-runtime` 预览并执行 `check-change`，不得自动调用完整battle matrix、连续verify、30分钟soak或finalize。

## 10. 文档与 OpenSpec 收口

- [x] 10.1 更新 `docs/client-architecture.md`、`docs/client-integration.md`、`docs/client-ui-architecture.md`、`docs/gameplay-simulation-architecture.md`、`docs/protocol-compatibility.md`、`docs/file-structure.md`、`docs/technology-versions.md` 和 `docs/roadmap.md`，记录owner、依赖、read path、恢复、回滚和B0.8进入门。
- [x] 10.2 增加client battle runtime runbook，说明native restore、Unity build、单场景diagnose、证据/secret处理、Player cleanup、failure定位和最终资格显式授权边界。
- [x] 10.3 执行formatter、comment/architecture/scope、generated parity、定向tests、`openspec validate implement-unity-gameplay-runtime --strict` 与 `git diff --check`，逐项审计requirements/scenarios且不扩大为最终产品资格。

## 11. 真实 Player 连续性与表现回归修复

- [x] 11.1 修复长帧重置后的 authority re-anchor：只在显式 lost-continuity 状态接受服务器已终结但本地未发送的 input frontier，正常路径仍拒绝 future acknowledgement。
- [x] 11.2 修复 current 平地预测落地，使本地 jump prediction 在 world Y=0 停止下坠且不把表现 grounded 回写为权威事实。
- [x] 11.3 在 Actor View render timeline 增加有界本地表现平滑，使 Camera 跟随平滑后的 target，不把 40 Hz prediction或权威 correction逐次硬写入逐帧 Transform。
- [x] 11.4 缓存 HUD 文本写入并暴露低敏 terminal reason；字体覆盖策略与现有 World overlay 的非重叠布局必须显式收口。
- [x] 11.5 增加 pure C#/Unity regression tests并执行本 change定向验证、strict、`git diff --check` 与 `.meta` diff审计。
- [x] 11.6 在未引入 CJK TMP font asset 的当前阶段，把 Battle HUD availability/health 文案收敛为现有 Liberation Sans SDF 可覆盖的 Basic Latin，并更新 PlayMode 断言和表现规格；不得改变登录页语言、Scene、Prefab 或 `.meta`。
- [x] 11.7 执行 HUD 直接影响面的 Unity 定向测试、OpenSpec strict、`git diff --check` 与 `.meta` diff审计，不扩大为真实 Player重建或完整最终资格。
