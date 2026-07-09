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

### Requirement: Unity 客户端必须提供本地 WebSocket smoke test 入口
MUST:Unity 客户端必须提供仅面向开发者的本地 smoke test 入口，用于验证本地服务端 `/ws`、二进制 Protobuf envelope、心跳和账号会话请求链路。

#### Scenario: 执行 WebSocket smoke test 成功
- **WHEN** 开发者在本地服务端、MySQL 和 Redis 可用时执行 Unity smoke test
- **THEN** smoke test 必须能连接 `/ws`，发送 `HeartbeatRequest` 并收到 `HeartbeatResponse`
- **AND** smoke test 必须能完成测试账号注册或登录，并在获得 session 后登出

#### Scenario: 测试账号已存在
- **WHEN** smoke test 注册固定测试账号时服务端返回账号已存在
- **THEN** smoke test 必须使用相同测试凭据回退登录，而不是创建新的随机账号

#### Scenario: 本地依赖不可用
- **WHEN** 本地服务端、MySQL 或 Redis 未启动导致 smoke test 失败
- **THEN** smoke test 必须输出可诊断的错误信息，并不得改变运行时玩家 UI 流程

### Requirement: Unity 客户端必须按房间快照刷新大厅 UI
MUST:Unity 客户端执行房间大厅操作后，必须以服务端返回的 `RoomSnapshot` 作为 UI 状态来源。

#### Scenario: 房间操作成功
- **WHEN** Unity 客户端创建、加入、准备、退出、转移房主或重连成功
- **THEN** 它必须使用响应中的 `RoomSnapshot` 刷新房间名、房主、成员、座位、阵营、准备状态和连接状态

#### Scenario: 本地状态与服务端状态不一致
- **WHEN** Unity 客户端本地 UI 状态与最新 `RoomSnapshot` 不一致
- **THEN** 它必须以服务端快照为准

### Requirement: Unity 客户端必须使用服务端注册登录登出
MUST:Unity 客户端不得把本地账号表单校验成功当作登录成功，必须通过服务端账号会话协议完成注册、登录、登出和会话恢复。

#### Scenario: 客户端注册成功
- **WHEN** 服务端返回注册成功响应
- **THEN** Unity 客户端记录玩家资料和 session 状态，并进入 `HomePage`

#### Scenario: 客户端登录成功
- **WHEN** 服务端返回登录成功响应
- **THEN** Unity 客户端记录玩家资料和 session 状态，并进入 `HomePage`

#### Scenario: 客户端登出成功
- **WHEN** 服务端返回登出成功响应
- **THEN** Unity 客户端清理本地账号状态并返回 `LoginPage`

### Requirement: Unity 客户端进入房间大厅前必须具备登录身份
MUST:Unity 客户端进入创房、进房、退房、房主转移等房间大厅流程前，必须拥有服务端确认的玩家身份。

#### Scenario: 未登录点击 Start Game
- **WHEN** 未登录客户端触发进入联机流程
- **THEN** 客户端必须返回或停留在 `LoginPage`，不得发送房间大厅请求

#### Scenario: 已登录点击 Start Game
- **WHEN** 已登录客户端触发进入联机流程
- **THEN** 客户端应进入房间大厅或创建/加入房间流程，而不是直接进入战斗模拟

### Requirement: Unity 客户端必须处理会话恢复失败
MUST:Unity 客户端启动时若尝试使用本地 session token 恢复会话失败，必须清理本地登录状态并展示登录入口。

#### Scenario: 本地 token 过期
- **WHEN** 客户端启动并提交过期 session token
- **THEN** 服务端拒绝恢复，客户端清理 token 并显示 `LoginPage`

### Requirement: Unity 联调文档必须包含最小验收路径
MUST:Unity 客户端接入文档必须提供本地服务端启动、版本检查、WebSocket 连接、心跳和房间大厅请求的最小验收路径。

#### Scenario: 开发者验证 Unity 接入
- **WHEN** 开发者按 Unity 接入文档进行联调
- **THEN** 文档必须能引导其确认 `/version`、`/ws`、心跳和至少一个房间大厅请求响应链路是否正常

### Requirement: Unity 客户端必须通过房间大厅进入联机流程
MUST:Unity 客户端在已登录玩家触发 `Start Game` 后，必须进入房间大厅入口或创建/加入房间流程，不得直接进入 `BattleScene` 或正式战斗模拟。

#### Scenario: 已登录玩家点击 Start Game
- **WHEN** 已登录玩家在 `HomePage` 触发 `Start Game`
- **THEN** Unity 客户端必须打开房间大厅入口或房间大厅页面
- **AND** 客户端不得加载 `BattleScene`

#### Scenario: 未登录玩家点击 Start Game
- **WHEN** 未登录玩家在 `HomePage` 触发 `Start Game`
- **THEN** Unity 客户端必须返回或停留在 `LoginPage`
- **AND** 客户端不得发送任何房间大厅请求

### Requirement: Unity 客户端必须提供房间大厅运行时状态边界
MUST:Unity 客户端必须通过独立房间状态边界管理当前 `RoomSnapshot`、房间操作结果和本地重连上下文，UI 页面不得自行维护最终房间事实状态。

#### Scenario: 房间操作成功
- **WHEN** Unity 客户端收到创建、加入、准备、退出、房主转移或重连响应
- **THEN** 房间状态边界必须保存响应中的最新 `RoomSnapshot`
- **AND** UI 必须从该快照刷新展示状态

#### Scenario: 登出或会话失效
- **WHEN** 玩家登出或账号会话恢复失败
- **THEN** Unity 客户端必须清理当前房间状态和本地重连上下文

### Requirement: Unity 客户端房间请求必须使用服务端确认的玩家身份
MUST:Unity 客户端发送现有房间大厅请求时，必须使用 `AccountSystem` 中服务端确认的当前玩家 ID 填充 payload `player_id`，不得允许 UI 输入或临时本地字符串决定玩家身份。

#### Scenario: 创建房间请求
- **WHEN** 已登录玩家请求创建房间
- **THEN** `CreateRoomRequest.player_id` 必须等于当前账号会话的 `CurrentPlayerID`

#### Scenario: 当前玩家身份缺失
- **WHEN** 客户端没有服务端确认的当前玩家 ID
- **THEN** 客户端必须拒绝发送房间大厅请求并提示需要重新登录

### Requirement: Unity 客户端必须提供房间大厅基础操作 UI
MUST:Unity 客户端必须提供可操作的房间大厅 UI，支持创建房间、输入 room id 加入房间、准备/取消准备、退出房间、房主转移和查看最新成员快照。

#### Scenario: 创建房间成功
- **WHEN** 玩家在房间大厅 UI 提交房间名和容量并且服务端创建成功
- **THEN** UI 必须展示服务端返回的房间 ID、房间名、容量、房主和成员列表

#### Scenario: 加入房间成功
- **WHEN** 玩家输入有效 room id 并且服务端加入成功
- **THEN** UI 必须展示服务端返回的最新成员列表、座位、阵营、准备状态和连接状态

#### Scenario: 设置准备状态
- **WHEN** 玩家点击准备或取消准备并且服务端处理成功
- **THEN** UI 必须以响应中的 `RoomSnapshot` 更新该玩家准备状态

#### Scenario: 房主转移成功
- **WHEN** 房主选择目标成员并且服务端转移成功
- **THEN** UI 必须以响应中的 `RoomSnapshot` 更新房主标记和可用操作

#### Scenario: 退出房间
- **WHEN** 玩家点击退出房间并且服务端处理成功
- **THEN** Unity 客户端必须清理当前房间快照并返回房间大厅入口或 `HomePage`

### Requirement: Unity 客户端必须支持房间身份重连恢复
MUST:Unity 客户端在本地存在最近 room id 且当前账号已登录时，必须允许尝试发送 `ReconnectRoomRequest` 恢复房间身份；恢复是否成功以服务端响应为准。

#### Scenario: 重连恢复成功
- **WHEN** 客户端使用当前玩家 ID 和最近 room id 发送 `ReconnectRoomRequest` 且服务端返回成功
- **THEN** 客户端必须保存响应中的 `RoomSnapshot` 并展示恢复后的房间大厅 UI

#### Scenario: 重连恢复失败
- **WHEN** 服务端拒绝 `ReconnectRoomRequest`
- **THEN** 客户端必须清理本地最近房间上下文
- **AND** 客户端必须停留在房间大厅入口或返回 `HomePage`

### Requirement: Unity 房间大厅联调文档必须覆盖最小端到端验收路径
MUST:Unity 客户端接入文档必须记录房间大厅端到端验收路径，覆盖本地服务端启动、登录、创建房间、第二玩家加入、准备/取消准备、退出、房主转移和重连恢复。

#### Scenario: 开发者执行本地房间大厅验收
- **WHEN** 开发者按文档启动本地服务端和 Unity 客户端
- **THEN** 文档必须能引导其完成至少一个创建房间、加入房间和准备状态刷新的成功链路

### Requirement: Unity 客户端必须提供房间开始入口
MUST:Unity 客户端必须在房间大厅 UI 中为当前房主提供开始房间入口，并通过实时协议发送 `StartRoomRequest`，不得在本地绕过服务端直接进入正式战斗。

#### Scenario: 房主点击开始
- **WHEN** 当前玩家是房主并点击开始房间
- **THEN** Unity 客户端必须发送 `StartRoomRequest` 并等待服务端响应

#### Scenario: 非房主查看房间页
- **WHEN** 当前玩家不是房主
- **THEN** Unity 客户端不得允许其发起开始房间请求

#### Scenario: 开始成功
- **WHEN** Unity 客户端收到成功的 `StartRoomResponse`
- **THEN** 客户端必须保存响应中的最新 `RoomSnapshot`
- **AND** 客户端可以进入后续占位场景或展示房间已开始状态

#### Scenario: 开始失败
- **WHEN** Unity 客户端收到开始房间的结构化错误响应
- **THEN** 客户端必须保持在房间大厅 UI，并展示或记录失败原因

### Requirement: Unity 客户端不得把开始闸门当作正式战斗
MUST:Unity 客户端对开始房间成功的处理只能进入第一阶段占位流程，不得实现或暗含 battle server、高频战斗同步、战斗结算、观战或回放。

#### Scenario: 开始成功后进入占位流程
- **WHEN** 房间开始请求成功
- **THEN** 客户端进入的后续界面或场景必须被视为占位流程，而不是正式战斗模拟
