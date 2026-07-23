## ADDED Requirements

### Requirement: Unity 必须使用独立 gameplay replica 与预测边界
App/Scene Scope MUST 通过纯 C# gameplay owner 保存网络 entity identity、本地输入历史、预测状态、权威 snapshot、插值状态和 GameplayCue；该 owner MUST 与 GameObject/Animator/View 分离，并通过 BattleNetworkClient 的有界 generation/session epoch 提交更新。Unity MUST NOT 运行服务器权威 AI、最终伤害、奖励或完整 GAS，也不得把 View Transform 作为网络事实。现有 Session、PersonalWorld、VisitSession 与 UI owner MUST 保持唯一。

#### Scenario: 本地角色收到权威校正
- **WHEN** gameplay owner 收到 current generation 的 snapshot 并判定本地预测超过容差
- **THEN** 它在纯 C# 状态中校正并重演未确认输入，Scene Actor View 只读取新的 presentation transform，Animator 或 Camera 不反向写入 snapshot

#### Scenario: 旧 UDP generation 的 snapshot 迟到
- **WHEN** reconnect 已建立新 battle generation，而旧 socket 的合法解密 snapshot 随后完成主线程投递
- **THEN** gameplay owner 通过 generation/session epoch 丢弃旧 snapshot，不覆盖新 world target、entity replica 或 Scene binding

### Requirement: 本地与远端 entity 必须使用不同表现收敛策略
本地玩家 Actor View MUST 跟随经过预测/校正的 presentation transform；远端玩家、怪物、Boss 与投射物 MUST 跟随插值或 profile 明确允许的有限外推 transform。Unity Physics MAY 用于表现查询和近似预测，但 MUST NOT 独立裁决服务器命中、AI 或最终位置。

#### Scenario: 远端玩家 snapshot 有网络抖动
- **WHEN** 两个有效 snapshot 的到达间隔发生 profile 允许的抖动
- **THEN** 远端 Actor View 在 interpolation buffer 上平滑呈现，不将每个原始 snapshot 直接跳变写入可见 Transform

#### Scenario: 本地预测碰撞与服务器不一致
- **WHEN** Unity 近似预测越过障碍而 C++ Jolt 权威状态未越过
- **THEN** 本地 presentation 在校正策略内收敛到服务器状态，客户端不生成权威穿越、命中或奖励

### Requirement: 战斗 UI 必须复用现有双 UI 与唯一 Router
菜单、背包、设置、邀请和其他页面型 UI MUST 继续由 UI Toolkit route/Host 承担；技能栏、准星、生命条、Boss 血条与世界空间标识 MUST 优先由 scene-bound uGUI Host 承担。战斗 UI MUST 只读取低敏 gameplay View State 和提交语义 intent，不得访问 socket、KCP、generated envelope、credential 或服务器 ECS。不得建立第二个全局 UI Router、UI Manager 或 active screen owner。

#### Scenario: gameplay Scene 卸载
- **WHEN** target generation 失效或 PersonalWorldScene 卸载
- **THEN** scene-bound HUD、世界血条、订阅和 cue Host 随 SceneLifetime 释放，App Scope 的 Session 和现有 UI Router 保持唯一且不保留已销毁 Unity 对象

### Requirement: Cinemachine 必须保持 Scene Scope 镜头表现工具
客户端 MAY 引入 Cinemachine，但 MUST 通过唯一 Scene Scope `CinemachineCameraHost` 消费 `Exploration`、`MeleeCombat`、`RangedAim`、`Cinematic` 等封闭 `CameraIntent` 和 presentation target。Cinemachine、Camera transform、镜头碰撞、blend 与 impulse MUST NOT 决定合法锁定目标、服务器 aim、Ability 激活、命中或 gameplay state；Camera Host MUST NOT 访问 battle socket、Session credential 或服务器协议 owner。

#### Scenario: 扇子进入远程瞄准
- **WHEN** gameplay/presentation 状态提交 current generation 的 `RangedAim` CameraIntent
- **THEN** Camera Host 切换登记镜头配置并跟随预测 presentation target，而攻击 aim intent 仍由 Input/gameplay 边界生成并由服务器裁决

#### Scenario: Boss 重击触发镜头震动
- **WHEN** current gameplay cue 请求登记的 camera impulse
- **THEN** Camera Host 只播放有限表现效果，震动后的 Camera transform 不改变玩家权威朝向、位置或命中查询
