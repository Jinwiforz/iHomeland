## Context

`qualify-server-v1` 已于 2026-07-16 归档，Unity C0 可以正式开始。仓库中已有使用 Unity 6000.5.2f1 创建的 URP 工程、`BootstrapScene`、Input System、Package lock 与 ProjectSettings，但它们尚未形成应用对象图或生命周期；Windows Company Name/Product Name 尚未纳入自动化基线，工程资产也尚未提交。

本 change 是后续 C# 协议生成、HTTP/WSS/TCP、账号与个人世界客户端的共同基础。它必须证明启动、失败回滚、场景替换和关闭行为，而不能提前实现任何后续业务。Unity 序列化资产与 `.meta` 由 Unity Editor 维护；实现期间，代码和可读配置可以在仓库中编辑，场景、Inspector、Build Profiles 与 Unity 导入结果通过 Editor 完成并复核。

## Goals / Non-Goals

**Goals:**

- 建立唯一且可重复加载的 `AppBootstrap -> AppComposition -> AppRoot` 应用入口。
- 以纯 C# 实现顺序初始化、失败回滚、逆序停止、显式 tick 与场景代际失效，使关键规则可脱离 GameObject 测试。
- 让 Unity Host 只桥接 Unity callbacks、主线程、跨场景 GameObject 生命周期和退出信号。
- 固定可由干净检出恢复的 Unity PC 工程、Package、BootstrapScene 和测试基线。
- 为后续客户端 changes 提供窄而真实的生命周期扩展点，不预建业务目录或万能容器。

**Non-Goals:**

- 不生成 C# 协议，不接入 HTTP、WSS、TCP、token、账号、PersonalWorld、WorldInstance 或 VisitSession。
- 不实现正式 UI、输入玩法、地图、Actor、资源下载或客户端内容生产流程。
- 不引入 Addressables、`Resources` 核心加载、AssetBundle 封装、UniTask、第三方 DI 或 service locator。
- 不建立没有实际消费者的 Application、Infrastructure、Presentation manager/interface，也不改变服务端冻结契约。

## Decisions

### 1. 现有 Unity 工程作为基线，而不是重新生成工程

保留已创建的 Unity 6000.5.2f1 URP 工程，`versions.yaml` 是 Editor 版本 owner，`ProjectVersion.txt` 必须与之相符。提交 `Packages/manifest.json`、`Packages/packages-lock.json`、必要 `ProjectSettings`、Unity 源资产及其由 Editor 生成的 `.meta`；继续忽略 Library、Temp、Logs、UserSettings、构建输出和 generated C#。

PC Standalone 是本阶段目标平台。`BootstrapScene` 是唯一启用的首个构建场景，Windows Company Name/Product Name 固定为项目身份且不保留模板名称。Application Identifier 不作为 Windows C0 的 UI 接线或质量门；ProjectSettings 可以保存平台键，但其具体值与发布语义由 Android 或 Apple 等实际启用对应目标平台的 change 管理。C0 没有 Unity Services 消费者，因此清空 Unity Cloud Project 绑定并关闭 Unity Connect 总开关，避免构建弹窗和 Player 隐式网络请求；后续能力若确需 Unity Services，必须由对应 change 明确所有权、配置与验收。C0 资产使用 Inspector 直接引用；没有远程内容、分包或按需卸载证据，因此不安装 Addressables，也不创建通用资源管理器。

Package 最小化以功能依赖为边界：锁定 Unity 模板已经声明的 built-in engine modules 和当前 IDE integrations，不把它们解释成已实现的业务系统；C0 自动化禁止 Addressables、第三方 DI/async 等新增 feature package。只有独立 change 能在目标平台完整导入、测试和构建后，才批量裁剪 template module，避免仅为缩短 manifest 破坏 Package lock 或序列化资产兼容性。

备选方案是重新创建空工程或立即引入 Addressables。前者会丢失已核对的版本和 URP/Input System 基线，后者会在没有内容分发需求时引入 Catalog、Group、Bundle 与发布生命周期，均不采用。

### 2. 只创建承载真实实现的最小程序集和目录

`Assets/App/Scripts/` 下建立一个 C0 runtime assembly，承载 `Core/Bootstrap`、`Core/Composition`、`Core/Lifetime` 与实际使用的 Scene lifetime primitive；EditMode 和 PlayMode tests 使用独立 test assemblies。后续 Application、Infrastructure、Presentation 目录和程序集只在对应 change 出现真实实现时创建。

该选择保持首个变更可读，避免为了目标目录树建立空类、空 manager 和只转发调用的万能接口。未来出现独立依赖方向或编译边界时，可以由对应 change 拆分程序集，而无需改变生命周期契约。

### 3. AppComposition 显式构造对象图

`AppBootstrap` 是 BootstrapScene 中的 MonoBehaviour 入口；它只获取必要 Inspector 引用并调用 `AppComposition`。`AppComposition` 使用构造参数和具体类型创建 App Scope 对象，返回一个不可变的 composition 结果给 `AppRoot`，不暴露按类型查询服务的容器。

`AppRoot` 是 `DontDestroyOnLoad` 的轻量 Unity Host，负责驱动生命周期、主线程工作和显式 tick。它不包含业务状态，不提供全局 `Instance` 给 feature 取服务。进程内静态状态只允许用于争用唯一 root，并在 Unity SubsystemRegistration 时重置，兼容关闭 Domain Reload 后的 PlayMode。

备选方案是第三方 DI 或静态 singleton。当前对象数量不支持额外依赖和隐式查找成本，因此不采用。

### 4. 初始化与停止由一个纯 C# 生命周期 owner 管理

生命周期状态固定为：

```text
Created -------------------------------> Stopped
   |
   v
Initializing -> Running -> Stopping ---> Stopped
   |
   v
 Failed
```

参与者按注册顺序串行初始化；只有初始化成功的参与者进入清理栈。任一步失败时，owner 在独立 rollback deadline 内逆序停止全部已成功参与者，保留原始启动错误并聚合清理错误。正常停止同样逆序尝试所有参与者，不因某个停止错误跳过剩余清理。

启动、停止和重复请求均由 owner 串行化：不会并行执行两次初始化；尚未启动即收到停止时直接进入 `Stopped`；初始化期间收到停止时完成当前启动决议但不再发布可执行工作的 `Running`；多个停止调用共享同一停止结果；处于 `Stopping`、`Stopped` 或 `Failed` 后不能重新启动旧对象图。deadline 到期必须结束等待并报告未完成项，不能在 Unity callback 中无限同步阻塞。

不使用 `Start`/`Awake` 顺序隐式表达依赖，也不让各参与者自行发现全局服务。

### 5. Update 只驱动两个明确入口

AppRoot 的 `Update` 只负责：

1. 在捕获的 Unity 主线程上有界排空 `MainThreadDispatcher`；
2. 按注册快照调用显式 `IAppTickable`。

Dispatcher 使用有界队列；停止后拒绝新工作；单个 callback 异常被记录但不阻止同一批次剩余 callback。非主线程不能执行 drain，tick 列表在 Composition 完成后冻结，避免 feature 任意注册逐帧逻辑。C0 只实现该运行边界，不接入网络 receive pump。

备选方案是每个 Service 使用 MonoBehaviour.Update 或全局 event bus。它们会扩散 Unity 生命周期依赖和隐式所有权，因此不采用。

### 6. Scene lifetime 使用代际与取消组合

App Scope 持有单一 Scene lifetime owner。每次激活新的 Scene Scope 都递增 generation，并取消、释放上一代 token；SceneContext、异步操作和回调捕获 generation 与 cancellation token，只有二者仍有效时才能写入场景对象。重复释放幂等，App Scope 停止时先使当前 Scene Scope 失效。

C0 只提供这个轻量机制和测试用 SceneContext Host，不创建 Shell、Gameplay、Tool 等空上下文，也不实现异步资源加载。后续场景 change 必须把真实订阅、加载和 view 引用挂到该 lifetime 上。

仅依赖 cancellation token 不能识别错误复用或忽略取消的迟到结果；仅使用 generation 又不能主动终止工作，因此两者组合。

### 7. 自动化分层验证纯逻辑与 Unity 行为

EditMode tests 直接实例化 lifecycle、dispatcher 和 scene lifetime，覆盖顺序、失败、聚合错误、幂等、容量、线程与 generation。PlayMode tests 加载或实例化最小 BootstrapScene，覆盖唯一 AppRoot、重复 bootstrap、`DontDestroyOnLoad`、Update 驱动与销毁清理。Editor 验收分别运行 `IHomeland.Client.Runtime.EditModeTests` 与 `IHomeland.Client.Runtime.PlayModeTests` 两个项目程序集，以明确 C0 结果边界。

最终还需在 Unity Editor 中确认无 Console error、BootstrapScene 组件引用完整，并执行 Windows Development build smoke。测试代码不得依赖真实网络、服务端、监听端口或正式 UI。

## Risks / Trade-offs

- [Unity 的 `OnApplicationQuit` 不能可靠等待任意长异步清理] → 提供可等待且有 deadline 的显式停止入口；退出 callback 只触发已有停止流程并记录结果，后续业务退出流程必须先停止再调用 `Application.Quit`。
- [关闭 Domain Reload 时静态唯一性状态可能污染下一次 PlayMode] → 使用 SubsystemRegistration 重置窄静态 guard；自动化覆盖重复 bootstrap、销毁清理和下一套对象图重新争用，Editor PlayMode 验收同时执行该 reset callback。
- [Dispatcher 或 tick 基础过早扩散成全局总线] → 只由 AppRoot/Composition 拥有，不暴露服务查询，不支持任意运行时注册；后续消费者必须通过窄依赖显式取得能力。
- [Editor 接线可能与仓库代码不同步] → 每次接线后检查 Unity diff、缺失脚本、序列化引用、Build Scene 顺序和生成的 `.meta`，再执行 PlayMode/build 验收。
- [C0 只验证空业务对象图，无法证明网络和 UI 生命周期] → 本 change 只冻结基础契约；C1/C2 必须用真实 channel、Service 和 screen 扩展相同验收，不把 C0 测试当作客户端资格结论。

## Migration Plan

1. 核对并最小化现有 Package 与 ProjectSettings，固定 Windows Company Name/Product Name，保持 Unity 版本与 `versions.yaml` 一致。
2. 增加 runtime/test assemblies、纯 C# 生命周期实现和 Unity Hosts；先通过不依赖场景的 EditMode tests。
3. 在 Unity Editor 中导入代码、保存 Unity 生成的 `.meta`，把 AppBootstrap/AppRoot 所需组件与引用接入 `BootstrapScene`。
4. 运行 PlayMode tests，重复进入 PlayMode并重载 BootstrapScene，验证唯一性、回滚和销毁行为。
5. 执行 Windows Development build smoke、OpenSpec strict、文本质量与 Git 检查后归档。

回滚时删除新增 runtime/test 代码和对应 Unity 生成资产，移除 BootstrapScene 接线并恢复本 change 修改的 ProjectSettings；服务端与冻结协议无需回滚。

## Open Questions

无。Addressables、协议生成、网络、业务 Service、正式 UI 与 Release 构建策略均由路线图中的后续独立 change 决定。
