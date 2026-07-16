## 1. 锁定 C# Protobuf 运行时

- [x] 1.1 在 `versions.yaml` 登记与项目 `protoc` 发布线兼容的官方 `Google.Protobuf` 版本、包地址、NuGet 包 SHA-256、Unity 兼容 DLL 路径与 assembly identity，并扩展仓库合同校验覆盖必填字段、格式和版本一致性。
- [x] 1.2 在协议 PowerShell 工具中实现受仓库边界保护的下载、缓存、SHA-256 校验、NuGet 解包和 DLL identity/目标校验，覆盖缓存命中、缓存损坏、下载失败和包结构漂移测试。

## 2. 建立 Unity C# 协议生成链

- [x] 2.1 把 C# Buf template 从一次性临时检查改为受控 staging 输出，保留项目锁定 `protoc` 后端，并校验 proto 文件与生成 C# 文件、namespace 和相对路径的完整对应关系。
- [x] 2.2 在 staging 中生成固定名称 `IHomeland.Client.Protocol.Generated` 的 asmdef，恢复 `Google.Protobuf.dll`，全部校验成功后再受控替换 `client/Assets/App/Generated/Protocol/`，失败时保留最后一次完整输出。
- [x] 2.3 扩展 `generate` 与 `verify`，验证两次 C# 生成的路径集合和逐文件摘要一致、没有陈旧文件、Generated 根保持忽略且没有生成源码、asmdef、DLL 或 `.meta` 被 Git 跟踪。
- [x] 2.4 为新增 PowerShell 函数和生成边界补充符合项目约定的中文契约注释、失败上下文和路径安全测试，不引入静默失败、全局 NuGet 状态或重复协议入口。

## 3. 建立 Go/C# golden parity

- [x] 3.1 新增独立客户端协议 EditMode 测试程序集，按稳定程序集名称引用生成协议，并实现仓库根、golden manifest 与 message registry 的严格只读加载。
- [x] 3.2 从生成程序集的 `FileDescriptor` 动态建立消息 descriptor 索引，对全部 golden packet 验证 payload 解析与字节重编码、proto-name JSON、`ReliableEnvelope`、SHA-256 和 registry 类型映射。
- [x] 3.3 增加与服务端相同的 world/visit 消息和 REQUEST/RESPONSE/COMMAND/PUSH 覆盖门禁，以及未知 descriptor、重复 descriptor、fixture/registry 漂移和损坏 Base64 的失败测试；禁止逐消息手写 switch。
- [x] 3.4 复核所有新增手写 C# 类型与成员的 `///` XML documentation，确保注释解释 fixture 所有权、descriptor 发现、确定性边界和失败语义，而不逐行复述实现。

## 4. 文档与完整验收

- [x] 4.1 更新协议兼容、客户端接入、文件结构和技术版本文档，明确 generation-before-compile、Generated 目录 owner、依赖恢复、asmdef 引用、Unity `.meta` 忽略和故障排查入口，删除与既有文档重复的说明。
- [x] 4.2 执行协议 `generate`/`verify`、Go 合同与 fixture 测试、生成确定性检查、tracked generated 检查及 `git diff --check`。
- [x] 4.3 在 Unity 运行全部客户端 EditMode（含 C# golden parity）与既有 PlayMode 测试，确认 Console 无编译错误且协议测试不依赖 Unity Services。
- [x] 4.4 执行 Windows Development build，确认生成协议程序集与 `Google.Protobuf` 正确进入构建且空白启动仍正常退出；最后执行本 change 和全仓 OpenSpec strict 验证。
