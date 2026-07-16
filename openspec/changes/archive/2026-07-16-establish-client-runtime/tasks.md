## 1. 固定 Unity 工程基线

- [x] 1.1 核对 `versions.yaml`、`ProjectVersion.txt`、Package manifest/lock、URP、Input System、Windows Company Name/Product Name、Unity Services 断开状态、序列化模式与 PlayMode 设置，移除残留模板名称并保持 Addressables、第三方 DI/async 与 `Resources` 核心目录不进入 C0。
- [x] 1.2 增加客户端基线 EditMode 检查，验证锁定 Editor、Windows 项目身份、Unity Cloud 未绑定、BootstrapScene 唯一首场景、必要 Package 与禁止跟踪的缓存/generated 边界。
- [x] 1.3 建立最小 runtime、EditMode test 与 PlayMode test assembly definitions，只创建承载本 change 真实代码和测试的目录。

## 2. 实现纯 C# 应用生命周期

- [x] 2.1 实现生命周期状态、参与者和不可变 composition 结果，使用 XML documentation 说明 owner、线程、deadline、失败与重复调用语义。
- [x] 2.2 实现串行初始化和成功项清理栈，确保中途失败在独立 deadline 内逆序回滚并保留启动错误与回滚错误。
- [x] 2.3 实现幂等逆序停止、并发停止结果共享、尽力清理与错误/超时聚合，禁止停止后的对象图重新进入 Running。

## 3. 实现运行调度与 Scene Scope 基础

- [x] 3.1 实现有界 MainThreadDispatcher，覆盖主线程 drain、容量/停止拒绝、callback 错误隔离和无 UnityEngine.Object 后台访问。
- [x] 3.2 实现由 Composition 冻结的显式 tickable 快照，使 AppRoot 无需反射、场景扫描或全局 event bus 即可逐帧驱动。
- [x] 3.3 实现单一 Scene lifetime owner、单调 generation、cancellation 与幂等释放，保证新场景、卸载和 App Scope 停止都会使旧回调失效。

## 4. 建立 Unity 应用入口

- [x] 4.1 实现 AppComposition，显式构造 C0 对象图并把 lifecycle、dispatcher、tickable 与 Scene lifetime 交给 AppRoot，不提供 service locator。
- [x] 4.2 实现轻量 AppRoot Host，负责 `DontDestroyOnLoad`、Update 驱动、显式停止和 Unity 退出/销毁桥接，不承载业务状态。
- [x] 4.3 实现 AppBootstrap 唯一性争用与 SubsystemRegistration 静态重置，使重复场景加载和关闭 Domain Reload 的连续 PlayMode 都只产生一个对象图。
- [x] 4.4 在 Unity Editor 中完成 AppBootstrap/AppRoot 组件挂载、直接序列化引用、Windows Company Name/Product Name、BootstrapScene 保存与 Build Profiles 首场景核对，并复核 Unity 生成的 `.meta` 和序列化 diff。

## 5. 覆盖生命周期自动化

- [x] 5.1 增加 EditMode tests，覆盖初始化顺序、部分失败、逆序回滚、聚合错误、重复/并发停止、deadline、Dispatcher 容量/线程与 Scene generation。
- [x] 5.2 增加 PlayMode tests，覆盖唯一 AppRoot、重复 bootstrap、跨场景持有、Update 驱动、销毁清理和下一套对象图重新争用。
- [x] 5.3 分别运行 `IHomeland.Client.Runtime.EditModeTests` 与 `IHomeland.Client.Runtime.PlayModeTests` 的全部测试，并执行 Windows Development build smoke，确认无 Console error、缺失脚本、缺失引用或重复 AppRoot。
  - 2026-07-16：EditMode 26/26 通过（0.696s）；PlayMode 4/4 通过（0.314s）；Windows Development build smoke 与 Player 启动通过，Console 无 error，Player log 未发现 `Curl error`、`Token Exchange` 或 `UnityConnectWebRequestException`。

## 6. 完成质量门

- [x] 6.1 复核全部手写 C# 类型与成员的中文 XML documentation、命名、所有权和生命周期注释，删除空 manager、万能接口、无消费者目录与重复逻辑。
- [x] 6.2 检查 Git 状态中不存在 Library、Temp、UserSettings、构建输出、generated C#、密钥或伪造 `.meta`，并确认 Unity 正常源资产及 Editor 生成 `.meta` 完整。
- [x] 6.3 运行客户端基线检查、OpenSpec change/all strict、`git diff --check` 与适用的仓库质量门，记录 C0 验收结果并准备 specs 同步与归档。
- [x] 6.4 归档前复盘 artifacts、长期 owner 文档、注释契约、并发生命周期、测试真实性与最小对象图，修正文档漂移和明确的 clean code 问题。
