## Why

Account、Session、PersonalWorld、Placement、VisitSession、HTTP、WSS 与 TLS/TCP 已分别具备可验收能力，但 production Composition Root 仍未把它们连接成可运行的 own-world / visit-world 服务端竖切。现在需要补齐 WorldInstance 运行态、连接生命周期、语义 deadline 与跨通道副作用，使第一业务里程碑从“组件可用”推进到“服务端业务闭环可用”。

## What Changes

- 在 production graph 中构造 Placement service 与进程内 WorldInstance runtime owner，使 own-world bootstrap 能幂等确保 current active assignment，而不是只读取可选 assignment。
- 连接 PersonalWorld、Placement、VisitSession 与 HTTP/WSS/TLS-TCP 编排，完成 invite、accept、admission、join、leave、kick、close、断线恢复和陈旧 admission 拒绝的端到端路径。
- 增加有界、受监督的语义 deadline 与 reconciliation owner，处理 placement lease、invite、reservation、Owner/Visitor reconnect grace 和 VisitSession expiry；陈旧任务必须由完整 identity/generation/revision 条件安全拒绝。
- 在事实提交后投递既有 WSS/TCP push，精确处理 assignment change、VisitSession snapshot/close 与 Visitor safe-return；网络副作用失败不得回滚、伪造或重复提交领域事实。
- 将 TCP 连接建立/断开转换为受信 application lifecycle 事件，确保 Owner/Visitor grace 只作用于当前 binding，主动 leave/close/safe-return 与进程 draining 不被误判为意外断线。
- 增加使用 production Composition Root、真实 MySQL/Redis 与临时 TLS 的竖切 integration tests，并覆盖 Redis 运行态丢失与进程重建后的安全恢复边界。
- 保持现有协议消息、公开 endpoint 与 storage ownership 不变；独立 Go 协议资格客户端、Unity 接入、Room、Party、ActivityInstance、战斗与 UDP/KCP 仍属于后续 change。

## Capabilities

### New Capabilities

- `server-personal-world-slice`: 定义 own-world / visit-world production 编排、WorldInstance 运行态、语义 deadline、连接生命周期、跨通道通知、安全返回与恢复验收边界。

### Modified Capabilities

- `server-http-bootstrap`: 将 own-world bootstrap 从“仅创建世界并读取可选 assignment”提升为“幂等确保 active assignment 后返回可准入投影”。

## Impact

- 主要影响 `server/internal/app`、`server/internal/placement`、`server/internal/visitsession`、`server/internal/transport/wscontrol`、`server/internal/transport/tcpgameplay`、相关配置、观测与 storage integration harness。
- 复用既有 MySQL/Redis schema、OpenAPI/Protobuf 与 message registry；不新增公开协议字段、message ID、listener、持久表或 Redis 事实 owner。
- production 生命周期会新增有界后台 owner，并调整启动回滚、readiness 与关闭顺序；资源预算、deadline、日志与 metrics 必须保持低敏和可观测。
