## Context

S0-S3、W0-W2、D0-D1、V0-V1、P0 与 N0 已归档，`complete-server-personal-world-slice` 也已用 production Composition Root、真实 MySQL/Redis 和临时 TLS 验收完整 own-world/visit-world 竖切。当前测试仍主要由各 package 或 `internal/app` 直接构造服务端对象图；它们能证明实现正确，却不能单独证明一个不认识内部类型的客户端可以仅凭已提交契约完成全部流程，也没有把 contract、fuzz/race、依赖故障、资源压力和证据报告收敛成 C0 前唯一资格门。

本 change 是服务端 v1 发布资格，不是新业务阶段。正式消费者位于既定的 `server/internal/testclient`，运行目标是独立 `cmd/server` 子进程；Docker、临时证书、动态端口和故障动作由仓库工具编排。资格结果需要同时服务于服务端回归和 Unity 后续接入，因此场景、契约摘要、命令与失败边界必须版本化、可审计且不依赖本机状态。

## Goals / Non-Goals

**Goals:**

- 交付不导入服务端业务实现的 Go HTTP/WSS/TLS-TCP 客户端和可组合 scenario runner。
- 用机器可读 manifest 固定 Q0 正向、恢复、安全、资源与关闭矩阵，并证明 manifest 与 runner 一一对应。
- 用单一 PowerShell 入口创建真实隔离环境、启动独立服务端、执行分层质量门、注入受控故障并可靠清理。
- 对 v1 contract sources、registry、fixtures/golden 和 endpoint manifest 示例形成确定性冻结证据。
- 输出低敏机器报告与长期资格文档，只有全部 mandatory gate 通过才解锁 C0。

**Non-Goals:**

- 不新增或修改公开 HTTP/Protobuf/message/error/route 契约、listener、table 或 Redis key。
- 不把 qualification client 变成 SDK、Unity 客户端实现或 production admin/fault API。
- 不用 Q0 runner 替代 package unit/contract/integration/fuzz/race tests，也不复制服务端 domain policy 计算预期结果。
- 不引入通用测试 DSL、通用 chaos platform、benchmark 排名、分布式部署或跨节点 placement。
- 不实现 C0、C# generation parity、Unity project、Room、Party、ActivityInstance、战斗或 UDP/KCP。

## Decisions

### 1. 资格客户端是严格黑盒消费者，测试编排与服务端对象图彻底分离

`server/internal/testclient` 只允许依赖 Go 标准库、锁定的 WebSocket/Protobuf runtime 和 `internal/generated/proto`。它自行实现公开 HTTP JSON、WSS binary envelope、TLS/TCP preface/frame、sequence、correlation、deadline 与 credential 生命周期，不导入 `internal/app`、domain owner、storage、`internal/protocol` 或任一 transport adapter。Architecture test 对 imports 使用 allowlist，新增内部依赖必须使资格门失败。

服务端始终以构建后的 `cmd/server` 独立子进程启动；runner 只从 HTTPS bootstrap/auth response 获得 advertised endpoints，从公开 response/push 观察事实。测试需要的账号、幂等 key、request/command identity 在客户端侧生成，但预期业务结果来自版本化场景和公开契约，不调用服务端 constructor 或 fake store。

**替代方案：**复用 `internal/app/storage_integration_test.go` 的 client helper。该 helper 与 Composition Root 同进程且可以接触内部配置/状态，不能证明独立消费者边界，因此只保留为较快的 integration layer，不作为 Q0 client。

### 2. Manifest 固定覆盖面，Go 场景函数表达流程，不创建通用 DSL

在 `shared/contracts/fixtures/qualification/` 增加 schema-versioned manifest。每个场景只声明稳定 ID、分组、mandatory 标记、phase、`execution`、预算、预期公开 outcome 和证据标签；不得包含 raw credential、随机运行值、Docker 名称或内部 storage mutation。`execution` 明确区分 contract、public wire black-box 与本轮实际执行的 layered owner evidence；`evidence-manifest.json` 是 layered scenario 到 package/test identity 的唯一映射源。Go runner 维护 `scenarioID -> function` 的显式封闭 registry，contract test 双向检查无 manifest 漏项、无隐藏 runner、无重复 ID、所有 outcome/phase/execution 枚举受控，并验证证据目录与真实测试声明同步。

复杂多 actor 时序保留为类型明确的 Go 场景函数，共享的 HTTP/WSS/TCP client、actor session 与断言 helper 才抽取。场景不把 server revision、deadline、generation 或错误码重新算法化；它从前一步公开结果取得 expected value，并只断言协议定义的单调、相等、拒绝或关闭关系。

**替代方案：**用 JSON/YAML 描述完整网络脚本。它会复制协议类型系统、条件分支和 secret 生命周期，形成难以评审的第二套测试语言，因此不采用。

### 3. 分层资格门聚合既有证明，黑盒矩阵负责跨通道和进程边界

单一入口按稳定阶段执行：contract/proto clean generation、Go unit/integration、显式 fuzz targets、race、真实 storage integration、独立黑盒 functional/recovery、layered resource/shutdown evidence、governance 与 cleanup。任何 mandatory 阶段失败立即阻止资格成功，但进入独立 cleanup budget；报告按固定 gate 顺序保留 pass/fail/skipped 与首个稳定失败类别，不能把未开始的 mandatory gate 从报告中省略。

黑盒 functional 至少覆盖 register/login/refresh、WSS、own bootstrap/admission/snapshot、重复 bootstrap、invite/accept/join、leave、kick、close、Owner/Visitor disconnect/reconnect 与 safe-return。破坏性故障场景在隔离 phase 中执行，避免 Redis flush、process restart 或 dependency restart 让后续断言依赖偶然顺序。已有 package tests 继续拥有 commit-unknown 注入和无法由公开网络稳定制造的精细线性化竞态；资格报告必须引用这些 mandatory layer，不能声称所有故障都由 wire client 单层证明。

**替代方案：**只运行 `go test ./...` 或只扩充一个巨型 end-to-end test。前者缺少独立消费者，后者会变慢、脆弱且丢失精确故障定位，因此采用分层聚合。

### 4. 资格入口拥有子进程，storage 工具继续拥有资源创建和删除

新增 `tools/qualification/qualification.ps1` 作为唯一 Q0 入口。它通过 `tools/go/go.ps1` 与 `tools/proto/proto.ps1` 使用锁定工具链，通过 `tools/storage/storage.ps1 -Action up/down/status` 获取隔离 MySQL/Redis，并在 `.local/qualification/<run-id>/` 创建临时 TLS、配置、server binary、PID、stdout/stderr 和 report。公开/诊断/TCP listener 均绑定 OS 动态 loopback 端口，advertised endpoints 与实际地址由临时配置显式对应。

入口用进程 API 启动隐藏子进程、异步排空 stdout/stderr、轮询独立 readiness，并在 timeout、Ctrl+C、test failure 或 panic-like script failure 后终止精确 PID。Docker 删除仍只由 storage owner 按 run-id label 执行。Redis flush、Redis/MySQL restart 等 fault driver 必须先读取精确 run state 并验证 ownership label，只允许封闭动作且不承担删除；不能按前缀枚举或影响其他 run。

**替代方案：**让 Go client 直接启动 Docker/服务端或向 production 增加 fault endpoint。前者混合消费者与基础设施 ownership，后者扩大攻击面，因此不采用。

### 5. 凭据、时间与故障使用受控能力，不写入 report 或日志

每个 actor 单独持有 access/refresh token、ticket、admission 与 connection；值默认不可格式化，自有 byte buffer 在消费、替换或失效后尽力覆零。Go immutable string 无法可靠覆零，因此只允许在紧邻标准库/协议调用时短暂创建，不得跨调用保存或写入日志。资格工具只向子进程环境传 secret reference，不把 secret value 拼入 command line、异常或 report。客户端日志只允许 scenario ID、phase、公开 operation/message ID、stable outcome 和随机低敏 connection label。

配置把 lease、invite、reservation 和 grace 缩短到现有合法最小范围，使 expiry/reconnect 场景在总预算内完成；断言始终读取服务端公开绝对 deadline，不用固定 sleep 猜边界。等待采用有界 polling/connection deadline，超时报告稳定阶段，不打印 response body、payload 或 backend text。

**替代方案：**使用 production 默认分钟级 deadline 或注入服务端 fake clock。前者使 Q0 过慢，后者破坏真实进程边界；合法短配置仍执行真实 timer/store 语义，因此采用。

### 6. 资源矩阵验证上界和收敛，不建立机器相关性能门

slow consumer、connection storm、backpressure、半帧/慢写与并发 response/push 由证据目录登记的确定性 owner tests 验证，并用 manifest profile 记录有限尝试、并发、字节与时间上限；这些结果在报告中标为 `execution=layered`，不伪称为 public wire。独立 server 前后只读取公开 WSS/TCP active/in-flight gauges 并验证回到基线；queue/runtime/deadline 与低基数 label vocabulary 由同轮 owner tests 验证，不按 CPU 型号设吞吐或毫秒排名。

所有 profile 必须小于服务端配置硬上限并有总连接/字节/时间预算。压力失败后仍必须证明 readiness/关闭和资源清理；不能为了通过而调高 production 默认上限或关闭背压。

**替代方案：**把 Q0 变成 benchmark 或无限 soak。结果不可重复且不适合作为功能冻结门；长期容量和 soak 应由后续独立性能 change 定义。

### 7. 资格证据同时提供机器报告、长期摘要和契约冻结校验

每次运行在 ignored 目录写 schema-versioned `report.json`，记录 run ID、阶段/场景稳定结果、耗时、工具/协议版本、contract aggregate digest 和清理结果，不记录 host绝对路径、端口、PID、credential、Player/Session/World/Visit identity 或原始日志。成功 apply 后更新 `docs/server-v1-qualification.md`，只保存完成范围、manifest版本、冻结 digest、执行命令、mandatory gate 汇总、已知非目标和 C0 解锁结论。

冻结 digest 由排序后的 committed schema、OpenAPI、registry、qualification manifest 与既有 fixtures/golden 原始 bytes 确定性计算；digest record 本身和人类报告不参与计算。Contract test 重算并拒绝静默漂移。Q0 后任何基础契约变更必须经过独立 OpenSpec、更新 fixtures/兼容策略并重新资格验收；Git commit 继续是完整源事实，digest 只是跨端交付的快速完整性证据，不生成或提交 descriptor/projection。

**替代方案：**提交每次运行的完整日志或仅口头声明测试通过。前者包含高基数本机状态且会制造噪声，后者不可审计；结构化低敏结果与稳定摘要兼顾自动化和长期证据。

### 8. C0 只由完整 mandatory gate 解锁

runner 的单个场景、已有 storage verify 或手工联调通过都不能单独标记 Q0。只有 manifest 中全部 mandatory scenario、所有分层 gate、资源清理和同一次 `verify` 内的 governance gate（OpenSpec strict、任务完成度、diff 格式、generated 边界）同时通过，资格报告才能声明 `qualified`。主 specs/owner docs 与 contract freeze 必须在运行前保持同步；任何 mandatory skipped 都使结论失败。

归档时同步 `delivery-sequencing`、`server-contracts`，更新 roadmap、client integration、file structure 与 server README；C0 proposal 必须引用已归档 Q0 和当前冻结摘要。资格 change 不创建 Unity 文件，防止用“准备客户端”绕过进入门。

**替代方案：**人工勾选或允许部分通过。它会使门禁无法重复执行并让 C0 建立在未知缺口上，因此不采用。

### 9. 资格客户端按公开 capability 长期分组演进

Q0 完成后保留 testclient、manifest 和 qualification 入口。服务端新增公开 operation、message、channel、credential 或恢复语义时，对应 OpenSpec 必须在同一 capability group 增加场景并继续运行全部旧 mandatory 回归；纯内部重构、存储实现替换或 package 移动只要公开行为不变，就不得要求 client 跟随内部结构修改。Activity、battle 等未来能力使用独立 qualification group，完整发布门聚合各 mandatory group，不能把所有流程堆入一个巨型 scenario。

资格客户端始终是自动化考官而非产品 SDK：只验证公开输入输出、兼容、安全和资源边界，不持有 Scene/UI/表现，也不复制服务器状态机或奖励结算。协议破坏性演进需要并行验证兼容版本或明确迁移窗口；旧场景只有在对应版本正式退役并把编号/语义保留为 reserved 后才能移出 active matrix，历史 manifest 仍由 Git 保留。

**替代方案：**Q0 后删除客户端，或把它持续扩张为无界的第二个游戏客户端。前者失去外部回归，后者复制产品逻辑且维护成本失控，因此采用按公开 capability 分组、旧回归常驻的长期模型。

## Risks / Trade-offs

- [完整资格运行时间较长且 fuzz/race 会消耗较多资源] → 阶段化输出、显式全局预算、固定 fuzz time 和可单独运行的开发场景；只有完整入口可产生 qualified 结论。
- [Windows 子进程或 Docker daemon 异常可能遗留资源] → 精确 PID、独立 cleanup budget、run-id ownership、可重复 `down` 和报告 cleanup outcome；禁止模糊递归删除。
- [跨通道异步 push 造成偶发顺序] → 只断言 registry 保证的单连接 sequence、correlation 和最终 revision，不假设跨连接全局顺序；每个等待都有业务 predicate 与 deadline。
- [短测试 deadline 改变时序压力] → 只在现有合法配置范围内缩短，并同时保留默认配置的 unit/storage tests；versioned manifest 记录 profile，资格报告只引用稳定 scenario ID/execution 且不记录本机端口。
- [Manifest 与 runner 漂移或出现隐藏场景] → 双向 completeness test、封闭枚举和 report 按 manifest ID 生成。
- [Freeze digest 被误当作版本兼容策略] → 主 contract sources、OpenSpec 和 Git 仍是 owner；digest 只证明交付集合完整，破坏性演进仍必须新版本/兼容窗口。
- [黑盒无法稳定制造所有 commit-unknown 线性化点] → 报告明确区分 wire、storage integration、domain/race 证据，不伪造单层覆盖。

## Migration Plan

1. 增加资格 manifest schema、client import boundary 与基础 HTTP/WSS/TCP consumer，不改变运行服务端。
2. 增加 functional/recovery/resource scenarios，并为每个 manifest ID 建立唯一 runner 与单独测试入口。
3. 增加 qualification PowerShell 编排、独立进程、临时 TLS、fault driver、report 和 cleanup contract tests。
4. 运行完整 Q0，修复服务端或契约暴露出的缺陷；行为修复必须同步现有 specs，不能在 test client 中放宽预期。
5. 生成冻结摘要与 `docs/server-v1-qualification.md`，同步主 specs/owner docs，strict 验证后归档。
6. 回滚仅删除资格 client/tool/manifest/report；服务端 schema 和运行行为没有迁移。若 Q0 暴露的服务端修复被回滚，则资格状态同时失效，C0 重新关闭。

## Open Questions

无。长期性能基线、跨节点部署、C# parity 与 Unity runtime 均由后续独立 change 处理，不阻塞 Q0。
