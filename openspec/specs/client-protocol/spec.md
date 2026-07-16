# Client Protocol 规格

## Purpose

定义 Unity 客户端协议的唯一生成输入、可重复产物、程序集边界、C# Protobuf 运行时治理、Go/C# golden parity 与独立验收要求。

## Requirements

### Requirement: 客户端协议由唯一冻结输入生成

系统 SHALL 仅以 `shared/proto/`、受治理 registry、版本清单和合同 fixture 作为 Unity C# 协议基线输入，并 SHALL 通过与 Go 生成相同的统一协议入口调用 Buf 和项目锁定的 `protoc`。客户端不得手写、复制或修改生成消息类型来形成第二份协议事实。

#### Scenario: 干净克隆生成客户端协议

- **WHEN** 在没有客户端生成目录和本机协议依赖缓存的干净克隆中执行统一协议生成入口
- **THEN** 系统从锁定输入恢复依赖并生成与当前 proto 快照一致的 Go 与 Unity C# 协议产物

#### Scenario: schema 输入无效

- **WHEN** proto、registry 或版本锁定不能通过现有严格合同校验
- **THEN** 客户端协议生成失败且不得发布部分生成结果到 Unity 目标目录

### Requirement: Unity 协议产物可重复且保持 Git 忽略

系统 SHALL 在受控 staging 中完成 C# 生成和校验后整体替换 `client/Assets/App/Generated/Protocol/`，并 SHALL 保证连续生成的受管文件内容一致。生成 C#、生成 asmdef 与依赖二进制 SHALL 保持 Git 忽略且可由统一入口恢复；Unity 导入这些本地产物后创建的 `.meta` SHALL 同样保持 Git 忽略，并由 Editor 按需重新生成。

#### Scenario: 连续生成保持确定性

- **WHEN** 使用相同 proto、版本清单和工具链连续执行两次客户端协议生成
- **THEN** 两次受管输出的相对路径集合和逐文件摘要完全一致，且第二次不残留首轮之外的文件

#### Scenario: staging 生成中途失败

- **WHEN** C# 生成、依赖校验或输出结构校验在 staging 阶段失败
- **THEN** 命令返回失败并且不会用不完整结果替换最后一次完整的 Unity 协议目录

#### Scenario: 生成物被 Git 跟踪

- **WHEN** verify 发现客户端 Generated 根、其 `.meta`、生成源码、生成 asmdef 或依赖二进制被 Git 跟踪
- **THEN** verify 失败并报告具体 tracked path

### Requirement: 生成协议具有稳定程序集边界

系统 SHALL 为生成模型建立固定名称 `IHomeland.Client.Protocol.Generated` 的 Unity asmdef；该程序集 SHALL 只拥有 Protobuf 生成类型及其编译依赖，不得拥有 transport、连接、业务 Service、UI 或场景生命周期。手写 asmdef SHALL 能按稳定程序集名称引用它，Unity 序列化资产 SHALL NOT 引用其生成脚本。

#### Scenario: 手写测试程序集引用协议

- **WHEN** Unity 在完成协议生成后编译引用 `IHomeland.Client.Protocol.Generated` 的 EditMode 测试程序集
- **THEN** 编译成功且测试能访问生成消息 descriptor 和 parser

#### Scenario: 生成程序集包含越界代码

- **WHEN** verify 发现协议生成目录中存在非工具产出的手写源码、transport adapter、业务 Service 或 UI 类型
- **THEN** verify 失败并指出协议程序集所有权越界

### Requirement: C# Protobuf 运行时受版本和完整性治理

系统 SHALL 在统一版本清单中锁定官方 `Google.Protobuf` C# 运行时的版本、来源、包摘要、Unity 兼容目标和 assembly identity。生成入口 SHALL 只消费摘要与结构均匹配的包，并 SHALL NOT 依赖系统 NuGet cache、全局安装或未声明 DLL。

#### Scenario: 首次恢复运行时依赖

- **WHEN** 本地缓存不存在且锁定的官方包可访问
- **THEN** 工具下载包、校验 SHA-256 和预期 DLL identity，并把 Unity 兼容 DLL 放入被忽略的生成目录

#### Scenario: 缓存包被篡改或目标不符

- **WHEN** 缓存包摘要、目标 framework、相对路径或 DLL identity 与版本清单不一致
- **THEN** 生成入口拒绝使用该依赖并返回可定位的完整性错误

### Requirement: C# 必须通过 Go golden parity

系统 SHALL 由 Unity EditMode 测试消费 `shared/contracts/fixtures/realtime/golden.json` 的全部 packet，并 SHALL 使用生成程序集 descriptor 动态解析 fixture 声明的 Protobuf 类型。测试 SHALL 验证 payload 字节、规范 JSON、可靠 envelope、SHA-256、registry 类型映射，以及与服务端相同的 world/visit 消息和 message kind 覆盖基线，不得维护逐消息手写解析分支。

#### Scenario: 全部 golden packet 兼容

- **WHEN** Unity EditMode 对当前 golden manifest 执行客户端协议 parity
- **THEN** 每个 payload 均可解析并无字节变化地重编码，JSON 语义、envelope 路由身份与摘要均与 Go fixture 一致

#### Scenario: fixture 引用未知 C# descriptor

- **WHEN** golden packet 的 `protobuf` 全名无法从生成程序集 descriptor 索引解析
- **THEN** 对应 parity 测试失败并报告 packet 名称和缺失的 Protobuf 全名

#### Scenario: registry 或 fixture 覆盖漂移

- **WHEN** 实时 message registry 与 golden manifest 的类型映射、world/visit 消息覆盖或 message kind 覆盖不一致
- **THEN** parity 测试失败且不得通过跳过未知消息继续验收

### Requirement: 客户端协议基线独立于网络和 Unity 云服务

系统 SHALL 允许协议生成和非 Unity verify 在不启动 Unity Editor、不登录 Unity Services 且不建立 HTTP/WSS/TCP 连接的情况下完成。真实 C# 编译与 parity SHALL 由 Unity EditMode 验收，客户端整体回归 SHALL 继续包含既有 PlayMode 与 Windows Development build。

#### Scenario: 离线 Unity Services 状态下生成

- **WHEN** Unity Services 未关联或未登录但锁定工具与协议依赖可用
- **THEN** 协议生成和非 Unity verify 不受 Unity Cloud 状态影响

#### Scenario: 完整客户端验收

- **WHEN** 客户端协议基线发生实现或依赖变化
- **THEN** 协议 verify、客户端 EditMode golden parity、既有 PlayMode、Windows Development build 与 OpenSpec strict 验证全部通过
