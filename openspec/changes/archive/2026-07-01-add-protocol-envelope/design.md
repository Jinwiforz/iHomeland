## Context

项目当前已经有 Go 服务端基础骨架，以及 `protocol` 主规格和 `docs/protocol-compatibility.md` 中的协议原则。后续 `add-websocket-gateway`、`add-room-lobby` 和客户端接入都依赖统一的实时消息信封，因此本 change 先建立可生成、可测试、可演进的 Protobuf envelope。

当前约束：

- 第一里程碑只服务“自定义房间大厅”，不实现 MOBA/RTS 高频战斗同步。
- WebSocket 是第一阶段默认传输，TCP 后续复用相同 envelope。
- 协议源文件应归属 `shared/proto/`，服务端生成代码归属 `server/internal/protocol` 或等价适配目录。
- 生成代码不得手工修改。

## Goals / Non-Goals

**Goals:**

- 定义 `envelope.proto` 和基础系统消息。
- 建立稳定 message id、request id、sequence、timestamp 和 payload 规则。
- 提供 Go 协议代码生成入口。
- 预留 Unity/Godot 客户端生成输出结构和说明。
- 更新协议兼容文档，明确 envelope 字段、错误响应和 message id 规则。
- 添加基础编码、解码、版本拒绝和生成结果可用性测试。

**Non-Goals:**

- 不实现 WebSocket 网关。
- 不实现 TCP 传输。
- 不实现房间业务消息。
- 不实现客户端工程或引擎专用逻辑。
- 不设计 battle server 高频同步协议。

## Decisions

### 使用 `shared/proto/realtime/v1` 作为协议源目录

协议源文件放在共享目录，表达客户端和服务端共同依赖。目录带 `v1`，方便未来出现破坏性协议版本时新增并行 schema。

替代方案：

- 放在 `server/proto/`：服务端所有权清晰，但容易让客户端接入变成二等公民。
- 直接放在 `shared/proto/` 根目录：简单，但后续消息增多后命名空间不清晰。

### Envelope 使用 `google.protobuf.Any` 承载 payload

Envelope 保持固定字段，业务消息通过 `Any` 作为 payload 承载。这样可以先建立基础契约，后续房间消息独立演进，不需要频繁修改 envelope 本体。

替代方案：

- 使用 `bytes payload`：传输更朴素，但解码类型需要额外注册表，早期调试体验较差。
- 使用 `oneof` 列出所有业务消息：类型清晰，但每新增业务消息都要修改 envelope，耦合过重。

### Message ID 使用分段常量并建立注册表

基础系统消息先保留低号段，例如心跳、心跳响应、错误响应和版本拒绝。后续房间消息、账号消息、匹配消息使用独立号段。

建议号段：

- `1-999`：系统和网关消息。
- `1000-1999`：账号或会话消息。
- `2000-2999`：房间大厅消息。
- `3000-3999`：匹配相关消息。
- `9000+`：实验或保留消息，发布前必须迁移到正式号段。

替代方案：

- 完全依赖 Protobuf full name 路由：避免数字表，但跨语言和日志排查不如数字 ID 直接。
- 每个模块自行定义 ID：短期方便，长期容易冲突。

### 协议生成入口放在 `tools/proto/generate.bat`

协议生成面向客户端与服务端，属于跨模块工具，不应放在 `server/scripts`。生成命令应只写相对路径，当前输出 Go 服务端代码，后续客户端生成入口也归属同一工具目录。

替代方案：

- 放在 `server/scripts/`：短期接近 Go 输出目录，但会把跨端协议生成误归类为服务端专属脚本。
- 使用 Makefile：跨平台较好，但当前开发环境以 Windows PowerShell/CMD 为主。

### 服务端协议适配包不依赖传输层

`server/internal/protocol` 只负责协议常量、编码解码辅助、版本范围校验和 message id 注册，不直接依赖 Gin、WebSocket 或 TCP。

替代方案：

- 在 gateway 中直接处理 Protobuf：实现更快，但会让协议兼容规则分散到传输层。

## Risks / Trade-offs

- [Risk] `Any` 需要各语言正确处理 type url。→ 在文档中固定 type url 约定，并为 Go 编解码添加测试。
- [Risk] message id 与 Protobuf message 类型可能不一致。→ 建立注册表和测试，确保基础消息 ID 与类型映射稳定。
- [Risk] 本机缺少 `protoc` 或 Go 插件导致生成失败。→ 在任务中加入工具检查和清晰错误提示，不把生成失败静默吞掉。
- [Risk] 提前设计过多业务消息会扩大范围。→ 本 change 只定义系统消息和 envelope，不加入房间业务消息。

## Migration Plan

1. 新增共享 Protobuf schema 和消息 ID 说明。
2. 增加跨端协议生成脚本和生成后的服务端协议代码。
3. 增加服务端协议适配包与测试。
4. 更新协议兼容文档和文件结构文档。
5. 后续 `add-websocket-gateway` 基于本 change 的 envelope 接入实时连接。

回滚策略：删除本 change 新增的协议 schema、生成代码、协议适配包、脚本和文档更新即可；当前没有线上兼容负担。

## Open Questions

- Unity 和 Godot 最终是否同时支持，由 `document-client-integration` 决定。
- 后续是否引入 Buf 管理 Protobuf lint 和 breaking change 检查，由协议复杂度增长后单独评估。
