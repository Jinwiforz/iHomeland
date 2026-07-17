## Context

Q0、C0、C1 与 PersonalWorld Services 已归档。当前客户端拥有唯一 `AppBootstrap -> AppComposition -> AppRoot`、有界主线程 dispatcher、App/Scene generation、HTTP/WSS/TLS-TCP owner，以及不可变 PersonalWorld/VisitSession 投影，但 `Presentation` 目标目录尚未落地，也没有 active screen、modal、输入或焦点 owner。下一条个人世界竖切将同时需要登录、shell、邀请/访问页面和场景型 overlay；若先在页面内直接操作 `UIDocument`、Canvas 或 Input System，两个 UI 框架会形成不同导航与生命周期语义。

现有 `InputSystem_Actions` 已登记 `Player` 与 `UI` action map，项目已锁定 Input System、UI Toolkit 和 uGUI，不需要新增第三方依赖。本 change 只建立表现基础设施，production route catalog 保持为空，避免用占位页面或假业务提前侵入下一条竖切。

## Goals / Non-Goals

**Goals:**

- 建立唯一、纯 C#、可确定性测试的 UI navigation owner。
- 让 UI Toolkit 与 uGUI 服从相同 route definition、层级、输入、焦点和生命周期语义。
- 把 UnityEngine 对象、`VisualElement`、Canvas、EventSystem 和 Input System 操作限制在轻量 Host。
- 对并发导航、Host 异常、应用停止和迟到 callback 提供有界、可回滚、不会复活旧 view 的行为。
- 在没有业务页面时完成 Composition、PlayMode 与 Windows build 验收，并保持默认启动无页面、无业务命令和无网络副作用。

**Non-Goals:**

- 不实现登录、shell、邀请/访问列表、设置、HUD 或世界空间 UI。
- 不接入 PersonalWorld/VisitSession command，不创建 SceneContext，不加载场景或远程资源。
- 不建立通用 event bus、service locator、反射式 view discovery 或任意字符串路由。
- 不创建视觉主题资产、UXML、USS 或 Prefab；主题存储在首个视觉页面 change 中确定，本 change 不预留空 Theme manager。

## Decisions

### 1. 以不可变 route definition 驱动唯一 `ClientUiRouter`

新增封闭的 route identity 与不可变 definition，至少包含 framework owner、layer、input mode 和 lifecycle。`ClientUiRouter` 在构造时复制并校验完整 registry：identity 必须唯一，enum 必须登记，scene-bound route 必须要求 scene generation。业务只能调用强类型 open/close/modal API，不能传任意资源路径、Host 类型或 sorting order。

Production registry 在本 change 中为空；EditMode/PlayMode fixtures 注入代表 UI Toolkit 与 uGUI 的测试 definitions。当前 enum 只预留后续接线所需的稳定 identity，不代表页面已经实现；下一条竖切显式增加 production definition 和 Host 引用。这样既能验收基础设施，也不把测试页面伪装成产品能力。

替代方案是让每个 view 自己导航或建立全局字符串 router。前者会产生多 owner 与 command 双发，后者失去编译期边界并把资源定位泄漏给业务，因此不采用。

### 2. 纯 C# router 与显式 Unity Host registry 分离

`Presentation/Navigation` 保存 route model、只读 snapshot、transition result、`ClientUiRouter` 和窄 Host 契约；`Presentation/Hosts/UIToolkit` 与 `Presentation/Hosts/UGUI` 保存具体 Unity adapter。一个持久 `ClientUiHostRoot` 由 BootstrapScene 直接序列化引用，显式持有 Input System 资产与两类 Host 列表，并在构造对象图前验证重复 identity、空引用和框架错配。

`AppBootstrap` 把该直接引用交给 `AppComposition`；Composition 只取得窄 registry/input 接口并创建唯一 router，不扫描 Scene、不调用 `FindObject*`，也不把 Host root 暴露为全局容器。Host 只获得当前 route binding、页面 cancellation 与焦点接口；内部异常由 router 映射为稳定、低敏且区分提交边界的 transition result，Host 不能取得 HTTP、channel、generated message 或完整 AppCompositionResult。

替代方案是由 router 创建 GameObject/UIDocument/Canvas，或由 Host 通过静态 singleton 自注册。前者混合业务状态与 Unity 生命周期，后者使启动顺序和重复注册不可控，因此不采用。

### 3. 导航转换串行、有界且按事务提交

Router 使用单一 transition gate 串行 open/close，等待队列设硬上限；容量耗尽、停止后调用、未登记 route 和 layer 冲突均返回稳定失败，不内联执行、不静默丢弃。每次转换递增 navigation generation，并按以下顺序执行：

1. 验证 definition、Host、scene generation 与调用参数；
2. 创建页面级 cancellation，完成 candidate 的 initialize/bind；
3. 暂停冲突 owner 的输入，但保留其可恢复显示状态；
4. show candidate，并在需要 UI 输入时尝试设置默认焦点；
5. 原子提交 active owner 与只读 route snapshot；默认焦点不可用不阻断提交，但记录 post-commit failure；
6. 按 lifecycle hide/unbind/dispose 被替换 owner，并在锁外通知 subscriber。

Candidate 在提交前失败时必须在独立 cleanup deadline 内逆序清理 candidate、恢复旧 owner 的输入和焦点，route snapshot 不变；提交后的默认焦点、旧 owner 清理或 subscriber 异常不回滚事实，但必须返回可观察的 post-commit failure。调用方取消只取消等待与未提交 candidate，不得猜测已经提交的页面状态。Router 不自动重试 Host 操作。

### 4. Layer、modal 与交互所有权由 router 统一裁决

层级固定为 Game World < HUD < Screen < Overlay < Modal < System。Screen 层最多一个 active route；Overlay 可以按打开顺序叠加，但只有未被更高层遮挡的 owner 可交互；Modal 使用严格栈，只有栈顶 modal 接收输入；System notice 可以覆盖 modal，但不得把业务 command 交给底层 view。每个 route identity 在任意层最多存在一个 active owner。

Host 只能使用 definition 分配的稳定 layer slot，不能提交任意超大 sorting order。跨框架遮挡与射线由 Host root 按 route snapshot 同步，UI Toolkit view 与 uGUI view 不互相持有控件引用。

### 5. Input System、cursor 与 focus 由单一协调器恢复

`ClientUiHostRoot` 在初始化时克隆序列化的 `InputSystem_Actions`，只操作 clone 中登记的 `Player` 与 `UI` map，停止时禁用并销毁 clone。Router 根据栈顶 route 的 input mode 原子切换 action map、cursor visible/lock 与 gameplay command gate。Modal 打开时底层 UI 和 gameplay action 均不可触发；关闭时只恢复打开 modal 前仍然有效的 mode。

Host 用不透明 focus token 保存 previous focus。UI Toolkit token 只在原 `VisualElement` 仍属于活动 panel、可见且可聚焦时恢复，uGUI token 只在原 GameObject 仍 active、属于当前 Canvas 且对应 `Selectable` 可交互时恢复；否则聚焦当前 route 的显式默认元素。焦点恢复失败返回低敏 post-commit failure，不允许回退到已销毁对象或抢占新 route。

直接依赖共享 InputActionAsset、分别让两种 Host enable/disable action map，或由页面自行设置 cursor 都会产生竞争和跨 PlayMode 污染，因此不采用。

### 6. Lifecycle policy 与 generation 阻止旧 view 复活

- `Cached`：hide 后保留已初始化 Host，但解除页面状态订阅并取消页面任务；再次打开必须重新 bind。
- `Recreate`：close 后执行 hide、unbind、dispose，下一次打开创建全新 generation。
- `SceneBound`：必须绑定当前 Scene Scope generation；generation 失效时先关闭交互再清理，迟到 callback 不能写入新 Scene 或 App Scope UI snapshot。

AppLifetime 初始化 UI Host root 后再初始化 router；停止时利用逆序栈先停止 router、拒绝新导航、取消当前与排队页面任务、清理 active/cached route 并解除 subscriber，再释放 Host/Input 资源，最后继续既有 world/channel/session/http 清理。若外层 shutdown deadline 在取得 transition gate 前到期，router 保持可重试停止状态，后续 stop 继续释放资源。UI 初始化和默认空 registry 不打开页面，也不查询或修改任何业务 Service。

### 7. 验收分为纯路由与实际 Unity Host 两层

EditMode 使用 fake Host/input/focus/clock 验证 registry、层级、并发容量、transaction rollback、lifecycle、generation、subscriber 隔离和逆序停止。PlayMode 以程序化 GameObject、UIDocument/PanelSettings test fixture、Canvas/EventSystem fixture 验证双 Host 的 show/hide、射线层级、action map、cursor、focus 与销毁；测试创建的 Unity 对象必须在 teardown 清理。Windows Development build 继续只启动空 BootstrapScene，并验证无自动页面、业务 command、网络副作用或未观察异常。

## Risks / Trade-offs

- [基础设施先于真实页面，容易出现只在 fake Host 中成立的契约] → PlayMode 同时覆盖实际 UI Toolkit 与 uGUI adapter；下一条竖切必须复用同一 router，不允许旁路。
- [串行 transition 使慢 Host 阻塞后续导航] → 候选阶段受页面 cancellation 约束，提交前回滚与提交后清理使用独立 cleanup deadline，App 停止继续受既有 shutdown deadline 约束；队列有硬上限，超时返回稳定失败。
- [跨框架 focus 与射线行为受 Unity 版本影响] → 锁定现有 Unity/Package 版本，在 PlayMode 和 PC build 中验证；不以编辑器当前选中对象作为状态来源。
- [BootstrapScene 新增 Host root 引用可能导致旧场景启动失败] → 启动前 fail fast 校验，并在同一 change 内完成场景引用和重复 bootstrap/关闭 Domain Reload 回归；回滚时同时移除字段、组件和对象图接线。
- [路由模型过早扩张为万能 UI framework] → 只实现当前文档已冻结的 route、layer、input、focus 和 lifecycle；动画、资源下载、主题、通知队列及业务 Presenter 在出现独立需求时再扩展。

## Migration Plan

1. 先落地纯 C# route model、router、Host/input seams 和 EditMode tests。
2. 增加 UI Toolkit/uGUI adapter 与 `ClientUiHostRoot`，通过程序化 PlayMode fixtures 验证实际 Unity 生命周期。
3. 在 BootstrapScene 为现有 AppRoot 绑定唯一 Host root 与 `InputSystem_Actions`，更新 AppComposition 生命周期顺序；production registry 保持为空。
4. 完成 EditMode、PlayMode、Windows Development build、Console/Player.log 和 OpenSpec strict 验收。
5. 若需回滚，移除 Host root 场景引用与 Presentation 新代码，恢复 AppBootstrap/AppComposition 原构造签名；因没有业务页面或持久数据，不需要数据迁移。

## Open Questions

无。真实页面 identity、主题 profile、UXML/Prefab 与资源定位由 `add-client-personal-world-vertical-slice` 在本路由契约内确定。
