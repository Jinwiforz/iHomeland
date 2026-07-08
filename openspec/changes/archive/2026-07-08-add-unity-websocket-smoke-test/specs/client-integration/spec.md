## ADDED Requirements

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
