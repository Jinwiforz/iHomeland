## Context

B0.3 已在 Windows x64 reference environment 交付 `ihomeland-sim-server`、`SimulationInstance`、固定 Tick、最小 ECS、Jolt/Detour adapters、16-Tick history 与低敏 evidence，并以同源 Release/ASan、10 个 model cases、连续 determinism 和 1/5/8 actor benchmark 形成 `implementation-qualified-windows-x64` 证据。该二进制目前只有离线 `--smoke` 模式，不接受长期 lifecycle command。

Go production graph 仍由 `server/internal/app/processWorldRuntime` 用内存 map 实现 `placement.RuntimeController`。它能证明 v1 的 assignment/lease/fencing 编排，但不会创建 C++ runtime，`RuntimeNodeID` 只是 Go 进程 incarnation，世界进入成功也不代表存在 `SimulationInstance`。现有 `worldAssignmentCoordinator` 还直接依赖该 concrete runtime 查询本地 stamp，使 production wiring 无法替换为跨进程 owner。

B0.4 必须跨 Go、C++、placement、Composition Root、MySQL receipt、fixtures 和进程资格工具工作，同时遵守：

- Go 继续拥有 PersonalWorld、WorldInstance placement、VisitSession、admission、持久事实与 settlement；C++ 只拥有 assignment 内的高频运行态。
- 完整 `AssignmentStamp` 是 start、drain、stop、target 和 result 的 fence；客户端 identity 或 C++ 自报字段不能覆盖它。
- 当前没有跨主机独立扩缩容、服务发现或团队 ownership 证据，不得据此引入 gRPC 或新的服务网格。
- B0.5 前不得创建 battle wire、numeric message ID、HTTPS battle ticket、Asio/KCP、UDP listener、AEAD 或 production UDP 端口。
- B0.2 只资格了单实例最多 8 actors；当前 33 actors VisitSession compatibility 不能被 B0.4 静默改成产品容量。
- 正常启动、部分失败、C++ crash、Go restart 与 shutdown 必须保持现有一次性 readiness、逆序 lifecycle、lease expiry 和 break-before-make 语义。

## Goals / Non-Goals

**Goals:**

- 让唯一 Go Composition Root 启动、验证、监督并关闭一个独立 C++ `SimulationNode` 进程。
- 通过 closed、跨语言、进程私有控制契约交付 node hello/health/capacity 和 instance start/drain/stop/status。
- 用真实 C++ ready receipt 替换 production `processWorldRuntime`，同时保持 placement 的纯 Go fake/unit-test 边界。
- 为 current active assignment 建立 generation-bound `SimulationTarget`，给 B0.5 提供不含 endpoint/credential 的可信输入。
- 建立有界 ResultProposal outbox、Go owner 裁决、MySQL immutable receipt 与 response-loss replay。
- 对 C++ crash、Go restart、stale assignment、容量、协议损坏、重复 result 和 shutdown 形成真实进程证据。

**Non-Goals:**

- 不支持跨机器 SimulationNode、动态服务发现、独立集群调度、gRPC 或 control-plane TLS listener。
- 不创建 battle `.proto`、公开 message registry、客户端 wire、UDP/KCP、安全 session、端口或 Unity runtime。
- 不定义剑、扇子、怪物、Boss 的奖励、资产、掉落或 PersonalWorld mutation 结算规则；首个登记 result kind 只覆盖 simulation lifecycle summary。
- 不改变 Account、Session、VisitSession、world admission 或现有 HTTPS/WSS/TLS-TCP wire。
- 不让 C++ 直连 MySQL/Redis，不把 ECS/history/evidence 当作持久世界事实，也不承诺 C++ crash 后恢复内存 simulation timeline。
- 不实现多 node 装箱策略、热迁移或 in-process child 自动复活；这些必须由实际容量或可用性证据触发后续 change。

## Decisions

### 1. 初始 topology 是一个 Go 进程监督一个多实例 C++ child

Composition Root 新增独立 `simulation_node` lifecycle component。它在 public runtime 构图前启动一个精确路径的：

```text
ihomeland-sim-server --control-stdio
```

一个 child 表示一个 `SimulationNode` incarnation，并可在配置和已资格预算内承载多个 `SimulationInstance`。Go 为每次 child 启动分别生成不可复活 `SimulationNodeID` 与 placement `RuntimeNodeID`；两者当前一对一，但前者标识 C++ process incarnation，后者保持 placement location 语义，避免未来一个 runtime node 与多个 simulation workers 出现时混淆身份。

选择 child supervision，而不是先建立远程 TCP/gRPC service，原因是当前首个可验证故障边界是“C++ crash 不拖垮 Go 内存安全，Go 能 fence 并重建 assignment”，不是跨机调度。Child process 已提供地址空间、allocator 和 crash 隔离，同时不引入 listener、证书、服务发现、端口或新的 runtime dependency。

备选方案：

- **保留进程内 map**：不能运行权威模拟，拒绝。
- **gRPC/HTTP/TCP control service**：需要跨机扩缩容、认证、端口、部署和 C++ runtime dependency 证据，当前过早。
- **Windows named pipe server**：允许 child 独立于 Go 存活，但增加 ACL、name discovery、server impersonation 和重连 surface；当前 Go restart 必须重建 generation，不需要保留旧 simulation timeline。
- **每个 instance 一个 child**：简化 instance manager，但与后续单 UDP multiplexer/node capacity 方向冲突，并造成无 profile 依据的进程开销。

### 2. IPC 使用 inherited anonymous pipes 与 length-prefixed canonical JSON

Go 为 stdin、stdout、stderr 分别创建匿名 pipe，只把需要的 child handles 继承给精确创建的进程。控制 frame 格式固定为：

```text
uint32_be frame_bytes
UTF-8 canonical JSON frame
```

Frame 顶层字段固定为 `schemaVersion`、`sessionNonce`、`sequence`、`requestId`、`kind` 与 `payload`。所有可能超过 JSON 精确整数范围的 ID、Tick、generation、fence 和 sequence 使用规范十进制字符串；unknown/duplicate field、非最短十进制、前导零、非 UTF-8、非 canonical object order 或 trailing bytes 均拒绝。初始 hard limit 为 64 KiB/frame，pending requests 与 result outbox 各不超过 256 entries；具体更小 payload limit 由 message schema登记。

Go 拥有唯一 serialized writer、唯一 reader 与 pending correlation；C++ 拥有唯一 control reader、唯一 serialized stdout writer。Windows C++ child 必须在首帧前把 stdin/stdout 切换为 binary mode，避免 CRT 文本模式把 4-byte length prefix 中的 `0x0A` 改写成 CRLF；真实 child 回归必须覆盖会产生该低字节的 frame 长度和至少 8 个并存 instance。stderr 进入独立有界低敏日志 pump，stdout 出现任何日志文本即协议失败。Context cancellation 只取消 Go caller 等待；已发送 request 仍以 request ID/status query 收敛，不能换 ID 猜测结果。

本地“认证”由四层共同构成：

1. Go 在 spawn 前校验 binary SHA-256、B0.3 build identity 和 qualification gate receipt；
2. 控制 handles 只由受信 parent 创建并继承，不存在可连接 listener；
3. Go 通过 OS child process handle 绑定 PID/exit status；
4. 每次启动生成 256-bit bootstrap nonce，hello 和所有 frame 都绑定 nonce 与单调 sequence。

Nonce 不是跨网络 credential，不能把该方案迁移到 TCP 后继续宣称安全。若未来需要独立主机，必须新 change 选择相互认证 transport。

不使用 Protobuf，是因为 control 是低频、私有且没有客户端 consumer；B0.3 已锁定 nlohmann/json，而引入 C++ Protobuf runtime 只为内部 lifecycle 会扩大依赖面。Closed JSON schema、canonical fixtures 和双方 parser parity 提供所需契约强度。

### 3. Control schema 有独立 source of truth，不能混入 battle wire registry

新增：

```text
shared/contracts/fixtures/simulation-control/
  schema/
  cases/
  manifest.json
  canonical-golden.json
tools/simulation-control/
```

初始 message inventory 仅包含：

- `node.hello.challenge` / `node.hello.receipt`
- `node.health.query` / `node.health.receipt`
- `instance.start` / `instance.ready`
- `instance.drain` / `instance.drained`
- `instance.stop` / `instance.stopped`
- `instance.status.query` / `instance.status.receipt`
- `result.proposal` / `result.ack`
- `node.shutdown` / `node.stopped`

每个 kind 登记 direction、max bytes、timeout class、request/replay identity 和允许 lifecycle state。它们没有 public numeric message ID、channel、QoS lane 或 client compatibility 承诺，因此不进入 `messages.json`、`routes.json` 或 `battle/wire/`。

Go 与 C++ 都只能消费 manifest/schema/golden；不得分别维护第二份枚举、默认值或 expected digest。Validator 双向检查 schema↔inventory↔cases↔golden completeness，连续运行不得改写 source。

### 4. B0.3 qualification receipt 是 node hello 的硬前置

发布/测试 binary 必须与旁置的 `ihomeland-build-identity.json` 和
`qualification-gate-receipt.json` 同源。Go spawn 前校验：

- binary digest 与 receipt 中 target/source identity；
- Release CI 与 ASan source identity 一致；
- compiler/dependency/model/profile/config schema digest；
- Windows x64 `implementation-qualified` 状态；
- control schema version 是 Go 支持的 exact version。

Child hello 再回报编译进 binary 的 build/model/profile identity。文件 receipt、binary hello 与 Go expected 三方不一致时 component 启动失败，不能退回 `processWorldRuntime` 或自动重建未资格 binary。

`tools/cpp/cpp.ps1 verify` 继续是 C++ core 资格 owner；B0.4 的 control 工具只能验证/消费其 receipt，不生成替代 B0.3 结论。

### 5. SimulationNode registry 是进程内 runtime owner，不进入 Redis

`server/internal/simulationcontrol` 定义 node/instance/result 消费侧 contracts、严格 value types 和 application policy；`server/internal/simulationcontrol/process` 实现 child/pipe adapter。Registry 只保存当前 child incarnation、health、draining、instance bindings、qualified actor capacity 和 request replay observations。

Node registration 是 hello 成功后的进程内线性化点。Health 同时消费 child process liveness 和定期 `node.health` receipt；默认 query interval/timeout 从严格配置读取并满足 `timeout > interval` 和 bounded maximum。Lifecycle request 共用唯一串行 operation lane；定时 health 仅在 lane 空闲时发送，lane 被 start/drain/stop 占用时记录固定低基数 `busy` 并跳过本轮，不能在 lifecycle 后排队并以过期 deadline 误杀健康 node。正在返回的 lifecycle receipt、child process liveness 与 session hard deadline 继续提供监督，真正的 EOF、deadline 或 protocol error 仍是 terminal failure。Self-reported capacity 取以下最小值：

```text
configured node instance cap
C++ compiled hard cap
B0.3 measured resource cap
当前可用 slot
```

每个 instance actor cap 固定不超过 8。Registry 不写 Redis，因为 child 生命周期严格属于当前 Go process，Redis 中的旧 node presence 会制造可复活资格。跨 Go process node discovery 属于未来远程控制 change。

Unexpected exit、EOF、health deadline 或 protocol error 是 terminal supervised task failure：registry 先原子进入 unhealthy/draining，清除 target 可用性，再通知 root 撤销 readiness 并非零关闭。Readiness 不从 draining 恢复，因此同一 Go process 不自动 spawn 新 incarnation。

### 6. C++ 新增窄 SimulationNode composition，不修改 gameplay owner

`simulation/` 新增：

```text
include/ihomeland/sim/control/   # 项目 value contract
src/control/                     # frame/schema/dispatcher
src/node/                        # instance registry 与 result outbox
```

`ihomeland_sim_control_adapter` 私有链接现有 JSON adapter 和 core；`ihomeland-sim-server --control-stdio` 组装 realtime steady TickClock、Jolt/Detour adapters、registered config/nav assets 和多个 `SimulationInstance`。Control thread 只把 lifecycle command 送入 node owner，不直接写 ECS/physics；instance worker 继续是唯一 Tick writer。

Instance registry 以 `WorldInstanceID` 索引，并保存完整 AssignmentStamp、start request ID、SimulationInstanceID、mapping generation 和 lifecycle。`SimulationInstanceID` 由 C++ CSPRNG 创建且不能由 Go/客户端指定；重复 exact start 返回原 ID，不同 stamp 复用 WorldInstanceID 返回 identity conflict。

Start 的成功边界是全部 runtime resources 初始化、worker Running、instance 登记完成和 exact ready receipt 可 replay。Drain 关闭 command ingress，在 deadline 内推进已有 batch、flush 已登记 result proposal 并进入 drained。Stop 只接受 exact stamp；missing 只有 replay cache/status 能证明相同 instance 已停止时才幂等成功。

本 change 使用登记的 `control-baseline-v1` 初始 config/map/nav/physics identity 验证 lifecycle，不引入产品内容或奖励配置。

### 7. Placement 保持线性化 owner，runtime adapter 增加有界 Drain

`placement.RuntimeController` 扩展为：

```go
type RuntimeController interface {
    Start(context.Context, AssignmentSnapshot) error
    Drain(context.Context, AssignmentStamp) error
    Stop(context.Context, AssignmentStamp) error
}
```

接口仍不暴露 JSON、process handle、SimulationInstance C++ type 或 endpoint。Remote adapter 在内部验证 ready/drain/stop receipt 并维护 stamp→SimulationInstance binding。`worldAssignmentCoordinator` 改依赖窄 `RuntimeInventory`/`SimulationTargetResolver`，不再依赖 concrete `*processWorldRuntime`。

Start 流程保持：

```text
store acquire starting
-> C++ start/ready exact stamp
-> store activate exact stamp
-> publish internal target
```

Sleep/Replace 增加 best-effort bounded drain：

```text
verify expected current
-> C++ drain while lease remains current
-> flush/ack bounded results or reach deadline
-> store revoke/replace (fence linearization)
-> C++ stop predecessor
-> start/activate successor when applicable
```

Drain 不能成为无限等待，也不能续租。Drain error、caller cancellation 或 deadline 后，application 使用独立 bounded cleanup context 继续 revoke/replace 和 stop；安全优先级是使旧 fence 失效。Stale expected stamp 在调用 C++ drain 前重新读取 current，避免延迟请求干扰 successor。

`processWorldRuntime` 从 production source/wiring 移除；placement 单元测试的 fake 只保留在 `_test.go`。如果迁移期间需要 characterization adapter，也必须受 build tag/test package 限制，最终 production graph 不得有 fallback 开关。

### 8. SimulationTarget 与现有 world admission 完全分层

`SimulationTarget` 是 Go 内部 immutable value：

```text
AssignmentStamp fingerprint
RuntimeNodeID
SimulationNodeID
SimulationInstanceID
mapping generation
model/profile/config identity
qualified actor capacity
target revision
```

Resolver 每次读取 registry 与 current placement；不缓存永久布尔资格。只有 assignment active/lease valid、node healthy、instance ready 和全部 binding 相等才返回。Replace、expiry、drain、node loss 或 stop 会使旧 target revision 失效。

B0.4 不把 target 写入现有 HTTP world bootstrap、WorldAdmission 或 TLS/TCP payload，不签发 battle ticket，也不提供 endpoint。B0.5 才能在同一 target 基础上绑定 advertised UDP endpoint、安全 session 和一次性 ticket。

VisitSession 保持当前最大容量和 v1 联机行为。后续 battle actor 加入必须通过独立 `SimulationAdmissionCapacity` port；第 9 个及之后 actor 对 battle target fail closed，但不踢出合法 Visitor，也不修改 VisitSession revision。

### 9. ResultProposal 使用先查 receipt、再验 fence、同事务决定

初始 result catalog 只登记 `simulation.lifecycle.summary.v1`。它证明完整控制链的 result handshake，不声明奖励、资产或 PersonalWorld mutation。未来 gameplay result kind 必须由拥有相应持久事实的独立 change 添加 committer 和 scenarios，不能用 generic map 或 callback 绕过 owner。

C++ node 拥有 node-global 最多 256 entries 的内存 outbox。Node-global 边界约束 child 总内存，并让 instance stop 后的 proposal 继续等待 ack；当前每次 lifecycle drain 每 instance 只产生一个 summary。Proposal 包含 ResultID、完整 stamp、SimulationInstanceID、Tick start/end、kind、payload/evidence SHA-256 和 bounded low-sensitive payload。C++ 重发同一 immutable proposal，收到 terminal ack 后删除；drain 在停止 worker 前先预留 outbox 容量，容量满必须保持未 drained，释放容量后可重试，不能丢弃后伪装成功。

Go 处理顺序：

1. strict decode 与 result kind/size/security validation；
2. 按 ResultID 查询 receipt；
3. Go 按全部 immutable 字段重算 fingerprint，任何不一致在进入 store 前失败；
4. 已存在且完整 proposal 完全相同则返回 `replayed` + 原 committed/rejected disposition；
5. 已存在但任一 immutable 字段不同则 identity conflict；
6. 首次 proposal 重新读取 current active assignment、`WriteFence`、node/instance binding 与 Tick policy；
7. 在 MySQL transaction 内调用该 kind 的 owner committer，并写 immutable receipt；
8. transaction commit 后才发送 terminal ack。

新增 additive migration `simulation_result_receipts`，至少保存 ResultID、proposal/assignment fingerprint、SimulationInstanceID、kind、Tick range、payload/evidence digest、disposition/reason 和 UTC decision time。表与每列使用中文短注释；不保存 raw token、ticket、AEAD key、账号凭据、完整 ECS snapshot 或未登记个人数据。

Response loss 由 receipt replay 解决。Go 按 kind 重算 payload/evidence digest；首个 lifecycle summary evidence 绑定 build/model/profile/config/navigation/physics、mapping generation、seed、actor capacity 与 terminal Tick。Go proposal inbox 按 ResultID 对 exact replay 去重、拒绝冲突，并在 receipt 与 ack 都成功前保留队首所有权，短暂 store/control failure 不能把未终结 proposal 静默出队；C++ 同时保留未 ack outbox entry，并以最多 256 entries 的 ack replay cache 接受 exact 重复 ack、拒绝 fingerprint 漂移。C++ crash 会丢失尚未提交的内存 outbox，这是明确的可诊断失败，不得假定结果已提交；已提交 receipt 始终防止重复 side effect。需要 crash-durable C++ outbox 时必须由真实 settlement loss tolerance、磁盘 retention 和数据保护要求驱动后续 change。

### 10. Lifecycle component 顺序让输入、结果、fence 和 storage 可安全关闭

启动顺序调整为：

```text
diagnostic
-> MySQL/Redis
-> simulation_node
-> public_runtime
-> ready
```

因此 shutdown 逆序为：

```text
stop public listeners / new world and visit commands
-> drain active simulation instances and settle bounded results
-> revoke placements and stop instance bindings
-> stop simulation node / close pipes / wait child
-> close MySQL/Redis
-> diagnostic
```

Child 在 stdin EOF、`node.shutdown` 或 parent stop deadline 时停止接受新 command、drain instances 并退出；超时后 Go 终止精确 child process，记录稳定 reason，并继续关闭 storage。正常 stop、startup rollback 和 unexpected child exit 都使用同一 owner path，不能遗留 goroutine、pipe、process 或 target。

Go restart 不恢复旧 child timeline：旧 child 看到 pipe EOF 后退出；新 Go process 创建新 node identities，解析 Redis/MySQL placement，发现 foreign/expired old node 时按既有 replacement 规则产生更高 generation/fence。已存在 result receipt 仍可解析 duplicate；未有 receipt 的旧 proposal不被推定成功。

### 11. Qualification 使用真实 child，不以 fake 或单侧绿色替代

`tools/simulation-control/simulation-control.ps1` 是 B0.4 唯一完整资格入口，聚合：

- control schema/manifest/canonical golden validator 与失败回归；
- Go value/policy/store unit、fuzz、race 与真实 MySQL receipt integration；
- C++ frame/node/outbox unit、contract、ASan 与 B0.3 full verify；
- real child process start/ready/drain/stop/status 和 response-loss proxy；
- own-world/visit-world 使用同一 active SimulationTarget；
- stale ready/drain/stop、capacity 8/9、malformed frame、stdout corruption；
- duplicate ResultID、fingerprint conflict、commit-unknown 与 ack loss；
- child crash、Go terminate/restart、lease/replacement 和 shutdown deadline；
- 现有 server v1 qualification、client v1 mandatory regression、scope/secret/cache/docs/OpenSpec gates。

报告绑定 Go binary、C++ binary/build receipt、model/profile/control schema、migration catalog 和 fixture digest。运行时 binary、临时 MySQL/Redis、配置、nonce、日志与报告只写入 ignored `.local/simulation-control/<run-id>/`；tracked freeze 只保存低敏 canonical digest 和门禁结论。

## Risks / Trade-offs

- [Child-process stdio 不能跨机器独立扩缩容] → 当前只声明本机进程隔离；出现跨主机容量、ownership 或故障域证据后，以独立 change 选择认证远程 transport。
- [Canonical JSON parser 在 Go/C++ 间产生整数或字段差异] → 大整数统一十进制字符串，closed schema、canonical golden、双向 parity 和 unknown-field negative gate 同时约束双方。
- [Control stdout 被库日志污染导致整个 node 失败] → stdout 专用于 frame、stderr 专用于日志，architecture test 扫描所有 control composition 输出，任何污染 fail closed。
- [Drain 在 revoke 前延长 stale writer 存活] → drain 只在 current lease 内有界执行，不续租；deadline 后无条件进入 revoke/replace，持久提交仍重验 WriteFence。
- [C++ crash 丢失未 ack 的内存 result] → 不产生假 committed；Go receipt 保证已提交结果幂等，未提交 loss 进入低敏 evidence。需要 crash-durable outbox 时单独设计 retention、加密和磁盘预算。
- [新增 MySQL receipt 形成没有业务价值的万能结果表] → catalog 初始只允许 lifecycle summary；每个新 kind 必须有明确 owner/committer/spec，unknown kind fail closed，table 只保存 idempotency receipt 而不是 opaque业务 payload。
- [C++ 成为 public runtime 强依赖后扩大 v1 启动成本] → binary/receipt 在配置阶段严格验证，component 启动失败完整回滚；qualification 重跑 v1 全矩阵，最终 production 不保留 silent in-process fallback。
- [一个 child 故障触发整个 Go process restart，降低局部可用性] → 这是符合当前一次性 readiness 与单 node证据的保守策略；多 node 故障隔离和在线 re-registration 等实际容量/可用性证据成立后再设计。
- [8-actor battle cap 与 33-person VisitSession compatibility 不一致] → 只 gate SimulationTarget 的 battle admission，不修改 VisitSession、邀请或现有 v1 world/visit 事实。

## Migration Plan

1. 先提交 control schema/fixtures/validator、Go/C++ value contracts 和失败回归，不改变 production wiring。
2. 添加 additive MySQL receipt migration、repository/committer tests 和中文 schema comments；现有数据无需 backfill。
3. 在 C++ 增加 `--control-stdio`、SimulationNode registry/outbox 和分层 CTest，同时保持 B0.3 `--smoke` 与 qualification digest 可重新生成。
4. 实现 Go child component、registry、runtime adapter、target resolver 和 result coordinator，以 fake/real child contract tests验证。
5. 修改 placement Drain contract 与 coordinator narrow ports，迁移 production Composition Root；同一提交移除 `processWorldRuntime` production fallback。
6. 运行 B0.3 full verify、Go format/test/race、storage integration、server/client v1 regression、真实 process fault matrix、OpenSpec strict 和文档门禁。
7. 只有完整 B0.4 report 通过后，才更新路线图状态并允许提出 B0.5。

部署回滚使用前一版本 Go binary 和配置，恢复原 v1 in-process runtime wiring。已应用的 `simulation_result_receipts` 是 additive、无 backfill 的 owner table，回滚时保留为空或只读，不执行 destructive down migration；重新部署 B0.4 时按既有 immutable receipt 继续解析。未应用 migration 的环境可随代码回滚直接移除本 change。

## 最终架构审计结论

- Production graph 只有 `simulation_node` 一个 C++ child/process owner，Go application 与 transport 只消费窄 `placement.RuntimeController`、`SimulationTarget` 和 result contracts；`processWorldRuntime` 仅保留为 `_test.go` fake，不存在运行时 fallback 或双 owner。
- `AssignmentStamp`、RuntimeNodeID、不可复活 node/instance identity、mapping generation、seed、config/navigation/physics、B0.3 build/model/profile 与 actor cap 构成同一 exact start binding。Terminal child、drain、replacement、lease 失效或 stop 会立即撤销 target；未知 stop 和已退休 WorldInstanceID 不解释为成功或复活。
- C++ target identity 由 `tools/cpp` 在锁定 source/toolchain manifest 后计算，并在同一 build tree 二次 configure 嵌入 `ihomeland-sim-server`；node hello、Go 配置与真实子进程测试消费该构建产物身份，不保留会与 qualification report 脱节的源码魔法常量。
- Result path 不是自报摘要直通 MySQL：双方重算 proposal fingerprint，Go 再按 kind 重算 payload/evidence digest；Go inbox、MySQL receipt、C++ outbox 与 ack replay 分别承担有界去重、持久幂等和响应丢失恢复。任何 ResultID、fingerprint、evidence、assignment 或 instance 漂移均 fail closed。
- 正常关闭顺序固定为 public input/semantic workers → drain/result → placement revoke → exact instance stop → node shutdown → storage；deadline 或依赖失败仍优先撤销 fence，未持久 result 明确记录为 incomplete，不伪装 committed。
- Windows child 使用独立 process group，服务端收到 Ctrl-Break/SIGTERM 等外部关闭请求时只由 Go parent 接收并转换为上述协议关闭；control child 不得绕过 `node.shutdown` 被同一 console signal 提前终止。强制回收仍只使用 parent 持有的精确 process handle。
- 同一 PersonalWorld 的 resolve/orphan-cleanup/activate 收敛使用可取消、引用归零即删除的临时 lane；它不全局串行不同 world，但禁止并发 bootstrap 的 stale `not-found` 观察停止另一请求刚启动且尚未发布 active 的 runtime。
- 本 change 不开放 C++ listener，不引入 gRPC/UDP/KCP、battle wire、客户端 endpoint、Room/Party/ActivityInstance、奖励结算或跨服能力。8 actor 是 battle simulation qualification cap，不改变 33 人 VisitSession 的社交兼容边界。
- B0.4 资格结论必须同时绑定本次 B0.3 Release/ASan、真实 child、真实 MySQL、server-v1 和 client-v1 报告；旧报告、占位 digest、仅自动客户端证据或 Go 进程内 fake 均不能满足完成条件。

## Open Questions

无阻塞问题。跨主机 control transport、多个 SimulationNode 的在线调度、crash-durable C++ result outbox、真实 gameplay result kind 和 UDP endpoint 均有明确后置门，不在 B0.4 实现期间临时决定。
