## Why

当前项目已经确定实时通信必须使用 Protobuf envelope，但还缺少可生成、可测试、可供客户端和服务端共同依赖的协议文件与消息编号规则。现在需要先建立稳定的协议信封，作为后续 WebSocket 网关、房间大厅和客户端接入的共同基础。

## What Changes

- 在 `shared/proto/` 下建立第一阶段实时通信 Protobuf schema。
- 定义统一 envelope，包含协议版本、消息 ID、请求 ID、序列号、时间戳和 payload。
- 定义基础系统消息，包括心跳、心跳响应、错误响应和协议版本拒绝信息。
- 建立消息 ID 分配规则和 Go 代码生成入口。
- 为 Unity/Godot 客户端生成输出预留目录和说明。
- 补充协议兼容文档中的 envelope 字段、错误码和 message id 规则。
- 不实现 WebSocket 网关、TCP 传输、房间业务消息和客户端工程集成。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `protocol`: 补充 Protobuf envelope、基础系统消息、消息 ID、生成代码和兼容约束的长期行为要求。

## Impact

- 影响 `shared/proto/` 协议源文件和生成配置。
- 影响 `server/internal/protocol` 或同等协议适配包。
- 影响服务端 Go module 的 Protobuf 生成依赖和生成脚本。
- 影响 `docs/protocol-compatibility.md` 中关于 envelope、错误响应和消息编号的说明。
- 为后续 `add-websocket-gateway` 和 `document-client-integration` 提供协议契约。
