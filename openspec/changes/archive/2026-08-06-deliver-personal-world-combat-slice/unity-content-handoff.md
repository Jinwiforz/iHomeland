# Unity 战斗内容制作与接线交接

## 1. 边界与当前状态

本清单对应 tasks 9.1-9.7 的 Unity Editor 工作。Prefab、Scene、Animator、AnimationClip、VFX、Audio、Input Actions、ScriptableObject asset 及其 `.meta` 由使用者在锁定 Unity `6000.5.2f1` 中制作和保存；不得手写、复制或预测 GUID/`.meta`。

代码侧已经提供：

- `ClientCombatResourceCatalog`：16 项强类型资源引用、10 项 numeric mapping、runtime completeness 校验。
- `ClientCombatActorView`：Animator、weapon anchor、AudioSource、world-health 与 Boss phase 纯表现接线。
- `ClientActorViewRegistry`：按 archetype 创建/替换/despawn Prefab，切换武器 display，消费去重 cue，并在 generation replacement/teardown 清理 Audio/VFX。
- `ClientBattleHudHost`：local health/max-health、weapon、primary state、Boss health/max-health、phase/dead。
- `ClientDevelopmentBuild`：唯一 catalog、production JSON 双向 parity、Animator 参数/clip/event 与资源完整性 build gate。
- `tools/client-battle-runtime`：Input 双设备 binding、Unity 资产路径、`.meta`、catalog 与 Scene direct-reference gate。

`ClientCombatActorView.cs` 是新增脚本，第一次打开 Unity 后必须等待 Editor 自动生成其 `.meta` 并完成编译。

## 2. 唯一允许的 production Content 路径

OpenSpec design 与 build gate 冻结以下根目录：

```text
Assets/App/Modules/PersonalWorldCombat/Content/
  Animations/
  Audio/
  Config/
  Materials/
  Prefabs/
    Actors/
    Camera/
    Projectiles/
    VFX/
    Weapons/
  UI/
```

旧 `Assets/App/Animations/Battle`、`Audio/Battle`、`Config/Battle`、`Materials/Battle`、`Prefabs/Battle` 与 `UI/PersonalWorld/Prefabs/Battle` 已由锁定 Unity Editor 的 `AssetDatabase` 迁移或删除。既有资源现在位于：

```text
Assets/App/Modules/PersonalWorldCombat/Content/Materials/PersonalWorldReference.mat
Assets/App/Modules/PersonalWorldCombat/Content/Prefabs/Actors/GenericActor.prefab
```

不要重新创建旧 Battle 目录；后续所有战斗资产只放入本节冻结的功能模块路径。

## 3. 冻结 identity

| kind | semantic ID | numeric ID |
|---|---|---:|
| actor | `personal-world-combat/actor/player` | 1 |
| actor | `personal-world-combat/actor/ordinary-monster` | 2 |
| actor | `personal-world-combat/actor/boss` | 3 |
| weapon | `personal-world-combat/weapon/sword` | 101 |
| weapon | `personal-world-combat/weapon/fan` | 102 |
| ability | `personal-world-combat/ability/sword-primary` | 201 |
| ability | `personal-world-combat/ability/fan-primary` | 202 |
| ability | `personal-world-combat/ability/monster-strike` | 203 |
| ability | `personal-world-combat/ability/boss-slam` | 204 |
| projectile | `personal-world-combat/projectile/fan-blade` | 301 |

Map identity 必须为 `personal-world-combat/map/arena`。完整 logical key 以 `shared/contracts/gameplay/battle/packages/personal-world-combat-v1/presentation.json` 为准；numeric parity 以同目录 `wire-mapping.json` 为准。

## 4. 第一次打开 Unity

1. 使用 `D:\Unity\Editor\6000.5.2f1\Editor\Unity.exe` 打开 `G:\Jinwiforz\iHomeland\client`。
2. 等待 Asset Import 和 script compilation 完成。
3. 确认 Unity 自动生成：
   `Assets/App/Modules/PersonalWorldCombat/Runtime/Scenes/ClientCombatActorView.cs.meta`。
4. Console 必须没有 C# compile error。此时 catalog、Prefab 与 Scene 尚未制作造成的完整性失败不等于 compile error。
5. 下列模块目录已经由本次迁移通过 Unity 创建；不要改名、重复创建或移回旧根。只在这些既有目录内创建后续内容资产。

## 5. Input Actions 复核

`Assets/App/Modules/Core/Content/Input/InputSystem_Actions.inputactions` 的唯一 `Player` map 必须包含：

- `SwitchWeapon`：Action Type `Button`，Keyboard `<Keyboard>/q`，Group `Keyboard&Mouse`。
- `SwitchWeapon`：Gamepad `<Gamepad>/leftShoulder`，Group `Gamepad`。
- `Primary`：保留 `<Mouse>/leftButton` 或 Keyboard binding，并保留 `<Gamepad>/buttonWest`。

不要使用已被 `Menu` 占用的 Tab 或已被 `Secondary` 占用的 Right Shoulder。保存 Input Action asset，但不要生成 C# wrapper。

## 6. Animator 与 AnimationClip

创建四个互异 Controller：

```text
Animations/SwordPrimary.controller
Animations/FanPrimary.controller
Animations/MonsterStrike.controller
Animations/BossSlam.controller
```

每个 Controller 必须精确包含：

- `Dead`，类型 `Bool`。
- `Ability`，类型 `Trigger`。
- 至少三个互异状态所引用的 clip：`Idle`、`Ability`、`Dead`。
- `Idle` 为默认状态。
- `Any State -> Ability` 使用 `Ability` trigger。
- `Ability -> Idle` 使用 Exit Time。
- `Any State -> Dead` 使用 `Dead == true`，无返回状态。

每个 Controller 必须能解析到至少三个 AnimationClip。全部 clip 禁止 Animation Event，禁止把 Prefab 根 Transform 当作 authority root motion。Actor Prefab 上 Animator 的 `Apply Root Motion` 必须关闭。

建议 clips：

```text
PlayerIdle.anim
SwordPrimary.anim
FanPrimary.anim
PlayerDead.anim
MonsterIdle.anim
MonsterStrike.anim
MonsterDead.anim
BossIdle.anim
BossSlam.anim
BossDead.anim
```

## 7. 武器 Prefab

创建：

```text
Prefabs/Weapons/SwordDisplay.prefab
Prefabs/Weapons/FanDisplay.prefab
```

要求：

- 根 Transform 为 Position `0,0,0`、Rotation `0,0,0`、Scale `1,1,1`。
- Sword 与 Fan 必须是两个不同 Prefab asset。
- 可以使用项目内 primitive mesh 与 URP Material。
- 不得包含 Collider、Rigidbody、NavMeshAgent、PlayerInput 或 gameplay MonoBehaviour。
- Prefab 本地偏移需要以 actor `WeaponAnchor` 为原点制作。

## 8. Actor Prefab 与 `ClientCombatActorView`

创建：

```text
Prefabs/Actors/PlayerCombatView.prefab
Prefabs/Actors/OrdinaryMonsterCombatView.prefab
Prefabs/Actors/BossCombatView.prefab
```

三个 Prefab 必须互异，根节点各包含且只能包含一个 `ClientCombatActorView`。

### 8.1 通用层级

```text
ActorRoot
  VisualRoot
    Body
  WeaponAnchor              # 只有 Player 必需
  WorldHealthCanvas
    WorldHealthText
  PhaseAccent               # Boss 可用，其他可省略
  AudioSourceObject
```

`WorldHealthCanvas` 可以使用 World Space Canvas，但不得带交互 Button。建议移除或禁用 GraphicRaycaster，`WorldHealthText` 使用 TextMeshProUGUI，Raycast Target 关闭。

三个 Prefab 均不得包含 Collider、Rigidbody、NavMeshAgent、PlayerInput、Camera 或 AudioListener。

### 8.2 Player Inspector

Player 根节点添加 Animator：

- Controller：`SwordPrimary.controller`。
- Apply Root Motion：关闭。

Player 根节点添加 `ClientCombatActorView` 并接线：

| 字段 | 引用 |
|---|---|
| Animator | Player 根节点 Animator |
| Weapon Anchor | `WeaponAnchor` Transform |
| Audio Source | `AudioSourceObject` 的 AudioSource |
| World Health Text | `WorldHealthText` TMP_Text |
| Phase Accent | None |

AudioSource：Play On Awake 关闭、Loop 关闭、Doppler Level 0、Spatial Blend 建议 0.65、Min Distance 1、Max Distance 25。

不要把 Sword/Fan 直接预放在 Player Prefab。Registry 会按 authority weapon state 在 `WeaponAnchor` 下创建唯一 display。

### 8.3 Ordinary Monster Inspector

- Animator Controller：`MonsterStrike.controller`。
- Apply Root Motion：关闭。
- `ClientCombatActorView.Animator`：Monster Animator。
- `Weapon Anchor`：None。
- `Audio Source`：Monster AudioSource，Play On Awake 关闭。
- `World Health Text`：Monster world-health TMP。
- `Phase Accent`：None。

### 8.4 Boss Inspector

- Animator Controller：`BossSlam.controller`。
- Apply Root Motion：关闭。
- `ClientCombatActorView.Animator`：Boss Animator。
- `Weapon Anchor`：None。
- `Audio Source`：Boss AudioSource，Play On Awake 关闭。
- `World Health Text`：Boss world-health TMP。
- `Phase Accent`：可选 Boss phase 强调子对象；phase > 1 且未死亡时自动显示。

## 9. Projectile Prefab

创建：

```text
Prefabs/Projectiles/FanBladeProjectileView.prefab
```

要求：

- 不添加 `ClientCombatActorView`。
- 不包含 Collider、Rigidbody、Trail collision、Particle collision、NavMeshAgent 或 authority script。
- Transform 与 lifecycle 只由 `ClientActorViewRegistry` 的 current projection 创建和销毁。

## 10. Damage VFX 与 Audio

创建：

```text
Prefabs/VFX/DamageImpactVfx.prefab
Audio/CombatCue.wav
```

Damage VFX：

- 至少一个 ParticleSystem。
- Play On Awake 关闭。
- Loop 关闭。
- Collision module 关闭。
- Trigger module 关闭。
- 不包含 Collider 或 Rigidbody。
- 建议 Duration 0.35 秒、Start Lifetime 0.2-0.35 秒。

Audio：

- 使用原创或授权明确的短 WAV。
- 建议 Force To Mono、Preload Audio Data、Decompress On Load。
- Actor AudioSource 只在 current reliable `Started` cue 上 `PlayOneShot`。

## 11. HUD Panel Prefab

创建：

```text
UI/PlayerStateHud.prefab
UI/BossStateHud.prefab
```

这两个 Prefab 只能是现有 Scene Canvas 下的 Panel，禁止包含 Canvas、EventSystem、Button 或独立 input owner。

Player 层级：

```text
PlayerStateHud
  HealthText
  WeaponText
  SkillText
```

Boss 层级：

```text
BossStateHud
  BossHealthText
  BossStateText
```

所有 TMP 文本 Raycast Target 关闭。

## 12. Cinemachine Rig Prefab

从 Scene 现有 `MeleeCombatRig` 和 `RangedAimRig` 创建：

```text
Prefabs/Camera/MeleeCombatRig.prefab
Prefabs/Camera/RangedAimRig.prefab
```

要求：

- 两个 Prefab 必须互异。
- Prefab 内不得包含 Unity Camera 或 AudioListener。
- Cinemachine component 可以保留。
- Prefab asset 不能引用 Scene `FollowProxy`；Scene instance 上继续把 Follow 指向现有 `FollowProxy`。
- 不新增第二套 Exploration/Cinematic rig。

## 13. 创建唯一 Catalog asset

在 Unity 菜单选择：

```text
Create > IHomeland > Combat > Client Combat Resource Catalog
```

必须保存为：

```text
Assets/App/Modules/PersonalWorldCombat/Content/Config/ClientCombatResourceCatalog.asset
```

Inspector identity 保持默认：

- Package ID：`personal-world-combat-v1`
- Map ID：`personal-world-combat/map/arena`

逐项接线：

| Catalog 字段 | Asset |
|---|---|
| Player Display | `PlayerCombatView.prefab` |
| Monster Display | `OrdinaryMonsterCombatView.prefab` |
| Boss Display | `BossCombatView.prefab` |
| Fan Projectile Display | `FanBladeProjectileView.prefab` |
| Sword Display | `SwordDisplay.prefab` |
| Fan Display | `FanDisplay.prefab` |
| Sword Animator | `SwordPrimary.controller` |
| Fan Animator | `FanPrimary.controller` |
| Monster Animator | `MonsterStrike.controller` |
| Boss Animator | `BossSlam.controller` |
| Damage Vfx | `DamageImpactVfx.prefab` |
| Combat Audio | `CombatCue.wav` |
| Player Hud | `PlayerStateHud.prefab` |
| Boss Hud | `BossStateHud.prefab` |
| Melee Camera | `MeleeCombatRig.prefab` |
| Ranged Camera | `RangedAimRig.prefab` |

所有字段必须非 None。只有这一份 catalog asset 可以存在。

## 14. PersonalWorldScene 接线

打开 `Assets/App/Modules/PersonalWorld/Content/Scenes/PersonalWorldScene.unity`。

### 14.1 Actor registry

选择现有 `ActorPresentation` / `ClientActorViewRegistry`：

- Resource Catalog：唯一 `ClientCombatResourceCatalog.asset`。
- Actor Prefab：暂时保留已迁移到 `Assets/App/Modules/PersonalWorldCombat/Content/Prefabs/Actors/GenericActor.prefab` 的现有引用，不删除。
- Actor Root：现有 `ActorRoot`。

Production runtime 只使用 Resource Catalog；GenericActor 只保留旧程序化 fixture/迁移兼容。

### 14.2 Battle HUD

把 `PlayerStateHud.prefab` 与 `BossStateHud.prefab` 实例化到现有 `BattleHUD` Canvas 下。选择现有 `ClientBattleHudHost`：

| 字段 | Scene 引用 |
|---|---|
| Root | 现有 BattleHUD CanvasGroup |
| Status Text | 现有 StatusText |
| Health Text | PlayerStateHud/HealthText |
| Weapon Text | PlayerStateHud/WeaponText |
| Skill Text | PlayerStateHud/SkillText |
| Boss Health Text | BossStateHud/BossHealthText |
| Boss State Text | BossStateHud/BossStateText |

CanvasGroup 必须 `Interactable=false`、`Blocks Raycasts=false`。Scene 不新增 Canvas、EventSystem 或 Router。

### 14.3 Camera

确认 Scene 中仍只有：

- `SceneCamera` 一个 Camera/AudioListener。
- `ExplorationRig`。
- `MeleeCombatRig` 的 Prefab instance。
- `RangedAimRig` 的 Prefab instance。
- `CinematicRig`。
- `FollowProxy`。

选择 `CinemachineCameraHost`，确认四个 rig、FollowProxy 与 ImpulseSource 引用均非 None。Sword 自动选择 melee intent，Fan 自动选择 ranged intent；Camera 不提交 aim/hit authority。

### 14.4 Arena anchors

保留且只保留一个 `PersonalWorldReferenceEnvironment`。代码已把纯表现地面扩展为 60×60 米，并绘制与 server source 对齐的三个无 Collider blocker：

- center `(0,1,0)`，size `(5,2,2)`。
- center `(-9,1.5,6.5)`，size `(3,3,3)`。
- center `(9,1.5,-6.5)`，size `(3,3,3)`。

在 `ReferenceEnvironment` 下创建空对象 `Anchors`，再创建：

| Anchor | Position | Yaw |
|---|---|---:|
| OwnerSpawn | `(-12,0,-12)` | 45 |
| Visitor1Spawn | `(-10,0,-12)` | 45 |
| Visitor2Spawn | `(-12,0,-10)` | 45 |
| Visitor3Spawn | `(-10,0,-10)` | 45 |
| Monster1Spawn | `(6,0,4)` | -135 |
| Monster2Spawn | `(9,0,7)` | -135 |
| Monster3Spawn | `(4,0,9)` | -135 |
| BossSpawn | `(15,0,15)` | -135 |
| ArenaCenterAnchor | `(0,0,0)` | 0 |
| BossFocusAnchor | `(15,1.5,15)` | -135 |

Anchor 全部为空对象，不含 Collider、NavMeshAgent 或 spawn script；server arena 才是 authority source。

## 15. 唯一 owner 审计

PersonalWorldScene 中以下组件必须各恰好一个：

- `PersonalWorldSceneContext`
- `ClientBattleSceneHost`
- `ClientActorViewRegistry`
- `ClientBattleHudHost`
- `CinemachineCameraHost`
- Camera
- AudioListener

BootstrapScene 中继续只有一个 EventSystem、AppBootstrap、AppRoot 与 ClientUiHostRoot。PersonalWorldScene 不新增 EventSystem、Router、PlayerInput 或连接 owner。

## 16. 保存与验证

1. `File > Save` 和 `File > Save Project`。
2. 关闭并重新打开 PersonalWorldScene，确认全部 direct reference 仍非 None。
3. Console 零 compile error、零 missing script/material/font。
4. 确认所有新 asset 与目录都有 Unity Editor 生成的 `.meta`。
5. 运行 EditMode 与 PlayMode tests。
6. 设置：

```powershell
$env:IHOMELAND_UNITY_EDITOR = 'D:\Unity\Editor\6000.5.2f1\Editor\Unity.exe'
```

7. 运行：

```powershell
.\tools\quality\quality.ps1 impact -Change deliver-personal-world-combat-slice
.\tools\quality\quality.ps1 check-change -Change deliver-personal-world-combat-slice
```

`impact` 必须包含 `personal-world-combat-targeted` 且不包含 `final-product-qualification`。不得运行 `quality.ps1 qualify`；完整网络矩阵、连续 verify、soak 与 finalize 仍需使用者另行明确授权。
