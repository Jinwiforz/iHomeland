## 1. Protobuf 源文件

- [x] 1.1 创建 `shared/proto/realtime/v1/envelope.proto`
- [x] 1.2 在 `envelope.proto` 中定义 `Envelope`，包含 `protocol_version`、`message_id`、`request_id`、`sequence`、`timestamp_ms` 和 `payload`
- [x] 1.3 定义基础系统消息：`HeartbeatRequest`、`HeartbeatResponse`、`ErrorResponse`、`ProtocolVersionUnsupported`
- [x] 1.4 在 Protobuf 注释中说明字段用途、版本边界和保留规则
- [x] 1.5 创建消息 ID 注册说明或常量来源，覆盖系统消息号段

## 2. 生成入口

- [x] 2.1 为服务端添加 Protobuf Go 生成依赖和必要工具说明
- [x] 2.2 创建 `tools/proto/generate.bat`，使用相对路径生成 Go 协议代码
- [x] 2.3 将 Go 生成结果限制在 `server/internal/protocol` 或明确的生成子目录内
- [x] 2.4 为 Unity/Godot 客户端输出预留目录或文档说明，不生成引擎专用代码

## 3. 服务端协议适配

- [x] 3.1 创建或补充 `server/internal/protocol` 包，封装协议版本范围和 message id 常量
- [x] 3.2 实现 envelope 编码辅助函数，支持将 Protobuf 消息包装为 envelope
- [x] 3.3 实现 envelope 解码辅助函数，支持校验 message id、payload 和 request id
- [x] 3.4 实现协议版本校验函数，返回包含支持范围的结构化错误信息
- [x] 3.5 确保协议适配包不依赖 Gin、WebSocket 或 TCP

## 4. 测试

- [x] 4.1 添加 envelope 编码和解码测试
- [x] 4.2 添加 message id 与消息类型映射测试
- [x] 4.3 添加 request id 在成功响应和错误响应中的关联测试
- [x] 4.4 添加协议版本过低和过高的拒绝测试
- [x] 4.5 添加生成代码可编译验证，确保 `go test ./...` 通过

## 5. 文档

- [x] 5.1 更新 `docs/protocol-compatibility.md`，明确 envelope 字段、message id 号段、错误响应和 reserved 规则
- [x] 5.2 更新 `docs/file-structure.md`，补充 `shared/proto/` 和服务端协议生成目录说明
- [x] 5.3 更新 `server/README.md`，补充协议生成入口
- [x] 5.4 确认文档不包含本机绝对路径、重复入口或无 owner 的说明

## 6. 验证

- [x] 6.1 运行 `tools/proto/generate.bat` 并确认生成结果存在
- [x] 6.2 运行 `server/scripts/test.bat`，确认服务端测试通过
- [x] 6.3 运行 OpenSpec 状态检查，确认 artifacts 和 tasks 可被识别
