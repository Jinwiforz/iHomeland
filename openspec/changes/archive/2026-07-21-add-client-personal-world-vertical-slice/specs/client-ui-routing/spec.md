## MODIFIED Requirements

### Requirement: 客户端 UI 必须由唯一强类型 router 管理

客户端 MUST 由 App Scope 的唯一 `ClientUiRouter` 管理 screen、overlay、modal 与 system route。每个 route definition MUST 使用封闭 identity，并登记唯一 framework owner、layer、input mode 与 lifecycle；registry MUST 在产生 Unity 副作用前完整校验重复 identity、非法 enum、缺失 Host 与不一致 binding。业务调用方 MUST NOT 传入任意资源路径、Host 类型、sorting order 或框架专属控件。Production registry MUST 只登记已交付且完成直接 Host 引用的产品 route；预留 identity 不得被当作已实现页面。

#### Scenario: 同一 screen 被重复打开

- **WHEN** 当前 generation 已有同 identity 的 active screen，又收到重复 open
- **THEN** router 返回幂等成功并保留唯一 active owner，不创建第二个 view、不重复 bind，也不再次提交页面 command

#### Scenario: Route definition 重复

- **WHEN** Composition 提供两个具有相同 identity 或同一 Host 被绑定到多个 identity 的 definition
- **THEN** UI registry 在 App Scope 初始化阶段失败并由既有生命周期回滚，不选择最后登记者覆盖

#### Scenario: Production registry 登记首批产品页面

- **WHEN** 个人世界竖切完成 Login、Shell、WorldVisit、WorldHud 与 ConnectionLost Host 接线
- **THEN** production registry 只冻结这些 route 的 definition/Host 一对一关系，Settings 等未交付 identity 继续被拒绝，router 不扫描 Scene 或按字符串发现页面

### Requirement: UI router 必须服从 App Scope 生命周期并默认无副作用

`AppComposition` MUST 显式创建 Host/input boundary 与唯一 router，并把它们登记到既有 AppLifetime。Router 初始化 MUST 只验证引用、克隆 Input System 资产和准备 route state，不得访问网络、调用 PersonalWorld/VisitSession command 或创建 SceneContext；产品 Experience MAY 在 router 与全部依赖初始化成功后打开本地初始 route。停止 MUST 先让上层 Experience 拒绝新 intent，再让 router 拒绝新导航、取消当前与排队导航、按独立 cleanup deadline 有界清理全部 active/cached route、解除 subscriber，随后释放 Scene、Host、focus 与 Input System 资源；迟到 callback MUST NOT 复活 UI。若外层 shutdown deadline 在取得 transition gate 前到期，后续 stop MUST 能继续完成清理，不能把未释放资源误标记为已停止。

#### Scenario: App 在 navigation 期间停止

- **WHEN** candidate 正在 initialize、bind 或 show 时 AppLifetime 进入停止
- **THEN** Experience/router 拒绝新请求、取消当前转换、逆序清理 candidate 与已提交 route，并在 deadline 内完成或返回可观察 shutdown failure

#### Scenario: BootstrapScene 启动产品入口

- **WHEN** AppRoot、product Hosts、router 与 Experience 完成完整初始化且尚无玩家输入
- **THEN** Login 成为唯一 active screen，但客户端不执行网络 bootstrap、认证、world/visit mutation、场景加载或未观察异常

### Requirement: 双 UI routing 必须分层验收且不依赖产品页面

Router/registry 基础设施 MUST 继续使用纯 C# EditMode tests 覆盖唯一 owner、幂等、并发容量、transaction rollback、layer/modal、input/focus、三种 lifecycle、generation、subscriber 隔离和逆序停止；通用 Host PlayMode tests MUST 使用程序化 UI Toolkit 与 uGUI Unity 对象覆盖 show/hide、跨框架遮挡、action map、cursor、focus 与销毁，不依赖产品 UXML/Prefab。产品 route/Host/Scene 的额外验收由对应产品 capability 承担。所有测试 MUST NOT 依赖真实账号、公共 listener、Unity Services、远程资源或本地缓存；既有 protocol parity、PersonalWorld Services tests 与 Windows Development build MUST 继续通过。

#### Scenario: 执行双 Host 基础 PlayMode tests

- **WHEN** 通用 fixtures 交替打开 UI Toolkit screen、uGUI overlay 和跨框架 modal，并控制关闭与销毁顺序
- **THEN** 任意时刻每个 identity 只有一个 owner、栈顶层级独占输入、旧 focus 不复活且 teardown 后不残留 EventSystem、panel、Canvas 或 action map 状态

#### Scenario: 构建带产品 routes 的 Windows Development Player

- **WHEN** production registry 已登记首批产品 routes 并构建、启动后正常关闭 Windows Development Player
- **THEN** Player 只打开本地初始 Login route，Player.log 不包含 UI 初始化未观察异常、重复 EventSystem、旧 view 回写、credential 或自动业务网络副作用，runtime/protocol 依赖保持完整
