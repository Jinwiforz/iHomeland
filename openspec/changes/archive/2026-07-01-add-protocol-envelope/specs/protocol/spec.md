## ADDED Requirements

### Requirement: Envelope 必须定义稳定字段
实时通信 envelope 必须包含协议版本、消息 ID、请求 ID、序列号、时间戳和 Protobuf payload，并且 WebSocket 与后续 TCP 必须复用同一结构。

#### Scenario: 编码业务消息
- **WHEN** 服务端或客户端发送实时业务消息
- **THEN** 消息必须被包装到统一 envelope 中，并携带协议版本、消息 ID 和 payload

#### Scenario: 复用传输层
- **WHEN** 后续新增 TCP 传输
- **THEN** TCP 必须复用同一 envelope schema，不得定义另一套业务消息信封

### Requirement: 消息 ID 必须稳定且不可复用
每个可路由消息必须分配稳定 message id；已发布 message id 不得复用，废弃消息必须保留编号并记录兼容策略。

#### Scenario: 新增消息
- **WHEN** 新增实时消息类型
- **THEN** 必须分配新的 message id，并在协议文档或注册表中记录用途和 owner

#### Scenario: 废弃消息
- **WHEN** 某个实时消息不再使用
- **THEN** 该 message id 必须保留为废弃状态，不得分配给其他消息

### Requirement: 请求响应必须可关联
需要响应的实时请求必须携带 request id；响应消息必须回传相同 request id，用于客户端和服务端关联请求、响应和错误。

#### Scenario: 请求成功
- **WHEN** 客户端发送需要响应的请求
- **THEN** 服务端响应必须携带相同 request id

#### Scenario: 请求失败
- **WHEN** 客户端请求因协议、权限或状态错误失败
- **THEN** 服务端错误响应必须携带相同 request id

### Requirement: 序列号必须支持基础去重和排查
客户端和服务端发送的 envelope 必须支持单连接内递增 sequence，用于基础顺序排查、重复消息识别和日志关联。

#### Scenario: 连续发送消息
- **WHEN** 同一连接连续发送多条 envelope
- **THEN** 发送方必须为这些 envelope 填充单连接内递增 sequence

### Requirement: 协议必须提供基础系统消息
协议必须定义心跳请求、心跳响应和错误响应等基础系统消息，作为网关连接生命周期和协议错误处理的共同契约。

#### Scenario: 心跳
- **WHEN** 客户端发送心跳请求
- **THEN** 服务端必须能返回心跳响应，并保留 request id 或 sequence 关联信息

#### Scenario: 结构化错误
- **WHEN** 服务端拒绝或无法处理某个 envelope
- **THEN** 服务端必须返回结构化错误响应，包含稳定错误码和可诊断上下文

### Requirement: 协议版本拒绝必须携带支持范围
当客户端协议版本不被服务端支持时，错误响应必须携带服务端支持的最小版本和最大版本。

#### Scenario: 客户端版本过低
- **WHEN** 客户端发送低于服务端支持范围的协议版本
- **THEN** 服务端必须返回协议版本不支持错误，并包含支持的版本范围

#### Scenario: 客户端版本过高
- **WHEN** 客户端发送高于服务端支持范围的协议版本
- **THEN** 服务端必须返回协议版本不支持错误，并包含支持的版本范围

### Requirement: 协议代码必须由 Protobuf 生成
服务端和客户端使用的协议类型必须由 Protobuf schema 生成，生成代码不得手工修改。

#### Scenario: 生成 Go 协议代码
- **WHEN** 开发者运行协议生成入口
- **THEN** Go 协议代码必须从 `shared/proto/` 下的 schema 生成到约定目录

#### Scenario: 修改生成代码
- **WHEN** 需要调整协议字段或消息类型
- **THEN** 必须修改 Protobuf schema 并重新生成代码，不得手工编辑生成结果
