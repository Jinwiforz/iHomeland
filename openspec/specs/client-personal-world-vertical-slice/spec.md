# Client Personal World Vertical Slice 规格

## Purpose

定义 Unity 客户端个人世界首期产品入口、表现状态、产品 Scene、访问动作、安全返回、失败收敛与双客户端验收边界。

## Requirements

### Requirement: 产品入口必须从安全会话恢复或本地登录显式启动

客户端 MUST 在 App Scope 全部初始化成功后先进入唯一 `RestoringSession` route/state，并对当前环境的 secure session store 执行一次有界恢复；没有 record、平台不支持、record 无效或服务端明确拒绝 lineage 时 MUST 清理恢复状态并打开唯一 `Login` route。合法 record MUST 先完成冻结 version/config bootstrap 与一次 refresh 轮换，再启动唯一 control run，并在该 run 首次进入 `Connected` 后显式请求进入自己的 PersonalWorld。Register/login intent 仍 MUST 由玩家在 Login 显式提交，且先完成同一 version/config bootstrap 再调用唯一 Session owner。若 control 在 `Connected` 前稳定失败，客户端 MUST 保留已安全提交的 Session、停止进入世界并呈现可重试的低敏连接失败，不能在无法接收定向 invite push 时伪装产品流程已就绪。Password MUST 只存在于当前输入与调用参数，页面隐藏、失败、停止或提交完成后 MUST 清除，不得进入 View State、日志、Scene、Prefab 或异常文本。

#### Scenario: 新进程没有可恢复 session

- **WHEN** Windows Player 完成 App Scope 初始化且 secure session store 不存在 current record
- **THEN** 恢复 owner 不发送 refresh、ticket、world 或 channel 请求，唯一 active screen 从 RestoringSession 收敛到 Login

#### Scenario: 新进程恢复 session 并进入自己的世界

- **WHEN** secure record、version/config、refresh、control start 和 own-world flow 依次成功
- **THEN** Session owner 以轮换后的唯一 lineage 提交新 generation，Experience 无需密码离开恢复状态并进入 OwnWorld，View 与 Scene 不取得 token、ticket 或 admission

#### Scenario: 恢复 lineage 被拒绝

- **WHEN** refresh 明确返回 unauthenticated、record 损坏/不匹配，或恢复提交结果未知
- **THEN** 客户端 fail closed 删除不可继续使用的 record、关闭候选 route/channel/target并显示 Login 或稳定低敏恢复失败，不自动循环 refresh

#### Scenario: 登录并进入自己的世界

- **WHEN** 玩家提交合法登录信息且 bootstrap、login、control start 和 own-world admission 依次成功
- **THEN** Session owner 保存唯一认证事实与安全 refresh lineage，Experience 离开 Login 并进入 own-world loading/scene 流程，View 与 Scene 不取得 token、ticket 或 admission

#### Scenario: 登录失败

- **WHEN** bootstrap 不兼容、凭据被拒绝、限流、timeout 或 transport failure
- **THEN** Login 保持唯一 active screen、恢复可重试输入并显示稳定低敏错误，password 被清除且客户端不自动重复认证

### Requirement: Experience 必须只拥有表现意图与不可变 View State

唯一 `ClientPersonalWorldExperience` MUST 只保存 presentation generation、当前 UI intent、scene transition intent 与从 Session、WorldAdmission、PersonalWorld 和 VisitSession snapshot 派生的不可变 View State。账号、world、visit、assignment、role、revision 和 credential 最终事实 MUST 继续由既有 App Scope owner 保存。页面 MUST 只接收当前 View State 与窄语义 action port，MUST NOT 取得 transport、generated message 或 `AppCompositionResult`。

#### Scenario: 页面重新打开

- **WHEN** WorldVisit 页面关闭期间 Service 收到更高 revision invite/member snapshot，随后页面重新打开
- **THEN** 页面从当前不可变 View State 完整呈现最新事实，不要求 channel 重放、不读取旧 view，也不复制 revision owner

#### Scenario: UI Toolkit 运行时重载

- **WHEN** `PanelRenderer` 在 WorldVisit route 与 gameplay command 仍存活时重建 VisualElement 树
- **THEN** 产品 View 将唯一 callback 和当前 View State 迁移到新根元素，旧根元素不再接收 command，route cancellation 与进行中的 gameplay command 不被 UI reload 取消

#### Scenario: 同一 mutation 被重复点击

- **WHEN** create-invite、accept、leave、kick、close 或 retry intent 尚未完成时再次提交同类 action
- **THEN** Experience 稳定拒绝重复 intent、保持单一首次调用和 disabled/loading 状态，不生成第二个 idempotency key 或 command

#### Scenario: Route 切换不自取消已提交 command

- **WHEN** world mutation 已取得唯一 intent，且其 `JoiningVisit`、`ReturningOwnWorld` 或其他中间态发布立即隐藏发起该 mutation 的 route
- **THEN** 旧 route binding 和回写资格被取消，但同一 HTTP/TCP command 继续受 App Scope、Session/target generation 与既有 owner lifecycle 约束，route token 不得在发送前或发送后自取消该 command，迟到结果也不得回写旧 view

### Requirement: OwnWorld 提交必须驱动受代际保护的内容 Scene

当 `WorldAdmissionCoordinator` 提交 `OwnWorld` 且 PersonalWorld assignment 完整一致时，Experience MUST 通过登记的 scene transition Host 加载唯一 `PersonalWorldScene`、创建新 Scene Scope generation、注入无 credential Scene View State，并以同 generation 打开 `WorldHud`。BootstrapScene/AppRoot MUST 保持常驻。SceneContext MUST 只持有 camera、lighting、场景对象和表现引用，不得保存 socket、world/visit snapshot 或 target 状态机。

#### Scenario: 首次进入自己的世界

- **WHEN** own-world flow 提交有效 target generation、assignment 和完整 world snapshot
- **THEN** Shell 显示 loading 直至 PersonalWorldScene 与 SceneContext 都提交成功，随后关闭阻断型 screen、打开 scene-bound WorldHud 并启用 gameplay input

#### Scenario: 场景加载晚于新 target

- **WHEN** generation 7 的 PersonalWorldScene 尚未完成加载，target 已切换到 generation 8
- **THEN** generation 7 的 Scene、Context、HUD binding 和 callback 被取消或卸载，不能覆盖 generation 8 或恢复旧输入

#### Scenario: SceneContext 被卸载

- **WHEN** PersonalWorldScene 卸载、目标切换或 App Scope 停止
- **THEN** Scene Scope 先失效并解除订阅，WorldHud hide/unbind，迟到 Unity callback 不能访问已销毁对象，App Scope 业务事实按其 owner 契约保留或停止

### Requirement: 邀请访问流程必须服从权威 role、revision 与 target flow

OwnWorld 中的 Owner MUST 能显式打开或读取 VisitSession、按目标 PlayerID 创建/撤销定向 invite、查看 member，并执行 kick/close；收到 inbox invite 的玩家 MUST 经过明确确认后才调用 `JoinVisitAsync`。UI MUST 从 VisitSession/WorldAdmission View State 推导 Owner/Visitor 权限、revision、expiry 和 loading 状态，不得从按钮、Scene 对象、邀请 payload endpoint 或本地“房主”概念推断权限。

#### Scenario: Owner 创建定向邀请

- **WHEN** current target 为 OwnWorld、VisitSession owner gate 有效且玩家提交合法目标 PlayerID
- **THEN** UI 只调用一次 open/create-invite 语义 action，expected revision 与 invite lifetime 由既有边界提供，并以权威结果刷新邀请和 member 状态

#### Scenario: Visitor 接受邀请

- **WHEN** 玩家在 inbox 中确认未过期 invite 且 accept、admission、JOIN、world snapshot 和 scene transition 全部成功
- **THEN** target 提交为 Visiting，PersonalWorldScene 以新 generation 重建或重绑定，HUD/WorldVisit 显示 Visitor role 且 Owner-only action 不可提交

#### Scenario: Visitor 尝试 Owner action

- **WHEN** Visiting 状态下 UI 或自动化尝试 create invite、kick 或 close VisitSession
- **THEN** action port 在写入 gameplay channel 前返回稳定 permission failure，route、scene、snapshot 和 revision 不变

#### Scenario: 离开后收到同一 Owner 的新邀请

- **WHEN** Visitor 接受 invite A、进入 Visiting、主动离开并返回 OwnWorld，随后同一 VisitSession 对同一玩家发布更高 created revision 的 invite B
- **THEN** inbox 不再包含已接受的 invite A，页面选择只绑定当前 invite B，点击接受提交 B 的 VisitSessionID/InviteID 并在成功后再次呈现 Visitor role；旧文本、旧 selection 或旧 callback 不能提交 invite A

#### Scenario: Owner member snapshot 退役已消费邀请

- **WHEN** Owner 创建定向 invite 后收到更高 revision 的完整 VisitSession snapshot，且目标玩家已出现在 Visitors member 集合
- **THEN** 对应 outgoing invite 必须立即从 Owner 列表与 selection 中退役，撤销按钮同步失效；后续 Visitor 离开不能重新显示该旧 identity

#### Scenario: 当前集合决定动作能力

- **WHEN** WorldVisit 页面反复打开、关闭或快速点击，而 current snapshot 没有 active Owner VisitSession、没有可撤销 invite、没有可移除 member 或没有可接受 inbox invite
- **THEN** 对应 open/create/revoke/kick/close/accept/leave 按钮严格按 current phase、role、lifecycle、集合 selection、连接健康和 single-flight 禁用，View 不产生必然被 policy 拒绝的 command

#### Scenario: 多邀请与多成员显式选择

- **WHEN** current snapshot 同时包含多个有效 inbox/outgoing invite 或多个 Visitor member
- **THEN** 页面以完整 identity 呈现可选项并要求玩家明确选择；集合 replacement 删除所选项时 selection 与相关按钮原子失效，页面不得按旧输入或列表顺序静默操作其他对象

### Requirement: 离开、踢出、关闭与 Owner grace 必须安全返回自己的世界

Visitor 主动 leave、Owner kick/close、Owner unavailable deadline 或合法 safe-return directive MUST 先使旧 Visitor target mutation gate 失效，再将表现状态切换为 `ReturningOwnWorld`、关闭 WorldVisit/WorldHud 的旧 binding、卸载旧 Scene generation，并只按 `WorldAdmissionCoordinator` 的权威 destination 重新解析 own-world。UI MUST NOT 恢复旧 Visitor scene、复用旧 endpoint 或自行选择 fallback world。

#### Scenario: Visitor 主动离开

- **WHEN** current Visitor 提交 leave 且服务端首次结果有效
- **THEN** 客户端停止旧 target command、卸载 Visitor scene、重新解析自己的 assignment，并在新 generation 的 OwnWorld scene 提交后恢复 HUD

#### Scenario: Owner 踢出 Visitor

- **WHEN** Owner 的 kick 结果或 Visitor 收到的 safe-return 指令与 current VisitSession 匹配
- **THEN** Owner 页面按更高 revision 更新 member，Visitor 立即进入 ReturningOwnWorld，旧 scene 和迟到 command 不得复活

#### Scenario: 返回自己的世界失败

- **WHEN** Visitor target 已关闭但 own-world bootstrap、admission、connect 或 Scene load 失败
- **THEN** Shell 保持 Returning/error 状态并只开放显式 retry/logout，旧 Visitor route、scene 和 gameplay mutation 继续关闭

### Requirement: Session、连接与业务失败必须映射为稳定产品状态

Experience MUST 将 protocol incompatibility、unauthenticated/session invalidation、permission、validation、revision conflict、not found、rate limit、dependency unavailable、transport/timeout 和内部失败映射为封闭低敏 UI failure。Session invalidation MUST 取消当前 presentation/recovery/scene intent、关闭产品 routes 和 Scene Scope、删除不可继续使用的 secure lineage 并回到 Login。Session 仍有效的 channel 中断 MUST 从唯一 recovery snapshot 映射为 `RecoveringControl`、`RecoveringWorld` 或稳定 `ConnectionLost`；UI MUST NOT 根据按钮、延时、frame tick 或单个 socket callback伪造恢复成功。

#### Scenario: Session 在 Visiting 时失效

- **WHEN** forced logout 或 session invalidation 使唯一 Session owner 清除当前 lineage
- **THEN** Experience 取消 target/recovery/scene/page intent、关闭旧 HUD/overlay、卸载内容 Scene并回到 Login，任何旧 generation completion 都不能重新认证或打开 world route

#### Scenario: Revision conflict

- **WHEN** Owner 或 Visitor mutation 返回 revision conflict
- **THEN** UI 不重发 mutation，只显示刷新中的只读状态，并允许既有 Service 按契约执行一次权威 snapshot 收敛

#### Scenario: 未知内部错误

- **WHEN** Host、Scene 或 application action 返回未登记异常
- **THEN** 玩家只看到通用可恢复错误和允许的 retry/logout action，Console/Player.log 不包含 password、token、ticket、admission、endpoint 或完整 payload

#### Scenario: Gameplay server 非预期断开

- **WHEN** OwnWorld 或 Visiting 的 TLS/TCP connection 因 transport、protocol 或 backpressure terminal failure 关闭，而 Session 仍有效
- **THEN** channel 事件立即使旧 world mutation 与 Scene/HUD 失效，Experience 显示唯一 RecoveringWorld 状态并由应用层恢复协调者尝试一次有界权威重建；显式 target 切换、safe-return、logout 或 shutdown 的关闭不得误报为 server failure

#### Scenario: 自动恢复耗尽后显式重新连接

- **WHEN** automatic recovery 已稳定失败为 ConnectionLost，服务端恢复后玩家提交一次 reconnect
- **THEN** 客户端复用同一恢复规则等待 control readiness，再以新 generation 重建权威 target、Scene 与 HUD，全部提交后才关闭 modal；不得创建并行 recovery、轮询 channel state、使用固定延时或保留失效 gameplay/Scene

#### Scenario: Control 单独恢复

- **WHEN** WSS 瞬时中断而 current gameplay 与 Session 仍健康
- **THEN** Experience 只呈现非阻断或明确受限的 RecoveringControl 状态，禁用依赖 control 完整性的邀请动作；WSS Connected 且完整 snapshot 收敛后恢复动作能力，不重建健康 Scene/gameplay

#### Scenario: 重连时 access 已到期但 refresh 仍有效

- **WHEN** automatic 或 manual recovery 需要授权 HTTP operation，唯一 Session owner 的 access 已越过绝对 expiry而 refresh lineage 仍有效
- **THEN** 首个授权 HTTP operation先共享一次 single-flight refresh，再以新 session generation 和新 access 继续 control ticket、world admission 与 Scene/HUD 恢复；不得先发送已知过期 access、显示内部错误 key、要求玩家重复登录或循环重试

#### Scenario: 到期刷新无法确认提交结论

- **WHEN** access 到期触发的 refresh 返回 timeout、transport 或 commit-unknown
- **THEN** Session owner 按既有 refresh 契约使旧 token lineage 与 secure record fail closed，停止原始授权 operation并显示低敏恢复失败；不得继续使用旧 access 或猜测 refresh 未提交

#### Scenario: World revision 不变但 assignment 升代

- **WHEN** 服务端恢复后返回相同 PersonalWorld revision、相同持久世界事实，以及更高 assignment generation 和新的 WorldInstance
- **THEN** 客户端接纳新的 runtime assignment 并完成 admission；同 generation 的 instance/endpoint 漂移、generation 回退或 lease deadline 回退仍必须 fail closed

### Requirement: 产品竖切必须完成分层与双客户端验收

实现 MUST 以 EditMode tests 覆盖 entry、View State、单 intent、route/scene/target generation、Owner/Visitor policy、错误映射、session invalidation 和 safe-return；PlayMode tests MUST 覆盖真实 UI Toolkit/uGUI Host、输入焦点、additive Scene、SceneContext、HUD 与 teardown。最终 MUST 使用本地服务端和两个 Windows Development Player 验证完整 Owner/Visitor 流程，并继续通过协议 parity、既有客户端测试和 OpenSpec strict。

#### Scenario: 双客户端访问闭环

- **WHEN** Client A 注册登录并进入 OwnWorld、创建定向 invite，Client B 注册登录、接受并进入 Visiting，随后分别执行主动离开和 Owner kick/close 场景
- **THEN** 两端都只显示与权威 role/target 一致的 route/scene，安全返回后无旧 Visitor callback、command、HUD 或 SceneContext 残留

#### Scenario: PC 显示与输入验收

- **WHEN** Windows Development Player 在登记的 16:9、16:10、21:9、窗口化/全屏、DPI、mouse/keyboard/gamepad 和文本输入组合下执行产品流程，并在 Gameplay mode 通过 `Player/Menu` 的 Tab 或 Gamepad Start 打开访问管理
- **THEN** 页面无关键内容裁切，Gameplay 保持 cursor 锁定隐藏，WorldVisit 打开后切换为 UI action map 且 cursor 解锁可见，`UI/Cancel` 关闭该 overlay 后恢复 Gameplay policy；modal/焦点/loading/disabled/error 可辨识且 Player.log 无未观察异常
