## 1. Experience 状态与入口

- [x] 1.1 定义封闭 presentation phase/failure、不可变 `ClientPersonalWorldViewState`、页面切片和窄语义 action ports，确保模型不含 password、token、ticket、admission、endpoint、generated message 或 Unity object。
- [x] 1.2 实现 App Scope 唯一 `ClientPersonalWorldExperience` 的 lifecycle、presentation generation、页面 intent cancellation、同类 mutation single-flight 与锁外 View State 通知。
- [x] 1.3 实现启动后只打开本地 Login route 的入口，以及显式 bootstrap -> register/login -> control run -> `EnterOwnWorldAsync` 顺序；失败和停止时清除 password 并拒绝迟到提交。
- [x] 1.4 订阅 Session、control、WorldAdmission、PersonalWorld 与 VisitSession 的窄状态边界，将权威 snapshot 派生为 Login/Shell/WorldVisit/WorldHud View State，不复制既有 owner。
- [x] 1.5 增加 Experience EditMode tests，覆盖冷启动零网络、register/login 成功与失败、重复提交、caller cancel、control run fault、session invalidation、subscriber 异常和逆序停止。

## 2. PersonalWorld 与 VisitSession 产品动作

- [x] 2.1 将 own-world loading/retry/logout 语义 action 连接到既有 `WorldAdmissionCoordinator`，只在 current session/presentation/target generation 匹配时提交 route/scene 意图。
- [x] 2.2 将 Owner open/create/revoke invite、kick 与 close action 连接到 `VisitSessionService`，由既有 Service 提供 expected revision、role gate 和首次 mutation 结果。
- [x] 2.3 将 inbox accept、Visitor leave 与 failed return retry 连接到 `WorldAdmissionCoordinator`，保持 accept/admission idempotency、credential 单次交付与 commit-unknown 不自动重试。
- [x] 2.4 收敛 safe-return、Owner grace、kick/close、revision conflict、permission 与 transport failure，先关闭旧 target mutation 和表现 binding，再进入权威 ReturningOwnWorld 或低敏错误状态。
- [x] 2.5 增加 product action EditMode tests，覆盖 Owner/Visitor 权限、invite expiry、重复 action、低/同 revision、join/leave/kick/close、safe-return、返回失败和旧 target 不复活。

## 3. Production routes 与产品 View

- [x] 3.1 增加 `WorldHud` route identity，并在 Composition 中登记 Login、Shell、WorldVisit、WorldHud 与 ConnectionLost 的 production definitions；Settings 保持未登记且每个 route/Host 一对一。
- [x] 3.2 为现有 UI Toolkit/uGUI Host 增加显式产品 binding seam，使页面取得当前 route cancellation、不可变 View State 和窄 action port，但不改变 router definition 或暴露资源路径/Composition 容器。
- [x] 3.3 实现 UI Toolkit Login View 的 register/login 模式、文本输入、password 清理、loading/disabled、默认 focus、validation 和低敏错误呈现。
- [x] 3.4 实现 UI Toolkit Shell、WorldVisit 与 ConnectionLost View，覆盖 own/visiting/returning 状态、invite/member 列表、Owner/Visitor actions、deadline、retry/logout 与 modal focus。
- [x] 3.5 实现 uGUI WorldHud View，只呈现 current role、world/visit 状态和打开 WorldVisit/leave 等登记动作，并以 Scene generation 控制 raycast、显示和销毁后回写。
- [x] 3.6 增加产品 View/Host PlayMode fixtures，验证 UI Toolkit 与 uGUI 同屏层级、action map、cursor、mouse/keyboard/gamepad focus、IME、重复 bind、hide/unbind 和 teardown 无 command 双发。

## 4. World Scene Scope

- [x] 4.1 定义只接受登记 build scene identity 的 scene catalog/transition 窄接口，并为 current scene handle、candidate load 与 scene generation 提供不可变状态和稳定失败。
- [x] 4.2 实现 `ClientWorldSceneTransitionHost` 的 additive load/unload、候选回滚、旧 generation 失效和 current handle 原子提交；只在已加载 Scene root 中校验唯一 `PersonalWorldSceneContext`，不使用全局扫描或静态注册。
- [x] 4.3 实现轻量 `PersonalWorldSceneContext`，只持有 camera、lighting、scene root 与表现引用，显式接收 `SceneLifetime` 和无 credential View State，并在 unload/stop 时解除订阅和取消任务。
- [x] 4.4 将 Experience 的 OwnWorld/Visiting/Returning/session-invalidated 转换连接到 scene Host 与 scene-bound WorldHud，固定先失效旧 UI/Scene、再加载新 generation、最后提交 HUD 的顺序。
- [x] 4.5 增加 Scene EditMode/PlayMode tests，覆盖唯一 Context、缺失/重复 Context、load failure、target 在 load 中切换、unload 竞态、App stop 和迟到 callback 不覆盖 current Scene。

## 5. Unity 产品资产与接线

- [x] 5.1 在 Unity Editor 中创建 Login、Shell、WorldVisit 与 ConnectionLost 的 UXML/USS，使用单一项目 USS 表达首期 color/typography/spacing/focus/disabled/loading/error 语义，不创建 Theme manager、Resources 或 Addressables。
- [x] 5.2 在 Unity Editor 中创建最小 `WorldHud` uGUI Prefab，使用 TextMeshPro 文本与带 `TextMeshProUGUI` 标签的 uGUI `Button`，配置 CanvasGroup、raycast、默认 focus 与直接引用；不创建 Legacy Text，不放置 world 业务事实、transport 或静态 singleton。
- [x] 5.3 在 Unity Editor 中创建 `PersonalWorldScene`，只配置首期 camera、lighting、scene root 与唯一 `PersonalWorldSceneContext`，不提前增加角色、战斗、NPC、地图流送或内容资源系统。
- [x] 5.4 在 Unity Editor 中为 BootstrapScene 接线四个 UI Toolkit Host、WorldHud Host、scene transition Host 与产品 action binding，并保持唯一 AppRoot、ClientUiHostRoot 和 EventSystem。
- [x] 5.5 在 Build Profiles/Build Settings 中保持 BootstrapScene 为 index 0 并加入 PersonalWorldScene；增加 Editor 资产校验，拒绝缺失 UXML/USS/Prefab、重复 Context、第二个应用根或未跟踪直接引用。不得手工编辑 Scene/Prefab YAML 或 `.meta`。

## 6. Composition、注释与 owner 文档

- [x] 6.1 在 `AppComposition` 显式创建并注入 scene Host 与 Experience，按依赖顺序登记 AppLifetime，使停止时 Experience -> router -> Scene -> world/channel/session 逆序清理；AppRoot 不成为 service locator。
- [x] 6.2 按 `docs/code-comment-convention.md` 为所有手写 C# 类型和成员补充中文 XML documentation，重点说明 presentation/target/scene generation、password 清理、single-flight、错误降敏、Host 引用与失败回滚。
- [x] 6.3 更新客户端架构、UI 架构、接入、文件结构、路线图和必要 README，记录 production routes、产品资产、PersonalWorldScene、最小 USS 主题决策与仍未交付的 gameplay/token restore/自动恢复边界，删除“空 registry/仅 BootstrapScene”等过期描述。
- [x] 6.4 检查 change 未引入第二套 session/world/visit 状态机、service locator、全局 event bus、静态 Scene 注册、任意资源路径、Resources/Addressables、手写 generated code 或 credential 日志。

## 7. 分层验收

- [x] 7.1 运行不依赖 Unity Editor 的静态 C# 编译检查、`tools/proto/proto.ps1 verify` 与既有 fixture parity，确认产品代码不引用被忽略 generated scripts 或新增第三方依赖。
- [x] 7.2 在 Unity Test Runner 执行全部 EditMode tests，确认 Experience、View State、action policy、error mapping、route/scene generation 与既有 protocol/network/world tests 全部通过。
- [x] 7.3 在 Unity Test Runner 执行全部 PlayMode tests，确认产品 UI Toolkit/uGUI Host、scene load/unload、Context、Input/focus、重复 bootstrap、销毁与退出清理全部通过且 Console 无非预期错误。
- [x] 7.4 构建并启动 Windows Development Player，确认未提交登录前只显示 Login 且零业务网络副作用，提交后能进入 PersonalWorldScene；检查 Player.log 无未观察异常、重复 owner、旧回写或 credential。
- [x] 7.5 使用本地服务端和两个 Windows Development Player 验证注册/登录、进入 OwnWorld、定向 invite、接受访问、Visitor 权限、主动离开、Owner kick/close 和安全返回，保存与 Go 资格客户端一致的验收结果。
- [x] 7.6 验证登记的 16:9、16:10、21:9、窗口化/全屏、DPI、mouse/keyboard/gamepad、文本输入/IME、modal/focus/cursor、loading/disabled/error 状态和页面销毁后不回写。
- [x] 7.7 运行 `openspec validate add-client-personal-world-vertical-slice --strict`、`openspec validate --all --strict` 与 `git diff --check`，复核 specs、tasks、owner 文档、Unity 资产边界和验收记录一致。

## 8. 权威状态一致性回归

- [x] 8.1 让 VisitSession inbox 在 accept 成功后线性化退役已消费 invite，并以更高 created revision 的同 target PUSH 替换旧 pending identity；commit-unknown 不猜测结果。
- [x] 8.2 移除 WorldVisit 对 VisitSessionID/InviteID 的自由文本提交，建立随不可变 View State replacement 校验的 invite/member selection，并为每个 action 投影精确 capability。
- [x] 8.3 让 action failure 绑定其 authority scope，在新 VisitSession/world snapshot 到达后确定性清除过期错误；禁止延时、tick 或自动 command 重试。
- [x] 8.4 由 gameplay channel 对非预期 terminal disconnect 发布事件，使 world flow、Scene/HUD 与 UI 立即进入不可交互的可见失败状态，且显式 close/safe-return/shutdown 不误报。
- [x] 8.5 增加 accept -> leave -> reinvite -> accept、revoke -> reinvite、旧 selection replacement、空集合按钮、重复点击和 gameplay disconnect 的 EditMode/PlayMode 回归。
- [x] 8.6 重新运行全量 Unity tests、协议/OpenSpec strict、Windows Development build 与双客户端操作矩阵，并以本轮日志替换此前不足的双端验收结论。
- [x] 8.7 让全部 Bearer HTTP operation 在 access 绝对到期后共享一次 Session single-flight refresh，并将 presentation failure 映射为玩家可理解的中文低敏文本，覆盖过期重连、并发 refresh 与失败收敛测试。

## 实现验收记录

- 2026-07-20 最终 Windows Development Player 位于 `.local/client-build/state-consistency-commercial-final/iHomeland.exe`。Unity EditMode 为 180/180，`StandaloneWindows64` Player PlayMode 为 18/18；协议 verify 的 9 个阶段及服务端全量 Go tests 通过。
- 使用该最终包启动两个真实 EXE，完成 invite A 接受进入 Visitor、主动离开安全返回、同一 VisitSession 的 replacement invite B 再次接受进入 Visitor。Visitor accept 后 inbox 立即为空；Owner member snapshot 同步清空已消费 outgoing invite、撤销按钮失效，未出现红色 command error。证据位于 `.local/client-acceptance/20260720-commercial-final/`。
- 同一对 EXE 中强制终止服务端，双方立即显示 `ConnectionLost` 且旧 Scene/HUD 不可交互；重启服务端后双方各提交一次 reconnect，均重建 own-world gameplay/Scene/HUD 并关闭 modal。日志没有 `protocolincompatible`、`internal`、未观察异常或 credential。
- 实测同时发现并修复 PersonalWorld revision 与 assignment generation 被错误耦合、显式 reconnect 只恢复 WSS、以及 Owner 保留已消费 outgoing invite 三个根因；恢复路径与投影收敛均为事件/权威 snapshot 驱动，不含固定延时、tick 修正或隐式 command 重试。
- 2026-07-20 补齐 access 绝对到期边界：全部 Bearer HTTP operation 在提交前经唯一 Session owner 检查 expiry，过期时共享既有 single-flight refresh，并只使用刷新后的 generation；不使用 timer、tick、固定延时或 401 重试循环。Unity EditMode 182/182、PlayMode 18/18 通过，新 Development Player 位于 `.local/client-build/windows-development-access-refresh/iHomeland.exe`。
- 2026-07-20 操作者完成真实 Windows 显示与输入矩阵，确认 16:9、16:10、21:9、窗口化/全屏、DPI、mouse/keyboard/gamepad、文本输入/IME、modal/focus/cursor、loading/disabled/error 及页面销毁后不回写全部通过；详细边界记录于 `display-input-acceptance.md`。

## 9. 最终跨 Change 审计

- [x] 9.1 审计全部五个 active change 的 artifacts、实现与测试映射，并将所有 delta Requirement/Scenario 同步到长期 `openspec/specs/`。
- [x] 9.2 审计 C#/Go/Proto 注释、状态 owner、异步任务、日志与反模式，修复输入回调未观察导航任务和 `server/.local/` 仓库卫生缺口。
- [x] 9.3 更新客户端 UI、文件结构与路线图中的过期交付描述，形成 `final-audit.md` 记录结论、验证和剩余非阻断风险。
- [x] 9.4 重新执行静态 C# build、Go vet/gofmt、协议 9 阶段、OpenSpec strict、delta/main parity 与 `git diff --check`。
