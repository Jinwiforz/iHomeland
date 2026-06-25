# Gateway 规格

## Requirements

### Requirement: 网关负责连接和协议入口
网关必须负责连接生命周期、envelope 编解码、协议版本校验、心跳、超时和消息分发。

#### Scenario: 客户端建立连接
- **WHEN** 客户端连接网关
- **THEN** 网关创建连接级 session 并准备接收 envelope 消息

### Requirement: 网关不得承载复杂业务状态机
网关不得直接维护房间状态机或修改房间内部状态，必须通过业务服务接口调用。

#### Scenario: 客户端请求加入房间
- **WHEN** 网关收到加入房间消息
- **THEN** 网关将请求转发给 room service 接口处理

### Requirement: 连接必须有心跳和清理机制
网关必须支持心跳、空闲超时、断开日志和资源清理。

#### Scenario: 客户端心跳超时
- **WHEN** 客户端超过配置时间未发送心跳
- **THEN** 网关关闭连接并清理 session
