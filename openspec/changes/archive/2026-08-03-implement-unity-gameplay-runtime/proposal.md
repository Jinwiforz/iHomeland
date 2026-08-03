## Why

B0.6 已交付真实 C++ 安全 UDP/KCP、独立协议客户端和代表性 network development-readiness，但 Unity Player 仍只能进入 PersonalWorld/VisitSession，不能消费服务器权威 battle input、snapshot 与 lifecycle。现在需要在不改变既有服务端权威、world/visit owner 和唯一 UI/Scene 生命周期的前提下，建立可由真实 C++ runtime 验收的 Unity gameplay 基础层，为后续 PersonalWorld combat 内容竖切提供稳定客户端边界。

## What Changes

- 增加 App Scope `BattleNetworkClient` 及 Infrastructure secure UDP/KCP adapter：经既有 HTTPS `BattleTicket` operation 建立 current target 绑定的 battle generation，严格消费冻结 handshake、AEAD、replay、raw/KCP route、rebind、resync、input acknowledgement 与 terminal lifecycle。
- 增加纯 C# `GameplayReplica`、`GameplayPrediction`、`InputHistory`、`PredictedStateHistory`、`GameplayInterpolation` 与 presentation projector，按 `battle-network-profile-v2` 的 40 Hz input、20 Hz simulation、10 Hz snapshot、16-Tick history 和校正/插值边界运行。
- 把 battle generation 与 Session epoch、World target/AssignmentStamp、Scene generation 组合为单一提交门；safe-return、assignment replacement、Session invalidation、断线恢复和 Scene teardown 必须原子退役旧 socket、history、replica 与表现订阅。
- 增加 PersonalWorldScene 的 Input System semantic action host、Actor/View binding、scene-bound uGUI battle HUD，并复用既有 UI Toolkit、`ClientUiRouter`、input/focus owner 和 `MainThreadDispatcher`，不建立第二套 UI 或网络 manager。
- 锁定并接入 Cinemachine 3.1.7，以唯一 Scene Scope `CinemachineCameraHost` 消费封闭 `CameraIntent`；镜头、Animator、VFX、HUD 和 Unity Physics 只承担输入采样与表现，不产生权威 transform、命中、伤害或 cooldown。
- 增加 wire/crypto/KCP parity、纯 C# prediction/reconciliation/interpolation、EditMode/PlayMode、真实 Go parent + C++ child + Unity Development Player 的 Owner/Visitor、loss/reconnect/assignment replacement 与 teardown 定向验收；维护完整最终资格能力，但不自动执行完整 battle fault/capacity/security/lifecycle/soak/finalize。
- 更新版本、客户端 owner registry、Composition/Scene/UI/file-structure 文档、质量目录与 closed `validation.json`，使新增 owner、依赖、检查和回滚边界可重复审计。
- 不交付剑、扇子、怪物、Boss、最终战斗内容配置、奖励/资产/结算、ActivityInstance、Room、Party、匹配、完整客户端 GAS/ECS、正式公网发布或完整产品资格；这些仍属于 B0.8 或后续独立 change。

## Capabilities

### New Capabilities

- `client-battle-runtime`: 定义 Unity BattleTicket/安全 UDP-KCP lifecycle、输入预测与权威校正、远端插值、App/Scene owner、Actor/HUD/Camera 表现、恢复/teardown 和定向真实 Player 验收。

### Modified Capabilities

无。

## Impact

- **客户端代码与资产：**`client/Assets/App/Scripts/{Application,Infrastructure,Presentation,Core,Scenes}`、Input Actions、PersonalWorldScene、Actor/HUD/Camera Prefab、asmdef、owner registry 与 EditMode/PlayMode tests。
- **协议消费：**复用现有 HTTPS BattleTicket、`battle/v1` Protobuf、numeric message `3000-3007`、secure wire fixtures、`battle-network-profile-v2` 和 input acknowledgement；不新增 message ID、不改变 allowed channel，也不把 battle 消息回退到 WSS/TLS-TCP。
- **依赖与构建：**`versions.yaml`、Unity `manifest.json`/`packages-lock.json` 增加并锁定 `com.unity.cinemachine` 3.1.7；3.1.5 在 current Editor 因已移除 InstanceID API 产生 `CS0619`，故采用首个包含 EntityID 修复之后的 2026-07-29 最新稳定版；Windows x64 client transport 复用已锁定 KCP/libsodium primitives，通过窄 adapter 隔离第三方类型并提供完整 rollback。
- **运行系统：**`AppComposition`、AppLifetime、WorldAdmission/Recovery、SceneLifetime、MainThreadDispatcher、ClientUiRouter 与真实 Go/C++ battle endpoint；服务端 owner、MySQL/Redis schema 和公开部署端口语义不变。
- **安全与回滚：**raw BattleTicket/proof/key 不进入 Application snapshot、View State、日志或 evidence；回滚到 B0.6 已归档的可运行提交时停止 Unity battle ticket 请求并移除 battle App/Scene feature，保留已占用协议 ID 与服务端 secure transport。
