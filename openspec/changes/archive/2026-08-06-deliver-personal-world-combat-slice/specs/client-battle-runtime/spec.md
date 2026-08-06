## ADDED Requirements

### Requirement: Unity 必须以 production mapping 投影 combat content 而不复制 authority

Unity build MUST 以build-time parity消费production presentation catalog、numeric wire mapping与tracked resource catalog，并为actor、weapon、ability、effect/cue logical key提供唯一Scene资源映射。Pure C# replica/projector MUST 从current generation的archetype、weapon、health/max-health、phase/dead、ability/lifecycle event产生immutable `ActorViewState`、`HudViewState`、`GameplayCue`与`CameraIntent`；ScriptableObject、Prefab、Animator、VFX、Audio、HUD和Scene collider MUST NOT包含或覆盖authority damage、cooldown、AI、hit、spawn、death或reward规则。

Unity工程 MUST 使用`Assets/App/Modules/<Feature>/<Layer>/`功能模块优先结构保存全部手写脚本、Editor工具、测试与非代码内容，并把稳定asmdef集中在`Assets/App/Assemblies/`。`Core` MUST 只拥有跨feature技术基础，第三方project resource MUST 隔离在`Assets/App/ThirdParty/`，没有复用证据不得创建万能`Shared`。模块 MUST 通过`.asmref`保持既有Foundation/Application/Infrastructure/Presentation/Runtime与test程序集DAG、`noEngineReferences`和generated protocol边界；物理迁移 MUST 通过锁定Unity Editor的`AssetDatabase`保留全部非目录asset GUID与序列化引用，一对一文件夹移动保留folder GUID，只表达旧拓扑且无资产/GUID引用的空folder GUID可由Unity显式退役。不得使用文件系统移动、手写`.meta`、制造循环程序集或让Content目录编译业务脚本。

全部手写类型 MUST 使用`IHomeland.Client.<Feature>.<Layer>[.<Subarea>]`namespace，集中asmdef MUST 使用中性`IHomeland.Client` root namespace，跨feature Composition、Editor入口、owner registry和tests MUST 只引用迁移后的类型名。Assembly name与依赖方向 MUST 继续表达稳定layer DAG，不能仅因目录迁移拆成循环feature assemblies；旧namespace不得在production源或当前工具契约中继续形成第二套身份。

#### Scenario: Boss lifecycle spawn
- **WHEN**current lifecycle/full state以登记Boss archetype与合法generation到达
- **THEN**pure C# mapping发布Boss view与HUD semantic state，Scene registry实例化唯一Boss表现，且Boss health/phase只随权威snapshot变化

#### Scenario: Presentation resource 缺失
- **WHEN**production semantic mapping存在但trackedUnity resource catalog缺少对应Prefab、Animator、VFX、Audio或HUD binding
- **THEN**build/parity失败或battle feature明确unavailable，不使用名字匹配、Resources扫描、默认Prefab或authority数值猜测恢复

#### Scenario: 旧 Battle asset 迁入功能模块
- **WHEN**既有GenericActor Prefab、PersonalWorldReference Material及Battle内容目录从旧类型目录迁入`Content/PersonalWorldCombat`
- **THEN**Unity AssetDatabase保留每个既有非目录asset GUID和Scene/Prefab引用，一对一移动目录保留folder GUID，合并后已空且无引用的旧Battle目录由Unity删除；该隔离步骤不重排非Battle资产

#### Scenario: 全 Unity 工程迁入功能模块
- **WHEN**手写runtime、Editor、tests、Scene、Prefab、UI、Input、rendering settings与project vendor resource按owner迁入Core、AppShell、Session、Networking、PersonalWorld、PersonalWorldCombat与独立ThirdParty
- **THEN**全部既有非目录asset GUID和序列化引用保持稳定，一对一目录GUID保留、无引用旧拓扑folder GUID由Unity退役，五个生产程序集与三个test程序集依赖不变，generated目录仍由工具独占，旧物理根只在确认无未登记文件后由Unity删除

#### Scenario: Feature namespace 与layer assembly同时成立
- **WHEN**PersonalWorldCombat、PersonalWorld、Session、Networking、AppShell与Core脚本完成namespace迁移并由分布式asmref编译
- **THEN**类型名以feature开头、程序集仍按Foundation/Application/Infrastructure/Presentation/Runtime单向依赖，Composition Root显式引用模块bundle且Scene/Prefab不存在missing script

### Requirement: Combat input、cue 与 HUD 必须服从唯一 input 和 Scene owner

Input System MUST 通过既有input/focus generation把move、aim、jump、switch-weapon与primary-ability映射为pure C# semantic intent；UI、View或Animator不得直接写transport。Scene-bound uGUI MUST 显示current player health/max-health、equipped weapon/primary state与Boss health/phase，UI Toolkit继续拥有页面/overlay；Actor animation、VFX与Audio MUST 只响应current authoritative ability/lifecycle/snapshot-derived cue。菜单、失焦、safe-return、terminal failure或Scene teardown MUST 先关闭input并使旧cue/View/HUD callback无效。

#### Scenario: 玩家切换到扇子并攻击
- **WHEN**current input owner提交switch edge且后续snapshot确认fan weapon，随后权威ability event确认fan primary
- **THEN**HUD与camera进入登记ranged presentation，Animator/VFX/Audio按fan semantic cue播放，但aim、projectile、hit与damage仍由server裁决

#### Scenario: UI Toolkit modal 打开时按下攻击
- **WHEN**会捕获输入的existing modal拥有focus且玩家触发primary或switch action
- **THEN**gameplay input owner不提交battle command，uGUI HUD不切换action map，关闭modal后只从current input state继续

#### Scenario: 旧 generation hit cue 迟到
- **WHEN**reconnect或assignment replacement后predecessor generation的合法ability event到达main thread
- **THEN**generation gate丢弃该event，不播放VFX/Audio、不修改Boss HUD，也不销毁successor actor View

#### Scenario: PC 同时存在键鼠与持续上报轴值的虚拟设备
- **WHEN**current Player map已配对Keyboard/Mouse，系统中的通用`Joystick`、`XR`、`Touch`或未被明确选择的虚拟`Gamepad`持续上报非零轴值，玩家按下并释放WASD
- **THEN**唯一input owner只读取已配对Keyboard/Mouse，release后的Move立即回到neutral，其他设备不能成为隐式后备值，且tracked Input Actions资产不被修改

#### Scenario: Player 从键鼠切换到一个 Gamepad
- **WHEN**exact Gamepad产生新的明确按钮按下，随后玩家操作其stick
- **THEN**唯一input owner在Input System callback之后把Player map原子配对到该单一Gamepad并抑制切换瞬变；其他Gamepad、Keyboard/Mouse、Joystick、XR与Touch不会与其轴值合并，后续键盘或鼠标明确输入可确定切回Keyboard/Mouse

#### Scenario: Aim binding 同时登记 Mouse 与 Touch group
- **WHEN**runtime private clone限制PC Player devices，且tracked `<Pointer>/delta` binding属于`Keyboard&Mouse;Touch`
- **THEN**Mouse delta仍产生semantic Aim并驱动current camera yaw，Touch因未配对而不可用；input owner不得按Touch group清空整个共享binding

#### Scenario: 玩家松开移动键
- **WHEN**local prediction已经因WASD产生位移，随后已配对Keyboard报告按键释放并提交neutral Move
- **THEN**后续InputTick携带neutral Move，prediction owner只完成current fixed-timestep `PresentationTransform`且Scene不再由raw Move或第二只计时器积分，因此不会持续自行移动、反向追赶或由未配对设备恢复移动

#### Scenario: Local prediction 位置采样低于 render cadence
- **WHEN**稳定移动按25 ms InputTick采样、20 Hz Simulation积分且authority按10 Hz每2 Tick发布snapshot，两个publication之间的中间SimulationTick没有新frame但仍处于4-Tick continuous hold窗口
- **THEN**GameplayPrediction从authority到future horizon逐Tick重演并在中间Tick复用last Move，send pacing只保留一个InputTick credit且不在放行帧burst；prediction-owned timeline发布唯一有界`PresentationTransform`，Scene同帧精确提交、Camera跟随同一输出，下一等价authority不产生一步rebase

#### Scenario: 移动中的 Actor 不再播放 Idle 根节点起伏
- **WHEN**immutable local prediction或remote interpolation Transform具有非零水平速度，且current actor未死亡
- **THEN**ActorView只由该Transform派生`Moving`并驱动Animator进入in-place Locomotion；`VisualRoot`保持零local position与单位scale，Idle呼吸只在水平速度为零时播放，Animator不读取raw Move、不启用Root Motion也不回写actor位置

#### Scenario: Local player 权威死亡
- **WHEN**current generation的完整snapshot提交local actor dead flag与零health
- **THEN**input gate在同一提交中关闭，pending edge、未确认input与预测history被清除，local presentation收敛到权威transform，并且successor generation完整baseline前不再产生移动或ability command

#### Scenario: Successor baseline 后 authority 继续终结缺失 InputTick
- **WHEN**同actor successor的full baseline携带非零ack，后续完整publication按冻结6-Tick gap-expiry数学边界继续推进frontier
- **THEN**客户端接受不超过该精确边界的单调ack、清除已终结history并把下一InputTick前向对齐；超过sent/skip与gap-expiry两种合法上限的future ack仍fail closed

### Requirement: Combat replica 必须从 full baseline 恢复完整可玩表现

Battle activation与reconnect MUST 等待current full baseline原子提交后才开放combat input。该baseline MUST 为所有可见entity提供可解析archetype、health/max-health、state flags及player weapon；lifecycle与delta MUST 在同generation上继续演进。Unknown/missing mapping、archetype冲突、health invariant、Boss phase、weapon或entity generation gap MUST 请求唯一resync或进入closed terminal/recovery path，不能部分更新View或用旧Scene缓存补齐。

#### Scenario: Battle-only reconnect 恢复进行中的 Boss 战
- **WHEN**successor generation收到包含current players、monsters、Boss、projectiles与weapon/phase状态的完整baseline
- **THEN**replica一次提交完整state，Scene重建对应View/HUD并在旧资源清理后开放input，predecessor prediction与cue不被继承

#### Scenario: Full state 的 health 超过 max health
- **WHEN**full或lifecycle initial state携带`health_milli > max_health_milli`
- **THEN**closed decoder/replica拒绝整组提交并走current resync/recovery，HUD不得clamp后显示伪造合法状态

### Requirement: Combat Scene 必须完成可重复 PC 表现与 teardown 验收

PersonalWorld Scene MUST 提供与server map identity对齐的基础可读地面、障碍、spawn与camera anchors，以及player/monster/Boss/projectile表现、Animation、VFX、Audio和uGUI HUD。EditMode/PlayMode与Windows build smoke MUST 覆盖mapping、input/focus、cue、HUD、generation、cancellation和资源释放；真实双Development Player场景 MUST 证明Owner/Visitor可见同一敌人/Boss状态、分别使用剑/扇子并在disconnect/reconnect、safe-return或replacement后收敛。Unity Physics/Scene结果 MUST NOT 被验收脚本当作server hit或death证明。

#### Scenario: PersonalWorld Scene 卸载
- **WHEN**Visitor safe-return、assignment replacement或App shutdown卸载currentScene generation
- **THEN**monster/Boss/projectile/player Views、Animator/VFX/Audio、HUD、input/camera hosts与subscriptions恰好释放一次，App Scope是否恢复battle只由current world/battle lifecycle决定

#### Scenario: Unity 已接管应用退出
- **WHEN**Player退出或Editor离开Play Mode触发`OnApplicationQuit`，且current PersonalWorld Scene、UI routes或battle generation仍active
- **THEN**Experience只撤销自身intent、subscriber与generation，Router唯一清理active/cached routes，Scene Host立即失效Context与SceneLifetime且不再等待显式`UnloadSceneAsync`，runtime Input Actions clone按当前Play Mode状态选择合法销毁API，AppLifetime在共享deadline内完成且不记录`IHOMELAND_APP_SHUTDOWN outcome=best_effort`或edit-mode `Destroy`错误

#### Scenario: 双 Player 击败 Boss
- **WHEN**Owner与Visitor Development Player通过真实Go/C++ runtime完成登录、邀请、进入同一PersonalWorld并协作战斗
- **THEN**双方看到可收敛的weapon、monster、Boss health/phase/death与encounter-complete表现，evidence绑定server authority状态而非客户端Collider或截图推断
