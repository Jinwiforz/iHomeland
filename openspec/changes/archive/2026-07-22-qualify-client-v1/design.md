## Context

C2 已经交付唯一 `SessionCoordinator`、有限恢复的 `ClientControlChannel`、单 generation 的 `ClientGameplayChannel`、`WorldAdmissionCoordinator`、PersonalWorld/VisitSession Services、产品 Experience、Scene transition 与五个 routes。当前进程内失败收敛已经有 generation/cancellation、single-flight、45 秒显式恢复总 deadline 和 gameplay heartbeat，但边界仍停在“进程重启重新登录、gameplay 断线玩家手工重试、Development build 与手工证据分散保存”。

服务端公开契约已经由 Q0 冻结：refresh 原子轮换且 replay 会使 lineage fail closed；WSS ticket、gameplay ticket 与 world admission 都是短期一次性凭据；Visitor `RECONNECT` 已有 admission purpose 和 typed first command。客户端不能为了资格便利新增第二套 token、从 PlayerID 推导 target、复用旧 admission，或让 UI/Scene 保存恢复事实。

本 change 同时触及 Windows 平台安全存储、Session 生命周期、WSS/TCP target 恢复、产品 UI 和发布资格工具，因此必须先固定 owner、提交顺序、故障分类与证据链。Unity 操作仍由开发者在锁定 Editor 执行；仓库自动化负责提供可重复命令、manifest、构建和结果验证，不依赖本机已经存在的 `Library` 或旧 Player。

## Goals / Non-Goals

**Goals:**

- 使用 Windows 当前用户范围的 OS 保护持久化唯一 refresh lineage，并对成功轮换、损坏、失效、退出和提交未知提供原子、fail-closed 语义。
- 在 App Scope 内建立唯一恢复协调者，使 WSS 与 gameplay 故障分别收敛，健康 channel 不被无条件重建，Visitor 在服务端 grace 内真正使用 `RECONNECT` 恢复原 membership。
- 将 session restore、automatic recovery、manual retry、Scene/UI commit 全部纳入 generation、single-flight 和 terminal snapshot，而不是 timer/tick 修正。
- 交付一条可审计的 C3 资格门，覆盖 clean generation、Unity tests、Development/Release Player、双客户端故障矩阵、五分钟受控 soak、低敏报告和精确清理。
- 保持服务端 operation、message ID、MySQL/Redis schema 和第一里程碑范围不变；公开 schema 变更只限恢复正确性所需的两项向后兼容 revision 字段。

**Non-Goals:**

- 不支持跨设备 token 同步、云存档、账号密码保存、多个 production 本地 profile 同时登录或平台通用 credential vault。
- 不保证回放 WSS 断线期间从未送达的 invite PUSH；现有协议缺少 inbox 全量读取，客户端只做安全失效与后续新 generation 收敛。
- 不把 `ClientGameplayChannel` 改为自行申请 admission 或无限后台重连；产品 target 恢复由 application coordinator 拥有。
- 不新增 server operation、message ID，不修改 token/refresh 语义，也不在两项兼容 revision 字段之外重新打开 Q0 协议范围。
- 不引入 Addressables、内容资源、ActivityInstance、Room、Party、战斗、UDP/KCP、性能排名或长时间容量压测。

## Decisions

### 1. Session owner 定义安全存储 port，Windows infrastructure adapter 使用 DPAPI CurrentUser

Application 层新增窄 `IClientSecureSessionStore`，只表达 `Read`、`Replace`、`Delete` 与稳定 failure kind；它不暴露文件、Windows handle 或任意 key/value API。Windows adapter 直接调用受控的 `CryptProtectData`/`CryptUnprotectData`，scope 固定为 current user，不引入第三方 credential package，也不提供跳过保护的 production 开关。测试使用完全受控的 fake store，非 Windows production adapter 明确返回 Unsupported。

持久文件位于 Unity `persistentDataPath` 下 owner-specific 目录。外层只含 magic、record schema、加密算法标识和 ciphertext 长度；environment identity、refresh token、session/account binding 与 record generation 全部位于受保护 payload。额外 entropy 由稳定 product/environment binding 派生，只用于隔离误用，不冒充 secret。读取在分配前校验文件硬上限、schema 和长度，native buffer 使用后归零并释放；日志只记录 operation、稳定 failure kind 和 record schema。

写入使用同目录临时文件、flush、原子 replace 和受控 ACL，不跨卷 move。启动时清理由 adapter 精确处理自身遗留 temp；不得枚举删除整个 `persistentDataPath`。同一 production profile 以进程级互斥 owner 拒绝第二个写者，避免两个 Player 轮换同一 refresh 后触发 replay。Local Development 的双客户端资格通过仅 Debug build 可用的隔离 data-profile 参数选择不同存储目录；Release 不解析该参数，也不获得多 Session 产品语义。

**替代方案：**PlayerPrefs、明文 JSON、设备 ID 派生 AES key或把 token 放 ScriptableObject。它们不能提供 OS 用户隔离、可靠密钥管理和安全删除边界，全部拒绝。

### 2. 安全 record 与内存 Session 采用“持久提交先于可用发布”的单一事务顺序

Register/login/refresh 返回只在 codec、lineage 和 generation 全部验证后形成候选 snapshot。Session owner 先 `Replace` 新 refresh record；只有成功后才原子发布 current in-memory snapshot。若首次 login 的 record write 失败，候选 session不向产品层开放，并 best-effort logout；logout 结果不影响本地 fail closed。若 refresh 已在服务端成功但新 record 无法落盘，旧 refresh 已不可再用，因此 owner 删除旧 record、清空内存并进入 Unresolved，不能继续使用新 access 或旧磁盘 record。

`AUTH_UNAUTHENTICATED`、forced invalidation、logout success/unknown、refresh unknown/replay、forget、record corruption 和 environment mismatch 都删除精确 record并递增 generation。删除失败仍不恢复内存资格，但形成稳定 security-storage failure，阻止自动恢复继续循环。Ticket、admission、access、password、world/visit/route state 永不进入 record。

**替代方案：**先发布内存 snapshot 再异步保存。进程在两步之间退出会留下已消费 refresh，UI也可能在持久化失败后建立新 socket，无法给出原子恢复承诺，因此不采用。

### 3. 启动恢复是 App Scope 的一次性有界事务，不是页面副作用

Composition 在 Session/HTTP 建立后、control/gameplay/Experience 业务启动前创建 `ClientSessionRestoreCoordinator`。Experience 初始发布 `RestoringSession`，coordinator 只执行一次 `Read -> version/config -> refresh -> secure Replace -> session commit`。无 record/Unsupported 直接成为 NotAvailable 并打开 Login；损坏、明确拒绝或 commit-unknown 清理 lineage 后以稳定低敏结果进入 Login；成功才启动 control 与 own-world flow。

恢复使用独立 generation 与总 deadline，App shutdown、玩家显式 forget 或新 login 使旧 completion 失效。没有 while-loop、Update polling 或失败后自动再次 refresh。`RestoringSession` 是 View State，不拥有 store/token；UI Host 重建只重放当前不可变状态。

**替代方案：**Login 页面 `OnShow` 自动 refresh。页面重载、route rollback 和多 Host 会重复网络副作用并把 credential 生命周期交给表现层，因此拒绝。

### 4. 唯一 ConnectionRecoveryCoordinator 组合 channel 状态，adapter 只恢复自身 transport

`ClientConnectionRecoveryCoordinator` 位于 Application/World 邻近 feature slice，但不合并 Session、PersonalWorld 或 VisitSession facts。它订阅 generation-bound control/gameplay health，只保存当前 recovery intent、source/target generation、desired authoritative target、阶段、deadline 和低敏 terminal result。Control channel 继续拥有自身有限 ticket/backoff/receive recovery；Gameplay channel继续只负责一条显式 connection generation、typed I/O、heartbeat 和 close。

Control 瞬时故障时 coordinator 不关闭健康 gameplay/Scene，只使依赖 control 完整性的邀请动作失效并呈现 `RecoveringControl`。新 WSS generation Connected 后，coordinator 清除旧 generation 的 control-only inbox/hints，并通过仍健康 gameplay请求 current world/VisitSession 完整 snapshot；这可恢复 member、role、revision 与 assignment，但不会伪造断线期间未收到的 invite。旧列表被清空后接受按钮无 stale identity，服务端明确拒绝仍沿用既有精确退役语义。

Gameplay unexpected disconnect 时 channel 先完成 terminal generation，coordinator 立即关闭旧 mutation gate、Scene/HUD binding并呈现 `RecoveringWorld`。它在同一总预算内执行有限、可取消的 classified transport attempts；backoff 只节流新的瞬时 transport 尝试，不用于推断业务成功或修改状态。Protocol/auth/policy/commit-unknown 不重试。预算耗尽恰好发布一次 `ConnectionLost`，玩家随后才能创建新 manual generation。

**替代方案：**让每个 channel 广播全局 event 并由 UI 各自重连。它会产生并行凭据、Scene 与 target owner，无法证明恢复顺序，因此不采用。

### 5. OwnWorld 与 Visiting 使用不同权威恢复计划

进入稳定 target 时 coordinator 保存不含 credential 的 `RecoveryTargetDescriptor`：服务端 Session identity/epoch、本地 session generation、target kind、PersonalWorld/VisitSession identity、role、最高 revision、assignment generation 和 Visitor reconnect absolute deadline。Gameplay 断开会使世界事实只读冻结，但旧 Service snapshot 与 Scene 立即从可操作状态退役。同一 Session identity/epoch 上的 single-flight refresh 可以只把 descriptor 的本地 generation 单调重绑定到 current；不同 Session identity/epoch、generation 回退或换账号不得继承旧 target。这样 access 在断线期间到期时仍可继续同一权威恢复，而新登录不能复活旧世界。

OwnWorld 恢复重新执行 bootstrap、OWN_WORLD admission、connect、snapshot 和 Scene commit；新 assignment generation 可在 world revision 不变时替换旧 runtime identity。若冻结 descriptor 同时包含 Owner VisitSession，恢复流必须在 target 校验与 Scene commit 前通过新 gameplay generation 依次读取并收敛 World 与 VisitSession 完整 snapshot；VisitSession aggregate revision 与 assignment generation 是独立单调轴，同一 VisitSession revision 下更高 assignment generation 可以替换旧 runtime identity。断线时退役本地 VisitSession projection 不能被解释为服务端已关闭访问，也不能在仅恢复 World 后把 descriptor 差异误分类为协议不兼容。Visiting 恢复只在 current Session、descriptor identity 和公开 deadline 仍匹配时签发 `RECONNECT` admission，建立 pending connection并调用现有 typed `VisitReconnectCommand` 作为首帧。Visitor 断开本身会提交新的 VisitSession revision，而已断开的 gameplay 无法接收该 replacement PUSH，因此 admission 必须把签发判断读取的 `visitRevision` 冻结进幂等 binding 并公开返回；客户端不得用断线前 revision、`+1` 推导或冲突重试猜测。Response 必须返回匹配 membership、VisitSession/world snapshot和更高/等价合法 revision后才提交 Visiting；not-found、expired、closed 或 safe-return 立即转入 ReturningOwnWorld，不能伪装 RECONNECT 成 JOIN 或回退为普通 OwnWorld success。

`SafeReturnDirective` 同时携带生成该指令的权威 VisitSession revision；同一 VisitSession 已提交更高 revision 或重新加入后，迟到的旧指令必须被 revision gate 丢弃，不能把新 membership 送回 OwnWorld。

所有 completion 同时校验 session、recovery、target 与 scene generation。Automatic 和 manual path 共用同一 state machine；按钮只是新的 intent来源，不能建立第二套流程。

**替代方案：**Visitor 断线一律回自己的世界。它浪费已冻结的 grace/RECONNECT 契约，也会让 Owner 在 grace 内仍显示 membership而 Visitor UI无恢复能力，因此 C3 改为真实 RECONNECT。

### 6. 产品 UI 只展示恢复阶段和权威能力，不拥有重试策略

现有 Shell/ConnectionLost route 扩展不可变状态，不新增平行 modal owner。`RestoringSession` 阻断账号输入；`RecoveringControl` 保留 gameplay 显示但禁用 invite inbox/outgoing 相关动作；`RecoveringWorld` 撤销 gameplay input并阻断旧 Scene；`ConnectionLost` 只在 automatic terminal 后提供一次 manual retry/logout。恢复成功必须先提交 Session/channel/target/snapshot/Scene，再原子关闭旧 modal；route cancellation不取消已经转交 App Scope 的 intent。

Owner member row继续使用服务端 snapshot 的 membership state，若协议只提供 reconnecting 状态则明确显示“重连中”；UI不根据 socket presence删除成员。任何 red error 只使用稳定 message key，不能显示内部 enum、exception、endpoint 或 credential。

**替代方案：**在 HUD 上依据 channel bool 切按钮和弹窗。多个回调时序会让 UI自造状态，正是本 change要消除的问题。

### 7. 资格 manifest 区分自动证据与真实 Player 证据，并绑定同一 digest

新增 `shared/contracts/fixtures/client-qualification/manifest.json`，而不混入服务端 Q0 manifest。场景只声明稳定 ID、group、execution、mandatory、budget、build profile、expected outcome与 evidence owner。自动 registry/Editor entry与 manifest 双向校验；真实显示输入、双 Player 和网络故障场景由结构化 evidence record引用当前 contract digest、Development build digest、scenario ID、时间与 pass/fail，不保存账号、截图路径或 runtime identity。旧 build证据自动 stale。

单一 `tools/client-qualification/client-qualification.ps1` 按阶段执行 proto clean generation、静态编译、EditMode、PlayMode、Development/Release build、Player smoke、自动 soak、evidence completeness、OpenSpec/docs/governance与cleanup。Unity命令通过锁定 Editor路径和 Editor methods执行；新增统一 `ClientWindowsBuild` 支持显式 Development/Release profile，构建源、scene list与 identity校验共用一条实现。机器 report写入 ignored `.local/client-qualification/<run-id>/`，只有摘要文档进入 Git。

同一入口额外提供不产生资格结论的 `diagnose` 动作。它从闭合 diagnostic registry 选择稳定场景，并只通过 automatic registry 的既有 scenario ownership汇总相关 EditMode/PlayMode fixture，禁止维护第二份测试 selector。诊断成功后只构建 Development Player，生成相对路径启动器与 `qualificationEvidence=false` 清单，写入独立 `.local/client-diagnostics/<run-id>/`；Owner与Visitor必须使用各自显式`-logFile`，防止两个进程竞争Unity默认`Player.log`而污染诊断归属。它不读取或写入 evidence/report，也不能成为 `finalize` 前置条件。代码或build变化后仍必须执行完整资格链，diagnose只缩短缺陷反馈周期。

服务端进程替换缺陷额外使用 `server-restart-recovery.ps1` 驱动真实 Development Player。Player 内的 Development-only owner 只观察产品 snapshot、接收外部 replacement-ready 文件信号并调用同一生产重连入口；PowerShell owner 只依据精确 listener PID 启停同一可执行文件，并用 Player 低敏 marker 与最终进程/端口事实裁决。该诊断不模拟 HTTP/WSS/TCP 成功、不修改产品 deadline，也不以 sleep 推进状态；输出隔离在 `.local/client-recovery-diagnostics/<run-id>/` 且不进入正式 evidence。

真实双 Player证据可以由用户操作，但必须由本次入口准备精确 server/storage/build run并输出场景步骤；完成后入口验证证据再产生结论。任一人工场景 missing/skipped/stale 都保持 not-qualified。工具只终止其记录的精确 PID并由 storage owner按 run-id清理，不按进程名或目录前缀删除。

**替代方案：**把昨晚/其他机器的截图、Unity Test Runner绿色界面或口头结论作为资格证据。它们没有 contract/build绑定且无法判断遗漏场景，不采用。

### 8. 五分钟 soak 验证资源守恒，不充当性能或31分钟idle测试

资格 soak使用当前 Development Player和固定 local profile，持续至少五分钟，跨越多个15秒 gameplay heartbeat与control heartbeat周期，并执行三轮channel断开/恢复、route open/close、Scene replacement。Development-only低敏诊断公开固定计数：AppRoot、active control/gameplay generation、heartbeat/recovery intent、pending、dispatcher、subscription和Scene owner；不公开identity或credential。每轮结束必须回到manifest允许基线，最终Player.log经过redaction扫描。

这个 profile证明空闲存活与重复恢复资源守恒，不设置CPU/GPU相关FPS/延迟阈值，也不替代未来容量、长稳或网络模拟change。服务端30分钟idle safety仍由已有owner测试和heartbeat协议资格证明，无需每次C3人工等待31分钟。

**替代方案：**单纯让Player静置31分钟。它耗时高、故障定位差且不能证明反复恢复资源是否增长，因此采用短时高覆盖受控soak。

### 9. 协议扩展仅限权威 revision，control gap 采取显式 fail-closed

为避免 Visitor reconnect 猜测断线期间已经推进的 VisitSession revision，World admission 兼容新增 `visitRevision`；为拒绝迟到的旧 safe-return，`SafeReturnDirective` 兼容新增产生该指令时的 `revision`。两者不新增 operation、message ID、存储结构或第二条业务入口，并由统一协议生成与 parity 门禁治理。

现有WSS连接没有邀请inbox全量读取或sequence跨连接重放契约，因此C3不能声称恢复断线期间从未到达的invite。新control generation提交时，旧inbox/hints完整清空，UI显示已同步当前连接但不保留旧邀请；Owner若需要可基于其gameplay完整snapshot撤销/重新邀请。若产品未来要求Visitor恢复所有仍有效邀请，必须单独提出server/client协议change，增加权威inbox snapshot或resume cursor，并重新运行Q0与C3。

**替代方案：**保留旧inbox直到expiry，或在客户端猜测Owner状态。前者会继续暴露已撤销邀请，后者违反服务端权威，因此选择可解释的数据缺失而不是错误事实。

## Risks / Trade-offs

- [DPAPI P/Invoke 在Unity后端或native buffer封送上存在平台细节] → adapter保持极窄，增加Windows EditMode/Player contract tests、oversize/corruption/native error与buffer释放测试；非Windows明确Unsupported。
- [Refresh服务端成功而本地原子replace失败会迫使用户重新登录] → 这是安全的fail-closed代价；删除旧record、清空access并记录稳定storage failure，绝不重放旧refresh。
- [两个production Player共享同一profile会争用secure record] → current-user named mutex拒绝第二个writer；双客户端资格只在Development使用隔离profile，不把多Session扩散为产品规格。
- [自动gameplay恢复可能与safe-return、forced logout或用户retry竞态] → 所有路径共享session/recovery/target generation和唯一terminal slot，terminal authority事件优先撤销intent，迟到completion只完成旧等待方。
- [Control恢复会清空仍可能在服务端有效的invite] → 当前协议不能无损同步；清空避免错误可点击状态，限制在control-only projection，并在文档明确未来完整恢复需要协议change。
- [完整C3运行较慢且含人工步骤] → 普通开发可按stage运行，manifest绑定证据可在同一build上续跑；只有最终聚合入口要求全部mandatory且不允许skip。
- [开发者把定向diagnose误认为正式资格] → 输出根目录、schema与文案均显式区分，固定`qualificationEvidence=false`且不生成evidence/report；finalize只接受完整资格run中的闭合证据。
- [Development诊断被误带入Release] → build contract test扫描程序集/命令入口，Release构建不解析qualification profile或暴露diagnostic port；两种build都做启动和日志smoke。
- [五分钟soak漏掉长周期容量问题] → C3只证明heartbeat和重复恢复守恒；长期性能/容量/网络模拟需要独立profile与change，不虚假宣称覆盖。

## Migration Plan

1. 建立 secure store port、Windows DPAPI adapter、record codec/atomic file owner和受控fake tests，先不接入启动流程。
2. 修改SessionCoordinator提交顺序，覆盖login/refresh/logout/invalidation/unknown与storage失败；再接入一次性startup restore和RestoringSession状态。
3. 建立channel health模型与ConnectionRecoveryCoordinator，先覆盖control独立恢复和旧inbox失效，再接入OwnWorld gameplay恢复。
4. 接入Visitor recovery descriptor、RECONNECT admission/first command、safe-return/expiry竞态和产品恢复View State；保持manual retry复用同一state machine。
5. 增加client qualification manifest、统一Windows builder、自动测试/soak/报告/cleanup入口和Development双profile隔离。
6. 运行全部自动gate，随后在同一Development build上完成双Player与故障矩阵；构建Release并执行smoke、redaction和evidence completeness。
7. 生成 `docs/client-v1-qualification.md`，同步roadmap、client architecture/integration/UI、file structure、workflow/README和长期specs；strict验证后才归档并声明qualified。
8. 回滚时先禁用startup restore和automatic recovery composition，删除精确secure record，再移除adapter/coordinator/qualification工具。服务端协议与数据无迁移；任何回滚都会使C3资格结论失效。

## Open Questions

无。跨设备凭据、production多profile、control invite全量恢复、长稳性能与其他平台安全存储均需要独立需求和OpenSpec，不阻塞Windows客户端v1资格。
