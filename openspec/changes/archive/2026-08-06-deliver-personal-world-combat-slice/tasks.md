## 1. 冻结 production content 与验证边界

- [x] 1.1 创建本 change 的 closed `validation.json`，只引用已登记直接影响面 check，并为 package、wire、Go/C++、Unity 与 strict 原因逐项说明；完整最终资格保持未授权。
- [x] 1.2 扩展 gameplay configuration schema/manifest 支持 production package 与 `wire-mapping` document kind，拒绝 unknown field、fixture namespace、classification、kind、digest 和路径漂移。
- [x] 1.3 扩展 semantic/numeric/coverage registries，登记 production actor/weapon/ability/projectile numeric ID、退役不复用、Boss phase、encounter 与 presentation completeness 规则。
- [x] 1.4 增加 production package 与 governance reference 的隔离失败回归，覆盖 numeric collision/missing/cross-kind、production误用fixture、authority/presentation越权和低敏诊断。
- [x] 1.5 更新 `tools/gameplay-config` 唯一validate入口，使它可只读验证fixture corpus与显式production root并连续产生相同Config/Nav/Physics/mapping摘要。

## 2. 创建 PersonalWorld combat production package

- [x] 2.1 在`shared/contracts/gameplay/battle/packages/personal-world-combat-v1/`创建closed package/authority/presentation/bindings/wire mapping，使用非fixture semantic namespace和`production` classification。
- [x] 2.2 冻结Owner/Visitor player、sword、fan、weapon grants、primary abilities、effects/projectile、ordinary monster、Boss phases与PersonalWorld encounter的typed reference图。
- [x] 2.3 冻结所有authority数值的单位、范围、toward-zero/checked-int64语义、team/friendly-fire、health、ability phase/cost/cooldown、damage、projectile、AI、Boss threshold、corpse lifetime与hard capacity。
- [x] 2.4 冻结actor/weapon/ability/projectile semantic ID到nonzero `uint32`的唯一mapping、retired集合与canonical digest，并为三端consumer建立正向和负向parity fixtures。
- [x] 2.5 创建基础arena的versionedmap/collision/navigation source与bindings，生成或登记exact map content、Detour navigation和Jolt physics identity，拒绝Unity Scene作为authority source。
- [x] 2.6 创建presentation logical resource catalog，覆盖player/monster/Boss/projectile、sword/fan、ability/effect/cue、HUD与camera intent，但不复制damage、cooldown、AI、hit、death或reward数值。

## 3. 兼容扩展 battle wire 与生成基线

- [x] 3.1 在battle proto为`BattleInputKind`追加`SWITCH_WEAPON`，冻结其zero-payload组合、unknown enum行为和current generation离散edge语义。
- [x] 3.2 为`BattleEntityState`增加archetype、equipped weapon与max-health字段，为`BattleEntityDelta`增加equipped weapon字段/state-mask bit，并冻结presence、range、immutability与cross-field invariants。
- [x] 3.3 更新state flag/mask、message/route/profile size projection、wire identity与compatibility registry，保持message `3000-3007`、direction、lane、rate、expiry、AEAD/replay和resync语义不变。
- [x] 3.4 更新canonical golden、malformed corpus与Go/C++/C# contract tests，覆盖player/Boss full state、weapon delta、lifecycle archetype parity、ability mapping、unknown field/ID/mask和1200-byte partition预算。
- [x] 3.5 通过统一proto入口重建并验证ignored Go/C++/C# generated binding，确认没有手工修改或跟踪generated code，旧wire binding在ticket/socket前fail closed。

## 4. 接入 Go production package selector

- [x] 4.1 扩展严格服务端配置，登记显式production package root/expected identity并验证路径、classification、Config/Nav/Physics/Wire identity；敏感日志不得输出本机绝对路径或package全文。
- [x] 4.2 实现Go package selector/loader，只解析部署选择与identity/binding，不解释Ability、AI、damage或Unity presentation，并在Composition Root启动child/listener前fail closed。
- [x] 4.3 扩展受监督C++ child启动参数和hello/ready校验，使Go只登记实际加载同一production identity且capacity兼容的SimulationNode。
- [x] 4.4 保持StartInstance、SimulationTarget、BattleTicket与session binding只携带现有Config/Nav/Physics digest，增加selector/source漂移、child mismatch、partial startup rollback与低敏observability测试。
- [x] 4.5 增加package predecessor/successor选择、higher-assignment-generation replacement与回滚测试，证明active instance不hot reload且旧target/ticket不能复活。

## 5. 实现 C++ authority loader 与 arena adapters

- [x] 5.1 实现C++ closed production authority/wire mapping loader，重算source identity、解析typed references/numeric units并拒绝fixture、unknown、duplicate、overflow与binding漂移。
- [x] 5.2 把immutable typed catalog接入SimulationNode和每个SimulationInstance，确保node ready/start rollback/stop按逆序释放且不在active timeline读取变化中的source。
- [x] 5.3 实现versioned arena collision loader和Jolt adapter，覆盖floor、static blocker、capsule/shape/projectile queries、stable Collider/Subshape排序与physics identity。
- [x] 5.4 实现Detour navigation asset加载和NavigationWorld adapter，覆盖spawn/path/query bounds、stable result排序、nav identity与失败启动回滚。
- [x] 5.5 增加authority loader、arena Jolt/Detour parity、identity drift、malformed content、capacity和continuous read-only tests，证明不依赖Unity Scene或本机路径。

## 6. 实现 production encounter 与战斗 pipeline

- [x] 6.1 在SimulationInstance S0按配置与seed确定创建player slots、少量ordinary monsters、一只Boss及其team/archetype/transform/attribute/weapon/AI components，并输出规范spawn lifecycle。
- [x] 6.2 扩展InputTimeline/normalization处理switch-weapon离散edge，拒绝非零附带字段；在AbilityActivation原子切换sword/fan grant并保持secondary ability稳定unsupported。
- [x] 6.3 接入sword primary的windup/active/recovery、Cost/Cooldown与一次权威shape sweep，按entity generation去重并产生规范DamageIntent和ability events。
- [x] 6.4 接入fan primary与deferred projectile完整lifecycle，使projectile从下一Tick移动、按首次world/enemy hit或expiry终结并遵守capacity reservation。
- [x] 6.5 接入Effect/Attribute/Death checked arithmetic、health clamp、friendly-fire policy、唯一death cause、corpse lifetime与deferred despawn，确保cue不拥有damage/death事实。
- [x] 6.6 实现ordinary monster `idle/acquire/chase/attack/recover/dead` AI、threat/distance/ActorID tie-break与独立PRNG stream，只向Movement/Ability提交intent。
- [x] 6.7 实现Boss严格phase progression、下一Ticktransition、phase ability/AI与death优先级，并发布权威phase/dead/encounter-complete暂态projection。
- [x] 6.8 增加无网络simulation fixtures/tests，覆盖solo/coop、sword/fan、duplicate/expired input、cooldown/capacity、AI determinism、Boss threshold/death竞争、projectile collision和连续canonical digest。

## 7. 接入 networked replication 与真实 C++ runtime

- [x] 7.1 从同一次committed Tick冻结archetype、weapon、health/max-health、phase/dead、transform、acknowledgement与entity generation，确保full/delta/lifecycle字段一致。
- [x] 7.2 把combat ability/lifecycle events接入current BattleSession KCP queue，冻结started/committed/completed/cancelled顺序、target集合、expiry/backpressure和无跨lanefallback语义。
- [x] 7.3 扩展full/delta partition、baseline/resync与late-session baseline，使新加入或重连Visitor可原子恢复全部players/monsters/Boss/projectiles和current weapon/phase。
- [x] 7.4 增加真实socket C++ tests，覆盖switch/primary ingress到damage/death projection、两session共享authority state、旧generation、reconnect takeover、event expiry、MTU和shutdown。
- [x] 7.5 扩展低敏runtime metrics/evidence，登记package/wire identity、actor/projectile/effect/query capacity、ability/lifecycle outcome与encounter-complete，禁止credential、PlayerID、完整package或settlement结论。

## 8. 扩展 Unity pure C# combat runtime

- [x] 8.1 扩展closed protocol adapter与client models，验证archetype/weapon/max-health、state mask/flags、numeric mapping、health invariant和lifecycle outer/initial parity。
- [x] 8.2 扩展semantic input与InputHistory，加入switch-weapon edge并保持move/aim/jump/primary cadence、lost-continuity edge保留、focus gate和无authority payload字段。
- [x] 8.3 扩展GameplayReplica处理production archetype/weapon/health/phase/dead与ability/lifecycle event，unknown/missing/conflict走唯一resync或terminal path且不部分提交。
- [x] 8.4 实现pure C# content mapping和GameplayPresentationProjector，发布immutableplayer/monster/Boss/projectile ActorViewState、weapon/ability/health/Boss HudViewState、GameplayCue与CameraIntent。
- [x] 8.5 扩展battle-only reconnect、assignment replacement、safe-return与Scene lease测试，证明successor full baseline恢复content且旧cue/View/HUD/prediction不能复活。
- [x] 8.6 增加EditMode pure/runtime tests，覆盖numeric/resource parity、switch/primary、ability cue dedupe、Boss health/phase/death、full baseline invariant、generation fencing和bounded collections。

## 9. 交付 PersonalWorld Scene 战斗表现

- [x] 9.1 创建tracked `ClientCombatResourceCatalog`，把production logical keys映射到player/monster/Boss/projectile Prefab、Animation、VFX、Audio、HUD和camera资源，并提供build-time completeness/parity gate。
- [x] 9.2 更新Input Actions与唯一Scene input host，加入明确的keyboard/gamepad switch与primary bindings，并通过Router/modal/window focus控制；所有`.meta`由锁定Unity Editor生成和复核。
- [x] 9.3 创建或更新player sword/fan、ordinary monster、Boss和projectile Prefab/Animator及基础VFX/Audio，使表现只消费semantic state/cue且无Collider命中回写。
- [x] 9.4 更新PersonalWorld Scene的可读arena地面、障碍、spawn/anchor与服务器map identity映射，保留唯一Camera/EventSystem/Router并禁止Scene/navmesh成为authority source。
- [x] 9.5 扩展`ClientActorViewRegistry`、scene-bound uGUI player/skill/Boss/world-health HUD与Cinemachine melee/ranged intents，复用现有UI Toolkit screen/input/focus owner。
- [x] 9.6 增加PlayMode覆盖actor/projectile spawn/despawn、weapon/ability animation、VFX/Audio、Boss HUD/phase/death、modal focus、reconnect baseline、safe-return、replacement和Scene teardown零迟到回写。
- [x] 9.7 通过Windows Development/Release build smoke验证资源打包、native/proto binding、无missing script/material/font、无重复owner和Release低敏诊断。
- [x] 9.8 通过锁定Unity Editor `AssetDatabase`把旧Battle目录、`GenericActor.prefab`与`PersonalWorldReference.mat`迁入`Assets/App/Modules/PersonalWorldCombat/Content/`，保留GUID/序列化引用并删除已空旧目录，不重排非Battle owner资产。
- [x] 9.9 把全部手写Unity脚本、Editor工具、tests和非代码资产迁入`Assets/App/Modules/<Feature>/<Layer>/`，集中asmdef到`Assets/App/Assemblies/`并用Unity生成`.asmref`保持程序集DAG、非目录asset GUID、序列化引用、build settings与generated边界；通过Unity退役已空且无引用的旧拓扑folder GUID，同步迁移`IHomeland.Client.<Feature>.<Layer>`namespace、Composition、owner registry、工具、fixture和文档后通过Unity编译与架构门禁。

## 10. 建立 combat 定向验收入口

- [x] 10.1 在quality catalog与唯一dispatcher登记`personal-world-combat-targeted` targeted-expensive check，并增加catalog/order/dry-run/unknown-or-missing check的quality contract回归。
- [x] 10.2 实现该check的隔离runner，统一构建/启动真实Go parent、C++ child、MySQL/Redis与Unity Development Players，隔离credential、endpoint、PID、evidence、binary receipt和cleanup。
- [x] 10.3 建立single-player scenario，验证登录、OwnWorld、battle activation、移动/跳跃、sword/fan切换与攻击、ordinary monsters、Boss phase/death和encounter-complete。
- [x] 10.4 建立Owner/Visitor scenario，验证邀请/接受、同一authority entity set、双方分别使用剑/扇子、协作击败Boss及role/membership不变。
- [x] 10.5 建立battle reconnect、safe-return或assignment replacement代表性场景，验证successor generation、full baseline恢复、旧event/View拒绝、稳定失败与零残留process/socket/Unity owner。
- [x] 10.6 确保runner只输出低敏定向development evidence，不生成或复用最终qualification report、诊断cache credential或旧run cleanup状态。

## 11. 同步 owner 文档与定向收口

- [x] 11.1 更新architecture、gameplay simulation、network transport/protocol compatibility、client architecture/UI/integration、file structure、technology versions与roadmap，登记production package、wire字段、owner、恢复、内容目录与B0.8完成边界。
- [x] 11.2 更新相关runbook，说明package选择/identity、arena assets、content build、单场景diagnose、双Player操作、低敏evidence、cleanup、rollback和最终资格显式授权边界。
- [x] 11.3 运行`quality.ps1 impact -Change deliver-personal-world-combat-slice`并审阅计划，只在closed `validation.json`完整且影响原因准确后执行`check-change`。
- [x] 11.4 通过定向check、OpenSpec strict、`git diff --check`、注释/owner/schema/registry/secret/cache/generated/Unity `.meta`审计，逐项勾选tasks且不自动调用`qualify`。
- [x] 11.5 同步五份delta spec到main specs，更新roadmap完成evidence与回滚提交边界，确认所有tasks、closed validation和strict通过后再申请归档。

## 12. 修复 Unity 退出清理路径

- [x] 12.1 将Experience停止收敛为本地intent/subscriber/generation撤销，由Router唯一清理active/cached routes；为退出后的迟到动作与route所有权增加EditMode回归。
- [x] 12.2 在AppRoot/Composition/Scene Host之间增加application-quitting通知，退出时立即失效Scene Context/Lifetime并跳过Unity已接管的显式Scene unload；普通unload语义保持不变。
- [x] 12.3 执行C#编译、相关EditMode/PlayMode与OpenSpec strict定向验证，并在已打开的Unity Editor中复核离开Play Mode不再产生`IHOMELAND_APP_SHUTDOWN outcome=best_effort`。

## 13. 收口 PC 输入、死亡预测与 runtime clone 生命周期

- [x] 13.1 在唯一input owner的runtime private clone上禁用非目标`Joystick`/`XR`/`Touch` binding group，并按Play Mode状态选择`Destroy`或`DestroyImmediate`，不修改tracked Input Actions或`.meta`。
- [x] 13.2 让current local actor权威死亡原子清空pending input与预测history、锚定权威transform并在同generation内持续关闭input，增加pure C#回归。
- [x] 13.3 通过Unity编译、相关EditMode/PlayMode、OpenSpec strict与手动Gameplay复核，证明静止不漂移、死亡后不可操作且离开Play Mode无runtime clone销毁错误。
- [x] 13.4 对齐C++ `InputTimeline`的精确6-Tick gap-expiry acknowledgement边界，允许合法neutral终结并前向跳过已终结InputTick，拒绝超出sent/skip与gap-expiry数学上限的future ack，并完成真实重进回归。

## 14. 根治空 player slot 被离线 AI 杀死

- [x] 14.1 冻结 BattleSession 参战资格契约：预留 actor slot 不代表在线；active session generation 在 SimulationTick barrier 成为 movement、Ability、AI target 与 damage 的共同 gate，断线/接管不隐式回血或复活。
- [x] 14.2 在 `BattleTransportRuntime -> SimulationNode -> SimulationInstance` 接入 generation-safe session lifecycle observer 与每 Tick active actor snapshot，保证所有 session 终结路径都会撤销 exact generation，旧 predecessor 不能关闭 successor。
- [x] 14.3 扩展 movement 与 production encounter，令 inactive player 保持权威状态但零速度、忽略 intent、不可被 AI/伤害选择；增加空实例长时间推进、activate/deactivate、takeover 与死亡重连不复活回归。
- [x] 14.4 编译 C++ 并执行当前影响面的定向测试、OpenSpec strict 与真实服务重启/Unity 首次进入复核，证明入场健康、离线不掉血、死亡后仍不可操作且重连不伪造复活。

## 15. 根治 PC 多设备输入污染与 local render 双写

- [x] 15.1 冻结唯一Player device pairing与single local presentation owner契约：默认Keyboard/Mouse、明确Gamepad按钮后切换到exact device、其他设备不聚合，shared Pointer binding不因Touch隔离失效，Actor位置只消费prediction presentation。
- [x] 15.2 把`ClientUiHostRoot`从按group清空binding改为runtime Player map显式设备配对与callback后原子切换，确保键盘release不会回落到虚拟Gamepad且Mouse delta继续产生Aim。
- [x] 15.3 删除`ClientActorViewRegistry`基于raw Move的第二条水平render motor，补充虚拟Gamepad隔离、WASD press/release、shared Mouse Aim、exact Gamepad切换与松键不自移PlayMode回归。
- [x] 15.4 通过Unity编译、相关EditMode/PlayMode、OpenSpec strict与真实Gameplay复核，证明操作后保持neutral不漂移、鼠标改变camera yaw、死亡仍关闭输入且退出无clone销毁错误。

## 16. 把离散 local prediction 重建为连续有界表现轨迹

- [x] 16.1 冻结唯一local presentation segment契约：只连接当前可见pose与最新immutable prediction target，以exact 50 ms SimulationTick按render delta推进，相同target不重启、不得读取raw Move或越过endpoint。
- [x] 16.2 用有界时间段轨迹替换`ClientActorViewRegistry`追逐阶梯target的`SmoothDamp`，保证correction从当前可见pose retarget且样本停止后精确停住。
- [x] 16.3 扩展PlayMode回归，覆盖60 Hz render消费20 Hz position target的连续帧位移、重复target不重启、波动frame delta、retarget、neutral停止与Camera同轨迹跟随。
- [x] 16.4 同步client architecture/UI/integration与main spec，明确local interpolation和remote interpolation的不同时间域及单一owner边界。
- [x] 16.5 通过Unity编译、相关EditMode/PlayMode、OpenSpec strict与真实Gameplay复核，证明移动连续、停止无漂移、Camera无离散台阶且不恢复第二条raw input路径。

## 17. 收归 fixed-timestep render 相位的唯一所有权

- [x] 17.1 根据真实Gameplay复核冻结根因：Scene收到prediction target后重新计50 ms会丢失25 ms InputTick与render frame的精确余量，形成与prediction并行的第二条时间轴；以Decision 15取代Scene计时所有权。
- [x] 17.2 让`GameplayPrediction`从current SimulationTick start/end、组内InputTick offset与未消费clock credit直接发布量化`PresentationTransform`，保证等价reconciliation不重启、continuity loss冻结可信pose、death/baseline锚定authority。
- [x] 17.3 删除`ClientActorViewRegistry`的local segment计时与推进路径，使Projector、Scene Actor和Camera只逐帧消费同一个prediction表现pose，不增加coroutine、`SmoothDamp`、阈值吸附或Unity Physics回写。
- [x] 17.4 扩展EditMode/PlayMode回归，覆盖29/21/17/8/13 ms波动frame序列、25/50 ms边界、等价reconciliation、同帧Actor提交、Camera同轨迹、重复neutral与无velocity外推。
- [x] 17.5 通过Unity编译、相关EditMode/PlayMode、OpenSpec strict与真实键鼠Gameplay复核，证明持续移动无周期性停顿/追赶、停止无漂移、Camera不引入第二时间轴。

## 18. 对齐10 Hz authority publication与continuous hold replay

- [x] 18.1 根据真实Gameplay与低敏日志冻结根因：10 Hz snapshot每次跨2个SimulationTick，旧client replay遗漏无新frame但仍处于server continuous hold窗口的中间Tick，稳定3 m/s时形成约150 mm周期性rebase；撤回send accumulator直接作为render相位的错误结论。
- [x] 18.2 在`GameplayPrediction`持久化authority continuous sample基点，并从latest authority到future input horizon逐个SimulationTick重演，与C++相同地在4-Tick闭区间hold Move、在有frame组按last Move/Aim与OR Jump只积分一次。
- [x] 18.3 把authority pacing clock credit限制为一个25 ms InputTick，禁止10 Hz publication放行帧catch-up burst；用prediction-owned current-visible到horizon timeline发布`PresentationTransform`，不恢复Scene计时器、raw Move motor或velocity外推。
- [x] 18.4 增加10 Hz回归，证明Sim12 Move经held Sim13到Sim14从150 mm连续预测到450 mm、每25 ms可见前进75 mm、放行只发送一个sample且下一authority reconciliation为零rebase；保留Actor/Camera同帧消费回归。
- [x] 18.5 通过Unity编译、相关EditMode/PlayMode、OpenSpec strict与真实键鼠Gameplay复核，证明角色相对地面连续移动、停止无漂移且正常publication不再产生约150 mm correction。

## 19. 分离locomotion动画与actor位移根节点

- [x] 19.1 根据真实Gameplay低敏日志与资产曲线冻结根因：`target_rebase_mm=0`证明prediction已与authority等价，剩余模型相对地面起伏来自移动中仍循环播放会修改`VisualRoot`垂直位置与Y缩放的`PlayerIdle`，禁止继续用位置平滑掩盖。
- [x] 19.2 在immutable`ClientActorViewState`中只按量化水平速度派生`Moving`，让`ClientCombatActorView`驱动exact`Moving/Bool`且保持Dead优先，不读取raw Move、Unity Transform差分或输入设备状态。
- [x] 19.3 通过幂等Unity Editor authoring入口创建共享in-place Locomotion Clip并规范四个production Animator的Idle/Locomotion/Ability/Dead状态图；全部`.anim`、`.controller`与`.meta`由Unity API生成或修改。
- [x] 19.4 扩展build-time Animator gate与定向EditMode/PlayMode回归，执行Unity编译、OpenSpec strict、架构门禁与diff审计，证明Moving参数、六条常量VisualRoot曲线、零Animation Event及authority/presentation边界稳定。
- [x] 19.5 由使用者执行真实键鼠Gameplay复核：持续移动时模型不再相对地面周期性上下起伏或缩放，停止后Idle呼吸恢复，攻击/死亡状态仍正确且Console零错误。
