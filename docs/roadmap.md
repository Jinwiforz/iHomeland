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
- Unity/Godot 输出路径预留
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

## 后续 OpenSpec Change 拆分

### add-websocket-gateway

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

目标：定义并接入第一阶段需要的 Redis/MySQL 边界。

不做：不做完整账号、背包、经济、战绩系统。

### add-internal-service-boundaries

目标：在确实需要拆分时，将进程内接口升级为 gRPC 边界。

不做：第一阶段默认不急着拆服务。

### document-client-integration

目标：文档化 Unity/Godot 协议生成、传输抽象和客户端连接流程。

不做：不在服务端实现引擎专用逻辑。

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
