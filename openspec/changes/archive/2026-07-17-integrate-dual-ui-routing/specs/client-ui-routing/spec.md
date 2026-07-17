## ADDED Requirements

### Requirement: 客户端 UI 必须由唯一强类型 router 管理

客户端 MUST 由 App Scope 的唯一 `ClientUiRouter` 管理 screen、overlay、modal 与 system route。每个 route definition MUST 使用封闭 identity，并登记唯一 framework owner、layer、input mode 与 lifecycle；registry MUST 在产生 Unity 副作用前完整校验重复 identity、非法 enum、缺失 Host 与不一致 binding。业务调用方 MUST NOT 传入任意资源路径、Host 类型、sorting order 或框架专属控件。

#### Scenario: 同一 screen 被重复打开

- **WHEN** 当前 generation 已有同 identity 的 active screen，又收到重复 open
- **THEN** router 返回幂等成功并保留唯一 active owner，不创建第二个 view、不重复 bind，也不再次提交页面 command

#### Scenario: Route definition 重复

- **WHEN** Composition 提供两个具有相同 identity 或同一 Host 被绑定到多个 identity 的 definition
- **THEN** UI registry 在 App Scope 初始化阶段失败并由既有生命周期回滚，不选择最后登记者覆盖

#### Scenario: Production registry 尚无业务页面

- **WHEN** 当前 change 的 BootstrapScene 启动且 production registry 为空
- **THEN** router 正常进入可用空状态，不创建占位页面、不扫描场景，也不自动打开登录、shell 或 world route

### Requirement: UI Toolkit 与 uGUI 必须服从同一 Host 契约

UI Toolkit 与 uGUI adapter MUST 实现相同的 initialize、bind、show、hide、unbind 与 dispose 契约，并由 BootstrapScene 的唯一 `ClientUiHostRoot` 通过直接序列化引用显式登记。Host MUST 只访问当前 route binding、页面 cancellation、输入/焦点接口与所需 Unity 对象；UI Toolkit view 与 uGUI view MUST NOT 互相持有控件引用，也 MUST NOT 访问 HTTP、WSS、TLS/TCP、generated message、credential 或完整 Composition 容器。

#### Scenario: Framework owner 与 Host 类型不匹配

- **WHEN** UI Toolkit definition 绑定 uGUI Host，或 uGUI definition 绑定 UI Toolkit Host
- **THEN** registry 拒绝整个配置且不初始化候选 view、EventSystem 或 action map

#### Scenario: Host 尝试保存业务事实

- **WHEN** view 需要显示 account、PersonalWorld 或 VisitSession 状态
- **THEN** Host 只绑定 Service 提供的不可变、无 credential projection 或派生 View State，不复制 generated payload 或成为第二份最终事实 owner

### Requirement: 导航转换必须串行、有界且事务化提交

Router MUST 通过单一有界 transition gate 串行执行 open、close、replace 与 modal 操作；容量耗尽、停止后调用和未登记 route MUST 返回稳定失败，不得内联执行、无界排队或静默丢弃。每次转换 MUST 使用单调 navigation generation 和页面级 cancellation；candidate 只有在 validation、initialize、bind、input suspension 与 show 全部成功后才能原子提交新的只读 route snapshot。重复 open/close 等无状态变化操作 MUST 返回未提交的幂等成功；提交后的默认 focus、被替换 route 清理或 snapshot subscriber 失败 MUST 返回可观察的 post-commit failure，且不得回滚已经提交的事实。

#### Scenario: Candidate show 失败

- **WHEN** 新 screen 已完成 initialize/bind，但 Host 在 show 阶段失败
- **THEN** router 逆序 unbind/dispose candidate、恢复原 screen 的输入与有效 focus，active snapshot 保持不变且不自动重试

#### Scenario: 已显示 route 缺少有效默认 focus

- **WHEN** UI 输入 route 已完成 show 并原子提交，但 Host 无法聚焦仍可见、可用且属于当前 generation 的默认元素
- **THEN** router 保留已提交 route 并返回可观察的 post-commit failure，不把该结果伪装为未提交失败，也不聚焦其他 route

#### Scenario: Snapshot subscriber 抛出异常

- **WHEN** route snapshot 已提交且一个 subscriber 在锁外通知阶段抛出异常
- **THEN** router 继续隔离通知其他 subscriber、保留已提交 snapshot 并返回可观察的 post-commit failure，不让 subscriber 异常破坏路由锁或回滚事实

#### Scenario: 并发导航超过容量

- **WHEN** 一个转换仍在等待且排队请求达到登记硬上限
- **THEN** 新请求立即获得稳定 overload 结果，不增长无界集合、不取消当前转换，也不绕过顺序直接操作 Host

#### Scenario: 调用方在提交边界取消

- **WHEN** caller cancellation 与 candidate 提交并发发生
- **THEN** router 以单一原子决议返回已提交或已取消；已取消 candidate 不能稍后显示，已提交 route 不能因旧 caller 的迟到取消而回滚

### Requirement: Layer 与交互所有权必须跨框架统一裁决

Router MUST 固定 Game World、HUD、Screen、Overlay、Modal、System 的相对层级，并只允许 definition 使用登记 slot。Screen 层 MUST 最多存在一个 active route；Modal MUST 使用严格栈且只有栈顶可交互；被更高层遮挡的 overlay、screen 与 gameplay command MUST 被统一阻断。Host MUST NOT 通过任意 sorting order、panel priority 或 raycast 配置越过 route snapshot 的层级裁决。

#### Scenario: uGUI modal 覆盖 UI Toolkit screen

- **WHEN** active UI Toolkit screen 上打开 uGUI modal
- **THEN** modal 位于 screen 之上并独占提交、取消、指针与 gameplay command，底层 screen 保持可恢复显示但不能交互

#### Scenario: 关闭栈顶 modal

- **WHEN** modal 栈包含两个 identity 且关闭栈顶项
- **THEN** 只有下一层仍有效 modal 恢复交互，screen 与 gameplay 继续被阻断，重复 close 保持幂等

### Requirement: Input、cursor 与 focus 必须由单一协调器管理

`ClientUiHostRoot` MUST 克隆并只操作序列化 `InputSystem_Actions` 中登记的 `Player` 与 `UI` action map；共享资产本身 MUST NOT 被运行时 enable/disable。Router MUST 依据当前最高交互 route 的 input mode 原子切换 action map、gameplay gate、cursor visible/lock 与默认 focus；Gameplay mode MUST 保持无 UI route owner，UI、Text 与 Modal mode MUST 绑定当前交互 route。Previous focus MUST 以不透明 token 保存，且只在目标对象仍属于当前有效 Host generation、可见并可选择时恢复。

#### Scenario: 打开 text/modal route

- **WHEN** gameplay mode 下打开要求文本或 modal 输入的 route
- **THEN** Player action map 与 gameplay command gate 在 view 可交互前关闭，UI action map、cursor 和默认 focus 按 definition 启用

#### Scenario: Previous focus 已被销毁

- **WHEN** modal 关闭时原 focus 对象已离开 panel、被销毁或属于旧 Host generation
- **THEN** coordinator 不访问旧对象，改为聚焦当前 route 的有效默认元素；若无默认元素则返回低敏 post-commit failure 而不抢占其他 route

#### Scenario: App Scope 停止后重新进入 PlayMode

- **WHEN** 上一次运行停止并销毁 Input System clone 后，在关闭 Domain Reload 的配置下开始新运行
- **THEN** 新 Host root 只启用一份 action map 状态，不沿用旧 cursor、focus token 或静态 UI owner

### Requirement: Route lifecycle 必须阻止隐藏或旧代际 view 回写

`Cached` route MUST 在 hide 时取消页面任务并解除状态订阅，再次 show 前重新 bind；`Recreate` route MUST 在 close 后完成 hide、unbind 与 dispose；`SceneBound` route MUST 绑定 current Scene Scope generation，并在 generation 失效时先关闭交互再清理。任何迟到 callback MUST 同时匹配 App running、navigation generation、Host generation、scene generation（如适用）与未取消页面 token，才能写入当前 view。

#### Scenario: Cached view 隐藏后响应到达

- **WHEN** cached screen 已 hide/unbind，但它先前发起的异步结果随后完成
- **THEN** 结果可以由所属业务 Service 按自身契约收敛，但不能修改隐藏 view、重新 show、恢复 focus 或提交页面 command

#### Scenario: Scene-bound view 的旧 callback 到达

- **WHEN** Scene A 的 scene-bound overlay 已因 generation 失效而关闭，Scene B 激活后旧 callback 才到达
- **THEN** callback 被 generation/cancellation gate 丢弃，不能绑定 Scene B 对象或改变 App Scope route snapshot

### Requirement: UI router 必须服从 App Scope 生命周期并默认无副作用

`AppComposition` MUST 显式创建 Host/input boundary 与唯一 router，并把它们登记到既有 AppLifetime。初始化 MUST 只验证引用、克隆 Input System 资产和准备空 route state，不得打开页面、访问网络、调用 PersonalWorld/VisitSession command 或创建 SceneContext。停止 MUST 先让 router 拒绝新导航、取消当前与排队导航、按独立 cleanup deadline 有界清理全部 active/cached route、解除 subscriber，再释放 Host、focus 与 Input System 资源；迟到 callback MUST NOT 复活 UI。若外层 shutdown deadline 在取得 transition gate 前到期，后续 stop MUST 能继续完成清理，不能把未释放资源误标记为已停止。

#### Scenario: App 在 navigation 期间停止

- **WHEN** candidate 正在 initialize、bind 或 show 时 AppLifetime 进入停止
- **THEN** router 拒绝新请求、取消当前转换、逆序清理 candidate 与已提交 route，并在 deadline 内完成或返回可观察 shutdown failure

#### Scenario: 空 BootstrapScene 启动

- **WHEN** AppRoot 完成完整 Composition 初始化但没有显式 navigation 请求
- **THEN** active route snapshot 为空，Player 不产生 UI 业务网络请求、world/visit mutation、场景加载或未观察异常

### Requirement: 双 UI routing 必须分层验收且不依赖产品页面

实现 MUST 使用纯 C# EditMode tests 覆盖 registry、唯一 owner、幂等、并发容量、transaction rollback、layer/modal、input/focus、三种 lifecycle、generation、subscriber 隔离和逆序停止；PlayMode tests MUST 使用实际 UI Toolkit 与 uGUI Unity 对象覆盖 Host show/hide、跨框架遮挡、action map、cursor、focus 与销毁。测试 MUST NOT 依赖真实账号、服务端 listener、Unity Services、产品 UXML/Prefab、远程资源或本地缓存；既有 protocol parity、PersonalWorld Services tests 与 Windows Development build MUST 继续通过。

#### Scenario: 执行双 Host PlayMode tests

- **WHEN** tests 交替打开 UI Toolkit screen、uGUI overlay 和跨框架 modal，并控制关闭与销毁顺序
- **THEN** 任意时刻每个 identity 只有一个 owner、栈顶层级独占输入、旧 focus 不复活且 teardown 后不残留 EventSystem、panel、Canvas 或 action map 状态

#### Scenario: 构建 Windows Development Player

- **WHEN** production route registry 为空并构建、启动后正常关闭 Windows Development Player
- **THEN** Player.log 不包含 UI 初始化未观察异常、重复 EventSystem、旧 view 回写、credential 或自动业务网络副作用，runtime/protocol 依赖保持完整
