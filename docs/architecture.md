# iHomeland 总体架构

## 架构定位

iHomeland 第一阶段采用“单进程优先、边界清晰、后续可拆分”的服务端架构。早期目标不是一次性搭建完整 MMO/MOBA 后台，而是先验证在线游戏最核心的链路：服务端启动、健康检查、版本接口、实时连接、Protobuf 信封、房间大厅、准备流程、退出流程、房主转移和断线重连。

第一里程碑固定为“自定义房间大厅”。暂不实现匹配系统、MOBA/RTS 高频战斗模拟、独立 battle server、跨服、观战、回放、排位和完整经济系统。

## 逻辑组件

### Client

客户端未来可选择 Unity 或 Godot。客户端只依赖 Protobuf 协议和传输抽象，不要求服务端包含任何引擎专用逻辑。

客户端职责：

- 建立 WebSocket 或后续 TCP 连接
- 发送和接收 Protobuf envelope
- 处理心跳、断线、重连和协议错误
- 展示房间大厅、成员、座位、阵营和准备状态
- 后续根据 battle server 设计接入战斗同步

### Gateway

网关负责实时连接和协议入口，不承载复杂业务状态机。

网关职责：

- 管理 WebSocket/TCP 连接
- 解码和编码 Protobuf envelope
- 校验协议版本
- 维护连接级 session
- 处理心跳、空闲超时和断开清理
- 将业务消息分发到 room、account、match 等模块接口

网关不得直接修改房间内部状态，必须通过房间服务接口调用。

### Room Service

房间服务是第一里程碑的核心业务模块。

房间服务职责：

- 创建房间
- 加入和退出房间
- 管理成员、座位、阵营、准备状态和房主
- 执行房间状态机迁移
- 处理断线保留和重连恢复
- 产出房间生命周期事件

房间服务第一阶段运行在服务端进程内，通过 Go interface 被 gateway 调用。暂不拆分独立 gRPC 服务。

### Storage

存储层分为 MySQL 和 Redis。

MySQL 用于持久事实数据：

- 玩家基础资料
- 房间摘要
- 对局摘要
- 配置快照
- 后续持久化业务数据

Redis 用于短期运行态数据：

- session
- presence
- room index
- reconnect token
- match queue
- lock
- rate limit

第一里程碑可以先使用内存 repository 验证房间逻辑。本地 MySQL 和 Redis 基础设施已经由 `local-infra` 规格约束，可通过 Docker Compose 或本机安装服务提供；业务读写接入仍由后续 `add-persistence-boundaries` 推进。

### Future Battle Server

未来 battle server 是独立架构层，不属于第一里程碑。

进入 battle server 前必须重新设计：

- tick rate
- 模拟模型
- 服务端权威
- 输入校验
- 同步和校正策略
- 兴趣管理
- 断线重连
- 观战和回放
- 反作弊
- 进程放置和区域路由

## 第一阶段服务边界

```text
Client(Unity/Godot)
  |
  | WebSocket + Protobuf envelope
  v
Gateway
  |
  | Go interface, in-process adapter
  v
Room Service
  |
  | repository/cache interface
  v
Storage Adapter
  |
  +-- Redis, later
  +-- MySQL, later
```

## 设计约束

- 第一阶段优先跑通端到端链路，不追求过早微服务化。
- Gateway 与 Room 必须通过接口解耦。
- Room 状态机必须能在无网络环境下测试。
- gRPC 是后续拆分部署的工具，不是第一阶段默认复杂度。
- WebSocket 是第一阶段默认传输；TCP 后续复用同一 envelope。
- 所有协议行为必须能由 Protobuf schema 和 OpenSpec spec 追溯。
