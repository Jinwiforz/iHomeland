## Why

服务端 v1 已完成独立资格验收，Unity 运行基础也已建立，但客户端仍没有可在干净克隆后重建、可由 Unity 稳定编译并与 Go golden packet 互操作的 C# 协议产物。必须先补齐这条协议供应链，后续网络通道和业务接入才能复用同一份冻结契约，而不是手写或复制协议模型。

## What Changes

- 把现有临时 C# Protobuf 生成升级为面向 Unity 的持久生成入口，先在受控 staging 中完整重建并校验，再整体替换被 Git 忽略的 `client/Assets/App/Generated/Protocol/`。
- 为生成协议建立稳定的 Unity 程序集边界，使现有和后续手写 asmdef 可以按程序集名称引用协议类型，同时禁止 Scene、Prefab 等序列化资产依赖生成脚本。
- 锁定、校验并自动准备与项目 `protoc` 发布线兼容的官方 `Google.Protobuf` C# 运行时；依赖及生成产物均可从受版本治理的输入恢复，不依赖本机预装状态。
- 建立 C# 消息描述符索引与 Go/C# golden parity 验收，覆盖 fixture 中全部 Protobuf 类型的解析、确定性重编码、JSON 语义和实时 envelope 字节一致性。
- 扩展协议统一入口的 verify 流程，验证 C# 生成确定性、干净重建、依赖完整性、无 tracked generated artifacts，以及 Unity EditMode 编译/测试入口所需的协议基线。
- 本 change 不实现 HTTP/WSS/TCP adapter、连接管理、业务 Service、UI 页面、资源管理或协议语义变更。

## Capabilities

### New Capabilities

- `client-protocol`: 定义 Unity 客户端协议生成、依赖恢复、程序集边界、Go/C# golden parity 与生成物治理要求。

### Modified Capabilities

无。

## Impact

- 影响 `tools/proto/`、`versions.yaml`、`shared/contracts/fixtures/` 的消费方式、`client/Assets/App/Generated/` 的本地产物布局，以及客户端协议 EditMode 测试程序集。
- `shared/proto/`、registry 与已冻结 fixtures 继续作为只读协议输入；公开 schema、message id、route、安全语义和服务端 v1 资格结论不变。
- 新增官方 `Google.Protobuf` C# 运行时依赖，其版本、来源与完整性进入统一技术版本治理；不引入第三方 DI、网络或异步框架。
- 生成 C#、生成程序集描述与依赖二进制保持 Git 忽略且可重建；仓库只提交生成器、配置、测试、fixture 与依赖锁定信息。
