## ADDED Requirements

### Requirement: 网关必须提供 WebSocket 实时入口
网关必须提供一个 WebSocket endpoint 作为第一阶段默认实时传输入口，并且该入口必须只接收二进制 Protobuf envelope 消息。

#### Scenario: 客户端通过 WebSocket 建立实时连接
- **WHEN** 客户端请求网关 WebSocket endpoint 并完成升级
- **THEN** 网关创建连接级 session 并开始接收二进制 envelope

#### Scenario: 客户端发送非二进制消息
- **WHEN** 客户端在 WebSocket 连接上发送非二进制消息
- **THEN** 网关返回或记录协议错误并按策略关闭连接

### Requirement: 网关必须维护连接级 session 生命周期
网关必须为每个 WebSocket 连接维护单进程内的连接级 session，session 至少记录 connection id、远端地址、协议版本、建立时间、最后活跃时间和关闭原因，并且连接关闭时必须清理。

#### Scenario: 连接注册
- **WHEN** WebSocket 连接建立成功
- **THEN** 网关为该连接分配唯一 connection id 并注册 session

#### Scenario: 连接关闭清理
- **WHEN** 客户端断开、服务端关闭或连接超时
- **THEN** 网关注销 session、释放连接资源并记录关闭原因

### Requirement: 网关必须校验 envelope 和协议版本
网关收到 WebSocket 消息后必须先解析 Protobuf envelope，再校验协议版本、message id、request id 和 payload，无法处理时必须返回结构化错误响应。

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
网关必须处理基础心跳请求、返回心跳响应、更新连接最后活跃时间，并在超过配置的空闲时间后关闭连接。

#### Scenario: 客户端发送心跳
- **WHEN** 客户端发送 `HeartbeatRequest` envelope
- **THEN** 网关返回 `HeartbeatResponse` envelope 并保留 request id 或 sequence 关联信息

#### Scenario: 连接超过空闲超时
- **WHEN** WebSocket 连接在配置时间内没有收到有效 envelope 或心跳
- **THEN** 网关关闭连接、清理 session 并记录超时原因

### Requirement: 网关必须通过接口分发业务消息
网关不得直接修改房间状态或承载房间状态机；除系统消息外的业务消息必须通过明确的分发接口交给业务模块处理。

#### Scenario: 收到后续房间业务消息
- **WHEN** 网关收到已注册的房间业务 message id
- **THEN** 网关将 session 摘要、envelope 元数据和业务 payload 转发给 room service 边界处理

#### Scenario: 未注册业务处理器
- **WHEN** 网关收到当前没有处理器的业务 message id
- **THEN** 网关返回 `MESSAGE_ID_UNSUPPORTED` 结构化错误响应，不修改任何房间状态
