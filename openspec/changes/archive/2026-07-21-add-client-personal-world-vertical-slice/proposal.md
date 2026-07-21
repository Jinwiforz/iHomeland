## Why

客户端已经具备冻结协议、HTTPS/WSS/TLS-TCP、Session、PersonalWorld/VisitSession Services 与统一 UI routing，但 production route registry、产品页面和 World Scene Scope 仍为空，玩家无法通过 Unity 完成第一里程碑的真实流程。现在需要把这些已验收边界连接成一个最小但完整的产品竖切，验证 UI、应用状态与场景生命周期在真实 Owner/Visitor 流程中保持单一所有权。

## What Changes

- 增加个人世界客户端竖切入口，在 AppRoot 完成初始化后显式打开登录流程，并通过窄组合边界调用既有 bootstrap、Session、WorldAdmission、PersonalWorld 与 VisitSession owner；不复制 token、world、visit 或连接事实。
- 交付 UI Toolkit 的登录/注册、个人世界 shell、邀请与访问状态页面，以及 uGUI 的 scene-bound world HUD；所有页面通过 `ClientUiRouter` 的 production definition 和显式 Host 引用接入，不直接访问 transport、generated message 或完整 Composition 容器。
- 增加具体 World Scene Scope 与场景切换适配，使 own-world、JoiningVisit、Visiting、ReturningOwnWorld 和 session invalidation 驱动可取消、可代际隔离的场景/UI 状态；旧 SceneContext、旧 route binding 与迟到 callback 不得复活。
- 将 loading、disabled、retry、revision conflict、permission、断线、Owner grace、kick/close/leave 和 safe-return 映射为稳定低敏 UI 状态，mutation 未判定时不自动重试或伪造成功。
- 通过纯 C# flow/presentation tests、真实 UI Toolkit/uGUI PlayMode tests、双客户端本地端到端流程和 Windows Development build 验证注册/登录、进入自己的世界、邀请访问、Visitor 权限、主动离开/踢出与安全返回。
- 本 change 不交付角色移动、战斗、NPC、任务、背包、聊天、完整美术地图、Addressables/Resources 资源系统、安全 token 持久化、独立通道自动恢复、Party、Room 或 ActivityInstance。

## Capabilities

### New Capabilities

- `client-personal-world-vertical-slice`: 定义注册/登录、个人世界进入、邀请访问、Owner/Visitor 页面与 World Scene Scope 的端到端产品行为、错误收敛和验收边界。

### Modified Capabilities

- `client-ui-routing`: 将空 production registry 演进为首批产品 route/Host，保持唯一 owner、输入焦点、层级与生命周期契约。
- `client-runtime`: 将仅有 BootstrapScene 的基线扩展为 BootstrapScene 首入口加具体 World Scene Scope，并冻结场景加载、generation、取消与退出清理行为。

## Impact

- 客户端将新增个人世界竖切的纯 C# flow/presentation model、UI Toolkit/uGUI 产品 Host、UXML/USS/Prefab 与 World SceneContext，并由 `AppComposition` 显式注入现有 Services、router 和 scene lifetime owner。
- BootstrapScene 将登记首批 production routes 和持久 UI Host；PC Build Settings 将保留 BootstrapScene 为首个入口，同时加入实际 world scene。
- 不修改服务端、协议、存储、消息路由、credential 语义或现有 aggregate owner；客户端只消费已冻结 operation、PUSH、projection 和 command policy。
- 后续 `qualify-client-v1` 将在本竖切之上补充 clean install、token restore、独立通道恢复、Release build 与更完整故障矩阵，本 change 不提前实现这些独立能力。
