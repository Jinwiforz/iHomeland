## 1. 冻结 control source contract 与 B0.3 输入

- [x] 1.1 建立 `shared/contracts/fixtures/simulation-control/` 的 README、closed schemas、message inventory、manifest、canonical golden 与正负 cases，登记版本、方向、大小、deadline、request/replay identity 和低敏规则
- [x] 1.2 让 control manifest 绑定 B0.3 Release/ASan build identity、qualification receipt、model/profile/config digest 与 Windows x64 资格状态，漂移时在启动 child 前 fail closed
- [x] 1.3 实现 `tools/simulation-control/` 的 schema/manifest/canonical validator 和隔离失败回归，覆盖 unknown/duplicate field、非规范整数、悬空登记、digest 漂移、超限 frame 与 source 写入
- [x] 1.4 增加 architecture gate，拒绝 control fixture/source 中出现 gRPC、TCP/UDP listener、production port、battle numeric message、ticket、Asio/KCP、AEAD 或 Unity 类型

## 2. 实现 C++ control frame 与 SimulationNode

- [x] 2.1 定义 C++ control value contracts 和 4-byte big-endian length-prefixed canonical JSON codec，严格校验 UTF-8、64 KiB hard limit、bootstrap nonce、单调 sequence、request ID 与 closed payload
- [x] 2.2 为 partial frame、trailing bytes、stdout contamination、wrong nonce/sequence、unknown kind、oversize、EOF 和 write failure 增加 C++ unit/negative tests
- [x] 2.3 实现不可复活 SimulationNode/SimulationInstance identities、hello/build receipt validation、node health/draining 状态与 checked instance/actor capacity accounting
- [x] 2.4 实现多实例 node registry 和 `control-baseline-v1` composition，组装 realtime TickClock、既有 Jolt/Detour/config adapters 与唯一 SimulationInstance worker，不复制 gameplay owner
- [x] 2.5 实现 exact AssignmentStamp + start request identity 的 start/ready/status 幂等与 replay cache，覆盖响应丢失、WorldInstanceID 冲突、stale generation 和部分启动 rollback
- [x] 2.6 实现 bounded drain/drained 和 exact stop/stopped，覆盖关闭 ingress、完成有限 Tick、result flush、deadline、missing/replayed stop 和 successor 隔离
- [x] 2.7 实现每 node 最大 256 entries 的 immutable ResultProposal outbox、ack/replay 状态机与容量失败，容量满后重试不得伪装 drained，未知 result kind 或 fingerprint 漂移必须拒绝
- [x] 2.8 为 `ihomeland-sim-server` 增加 `--control-stdio`，保证 stdout 只输出 frame、stderr 只输出低敏日志、stdin EOF/node.shutdown 走同一 drain/stop owner
- [x] 2.9 建立 `ihomeland_sim_control_adapter` target、分层 CTest labels、public-header/scope/comment gates，并重跑 B0.3 smoke、model cases、determinism、ASan 和 benchmark 证明 core 无回归

## 3. 实现 Go control domain 与进程 adapter

- [x] 3.1 在 `server/internal/simulationcontrol` 建立严格 SimulationNodeID、SimulationInstanceID、control session、request/receipt、capacity、target 和 result value contracts，并为所有导出/业务声明补齐中文契约注释
- [x] 3.2 实现与 C++ golden 对等的 Go canonical JSON/frame codec、单 reader、serialized writer、有界 pending correlation 和 stable error mapping
- [x] 3.3 实现 binary SHA-256、B0.3 build identity/qualification receipt 校验和精确 child process spawn，限制继承 handles 并分别管理 stdin/stdout/stderr
- [x] 3.4 实现 256-bit bootstrap nonce hello、OS child handle 绑定、health query/deadline、terminal process exit/EOF/protocol failure 与低敏 stderr pump
- [x] 3.5 实现单 node registry、health/draining transition、capacity selector 和 metrics snapshot，C++ 自报值只能缩小配置及 8-actor qualification cap
- [x] 3.6 实现跨进程 `placement.RuntimeController` adapter 的 start/drain/stop/status resolution，所有 receipt 必须完整匹配 stamp、node incarnation 与 SimulationInstanceID
- [x] 3.7 实现 `SimulationTargetResolver` 和 target revision invalidation，只为 current active/lease-valid/healthy/ready exact binding 返回无 credential、无 endpoint 的内部 target
- [x] 3.8 增加 Go unit/fuzz/race tests，覆盖 frame fragmentation、并发 pending、cancel-after-send、response loss、stale receipt、capacity 8/9、target replacement 和 terminal node failure

## 4. 建立 ResultProposal 持久裁决

- [x] 4.1 新增 additive `simulation_result_receipts` MySQL migration，为表和每列写中文短注释并登记 immutable ResultID、proposal/assignment fingerprint、instance、kind、Tick range、digests、disposition/reason 与 UTC 时间
- [x] 4.2 定义 receipt store、result catalog 和 owner committer 消费侧 contracts，初始只登记 `simulation.lifecycle.summary.v1`，禁止 generic opaque payload 或未登记 settlement owner
- [x] 4.3 实现 production MySQL receipt adapter 和同 transaction decision，区分 first commit、matching replay、fingerprint conflict、rejected 与 commit-unknown
- [x] 4.4 实现 Go result coordinator 的“先查 receipt、再验 current WriteFence/node/instance/Tick、安全字段、最后 transaction commit/ack”流程
- [x] 4.5 增加 unit、race 和真实 MySQL integration tests，覆盖 ack loss、duplicate ResultID、不同 fingerprint、stale assignment、unknown kind、malformed payload、transaction rollback 和 Go restart replay

## 5. 扩展 placement lifecycle 接缝

- [x] 5.1 为 `placement.RuntimeController` 增加 bounded `Drain`，同步 deterministic fakes、contract tests 和注释，不让 placement package 依赖 process、JSON 或 C++ 类型
- [x] 5.2 修改 Sleep 流程为 current recheck、best-effort drain、revoke-before-stop，并表达 drain failure、placement committed 与 cleanup failure 的部分成功结果
- [x] 5.3 修改 Replace/migration 流程为 predecessor current recheck、bounded drain、break-before-make replace、successor remote ready/activate 和 predecessor exact stop
- [x] 5.4 将 `worldAssignmentCoordinator` 从 concrete `*processWorldRuntime` 改为窄 RuntimeInventory/TargetResolver/NodeSelector ports，保持 assignment loss、VisitSession invalidation 和 safe-return owner 不变
- [x] 5.5 扩展 placement unit/fuzz/race tests，覆盖 drain success/timeout/cancel、stale request 不干扰 successor、commit-unknown、ready race、same/cross-node replace 和 cleanup partial success

## 6. 接入 Composition Root 与可观测生命周期

- [x] 6.1 增加严格 `simulationControl` 配置与示例，校验 binary/receipt 路径、instance cap、health interval/timeout、frame/pending/drain/shutdown budgets，拒绝 secret、endpoint 和 silent defaults
- [x] 6.2 新增 `simulation_node` lifecycle component 与专用受监督 task owner，按 storage -> simulation node -> public runtime 顺序启动并在任一部分失败时逆序回滚
- [x] 6.3 将 production placement、world assignment、SimulationTarget 和 result coordinator 接到真实 child adapter，移除 `processWorldRuntime` production wiring 与 fallback，fake 只保留在 `_test.go`
- [x] 6.4 实现 unexpected child failure 撤销 readiness、停止新 target 并触发非零受控关闭；同一 Go process 不静默 respawn 新 node incarnation
- [x] 6.5 实现“停止 public input -> drain/result -> revoke/stop instances -> shutdown child -> storage”的 deadline 顺序，超时只终止精确 child process且不恢复旧 fence
- [x] 6.6 增加 node health、instance/capacity、control queue、request latency、drain、result disposition、process exit 和 shutdown outcome 的低敏 metrics/logs，禁止输出完整 stamp、nonce、payload 或本机 secret path
- [x] 6.7 增加 Composition Root startup rollback、partial graph、task panic、child exit、stderr pressure、shutdown timeout、goroutine/handle/process cleanup tests

## 7. 完成真实进程与故障恢复验收

- [x] 7.1 实现 Go/C++ real-child harness，使用真实 binary/receipt、临时配置和受控 frame proxy 验证 hello、start、status、drain、stop、shutdown 与无 listener/端口
- [x] 7.2 验证 own-world 与 visit-world 解析到同一 current AssignmentStamp/SimulationInstance target，Visitor 不能覆盖 owner、node、instance 或 actor identity
- [x] 7.3 覆盖 start/ready/drain/stop 响应丢失、重复 request、乱序/损坏 frame、stdout 污染、node capacity exhaustion 和 8/9 actor battle admission gate
- [x] 7.4 覆盖 stale assignment、lease expiry、same-node rebuild、foreign-node recovery、C++ crash、Go terminate/restart 和更高 generation/fence reconstruction
- [x] 7.5 覆盖 ResultProposal committed/rejected/replayed、ack loss、ResultID conflict、outbox saturation、drain flush deadline 和未持久 proposal 不得伪装成功
- [x] 7.6 验证现有 VisitSession 33-actor compatibility、HTTPS/WSS/TLS-TCP world/visit API、safe-return 和 client-visible assignment 投影均未被 SimulationTarget 或 8-actor gate改变

## 8. 文档、资格与归档门

- [x] 8.1 更新 `docs/architecture.md`、`docs/gameplay-simulation-architecture.md` 与 `docs/network-transport-architecture.md`，记录 child topology、owner、control authentication、target、result、故障和后续远程 transport 边界
- [x] 8.2 更新 `docs/file-structure.md`、`docs/technology-versions.md`、`docs/network-port-allocation.md`、`server/README.md` 与 simulation README，记录目录、命令、既有 JSON 依赖复用、无端口和启动/关闭方式
- [x] 8.3 更新 `docs/roadmap.md` 的 B0.4 implementation/completion evidence 和 B0.5 精确进入条件，保持 battle wire、Asio/KCP、UDP port、network qualification 与 Unity runtime 门关闭
- [x] 8.4 完成 `tools/simulation-control/simulation-control.ps1 verify`，聚合 schema parity、Go format/test/race、真实 MySQL、C++ Release/ASan/B0.3 verify、real-process fault matrix、cache/secret/scope/docs gates
- [x] 8.5 重跑现有 server v1 完整 qualification 和 client v1 mandatory regression，证明 production child 接线未破坏账号、own-world、visit-world、恢复、UI/Scene 或公开 contract
- [x] 8.6 连续两次生成低敏 B0.4 qualification report，验证 Go/C++ build、model/profile/control schema、migration/fixture digest、当前 server/client v1 报告和结论无漂移且 source corpus只读
- [x] 8.7 同步 `go-simulation-control`、`server-world-instance-placement` 与 `delivery-sequencing` delta 到主 specs，运行 OpenSpec strict、`git diff --check`、注释/TODO/generated/cache/secret 扫描
- [x] 8.8 在全部 tasks 勾选、qualification qualified、主 specs 同步且 strict 通过后准备归档，并保留 change 开始提交作为代码回滚点和 additive receipt migration 的非破坏性回滚说明
