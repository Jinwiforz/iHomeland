# 协议兼容规则

## 基本原则

- 实时通信使用 Protobuf。
- WebSocket 和 TCP 共享同一 envelope。
- 协议版本必须显式携带。
- 服务端必须拒绝不支持的协议版本，并返回结构化错误。
- 生成代码不得手工修改。

## 字段规则

- 新增字段必须使用新的 field number。
- 已发布 field number 不得复用。
- 删除字段必须使用 `reserved` 保留编号和名称。
- 不得随意改变字段语义。
- 不得在不升级协议版本的情况下改变必填行为。

## 消息规则

- 每个消息必须有稳定 message id。
- message id 不得复用。
- 废弃消息应保留兼容期。
- 破坏性变更必须更新 protocol version。

## Envelope 建议字段

实时通信 envelope 必须包含：

- `protocol_version`：客户端与服务端显式校验的协议版本。
- `message_id`：稳定消息编号，用于跨语言路由、日志排查和兼容管理。
- `request_id`：请求、响应和错误的关联 ID；需要响应的消息必须填写。
- `sequence`：单连接内递增序列号，用于基础顺序排查和重复消息识别。
- `timestamp_ms`：发送方生成消息时的 Unix 毫秒时间戳。
- `payload`：实际 Protobuf 消息。

WebSocket 和后续 TCP 必须复用同一 envelope schema。

Unity 客户端必须用二进制 WebSocket 帧发送 Protobuf envelope。发送需要响应的请求时，`protocol_version`、`message_id`、`request_id`、`sequence` 和 `payload` 必须完整设置；收到响应时，客户端必须用 `request_id` 关联本地请求，并按 `message_id` 解码 payload。

客户端建立 WebSocket 后必须定时发送 `HeartbeatRequest`，间隔应小于服务端 gateway `IdleTimeout`。心跳必须复用普通请求响应通道，避免同一连接出现多个并发接收者；服务端收到心跳后返回 `HeartbeatResponse`，客户端可用 `client_time_ms` 计算 RTT，并用 `server_time_ms` 记录服务端时间观测值。

## Message ID 规则

第一阶段号段：

- `1-999`：系统和网关消息。
- `1000-1999`：账号或会话消息。
- `2000-2999`：房间大厅消息。
- `3000-3999`：匹配相关消息。
- `9000+`：实验或保留消息，发布前必须迁移到正式号段。

当前系统消息：

- `1`：`HeartbeatRequest`
- `2`：`HeartbeatResponse`
- `3`：`ErrorResponse`
- `4`：`ProtocolVersionUnsupported`

当前账号会话消息：

- `1000`：`RegisterRequest`
- `1001`：`RegisterResponse`
- `1002`：`LoginRequest`
- `1003`：`LoginResponse`
- `1004`：`LogoutRequest`
- `1005`：`LogoutResponse`
- `1006`：`ResumeSessionRequest`
- `1007`：`ResumeSessionResponse`
- `1008`：`GetCurrentPlayerRequest`
- `1009`：`GetCurrentPlayerResponse`

账号会话消息 owner 为 `account`。注册、登录和恢复成功响应必须返回服务端确认的玩家资料、session token 和过期时间；登出成功后服务端必须失效当前会话并清理 gateway session 身份。

当前房间大厅消息：

- `2000`：`CreateRoomRequest`
- `2001`：`CreateRoomResponse`
- `2002`：`JoinRoomRequest`
- `2003`：`JoinRoomResponse`
- `2004`：`SetReadyRequest`
- `2005`：`SetReadyResponse`
- `2006`：`LeaveRoomRequest`
- `2007`：`LeaveRoomResponse`
- `2008`：`TransferHostRequest`
- `2009`：`TransferHostResponse`
- `2010`：`ReconnectRoomRequest`
- `2011`：`ReconnectRoomResponse`
- `2012`：`RoomSnapshotPushed`

房间大厅消息 owner 为 `room`。客户端接入房间大厅时需要使用 `RoomSnapshot` 刷新房间 UI，并在请求中携带 `player_id` 和 envelope `request_id`。现阶段仍保留 payload `player_id` 字段，但服务端会以 gateway connection session 中已绑定的 `PlayerID` 作为身份事实来源；未登录或 payload `player_id` 与 session 身份不一致时，服务端必须返回结构化 `UNAUTHENTICATED` 错误，不得修改房间状态。

每个 message id 必须有 owner。已发布 message id 不得复用；废弃消息必须保留编号并记录迁移策略。

## 错误响应规则

服务端拒绝或无法处理 envelope 时必须返回结构化错误响应。

`ErrorResponse` 字段语义：

- `code`：给程序分支判断使用，必须稳定、枚举化，不承载动态上下文。
- `message`：给日志和必要的 UI 提示使用的简短可读摘要，应稳定且便于搜索。
- `detail`：给开发调试使用的具体诊断信息，可以包含动态上下文，客户端不得依赖它做业务分支。

当前基础错误码：

- `PROTOCOL_VERSION_UNSUPPORTED`
- `MESSAGE_ID_UNSUPPORTED`
- `PAYLOAD_INVALID`
- `REQUEST_ID_REQUIRED`

账号会话和房间身份边界接入后，账号已存在、账号凭据非法、未登录、session 过期和房间请求身份不一致必须复用结构化错误响应；当前账号态错误码包括 `ACCOUNT_ALREADY_EXISTS`、`ACCOUNT_CREDENTIAL_INVALID`、`SESSION_INVALID` 和 `UNAUTHENTICATED`。

需要响应的请求失败时，错误响应必须回传相同 `request_id`。

## 版本拒绝策略

当客户端协议版本低于服务端支持范围时：

- 服务端返回 `PROTOCOL_VERSION_UNSUPPORTED`
- 错误响应包含服务端支持范围
- 网关按策略关闭连接或限制会话

当客户端协议版本高于服务端支持范围时：

- 服务端返回 `PROTOCOL_VERSION_UNSUPPORTED`
- 客户端应提示版本不匹配

Unity 客户端收到 `PROTOCOL_VERSION_UNSUPPORTED` 后，应停止继续发送业务请求，提示版本不匹配，并保留服务端返回的支持范围用于排查。

## 文档要求

每次协议变更必须说明：

- 新增、修改或废弃的消息
- 是否破坏兼容
- 客户端升级要求
- 服务端拒绝策略是否变化

Unity 客户端接入、生成和联调要求见 `docs/client-integration.md`。
