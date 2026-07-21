## Context

TLS/TCP gameplay 当前只在收到业务 envelope 时刷新服务端 read deadline。客户端没有 application heartbeat，因此一个仍在个人世界、控制面 WSS 也健康的玩家，只要 30 分钟没有提交业务消息就会被当作 idle connection 关闭。断线后的显式恢复依次执行 control ready、bootstrap、admission、TCP connect 与 world snapshot，但 presentation 没有覆盖整笔恢复事务的 deadline；同时 gameplay channel 把底层异常直接折叠成低敏枚举，Development 日志无法判断失败阶段。

该问题涉及协议 registry、服务端 transport、客户端 App Scope channel owner 和 PersonalWorld presentation flow。PersonalWorld/VisitSession 最终事实、Scene Scope 与 UI owner 不变。

## Goals / Non-Goals

**Goals:**

- 合法 active gameplay connection 在玩家没有业务操作时仍能证明存活，不触发服务端业务 idle timeout。
- 网络黑洞、server close、heartbeat response 超时与显式恢复失败都在有限 deadline 内收敛到唯一 terminal state。
- UI 只呈现 coordinator/experience 已提交 snapshot，不长期显示无法完成的中间态。
- Development 诊断能够定位 generation 与失败 stage，同时继续保护 credential、payload、endpoint 和服务端内部文本。
- 保持单 reader、单 serialized writer、单 heartbeat owner 与 generation cancellation 不变量。

**Non-Goals:**

- 不实现后台无限自动重试、指数退避、tick 状态修正或离线世界模拟。
- 不改变 VisitSession grace、safe-return、membership、邀请或业务 command 语义。
- 不使用 WSS heartbeat 冒充 gameplay liveness，也不通过 world snapshot 轮询保活。
- 不修改 Scene、Prefab、ScriptableObject、UI 布局、MySQL 或 Redis。

## Decisions

### 1. Heartbeat 是 TLS/TCP GAMEPLAY 上的显式 request/response

在 common owner 范围增加 message `1 GAMEPLAY_HEARTBEAT_REQUEST` 与 `2 GAMEPLAY_HEARTBEAT_RESPONSE`，payload 为项目定义的空 Protobuf message，唯一允许通道为 `TLS_TCP`、auth scope 为 `GAMEPLAY`、QoS 为 `RELIABLE_ORDERED`。request 使用标准 16-byte request correlation，response 必须匹配；heartbeat 不调用 PersonalWorld/VisitSession application service，不修改业务 revision，也不产生业务 PUSH。

选择显式 application heartbeat，是因为 OS TCP keepalive 只证明内核 peer 可达，WSS heartbeat 只证明控制面存活，而复用 `WORLD_SNAPSHOT_REQUEST` 会制造无意义业务读取、污染 rate limit 与 metrics。独立 heartbeat 让 liveness、授权和观测语义可验收。

客户端 active generation 每 15 秒最多发起一个 heartbeat，单次 response deadline 为 10 秒；任一时刻最多一个 heartbeat pending。服务端保留 30 分钟 idle safety deadline，任何合法 C2S envelope（包括 heartbeat）都会自然刷新 read deadline。该比例允许短暂调度抖动，又能在远早于 idle safety deadline 时发现黑洞。

### 2. Heartbeat 与 connection generation 共用生命周期，但不成为第二 writer

heartbeat owner 只调用现有 typed `SendAsync`，因此仍由同一 writer queue 串行编码和写入。它使用 current connection cancellation，close 时先关闭 mutation/heartbeat gate、撤销 generation，再完成 pending 并等待 reader、writer、heartbeat owner。旧 generation 的 delay、response 或 callback 不能影响新 connection。

heartbeat timeout/transport failure 提交一次 `HeartbeatTimeout` 或现有 transport close reason，然后复用统一 unexpected-disconnect 路径。notification 只在所有 socket I/O owner 退出后发布；heartbeat task 不等待包含自身的 task 集，避免自等待死锁。

### 3. 显式恢复是一笔有总 deadline 的事务

`RetryConnectionAsync` 为 control ready、own-world resolution、scene synchronization 和 route commit 建立 45 秒总 deadline。内部 HTTP、connect 与 operation deadline 继续保留，但不能把总时长无限叠加。deadline 到期会撤销当次 presentation/world intent，关闭可能已建立但未提交的 gameplay generation，并提交稳定 `Transport`/`Timeout` 失败 snapshot；按钮恢复可操作，重复点击由 intent gate 拒绝，不创建并行恢复。

恢复期间新建的 control run 使用独立 cancellation owner：在首次进入 `Connected` 前由恢复事务负责在 deadline、caller cancellation 或失败时撤销；首次进入 `Connected` 后所有权转交给 App Scope 的 `ClientControlChannel`，仅由 channel lifetime、明确 shutdown 或后续 transport terminal 结束。`ConnectionLost` route token 只约束页面等待，关闭该 route 不得取消已经提交的 control run，否则会形成“关闭弹窗 → 断开新连接 → 重新打开弹窗”的自激循环。

不增加延时后重试或 tick 修正。成功与失败都由 async operation、cancellation 和 generation event 驱动。

### 4. 公开错误低敏，内部诊断保留 cause 分类

客户端 gameplay channel 产生结构化诊断事件：`component`、`operation`、`generation`、`stage`、稳定 close/failure kind 和异常 CLR type。Development adapter 将其写入 `Player.log`；Release 可以使用 no-op/受控 sink。事件使用普通有界主线程队列且拥塞时允许丢弃，禁止占用 terminal lifecycle 的唯一保留槽。事件禁止携带 exception message、stack 中的 payload、endpoint、ticket、admission 或 session/player/world ID。

服务端继续使用低基数 heartbeat dispatch/close metrics；message ID 来自冻结 registry，close reason 来自固定枚举。日志和 metrics 不增加玩家或连接 identity label。

### 5. 协议按 server-first 顺序部署

先部署支持 heartbeat 的服务端，再发布新客户端。旧客户端连新服务端仍按既有规则运行，最多在 30 分钟无业务帧后断开；新客户端只连接已支持 registry 的服务端。回滚时先回滚客户端，再回滚服务端。协议版本不降级、不探测未知 route，也不在 WSS 双写 heartbeat。

## Risks / Trade-offs

- [每连接固定 heartbeat 增加少量帧和 metric] → payload 为空、15 秒间隔、单 pending，并配置独立低成本 rate policy。
- [主线程长时间冻结可能导致 heartbeat deadline] → heartbeat I/O 不依赖 Unity frame tick；只有 terminal notification 投递主线程，恢复后 snapshot 仍通过 generation gate。
- [混合版本中新客户端连接旧服务端会被 unknown route 关闭] → 强制 server-first 发布，并由 registry/版本交付包控制客户端发布门。
- [恢复总 deadline 过短会把慢依赖判失败] → 45 秒覆盖当前各阶段预算；超时只终止本次 intent，用户可在依赖恢复后再次显式重试。
- [诊断事件本身失败或背压] → 诊断不得阻塞 connection owner；sink 失败被隔离，terminal close 与状态提交优先。

## Migration Plan

1. 合并协议源、registry、fixtures 与双端 catalog/codec 支持，先部署服务端。
2. 验证真实 TLS/TCP heartbeat、idle safety、指标标签与旧客户端兼容。
3. 发布包含 heartbeat owner、恢复 deadline 和 Development 诊断的客户端。
4. Windows Development 双客户端手测挂机、断网、server restart、重复重连、Owner/Visitor safe-return。
5. 回滚按客户端优先、服务端随后执行；无需数据迁移。

## Open Questions

无。heartbeat interval、deadline、message identity 和发布顺序在本 change 内冻结。
