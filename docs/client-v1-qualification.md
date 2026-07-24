# 客户端 v1 资格验收

本文档定义 C3 的唯一资格结论、冻结输入、自动证据、真实双 Player 证据与清理边界。实现完成、局部测试绿色或人工口头确认均不等于 `client-v1 qualified`；只有同一 contract/build digest 下全部 mandatory 场景通过，报告才可以给出该结论。

## 资格输入与 owner

- 场景契约：`shared/contracts/fixtures/client-qualification/manifest.json`
- manifest 闭合 schema：`manifest.schema.json`
- operator/自动证据闭合 schema：`evidence.schema.json`
- 人工清单闭合 schema：`manual-checklist.schema.json`
- 最终机器报告闭合 schema：`report.schema.json`
- 自动 runner 双向登记：`automatic-registry.json`
- 定向开发场景登记：`diagnostic-registry.json`
- 定向开发清单 schema：`diagnostic-checklist.schema.json`
- 唯一入口：`tools/client-qualification/client-qualification.ps1`
- 工具失败回归：`tools/client-qualification/client-qualification.tests.ps1`
- 机器输出：被忽略的 `.local/client-qualification/<run-id>/`
- 开发诊断输出：被忽略的 `.local/client-diagnostics/<run-id>/`
- 真实换服恢复诊断：`tools/client-qualification/server-restart-recovery.ps1`
- 换服恢复输出：被忽略的 `.local/client-recovery-diagnostics/<run-id>/`

manifest 只保存稳定 scenario ID、group、execution、mandatory、precondition、budget、expected outcome、build profile 与 evidence owner。证据只保存 scenario ID、pass/fail、证据类型、UTC 时间和冻结 digest；账号、密码、token、Player/World/Visit identity、endpoint、PID、绝对路径、截图路径和原始日志不得写入 manifest、evidence 或 report。

## 定向开发诊断

缺陷修复期间使用 `diagnose` 缩短反馈周期，不要反复执行完整资格链。例如 Owner gameplay 恢复场景：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  .\tools\client-qualification\client-qualification.ps1 `
  -Action diagnose `
  -Scenario owner-gameplay-recovery `
  -UnityEditorPath 'D:\Unity\Editor\6000.5.2f1\Editor\Unity.exe'
```

支持的稳定场景由 `diagnostic-registry.json` 闭合登记，包括 `owner-gameplay-recovery`、`visitor-gameplay-reconnect`、`control-recovery`、`session-restore` 与 `ui-scene-lifecycle`。入口复用automatic registry选择相关fixture，定向运行所需EditMode/PlayMode，随后只构建Development Player。输出目录中的`launch-two-players.ps1`默认使用隔离profile启动两个客户端、记录由自身创建且绑定启动时间的PID，并把Owner与Visitor日志分别固定到`player-logs/owner.log`和`player-logs/visitor.log`，避免Unity默认`Player.log`被两个进程竞争覆盖；profile只隔离secure-session存储，不能自动选择账号，操作者必须分别登录Owner与Visitor并在继续前确认两者OwnWorld ID不同。`-LaunchRole owner|visitor`只启动并替换指定profile的PID与日志记录，用于进程重启恢复而不复制另一角色；`-ShowStepsOnly`只重放步骤而不重复启动Player；`-ShowConnections`只消费该PID记录并读取操作系统当前8080/8444连接，不依赖WMI进程枚举，同时打印目标角色的独立日志路径。它输出TCPView应关闭的PID、LocalPort、RemotePort以及必须保留的端口。8080由HTTP与WSS复用，恢复期间可以短暂出现HTTP连接；目标连接不唯一时入口必须失败并要求重新查询，不能猜测control socket。`diagnostic-checklist.json`保存与当前构建绑定的步骤和可选连接指引。

诊断清单固定声明`qualificationEvidence=false`，不会生成`automatic-evidence.json`或正式`report.json`，不能交给`finalize`。它只证明当前问题具备进入人工复测的条件；代码冻结后仍必须从`automatic`开始完成同一digest上的正式资格链。

### 一键真实 Player 换服恢复诊断

`server-restart-recovery.ps1` 用真实 Windows Development Player 和生产 `AppBootstrap -> AppComposition -> AppRoot` 对象图执行一次完整服务端进程替换。工具精确停止当前由指定服务端可执行文件持有的 8080/8081/8444 listener，使用同一 `StorageRunId` 启动初始服务端，登录并开放 VisitSession，实际终止服务端，等待 Player 提交 `ConnectionLost`，启动 replacement server，再调用产品“重新连接”入口。通过条件是 Player 权威收敛到 OwnWorld、旧 VisitSession 与断线 route 已退役、资源计数回到基线、Player 正常退出，且三个 listener 最终由同一个 replacement server PID 持有。

账号密码只允许通过当前父进程环境传入，Player 读取后立即清除；命令行、日志和诊断目录不得保存 credential。示例：

```powershell
$env:IHOMELAND_QUALIFICATION_USERNAME = '<测试账号>'
$env:IHOMELAND_QUALIFICATION_PASSWORD = '<测试密码>'

powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  .\tools\client-qualification\server-restart-recovery.ps1 `
  -StorageRunId '<storage-run-id>' `
  -PlayerPath '<Development Player绝对或仓库相对路径>'

Remove-Item Env:IHOMELAND_QUALIFICATION_USERNAME -ErrorAction SilentlyContinue
Remove-Item Env:IHOMELAND_QUALIFICATION_PASSWORD -ErrorAction SilentlyContinue
```

工具默认保留 replacement server 供后续诊断；传入 `-StopReplacementServer` 才在成功后停止它。它只终止精确 listener owner 和自己启动的 Player/server，不按进程名批量结束，也不创建或删除 storage run。该入口用于缺陷回归，不能替代 manifest 中要求的正式双 Player operator 证据。

## 执行顺序

Windows PowerShell 5.1 必须显式使用 `ExecutionPolicy Bypass`。`UnityEditorPath` 必须是 `client/ProjectSettings/ProjectVersion.txt` 锁定版本的绝对 `Unity.exe` 路径。

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  .\tools\client-qualification\client-qualification.ps1 `
  -Action validate

powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  .\tools\client-qualification\client-qualification.ps1 `
  -Action automatic `
  -UnityEditorPath 'D:\Unity\Editor\6000.5.2f1\Editor\Unity.exe'

$env:IHOMELAND_QUALIFICATION_USERNAME = '<仅当前进程使用的测试账号>'
$env:IHOMELAND_QUALIFICATION_PASSWORD = '<仅当前进程使用的测试密码>'
powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  .\tools\client-qualification\client-qualification.ps1 `
  -Action soak -RunId '<automatic输出的run-id>'
Remove-Item Env:IHOMELAND_QUALIFICATION_USERNAME
Remove-Item Env:IHOMELAND_QUALIFICATION_PASSWORD
```

`automatic` 固定执行 protocol clean generation/parity、全部 EditMode、全部 PlayMode、Development build、Release build、Release surface scan、两种真实 Player 启动 smoke 与资格工具失败回归。Smoke 必须从 Player.log 观察到固定低敏 `[IHOMELAND_APP] state=running`，不能只凭进程存活通过；资格入口随后只终止自己启动的精确 Player。任一测试 missing、failed、inconclusive、skipped、Player 提前退出或超时都失败。`soak` 继续同一 run，账号密码只由父进程环境传入Development Player内存，不进入命令行、manifest、evidence、report或低敏结论。脱敏通过后会重新执行治理门并按 manifest 前置关系登记证据；两个动作都保持 `not-qualified`，不会伪造人工结果。

Development Player的secure session根固定在当前ignored run目录。Release按产品规格不能解析资格profile或存储根，因此Release smoke只允许在当前Windows用户的产品default secure record不存在时运行；发现既有record会稳定失败，操作者必须改用干净Windows用户，资格工具不会读取、轮换或删除日常客户端lineage。

继续同一 run 时必须显式传入该 run 的 32 位小写十六进制 `RunId`：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  .\tools\client-qualification\client-qualification.ps1 `
  -Action prepare-manual -RunId '<run-id>'
```

`prepare-manual` 只有在同一 run 的五分钟 soak 证据已经通过后才生成 `manual-checklist.json` 与 `evidence.json`。清单只保存固定步骤代码、`qualification-owner`/`qualification-visitor` 隔离 profile 和相对 artifact 名；账号、密码与运行 identity 仍只存在于 operator 运行环境。Operator 可以是人，也可以是受控自动化代理，但必须真实启动本次 Development Player、两个隔离 profile 和独立服务端/storage，按低敏进程间信号观察完成条件，并且只终止自己持有的精确 PID；不得直接调用测试替身、伪造信号或仅根据进程存活追加记录。完成三项 operator 场景后，在该 run 的 `evidence.json` 中只追加对应记录，再执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  .\tools\client-qualification\client-qualification.ps1 `
  -Action finalize -RunId '<run-id>' -EvidencePath '<evidence.json>'
```

`finalize` 会重新计算当前 tracked contract 与两种 Player 目录 digest，拒绝旧 build、未知/重复 scenario、`skipped`、missing mandatory、failed record 和 contract 漂移。
Evidence loader还要求每个已登记scenario的全部precondition已经通过，且前置记录的UTC完成时间不晚于当前记录；只凑齐ID但顺序错误的文件同样失败。

## Development 与 Release

`ClientDevelopmentBuild` 是当前唯一 Windows build owner；名称为历史兼容保留，Development 与 Release 均进入同一个实现，使用相同启用场景、product identity、输出规则和结果校验。Release 编译时不包含 `-ihomelandDataProfile` 解析分支；资格入口还会扫描 Release `.exe/.dll`，拒绝资格 profile、故障注入标记和测试账号词汇。

Windows secure Session 使用 DPAPI `CurrentUser`、environment entropy、owner-specific 文件、同目录 flush/atomic replace、受控 ACL 与 production profile named mutex。只有 Editor 或 Development Player 的 Local/Test 环境可以使用隔离 data profile；Release 和 Production 始终使用 `default`。非 Windows 平台明确返回 Unsupported，不降级到 PlayerPrefs、明文或自制加密。

## 恢复自动矩阵

EditMode 必须覆盖：

- secure record 读写、替换、删除、损坏、oversize、DPAPI failure、第二 writer 和停止；
- login/refresh 持久化提交顺序、single-flight、commit-unknown、旧 lineage 与迟到 completion；
- WSS generation health、control-only projection 失效、snapshot reconciliation 与 gameplay 不重建；
- OwnWorld descriptor 恢复、Visitor `RECONNECT` 唯一首帧、identity/revision/assignment gate 与 safe-return；
- automatic/manual single-flight、deadline、session/intent/target/scene 四重 gate、shutdown 与三轮资源守恒；
- UI action matrix、route cancellation、UI reload、stale callback、backpressure 与低敏文案。

PlayMode 必须覆盖真实 UI Toolkit/uGUI Host、focus/cursor、Scene replacement、HUD/modal 所有权与 teardown。生产代码不得使用 frame tick、真实 sleep、任意延时或页面缓存去修正权威状态。

## 真实双 Player operator 场景

Operator 证据必须使用本次 Development build、两个隔离 data profile、独立服务端/storage run 与至少两个真实账号。账号与 runtime identity 只在运行环境中存在，不写入 evidence。后台或隐藏窗口不降低证据等级：判断依据仍是两个真实 Player 的产品对象图、真实 socket、服务端持久事实、固定低敏 pass/fail marker 和精确进程退出状态。

1. 两端从 clean client state 登录并进入各自 OwnWorld。
2. Owner Open、create invite；Visitor 接受并以 Visitor role 进入 Owner target。
3. 分别验证 leave、kick、close、撤销、重新邀请与 Owner/Visitor command 权限。
4. 单独中断 WSS：健康 gameplay/Scene 保留，邀请动作冻结，完整 snapshot 后恢复。
5. 单独中断 Owner gameplay：旧 Scene/input 立即失效，新 assignment/Scene 提交后恢复。
6. 单独中断 Visitor gameplay：grace 内首帧必须为 RECONNECT，并恢复同一 VisitSession/member；权威拒绝则安全返回 OwnWorld。
7. 分别关闭并重启 Owner/Visitor Player，验证 secure refresh restore、不要求重新输入密码、不恢复旧 socket/callback。
8. 使用刚完成登录或refresh且access尚未到期的两端，在访问过程中停止服务端，再以保留 MySQL/Redis 的新进程启动；旧 admission/assignment 不可复用，无法恢复的访问明确结束。该场景必须在本地15分钟access TTL内完成；若停服期间access到期且refresh返回commit-unknown，Session按安全规格进入`Unresolved`并回到Login，应作为第9项失效收敛记录，重新登录后再执行本项，不能误判为服务端保留存储恢复失败。
9. 使 Session 失效，验证两个 channel、target、Scene 与 modal 收敛到 Login，旧 generation 不能回写。

每一步都要核对 role、target、revision、Visitors/invites、route、Scene 与可用 action。界面暂时保留的 Visitor 必须明确标注权威 online/reconnecting 状态；不能把恢复窗口误写成在线。

## 五分钟 soak

Soak 最少持续五分钟，跨越多个 control/gameplay heartbeat 周期，并执行至少三轮 channel disconnect/recovery、route open/close 与 Scene replacement。Development-only驱动使用正常登录和真实产品graph，只通过通道owner的显式qualification seam提交transport故障；恢复仍走同一生产状态机。每轮比较 AppRoot、control/gameplay generation、heartbeat owner、recovery intent、pending、dispatcher、subscription、socket/task 与 Scene owner 基线；功能成功但资源计数增长仍判失败。

Soak 不是 31 分钟 idle、性能排名或容量测试。`five-minute-recovery-soak` 只有在Development Player自然以0退出且Player.log包含固定低敏pass marker时才产生证据；缺少凭据、server、marker或任一资源基线均失败。

## 清理与报告

资格入口只持有自己通过 `Start-Process -PassThru` 创建的精确 Process 对象；成功、失败、超时与 Ctrl+C 后只终止这些 PID。禁止按进程名清理，禁止递归删除不经验证的目录。storage 必须由其 run-id owner 清理。

报告保留首个稳定失败阶段，并独立记录 cleanup 结果；cleanup failure 不能覆盖主失败，也不能被忽略。原始 Unity/Player 日志只留在当前 ignored run 目录，最终 report 只含 schema version、冻结 digest、公开工具版本、阶段 outcome/耗时、证据记录和 cleanup 结论，不含 run-id、PID、路径或业务 identity。

## 当前资格状态

`establish-go-simulation-control` 接入 production C++ child 后，`client-v1` 已在
2026-07-24 以当前源码重新完成同一冻结输入下的全部 20 项 mandatory 资格：
contract digest 为
`541c50c6f24f8da6bd4878d328c86f5e9eff631d4e3786eca57415fb30d26ec5`，
Development build digest 为
`badaaa358119447b9f92368ea1163b90f4378fd0e7fdb49d880e15ee7d6db21e`，
Release build digest 为
`d1e8b6bb750acf486a6999d8ad1f91c73d52c1e793489d64dc9273e8e6419336`。
自动门、五分钟真实 Player soak、双 Player 产品/分通道故障、Owner/Visitor 进程恢复、
Session 失效、真实服务端停止与同库重启、redaction、governance 和 cleanup 均通过，
最终报告得到 `qualified=true`、`cleanup=pass`。因此当前冻结输入的结论是
**client-v1 qualified**；任一 contract 或 Player build digest 变化都会使该结论失效并
要求重新运行完整资格链。
