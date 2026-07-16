## ADDED Requirements

### Requirement: Unity 工程基线必须可重复恢复
客户端工程 MUST 使用 `versions.yaml` 锁定的 Unity Editor 版本，提交 Package manifest/lock、必要 ProjectSettings、Unity 源资产及其 Editor 生成的 `.meta`，并保持 Library、Temp、UserSettings、构建输出与 generated C# 不进入 Git。BootstrapScene MUST 是 PC 构建中唯一且首个启用的启动场景；Windows Player 的 Company Name 与 Product Name MUST 使用项目身份，不得保留 Unity 模板值。C0 没有 Unity Services 消费者时 MUST 保持 Unity Cloud Project 未绑定且 Unity Connect 总开关关闭。C0 MUST NOT 把 ProjectSettings 保存的 Standalone Application Identifier 当作 Windows UI 契约或质量门；平台专属标识的发布语义由实际启用对应目标平台的 change 管理。

#### Scenario: 从干净检出恢复客户端工程
- **WHEN** 在没有 Unity 本地缓存的新环境中使用锁定 Editor 打开仓库
- **THEN** Package 能按 lock 恢复，BootstrapScene 引用不丢失且项目无需依赖未跟踪资产即可进入编译和测试

#### Scenario: Unity 版本、Windows 项目身份或启动场景漂移
- **WHEN** `ProjectVersion.txt` 与 `versions.yaml` 不一致，Windows Company Name/Product Name 不是登记的项目身份，或 BootstrapScene 不是构建列表中唯一且首个启用场景
- **THEN** 客户端基线验证失败并报告具体漂移项；修正前的后续测试或构建不能作为 C0 验收证据

#### Scenario: C0 意外绑定 Unity Services
- **WHEN** ProjectSettings 保存了 Unity Cloud Project ID 或启用了 Unity Connect 总开关
- **THEN** 客户端基线验证失败，避免构建和 Player 隐式依赖未登记的云端项目或服务

### Requirement: 应用根必须全程唯一
AppBootstrap MUST 只通过唯一 AppComposition 创建 App Scope；AppRoot MUST 跨场景持有该作用域，但不得向 feature 暴露全局 service locator。重复加载 BootstrapScene、重复执行 bootstrap 或关闭 Domain Reload 后重新进入 PlayMode MUST NOT 创建第二个对象图、第二组 tick 或第二个持久 Host。

#### Scenario: 重复加载 BootstrapScene
- **WHEN** 已存在运行中的 AppRoot 时再次加载或实例化 BootstrapScene
- **THEN** 重复入口被安全拒绝或销毁，原 AppRoot 与对象图保持唯一且不重复初始化

#### Scenario: 关闭 Domain Reload 后重新进入 PlayMode
- **WHEN** Editor 使用关闭 Domain Reload 的 PlayMode 设置连续运行两次
- **THEN** 上一次运行的静态唯一性状态不会阻止新运行建立一套且仅一套 AppRoot

### Requirement: 应用生命周期必须事务化清理
App Scope 生命周期 owner MUST 串行初始化参与者，只把初始化成功项加入清理栈；初始化失败时 MUST 在独立 deadline 内逆序回滚全部成功项。正常停止 MUST 幂等、逆序尝试全部清理项并聚合错误，不得因单项失败、重复停止或 Unity 退出 callback 无限阻塞。

#### Scenario: 中途初始化失败
- **WHEN** 第 N 个生命周期参与者初始化失败
- **THEN** 只有前 N-1 个成功参与者按逆序各停止一次，失败参与者与未开始参与者不进入清理栈，调用方收到原始启动错误及任何回滚错误

#### Scenario: 多次请求停止
- **WHEN** AppRoot 在运行中收到并发或连续的停止请求
- **THEN** 所有调用共享一次逆序停止结果，每个已启动参与者最多停止一次且最终状态不可重新进入 Running

#### Scenario: 初始化边界收到停止请求
- **WHEN** App Scope 尚未启动或仍在初始化时收到停止请求
- **THEN** 尚未启动的对象图原子进入 Stopped；正在初始化的对象图完成当前启动决议后不再发布可执行工作的 Running，并只清理已成功参与者一次；停止任务完成后对象图保持终态

#### Scenario: 单个清理项失败或超时
- **WHEN** 一个参与者停止时报错或超过关闭 deadline
- **THEN** owner 继续尝试其他可执行清理项，并返回包含失败或超时原因的可观察结果而不无限等待

### Requirement: 逐帧与主线程工作必须显式且有界
AppRoot Update MUST 只驱动 Composition 明确登记的 tickable 与有界 MainThreadDispatcher。Dispatcher MUST 只在捕获的 Unity 主线程排空工作、在容量耗尽或停止后拒绝新工作，并隔离单个 callback 错误；未登记对象不得凭借全局扫描或事件总线获得逐帧执行。

#### Scenario: 后台线程投递主线程工作
- **WHEN** 后台线程在容量范围内向运行中的 Dispatcher 投递 callback
- **THEN** callback 只在后续 Unity 主线程 Update 中执行一次，且不会在投递线程访问 UnityEngine.Object

#### Scenario: 队列达到容量或应用已经停止
- **WHEN** Dispatcher 队列已满或 App Scope 已进入停止状态
- **THEN** 新 callback 被显式拒绝并产生可观察结果，不得无界增长、静默丢失或在错误线程内联执行

#### Scenario: 未登记逐帧对象
- **WHEN** 一个对象未被 AppComposition 加入冻结后的 tickable 列表
- **THEN** AppRoot 不会通过反射、场景扫描或全局事件自动调用该对象

### Requirement: Scene Scope 必须以代际与取消隔离
App Scope MUST 为活动 Scene Scope 分配单调递增 generation 与专属 cancellation token。新 Scene Scope 激活、旧 SceneContext 卸载或 App Scope 停止时 MUST 先使旧 token 取消并使 generation 失效；迟到 callback 只有在 generation 当前且 token 未取消时才能写入场景对象。

#### Scenario: 场景替换后旧回调到达
- **WHEN** Scene A 卸载并激活 Scene B 后，Scene A 发起的异步 callback 才完成
- **THEN** callback 因旧 generation 或已取消 token 被丢弃，不能修改 Scene B、已销毁 View 或 App Scope 业务状态

#### Scenario: 重复释放 Scene Scope
- **WHEN** SceneContext 卸载与 App Scope 停止先后释放同一 Scene Scope
- **THEN** 取消与清理保持幂等，不重复回写、不抛出资源已释放导致的未处理异常

### Requirement: C0 资源与功能边界必须保持最小
C0 MUST 仅使用构建内场景、Inspector 直接引用和当前必要 Package，不得引入 Addressables、通用 ResourceManager、`Resources` 核心内容目录、第三方 DI 或异步库。网络协议、HTTP/WSS/TCP、业务 Service、正式 screen、地图与内容 Prefab MUST 留给满足各自进入条件的后续 change。

#### Scenario: BootstrapScene 需要固定启动引用
- **WHEN** AppBootstrap 需要引用 AppRoot 或其他 C0 Host
- **THEN** 引用通过 BootstrapScene 的直接序列化接线完成，不创建字符串资源地址、远程 Catalog 或万能资源加载接口

#### Scenario: 实现中发现后续业务需求
- **WHEN** C0 开发过程中需要账号、连接、个人世界、正式 UI 或远程资源能力才能继续
- **THEN** 当前 change 停止扩张并把该能力留给路线图规定的独立 change，不以占位 manager 或假实现绕过边界

### Requirement: C0 必须具备分层自动化验收
客户端 MUST 提供不依赖 GameObject 的 EditMode tests 和覆盖实际 Unity Host 生命周期的 PlayMode tests，并完成无 Console 编译错误的 Windows Development build smoke。测试 MUST NOT 依赖真实服务端、listener、网络、正式 UI 或本地缓存。

#### Scenario: 验证纯 C# 生命周期规则
- **WHEN** 运行 C0 EditMode tests
- **THEN** 初始化顺序、失败回滚、逆序停止、幂等、deadline、Dispatcher 容量/线程与 Scene generation 均有确定性断言

#### Scenario: 验证 Unity Host 生命周期
- **WHEN** 运行 C0 PlayMode tests
- **THEN** 唯一 AppRoot、重复 bootstrap、跨场景持有、Update 驱动、销毁与退出清理均通过实际 Unity 生命周期验证

#### Scenario: 构建 Windows Development Player
- **WHEN** 在锁定 Unity Editor 和干净客户端工程状态下执行 Windows Development build smoke
- **THEN** BootstrapScene 成为 Player 入口，构建成功且启动过程中没有缺失脚本、缺失序列化引用或重复 AppRoot
