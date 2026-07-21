# Client Runtime 规格

## Purpose

定义 Unity 客户端进入条件、Composition Root、App/Scene Scope、Service/Host 和双 UI 行为。

## Requirements

### Requirement: 客户端必须等待服务端 v1 冻结
Unity 客户端 MUST 只在服务端通过资格验收并冻结 schema、route registry、endpoint manifest 和 contract fixtures 后开始运行时实现。

#### Scenario: 服务端契约未冻结
- **WHEN** 服务端 v1 仍允许修改基础 session、错误或消息路由语义
- **THEN** 客户端不得实现依赖这些未冻结行为的运行时代码

### Requirement: 客户端必须使用 Composition Root
AppBootstrap MUST 调用唯一 AppComposition 创建 AppRoot、纯 C# Services、必要 Unity Hosts 和显式依赖，并管理失败回滚与逆序关闭。

#### Scenario: 重复执行 bootstrap
- **WHEN** 启动场景重复加载或应用根已经存在
- **THEN** 客户端不得创建第二套 session、network channels、UI hosts 或业务 services

### Requirement: App Scope 与 Scene Scope 必须分离
App Scope MUST 持有账号、网络、PersonalWorld、WorldInstance/VisitSession 投影和应用流程，Scene Scope MUST 持有 camera、lighting、地图、角色和场景型 UI；场景对象不得成为应用事实 owner。

#### Scenario: 卸载 gameplay scene
- **WHEN** SceneContext 随场景卸载
- **THEN** App Scope 保持账号、连接、world identity 与 visit lifecycle，并释放所有已卸载场景引用、订阅和迟到 callback

### Requirement: 业务逻辑必须优先使用纯 C# Service
不依赖 Unity 生命周期函数、GameObject 或 Inspector 引用的账号、个人世界、访客会话、网络状态与规则 MUST 使用可独立测试的普通 C# 类型。

#### Scenario: 测试 PersonalWorld Service
- **WHEN** 测试 own-world admission、world revision、Visitor role 和 safe-return 投影
- **THEN** 测试无需创建 GameObject、Scene 或具体 UI view

### Requirement: Unity Host 必须保持轻量
MonoBehaviour MUST 只承担引擎回调、Coroutine、主线程投递、GameObject 生命周期、Audio、UI 和 SceneContext adapter，不得实现业务状态机。

#### Scenario: 网络线程收到 world push
- **WHEN** 后台 receive pump 收到更高 revision 的 world 或 visit snapshot
- **THEN** 它通过主线程 Host 更新对应纯 C# Service，活动 view 与 Scene adapter 再读取只读状态

### Requirement: 双 UI 必须按页面适配度选择
客户端 MUST 以完整逻辑页面为单位选择 UI Toolkit 或 uGUI，不得为展示技术栈而强制混搭；同一 screen 只能有一个 active owner。

#### Scenario: 实现邀请与访问列表
- **WHEN** 页面包含好友邀请、访问状态、筛选和可复用样式
- **THEN** 客户端优先评估 UI Toolkit，并通过统一 UI 入口管理生命周期和输入

#### Scenario: 实现世界空间血条
- **WHEN** UI 需要跟随场景角色和 camera
- **THEN** 客户端优先使用 uGUI 或明确适合世界空间的实现

### Requirement: Unity 资产不得保存在线业务事实
Scene、Prefab 和 ScriptableObject MUST 只保存内容、表现、配置和共享定义，不得作为 session、socket、PersonalWorld、WorldInstance、VisitSession 或玩家资产的最终 owner。

#### Scenario: 收到最新世界快照
- **WHEN** 服务端推送更高 revision 的 PersonalWorld 或 VisitSession snapshot
- **THEN** App Scope Service 更新唯一运行时事实，UI 与场景只从该事实派生展示

### Requirement: Unity 工程基线必须可重复恢复
客户端工程 MUST 使用 `versions.yaml` 锁定的 Unity Editor 版本，提交 Package manifest/lock、必要 ProjectSettings、Unity 源资产及其 Editor 生成的 `.meta`，并保持 Library、Temp、UserSettings、构建输出与 generated C# 不进入 Git。BootstrapScene MUST 是 PC 构建中首个且唯一的应用入口；已批准的内容 Scene MAY 作为后续 enabled scene 加入 build，但 MUST NOT 包含第二个 AppRoot、session、channel 或持久 UI root。Windows Player 的 Company Name 与 Product Name MUST 使用项目身份，不得保留 Unity 模板值。没有 Unity Services 消费者时 MUST 保持 Unity Cloud Project 未绑定且 Unity Connect 总开关关闭。客户端 MUST NOT 把 ProjectSettings 保存的 Standalone Application Identifier 当作 Windows UI 契约或质量门；平台专属标识的发布语义由实际启用对应目标平台的 change 管理。

#### Scenario: 从干净检出恢复客户端工程
- **WHEN** 在没有 Unity 本地缓存的新环境中使用锁定 Editor 打开仓库
- **THEN** Package 能按 lock 恢复，BootstrapScene、PersonalWorldScene、产品 UXML/USS/Prefab 直接引用不丢失，项目无需依赖未跟踪资产即可进入编译和测试

#### Scenario: Unity 版本、Windows 项目身份或构建场景漂移
- **WHEN** `ProjectVersion.txt` 与 `versions.yaml` 不一致，Windows Company Name/Product Name 不是登记的项目身份，BootstrapScene 不是 build index 0，或登记内容 Scene 缺失/重复应用根
- **THEN** 客户端基线验证失败并报告具体漂移项；修正前的后续测试或构建不能作为验收证据

#### Scenario: 客户端意外绑定 Unity Services
- **WHEN** ProjectSettings 保存了 Unity Cloud Project ID 或启用了 Unity Connect 总开关
- **THEN** 客户端基线验证失败，避免构建和 Player 隐式依赖未登记的云端项目或服务

### Requirement: 产品 Scene 必须通过唯一场景转换边界管理

App Scope MUST 由唯一 scene transition Host 按登记的 build scene identity 加载和卸载产品 Scene，并由既有 `SceneLifetimeOwner` 为每次提交分配 generation/cancellation。Scene transition Host MUST 只负责 Unity SceneManager 操作、当前 Scene handle 与 Context 发现/注入，不得决定 world target、保存业务 snapshot 或接受任意资源路径。加载后的 Scene MUST 恰好包含一个对应 `SceneContext`，且 Context MUST 在 generation current 时才能写入 Unity 对象。

#### Scenario: 加载 PersonalWorldScene

- **WHEN** application flow 请求为 current target generation 加载登记的 PersonalWorldScene
- **THEN** Host additive 加载该 build scene、验证唯一 Context、注入新 SceneLifetime，并只在全部步骤成功后发布可显示的 Scene Scope

#### Scenario: Scene 缺少或重复 Context

- **WHEN** 加载的 PersonalWorldScene 没有 Context 或包含两个同类 Context
- **THEN** scene transition 失败、使候选 generation 失效并卸载候选 Scene，不选择任意一个 Context 继续运行

#### Scenario: 目标切换或 App 停止

- **WHEN** current target generation 改变、SceneContext 卸载或 AppLifetime 停止
- **THEN** Host 先取消旧 SceneLifetime 和 scene-bound UI，再卸载旧 Scene，并拒绝旧 load/unload callback 覆盖新的 current Scene handle

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
