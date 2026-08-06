# Client Battle Runtime Runbook

## 目的与授权边界

本 Runbook 只覆盖 `implement-unity-gameplay-runtime` 的定向开发、依赖恢复、Unity 接线、诊断和清理。普通开发检查不得自动升级为完整 battle network matrix、连续两次 verify、30 分钟 soak 或 finalize。只有使用者显式冻结 current clean HEAD 后，才能调用最终资格。

服务端基础 movement 前置已闭合：C++ runtime 从 validated move/aim/jump 推进每
instance 的权威 movement state，snapshot 在同一 committed Tick 发布全部 actor
position、yaw、velocity、bit 4 grounded 与 acknowledgement。Current physics 只实现
Y=0 有界平地，不能替代正式地图碰撞。Unity Input、Scene binding、EditMode/PlayMode、
Windows Player 定向入口，以及单人和 Owner/Visitor 代表性人工体验已于 2026-08-03
完成；该结论只关闭 B0.7 开发验收，不等价于完整 battle network 或产品 combat 最终资格。

Unity local prediction保持25 ms输入采样但只按50 ms `SimulationTick`积分；同组第二个
InputTick只能从组起点重算，不能追加第二次位移。手动验收若观察到角色或相机按snapshot
周期前后拉回、移动中落地横向跳变或jump edge在视觉落地后无响应，应视为失败，不能通过
增大Scene smoothing或Cinemachine damping掩盖。连续ACK缺口可以暂时保留已被服务器
消费的InputTick用于重发，但映射到`latest ServerTick`及更早`SimulationTick`的frame
不得从current authority state再次积分；只有authority horizon之后的组可以重演。

Editor/Development手动体验期间，render/prediction telemetry 只在内存中做有界聚合，
不得周期性写 Console 或制造额外主线程尖峰；Scene 解绑时至多输出一次低敏摘要。
`target_rebase_mm`是本地表现target在一次reconciliation前后的实际改变量，持续移动且
网络稳定时应长期接近0；`max_target_rebase_mm`记录current battle generation内峰值。
`prediction_lead_x_mm/y_mm/z_mm`是local horizon相对authority的位置差，可以非零，
不应误读为校正抖动。按Space后`last_jump_input_tick`与
`last_jump_simulation_tick`必须推进，`jump_predicted=True`且
`predicted_grounded`应先变为False。首个baseline最多等待5秒，超时后必须进入
battle-only recovery，不能永久停在同步状态。摘要不含账号、PlayerID、endpoint、
ticket、payload或credential，Release不编译。

同一台开发机同时运行两个未限帧的 Development Player、服务端、Unity Editor或录屏软件
时，Windows 调度、GPU合成与后台渲染竞争可能造成肉眼可见的帧时间停顿。这种环境适合
验证双角色连接、可见性、移动、跳跃和恢复语义，但不能单独归因网络回拉，也不作为商业
手感基准。主观平滑度应优先用单个独立 Player关闭录屏复测；需要双人手感结论时使用两台
机器或受控帧率与图形配置，并结合内存摘要区分render frame spike和authority correction。

恢复期HUD使用Basic Latin基础字体可显示的闭合文案：`Connection lost.
Reconnecting...`表示输入已关闭且画面/HP为`last known`；`Connection restored.
Loading latest state...`表示successor已认证但尚未提交baseline；`Battle ready`才表示
当前角色、生命值与输入来自current generation。`Connection failed. Re-enter the
world.`或`Session expired. Sign in again.`是终态玩家动作。不得把`Protocol`、
`Transport`、generation或永久`Synchronizing world / HP --`直接暴露为产品状态。

## 冻结环境

- 仓库根：运行命令时的 current iHomeland checkout。
- Unity Editor：`6000.5.2f1`。
- Cinemachine：`com.unity.cinemachine` `3.1.7`。
- Native ABI：`ihomeland-client-battle-native-v1`。
- Windows native platform：`x86_64`。
- Native dependencies：项目锁定的 libsodium `1.0.22` 与 KCP `2.1.1`。
- Profile/wire：`battle-network-profile-v2`、`battle-wire-v1`、routes `3000-3007`。

不得使用系统/NuGet native fallback、用户级 C++ cache、PATH 上的同名 DLL 或 tracked generated plugin。

## Native 恢复与构建

首次在本机恢复项目锁定依赖：

```powershell
& .\tools\cpp\cpp.ps1 restore
```

验证 B0.7 进入身份，不启动 Unity：

```powershell
& .\tools\client-battle-runtime\client-battle-runtime.ps1 -Action validate
& .\tools\client-battle-runtime\client-battle-runtime.tests.ps1
```

重建 Windows x64 Release plugin：

```powershell
& .\tools\client-battle-runtime\client-battle-runtime.ps1 `
  -Action native-build `
  -NativePreset windows-msvc-release
```

入口会执行 exact CMake configure/build、`client.battle-native.abi` CTest，并把 DLL 复制到被 Git 忽略的：

```text
client/Assets/App/Generated/Plugins/x86_64/ihomeland_client_battle_native.dll
```

禁止手工复制其他来源的 DLL。若 restore、checksum、license、C ABI 或 link gate 失败，先修复源事实，不得临时换库。

## Windows Player 与真实 Battle Harness

关闭所有正在打开 `client/` 的 Unity Editor 后，可单独执行完整定向 Player 门：

```powershell
$env:IHOMELAND_UNITY_EDITOR = 'D:\Unity\Editor\6000.5.2f1\Editor\Unity.exe'
& .\tools\client-battle-runtime\client-battle-runtime.ps1 `
  -Action player-targeted `
  -NativePreset windows-msvc-ci
```

该入口按顺序完成：

1. 重建 native plugin并验证连续 digest、C ABI 与真实 C++ child contract；
2. 用唯一 `ClientDevelopmentBuild` 分别构建 Windows Development/Release Player；
3. 核对两种 Player 中只有一个与 current producer 同 digest 的 native DLL；
4. 启动并精确停止两种 Player smoke，拒绝 Release 包含任何 Development资格入口；
5. 为当前 run 分配独立 MySQL/Redis、动态 HTTP/WSS/TCP/UDP endpoint和两个一次性账号；
6. 启动一个精确 Go parent，按 `ParentProcessId` 捕获它唯一的 C++ simulation child，再启动两个隔离 profile 的 Development Player；
7. 验证Owner/Visitor ticket与8 actor hard cap内的active actor identity、move/jump/aim acknowledgement、本地预测收敛、远端插值；连续运行210秒后注入battle-only断线，确认稳定actor slot、predecessor撤销、successor full baseline、Actor/HUD/Input恢复，再验证assignment replacement/safe-return、旧代拒绝及logout后零Scene/socket/pump/native lease；
8. 扫描日志和run-local文本产物，拒绝credential、storage password、world/battle derivation key泄漏，并把任一PID、listener或storage清理失败判为整个run失败。

成功结论包含：

```text
CLIENT_QUALIFICATION_LOCAL_BATTLE_PASS run-id=<run-id>
[PASS] client battle runtime Windows Players and real process harness passed: run-id=<run-id>
```

构建、Player log与低敏排障信息只位于 ignored
`.local/client-battle-runtime-player/<run-id>/`；携带临时 PlayerID 的协调目录无论成功失败都会删除。

## Unity Package 与源码刷新

以下操作必须由使用者在 Unity `6000.5.2f1` 中完成：

1. 关闭其他打开同一 `client/` 项目的 Unity 进程。
2. 用 Unity Hub 或精确 `Unity.exe` 打开 `client/`。
3. 等待 Package Manager 完成 `com.unity.cinemachine` `3.1.7` resolve。
4. 确认 `client/Packages/packages-lock.json` 生成 exact 3.1.7 锁定，且没有 preview、Git URL 或 indirect drift。
5. 等待脚本刷新完成，确认 Console 没有 compile error。
6. 由 Unity 自动生成本 change 新增脚本所需 `.meta`；不要手工编造 GUID。
7. 检查 `IHomeland.Client.Runtime`、Application、Infrastructure 与 EditModeTests asmdef 没有循环引用或 missing script。

Unity 生成的 `.csproj` 不是源事实，不手工提交修改。

## Input Actions 接线

打开 `Assets/App/Modules/Core/Content/Input/InputSystem_Actions.inputactions`，保留唯一 `Player` 与 `UI` map owner。

在 `Player` map 中建立以下 exact actions：

| Action | Type | Expected Control Type | 建议现有绑定迁移 |
|---|---|---|---|
| `Move` | Value | Vector2 | 保留当前 WASD、左摇杆 |
| `Aim` | Value | Vector2 | 将现有 `Look` 重命名并保留鼠标 delta、右摇杆 |
| `Jump` | Button | Button | 保留 Space、Gamepad South |
| `Primary` | Button | Button | 将现有 `Attack` 重命名并保留左键/主攻击键 |
| `Secondary` | Button | Button | 新增右键与选定 Gamepad binding |
| `Interact` | Button | Button | 保留 E、Gamepad North |

Input System 1.19 Editor 在 `Action Type = Button` 时可能不显示独立 Control Type，
并把 `expectedControlType` 留空；这是由 Button Action Type 推导 control 的 current
Editor 表达，不需要删除并重建 Action。Move/Aim 仍必须显式显示 Vector2。

`Menu` 继续由 `ClientUiHostRoot` 拥有；不要创建第二个 PlayerInput、第二份 action asset clone 或 Scene-local action map owner。打开 UI Toolkit 页面/modal 时，既有 Router 必须切换到 UI map，battle input 自动停止。

## PersonalWorldScene 接线

在 `PersonalWorldScene` 的现有 `PersonalWorldSceneContext` 同一 Scene 下完成：

1. 创建一个 Scene root，例如 `BattlePresentationRoot`。
2. 在该 root 或其明确子对象上添加唯一 `ClientBattleSceneHost`。
3. 添加唯一 `ClientActorViewRegistry`，创建 `ActorRoot` 子节点并直接引用。
4. 创建 generic actor Prefab，至少包含可见 Renderer；不得包含 Session、socket、world snapshot 或业务 manager。把 Prefab 直接赋给 registry。
5. 添加唯一 `ClientBattleHudHost`，引用 Scene-bound CanvasGroup、状态 TMP_Text 与生命 TMP_Text。该 overlay 不创建第二个 EventSystem，也不切换 logical screen。
6. 添加唯一 `CinemachineCameraHost` 与 `FollowProxy`。
7. 把 Actor registry、HUD host、Camera host 直接赋给 `ClientBattleSceneHost`。
8. 把 `ClientBattleSceneHost` 直接赋给 `PersonalWorldSceneContext` 的 battle host 字段。
9. 保存 Scene，重新打开后确认引用仍完整且 Console 无 missing script。

Scene teardown 只释放 Input/Actor/HUD/Camera binding；不得在 `OnDestroy` 里直接关闭或重连 App Scope battle connection。

## Cinemachine 3.1.7 接线

在同一 Scene 创建四个互异 rig root：

- `ExplorationRig`
- `MeleeCombatRig`
- `RangedAimRig`
- `CinematicRig`

每个 root 按 Cinemachine 3.1.7 正式组件配置，并共同跟随 `FollowProxy`。将四个 root 直接赋给 `CinemachineCameraHost`。默认只启用 `ExplorationRig`；Host 会保证 intent 切换时恰好一个 root active。

当前generic capsule验收基线如下，避免2米近距与低肩点让角色占满视野，并避免0.5秒
垂直阻尼放大跳跃时的视觉拖拽：

| Rig | Damping | Shoulder Offset | Vertical Arm | Distance |
|---|---|---|---:|---:|
| `ExplorationRig` | `(0.06, 0.10, 0.08)` | `(0.45, 1.25, 0)` | `0.35` | `4.5` |
| `MeleeCombatRig` | `(0.05, 0.08, 0.06)` | `(0.40, 1.15, 0)` | `0.30` | `3.6` |
| `RangedAimRig` | `(0.05, 0.08, 0.06)` | `(0.65, 1.30, 0)` | `0.25` | `3.2` |
| `CinematicRig` | `(0.12, 0.15, 0.12)` | `(0, 1.40, 0)` | `0.30` | `5.0` |

3.1.5 虽被 Unity 6000.0 文档列为 released，但在 6000.5.2f1 中会因
`CinemachineStoryboard` 调用已移除的 `GetInstanceID()` 而产生 `CS0619`。
官方从 3.1.6 起把 InstanceID API 转换为 EntityID，本项目锁定 2026-07-29
最新非 preview 稳定版 3.1.7。不得修改 `Library/PackageCache` 源码规避该错误。

Camera 只消费 presentation target。不得从 Camera Transform、屏幕中心 raycast 或 Cinemachine target 推导服务器 aim、锁定、ability、hit 或 damage。Impulse 资产与正式 blend/priority 回归完成前，不勾选 OpenSpec task 8.4。

## Unity 定向测试

完成 Package、`.meta` 与 InputActions 刷新后，在 Unity Test Runner 先运行：

```text
IHomeland.Client.PersonalWorldCombat.Tests.EditMode.ClientBattleRuntimeTests
IHomeland.Client.PersonalWorldCombat.Tests.EditMode.ClientBattleInputContractTests
```

也可关闭交互式 Editor 后执行：

```powershell
$env:IHOMELAND_UNITY_EDITOR = 'D:\Unity\Editor\6000.5.2f1\Editor\Unity.exe'
& .\tools\client-battle-runtime\client-battle-runtime.ps1 -Action unity-tests
```

结果与 log 只进入 ignored `.local/client-battle-runtime-unity/<run-id>/`。该 action负责current Editor定向 EditMode与`PersonalWorldSceneContextPlayModeTests`；Development/Release build smoke和真实双Player场景由`player-targeted`负责。若项目正被另一个 Editor 打开，先关闭该 Editor，不得并发打开同一 Library。

## 定向质量门

只读预览：

```powershell
& .\tools\quality\quality.ps1 impact `
  -Change implement-unity-gameplay-runtime
```

Unity 接线完成后执行 current change 检查：

```powershell
$env:IHOMELAND_UNITY_EDITOR = 'D:\Unity\Editor\6000.5.2f1\Editor\Unity.exe'
& .\tools\quality\quality.ps1 check-change `
  -Change implement-unity-gameplay-runtime
```

该计划只引用 `validation.json` 与中央 catalog 登记的检查，其中
`client-battle-runtime-targeted`会执行上述Player门。Current change没有修改
`.proto`或message ID，因此不调用会重建Unity generated目录的`proto-verify`；
source manifest、field/route registry与Player实编译仍会校验current consumer。
不得把历史完整资格、soak 或 finalize 临时塞进 validation plan。

## 单场景网络诊断

真实 UDP 排障只选一个已登记 scenario：

```powershell
& .\tools\quality\quality.ps1 diagnose `
  -Scenario real-clean-default `
  -TimeoutSeconds 7200
```

诊断可使用已验证的 ignored binary cache，但每次 run 必须隔离 credential、endpoint、process、evidence 与 cleanup。诊断结果不是最终资格证据。

## Secret 与 Evidence

以下内容不得进入日志、exception、TestResult、截图、Scene、Prefab、PlayerPrefs、Git diff 或长期 evidence：

- BattleTicket 与 ticket secret
- 一次性用户名、密码与临时 PlayerID
- cookie、nonce、transcript proof
- traffic/rekey key
- binding fingerprint
- raw endpoint、raw datagram、credential-bearing HTTP body
- access/refresh token

允许记录稳定 component、operation、generation、outcome、reason、count、bytes、duration 与低基数 route/lane。失败时保留低敏 Unity log、TestResult XML、CMake/CTest 输出和精确 run-id，不复制原始 secret。

## Player 与进程清理

- 只终止当前 run 显式启动并记录的精确 Unity Player、Go parent与其唯一C++ child PID。
- 只删除当前 run 的 ignored profile、storage、endpoint 与 evidence 目录。
- 不清理操作者 default secure Session，不终止名称相同但不属于当前 run 的进程。
- 退出 Player 前先退役 Scene input/Actor/HUD/Camera，再停止 battle runtime、socket 与 native context。
- cleanup failure 必须作为该 run 的失败，不能被主场景通过掩盖。

## 最终资格

普通 change 完成、准备归档、网络边界变化或测试已接近通过，都不构成最终资格授权。只有使用者明确要求，且候选等于 current clean HEAD 时才运行：

```powershell
$candidate = git rev-parse HEAD
& .\tools\quality\quality.ps1 qualify -Candidate $candidate
```

该动作会执行完整 capability suites、连续 verify、长时 soak 与 finalize；不得从本 Runbook 的开发步骤自动推断执行。
