## Context

服务端当前已经具备 Gin HTTP server、`/healthz`、`/readyz`、`/version`、配置加载、项目级 logger，以及 `server/internal/protocol` 中的 envelope 构造、解码、消息 ID 注册和协议版本校验。缺口在于实时连接入口：客户端还无法通过 WebSocket 建立长连接，也没有连接级 session、心跳、空闲超时和清理逻辑。

第一里程碑仍然只面向“自定义房间大厅”。本 change 要把 WebSocket + Protobuf envelope 的传输链路做扎实，但不能把房间状态机、匹配、TCP 或 battle server 提前塞进网关。

## Goals / Non-Goals

**Goals:**

- 在 HTTP server 上注册 WebSocket endpoint，接收二进制 Protobuf envelope。
- 建立 `server/internal/gateway` 包，封装连接生命周期、session、读写循环、心跳、空闲超时、关闭和日志。
- 复用 `server/internal/protocol` 完成 envelope 编解码、协议版本校验、系统消息构造和结构化错误响应。
- 用小接口预留业务消息分发边界，使后续 room service 可以通过接口接入。
- 覆盖连接、心跳、协议版本拒绝、非法 payload、未知 message id、空闲超时和清理测试。

**Non-Goals:**

- 不实现创建房间、加入房间、准备、退出、房主转移或重连恢复业务。
- 不新增 TCP 传输，不改变现有 Protobuf schema，不手工修改生成代码。
- 不接入 Redis/MySQL session 持久化；本 change 的 session 是单进程连接级运行态。
- 不实现匹配系统、观战、回放、独立 battle server 或高频战斗模拟。

## Decisions

### 1. WebSocket endpoint 归属 `gateway`，注册由 `app` 组装

`gateway` 包提供 `RegisterRoutes(router gin.IRouter, server *gateway.Server)` 或等价注册函数，`app.NewHTTPServer` 负责创建依赖并把路由挂到 Gin。这样入口组装集中在 `app`，连接和协议细节留在 `gateway`。

替代方案是在 `cmd/server/main.go` 里直接注册 WebSocket。该方案会让启动编排知道过多业务模块细节，不符合当前 `app` 组装层的职责。

### 2. 使用基于 `net/http` 的轻量 WebSocket 库

优先选择 `github.com/coder/websocket`。它提供 `context.Context` 友好的 API、二进制消息支持、关闭握手和较少依赖，适合当前 Go + Gin + 标准 HTTP server 的形态。Gin handler 可以直接透传 `http.ResponseWriter` 和 `*http.Request`，不会把业务逻辑绑定到 Gin。

替代方案是 `github.com/gorilla/websocket`。它成熟且使用广泛，但当前项目更需要 context 取消、超时和关闭路径清晰，`coder/websocket` 更贴合本 change 的生命周期需求。

### 3. 一个连接一个 session，session 只保存运行态元数据

连接建立后创建唯一 `connectionID`，记录远端地址、协议版本、建立时间、最后活跃时间、关闭原因和发送序列号。session 由 gateway 管理，断开时必须注销并释放资源。

本 change 不把 session 写入 Redis。Redis 的 presence、reconnect token 和跨进程恢复属于后续持久化边界或 room lobby change；过早写入 Redis 会把纯传输能力和业务恢复语义混在一起。

### 4. 网关只处理系统消息，业务消息走分发接口

网关直接处理 heartbeat、协议版本错误和基础 envelope 错误。非系统消息通过 `Dispatcher` 之类的小接口传递给后续业务模块；当没有注册处理器或 message id 不支持时，返回结构化错误。

替代方案是在网关内 switch 所有房间消息。该方案会让网关承载业务状态机，违反 gateway 与 room 的边界。

### 5. 错误响应先发送，必要时再关闭连接

非法 Protobuf payload、未知 message id、缺失 request id 等可恢复错误返回 `ErrorResponse` 后保持连接；协议版本不支持、非二进制 WebSocket 消息、读写协议错误和空闲超时返回错误或关闭原因后关闭连接。关闭原因必须进入日志，便于排查。

### 6. 心跳以应用层 envelope 为准

客户端发送 `HeartbeatRequest` envelope，服务端返回 `HeartbeatResponse` envelope，并更新最后活跃时间。WebSocket ping/pong 可以作为库或底层连接健康补充，但不替代项目协议中的心跳消息。

## Risks / Trade-offs

- [Risk] 单进程内存 session 无法跨进程恢复。→ 本 change 明确只解决连接级运行态；跨进程 presence 和 reconnect token 放到后续 change。
- [Risk] 读写循环和关闭路径容易产生 goroutine 泄漏。→ 连接必须由 context 统一取消，测试覆盖主动关闭、超时关闭和服务端关闭。
- [Risk] 错误响应可能因连接已损坏而发送失败。→ 发送失败必须记录日志并进入清理路径，不吞错。
- [Risk] 业务分发接口过早变复杂。→ 初期只暴露 message id、request id、session 摘要和 payload，不引入 gRPC 或跨进程抽象。
- [Risk] WebSocket 依赖带来维护成本。→ 选择依赖少、API 简洁且适合 `net/http` 的库，并通过包内封装隔离第三方 API。

## Migration Plan

1. 新增 `server/internal/gateway` 包并保持现有 HTTP 接口不变。
2. 在 `app.NewHTTPServer` 注册 WebSocket endpoint；未连接 WebSocket 的调用方不受影响。
3. 增加测试和本地验证脚本中可选的 WebSocket 检查。
4. 回滚时移除 gateway 路由注册和依赖，基础 HTTP 控制面仍可运行。

## Open Questions

- WebSocket endpoint 路径暂定为 `/ws`；若后续需要多协议版本并存，可以在后续 change 中扩展为 `/realtime/v1/ws`，但本 change 不提前引入版本化路由。
- 连接鉴权暂不在本 change 实现；后续 account/session change 需要定义 player identity 与 gateway session 的绑定方式。
