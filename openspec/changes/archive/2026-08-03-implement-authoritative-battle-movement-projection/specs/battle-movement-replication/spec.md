## ADDED Requirements

### Requirement: Battle input 必须进入唯一权威 Movement/Physics commit

Current BattleSession 的 move、aim 与 jump intent MUST 经 `CommandIngress` 和 `InputTimeline` 规范化后，只由 `SimulationInstance` 唯一 worker 在目标 SimulationTick消费。连续 move与aim MUST 对同一 Tick取规范排序后的最后有效样本；move可按登记 hold窗口延续，aim保持最后已提交 yaw，jump MUST 只作为当前 Tick离散边沿消费一次且不得 hold。Movement MUST 只产生 candidate，position、yaw、velocity与grounded MUST 在 Physics stage成功后作为一个 actor state原子提交；客户端、网络线程、Unity Scene和snapshot encoder不得直接修改该状态。

#### Scenario: 两个 move sample 映射到同一 Tick

- **WHEN** current actor的两个合法 move sample映射到同一 SimulationTick且网络到达顺序相反
- **THEN** worker按 InputTick与command sequence规范顺序选择最后样本，产生相同 committed position与velocity

#### Scenario: Grounded actor 跳跃

- **WHEN** current actor在 Movement stage开始时authority grounded且当前 Tick含唯一合法 jump edge
- **THEN** server应用一次 jump impulse，Physics提交 airborne state与正向Y velocity，后续 snapshot不读取客户端grounded或预测Transform

#### Scenario: 空中重复跳跃

- **WHEN** authority state已 airborne且后续 Tick又收到 jump edge
- **THEN** server不应用第二次 impulse、不把edge延后到落地后执行，并继续按gravity推进 committed velocity与position

### Requirement: 客户端预测必须按权威 SimulationTick 重演

Unity客户端MUST把25 ms cadence限制为semantic input采样、InputTick生成与bundle发送，并MUST按50 ms authority `SimulationTick`聚合本地预测。映射到同一SimulationTick的frame MUST使用与server resolution相同的fold：move和aim取最后有效样本、jump edge执行OR，Movement只从该组共同起点积分一次。组内首个frame MAY立即预测完整SimulationTick以降低输入延迟；后续同组frame MUST从组起点重算，MUST NOT在已有组结果上追加第二次积分。Reconciliation MUST从authority state只按严格晚于latest authority ServerTick的未确认future SimulationTick组重演；因连续ack gap仍保留但映射Tick不晚于authority的frame MUST只保留发送/确认所有权，MUST NOT重复积分。每次成功reconcile后，尚未生成的InputTick MUST在落后时只向前重对齐到`latest authority ServerTick + frozen lead`；本地clock快于authority时MUST停在已观察server Tick的冻结early window末端等待新snapshot，不得发送服务器必然以TooEarly拒绝的frame，也不得把该等待误判为render overrun。已发送history、ack和sequence不得改写，已采样离散edge不得丢失；发生InputTick跳号时，bundle MUST只携带newest frame之前的连续history尾段。current PersonalWorld平地落点MUST复现server crossing fraction与toward-zero整数缩放，不得以Scene smoothing掩盖周期性预测漂移。Scene local Actor MUST以保留跨render frame位置/角速度的平滑状态追踪prediction target，目标更新MUST NOT重置该速度。Local水平render motion MUST在Gameplay owner可用时以current world-space semantic move逐帧推进，neutral/UI/松键当帧停止，prediction sample只能提供有界连续校正，MUST NOT继续按旧velocity外推后反向回到停止点。Vertical render target MAY继续使用已投影velocity有界推进；重复消费同一sample MUST NOT重置其年龄。Camera position MUST只跟随平滑后的Actor Transform，非Cinematic camera rotation MUST逐render frame消费当前semantic aim yaw，且MUST使用不会让current generic capsule进入近裁剪主视野的Scene-owned第三人称构图。Local move MUST在发送前按同yaw转换为world X/Z，不得从Camera Transform反推。Gameplay输入owner被UI或focus替代而battle gate仍active时，客户端MUST提交零move、无edge的neutral sample，不得hold切换前的连续输入。Editor/Development运行中render telemetry MUST只做有界内存聚合，并MUST NOT周期性写Console；Scene解绑后MAY一次输出低敏摘要。PersonalWorld MUST提供不参与authority或Physics的稳定世界空间地面/固定参照，以便玩家判断Actor与Camera运动。

#### Scenario: 两个 InputTick 映射到同一 SimulationTick

- **WHEN** 两个连续25 ms InputTick映射到同一50 ms SimulationTick且都仍在本地prediction history
- **THEN**客户端只产生一次50 ms movement结果，第二个frame只按last move/aim与OR jump从组起点重算，不能额外推进position

#### Scenario: Authority 确认完整预测组

- **WHEN** authority snapshot精确确认一个已预测SimulationTick且后续未确认组使用相同输入和整数参数
- **THEN**客户端从authority重演后的current transform与reconciliation前完全一致，不产生周期性前后拉回

#### Scenario: 连续 ack gap 保留已进入 authority horizon 的 frame

- **WHEN** authority snapshot已推进到某ServerTick，但更早missing InputTick让ack frontier未推进，client history仍包含映射到该Tick或更早的frame
- **THEN**客户端保留这些frame用于有限发送与确认，但只从authority state积分严格future group，reconciliation前后current target保持一致

#### Scenario: 移动中落到 current 平地

- **WHEN**预测actor在具有水平速度的50 ms step内从world Y大于零穿越Y=0
- **THEN**客户端使用与server平地adapter相同的百万分比crossing fraction同比例截断三轴位移，并提交相同position、零负向Y velocity与grounded候选

#### Scenario: Local target 在连续 render frame 更新

- **WHEN**20 Hz prediction target在相邻render frame之间推进或修订且local Actor已经具有平滑速度
- **THEN**Scene表现保留跨帧速度并以临界阻尼继续收敛，不在target更新帧snap或产生速度尖峰，Camera follow proxy读取同一平滑Transform

#### Scenario: Local target 在50 ms prediction sample之间推进

- **WHEN**最新local prediction sample携带非零world velocity，后续render frame重复消费同一immutable sample
- **THEN**Scene在有界年龄内按velocity连续推进render target，不会每50 ms加速一次再减速；超过1.5个simulation step时不再继续推测

#### Scenario: Camera与移动共享current semantic yaw

- **WHEN**玩家在Exploration中移动鼠标或右摇杆，并按下W/前向输入
- **THEN**Camera当帧按semantic yaw转动，发往prediction/server的move按同yaw转换为world方向，画面前方与角色移动前方一致

#### Scenario: Tab菜单取代Gameplay输入owner

- **WHEN**玩家按住非零WASD后以Tab打开UI，Player map被禁用但battle generation仍active
- **THEN**Scene host在下一render frame向prediction提交neutral continuous sample，角色停止且UI期间不在后台移动；返回Gameplay后从current Player map重新采样

#### Scenario: 松开移动输入时local render停止

- **WHEN**local Actor正在以非零semantic move逐render frame移动，下一帧Gameplay sample归零
- **THEN**水平render motor当帧停止继续外推，后续prediction sample只做有界连续校正，Actor Transform不得先越过最终点再反向移动

#### Scenario: 运行中收集render telemetry

- **WHEN**Editor或Development Player处于active battle并连续采集frame time
- **THEN**客户端只更新有界内存计数，不周期性写Console或创建无界样本；Scene解绑后最多输出一次不含identity、endpoint或payload的摘要

#### Scenario: Exploration camera 跟随 current generic actor

- **WHEN**current local generic capsule激活且Camera intent为Exploration
- **THEN**Scene-owned rig以上半身高度和可读第三人称距离跟随平滑Actor，胶囊体不得几乎填满Game View，Camera damping不得重新引入半秒级双重滞后

#### Scenario: Render长帧期间按下跳跃

- **WHEN**已采样Jump edge后render clock overrun使prediction要求从下一authority snapshot重建timeline
- **THEN**input gate在re-anchor前保持关闭，但该edge不会被丢弃，并在新timeline的首个合法InputTick发送且预测一次

#### Scenario: 可恢复连接切换到 successor generation

- **WHEN**current Actor已有可信presentation且battle transport进入Retrying并取得successor baseline
- **THEN**Scene在输入关闭期间保留最后可信Actor/Camera画面并明确标识重连和last known生命值，首个successor projection无空白帧地替换它；终态失败或目标切换不得继续显示旧Actor

#### Scenario: Successor baseline 携带 predecessor 已处理输入

- **WHEN**同actor的successor full baseline携带非零`LastProcessedInputTick`且client新transport generation尚未发送输入
- **THEN**prediction以该值初始化ack与continuity anchor、清空旧预测history并从更大的新InputTick继续；后续相同或递增authority ack不得被误判为future ack

#### Scenario: 长期运行后本地 InputTick 落后 authority

- **WHEN**Unity本地累计时钟因焦点、帧钳制或长期漂移使下一InputTick映射到`latest authority ServerTick`或更早，且Scene已经采样Jump或连续move/aim
- **THEN**prediction把尚未生成的InputTick只向前推进到冻结lead horizon，首个新frame携带当前continuous sample与离散edge一次，本地立即按future SimulationTick预测，且bundle不跨越跳号空洞

#### Scenario: 本地 InputTick 快于 authority

- **WHEN**客户端累计clock使下一InputTick超过最新已观察ServerTick的冻结early window，且玩家已采样Jump或continuous input
- **THEN**客户端保留该sample并停止生成TooEarly frame，等待新authority snapshot后在合法horizon内恢复发送；等待不触发clock overrun且Jump只发送一次

#### Scenario: 玩家使用世界参照判断运动

- **WHEN**current PersonalWorld显示local Actor并移动、跳跃或转动Camera
- **THEN**Scene显示稳定地面网格与固定参照物产生可读视差，但这些Renderer不得改变authority position、grounded、Physics query或input resolution

### Requirement: Movement state 与输入确认必须形成同一 committed projection

Runtime MUST 为每个 mapping generation维护固定且有界的 actor state集合。每次成功 Tick MUST 原子发布同一 nonzero `ServerTick` 的完整公开 actor states和全部 actor acknowledgement；网络线程 MUST 只读取 owned immutable副本。Snapshot面向某个 authenticated BattleSession时 MUST 选择该 actor/generation的 `LastProcessedInputTick`，同时携带同一 commit的全部公开 actor states；任一 identity、generation或Tick不一致 MUST fail closed。公开 active actor集合发生变化时，producer MUST 强制发送 full baseline，因为现有 delta state不能表达 entity创建或移除；不得继续在旧actor集合上发布delta。

#### Scenario: Snapshot 与 acknowledgement 并发读取

- **WHEN** network thread在simulation worker提交下一 Tick期间请求snapshot projection
- **THEN** 它只能取得提交前或提交后的完整副本，不能组合旧movement state与新acknowledgement

#### Scenario: Owner 与 Visitor 观察同一 actor set

- **WHEN** Owner和Visitor分别通过自己的authenticated BattleSession读取同一 committed Tick
- **THEN** 两者获得相同排序的公开 actor state集合，但各自snapshot只携带自己actor与mapping generation的输入确认前沿

#### Scenario: 固定容量包含未占用 actor slot

- **WHEN** simulation 为最多 8 个 actor 预分配有界 state，但当前 instance 只有一个 authenticated active BattleSession
- **THEN** snapshot 只发布该 active session 绑定的 actor，不把另外 7 个容量槽投影为 entity、Actor View 或可观察占位状态

#### Scenario: Active session 集合发生变化

- **WHEN** 第二个 authenticated session 加入或一个既有 session 终结并从 active registry 移除
- **THEN** 下一次publication强制发送包含更新后active actor集合的full baseline；加入者出现且离开者消失，后续delta只在该新基线内推进，内部固定容量 state不决定公开生命周期

### Requirement: Snapshot movement registry 必须跨 producer 与 Unity consumer 闭合

Full与delta snapshot MUST 显式编码 position X/Y/Z、yaw、velocity X/Y/Z、health和`state_flags`。Yaw MUST 规范为 `[-180000, 180000)`；`state_flags` MUST 只允许phase mask `0x0000000f`、grounded `0x00000010` 与dead `0x80000000`。Producer、C++协议客户端和Unity decoder MUST 拒绝未知 bit、缺失 transform scalar、非法yaw或mask/presence不一致。Unity reconciliation MUST 只从grounded bit取得authority grounded，不得从position高度、Scene collider、phase或本地prediction推导。

#### Scenario: Grounded snapshot 进入 reconciliation

- **WHEN** current local entity的合法snapshot设置grounded bit并携带完整transform
- **THEN** Unity prediction以该authority transform与grounded为重演基点，且pure client prediction仍不成为服务器事实

#### Scenario: Snapshot 含未知 state flag

- **WHEN** full、delta或lifecycle state设置registry之外的任一 bit
- **THEN** C++/Unity consumer拒绝该state或完整publication，不掩码、不猜测含义且不更新replica

#### Scenario: Producer 未写 velocity presence

- **WHEN** snapshot transform缺少任一yaw或velocity scalar的显式presence
- **THEN** consumer把producer视为不兼容并拒绝publication，不能把缺失字段默认为零
