# Unity 客户端接入

本文档说明第一阶段 Unity 客户端如何接入 iHomeland 服务端。它的 owner 是 `client-integration`，维护场景包括协议生成、实时连接、房间大厅联调和客户端接入验收。

当前范围只覆盖自定义房间大厅：版本检查、WebSocket 连接、Protobuf envelope、心跳、错误响应、创建房间、加入房间、准备、退出、房主转移和断线重连。本文档不创建 Unity 工程，不规定具体 Unity 第三方包，不实现登录、匹配、battle server、观战、回放或完整 UI。

## 协议来源

Unity 客户端必须从仓库共享协议生成 C# 代码：

```text
shared/proto/
```

当前实时协议入口：

```text
shared/proto/realtime/v1/envelope.proto
```

规则：

- `.proto` 是客户端和服务端的唯一协议源。
- Unity 生成代码不得手工修改。
- Unity 工程不得复制一份独立 `.proto` 作为长期来源。
- `.proto` 变化后，Unity 侧必须重新生成 C# 协议代码。
- 生成输出目录由后续 Unity 工程结构确定，建议归属 `client/` 内的生成代码目录。

## 版本检查

Unity 客户端启动或进入联机流程前，应请求：

```text
GET /version
```

客户端使用该响应确认 release、server、client 和 protocol 版本信息。实时消息仍以 envelope 中的 `protocol_version` 作为服务端强校验来源。

## WebSocket 连接

第一阶段实时入口：

```text
ws://{host}/ws
```

连接规则：

- Unity 客户端必须使用 WebSocket 二进制帧。
- 业务消息必须编码为 Protobuf `Envelope`。
- 不得使用文本帧承载业务消息。
- 连接断开后，客户端应停止发送业务请求，并根据 UI 状态提示重连或返回大厅入口。

## Envelope 规则

Unity 客户端发送实时请求时必须设置：

```text
protocol_version
message_id
request_id
sequence
timestamp_ms
payload
```

字段含义：

- `protocol_version`：客户端使用的协议版本，必须在服务端支持范围内。
- `message_id`：稳定消息 ID，用于服务端路由和客户端解码。
- `request_id`：请求/响应/错误关联 ID；需要响应的请求必须非空。
- `sequence`：单连接内递增序号，用于排查顺序和重复消息。
- `timestamp_ms`：客户端发送时的 Unix 毫秒时间戳。
- `payload`：对应 `message_id` 的 Protobuf 消息二进制。

Unity 客户端收到响应后，必须先按 envelope 解码，再用 `message_id` 选择具体 payload 类型，并用 `request_id` 关联本地 pending 请求。

## 心跳

客户端需要周期性发送：

```text
message_id = 1
payload = HeartbeatRequest
```

服务端正常返回：

```text
message_id = 2
payload = HeartbeatResponse
```

客户端应记录最近一次心跳响应时间。若连接长时间没有响应，应让 UI 进入断线或重连状态，避免继续把按钮操作当作已连接请求发送。

## 错误响应

服务端无法处理请求时会返回：

```text
message_id = 3
payload = ErrorResponse
```

Unity 客户端必须读取：

- 错误码
- 错误消息
- 对应 `request_id`
- 可选的协议版本支持范围

当前基础错误码包括：

- `PROTOCOL_VERSION_UNSUPPORTED`
- `MESSAGE_ID_UNSUPPORTED`
- `PAYLOAD_INVALID`
- `REQUEST_ID_REQUIRED`

如果收到协议版本不兼容错误，客户端应停止继续发送业务请求，并提示版本不匹配。

## 房间大厅流程

当前房间大厅 message id：

| 操作 | Request | Response |
| --- | --- | --- |
| 创建房间 | `2000 CreateRoomRequest` | `2001 CreateRoomResponse` |
| 加入房间 | `2002 JoinRoomRequest` | `2003 JoinRoomResponse` |
| 准备/取消准备 | `2004 SetReadyRequest` | `2005 SetReadyResponse` |
| 退出房间 | `2006 LeaveRoomRequest` | `2007 LeaveRoomResponse` |
| 转移房主 | `2008 TransferHostRequest` | `2009 TransferHostResponse` |
| 重连恢复房间身份 | `2010 ReconnectRoomRequest` | `2011 ReconnectRoomResponse` |
| 房间快照推送 | 无 | `2012 RoomSnapshotPushed` |

房间大厅请求必须携带业务所需的 `player_id`，并在 envelope 中携带非空 `request_id`。

## UI 状态来源

Unity 客户端执行房间操作成功后，必须使用服务端返回的 `RoomSnapshot` 刷新 UI。不要把本地按钮点击结果直接当作最终状态。

`RoomSnapshot` 至少用于刷新：

- 房间 ID 和房间名
- 房间状态
- 房主玩家 ID
- 容量和成员列表
- 成员座位
- 成员阵营
- 准备状态
- 在线/断线状态
- 重连截止时间

当本地 UI 与最新 `RoomSnapshot` 不一致时，以服务端快照为准。

## 本地联调步骤

1. 启动服务端本地依赖：

```powershell
.\server\scripts\start-local-infra.bat
```

2. 启动服务端：

```powershell
cd G:\Jinwiforz\iHomeland\server
.\scripts\run.bat
```

3. 检查基础接口：

```powershell
.\server\scripts\verify-local.bat
```

4. Unity 客户端请求 `/version`，确认版本响应可解析。

5. Unity 客户端连接 `ws://127.0.0.1:8080/ws`。

6. 发送 `HeartbeatRequest`，确认收到 `HeartbeatResponse`。

7. 发送 `CreateRoomRequest`，确认收到 `CreateRoomResponse` 和 `RoomSnapshot`。

8. 使用第二个测试玩家发送 `JoinRoomRequest`，确认成员列表刷新。

9. 发送 `SetReadyRequest`，确认准备状态以服务端快照刷新。

10. 断开连接后在重连保留期内发送 `ReconnectRoomRequest`，确认身份恢复。

## 当前验收边界

在 Unity 工程创建前，服务端侧通过 Go 测试验证 `/ws`、envelope、心跳、错误响应和房间大厅链路：

```powershell
cd G:\Jinwiforz\iHomeland\server
go test ./...
```

Unity 工程创建后，必须补充 Unity 侧协议生成脚本和客户端联调验证。
