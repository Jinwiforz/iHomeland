# 客户端运行时模块化重构实施基线

## 1. 基线用途

本文记录 `modularize-client-runtime-architecture` 开始 apply 时的可审计起点。后续每个迁移阶段必须以本基线和 characterization tests 为行为对照；mandatory gate 失败时，应回到最近一次通过全部相关 gate 的阶段，而不是保留兼容双实现。

本记录不保存本机 qualification run-id、临时目录、凭据或其他不可提交信息。

## 2. 源代码与工作区

| 项目 | 基线值 |
| --- | --- |
| Git branch | `develop` |
| Git commit | `b1be555bbd8e47e31ed20e20abb382fb6987fbef` |
| apply 前已有工作区改动 | 仅新增、未跟踪的 `openspec/changes/modularize-client-runtime-architecture/` |
| Unity Editor | `6000.5.2f1` |
| Unity revision | `eb73d3b415a1` |

回滚判断以 Git commit、受影响文件差异、自动化 gate 和 qualification digest 共同为准，不把未提交的本机 Unity 生成物作为基线。

## 3. 合同、版本与构建摘要

| 证据 | SHA-256 / digest |
| --- | --- |
| `versions.yaml` | `383C27713148D6092DCCA451024FF8613497D8CFCA9B948FD14C5667070BD52F` |
| `release.json` | `03D0CE77F8812DB444EDFE9BB8A287C292DB05812A6776C8AB1DF97E38F1CD6B` |
| `client/version.json` | `651D59109CB7545FB0056CE353CBDC3CF8B58F73A812AC18BF249067BB07E15E` |
| 最后一次通过 qualification 的 contract digest | `9f57410066bd9747330300240144fa0984026d7447c9b6afee95731b8bc3468d` |
| 最后一次通过 qualification 的 Development build digest | `b4b93d409466670bf2159c737e4c70a327b46b14f623bf073677fc4ff382e062` |
| 最后一次通过 qualification 的 Release build digest | `0f0b6c2276b211a468df440a380d1ad7489b2063a7d143361ceb336383e3d3f7` |
| 最后一次通过 qualification 的 cleanup | `pass` |

最后一次完整 client-v1 qualification 状态为 `qualified=true`、schema version 为 `1`。工具基线为 Unity `6000.5.2f1`、.NET SDK `8.0.100`、Go `1.25.0`、Git `2.54.0` 和 Windows PowerShell `5.1.19041.2673`。

## 4. 手写 asmdef 基线

| asmdef | SHA-256 |
| --- | --- |
| `client/Assets/App/Scripts/IHomeland.Client.Runtime.asmdef` | `2D54D74FADACC3ADDEEEB3D4D7E3D94F43ADA211761180FAE191AEAACBD9B85E` |
| `client/Assets/App/Tests/EditMode/IHomeland.Client.Runtime.EditModeTests.asmdef` | `E3B3AC2C384A8C7C0F5F8702E210D5D8B28EDFBDACF8297A6B4243DF2F807469` |
| `client/Assets/App/Tests/EditMode/Protocol/IHomeland.Client.Protocol.EditModeTests.asmdef` | `8AD18965183888CDE7FA1499EFC9C8C450F33D175570A910A9C423D9C324A86E` |
| `client/Assets/App/Tests/PlayMode/IHomeland.Client.Runtime.PlayModeTests.asmdef` | `4933B04EC4C9E0EA9048E5A845B2319422326D9FB9751902C78ED84F402A159F` |

生产手写代码当前全部位于 `IHomeland.Client.Runtime`。它直接引用 `IHomeland.Client.Protocol.Generated`、`Unity.InputSystem`、`Unity.TextMeshPro` 和 `Google.Protobuf.dll`，`autoReferenced=true`、`noEngineReferences=false`。因此 Application、Infrastructure、Presentation 和 Unity Runtime 的编译期边界尚未建立。

迁移目标是单向 DAG：

```text
Foundation
├── Application
│   └── Presentation
└── Infrastructure

Runtime -> Foundation + Application + Infrastructure + Presentation
Infrastructure -> Foundation + Application + Protocol.Generated
```

实际 asmdef 引用必须由 architecture gate 校验；禁止通过循环引用、全局容器或复制合同绕过边界。

## 5. 核心类型体量

| 类型 / 文件 | 基线行数 |
| --- | ---: |
| `ClientPersonalWorldExperience` | 2199 |
| `WorldAdmissionCoordinator` | 1745 |
| `ClientGameplayChannel` | 1388 |
| `SessionCoordinator` | 1379 |
| `VisitSessionService` | 1334 |
| `ClientUiRouter` | 1314 |
| `ClientQualificationPlayerSoak` | 1291 |
| `ClientConnectionRecoveryCoordinator` | 1211 |
| `ClientControlChannel` | 1051 |
| `ClientPersonalWorldUiToolkitView` | 994 |
| `ClientHttpCodec` | 919 |
| `PersonalWorldService` | 638 |
| `AppComposition` | 303 |

行数只作为复杂度提示，不作为硬门槛。重构完成条件是 owner、状态迁移、依赖方向和测试边界清晰，而不是机械降低单文件行数。

## 6. 状态 owner 基线

| 状态 | 当前唯一 owner | 允许协作者 / 输出 |
| --- | --- | --- |
| endpoint 与环境配置 | `ClientConfigurationStore` | Runtime 配置 Host；输出只读配置快照 |
| Session、token、ticket、admission | `SessionCoordinator` | HTTP/security adapter；输出 Session 快照与失效事件 |
| WSS connection lifecycle | `ClientControlChannel` | WebSocket adapter、control codec；输出 typed push 与 terminal event |
| TLS/TCP connection、pending、heartbeat | `ClientGameplayChannel` | TCP adapter、gameplay codec；输出 typed response/push 与 terminal event |
| PersonalWorld 与 assignment projection | `PersonalWorldService` | control/gameplay typed inputs；输出只读 world 快照 |
| VisitSession、invite inbox、role、membership | `VisitSessionService` | control/gameplay typed inputs；输出只读 visit 快照 |
| current world target 与 target generation | `WorldAdmissionCoordinator` | Session、world、visit 与 gameplay facades；输出 target 快照 |
| recovery intent 与恢复 single-flight | `ClientConnectionRecoveryCoordinator` | Session/control/gameplay/world operations；输出低敏恢复状态 |
| route/navigation generation | `ClientUiRouter` | UI registry、Runtime input/focus Host；输出 route 快照 |
| presentation generation、active intent 与 View State | `ClientPersonalWorldExperience` | owners、Router、Scene port；输出产品 View State |
| Scene generation 与 Scene 对象 | `SceneLifetimeOwner`、`ClientWorldSceneTransitionHost`、`SceneContext` | Runtime Scene/Host；不得持有业务最终事实 |

该表描述开始迁移时的逻辑 ownership。后续 owner registry 必须把 command、snapshot、module、允许协作者和测试入口声明为机器可检查数据。

## 7. 核心构造依赖基线

- `SessionCoordinator`：`ClientConfigurationStore`、`IClientHttpApi`、`IClientClock`、`IClientSecureSessionStore`、environment binding。
- `ClientControlChannel`：`ClientEnvironment`、`ClientConfigurationStore`、`SessionCoordinator`、`IClientWebSocketFactory`、`ClientControlCodec`、`MainThreadDispatcher`、delay/retry policy、gameplay invalidation callback。
- `ClientGameplayChannel`：`ClientConfigurationStore`、`SessionCoordinator`、`IClientGameplayConnectionFactory`、`ClientGameplayCodec`、`MainThreadDispatcher`、delay。
- `PersonalWorldService`：`ClientControlChannel`、`ClientGameplayChannel`。
- `VisitSessionService`：`ClientControlChannel`、`ClientGameplayChannel`、`IClientClock`。
- `WorldAdmissionCoordinator`：`SessionCoordinator`、`IClientClock`、`ClientGameplayChannel`、`PersonalWorldService`、`VisitSessionService`。
- `ClientConnectionRecoveryCoordinator`：`SessionCoordinator`、`ClientControlChannel`、`ClientGameplayChannel`、`IClientConnectionRecoveryOperations`、deadline。
- `ClientUiRouter`：`ClientUiRegistry`、`IClientUiInputCoordinator`、bounded queue capacity、cleanup timeout。
- `ClientPersonalWorldExperience`：bootstrap、Session/restore/recovery、dispatcher、control、world/visit/admission、Router、Scene transition port、recovery timeout。

主要待消除的编译期问题：

- Application 代码直接引用 `Infrastructure.Http`、`Infrastructure.WebSocket`、`Infrastructure.Tcp` 和 generated Protocol 类型。
- Presentation 的 Experience 直接引用 HTTP/WSS concrete failure 与 Runtime Scene adapter。
- Navigation contracts/diagnostics 直接引用 UnityEngine。
- Configuration 直接引用 HTTP concrete 类型。
- `AppCompositionResult` 暴露范围过宽，顶层装配清单随着内部对象数量线性增长。

## 8. 公开与 internal action 基线

### Session

`InitializeAsync`、`RegisterAsync`、`LoginAsync`、`RestoreAsync`、`RefreshAsync`、`LogoutAsync`、`IssueConnectionTicketAsync`、`GetWorldBootstrapAsync`、`AcceptVisitInviteAsync`、`IssueWorldAdmissionAsync`、`TryTakeConnectionTicket`、`TryTakeWorldAdmission`、`TryGetCurrent`、`TryInvalidateFromControlAsync`、`ForgetAsync`、`StopAsync`。

### Control

`InjectQualificationTransportDisconnect`、`InitializeAsync`、`RunAsync`、`WaitUntilConnectedAsync`、`StopAsync`。

### Gameplay

`InjectQualificationTransportDisconnect`、`InitializeAsync`、`ConnectAsync`、`JoinPendingVisitAsync`、`ReconnectPendingVisitAsync`、`InvalidateSession`、`CloseAsync`、`StopAsync`。

### PersonalWorld

`InitializeAsync`、`ApplyBootstrap`、`ApplyWorldSnapshot`、`ApplyAssignmentHint`、`ClearCurrentTarget`、`InvalidateControlProjection`、`StopAsync`。

### VisitSession

`InitializeAsync`、`SetTargetRole`、`ClearTargetRole`、`InvalidateControlProjection`、`ApplySnapshot`、`ApplyInvite`、`RetireAcceptedInvite`、`RetireRejectedInvite`、`ApplyOwnerAvailability`、`ApplyClosedNotice`、`OpenAsync`、`CreateInviteAsync`、`RevokeInviteAsync`、`KickAsync`、`CloseAsync`、`LeaveAsync`、`StopAsync`、`ApplySafeReturn`。

### WorldAdmission

`InitializeAsync`、`EnterOwnWorldAsync`、`JoinVisitAsync`、`LeaveVisitAsync`、`RetryReturnAsync`、`InvalidateSession`、`StopAsync`。

### Recovery

`InitializeAsync`、`BeginControlRecovery`、`CompleteControlRecoveryAsync`、`BeginAutomaticWorldRecovery`、`BeginManualWorldRecovery`、`BeginManualRecovery`、`BeginManualControlRecovery`、`WaitForSettledAsync`、`ConfirmSceneCommit`、`FailSceneCommit`、`StopAsync`。

### Router

`InitializeAsync`、`OpenAsync`、`CloseAsync`、`InvalidateSceneAsync`、`CanCommit`、`StopAsync`。

### Experience

`InitializeAsync`、`RegisterAsync`、`LoginAsync`、`RetryEnterOwnWorldAsync`、`RetryReturnAsync`、`RetryConnectionAsync`、`LogoutAsync`、`OpenVisitAsync`、`CreateInviteAsync`、`RevokeInviteAsync`、`AcceptInviteAsync`、`KickVisitorAsync`、`CloseVisitAsync`、`LeaveVisitAsync`、`ShowWorldVisitAsync`、`RequestWorldVisitFromGameplayMenu`、`RequestUiCancel`、`StopAsync`。

Experience 当前同时实现 `IAppLifetimeParticipant`、`IClientLoginActions`、`IClientShellActions`、`IClientWorldVisitActions`、`IClientWorldHudActions` 和 `IClientUiProductContext`。拆分后仍以语义 action facade 服务调用方，但状态迁移、projection、transaction 和 adapter 细节不得继续堆积在该 facade。

## 9. AppComposition 基线与目标

当前 `AppBootstrap -> AppComposition -> AppRoot` 是唯一应用入口，保留该生命周期边界。问题不是显式构造函数注入，而是 `AppComposition` 同时知道所有内部对象且 `AppCompositionResult` 暴露过宽。

目标采用两级强类型装配：

```text
AppComposition
├── FoundationComposition
├── InfrastructureComposition
├── SessionComposition
├── ChannelComposition
├── WorldComposition
├── PresentationComposition
└── RuntimeQualificationComposition
```

每个 module composition 只显式创建本模块私有组件并返回封闭 bundle；顶层只连接跨模块 facade、port 和生命周期参与者。禁止 `Resolve<T>()`、反射扫描、全局容器和 service locator。

## 10. 回滚与阶段判定

1. 每个阶段先补足或保持 characterization tests，再替换实现。
2. 新旧实现不得长期双写；阶段提交前删除被替换路径。
3. 协议 parity、相关 EditMode/PlayMode、architecture gate 或构建任一 mandatory gate 失败，该阶段不得标记完成。
4. qualification 使用同一 contract/build digest；不把混合 digest 证据视为成功。
5. Unity 序列化资产的 `.meta` 只由 Unity 管理，本次 apply 不手工创建或修改 `.meta`。

## 11. apply 前自动门基线

首次运行 `automatic` 时，协议、Unity tests、两种 build、Release surface scan、双 Player smoke 和 qualification 工具回归均通过，但治理阶段发现资格脚本仍硬编码校验已经归档的 `qualify-client-v1` change。该失败属于现有资格入口的过期 gate，而不是客户端行为失败。

修复后，治理阶段改为非交互地严格校验当前全部 OpenSpec specs 与 active changes，并增加回归防止重新依赖已归档 change 名称。随后从头重跑完整 `automatic`，结果如下：

| 项目 | 结果 |
| --- | --- |
| contract digest | `9f57410066bd9747330300240144fa0984026d7447c9b6afee95731b8bc3468d` |
| Development build digest | `48b58d7e5e8f132dad93c4273bfff3eefe4e7c17cf2406429eb7c800723805ad` |
| Release build digest | `d05f4394f187012b2ac19a54486594fa768ea1f8a97da93e28bc74ef5e0dcb47` |
| automatic stages | `13/13 pass` |
| EditMode | `257/257 pass` |
| PlayMode | `18/18 pass` |
| qualification records | `14` |
| cleanup | `pass` |
| `.meta` Git 差异 | `none` |

`automatic` 报告保持 `qualified=false` 是既有资格语义：只有同一 digest 上继续完成 soak、人工证据与 finalize 才能产生最终资格结论。对本 change 的进入基线而言，所有本阶段要求的自动 mandatory gate 已通过。

## 12. Owner registry 与架构报告基线

声明式 owner registry 位于 `client/Architecture/owner-registry.json`，统一只读入口为 `tools/client-architecture/client-architecture.ps1`。迁移开始时的 report-only 结果为：

| 项目 | 基线 |
| --- | ---: |
| 唯一 owner | 11 |
| 唯一 state kind | 16 |
| owner/source/command/snapshot/test 差异 | 0 |
| 手写 asmdef | 4 |
| 待迁移禁止 import | 39 |
| `AppCompositionResult` 外部引用文件 | 3 |
| Unity 序列化脚本引用关系 | 1 |

迁移期 `mode=report-only`、`hardGateEnabled=false`。Owner registry 自身的唯一性和源码对应关系已经可验证；asmdef DAG、禁止 namespace、Composition 容器扩散、序列化完整性和旧单体归属在 task 11.3/11.4 才切换为完整 hard gate。
