# iHomeland 路线图

## 当前原则

当前路线图用于记录长期方向，不等同于单个 OpenSpec change 的实现清单。每个实现型 change 必须范围足够小，能被 AI 或开发者一次性执行、验证和回滚。

## 第一里程碑：自定义房间大厅

目标：

- 服务端可以启动
- 提供健康检查和版本接口
- 客户端可以通过 WebSocket 连接
- 客户端和服务端可以收发 Protobuf envelope
- 可以创建房间
- 可以加入房间
- 可以准备和取消准备
- 可以由房主在成员准备且在线时开始房间
- 可以退出房间
- 可以转移房主
- 可以断线重连并恢复房间身份

不做：

- 不做匹配系统
- 不做 MOBA/RTS 高频战斗模拟
- 不做独立 battle server
- 不做跨服
- 不做观战和回放
- 不做完整持久化业务

## 已完成基线

### establish-game-platform-architecture

状态：已归档。

产出：

- 项目总体架构
- 工程标准
- 项目流程规范
- 文件结构规划
- 协议兼容规则
- Redis key 规则
- `protocol`、`gateway`、`room`、`storage` 主规格

不做：不实现服务端功能，不生成大量业务代码。

### add-server-foundation

状态：已归档。

目标：初始化 Go 服务端骨架。

范围：

- Go module
- `server/cmd/server/main.go`
- `server/internal/` 基础目录
- 配置加载
- 项目级 logger
- Gin HTTP server
- `/healthz`
- `/readyz`
- `/version`
- 基础测试
- 本地运行和测试脚本

不做：

- 不做 WebSocket
- 不做房间业务
- 不接入 Redis/MySQL 真实业务数据
- 不拆分 gRPC 服务

### add-protocol-envelope

状态：已归档。

目标：建立 Protobuf 实时消息信封。

范围：

- `shared/proto/`
- `envelope.proto`
- 协议版本
- 消息 ID
- 请求 ID
- 序列号
- 心跳消息
- 错误响应
- 跨端协议生成脚本
- Go 服务端生成代码
- 服务端协议适配包
- Unity 输出路径预留
- 协议兼容文档

不做：

- 不实现 WebSocket 网关
- 不实现房间业务

### add-local-infra

状态：已归档。

目标：建立本地开发环境。

范围：

- `server/compose.yaml`
- Docker Compose 本地 MySQL、Redis
- `server/.env.example`
- `server/config/local.yaml` 本地依赖配置
- MySQL、Redis TCP 依赖探测
- `/readyz` 输出本地依赖状态
- 本地启动、停止和验证脚本
- 支持 Docker Compose 与本机安装 MySQL/Redis 两种本地依赖来源

不做：

- 不做生产部署
- 不设计完整数据库 schema
- 不创建业务 Redis key
- 不接入房间持久化读写

### add-websocket-gateway

状态：已归档。

目标：实现实时网关基础能力。

范围：

- WebSocket endpoint
- Protobuf envelope 解码
- 协议版本校验
- 结构化错误响应
- session 抽象
- 连接注册和清理
- 心跳和空闲超时
- 网关测试

不做：

- 不实现房间业务
- 不实现 TCP
- 不接入 battle server

### add-room-lobby

状态：已归档。

目标：实现第一个游戏业务功能“自定义房间大厅”。

范围：

- 房间模型
- 房主权限
- 状态迁移
- 创建房间
- 加入房间
- 退出房间
- 准备/取消准备
- 房主转移
- 房间解散
- 断线保留
- 重连恢复
- 房间状态机测试

不做：

- 不做匹配系统
- 不做完整持久化
- 不做高频战斗模拟

### add-persistence-boundaries

状态：已归档。

目标：定义并接入第一阶段需要的 Redis/MySQL 边界。

范围：

- `server/internal/storage` repository/cache interface
- 测试用 fake/in-memory storage adapter
- 账号资料 MySQL repository 和账号 session Redis cache
- Redis key builder 和 TTL 常量
- 第一阶段 MySQL migration 入口
- MySQL room summary repository 骨架和参数校验
- Redis presence、room index、reconnect token cache 骨架和参数校验
- room service 通过接口保存房间摘要
- 断线和重连通过 reconnect token cache 管理短期资格
- storage、room fake storage 集成测试
- Redis key、架构、文件结构和服务端 README 文档更新

不做：

- 不做完整账号、背包、经济、战绩系统
- 不在该 change 内启用账号 runtime 的真实 Redis/MySQL 读写；该事项已并入 `add-account-session`
- 不实现多进程房间一致性或 battle server 持久化

## 后续 OpenSpec Change 拆分

### document-client-integration

状态：已归档。

目标：文档化 Unity 协议生成、传输抽象和客户端连接流程。

不做：不在服务端实现引擎专用逻辑。

### create-unity-client-skeleton

状态：已由当前客户端基础链路覆盖，后续如需归档可补建文档型 change。

目标：创建最小 Unity 客户端工程骨架，明确目录、版本文件、项目设置和启动场景。

当前现状：

- 已有 `MainScene`
- 已有 `LoadingPage`
- 已有 `LoginPage`
- 已有 `HomePage`
- 已有 `BattleScene`
- 已有 `AppRoot`、`AppBootstrap`、基础 Systems 和 UI 页面打开流程
- `AccountSystem` 已改为通过 `NetworkSystem` 调用服务端账号协议
- `HomePage.Start Game` 当前进入 `RoomPage` 房间大厅，不再直接加载 `BattleScene`

不做：不实现完整 UI、美术资源、登录系统或战斗玩法。

### add-account-session

状态：已归档。

目标：实现第一阶段完整注册、登录登出、会话恢复、gateway 身份绑定、最小玩家资料和 Unity `AccountSystem` 真实服务端接入。

产出：

- 服务端账号协议、account service、gateway 身份绑定和 storage 边界已实现。
- 账号 runtime 已接入真实 MySQL `account_player` 和 Redis `ih:{env}:account:session:{sessionToken}`；fake/in-memory storage 只保留给单元测试。
- 服务端启动时会创建并 ping MySQL/Redis client，依赖不可用时拒绝启动，关闭时释放连接池。
- Unity C# Protobuf 生成代码和 Google.Protobuf runtime 已接入。
- Unity `NetworkSystem`、`AccountSystem`、`LoginPage` 和 `HomePage` 已改为真实账号链路。
- Unity 编辑器已完成启动、注册、登录、登出和恢复失败路径手动验证。

不做：不实现密码找回、第三方登录、好友、背包、经济、匹配或 battle server。

### add-unity-protobuf-generation

状态：已并入 `add-account-session`。

目标：为 Unity 客户端补齐 C# Protobuf 生成脚本、输出目录和生成代码管理规则。

当前约束：项目只保留 `tools/proto/generate.bat` 一个协议生成入口，该脚本同时生成服务端 Go 代码和 Unity C# 代码。

不做：不手写协议结构，不改变现有 `.proto` 语义，不新增第二个客户端专用生成脚本。

### add-unity-websocket-smoke-test

状态：已归档。

目标：实现 Unity 侧最小实时联调：连接 `/ws`、发送心跳、接收响应、发送账号注册或登录请求，并为后续房间大厅 UI 接入验证链路。

产出：

- 新增 Unity Editor 菜单 `iHomeland/Smoke Test/WebSocket Account`。
- smoke test 使用二进制 Protobuf envelope 连接本地 `/ws` 并校验 `HeartbeatResponse`。
- smoke test 使用固定测试账号注册；账号已存在时回退登录。
- smoke test 成功获得 session 后发送登出请求并关闭 WebSocket。
- smoke test 已在 Unity Editor 中手动验证通过。
- `client-integration` 长期规格和 Unity 接入文档已同步。

不做：不实现完整房间界面、不做复杂重连 UI、不引入 battle server。

### add-unity-room-lobby-flow

状态：已归档。

目标：接入已有服务端房间模块，让 Unity 客户端支持创房、进房、准备/取消准备、退房、房主转移、断线重连恢复和房间快照刷新。

当前产出：

- Unity `NetworkSystem` 已新增房间大厅请求方法。
- Unity `RoomSystem` 已接入 `AppRoot` 生命周期，负责 `RoomSnapshot`、当前房间操作和最近 room id。
- Unity `RoomPage` 脚本和 `HomePage.Start Game` 到 `RoomPage` 的代码路径已建立。
- `RoomPage.prefab` 已由 Unity Editor 内创建和绑定，房间大厅基础端到端路径已完成手动验证。

不做：不实现匹配系统，不进入正式战斗模拟，不引入独立 battle server。

### harden-room-identity-boundary

状态：已归档。

目标：加固账号会话到房间大厅之间的服务端身份边界，确保房间请求不能只依赖客户端 payload 中的 `player_id`。

产出：

- room dispatcher 已要求房间大厅请求来自已绑定玩家身份的 gateway connection。
- 服务端会校验 payload `player_id` 与 connection session `PlayerID` 一致，不一致时返回结构化 `UNAUTHENTICATED` 错误。
- 未登录 connection 发送创房、进房、准备、退出、房主转移或重连房间请求会被拒绝，且不会调用 room service 修改状态。
- WebSocket 集成测试已覆盖正常已登录房间流程、未登录房间请求拒绝和伪造 `player_id` 拒绝。

不做：不删除 Protobuf 房间请求中的 `player_id` 字段，不新增协议错误码，不实现开始游戏闸门、房间持久化或 battle server。

### add-room-start-gate

状态：已归档。

目标：在房间大厅内定义“开始游戏”的第一阶段闸门：只校验房主、成员准备状态和房间状态，并决定是否允许从大厅进入后续占位场景。

当前产出：

- 已新增 `StartRoomRequest`、`StartRoomResponse`、`ROOM_STATE_STARTED` 和稳定 room message id。
- 服务端房间状态机已校验房主权限、房间开放状态、非房主准备状态和所有成员在线状态。
- Unity 侧已在 `NetworkSystem`、`RoomSystem` 和 `RoomPage` 接入开始入口，`RoomPage.prefab` 已绑定开始按钮；当前只展示已开始状态，不加载正式战斗场景。
- Unity Editor 中已完成单人房主创房后开始按钮可点击和开始闸门手动回归。

不做：不实现高频战斗同步、服务端权威模拟、观战、回放或战斗结算。

### add-internal-service-boundaries

目标：在确实需要拆分时，将进程内接口升级为 gRPC 边界。

不做：在 Unity 客户端最小联调完成前，不急着拆服务。

## Battle Server 进入条件

只有当第一里程碑完成，并且需要实现权威高频战斗时，才创建 battle server change。该 change 至少必须回答：

- tick rate 是多少
- 是否需要确定性模拟
- 客户端输入如何校验
- 如何做状态同步或校正
- 如何处理重连
- 是否需要回放和观战
- 如何做反作弊
- 战斗进程如何放置和迁移
