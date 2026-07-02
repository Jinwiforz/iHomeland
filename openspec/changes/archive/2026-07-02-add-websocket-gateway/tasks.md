## 1. 依赖与配置

- [x] 1.1 在 `server/go.mod` 中新增 WebSocket 依赖，并运行 Go module 整理命令更新 `go.sum`
- [x] 1.2 在服务端配置中新增 gateway 心跳超时、读超时或空闲超时配置项，并补充配置默认值与校验测试
- [x] 1.3 在 `server/internal/app` 中预留 gateway 组装入口，保持现有 `/healthz`、`/readyz` 和 `/version` 行为不变

## 2. Gateway 核心结构

- [x] 2.1 创建 `server/internal/gateway` 包，定义 `Server`、`Session`、`Dispatcher` 和连接关闭原因类型
- [x] 2.2 实现 WebSocket endpoint 注册和升级逻辑，只接受二进制消息
- [x] 2.3 实现 connection id 分配、session 注册、最后活跃时间更新、注销和资源清理
- [x] 2.4 实现连接读循环、写响应路径、context 取消和关闭握手

## 3. Envelope 处理与分发

- [x] 3.1 在 gateway 中接入 `protocol` 包完成 envelope 解析、payload 解码和协议版本校验
- [x] 3.2 实现 `HeartbeatRequest` 到 `HeartbeatResponse` 的处理，并保留 request id 或 sequence 关联信息
- [x] 3.3 对非法 payload、不支持 message id、缺失 request id 和协议版本不支持返回结构化错误响应
- [x] 3.4 实现非系统消息的 `Dispatcher` 调用路径，未注册处理器时返回 `MESSAGE_ID_UNSUPPORTED`

## 4. 集成与日志

- [x] 4.1 在 `app.NewHTTPServer` 中注册 WebSocket gateway endpoint
- [x] 4.2 为连接建立、协议错误、心跳超时、客户端断开和清理结果添加稳定结构化日志字段
- [x] 4.3 确认 gateway 不直接依赖 room 状态机、Redis、MySQL、Gin handler 业务状态或 TCP socket

## 5. 测试与验证

- [x] 5.1 添加 WebSocket 连接成功并创建 session 的测试
- [x] 5.2 添加心跳请求返回心跳响应的测试
- [x] 5.3 添加协议版本不支持并携带支持范围的测试
- [x] 5.4 添加非法 payload、非二进制消息和未知 message id 的错误响应或关闭测试
- [x] 5.5 添加空闲超时关闭连接并清理 session 的测试
- [x] 5.6 运行 `server/scripts/test.bat` 或等价 Go 测试命令，确认服务端测试通过
