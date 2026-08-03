## Context

`SimulationNode::Start` 当前为每个 actor 创建静态 `StateProjectionToken`，Tick observer 只调用 `InputTimeline::Resolve` 并发布 acknowledgement。`BattleReplicationSnapshot` 随后组合 acknowledgement 与静态 state，因此 move/aim/jump 虽然进入 C++ inbox，却不会改变服务器 snapshot。`StateProjectionToken` 也只含 position、health、phase 与 alive；snapshot encoder 没有显式写 `QuantizedTransform` 的 yaw/velocity 字段，并把低四位 phase 与 bit 31 dead 临时拼为 `state_flags`。Unity decoder 要求七个 transform scalar 都有 presence，且在没有 authority grounded registry 时故意把 reconciliation 的 grounded 保持为 `false`。

本 change 跨越 C++ simulation、battle wire source corpus和 Unity consumer，但不改变 message ID、Protobuf field number、UDP/KCP lane、MTU 或 ticket/session ownership。用户要求不得新增或修改 `.meta`，也不由自动化控制 Unity Editor 前台；初始实现只改源码、结构化契约、测试和文档。真实Editor录屏随后证明既有Scene camera参数会把local胶囊体放大到几乎占满画面，因此允许直接更新已有PersonalWorld Scene的Cinemachine数值字段，但仍不创建资产、不修改`.meta`，并由后台批处理验证反序列化。

## Goals / Non-Goals

**Goals:**

- 让 current battle move、aim、jump input 在唯一 simulation worker 上推进服务器权威 movement state。
- 原子发布同一 committed Tick 的全部 actor state 与 current session actor acknowledgement。
- 显式写出 position、yaw、velocity，并用登记 bit 表达 grounded。
- 让 Unity reconciliation 只消费服务器 grounded bit，保留本地 prediction 的非权威定位。
- 以 C++ runtime、snapshot encoder、独立协议客户端和纯 C# tests 证明跨端一致。

**Non-Goals:**

- 不实现 hit、damage、ability、AI、reward、经济或持久事实。
- 不引入 Room、Party、ActivityInstance、新端口、新 lane 或 transport fallback。
- 不在本 change 导入正式 PersonalWorld 地图碰撞烘焙；首期 runtime 使用由 server `physics_identity` 约束的有界平地 PhysicsWorld，后续地图 adapter 仍通过同一 port 替换。
- 不声称 Unity/C++ 完整 Lockstep，也不把客户端 Transform、grounded 或 Scene collider作为服务器输入。
- 不运行完整 battle qualification、连续 verify、soak 或 finalize。

## Decisions

### 1. 为 live runtime 建立单一 `BattleMovementReplicationStore`

每个 `SimulationNode::Entry` 创建一个固定 actor 集合的 store。store 的 mutable movement state只在 `SimulationInstance` Tick observer所在的唯一 worker上推进；网络线程只能取得 owned immutable snapshot。Tick observer先解析完整 `ActorInputResolution`，计算全部 actor 的 candidate Movement/Physics state，全部成功后在一个 mutex临界区同时发布：

- nonzero committed `ServerTick`；
- current mapping generation；
- 按 ActorID排序的完整 state集合；
- 同一 Tick 的全部 `LastProcessedInputTick`。

`BattleReplicationSnapshot` 从这一份冻结副本中选择当前 session actor的 acknowledgement。Transport 在仍持有 session registry mutex 的边界，以当前 authenticated active session actor identity 过滤同一 commit 的 state；因此内部固定容量可保持确定性和有界分配，而公开 snapshot 只返回实际参与者。新 session 的 bootstrap显式包含自身与既有 active sessions，终结 session 从 registry移除后的下一次发布即不再包含该 actor。由于现有 delta state不表达entity创建或移除，session会记住最近一次成功发布的actor identity集合；集合变化时下一次publication必须强制发送full baseline，成功后才能以新集合继续delta。该过滤与基线切换不得重新生成movement state或acknowledgement，从而继续避免torn Tick。

备选方案是继续维护独立 `InputAcknowledgementStore` 与静态 state vector，再以 Tick相等重试。该方案会引入无界重试或临时无 snapshot窗口，而且无法形成真正的单 commit，所以不采用。

### 2. `InputTimeline` 输出 typed movement resolution

`IngressCommand` 保留 canonical payload用于确定性与 evidence，同时携带经过 `CommandIngress` 校验后的 typed move/aim值。`ActorInputResolution` 增加：

- 当前 Tick 应用的 move X/Z permille；
- 是否沿用 hold sample；
- 当前 Tick 是否存在合法 jump edge；
- 当前 Tick 最后一个规范排序 aim yaw update。

多个 input sample映射到同一 SimulationTick时，move和aim都取规范排序后的最后有效值；jump只作为当前 Tick的一次 edge被 Movement消费，不能 hold到后续 Tick。选择 typed resolution而不是在 Movement stage重新解析字符串，可以让 payload验证仍由 ingress唯一拥有，并避免第二套宽松 parser。

`SimulationInstance` 的 ready batch可能包含已由 `CommandIngress` 验证仍在冻结 late window内、但目标 Tick在网络到达前刚提交的 input。`InputTimeline` 必须在首个尚未提交的 current Tick消费这些 ready command；否则系统会一边推进 acknowledgement、一边静默丢失仍被判定为有效的 move/aim/jump。超出 late window的 command仍在 ingress拒绝，不能借此补执行真正过期输入。

### 3. 复用现有整数 Movement 模型，由 Physics stage提交状态

runtime参数固定为 50 ms、input scale 1000、maximum horizontal speed 3000 mm/s、acceleration/deceleration 60000 mm/s²、gravity 10000 mm/s²、jump speed 5000 mm/s，并与 `ClientBattlePolicy` 的 prediction参数保持契约一致。Movement只计算 candidate velocity/位移；server-owned `PhysicsWorld` 决议 capsule move与ground contact，Physics stage才提交 position、velocity、grounded。

客户端的25 ms cadence只负责semantic input采样、InputTick与bundle发送，不能把每个InputTick都当作一次Movement积分。映射到同一50 ms `SimulationTick` 的frame必须按server resolution规则fold：move与aim取最后样本、jump执行OR，然后从该SimulationTick的同一起点只积分一次。为了不增加首个样本的输入延迟，客户端可以在组内首个frame到达时立即预测整次50 ms step；同组后续frame只能从组起点重算，不能在已有结果上再叠加位移。

连续ack frontier可能被更早的missing InputTick阻塞，即使较新的frame已经在server current Tick被消费，它们仍会以“未确认”身份留在client history。由于authority snapshot已经提交到`latestServerTick`，client不得从该authority state再次积分`SimulationTick <= latestServerTick`的history；这些frame只继续服务于有限bundle冗余与ack pruning。重演只对严格晚于latest authority Tick的future group各积分一次。否则在本地网络也会按150 mm step累计出300/600/900 mm前后拉回，Scene smoothing无法修复该simulation horizon错误。

本 change为尚无正式地图碰撞资产的 current PersonalWorld runtime提供有界平地 adapter：ground level为 world Y=0，只接受登记的 ground-probe与capsule-move query，固定 capsule/step/slope policy并拒绝未知 query、超限 identity或容量。它是服务器 simulation adapter，不读取 Unity Scene。未来正式地图 adapter必须通过独立 change绑定 `physics_identity`，并保持同一项目 value contract和失败语义。

Current PersonalWorld客户端预测的平地落点还必须复现server adapter的百万分比crossing fraction与toward-zero整数缩放，包括落地Tick对水平位移的同比例截断；直接把Y钳制为零同时保留完整水平位移会在每次落地制造authority correction，因此不采用。

备选方案是在 Unity 端按 Transform Y或 collider推断 grounded；这会把表现状态升级为权威事实，直接违反 Scene/App Scope与服务器裁决边界，因此不采用。直接在 Movement中无 adapter地 clamp Y也不采用，因为会绕过 Physics mutation owner。

### 4. 扩展 `StateProjectionToken`，不改变 Protobuf schema

`StateProjectionToken` 增加 yaw、三轴 velocity与grounded。full/delta encoder必须显式设置 `QuantizedTransform` 的全部七个 scalar；yaw在 producer边界规范到 `[-180000, 180000)`，position/velocity继续做 checked/clamped 32-bit量化。现有 `battle.proto` 已有这些字段，因此不新增 field、不改 field number，也不需要 generated code schema变更。

`state_flags` registry固定为：

| Bit | Mask | Owner | 含义 |
|---:|---:|---|---|
| 0–3 | `0x0000000f` | gameplay phase projection | 保留现有 phase token |
| 4 | `0x00000010` | Movement/Physics | authoritative grounded |
| 31 | `0x80000000` | Death | entity dead |

其余 bit必须为零。选择 bit 4而不是 bit 0，是为了不重解释已有低四位 phase值；这保持当前 wire v1语义可迁移。registry写入 battle wire source corpus并由 producer、C++ protocol client与Unity decoder共同消费/断言。

### 5. Unity 只解码、消费，不成为 movement owner

Unity `ClientBattleEntityState.KnownStateFlags` 加入 grounded bit并提供封闭 `Grounded`投影。`ClientBattleRuntimeCoordinator.AcceptSnapshot` 将该值传给 prediction activation/reconciliation，删除恒为 `false` 的临时逻辑。local prediction仍只改善表现；authoritative correction基点始终来自 current full/delta replica。remote actor继续只使用 interpolation。Scene Actor registry在render timeline以保留跨帧位置/角速度的临界阻尼追踪local target，目标更新不能把平滑速度清零；Camera只跟随该平滑后的Actor Transform。该滤波只处理正常20 Hz target到render cadence的表现转换，不得用增大平滑延迟掩盖25/50 ms重复积分造成的往返target。

Editor与Development可通过既有qualification snapshot每两秒输出一次低敏prediction摘要，包含target rebase、authority/prediction三轴提前量、current generation最大rebase、未确认input数量、最近jump InputTick/SimulationTick及authority/predicted grounded。baseline尚未就绪时输出独立runtime摘要，而不是静默跳过。该日志不含PlayerID、endpoint、ticket、payload或credential，Release通过编译条件完整移除。它只提供诊断证据，不修改prediction、Scene Transform或authority state。

### 6. Scene camera 使用可读第三人称构图但不拥有 gameplay

真实Editor录屏显示既有四个rig共同使用`ShoulderOffset.y=-0.4`、`CameraDistance=2`与最长0.5秒轴向阻尼；FollowProxy位于胶囊体中心时，local actor几乎填满整个Game View，任何150 mm prediction step或camera vertical lag都会被视觉放大。已有Scene直接把四个rig与FollowProxy绑定到唯一`CinemachineCameraHost`，因此只调整这些已有serialized数值：

- Exploration以角色上半身高度为构图锚点并使用约4.5米距离；
- Melee与Ranged保留更近构图但不得进入胶囊体近裁剪范围；
- Cinematic使用更远只读构图；
- position damping保持低延迟且不与Actor 50 ms临界阻尼形成半秒级双重滞后。

Camera仍只消费平滑Actor Transform，不生成aim、collision或authority。Prefab、Input Actions、Host引用和`.meta`均不改变。

Editor停止Play或应用退出时，Unity不保证继续提供足够主线程帧让全部异步UI/connection清理在5秒内完成。App Scope仍执行原有尽力停止并释放claim；仅当`OnApplicationQuit`已确认进程/Play生命周期终结时，把`AppShutdownException`记录为不含credential的warning摘要。显式停止、普通Scene切换或非退出`OnDestroy`仍以exception暴露清理失败，禁止全局吞错。

### 7. Authenticated successor 接管旧 actor 会话并保持玩家可见状态闭合

真实双Player长时运行证明prediction可连续推进超过210秒；随后注入transport断线时，新ticket与`ServerAccept`成功，但旧UDP session仍保留在server active registry。新session的`ActiveProjection`因同actor重复而fail closed，导致新客户端永久停在`LoadingBaseline`。UDP不能依赖已断线client主动发送close，因此runtime在新session已认证且`ServerAccept`成功排队后、首个full snapshot前，必须查找同一`SimulationInstanceId`、`MappingGeneration`与ActorID的active predecessor，以`Target` lifecycle原因终结并移除它。该操作不影响其他actor；旧route立即不可路由，新session必须得到包含唯一actor的full baseline。

真实双Player恢复进一步证明，一次性BattleTicket的绝对expiry被错误应用到了已经`Consumed`的active actor：owner安装successor ticket时，child对ticket registry执行惰性expiry，顺带把仍在线visitor从`Consumed`改成`Expired`；visitor下一包authority验证失败，successor baseline因此只剩owner。Ticket expiry只约束尚未消费的credential能否建立新BattleSession，不能充当已认证会话的隐式租约。`Installed`在deadline后转为`Expired`并释放slot；`Consumed`则持续代表active actor和slot owner，只能由显式revoke、同actor successor、session/assignment/target/instance/node终结收口。这样既保留一次性ticket的抗重放边界，也避免其他玩家重连时误杀无关actor。

客户端把认证后的首个full baseline等待限定为5秒；deadline到期发布可恢复`Timeout`，交给既有single-flight recovery，不得无限保留`LoadingBaseline`。同target的`forceReconnect`在successor socket尚未建立时必须继续发布`Retrying`，不能退化为首入场使用的`Connecting`；Scene generation切换在`Retrying`和successor `LoadingBaseline`阶段保留最后已提交Actor实例与Camera follow，首个successor presentation按entity identity原子替换；目标切换、终态失败或Scene unbind仍清除旧view。HUD对保留画面明确显示`last known`，并使用可执行的Basic Latin产品文案区分进入、载入、重连、成功和终态失败，不显示transport/protocol内部token。

同actor的simulation input timeline不因transport generation替换而清零，因此successor full baseline可合法携带predecessor时期已经处理的非零`LastProcessedInputTick`。新`GameplayPrediction` generation必须把该值同时设为`LastAcknowledgedInputTick`与continuity anchor，并从`max(FirstInputTickAfterBaseline, anchor + 1)`开始发送；旧input/predicted history、command sequence、密钥与packet sequence仍全部重置。否则首个后续snapshot会把合法既有ack误判为超出本地sent frontier并终结successor。

Prediction render clock overrun仍保持fail closed并从下一authority snapshot重建timeline，但不得丢弃overrun前已经由Input owner采样的离散edge。该edge只保留在pending sample中，re-anchor后进入新InputTick一次；协议错误、send失败或history溢出仍清空pending，避免不可信命令跨边界复活。

Connected UDP endpoint在VisitWorld安全返回OwnWorld的快速target替换中可能复用本地port；旧attempt的`Retry`或`ServerAccept`因此可能先于current响应到达新socket。Client只凭公开magic识别phase后，必须再由current handshake transcript完成认证；无法认证的候选原位清零并在同一绝对deadline内继续等待，不得接受、不得延长deadline，也不得立即把可丢弃的旧datagram升级为终态`Security`。如果current响应始终缺失，既有deadline仍收敛为可恢复`Timeout`。

真实双Player资格harness必须先等待Owner完成首次账号、OwnWorld与own battle baseline，再启动Visitor。Owner随后继续等待Visitor identity，因此该barrier不缩减双端battle并发、210秒movement或断线恢复覆盖，只避免两个首次世界bootstrap同时争用本地服务端初始化事务而产生与battle无关的瞬时503。

### 8. 未生成 InputTick 随 authority horizon 前向重对齐

2026-07-31真实Editor录屏对应的Development日志显示，最近Jump的`InputTick=3191`映射为`SimulationTick=1596`，而同一摘要的authority已经提交到`ServerTick=1606`。该edge已经晚约10个SimulationTick（约500 ms），因此本地future-only replay正确地没有再次积分它，却表现为落地按键无响应；迟到的move/aim随后由authority提交又形成150 mm位置修订和14200 millidegree yaw修订。根因不是Scene damping，而是prediction只在首个baseline冻结`_nextInputTick`，后续完全按Unity本地累计时间推进，未处理Editor焦点、帧时钟钳制及两端长期微小漂移。

每次完整authority snapshot成功reconcile后，prediction必须计算`latestServerTick + InputLeadSimulationTicks`对应的最小InputTick，并只在当前`_nextInputTick`更小时向前推进。已经发送的history、ack frontier与command sequence不得改写；尚未生成的空洞由server既有gap expiry语义收口。Scene已经采样但尚未生成frame的Jump/ability edge继续保留在`_pendingInput`，并由重对齐后的首个合法future InputTick发送一次。

InputTick发生前向跳号后，bundle不能把跳号前history与新frame拼成伪连续区间。发送端只选择紧邻newest frame的连续history尾段；若不存在相邻前驱则发送单frame bundle，后续自然恢复1/2/3深度。Prediction replay仍可保留较旧未确认history，并继续只积分严格future authority组。

真实双Player回归进一步显示，Player运行期间客户端25 ms clock可能短时快于C++ simulation：诊断中的last sent SimulationTick持续领先最新authority 3至5 Tick，而server raw route的冻结early window仅允许2 Tick。此时UDP与AEAD仍正常、ack可因gap expiry继续推进，但move/aim/jump全部可能在route gate以TooEarly被拒绝，造成“本地先动再归零”和Jump无响应。Prediction因此还必须在生成前以最新已观察ServerTick限制上界：只允许两个属于`latestServerTick + InputLeadSimulationTicks`的InputTick；到达边缘后保留有限clock credit与pending edge并等待新snapshot。该authority pacing不是render overrun，不关闭input，也不产生catch-up风暴。

PersonalWorld当前只有天空盒形成的颜色地平线，缺少可判断速度和相机稳定性的世界空间纹理。Scene增加纯表现地面网格与固定参照物，提供方向、距离和视差线索；它们不带Gameplay owner，不进入服务器Physics、grounded、navigation或snapshot，不成为正式地图碰撞实现。

### 9. Render timeline 与输入owner切换必须所见即所得

2026-08-03真实Editor录屏中，主要移动时段的30 fps录制帧每帧都有画面变化，因此“顿顿的”不是录屏大量丢帧。现有registry虽每帧调用`SmoothDamp`，但跟随目标仍只在50 ms prediction step后前进；平滑器仅将位置阶跃变成速度脉冲，不能生成稳定render velocity。

Local Actor entry因此保留最新prediction sample的position、velocity与年龄。只有量化sample实际变化时重置年龄；重复消费同一immutable state不得把年龄清零。Render target在一个50 ms simulation step加有界到达抖动余量内按该velocity线性推进，超出后停止外推；正常下一sample与前一外推位置连续，authority correction仍由临界阻尼吸收。这只是纯表现外推，不产生input、collision、grounded或可回写事实。

Mouse/right-stick Aim在Scene host已以render cadence累积为semantic yaw。Camera follow proxy的position仍只读平滑Actor，但Exploration/Melee/RangedAim的纯表现rotation直接使用该current semantic yaw，避免相机等待下一prediction step。Cinematic保留actor projection rotation。输入发送前，local `Move(x,y)`以同yaw旋转为world X/Z；它不读Unity Camera Transform，所以仍是确定的semantic intent投影。

Tab打开UI时，唯一Input owner正确地禁用Player map并启用UI map，但Scene host过去在`TrySampleBattleInput=false`时什么都不提交，使`GameplayPrediction._pendingInput`继续hold切换前的非零move。修复后，只要battle input gate仍active而Gameplay sample不可用，Scene host就提交保留current aim、move为零、全部edge为false的neutral sample。这使UI、失焦和owner transition都不会在后台移动，恢复Gameplay时则从重新启用的Player map读取当前状态。

### 10. Render motor 与遥测不得制造长帧或停止回摆

2026-08-03 14:09真实Editor录屏为30 fps、206帧；Game View实际运动区间仍出现13个完全重复帧和明显隔帧更新。对应Editor日志显示正常窗口平均帧时仅4–8 ms，但两秒窗口最大帧普遍为160–248 ms，最近两次为392 ms与202 ms。诊断本身每两秒向当前可见Console写入并触发Editor重绘，形成稳定的observer effect。Scene host因此只在内存中累计frame count、总时长、最大帧与长帧分桶，运行中不写Console；解绑后一次输出无调用栈摘要。Release继续完整移除该路径。

旧local target会按sample velocity最多外推75 ms。server/current prediction在move归零的simulation step先把水平velocity降为零、再以零velocity积分，因此旧sample外推点会越过最终prediction position，下一sample到达时临界阻尼只能向后修正，正好对应松键小回摆。Local render motor改为消费Scene host已经量化的current world-space semantic move：每个render frame直接以冻结最大水平速度推进表现位置，neutral/UI/松键当帧停止；prediction sample只在超过一个正常50 ms最大水平步长时以有界速度连续修正，不产生反向target跳变。该motor不写回prediction、socket或authority，不改变server 50 ms Movement积分。Vertical jump仍以prediction state和连续render外推为基点，本轮不引入第二套碰撞或grounded owner。

## Risks / Trade-offs

- [平地 adapter 不能代表正式地图碰撞] → 明确限制为 current PersonalWorld B0.7 movement/jump前置，绑定 `physics_identity`，后续地图 collision需要独立 change替换，禁止把本次结果外推为完整 physics资格。
- [多个 actor初始位置重叠] → 当前 adapter不建立 actor-to-actor dynamic collision，identity与远端复制仍可验收；spawn layout和actor collision属于后续内容/地图规格。
- [state token扩展改变 canonical projection digest] → 同步更新 projection tests、wire corpus manifest与所有直接绑定 digest；不手改 generated code。
- [Tick observer异常导致实例失败] → candidate先完整计算，只有全部 Physics query与projection验证成功后才原子发布；失败不暴露半 Tick state。
- [新增 transform字段扩大 payload] → 保持现有 raw route payload ceiling与partition hard limit，通过实际 Protobuf size测试验证，不扩大1200-byte MTU。
- [工作区已有 Unity生成的 `.meta`] → 保留用户文件，自动化仅审计本 change diff中没有新增或修改 `.meta`。
- [25 ms input采样被误作25 ms movement积分] → 按`SimulationTick`聚合history，从组起点重算同组末态，并以ack前后位置完全不变的回归测试锁定；Scene smoothing只处理正常render cadence。
- [客户端平地钳制与server fraction量化不同] → 复现百万分比crossing与toward-zero缩放，增加移动中落地的position/velocity/grounded精确断言。
- [20 Hz local target在render timeline产生速度脉冲] → Scene registry保留临界阻尼速度状态，目标更新不重置速度，并以连续三帧retarget位移上界测试锁定；Camera仍只跟随平滑Actor。
- [Ack gap让已落入authority horizon的frame继续留在history] → Prediction按`latestServerTick`排除过期组，只保留严格future group积分，并以ack frontier不推进但authority Tick前进的回归测试锁定target不变。
- [近距中心Camera放大prediction量化与垂直滞后] → 直接更新已有Scene rig的上半身锚点、距离与阻尼；PlayMode反序列化检查锁定Exploration构图，不创建第二套camera owner。
- [UDP predecessor无法感知client进程消失] → 只允许已完成新ticket认证的同actor successor在首个baseline前接管；不同actor与不同instance/mapping不受影响，旧route立即失效并记录lifecycle close。
- [短期ticket expiry被误当成active session lease] → 只对`Installed`凭据提交expiry；`Consumed`继续占用actor slot并由显式authority lifecycle终结，以双actor过期后successor测试锁定无关visitor不被移除。
- [快速target替换复用UDP endpoint并收到旧handshake响应] → envelope只做phase预筛；必须用current transcript认证，失败候选清零丢弃并受原绝对deadline约束，绝不接受旧响应或把deadline滑动。
- [双Player同时首次创建OwnWorld触发瞬时503] → 真实battle资格先等待Owner完成own battle ready再启动Visitor；双方进入同一battle后的持续并发、恢复与teardown矩阵保持不变。
- [恢复期保留旧画面可能被误认作实时事实] → 关闭输入并在HUD明确显示重连与`last known`；只在可恢复状态保留，终态失败和目标切换立即清除。
- [首个baseline永远缺失] → 5秒deadline发布可恢复Timeout并进入有界重试，禁止无限loading；最终失败给出重新进入世界的玩家动作。
- [Unity与server长期时钟漂移让新input落入authority过去] → 每次reconcile仅前向校准尚未生成的InputTick到冻结lead horizon，保留pending edge并以落地Jump与aim/move drift回归测试锁定。
- [Unity本地clock快于server使input超出early window] → 在input生成前按最新已观察ServerTick限制冻结horizon；边缘等待保留pending edge与有限clock credit，不发送TooEarly frame、不误触发render overrun。
- [InputTick前向跳号破坏bundle相邻约束] → bundle只携带newest frame之前的连续history尾段；旧history仍由ack与future-only replay分别管理。
- [世界参照被误当成碰撞事实] → 网格和参照物只含Scene Renderer，不挂载业务owner或Collider；current服务器平地adapter仍是grounded唯一裁决者。
- [有界render外推在snapshot中断时继续移动] → 外推年龄不超过1.5个simulation step，超限后只让已有平滑状态收敛；新sample仍是唯一target基点。
- [Camera直接读semantic aim被误当为authority] → 只改follow proxy的Scene rotation，输入协议仍提交同一量化yaw，服务器仍唯一提交actor orientation。
- [UI切换后旧WASD在prediction中hold] → battle gate active但Gameplay sample不可用时持续提交neutral continuous sample，并以UI打开期间位移停止及返回后重新采样的回归测试锁定。
- [运行中Console诊断制造周期性长帧] → frame telemetry只进入定长内存聚合，Scene解绑后一次无调用栈输出；以运行中零日志和长帧分桶回归锁定observer effect不会复发。
- [旧sample外推越过停止点] → local水平render motor消费current semantic move并逐帧积分，prediction只做有界连续校正；松键/neutral帧禁止继续使用旧水平velocity外推。

## Migration Plan

1. 先冻结 delta specs、wire registry与 closed validation plan。
2. 实现 typed input resolution、server Physics adapter与原子 movement/ack projection store。
3. 更新 snapshot encoder、C++ runtime/transport/protocol tests。
4. 更新 Unity known flags、grounded reconciliation和纯 C# tests。
5. 根据真实录屏修正stale history replay、已有Scene camera数值与Development诊断，不创建资产或`.meta`。
6. 修复同actor successor接管、已消费ticket生命周期、baseline deadline、恢复期最后可信画面与长帧离散edge保留。
7. 修复长期运行的InputTick drift，增加纯表现地面网格与固定参照物。
8. 运行 `quality.ps1 impact/check-change` 定向验证；成功后把父 change 6.8标记完成。
9. 使用真实双Player连续运行并注入transport断线，验证successor baseline、Actor、HUD和Input恢复后再交付用户主观验收。
10. 根据2026-08-03真实录屏增加local render extrapolation、render-cadence camera aim、camera-relative move与UI切换neutralization，完成定向自动验证后再由用户评估最终手感。
11. 根据第二段真实录屏与Editor帧诊断移除运行中Console observer effect，并以semantic move驱动local水平render motor，自动验证后重新进行录屏与手感验收。

回滚时同时撤销 store、registry和Unity grounded consumer，恢复父 change 6.8未完成状态；不得只回滚一端后继续宣称真实 movement/jump可验收。

## Open Questions

无。本 change明确采用 current PersonalWorld平地 PhysicsWorld作为过渡 adapter；正式地图碰撞、spawn layout与actor collision均需后续独立规格。
