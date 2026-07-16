# 协议治理与兼容规则

## 文档职责

本文档定义 iHomeland v1 协议的编号、字段、版本、路由、生成和演进规则。当前基线由 `shared/proto/`、`shared/contracts/` 与 `openspec/specs/server-contracts/` 共同约束，并在 `qualify-server-v1` 冻结为客户端唯一接入契约。

## 协议源

- Protobuf 源文件位于 `shared/proto/`。
- HTTPS OpenAPI 位于 `shared/contracts/http/v1/openapi.yaml`。
- Message、route registry 与错误目录位于 `shared/contracts/registry/`，属于协议源的一部分。
- Go 与 C# 代码只通过统一工具生成。
- 服务端 `generated` 与客户端 `Generated` 目录由 Git 忽略，不手工修改，也不承载 Unity 序列化引用。
- Contract fixtures 与 golden packets 必须版本化。
- 规范、编译器与插件版本统一来自根目录 `versions.yaml`，规则见 `docs/technology-versions.md`。

## 包划分

建议按职责拆分：

```text
shared/proto/
  ihomeland/common/v1/
    envelope.proto
    error.proto
    pagination.proto
  ihomeland/account/v1/
    account.proto
  ihomeland/session/v1/
    session.proto
  ihomeland/control/v1/
    control.proto
  ihomeland/world/v1/         PersonalWorld 公开投影与 snapshot
  ihomeland/visit/v1/         VisitSession 控制、snapshot 与 safe-return
  ihomeland/battle/v1/        battle 阶段创建
```

Domain model 不直接使用 generated protobuf type；application/transport adapter 负责转换。

## 版本层级

- API/package version：`v1`、`v2`，用于不兼容契约代际。
- Envelope protocol version：用于连接级能力协商和拒绝。
- Build/version：用于客户端构建兼容与 endpoint manifest。
- Message revision：由业务 payload 或 aggregate revision 表达，不滥用协议版本。

版本不用于绕过字段兼容。能以兼容字段演进解决的问题，不创建新 package version。

## Message ID 范围

当前已分配编号与 active owner range 的唯一事实是 `shared/contracts/registry/messages.json`。下表只规定长期预留策略；尚未进入 registry 的范围不代表对应 capability 已经存在：

| 范围 | Owner |
|---|---|
| `1-99` | common/system |
| `100-499` | session/auth |
| `500-999` | control |
| `1000-1999` | account/profile |
| `2000-2099` | world |
| `2100-2299` | visit |
| `2300+` | 由业务协议 change 按 owner 分配；未登记区间不得占位 |
| `20000+` | reserved by explicit OpenSpec |

规则：

- 编号全局唯一。
- 每个实时 message 与稳定 error 都必须有 owner。
- 已冻结编号不得复用，即使消息废弃。
- 废弃编号进入 reserved 清单。
- 不依赖奇偶数推断方向，方向由 route registry 明示。

World 首批登记 `2000-2003`，Visit 首批登记 `2100-2122`；区间内其余编号仍是未分配状态，不得把 owner range 当成可发送消息清单。未登记 world interaction 默认拒绝，不能进入通用 dispatcher。

## 字段兼容

允许：

- 新增保持显式 presence 的字段
- 新增 enum value 并为 unknown value 提供行为
- 新增 message
- 扩大不改变语义的长度上限（经过安全评审）

禁止：

- 修改已有 field number
- 复用已删除 field number 或 name
- 改变字段含义、单位、时区或编码
- 把 optional 改成实际必填但不提升版本
- 改变 enum 数值含义
- 将无符号/有符号、秒/毫秒等类型语义直接替换
- 无版本升级或 capability negotiation 时，收紧已发布 HTTP/JSON input 的 pattern、长度、枚举或组合约束

删除字段时必须使用 `reserved` 保留 number 和 name。

### 字段验证边界

- `.proto` 注释负责定义范围、单位与不变量，但注释本身不执行校验。
- Transport adapter 必须在调用 application service 前执行可测试的 payload 字段验证，拒绝空 identifier、越界长度、非法 enum、超范围数值和不完整组合。
- Application/domain 必须再次保护权限、容量、状态迁移和 revision 等业务不变量，不能假设 adapter 永远正确。
- 字段规则使用生成式 validator 还是显式 Go validator，由对应实现 change 决定；同一规则只能有一个权威实现，不得散落在 handler。

## Enum 规则

- `0` 必须表示 `UNSPECIFIED` 或 `UNKNOWN`。
- 接收方必须处理未知值。
- 状态机不得把未知值静默映射为合法业务状态。
- 新增值不能改变旧客户端对已有值的判断。

## Error Contract

错误响应至少包含：

- stable error code
- safe user-facing message 或 message key
- request id
- retryable
- optional retry-after
- bounded details

错误类别：

- protocol/version
- unauthenticated/permission
- validation
- conflict/state transition
- not found
- rate limited
- dependency unavailable
- internal

内部堆栈、SQL、Redis、token、密码和密钥不得进入客户端 error details。

`retryable` 只表示保持输入不变并稍后重试可能成功。ticket 过期或 revision 冲突要求先取得新 ticket/snapshot，因此不得标记为可直接重试。

World 使用 `2000-2006`，Visit 使用 `2100-2109` 作为当前稳定错误；权限、validation、rate limit、dependency 与 internal failure 继续复用 shared error。Public error 不得包含 credential、nonce、完整 assignment、runtime node/fence、session epoch、binding、fingerprint 或 backend detail。

## Route Registry

Route Registry 的字段与通道选择原则由 `docs/network-transport-architecture.md` 定义，当前机器事实由 `shared/contracts/registry/routes.json` 持有。协议治理要求 schema、route registry 和服务端 dispatcher 由测试确认一致，未登记或复用已冻结 message id 的消息不得进入正式 listener。实时 error 也必须拥有独立 message id、单一 channel 和 `ErrorPayload` 类型，不能复用原操作 message id 却更换 payload 类型。

`ratePolicy` 是稳定策略引用，不是完整限流实现。正式 listener 启用前，每个引用必须解析为机器可读配置，明确 identity/IP/connection/message 作用维度、rate、burst、依赖故障语义、拒绝错误和观测指标；缺失策略必须使启动或 readiness 失败，不能静默退化为不限流。

## Request、Command 与 Push

- HTTP 使用标准 request/response correlation 与 idempotency key（需要时）。
- 实时 request 使用不可预测或足够唯一的 request id。
- mutation command 使用 command id 处理重复提交。
- response 使用原 request id 或 command id，registry 统一登记为 `CORRELATION_ID`，且 envelope 中必须恰有一种关联标识。
- Push 不伪装成 request response，必须有独立 direction 和 handler。
- Snapshot 带单调 revision；客户端拒绝低 revision 覆盖高 revision。
- 战斗消息使用 tick/sequence 与 expiry，不复用 PersonalWorld、VisitSession 或 ActivityInstance revision。

## 时间与大小

- 字段名必须包含单位，例如 `_ms`、`_seconds`。
- 跨端绝对时间默认 Unix epoch milliseconds。
- duration 使用明确单位或 Protobuf Duration。
- 每条实时 route 限制完整 encoded envelope 大小，不能只限制内部 payload 后忽略 envelope 开销。
- TCP frame、HTTP body、WSS message 和 UDP datagram 分别配置上限。
- 每个 HTTP operation 使用 `x-ihomeland-body-limit-bytes`、`x-ihomeland-timeout-ms` 和 `x-ihomeland-idempotency` 声明 adapter 必须执行的资源与重试边界。
- world bootstrap 不带 body；invite accept 与 admission issuance 都要求 `Idempotency-Key`。相同 key、相同语义重放首次结果，相同 key 改变语义返回稳定 conflict。

WSS control 只发送 registry 登记的 9 类 `SERVER_TO_CLIENT/PUSH`，每条消息使用 deterministic payload 和 protocol version 1 `ReliableEnvelope`，且完整 envelope 同时满足 route `maxSize` 与全局 realtime frame 上限。每连接 sequence 从 1 单调递增，push 不携带 request/command correlation；unknown message、错误 generated payload 类型、TLS/TCP route 或客户端 application frame 必须 fail closed。

TLS/TCP gameplay 在 `ReliableEnvelope` stream 前使用版本化 `IHTP` authentication preface。preface 使用独立 4-byte big-endian frame、固定 ticket/admission 语法和封闭 purpose，不登记业务 message ID；格式基线由 `shared/contracts/fixtures/realtime/tcp-preface.json` 持有。后续 stream 只接受 registry 中 2000-2002、2103-2122 的精确 TLS_TCP route：C2S REQUEST/COMMAND 与 S2C RESPONSE/ERROR/PUSH 不能调换方向、kind、correlation 或 generated payload type。response/error 必须回显原 request/command identity，push 不携带 correlation。

## World/Visit 公开投影与 credential 分层

- Client-safe assignment 只公开 PersonalWorldID、WorldInstanceID、TLS/TCP endpoint、generation 与 lease expiry；RuntimeNodeID、FencingToken 和完整 AssignmentStamp 只保留在服务端 binding。
- Wire 绝对时间统一使用 Unix epoch milliseconds，并在字段名使用 `_at_ms` 或 `_expires_at_ms`；这不改变服务端持久事实的时间精度。
- HTTPS bearer 只证明 account/session lineage；`ConnectionTicket` 只允许连接一个 endpoint/channel；invite 与 `AdmissionIntent` 只表达领域资格；opaque world admission 才允许已认证 TLS/TCP connection 进入 current world target。
- Admission purpose 必须显式为 `OWN_WORLD`、`JOIN` 或 `RECONNECT`。Visitor 的 `JOIN` 只匹配 active reserved membership，`RECONNECT` 只匹配 active reconnecting membership；GAMEPLAY scope 本身不授予 Owner/Visitor role。
- Admission 只由 OpenAPI `WorldAdmissionResponse` 公开安全 ASCII opaque credential、endpoint、role、purpose 与 expiry；realtime schema 原样消费同一 string，不重复定义响应 DTO，也不公开可伪造 claims JSON。完整 binding、credential digest/consume identity 原子消费与 replay 防护规则由 `docs/network-transport-architecture.md` 的 World Admission 章节持有。

## 生成与验证

统一生成入口必须：

- 遵守 `docs/technology-versions.md` 的版本、下载校验与本机缓存规则
- 由 Buf 统一调度 Go local plugin 与受管 C# `protoc_builtin`，开发者和 CI 不直接执行 `protoc`
- S0 在编译前生成已忽略的 Go code，并在临时目录验证 C#；客户端协议 change 将 C# 生成到已忽略的 `Generated` 目录
- 校验格式与 breaking rules
- 直接对 Git 中的 Proto 源执行 breaking check，并使用生成代码注册的 descriptor 校验 route registry
- S0 运行 Go golden packet tests 并验证 C# generation；客户端协议 change 再增加 Go/C# parity tests
- 检查被忽略的生成结果是否可重复，并确保版本化 fixtures 没有未确认漂移

项目只使用 Buf 作为 schema 治理与 generation orchestration 的公开入口。`protoc` 是 Buf 管理下的 C# 内置 generator 后端，不构成第二套开发命令。

PowerShell 统一入口：

```powershell
& .\tools\proto\proto.ps1 format
& .\tools\proto\proto.ps1 generate
& .\tools\proto\proto.ps1 fixtures
& .\tools\proto\proto.ps1 verify
```

本地开发与 CI 必须调用相同的非交互脚本，不得在外部入口中复制协议生成或验证逻辑。

## 服务端 v1 资格冻结

`shared/contracts/fixtures/qualification/manifest.json` 版本化登记服务端 v1 的 mandatory 回归场景，并用 `execution` 区分 contract、public wire 与 layered evidence；`evidence-manifest.json` 是 layered scenario 到稳定 package/test identity 的唯一映射源。`endpoint-manifest.json` 提供公开 version/config 投影示例，`freeze.json` 只保存按排序 path+raw-content 计算的 aggregate SHA-256。Digest 是交付集合完整性证据，不替代 Buf breaking policy、OpenAPI/registry owner、fixtures 或 Git 历史。

完整资格入口必须先通过本节的生成与兼容门禁，再由不导入服务端业务实现的 Go client 验证 HTTPS/WSS/TLS-TCP。公开 operation、message/channel、credential、错误或恢复语义变化时，必须在同一 OpenSpec 中更新相关 source contract、fixtures、qualification capability group 与冻结摘要；纯内部重构不得为了迁就实现而修改外部预期。执行命令、当前 digest、报告和长期演进规则见 `docs/server-v1-qualification.md`。

CI 必须拒绝：

- 手工修改生成代码
- 未先生成就编译，或 schema 变化但 fixtures 未同步
- 未登记 message id
- field number/name 复用
- route 与 server registry 不一致

## 破坏性变更

新 v1 冻结后需要破坏性变更时：

1. 创建 OpenSpec change，说明原因与客户端影响。
2. 新建 package/API version 或 capability negotiation。
3. 定义服务端支持窗口与关闭条件。
4. 禁止同一业务 command 跨两个 transport 双写。
5. 提供 contract fixtures、自动化测试和观测指标。
6. 完成客户端与服务端切换后保留旧编号为 reserved。

基础契约未冻结的业务区间不得预留占位消息；进入 `qualify-server-v1` 后的 world/visit artifacts 也不得无记录地改变。
