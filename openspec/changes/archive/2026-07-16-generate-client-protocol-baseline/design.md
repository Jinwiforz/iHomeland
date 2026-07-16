## Context

`shared/proto/` 已是实时协议唯一 schema 源，`shared/contracts/registry/` 与 `shared/contracts/fixtures/realtime/golden.json` 已由服务端合同工具严格验证。现有 `tools/proto/proto.ps1` 只把 C# 生成到 `.tmp/csharp` 做模板检查并立即删除，因此 Unity 无法引用协议类型，也没有执行 Go/C# 字节互操作验收。

Unity 手写代码已经进入 asmdef 边界，而 `client/Assets/App/Generated/` 整体被 Git 忽略。若只把 `.cs` 放入该目录，它们会落入预定义程序集，手写 asmdef 无法稳定引用；生成代码还依赖官方 `Google.Protobuf` C# 运行时，不能假设任意电脑已经安装对应 DLL。客户端工程当前也不使用 NuGet 集成插件，因此需要由项目工具恢复这一编译输入。

本 change 的输入只包括冻结的 proto、registry、golden fixture、统一版本清单和当前 Unity 工程基线。协议 schema、网络通道、业务状态与服务端运行图均不在本次修改范围内。

## Goals / Non-Goals

**Goals:**

- 让干净克隆可通过统一协议入口恢复 Unity 所需的完整 C# 协议编译输入。
- 让生成协议拥有稳定、可被手写 asmdef 引用的程序集名称和明确 owner。
- 让官方 C# Protobuf 运行时的版本、来源、完整性和 Unity 兼容目标可验证。
- 用同一份 Go golden fixture 验证 C# descriptor、payload、JSON、envelope 与摘要语义。
- 保持所有可推导 C# 源码、程序集描述、依赖二进制与 Unity 自动生成的 `.meta` 不进入 Git。
- 让生成失败、依赖损坏、fixture 漂移和 tracked generated artifact 都能尽早失败并给出可定位错误。

**Non-Goals:**

- 不修改 `.proto`、message id、route、错误码、fixture 语义或兼容性政策。
- 不实现 HTTP、WSS、TLS/TCP、连接会话、重试、业务 Service、UI 或资源管理。
- 不引入 NuGetForUnity、第三方 DI、异步框架或自研运行时序列化协议。
- 不让 Scene、Prefab、ScriptableObject 等序列化资产引用生成类型。
- 不把本机 Unity Library、Temp、UserSettings、生成 `.meta` 或依赖缓存提交进仓库。

## Decisions

### 1. `proto.ps1` 继续作为唯一协议编排入口

`generate` 将同时重建 Go 与 Unity C# 协议产物；`verify` 在现有 schema、registry、fixture 和 Go 门禁之外，增加 C# 依赖、生成确定性与 parity 门禁。Buf 仍是 Protobuf 生成的公开编排器，项目锁定的 `protoc` 仍是 C# builtin 后端。

不增加第二个面向 Unity 的独立生成命令作为正常路径，避免 Go 与 C# 在不同入口、不同 schema 快照上生成。可以保留内部函数用于测试，但文档和 CI 只调用统一入口。

备选方案是由 Unity Editor 菜单执行生成；这会让协议恢复依赖 Editor 已经成功打开，而干净克隆恰好可能因缺少生成程序集而无法完成编译，因此不采用。

### 2. 先在 staging 目录完整生成，再替换忽略的 Unity 目录

C# 输出先写入仓库内受控的临时 staging 目录。工具依据生成文件声明的 Proto source 和源文件 `csharp_namespace` 验证一一对应关系，不写死当前 package 列表或文件命名算法，并准备程序集描述和运行时依赖；全部成功后才替换 `client/Assets/App/Generated/Protocol/`。替换 backup 留在 `.tmp` 且位于 staging cleanup 根之外，不短暂进入 Unity `Assets`；替换前后都校验目录位于仓库根且互不包含，失败不得删除其他本地内容。

目标布局保持单一 owner：

```text
client/Assets/App/Generated/Protocol/
├── IHomeland.Client.Protocol.Generated.asmdef
├── Sources/                  # protoc 生成的 C#
└── Runtime/
    └── Google.Protobuf.dll   # 由锁定 NuGet 包恢复
```

目录、`.cs`、asmdef、DLL 及 Unity 随后生成的 `.meta` 都是可推导产物并保持忽略。手写代码、测试和序列化资产不得放入该目录。

备选方案是在忽略目录内例外提交 asmdef；这会混合手写事实与工具产物、增加 `.gitignore` 和 `.meta` 例外，也不能解决依赖 DLL 恢复，因此不采用。另一个备选方案是让生成源码落入 `Assembly-CSharp`；现有手写 asmdef 无法引用预定义程序集，因此不采用。

### 3. 生成稳定协议程序集，不提前建立业务 adapter

工具生成固定名称 `IHomeland.Client.Protocol.Generated` 的 asmdef，root namespace 与各 `.proto` 的 `csharp_namespace` 保持现状。程序集只包含生成模型，不包含 transport、registry policy、重试或业务 façade。后续手写程序集按稳定名称引用它，而不是按磁盘路径复制类型。

生成程序集通过 Unity 默认的预编译插件引用能力消费同一生成根中的 `Google.Protobuf.dll`。verify 必须校验程序集描述、DLL assembly identity 与目标 framework，防止本机其他同名 DLL 被静默选中。

### 4. 从官方 NuGet 包恢复并验证 `Google.Protobuf`

`versions.yaml` 增加 C# Protobuf runtime 的精确版本、官方包地址、NuGet 包 SHA-256、预期 DLL 相对路径与 assembly identity。版本选择必须与项目 `protoc` 发布线兼容，并使用 Unity 当前支持的 .NET Standard 目标；具体版本只在统一版本清单中维护，change artifacts 不复制第二份版本事实。

工具先查找仓库本地忽略缓存；缓存缺失时从锁定官方地址下载，校验 SHA-256 后解包。缓存存在但摘要、包结构、DLL identity 或目标不符时必须失败或重新恢复，不能继续使用。项目不依赖系统 NuGet cache、全局 PATH 或 Unity Package Manager 的隐式解析结果。

当前 `netstandard2.0` 资产声明的 `System.Memory`、`System.Runtime.CompilerServices.Unsafe` 及其 BCL 能力由锁定的 Unity runtime 提供，不把这些程序集的 NuGet 副本放入 Generated。该判断必须由真实 EditMode 与目标平台 build 证明；升级 Unity 或 `Google.Protobuf` 时重新验收，不能沿用旧结论。

备选方案是提交 DLL；这会把可从锁定来源恢复的二进制纳入仓库，并引入二进制审查与 Unity `.meta` 管理成本，因此不采用。备选方案是安装 NuGetForUnity；当前只需要一个基础运行时，引入额外包管理扩展的收益不足以抵消供应链和工程复杂度。

### 5. parity 由 Unity EditMode 对全部 golden packet 动态执行

新增独立的客户端协议 EditMode 测试程序集，引用稳定的生成协议程序集。测试从仓库内读取 `golden.json` 和 message registry，并通过生成程序集公开的 `FileDescriptor` 动态建立 `protobuf full name -> MessageDescriptor` 索引，不手写逐消息 switch，也不生成第二份 tracked registry projection。

每个 packet 至少验证：

1. `payloadBase64` 能由声明的 C# descriptor 解析并重新编码为完全相同的字节；
2. C# JSON formatter 使用 proto field name 且不输出未设置默认值时，与 `payloadJson` 的紧凑语义一致；
3. 非零 message id 的 `ReliableEnvelope` 可解析，message id 与 payload 一致，重编码字节不变；
4. SHA-256 对 envelope 或独立 payload 的选择与 fixture 规则一致；
5. fixture 的 protobuf 名称与 message registry 一致，并满足服务端既有 golden 覆盖基线：全部 world/visit 消息、REQUEST/RESPONSE/COMMAND/PUSH 四种 kind、代表性 control push 与独立 ticket。

这组测试证明 C# 消费的是同一冻结契约，但不把网络收发职责塞入协议程序集。测试查找仓库根目录时复用当前客户端测试基线的路径约束，并在 fixture 缺失或从非项目布局运行时明确失败。

### 6. 自动门禁与 Unity 验收分层

PowerShell `verify` 负责无需启动 Unity 即可完成的门禁：依赖锁定、下载摘要、staging 生成、结构检查、重复生成摘要一致、忽略规则和 tracked generated 检查，以及现有 Go 合同/fixture 测试。Unity EditMode 负责真实 C# 编译和运行 parity；apply 完成验收同时执行 EditMode、现有 PlayMode 和 Windows Development build，避免仅靠文本检查宣称 Unity 可消费。

verify 不把 Unity Editor 进程、Unity Hub 登录或 Cloud Services 作为协议生成前置条件。Windows build 仍使用项目现有本地输出边界，不把构建结果纳入 Git。

## Risks / Trade-offs

- [首次生成需要访问官方 NuGet 源，网络或代理可能失败] → 摘要通过的本地忽略缓存可复用；错误必须区分下载失败与完整性失败，并允许先显式准备依赖后重跑。
- [生成目录被 Unity 打开后会产生大量 `.meta`] → 整个 Generated 根及 `Generated.meta` 保持忽略，verify 同时检查没有被强制跟踪；不由脚本伪造 GUID。
- [DLL 默认导入行为在 Unity 升级后发生变化] → EditMode 编译和 Windows Development build 是升级门禁；版本升级必须重新验证目标 framework 与 assembly identity。
- [C# 与 Go 的默认 JSON 或序列化细节存在差异] → parity 使用已有 canonical fixture 和明确 formatter 设置；差异必须通过兼容性评审解决，不允许在客户端宽松跳过字段或改写 fixture。
- [反射 descriptor 索引增加测试复杂度] → 反射只存在于 EditMode 验收，不进入运行时热路径；它避免维护逐消息副本，并能自动覆盖新增 proto 文件。
- [协议程序集缺失会使引用它的手写 asmdef 编译失败] → 这是 generation-before-compile 的显式约束；初始化和 CI 文档必须先执行统一生成入口，禁止回退到陈旧 tracked 代码。

## Migration Plan

1. 在统一版本清单中登记并验证 C# Protobuf runtime 锁定信息。
2. 把 C# Buf 输出从临时模板改为 staging，并扩展协议脚本完成依赖恢复、结构校验和受控替换。
3. 生成被忽略的协议程序集输入，确认连续两次生成内容摘要一致且没有 tracked generated artifact。
4. 增加客户端协议 EditMode 测试程序集，执行全部 golden parity。
5. 运行协议 verify、既有客户端 EditMode/PlayMode 和 Windows Development build，再执行 OpenSpec strict 验证。

回滚时移除本 change 新增的工具、版本锁定和测试，然后删除被忽略的 `client/Assets/App/Generated/Protocol/` 与对应本地依赖缓存。公开 proto、registry、fixture 和服务端产物没有迁移，因此无需数据或协议回滚。

## Open Questions

无。未来 HTTP OpenAPI C# 生成、网络 adapter 与运行时消息路由分别由后续独立 change 决定。
