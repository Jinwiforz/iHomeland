## 1. 契约工具链与目录基线

- [x] 1.1 更新 `docs/roadmap.md`、`docs/protocol-compatibility.md` 与 `docs/file-structure.md`，明确 `establish-server-contracts`、S0 最小 Go module、契约目录和 S1 runtime 边界
- [x] 1.2 创建根目录 `versions.yaml` 与最小 `server/go.mod`，在版本目录中按分类记录当前正式协议、工具、语言、基础设施和客户端运行时版本及官方来源，只引入 generated Protobuf、OpenAPI 验证和 contract test 所需依赖
- [x] 1.3 使用版本目录选定的 Buf 配置格式、Protobuf edition、`STANDARD` lint、`FILE` breaking 与锁定依赖，建立分离的 Go local plugin/C# protoc builtin generation templates
- [x] 1.4 创建 `tools/proto/proto.ps1` 非交互 PowerShell 命令，统一 format、lint、generate、fixtures 和 verify 退出码

## 2. Registry 与基础协议

- [x] 2.1 定义 message、error 与 route registry 的 JSON 结构、owner 编号范围、reserved 规则和示例，并为 registry parser 增加单元测试
- [x] 2.2 创建 `common/v1` Protobuf，定义 protocol version、message kind、可靠 envelope、结构化错误与分页基础类型
- [x] 2.3 实现 registry validator，拒绝重复/越界编号、未知 symbol、错误 channel、无效 kind/id 组合和不一致错误映射
- [x] 2.4 为 realtime command 增加身份字段禁用检查，确保操作者只能来自已认证 connection context 与后续 admission

## 3. 基础账号与连接契约

- [x] 3.1 创建 `account/v1` 与 `session/v1` Protobuf，定义账号摘要、session epoch、auth scope、endpoint 与一次性 connection ticket 类型
- [x] 3.2 创建 `control/v1` Protobuf，定义 maintenance、forced logout、queue、endpoint update 与 session invalidation push
- [x] 3.3 为 TLS/TCP 定义通用 `GAMEPLAY` scope，明确连接 capability 不替代后续业务 admission 与授权
- [x] 3.4 分配并登记基础 control message id、error code 与 routes，确认每个实时消息只有一个 allowed channel
- [x] 3.5 使用版本目录选定的 OpenAPI 规范创建 HTTPS contract，覆盖 version/config、register/login/refresh/logout、ticket、endpoint manifest、限制与结构化错误

## 4. 生成代码与静态验证

- [x] 4.1 在编译前生成已忽略的 `server/internal/generated/proto/` Go code，验证 package、`go_package`、注释和 clean regeneration
- [x] 4.2 验证项目内 `protoc` 的版本、SHA-256、C# generation template、namespace 与路径，只在临时目录生成，不提交 C# 产物或创建 Unity 工程
- [x] 4.3 使用生成代码注册的 descriptor 与内存 registry projection，验证 schema full name、OpenAPI `operationId`、message/error/route 引用完全一致
- [x] 4.4 增加 format、lint、breaking、OpenAPI、Protobuf edition、registry、版本目录一致性和 generated diff 的统一验证测试，并在工具不完整支持目标规范时失败

## 5. Envelope、Framing 与 Fixtures

- [x] 5.1 实现无 listener 依赖的可靠 envelope codec 与 4-byte big-endian TLS/TCP frame codec
- [x] 5.2 增加半帧、粘包、连续帧、零长度、截断、超长和 1 MiB 硬上限测试
- [x] 5.3 创建 HTTPS 正向、边界和稳定错误 fixtures，确保不包含真实凭据、密钥或可用 token/ticket
- [x] 5.4 创建实时 deterministic golden packet 生成器与清单，覆盖 control push、gameplay ticket 和通用 envelope
- [x] 5.5 创建未知 message、错误 channel、无效 kind/id、未知 enum、截断/超长 frame 与非法身份字段 negative fixtures
- [x] 5.6 增加 Go golden tests，完成 registry lookup、decode、deterministic re-encode、SHA-256 与预期拒绝原因校验

## 6. S0 验收

- [x] 6.1 从无 generated code 的 clean checkout 连续执行两次 generate/verify，确认摘要一致、tracked fixtures 无漂移且 C# 临时目录不存在
- [x] 6.2 运行 Go unit/contract tests、race、Buf lint/breaking、OpenAPI validation、registry validation 和 `git diff --check`
- [x] 6.3 更新协议、目录、命令和客户端接入文档，记录全部 artifacts、owner、版本与后续 changes 的消费入口
- [x] 6.4 同步 `server-contracts` 主 spec，确认所有 tasks 完成并通过 OpenSpec strict
