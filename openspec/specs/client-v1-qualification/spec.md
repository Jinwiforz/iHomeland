# Client V1 Qualification 规格

## Purpose

定义第一业务里程碑 Unity 客户端 v1 的资格清单、确定性构建、自动化与真实双 Player 故障矩阵、有限 soak、低敏证据和唯一合格结论边界。

## Requirements

### Requirement: 客户端资格 manifest 与证据必须完整对应

仓库 MUST 维护 schema-versioned 客户端 v1 qualification manifest。每个场景 MUST 登记唯一稳定 ID、capability group、mandatory、execution kind、前置条件、总预算、预期稳定 outcome、所需 build profile 与证据 owner；manifest MUST NOT 保存账号密码、token、Player/World/Visit identity、本机绝对路径、端口或原始日志。资格入口 MUST 双向验证 manifest、自动 runner 与人工证据记录：不得存在未实现 mandatory 场景、隐藏 runner、重复 ID、过期 build digest、未知 outcome 或以 skipped 计为通过的场景。

#### Scenario: Manifest 场景没有证据 owner

- **WHEN** mandatory 场景没有对应自动 runner，且没有绑定同一 build digest 的受控人工证据记录
- **THEN** completeness gate 失败，最终报告不得声明 qualified

#### Scenario: 使用旧 Player 的人工结果

- **WHEN** 双客户端记录引用的 contract 或 Player build digest 与本次资格输入不一致
- **THEN** 证据被判定 stale，场景必须在当前构建上重新执行

### Requirement: Clean 输入必须可确定性恢复并构建两种 Windows Player

资格入口 MUST 从不含 `Library`、`Temp`、`UserSettings`、generated C# 和构建输出的客户端源输入开始，先通过统一入口恢复锁定 packages 与 protocol generation，再验证第二次 generation 无差异。它 MUST 执行静态编译、全部 EditMode/PlayMode tests、protocol parity，并分别构建 Windows Development 与 Release Player；两个 Player MUST 使用同一冻结 contract digest、scene list、product identity 和 production object graph，Release MUST NOT 包含测试账号、测试密钥、故障注入入口或 Development-only bypass。

#### Scenario: 只有 Development build 成功

- **WHEN** Development Player 与 Unity tests 通过，但 Release Player 未构建、启动失败或 contract digest 不同
- **THEN** build gate 失败，不能用 Development 结果替代 Release 资格

#### Scenario: Clean generation 产生 tracked projection

- **WHEN** protocol 或 Unity 恢复在 Git 工作区生成应忽略的 C#、descriptor、cache、build 或临时 `.meta`
- **THEN** governance gate 失败并报告稳定文件类别，不把生成物加入资格提交

### Requirement: 自动化矩阵必须验证状态所有权与故障收敛

Mandatory automated scenarios MUST 覆盖 secure session store 的原子写入/读取/替换/删除/损坏，refresh single-flight 与 commit-unknown，WSS/TCP 独立断开和恢复，低/重复/冲突 revision，assignment generation replacement，stale callback，重复 intent，Scene/UI generation、input/focus、shutdown、backpressure 与 credential redaction。测试 MUST 使用可控 clock、transport、platform store 和 lifecycle seams 驱动事件，不得用 frame tick、真实 sleep 或修改 production deadline 来猜测状态。

#### Scenario: 旧恢复完成晚于新 session

- **WHEN** 测试控制旧 persisted refresh、玩家新登录和迟到恢复 callback 的提交顺序
- **THEN** 只有 current session/presentation/target generation 可以提交，旧 lineage 被删除且不能打开旧 route、Scene 或 channel

#### Scenario: WSS 与 gameplay 分别失败

- **WHEN** 测试独立终止一个 channel，并保持另一个 channel 健康
- **THEN** 只有受影响的 capability 进入恢复边界，健康 owner 不被重复创建，最终状态只能是权威恢复成功或稳定 terminal failure

### Requirement: 真实双 Player 矩阵必须覆盖完整产品与服务端故障

Mandatory Player scenarios MUST 使用本次 Development build、独立进程的本地服务端和至少两个真实账号，从 clean local client state 完成登录、OwnWorld、邀请、Visitor JOIN、leave、kick、close 与重新邀请；随后分别覆盖 Owner/Visitor Player 退出重启、单通道网络中断、服务端停止/重启、session 失效与持久 refresh restore。每个步骤 MUST 断言当前 role、target、revision、Visitors/invites、route、Scene 与可用 action 一致；服务端恢复后不得残留旧 socket、member、invite、modal 或 Scene callback。

#### Scenario: Visitor gameplay 中断后仍在 grace 内

- **WHEN** Visitor 的 gameplay connection 单独中断而 Session 与 WSS 仍有效，服务端 membership 仍允许 `RECONNECT`
- **THEN** Visitor 使用新凭据恢复同一权威 VisitSession，Owner 观察同一 member 的 reconnect/online 收敛，而不是进入自己的世界或产生第二个 member

#### Scenario: 服务端进程替换

- **WHEN** 两个 Player 在访问过程中失去服务端，服务端以保留 MySQL/Redis 的新进程恢复
- **THEN** 客户端拒绝旧 connection/admission/assignment，只使用新 generation 重建权威目标；无法恢复的 VisitSession 明确结束并让双方安全回到 OwnWorld

### Requirement: 资格 soak 必须有界验证空闲与重复恢复

资格 profile MUST 在固定有限预算内持续运行至少五分钟，并同时覆盖无业务操作的 heartbeat、周期性 snapshot、至少三轮受控 channel disconnect/recovery、页面反复打开关闭和 Scene/target replacement。Soak MUST 断言单一 AppRoot、session、WSS run、gameplay generation、heartbeat owner、recovery intent 与 active Scene，且 pending、dispatcher、subscription、socket 与 task 计数在每轮后回到允许基线；它 MUST NOT 建立依赖 CPU 型号的帧率、吞吐或延迟排名。

#### Scenario: 空闲 Player 经过多个 heartbeat 周期

- **WHEN** OwnWorld 和 Visiting Player 在 soak 窗口内不提交业务 command
- **THEN** heartbeat 保持连接或产生一次可解释的恢复，客户端不因空闲进入假死、重复弹窗或遗留并行 heartbeat

#### Scenario: 恢复循环发生资源增长

- **WHEN** 每轮功能结果都成功但 tracked socket、task、subscription、pending 或 Scene owner 高于前一轮允许基线
- **THEN** soak gate 失败，不能以最终 UI 看似正常声明通过

### Requirement: 单一资格入口必须产出低敏可审计结论

项目 MUST 提供唯一可产生 client-v1 qualified 结论的入口。它 MUST 使用锁定工具链和显式阶段 deadline，精确拥有自己创建的 server/storage/Player/build/report 资源，并在成功、失败、超时或中断后执行独立 cleanup budget。机器报告 MUST 记录 schema/manifest/contract/build digest、工具版本、阶段与场景稳定 outcome、耗时、证据类型和 cleanup 结论，但 MUST NOT 记录 credential、账号、运行 identity、endpoint、PID、本机绝对路径、原始 payload 或 backend/exception 文本。任何 mandatory failure、missing、skipped、stale evidence、cleanup failure 或工作区 contract 漂移 MUST 使结论为 not-qualified。

#### Scenario: 定向开发诊断不冒充资格证据

- **WHEN** 开发者以闭合 diagnostic scenario ID 请求快速验证单个恢复或生命周期问题
- **THEN** 同一入口只复用 automatic registry 登记的相关 Unity fixtures、构建单个 Development Player并生成隔离双 Player 清单；输出必须位于独立 ignored diagnostics run、显式声明 `qualificationEvidence=false`，不得生成或修改 automatic evidence、正式 report 或 qualified 结论，未知 scenario 必须在启动 Unity 前失败

#### Scenario: 场景失败后仍完成清理

- **WHEN** 任一 Unity test、Player 场景或 soak 超时失败
- **THEN** 入口保留首个稳定失败阶段，在独立预算内停止精确拥有的进程和 storage run，并同时报告 cleanup failure 而不覆盖主失败

#### Scenario: 全部 mandatory gate 通过

- **WHEN** clean generation、自动测试、Development/Release build、双 Player、故障恢复、soak、文档与 cleanup 在同一 contract/build 证据链上全部通过
- **THEN** 报告才可声明 client v1 qualified，并允许归档 `qualify-client-v1`
