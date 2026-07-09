# iHomeland 总体架构

## 架构定位

iHomeland 第一阶段采用“单进程优先、边界清晰、后续可拆分”的服务端架构。早期目标不是一次性搭建完整 MMO/MOBA 后台，而是先验证在线游戏最核心的链路：服务端启动、健康检查、版本接口、实时连接、Protobuf 信封、房间大厅、准备流程、退出流程、房主转移和断线重连。

第一里程碑固定为“自定义房间大厅”。暂不实现匹配系统、MOBA/RTS 高频战斗模拟、独立 battle server、跨服、观战、回放、排位和完整经济系统。

## 逻辑组件

### Unity Client

第一阶段客户端使用 Unity。Unity 客户端只依赖 Protobuf 协议和传输抽象，不要求服务端包含任何引擎专用逻辑。

Unity 客户端职责：

- 建立 WebSocket 或后续 TCP 连接
- 发送和接收 Protobuf envelope
- 通过账号会话完成注册、登录、登出、会话恢复和玩家身份持有
- 处理心跳、断线、重连和协议错误
- 展示房间大厅、成员、座位、阵营和准备状态
- 后续根据 battle server 设计接入战斗同步

当前客户端已经具备基础 UI 和场景链路：

```text
MainScene
  -> LoadingPage
  -> LoginPage
  -> HomePage
  -> LoadingPage
  -> BattleScene
```

该链路目前只表示客户端外层生命周期已经跑通，不代表第一里程碑可以直接进入战斗。当前 `Start Game` 已进入 `RoomPage` 房间大厅流程；在第一里程碑完成前不得把正式玩法推进到 battle server 或高频战斗模拟。

### Gateway

网关负责实时连接和协议入口，不承载复杂业务状态机。

网关职责：

- 管理 WebSocket/TCP 连接
- 解码和编码 Protobuf envelope
- 校验协议版本
- 维护连接级 session
- 绑定账号会话确认后的玩家身份
- 使用连接级玩家身份授权房间大厅请求，拒绝未登录或 payload `player_id` 与 session 身份不一致的请求
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

### Account Service

账号服务是房间大厅端到端接入前的身份边界。

账号服务职责：

- 校验第一阶段注册和登录请求
- 生成或查询稳定玩家身份
- 签发、恢复和失效 session token
- 将登录结果返回给 gateway 绑定连接身份
- 为客户端 `AccountSystem` 提供真实登录和登出状态来源

账号服务第一阶段运行在服务端进程内，通过 Go interface 被 gateway/app 组合调用。暂不实现密码找回、第三方登录、好友、背包、经济或完整权限系统。

### Storage

存储层分为 MySQL 和 Redis。

MySQL 用于持久事实数据：

- 玩家基础资料
- 第一阶段账号基础资料
- 房间摘要
- 对局摘要
- 配置快照
- 后续持久化业务数据

Redis 用于短期运行态数据：

- session
- account session
- presence
- room index
- reconnect token
- match queue
- lock
- rate limit

账号会话 runtime 使用真实 MySQL/Redis：玩家账号资料写入 MySQL `account_player`，短期 session token 写入 Redis `ih:{env}:account:session:{sessionToken}`。第一里程碑的 room service 仍可使用进程内 repository 验证房间状态机；房间摘要、presence、room index 和 reconnect token 的真实持久化由后续房间存储 change 推进。Room service 只能依赖 storage repository/cache interface，不能直接依赖 Redis 或 MySQL client。

当前 storage 边界约束：

- MySQL 保存第一阶段最小账号事实，并预留房间摘要和后续对局摘要。
- Redis 保存账号 session 等短期运行态，并预留 presence、room index、reconnect token、lock 和 rate limit。
- Redis key 必须记录 owner、用途、TTL、value、重建来源和清理触发。
- `/readyz` 只证明 MySQL/Redis 地址可连接，不证明业务恢复路径已经通过；业务恢复能力由 storage/room 测试或后续明确集成测试验证。

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
Unity Client
  |
  | WebSocket + Protobuf envelope
  v
Gateway
  |
  | Go interface, in-process adapter
  +--> Account Service
  |
  | authenticated session
  v
Room Service
  |
  | repository/cache interface
  v
Storage Adapter
  |
  +-- Redis runtime cache
  +-- MySQL persistent summaries
```

## 设计约束

- 第一阶段优先跑通端到端链路，不追求过早微服务化。
- Gateway 与 Room 必须通过接口解耦。
- Room 状态机必须能在无网络环境下测试。
- gRPC 是后续拆分部署的工具，不是第一阶段默认复杂度。
- WebSocket 是第一阶段默认传输；TCP 后续复用同一 envelope。
- 所有协议行为必须能由 Protobuf schema 和 OpenSpec spec 追溯。
