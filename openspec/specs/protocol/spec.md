# Protocol 规格

## Purpose

定义实时通信 Protobuf envelope、协议版本、消息 ID 和兼容策略的长期行为契约，确保客户端和服务端可追溯地演进协议。

## Requirements

### Requirement: 实时通信必须使用 Protobuf Envelope
MUST:客户端与服务端的实时通信必须通过 Protobuf envelope 承载，WebSocket 和后续 TCP 必须复用同一 envelope 结构。

#### Scenario: 网关收到实时消息
- **WHEN** 网关收到客户端实时消息
- **THEN** 网关先解析 envelope，再根据 message id 分发消息

### Requirement: 协议版本必须显式校验
MUST:服务端必须校验客户端协议版本，不支持的版本必须返回结构化错误。

#### Scenario: 客户端版本不兼容
- **WHEN** 客户端使用不支持的协议版本连接
- **THEN** 服务端返回协议版本错误并按策略关闭或限制连接

### Requirement: 协议变更必须保持可追溯
MUST:新增、废弃或破坏性协议变更必须记录兼容策略，并更新对应文档或 OpenSpec change。

#### Scenario: 字段被废弃
- **WHEN** Protobuf 字段不再使用
- **THEN** schema 使用 reserved 保留字段编号和名称

### Requirement: Envelope 必须定义稳定字段
MUST:实时通信 envelope 必须包含协议版本、消息 ID、请求 ID、序列号、时间戳和 Protobuf payload，并且 WebSocket 与后续 TCP 必须复用同一结构。

#### Scenario: 编码业务消息
- **WHEN** 服务端或客户端发送实时业务消息
- **THEN** 消息必须被包装到统一 envelope 中，并携带协议版本、消息 ID 和 payload

#### Scenario: 复用传输层
- **WHEN** 后续新增 TCP 传输
- **THEN** TCP 必须复用同一 envelope schema，不得定义另一套业务消息信封

### Requirement: 消息 ID 必须稳定且不可复用
MUST:每个可路由消息必须分配稳定 message id；已发布 message id 不得复用，废弃消息必须保留编号并记录兼容策略。

#### Scenario: 新增消息
- **WHEN** 新增实时消息类型
- **THEN** 必须分配新的 message id，并在协议文档或注册表中记录用途和 owner

#### Scenario: 废弃消息
- **WHEN** 某个实时消息不再使用
- **THEN** 该 message id 必须保留为废弃状态，不得分配给其他消息

### Requirement: 请求响应必须可关联
MUST:需要响应的实时请求必须携带 request id；响应消息必须回传相同 request id，用于客户端和服务端关联请求、响应和错误。

#### Scenario: 请求成功
- **WHEN** 客户端发送需要响应的请求
- **THEN** 服务端响应必须携带相同 request id

#### Scenario: 请求失败
- **WHEN** 客户端请求因协议、权限或状态错误失败
- **THEN** 服务端错误响应必须携带相同 request id

### Requirement: 序列号必须支持基础去重和排查
MUST:客户端和服务端发送的 envelope 必须支持单连接内递增 sequence，用于基础顺序排查、重复消息识别和日志关联。

#### Scenario: 连续发送消息
- **WHEN** 同一连接连续发送多条 envelope
- **THEN** 发送方必须为这些 envelope 填充单连接内递增 sequence

### Requirement: 协议必须提供基础系统消息
MUST:协议必须定义心跳请求、心跳响应和错误响应等基础系统消息，作为网关连接生命周期和协议错误处理的共同契约。

#### Scenario: 心跳
- **WHEN** 客户端发送心跳请求
- **THEN** 服务端必须能返回心跳响应，并保留 request id 或 sequence 关联信息

#### Scenario: 结构化错误
- **WHEN** 服务端拒绝或无法处理某个 envelope
- **THEN** 服务端必须返回结构化错误响应，包含稳定错误码和可诊断上下文

### Requirement: 协议版本拒绝必须携带支持范围
MUST:当客户端协议版本不被服务端支持时，错误响应必须携带服务端支持的最小版本和最大版本。

#### Scenario: 客户端版本过低
- **WHEN** 客户端发送低于服务端支持范围的协议版本
- **THEN** 服务端必须返回协议版本不支持错误，并包含支持的版本范围

#### Scenario: 客户端版本过高
- **WHEN** 客户端发送高于服务端支持范围的协议版本
- **THEN** 服务端必须返回协议版本不支持错误，并包含支持的版本范围

### Requirement: 协议代码必须由 Protobuf 生成
MUST:服务端和客户端使用的协议类型必须由 Protobuf schema 生成，生成代码不得手工修改。

#### Scenario: 生成 Go 协议代码
- **WHEN** 开发者运行协议生成入口
- **THEN** Go 协议代码必须从 `shared/proto/` 下的 schema 生成到约定目录

#### Scenario: 修改生成代码
- **WHEN** 需要调整协议字段或消息类型
- **THEN** 必须修改 Protobuf schema 并重新生成代码，不得手工编辑生成结果

### Requirement: 协议必须定义房间大厅消息
MUST:实时协议必须为自定义房间大厅定义稳定的 Protobuf 消息和 message id，所有房间大厅消息必须使用 `2000-2999` 号段并记录 owner 为 `room`。

#### Scenario: 新增创建房间消息
- **WHEN** 协议新增 `CreateRoomRequest` 和 `CreateRoomResponse`
- **THEN** 它们必须分配稳定 message id，并在协议注册表中记录用途和 owner

#### Scenario: 新增加入房间消息
- **WHEN** 协议新增 `JoinRoomRequest` 和 `JoinRoomResponse`
- **THEN** 它们必须使用房间大厅号段，并通过 request id 关联请求和响应

#### Scenario: 新增准备和退出消息
- **WHEN** 协议新增准备、取消准备、退出和房主转移消息
- **THEN** 每个可路由消息必须有稳定 message id，且响应必须返回相同 request id

### Requirement: 协议必须定义账号会话消息
MUST:实时协议必须为第一阶段账号会话定义稳定的 Protobuf 消息和 message id，所有账号会话消息必须使用 `1000-1999` 号段并记录 owner 为 `account`。

#### Scenario: 新增登录消息
- **WHEN** 协议新增 `LoginRequest` 和 `LoginResponse`
- **THEN** 它们必须使用账号会话号段，并通过 request id 关联请求和响应

#### Scenario: 新增注册消息
- **WHEN** 协议新增 `RegisterRequest` 和 `RegisterResponse`
- **THEN** 它们必须使用账号会话号段，并通过 request id 关联请求和响应

#### Scenario: 新增登出和恢复消息
- **WHEN** 协议新增 `LogoutRequest`、`ResumeSessionRequest` 和对应响应
- **THEN** 每个可路由消息必须有稳定 message id，且响应必须返回相同 request id

### Requirement: 账号协议响应必须携带玩家资料和会话状态
MUST:账号注册、登录和会话恢复成功响应必须携带客户端进入在线流程所需的玩家资料、session token 和过期时间。

#### Scenario: 注册成功响应
- **WHEN** 服务端处理注册请求成功
- **THEN** 响应 payload 必须包含 `PlayerProfile`、`session_token` 和 `expires_at_ms`

#### Scenario: 登录成功响应
- **WHEN** 服务端处理登录请求成功
- **THEN** 响应 payload 必须包含 `PlayerProfile`、`session_token` 和 `expires_at_ms`

#### Scenario: 会话恢复成功响应
- **WHEN** 服务端处理有效 session token 的恢复请求成功
- **THEN** 响应 payload 必须包含最新玩家资料和会话过期时间

### Requirement: 账号协议错误必须复用结构化错误响应
MUST:账号请求失败时必须复用实时协议的 `ErrorResponse`，并通过稳定错误码或 detail 表达账号已存在、凭据非法、未登录、session 过期和权限不足。

#### Scenario: 注册账号已存在
- **WHEN** 客户端提交已存在账号名进行注册
- **THEN** 服务端返回结构化错误并保留原请求的 request id

#### Scenario: 登录凭据非法
- **WHEN** 客户端提交非法账号凭据
- **THEN** 服务端返回结构化错误并保留原请求的 request id

#### Scenario: session token 无效
- **WHEN** 客户端提交无效或过期 session token
- **THEN** 服务端返回结构化错误，客户端不得继续视为已登录

### Requirement: 房间协议响应必须携带房间快照
MUST:房间大厅操作成功响应必须携带可供客户端刷新大厅界面的房间快照，快照必须包含房间状态、房主、成员、座位、阵营、准备状态和连接状态。

#### Scenario: 创建房间成功
- **WHEN** 服务端处理创建房间请求成功
- **THEN** 响应 payload 必须包含最新房间快照

#### Scenario: 成员状态变化
- **WHEN** 成员加入、准备、退出、房主转移或重连恢复成功
- **THEN** 响应 payload 必须包含变化后的房间快照

### Requirement: 房间协议请求必须携带玩家身份和请求关联信息
MUST:在账号系统接入前，房间大厅请求必须显式携带 `player_id`，并且需要响应的请求必须携带 envelope `request_id`。

#### Scenario: 请求缺少 player id
- **WHEN** 客户端发送缺少 `player_id` 的房间大厅请求
- **THEN** 服务端必须拒绝请求并返回结构化错误

#### Scenario: 请求缺少 request id
- **WHEN** 客户端发送需要响应但缺少 envelope `request_id` 的房间大厅请求
- **THEN** 网关或 room dispatcher 必须拒绝请求并返回 `REQUEST_ID_REQUIRED`

### Requirement: 房间协议错误必须复用结构化错误响应
MUST:房间大厅请求失败时必须复用实时协议的 `ErrorResponse`，并通过稳定错误码或可诊断 detail 表达权限、状态迁移、容量、成员不存在和重连资格错误。

#### Scenario: 加入已满房间失败
- **WHEN** 玩家请求加入已满房间
- **THEN** 服务端返回结构化错误响应并保留原请求的 request id

#### Scenario: 非房主转移房主失败
- **WHEN** 非房主请求转移房主
- **THEN** 服务端返回结构化错误响应并保持房间状态不变

### Requirement: 房间快照推送必须可追踪
MUST:当房间状态因成员操作发生变化时，服务端可以向相关连接发送房间快照推送；推送消息必须使用稳定 message id 并携带可诊断的 sequence。

#### Scenario: 成员加入后推送快照
- **WHEN** 成员加入导致房间状态变化
- **THEN** 服务端可以向房间内在线成员发送 `RoomSnapshotPushed`，并携带最新房间快照
