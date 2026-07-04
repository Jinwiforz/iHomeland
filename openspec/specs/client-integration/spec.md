# Client Integration 规格

## Purpose

定义 Unity 客户端接入服务端版本接口、WebSocket、Protobuf envelope 和房间大厅联调的长期行为契约，确保客户端集成可验证。

## Requirements

### Requirement: 第一阶段客户端必须以 Unity 为目标
MUST:iHomeland 第一阶段客户端接入文档和工程规划必须以 Unity 为目标，不得继续把 Godot 作为并列待选客户端方向。

#### Scenario: 文档描述客户端技术方向
- **WHEN** 项目文档描述第一阶段客户端
- **THEN** 文档必须明确客户端为 Unity，并避免使用 Unity/Godot 二选一表述

### Requirement: Unity 客户端必须从共享 Protobuf 生成协议代码
MUST:Unity 客户端必须以 `shared/proto/` 中的 `.proto` 文件作为协议源，生成 C# 协议代码；生成代码不得手工修改。

#### Scenario: 生成 Unity 协议代码
- **WHEN** Unity 工程需要实时协议类型
- **THEN** 它必须从 `shared/proto/` 生成 C# 代码，而不是复制或手写协议结构

#### Scenario: 协议源发生变化
- **WHEN** `.proto` 文件新增或修改消息
- **THEN** Unity 客户端必须重新生成协议代码，并通过协议版本和消息 ID 兼容规则接入

### Requirement: Unity 客户端必须使用 WebSocket 二进制 envelope
MUST:Unity 客户端连接实时网关时必须使用 WebSocket `/ws`，并以二进制帧收发 Protobuf envelope。

#### Scenario: 建立实时连接
- **WHEN** Unity 客户端连接本地服务端实时网关
- **THEN** 它必须连接 `/ws` 并发送二进制 Protobuf envelope

#### Scenario: 收到文本帧需求
- **WHEN** Unity 客户端实现实时消息发送
- **THEN** 它不得使用文本帧承载业务消息

### Requirement: Unity 客户端必须处理 envelope 基础字段
MUST:Unity 客户端必须正确设置和处理 `protocol_version`、`message_id`、`request_id`、`sequence` 和 payload。

#### Scenario: 发送需要响应的请求
- **WHEN** Unity 客户端发送房间大厅请求
- **THEN** envelope 必须携带受支持的 `protocol_version`、正确的 `message_id`、非空 `request_id` 和 Protobuf payload

#### Scenario: 接收响应
- **WHEN** Unity 客户端收到响应 envelope
- **THEN** 它必须使用 `request_id` 关联请求和响应，并按 `message_id` 解码 payload

### Requirement: Unity 客户端必须实现心跳和错误处理
MUST:Unity 客户端必须周期性发送心跳，并能够展示或记录服务端结构化错误。

#### Scenario: 维持连接
- **WHEN** Unity 客户端 WebSocket 连接处于空闲状态
- **THEN** 它必须按服务端空闲超时策略发送 `HeartbeatRequest`

#### Scenario: 收到错误响应
- **WHEN** Unity 客户端收到 `ErrorResponse`
- **THEN** 它必须读取错误码、错误消息和可选版本范围，并更新 UI 或日志状态

### Requirement: Unity 客户端必须按房间快照刷新大厅 UI
MUST:Unity 客户端执行房间大厅操作后，必须以服务端返回的 `RoomSnapshot` 作为 UI 状态来源。

#### Scenario: 房间操作成功
- **WHEN** Unity 客户端创建、加入、准备、退出、转移房主或重连成功
- **THEN** 它必须使用响应中的 `RoomSnapshot` 刷新房间名、房主、成员、座位、阵营、准备状态和连接状态

#### Scenario: 本地状态与服务端状态不一致
- **WHEN** Unity 客户端本地 UI 状态与最新 `RoomSnapshot` 不一致
- **THEN** 它必须以服务端快照为准

### Requirement: Unity 联调文档必须包含最小验收路径
MUST:Unity 客户端接入文档必须提供本地服务端启动、版本检查、WebSocket 连接、心跳和房间大厅请求的最小验收路径。

#### Scenario: 开发者验证 Unity 接入
- **WHEN** 开发者按 Unity 接入文档进行联调
- **THEN** 文档必须能引导其确认 `/version`、`/ws`、心跳和至少一个房间大厅请求响应链路是否正常
