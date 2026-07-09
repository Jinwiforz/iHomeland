# Gateway 规格

## Purpose

定义实时网关连接入口、WebSocket 会话、心跳、协议错误和消息分发边界的长期行为契约，确保传输层不承载业务状态机。

## Requirements

### Requirement: 网关负责连接和协议入口
MUST:网关必须负责连接生命周期、envelope 编解码、协议版本校验、心跳、超时和消息分发。

#### Scenario: 客户端建立连接
- **WHEN** 客户端连接网关
- **THEN** 网关创建连接级 session 并准备接收 envelope 消息

### Requirement: 网关不得承载复杂业务状态机
MUST:网关不得直接维护房间状态机或修改房间内部状态，必须通过业务服务接口调用。

#### Scenario: 客户端请求加入房间
- **WHEN** 网关收到加入房间消息
- **THEN** 网关将请求转发给 room service 接口处理

### Requirement: 连接必须有心跳和清理机制
MUST:网关必须支持心跳、空闲超时、断开日志和资源清理。

#### Scenario: 客户端心跳超时
- **WHEN** 客户端超过配置时间未发送心跳
- **THEN** 网关关闭连接并清理 session

### Requirement: 网关必须提供 WebSocket 实时入口
MUST:网关必须提供一个 WebSocket endpoint 作为第一阶段默认实时传输入口，并且该入口必须只接收二进制 Protobuf envelope 消息。

#### Scenario: 客户端通过 WebSocket 建立实时连接
- **WHEN** 客户端请求网关 WebSocket endpoint 并完成升级
- **THEN** 网关创建连接级 session 并开始接收二进制 envelope

#### Scenario: 客户端发送非二进制消息
- **WHEN** 客户端在 WebSocket 连接上发送非二进制消息
- **THEN** 网关返回或记录协议错误并按策略关闭连接

### Requirement: 网关必须维护连接级 session 生命周期
MUST:网关必须为每个 WebSocket 连接维护单进程内的连接级 session，session 至少记录 connection id、远端地址、协议版本、建立时间、最后活跃时间和关闭原因，并且连接关闭时必须清理。

#### Scenario: 连接注册
- **WHEN** WebSocket 连接建立成功
- **THEN** 网关为该连接分配唯一 connection id 并注册 session

#### Scenario: 连接关闭清理
- **WHEN** 客户端断开、服务端关闭或连接超时
- **THEN** 网关注销 session、释放连接资源并记录关闭原因

### Requirement: 网关必须校验 envelope 和协议版本
MUST:网关收到 WebSocket 消息后必须先解析 Protobuf envelope，再校验协议版本、message id、request id 和 payload，无法处理时必须返回结构化错误响应。

#### Scenario: 客户端协议版本不受支持
- **WHEN** 客户端发送协议版本低于或高于服务端支持范围的 envelope
- **THEN** 网关返回协议版本不支持错误并携带服务端支持的版本范围

#### Scenario: 客户端发送非法 payload
- **WHEN** 客户端发送无法解码为目标 message id 对应消息的 envelope
- **THEN** 网关返回 `PAYLOAD_INVALID` 结构化错误响应

#### Scenario: 客户端发送不支持的 message id
- **WHEN** 客户端发送当前服务端未注册或未支持的 message id
- **THEN** 网关返回 `MESSAGE_ID_UNSUPPORTED` 结构化错误响应

### Requirement: 网关必须处理协议心跳和空闲超时
MUST:网关必须处理基础心跳请求、返回心跳响应、更新连接最后活跃时间，并在超过配置的空闲时间后关闭连接。

#### Scenario: 客户端发送心跳
- **WHEN** 客户端发送 `HeartbeatRequest` envelope
- **THEN** 网关返回 `HeartbeatResponse` envelope 并保留 request id 或 sequence 关联信息

#### Scenario: 连接超过空闲超时
- **WHEN** WebSocket 连接在配置时间内没有收到有效 envelope 或心跳
- **THEN** 网关关闭连接、清理 session 并记录超时原因

### Requirement: 网关必须通过接口分发业务消息
MUST:网关不得直接修改房间状态或承载房间状态机；除系统消息外的业务消息必须通过明确的分发接口交给业务模块处理。

#### Scenario: 收到后续房间业务消息
- **WHEN** 网关收到已注册的房间业务 message id
- **THEN** 网关将 session 摘要、envelope 元数据和业务 payload 转发给 room service 边界处理

#### Scenario: 未注册业务处理器
- **WHEN** 网关收到当前没有处理器的业务 message id
- **THEN** 网关返回 `MESSAGE_ID_UNSUPPORTED` 结构化错误响应，不修改任何房间状态

### Requirement: 网关 session 必须支持登录身份绑定
MUST:网关连接级 session 必须能在账号登录或会话恢复成功后绑定 `player_id`，并在登出、连接关闭或会话失效时清理该身份。

#### Scenario: 登录成功后绑定身份
- **WHEN** account service 确认登录成功
- **THEN** 网关 session 记录该连接对应的 `player_id` 和 session token 摘要

#### Scenario: 登出后清理身份
- **WHEN** 已登录连接完成登出
- **THEN** 网关 session 必须清理玩家身份，使后续需要登录的业务请求无法通过身份校验

### Requirement: 网关必须通过账号边界处理账号消息
MUST:网关不得直接校验账号凭据或维护账号业务状态，账号会话消息必须通过 account service 或 account dispatcher 处理。

#### Scenario: 收到登录请求
- **WHEN** 网关收到账号登录 message id
- **THEN** 网关将 session 摘要、envelope 元数据和 payload 转发给 account 边界处理

#### Scenario: 账号处理器缺失
- **WHEN** 网关收到账号会话 message id 但没有注册 account 处理器
- **THEN** 网关返回 `MESSAGE_ID_UNSUPPORTED` 结构化错误，不改变连接身份

### Requirement: 房间业务分发必须使用连接身份授权
MUST:gateway 将房间大厅请求交给业务分发边界时，服务端必须使用 connection session 中已绑定的玩家身份作为授权依据；未绑定玩家身份的 connection 不得执行房间大厅状态变更。

#### Scenario: 未登录连接发送房间请求
- **WHEN** 未绑定玩家身份的 WebSocket connection 发送创建、加入、准备、退出、房主转移或重连房间请求
- **THEN** 服务端必须返回结构化 `UNAUTHENTICATED` 错误
- **AND** 不得调用 room service 修改房间状态

#### Scenario: 已登录连接发送房间请求
- **WHEN** 已绑定玩家身份的 WebSocket connection 发送房间大厅请求
- **THEN** 服务端必须把 connection session 中的玩家身份用于请求授权校验
