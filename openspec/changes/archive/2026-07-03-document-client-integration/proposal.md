## Why

服务端已经具备 WebSocket、Protobuf envelope、房间大厅和 storage 边界，但客户端接入路径还没有被文档化。现在客户端技术选型已确定为 Unity，需要把跨端协议生成、连接流程、消息封包和联调边界写清楚，避免后续客户端工程启动时反复猜服务端约定。

## What Changes

- 明确 iHomeland 第一阶段客户端为 Unity，不再保留 Unity/Godot 二选一表述。
- 新增 Unity 客户端接入规格，覆盖 Protobuf 生成、WebSocket 连接、envelope 封包/解包、心跳、错误响应和房间大厅消息流程。
- 更新现有文档中的客户端定位、目录职责、协议生成说明和路线图。
- 增加面向 Unity 客户端开发者的接入文档，说明最小联调步骤和验收方式。
- 不创建 Unity 工程、不实现客户端 UI、不引入客户端依赖包。

## Capabilities

### New Capabilities

- `client-integration`: Unity 客户端接入服务端实时协议、版本接口、WebSocket 传输和房间大厅流程的长期行为要求。

### Modified Capabilities

- 无。

## Impact

- 影响 `docs/` 下架构、文件结构、路线图、协议兼容和新增 Unity 客户端接入文档。
- 影响根 `README.md`、`AGENTS.md` 和 `shared/proto/README.md` 中的客户端技术表述。
- 影响 `openspec/specs/client-integration/spec.md` 新增长期规格。
- 不影响服务端 Go 运行时代码、不变更 Protobuf schema、不改变 WebSocket endpoint。
