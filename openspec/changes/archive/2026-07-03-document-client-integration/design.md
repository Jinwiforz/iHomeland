## Context

服务端已经完成第一阶段核心链路：WebSocket `/ws`、Protobuf envelope、协议版本校验、心跳、结构化错误、房间大厅操作和断线重连。客户端技术选型现在确定为 Unity，因此文档和长期规格需要从“Unity 或 Godot”收敛为“Unity 客户端”，并说明 Unity 工程未来如何接入这些服务端能力。

当前仓库还没有 Unity 工程。本 change 的重点是接入契约和文档，不引入 Unity 包、不生成 C# 代码、不改变服务端运行时代码。

## Goals / Non-Goals

**Goals:**

- 明确 Unity 是第一阶段客户端技术方向。
- 文档化 Unity 客户端 Protobuf 生成输入、输出归属和禁止手工修改生成代码的规则。
- 文档化 Unity 客户端 WebSocket 连接、二进制 envelope 收发、协议版本、request id、sequence、心跳和错误处理。
- 文档化房间大厅最小联调流程：创建、加入、准备、退出、房主转移、断线重连。
- 给未来 Unity 工程提供可执行的本地联调步骤和验收清单。

**Non-Goals:**

- 不创建 Unity 项目、不提交 Unity `.meta`、场景、Prefab、UI 或 C# runtime 代码。
- 不引入客户端 WebSocket/Protobuf 依赖包。
- 不改 Protobuf schema、不新增服务端接口。
- 不实现登录、账号、匹配、battle server、观战、回放或完整资源热更新。

## Decisions

### 1. 第一阶段客户端固定为 Unity

文档中不再使用“Unity 或 Godot”描述第一阶段客户端。Unity 相关路径、生成说明和接入文档都以 `client/` 下未来 Unity 工程为目标。

替代方案是继续保留双引擎表述。该方案会让协议生成路径、依赖选择和联调说明长期模糊，不利于启动真实客户端工程。

### 2. 以共享 Protobuf 源文件作为唯一协议输入

Unity 客户端必须从 `shared/proto/` 生成 C# 协议代码，生成代码不得手工修改。服务端当前 Go 生成路径保持不变，Unity 生成入口可由后续客户端工程补充到 `tools/proto/`。

替代方案是在 Unity 工程内复制 `.proto`。该方案会造成协议源分叉，后续版本兼容风险更高。

### 3. Unity 接入文档只描述契约，不绑定具体第三方库

WebSocket 和 Protobuf C# runtime 的具体包选择留给 Unity 工程实现阶段；文档只要求二进制 WebSocket、Protobuf envelope、版本校验、request id、sequence、心跳和错误处理语义。

替代方案是在当前 change 指定具体 Unity 插件。仓库还没有 Unity 工程，过早指定依赖会增加维护成本。

### 4. 联调验收以服务端现有能力为准

Unity 客户端最小联调必须能访问 `/version`，连接 `/ws`，发送 `HeartbeatRequest`，并按 envelope 发送房间大厅请求。没有客户端工程时，服务端已有 Go 测试仍作为当前功能回归手段；客户端工程出现后再增加 Unity 侧验证脚本或 PlayMode 测试。

## Risks / Trade-offs

- [Risk] 文档过早写入具体 Unity 插件，后续工程选择变化会造成返工。→ 本 change 只固定协议和传输语义，不固定第三方库。
- [Risk] 没有 Unity 工程时无法验证 C# 生成代码。→ 当前只验收文档和服务端契约；创建 Unity 工程时必须补生成脚本和客户端侧测试。
- [Risk] 移除 Godot 表述后未来想换引擎。→ 未来若重新评估客户端引擎，必须创建单独 OpenSpec change 调整客户端边界。

## Migration Plan

1. 新增 `client-integration` delta spec。
2. 新增 Unity 客户端接入文档。
3. 更新 README、AGENTS、架构、文件结构、路线图、协议说明中不再准确的 Unity/Godot 表述。
4. 运行文档搜索，确认没有保留第一阶段客户端未确定表述。

## Open Questions

- Unity 工程创建后具体使用哪个 WebSocket/Protobuf C# 包，需要在客户端工程 change 中决定。
- Unity 生成代码输出目录需要结合实际 Unity 工程结构确定。
