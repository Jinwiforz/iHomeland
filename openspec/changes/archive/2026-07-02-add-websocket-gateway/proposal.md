## Why

第一里程碑需要先打通客户端到服务端的实时连接链路，使 Unity 或 Godot 客户端能够通过 WebSocket 发送和接收 Protobuf envelope。当前项目已有 HTTP 基础接口和协议 envelope，但缺少实际承载连接、版本校验、心跳和错误响应的网关入口。

## What Changes

- 新增 WebSocket endpoint，作为第一阶段默认实时传输入口。
- 在网关中接收二进制 Protobuf envelope，并复用现有协议适配层完成编解码、版本校验和基础系统消息处理。
- 建立连接级 session 抽象，记录 connection id、协议版本、远端地址、活跃时间和关闭原因。
- 支持心跳请求、心跳响应、空闲超时、连接关闭日志和 session 清理。
- 对非法 envelope、不支持协议版本、缺失 request id 和不支持 message id 返回结构化错误响应。
- 为后续 room service 分发预留明确接口边界，但本 change 不实现房间业务、匹配系统、TCP 传输或 battle server。

## Capabilities

### New Capabilities

- 无。

### Modified Capabilities

- `gateway`: 细化网关通过 WebSocket 承载 Protobuf envelope 的连接生命周期、session、心跳、超时、错误响应和消息分发边界。

## Impact

- 影响 `server/internal/gateway`、`server/internal/app` 和服务端启动路由注册。
- 复用 `server/internal/protocol` 与 `shared/proto/` 生成代码，不手工修改生成代码。
- 可能新增 WebSocket 依赖库及对应测试工具。
- 增加网关单元测试或不依赖真实客户端的集成测试，覆盖连接、心跳、版本拒绝、非法 payload、空闲超时和清理行为。
