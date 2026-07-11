## Context

项目已经定义 HTTPS、WSS、TLS/TCP 的职责与统一 session 边界，但尚无机器可读 schema、编号目录、路由 registry、framing 或 fixtures。后续 session、account、world/visit protocol 和 transport changes 如果分别定义基础机制，会造成编号冲突、身份语义漂移和跨端生成不一致。

本 change 位于路线图 S0，只建立契约源、生成工具和验证资产。它必须能由 Go 测试独立验收，同时遵守客户端进入门：不创建 Unity 工程、C# 生成物或运行时网络实现。

## Goals / Non-Goals

**Goals:**

- 建立基础 HTTPS 与实时消息的唯一机器可读契约源。
- 固定 message id、error code、route、envelope、framing 和 session/ticket 语义，具体业务 wire contract 等待领域规则稳定。
- 让 schema、registry、ignored generated code 和 fixtures 可以由单一非交互入口重复生成和验证。
- 在编译前生成 Go 协议代码，为后续 Go test client 和服务端 adapter 提供稳定输入，同时避免 generated diff 污染仓库。
- 冻结 C# 生成模板和跨语言兼容约束，但延后 C# 产物。

**Non-Goals:**

- 不实现 Gin handler、WSS/TCP listener、dispatcher 或连接生命周期。
- 不实现账号、session、个人世界或访客会话领域规则与 repository。
- 不实现 token 签名、密码哈希、ticket 存储或 TLS 证书管理。
- 不创建 Unity 工程、C# 生成代码、scene、prefab 或客户端 runtime。
- 不定义 battle、UDP/KCP、匹配、资产、奖励或结算契约。

## Decisions

### 1. 按传输语义划分契约源

目录固定为：

```text
shared/
  proto/ihomeland/
    common/v1/
    account/v1/
    session/v1/
    control/v1/
  contracts/
    http/v1/openapi.yaml
    registry/messages.json
    registry/errors.json
    registry/routes.json
    fixtures/http/
    fixtures/realtime/
```

- Protobuf 是 WSS/TLS-TCP 实时 payload 与可靠 envelope 的唯一 schema 源。
- OpenAPI 是 HTTPS JSON path、method、body、response 和 status 的唯一 schema 源，具体规范版本来自根目录 `versions.yaml`。
- JSON registries 保存跨 schema 元数据：稳定编号、owner、allowed channel、auth scope、大小、限流、幂等和错误映射。
- Registry 只引用 Protobuf full name 或 OpenAPI `operationId`，不复制字段结构。

不使用 Protobuf `service`/RPC 描述 HTTP 或实时路由，因为项目不采用 gRPC，虚构 RPC 会把 transport 语义错误地带入 schema。替代方案是让所有通道共享 Protobuf service，但这会同时重复 OpenAPI 与 route registry，因此拒绝。

### 2. Protobuf edition 与 presence 由版本目录和协议语义共同约束

- 每个项目自有 `.proto` 文件使用 `versions.yaml` 选定的 Protobuf edition，不在 change artifacts 中固化具体 edition 数字。
- package 使用 `ihomeland.<owner>.v1`，目录与 package version 一致。
- singular scalar 必须使用显式 presence；若所选 edition 未默认启用，则必须在 schema 中显式配置。除非独立兼容性决策证明必要，不得切换为 `IMPLICIT`，避免默认值掩盖字段缺失。
- enum 零值统一为 `<ENUM>_UNSPECIFIED`，接收方必须处理未知值。
- 已分配 field number 与 enum number 不复用；删除时同时 reserved number 与 name。
- 每个 message、enum 和 field 按 `docs/code-comment-convention.md` 注释单位、范围、方向和身份语义。
- 不使用 `google.protobuf.Any`；registry 已能从 message id 定位具体 payload，额外 type URL 只会增加大小和动态类型风险。

### 3. HTTPS 使用 OpenAPI JSON 契约

OpenAPI 至少定义：

- `GET /v1/version`
- `GET /v1/config`
- `POST /v1/auth/register`
- `POST /v1/auth/login`
- `POST /v1/auth/refresh`
- `POST /v1/auth/logout`
- `POST /v1/session/tickets`

请求与响应使用 UTF-8 JSON。每个 operation 必须有稳定 `operationId`、auth requirement、`x-ihomeland-body-limit-bytes`、`x-ihomeland-timeout-ms`、`x-ihomeland-idempotency`、成功响应和结构化错误响应。Endpoint manifest 作为 version/config 与 ticket 响应引用的共享 schema 定义，不另建第二份 JSON Schema。

OpenAPI 只定义 HTTP API 语义，不把 HTTP 或 TLS 协商版本写进业务 schema。OpenAPI、HTTP 与 TLS 的当前基线统一记录在 `versions.yaml`；后续 HTTPS adapter 是否保留旧协议回退，由部署兼容性和网络实测在对应 transport change 中决定，并同步更新版本目录与验证结果。

替代方案是用 Protobuf JSON 映射承载全部 HTTPS body。该方案会让浏览器、运维工具和 OpenAPI 消费者依赖 Protobuf presence/JSON 特例，且账号面没有复用实时 payload 的收益，因此拒绝。

### 4. Registry 是协议治理索引

`messages.json` 为每个实时 message 分配全局唯一 `message_id`、symbolic name、Protobuf full name 和 direction。`errors.json` 为每个稳定错误分配数值 code、symbolic name、owner、category、默认 retryable、HTTP status 和安全 message key。

`routes.json` 引用 message entry，并登记：

- owner
- allowed channel
- auth scope
- QoS
- 完整 encoded envelope max size
- rate limit policy key
- idempotency strategy
- timeout/expiry

验证器必须拒绝重复编号、重复名称、reserved 复用、未知引用、错误通道、超出 owner 编号段和 route/schema 不一致。删除消息或错误时必须在同一变更中把旧编号加入 `reserved` 并审查 Git diff；业务实现只能消费内存 projection，不维护第二份手写 switch 常量表。

### 5. 可靠 envelope 与 TLS/TCP framing 固定

可靠实时消息统一使用 `ReliableEnvelope`，字段覆盖：

- protocol version
- message id
- message kind：request、response、command、push、error
- 可选 request id
- 可选 command id
- connection sequence
- Unix epoch milliseconds timestamp
- encoded payload bytes

Request id 与 command id 使用固定 16-byte 值；registry 决定某消息需要哪一种。错误使用绑定到单一 channel 的独立 message id 与已登记的 common error payload，并通过 request/command id 关联原操作；不得复用原操作的 message id 却装入另一种 payload。内部异常或堆栈不得进入 envelope。

TLS/TCP 外层使用 4-byte unsigned big-endian length prefix，长度表示其后的 envelope 字节数，不包含 prefix。全局硬上限为 1 MiB，每条 route 的 `max_size` 必须更小或相等。Codec 必须处理半帧、粘包、连续帧、零长度、超长和截断输入。

WSS 的一个 binary message 对应一个 envelope，不再增加 TCP length prefix。未来 KCP 是否复用 envelope 由 battle network profile 决定，本 change 不预留 battle message。

### 6. 身份只来自 session 与 connection context

HTTPS account contract 可以接收凭据；实时 command 不携带可决定操作者身份的 player id。Connection ticket contract 必须绑定：

- session id 与 session epoch
- target channel/audience
- endpoint
- auth scopes
- one-time nonce
- issued/expiry time

WSS ticket 只授予 control scope，TLS/TCP ticket 只授予通用 gameplay scope。Gameplay scope 只允许建立可靠业务连接，不能替代 PersonalWorld、VisitSession、ActivityInstance 或其他领域的 admission 与授权。Registry validator 对 command schema 执行禁止身份字段检查，防止后续消息绕过该边界。

### 7. 业务协议在领域语义稳定后增量冻结

S0 只登记 control push、通用 envelope、session/ticket 和账号 HTTP 契约，不预建 PersonalWorld、VisitSession、ActivityInstance、Room 或 battle message。每个业务 protocol change 必须在对应 domain/application 核心通过独立测试后，基于真实 command、授权、revision 和失败语义分配编号并添加 routes、errors 与 fixtures。

这避免 wire shape 反向决定 repository transaction、锁、状态机或 membership 模型，也避免尚未存在的 Room/Activity 需求占用兼容边界。

### 8. 集中版本治理与分阶段生成

- 根目录 `versions.yaml` 是技术版本的唯一治理源，与记录产品发布号的 `release.json`、`server/version.json` 和 `client/version.json` 分离。
- `versions.yaml` 按 protocol specifications、toolchains、languages、infrastructure、client/runtime 分类记录精确版本、官方来源、核验日期和兼容性约束；后续 Go、MySQL、Redis、Unity、Docker image 与网络协议版本也进入同一目录。
- Change artifacts 只描述能力、约束和升级行为，不复制会随维护变化的具体版本号。
- `buf.yaml` 与 generation templates 使用 `versions.yaml` 选定的当前配置格式、`STANDARD` lint 和 `FILE` breaking category。
- `buf.lock` 锁定 schema dependencies。
- `tools/proto/buf.gen.go.yaml` 与 `tools/proto/buf.gen.csharp.yaml` 分离，并固定 generator/compiler version、来源与 options。
- S0 执行 Go generation，输出到已忽略的 `server/internal/generated/proto/`，编译和确定性校验后不提交产物。
- S0 建立最小 `server/go.mod` 以编译 generated Go 和运行 contract tests，不创建 `cmd/server` runtime。
- C# template 在 S0 的临时目录验证可解析且路径/namespace 稳定；客户端协议 change 输出到已忽略的 `client/Assets/App/Generated/`，且不得被 Unity 序列化资产引用。
- Buf CLI、Protobuf runtime/generators、OpenAPI validator 与插件从 `versions.yaml` 解析或由验证器核对，本机 SDK、编译器、生成器和验证依赖位于已忽略的 `.local/`，与进入 Git 的 `tools/` 自动化源码明确分离。Go generator 由项目 Go SDK 安装后作为 Buf local plugin 调度；Buf release asset、官方 `protoc` 与 OpenAPI validation schema 同时校验版本和 SHA-256。`protoc` 只作为 Buf `protoc_builtin: csharp` 的受管后端，并只输出临时资格验证结果。Buf 是 schema 治理与 generation orchestration 的唯一公开入口，开发者和 CI 不直接调用 `protoc`。`go.mod`、`.proto`、Unity `ProjectVersion.txt` 和容器镜像等生态要求的版本声明可以重复具体值，但必须与目录一致。

版本目录锁定的是可复现基线，不是永久禁止升级。升级时先核对最新正式版本及官方支持状态，再在同一变更中更新 `versions.yaml`、受影响的生态配置、生成配置、fixtures 和兼容性测试；目标规范与工具链无法组合工作时必须阻断升级，不得静默降级或留下半更新状态。

`tools/proto/proto.ps1` 提供本地终端与 CI 可调用的非交互命令，至少包含 format、lint、generate、fixtures、verify。所有命令使用同一实现，失败时返回非零退出码。

### 9. Fixtures 同时验证编码与治理

- HTTP fixtures 包含代表性的 request、success、稳定 error 和边界输入。
- Realtime golden packets 使用 deterministic Protobuf marshal，记录 message id、类型、二进制、可读 JSON 与 SHA-256。
- Golden payload 避免依赖 map 序列化顺序；确需 map 时验证语义而非原始字节顺序。
- Negative fixture manifest 固定未知 message id、错误 channel、无效 kind/id 组合、超长 frame、截断 frame、未知 enum 和身份字段等拒绝原因；对应 contract/codec tests 必须执行真实拒绝行为。
- Fixtures 不包含真实密码、token、ticket、密钥或可用 endpoint 凭据。

Clean checkout 必须在 generated code 不存在时先完成 generation；连续生成两次的摘要必须一致，fixtures 命令只在显式更新兼容性基线时写入 tracked files。Go tests 必须对全部 golden packet 完成 decode、re-encode 和 registry lookup。

## Risks / Trade-offs

- [领域实现前冻结契约可能遗漏规则] -> S0 只冻结协议治理与基础连接语义，业务契约由对应领域 change 增量定义。
- [OpenAPI、Protobuf 与 registry 出现漂移] -> 用 full name/operationId 引用并由单一 validator 交叉检查，禁止复制字段定义。
- [生成工具版本漂移] -> 锁定 Buf、plugin、`protoc`、OpenAPI validator 和 Go dependency 版本，对可下载 compiler 校验官方 SHA-256，并要求 clean generation 摘要一致且 tracked baselines 无漂移。
- [忽略 generated code 后无法直接离线编译] -> 统一入口强制先生成后编译，CI 从无 generated code 的检出状态验收；首次 bootstrap 准备锁定工具后可离线重复生成，不以提交生成代码绕过。
- [C# 产物延后导致跨语言问题发现较晚] -> S0 冻结 C# template 与 namespace，fixtures 使用语言中立数据；客户端 change 首项即运行 C# generation 与 parity tests。
- [Golden packet 过多导致维护成本] -> S0 只为 control push、gameplay ticket 和通用 envelope 保留代表性 golden，业务 golden 随所属 protocol change 添加。
- [S0 提前创建 Go module] -> module 只承载 generated code 与 contract tests，S1 直接扩展同一 module，避免额外 module 边界。

## Migration Plan

1. 更新路线图与协议文档中的 change 名称和生成阶段。
2. 建立 schema、OpenAPI 与 registries，先通过静态验证。
3. 固定工具版本，建立被忽略的 Go/C# generation，并使用生成代码的 descriptor registry 与内存 route projection 完成验证。
4. 生成 fixtures/golden packets，完成 codec、framing 和治理测试。
5. 建立 S0 artifacts，供 runtime、session、account 和后续业务协议 changes 增量消费。

当前没有生产消费者，失败时可整体回退本 change。后续 change 开始消费后，任何不兼容修改必须创建独立 OpenSpec change，并通过 version/compatibility 规则迁移，不能改写已冻结编号。

## Open Questions

无。具体 message id 与 error code 按已定义 owner 范围在实现任务中分配，并由 registry validator 验收。
