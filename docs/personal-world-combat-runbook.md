# PersonalWorld production combat 运行手册

## 适用范围

本手册只用于 `personal-world-combat-v1` 的开发、定向验证和回滚。它不授权完整产品资格、连续 verify、长时 soak 或 finalize，也不产生奖励/结算结论。

## Source 与 identity

- Package root：`shared/contracts/gameplay/battle/packages/personal-world-combat-v1/`
- Arena source：`simulation/content/personal-world-combat-v1/`
- Config identity：`d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b`
- Navigation identity：`14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f`
- Physics identity：`64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e`
- Mapping identity：`9e78652d1d05524c2a67668b208f703120a214bfb45f053c94f6c3dc79595e3b`
- Wire identity：`9a40facbb23aafc556d38b403c8f8b1e264e0f2d9414e32da434b11d07554432`

Go 必须在启动 child/listener 前选择完整 production package 并校验 identity。C++ child hello/ready 必须回报同一组 identity 与兼容 capacity；active instance 不读取变化中的 source。升级或回滚都使用完整 package、显式配置和更高 assignment generation，不替换 active timeline 内的单个文件。

## 只读 package 与 arena 检查

```powershell
pwsh -NoLogo -NoProfile -File tools/personal-world-combat/personal-world-combat.ps1 -Action validate
```

该动作同时验证 governance corpus 与显式 production root，不启动 Go、C++、storage 或 Unity。失败诊断只能输出稳定 reason 和公开 identity，不输出本机 package 绝对路径或完整文档。

Arena 的 `arena.json`、`navigation.json` 与 `physics.json` 分别由 map content、Detour 和 Jolt identity 绑定。Unity Scene 只复刻视觉布局；不得把 Scene、NavMesh、Collider 或 Transform 导出结果当作 authority source。

## Unity 内容制作

锁定 Editor 为 `6000.5.2f1`。Prefab、Scene、Animator、AnimationClip、VFX、Audio、Input Actions、ScriptableObject asset 和 `.meta` 必须由该 Editor 创建/保存；禁止手写或复制 GUID。已交付内容的逐项制作清单保存在 `openspec/changes/archive/2026-08-06-deliver-personal-world-combat-slice/unity-content-handoff.md`。

完成后设置：

```powershell
$env:IHOMELAND_UNITY_EDITOR = 'D:\Unity\Editor\6000.5.2f1\Editor\Unity.exe'
```

路径只保留在当前进程环境，不写入 tracked 配置、日志或 evidence。

## 定向检查历史与后续变更

`deliver-personal-world-combat-slice` 已于 2026-08-06 归档，其 closed plan 与运行证据保存在 `openspec/changes/archive/2026-08-06-deliver-personal-world-combat-slice/validation.json` 和归档 artifacts 中。归档前使用的入口为：

```powershell
pwsh -NoLogo -NoProfile -File tools/quality/quality.ps1 impact -Change deliver-personal-world-combat-slice
```

确认不存在 `final-product-qualification` 后执行：

```powershell
pwsh -NoLogo -NoProfile -File tools/quality/quality.ps1 check-change -Change deliver-personal-world-combat-slice
```

以上命令只记录归档前的可重复证据，不再作为归档后修改代码的当前 change 入口。后续任何功能、修复或重构必须建立新的 OpenSpec change，在新的 closed `validation.json` 中按影响面登记 checks，再通过 `impact` 与 `check-change` 执行。`personal-world-combat-targeted` 会复用登记 owner，验证 production package/proto、C++ encounter与真实 socket、Go selector/supervision、Unity EditMode/PlayMode、Windows Development/Release build，以及真实 Go parent、C++ child、MySQL、Redis 和双 Development Player。每次运行隔离 credential、endpoint、PID、Player storage、binary receipt、evidence 与 cleanup；不得消费旧诊断 cache credential 或旧 evidence。

## 双 Player 观察点

- Owner 先进入 OwnWorld；Visitor 接受邀请后，两端必须看到同一 3 个 ordinary monsters、1 个 Boss 与两个 active player。
- 两端分别观察 current weapon，提交 current primary、SwitchWeapon、另一 primary；weapon projection、各自 local actor 的 authority ability event 与 ability identity 必须推进，不能用 AI/对端 event 代替本地攻击证明。
- Boss health/max-health/phase/dead 只来自 full/delta；cue、Animator、VFX、Audio 和 Collider 不得回写。
- battle-only disconnect 建立 successor generation 与完整 content baseline；predecessor input/event/View/HUD/prediction 不得复活。
- Successor baseline 恢复后，两端以受限 move/primary intent 协作使用 fan projectile；必须共同观察 Boss health 单调下降、phase 推进和唯一 death，且 evidence 不包含 target、damage 或 settlement 声明。
- Visit close 或 safe-return 后，Owner 保持 own-world authority，Visitor 回到自己的 own-world；两端最终释放旧 socket、native lease、Scene owner 与 Player PID。

若只排查单个真实网络场景，使用 `quality.ps1 diagnose -Scenario <registered-id>`；不要直接拼接底层 gateway、credential 或 process 命令。Combat targeted evidence 只表示开发检查通过，不等同最终资格。

## Evidence 与清理

运行产物位于 ignored `.local/personal-world-combat/<run-id>/` 及其复用 owner 的精确 run 目录。Evidence 只保存公开 Config/Wire identity、closed scenario ID、完成时间与低敏结论；不得保存 PlayerID、账号、密码、ticket、endpoint、key、完整 package、raw payload 或 settlement 结论。

Runner 的 `finally`/owner cleanup 必须终止精确 PID、关闭 listener/socket、释放 Unity/Native owner并清除资格 profile。若 cleanup 失败，本次结果失败，不能复用该 run 继续验收；先按 owner 日志定位残留，再启动全新 run。

## 回滚

1. 恢复上一完整 production package root 与匹配 Config/Nav/Physics/Mapping/Wire identity。
2. 更新 Go deployment selector，启动新 C++ child；不得向 active instance 写入旧文件。
3. 通过更高 assignment generation 替换 instance，使旧 target/ticket/session fail closed。
4. Unity build 必须同时回滚到与旧 mapping 完整对应的 resource catalog 和资源集合；缺项时禁用 battle feature，而不是回退 generic authority。
5. 通过独立 OpenSpec change 声明回滚影响面并运行其 closed 定向检查；不得直接复用已归档 change 的 validation 结论。只有使用者明确要求冻结发布候选时，才能另外运行 `quality.ps1 qualify -Candidate <current-clean-HEAD>`。
