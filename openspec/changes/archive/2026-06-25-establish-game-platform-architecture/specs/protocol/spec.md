## ADDED Requirements

### Requirement: 实时通信必须使用 Protobuf Envelope
客户端与服务端的实时通信必须通过 Protobuf envelope 承载，WebSocket 和后续 TCP 必须复用同一 envelope 结构。

#### Scenario: 网关收到实时消息
- **WHEN** 网关收到客户端实时消息
- **THEN** 网关先解析 envelope，再根据 message id 分发消息

### Requirement: 协议版本必须显式校验
服务端必须校验客户端协议版本，不支持的版本必须返回结构化错误。

#### Scenario: 客户端版本不兼容
- **WHEN** 客户端使用不支持的协议版本连接
- **THEN** 服务端返回协议版本错误并按策略关闭或限制连接

### Requirement: 协议变更必须保持可追溯
新增、废弃或破坏性协议变更必须记录兼容策略，并更新对应文档或 OpenSpec change。

#### Scenario: 字段被废弃
- **WHEN** Protobuf 字段不再使用
- **THEN** schema 使用 reserved 保留字段编号和名称
