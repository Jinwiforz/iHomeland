## 1. 路由模型与登记边界

- [x] 1.1 按 `docs/file-structure.md` 建立 `Presentation/Navigation`、`Presentation/Hosts/UIToolkit` 与 `Presentation/Hosts/UGUI` 手写代码目录，不创建产品页面、空 Theme manager 或第二个程序集。
- [x] 1.2 实现封闭 route identity、framework owner、layer、input mode、lifecycle、不可变 definition 与只读 route snapshot，并为所有非法 enum、缺失 scene generation 和重复 identity 提供稳定 validation failure。
- [x] 1.3 定义 initialize/bind/show/hide/unbind/dispose、input、focus 与 Host registry 的最小窄接口，确保接口不暴露资源路径、transport、generated message、credential 或 AppCompositionResult。
- [x] 1.4 增加 registry EditMode tests，覆盖空 production registry、重复 identity、Host 重用、framework 错配、definition 防御性复制与未登记 route 拒绝。

## 2. 事务化 ClientUiRouter

- [x] 2.1 实现 App Scope 唯一 `ClientUiRouter` 的有界 transition gate、单调 navigation generation、页面 cancellation、不可变状态提交和停止后拒绝。
- [x] 2.2 实现 Screen 单 owner、Overlay 顺序、Modal 严格栈与 System 顶层语义，并固定跨框架 layer slot，禁止 Host 注入任意 sorting order。
- [x] 2.3 实现 candidate initialize/bind/show 与 focus 尝试的事务提交，以及任一提交前失败后的逆序清理、旧 owner 输入与 focus 恢复；提交后 focus/cleanup/subscriber 失败必须可观察，subscriber 必须在锁外隔离通知。
- [x] 2.4 实现 `Cached`、`Recreate` 与 `SceneBound` 的 hide/unbind/dispose、重新 bind、scene generation 和迟到 callback gate，不允许旧 view 复活。
- [x] 2.5 增加 router EditMode tests，覆盖重复 open/close、replace、modal 嵌套、并发容量、caller cancel/commit 竞态、Host 阶段失败、subscriber 异常、scene generation 与幂等 stop。

## 3. Input System 与双 Unity Host

- [x] 3.1 实现持久 `ClientUiHostRoot` 的显式 UI Toolkit/uGUI Host 列表和启动前完整校验，不使用 `FindObject*`、反射扫描、静态自注册或 service locator。
- [x] 3.2 实现 Input System 资产 clone 的唯一 owner，只使用登记的 `Player`/`UI` action map，并统一 input mode、gameplay gate、cursor visible/lock 与停止清理。
- [x] 3.3 实现 UI Toolkit Host 对 UIDocument/panel、VisualElement bind/show/hide、稳定 layer 与有效 focus token 的轻量适配。
- [x] 3.4 实现 uGUI Host 对 Canvas/EventSystem、GameObject bind/show/hide、稳定 sorting layer、raycast 与有效 focus token 的轻量适配。
- [x] 3.5 增加程序化 PlayMode fixtures，交替验证 UI Toolkit screen、uGUI overlay、跨框架 modal、action map、cursor、previous/default focus、射线遮挡、销毁和 teardown 无残留。

## 4. Composition 与 BootstrapScene 接线

- [x] 4.1 扩展 `AppBootstrap` 的直接序列化 Host root 引用与程序化 fixture 配置，在构造对象图前 fail fast 校验同一持久 root 和必需 Input System 引用。
- [x] 4.2 在 `AppComposition` 显式构造空 production registry、Input/Host boundary 与唯一 router，并按依赖顺序登记 AppLifetime，使逆序停止先关闭 router 再释放 Host/Input，且不改变既有 world/channel/session/http owner 顺序。
- [x] 4.3 仅向需要接续页面的窄组合边界导出 `ClientUiRouter`，更新 composition tests 证明重复 bootstrap、启动失败回滚和关闭 Domain Reload 后不会产生第二个 UI owner。
- [x] 4.4 在 Unity Editor 中为 BootstrapScene 的现有 AppRoot 配置唯一 `ClientUiHostRoot` 和 `InputSystem_Actions` 直接引用；不手工编辑 Scene YAML 或 `.meta`，production Host 列表与 route registry 保持为空。

## 5. 注释与项目文档

- [x] 5.1 按 `docs/code-comment-convention.md` 为所有手写 C# 类型和成员补充中文 XML documentation，重点说明 generation、事务提交、锁外通知、focus token、Input clone、失败与所有权不变量。
- [x] 5.2 更新客户端架构、UI 架构、接入、文件结构与路线图，记录当前已落地 UI routing/Host 边界、空 production registry 和下一条个人世界竖切进入条件，删除与实现不一致或重复描述。
- [x] 5.3 检查 change 未引入产品 UXML/USS/Prefab、Resources/Addressables、业务 Presenter、SceneContext、网络 operation、手写 `.meta` 或 generated code，并保持 UI 不拥有账号/world/visit 最终事实。

## 6. 分层验收

- [x] 6.1 运行不依赖 Unity Editor 的静态 C# 编译检查和既有协议生成/fixture parity 验证，确认新增 Presentation 代码不引用被忽略 generated scripts 或新增依赖。
- [x] 6.2 在 Unity Test Runner 执行全部 EditMode tests，确认 registry/router/input/lifecycle 新测试与既有 protocol、HTTP、WSS、TLS/TCP、PersonalWorld tests 全部通过。
- [x] 6.3 在 Unity Test Runner 执行全部 PlayMode tests，确认双 Host、AppRoot 唯一性、重复 bootstrap、input/focus、销毁与退出清理全部通过且 Console 无非预期错误。
- [x] 6.4 构建并启动 Windows Development Player，确认空 route snapshot 不打开页面、不发起业务网络动作，Player.log 无 UI 初始化未观察异常、重复 EventSystem、旧 view 回写或 credential。
- [x] 6.5 运行 `openspec validate integrate-dual-ui-routing --strict`、`openspec validate --all --strict` 与 `git diff --check`，检查文档、生成边界、Unity 忽略项和任务勾选一致。
