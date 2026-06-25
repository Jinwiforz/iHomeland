## 为什么

iHomeland 在进入服务端实现前，需要先建立一套稳定的项目基线：总体架构、工程标准、路线图、第一里程碑范围，以及长期 OpenSpec 规格入口。

之前的任务清单覆盖了 Go 服务端、协议、Docker、WebSocket、房间、持久化、gRPC 和客户端集成，范围更像项目路线图，不适合作为单个 OpenSpec change 直接 apply。因此本 change 收缩为“架构基线 change”，只做项目文档和长期规格基础，不实现大量代码。

## 变更内容

- 建立项目总体架构文档，说明客户端、网关、房间服务、存储和未来 battle server 的职责边界。
- 建立工程标准文档，说明 Go 命名、目录结构、日志、配置、错误处理、测试和 OpenSpec 使用规则。
- 建立路线图文档，将服务端基础、协议信封、本地基础设施、WebSocket 网关、房间大厅等拆成后续独立 change。
- 明确第一里程碑为“自定义房间大厅”。
- 明确第一里程碑暂不实现匹配系统、MOBA/RTS 高频战斗模拟、独立 battle server 和完整持久化业务。
- 建立主规格入口：`protocol`、`gateway`、`room`、`storage`。

## 能力范围

### 新增能力

- `protocol`: 实时通信协议、Protobuf envelope、协议版本和兼容规则的长期行为约束。
- `gateway`: 实时网关职责、连接生命周期、心跳、超时、session 和消息分发边界。
- `room`: 自定义房间大厅、房间状态机、成员身份、房主权限和重连恢复边界。
- `storage`: MySQL 持久事实来源、Redis 短期运行态、key 规则和幂等写入边界。

### 修改能力

- 无。

## 影响

- 当前 change 从“全量实现路线图”调整为“项目架构基线”。
- 具体实现将拆入后续 change：`add-server-foundation`、`add-protocol-envelope`、`add-local-infra`、`add-websocket-gateway`、`add-room-lobby`。
- `docs/roadmap.md` 承载长期路线，避免 `tasks.md` 变成半个项目的开发清单。
- 第一阶段采用单进程优先，通过 Go interface 和 in-process adapter 保持边界；暂不急于拆分 gRPC。
