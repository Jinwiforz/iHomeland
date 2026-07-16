## Context

`establish-client-runtime` 已建立 `AppBootstrap -> AppComposition -> AppRoot`、AppLifetime、主线程投递和 Scene Scope 边界；`generate-client-protocol-baseline` 已冻结可重复恢复的 C# Protobuf 与跨端 golden parity。客户端目前仍没有 HTTP 配置、账号会话或 credential owner，后续 WSS/TLS-TCP 因而没有合法 ticket 来源。

服务端 v1 已冻结 10 个 OpenAPI operation。本 change 只消费路线图 C1 首段要求的 8 个 operation：version、config、register、login、refresh、logout、connection ticket 与 own-world bootstrap。Invite accept 和 world admission 需要 VisitSession/WorldAdmission 业务编排与幂等 identity，由后续个人世界 Services change 增加。本 change 不修改 OpenAPI、服务端 error registry 或服务端业务语义。

Unity 客户端仍以 PC 为首个目标。配置可以使用 Unity 资产，但在线账号和 credential 不能进入 Scene、Prefab、ScriptableObject 或 PlayerPrefs。实现不得依赖 Unity Services，也不为网络能力引入第三方 DI、async 或 HTTP 框架。

## Goals / Non-Goals

**Goals:**

- 建立可由纯 C# 上层调用、可注入测试 transport 的强类型 HTTP 边界。
- 以唯一 Configuration owner 与 Session owner 管理配置、token lineage、ticket 和并发提交。
- 精确区分 server error、timeout、caller cancellation、transport failure 与 malformed response。
- 保证请求、响应、日志、异常、关闭和迟到 callback 都遵守有界与凭据安全规则。
- 接入现有 Composition/AppLifetime，同时保持无显式调用时不访问网络。

**Non-Goals:**

- 不实现 invite accept、world admission、WSS、TLS/TCP、重连或 realtime dispatcher。
- 不实现 PersonalWorld/VisitSession 最终状态 owner、Scene 流程、登录 UI 或任何业务页面。
- 不持久化 refresh token；Windows secure storage 与未来移动平台 keystore 需要独立 capability。
- 不自动重试、离线排队、后台刷新或从服务端响应改写 HTTP base URI。
- 不生成或提交 OpenAPI generated C# code，不引入 Addressables/Resources 或 Unity Cloud 服务。

## Decisions

### 1. 使用分层而非万能 HTTP client

对象关系固定为：

```text
ClientEnvironmentProfile (Unity 非敏感配置)
  -> ClientEnvironment (不可变纯 C# 投影)
      -> ClientHttpTransport (HttpClient、deadline、bounded body)
ClientHttpTransport + ClientHttpCodec + ClientHttpOperationCatalog
  -> ClientHttpApi
ClientHttpApi + ClientConfigurationStore
  -> ClientBootstrapService
  -> SessionCoordinator
```

`ClientHttpTransport` 只接受冻结的 operation descriptor，不暴露任意 method/path/body。`ClientBootstrapService` 负责 version/config 和兼容性；`SessionCoordinator` 负责 register/login/refresh/logout/ticket 的 lineage。Own-world bootstrap 作为强类型查询返回给调用方，但本 change 不保存 PersonalWorld 投影。

备选的单个 `ApiManager.Send(path, body)` 会让认证、deadline、响应类型和敏感字段依赖调用点约定，也允许后续 feature 绕过 operation registry，因此拒绝。为每个 operation 建立独立 transport 类又会重复连接池、取消和错误映射，同样拒绝。

### 2. 使用 BCL `HttpClient` 与 `System.Text.Json`

Unity 6.5 的目标 profile 已提供 `System.Net.Http` 与 `System.Text.Json`。运行时复用一个注入的 `HttpClient`/handler，使用 `ResponseHeadersRead` 与每个 operation 的显式流式上限，避免默认无界缓冲；codec 使用 `Utf8JsonWriter` 只写 OpenAPI 请求字段，并通过 `JsonDocument` 在提交状态前构造和验证不可变响应投影。

选择 BCL 的原因是 cancellation、deadline、header 和 handler 测试边界无需 MonoBehaviour 或真实 listener。`UnityWebRequest` 适合引擎资源和平台特殊传输，但会把本 change 的纯 C# contract tests 与 Unity callback 绑定；当前 PC JSON API 没有这项收益。新增 JSON package 或完整 OpenAPI generator 会扩大供应链和生成门禁，也不在本 change 引入。

显式读写避免 reflection-based serializer DTO 在未来 IL2CPP/AOT 下产生未登记 metadata 依赖。客户端容忍未知 response property 以支持向后兼容的 additive field，但严格验证当前版本的 required field、enum、范围和集合上限。解析失败只形成安全错误，不保留或输出原始 body。`PublicLimits.httpBodyBytes` 仍表示服务端请求预算，不能误用为响应上限。

### 3. 环境配置使用 ScriptableObject，运行事实复制到纯 C#

`ClientEnvironmentProfile` 只保存 environment kind 与 HTTP base URI，并由 BootstrapScene 直接序列化引用。`AppBootstrap` 在构造对象图前把它验证并复制成不可变 `ClientEnvironment`；客户端版本继续取既有 PlayerSettings/Application identity，协议版本继续取冻结 contract baseline，避免在 profile 复制第二份版本事实。后续 Service 不持有或修改 ScriptableObject。Production 只允许 HTTPS，local/test 明文同时要求显式环境和 loopback。

这符合项目中“ScriptableObject 保存配置、Runtime Service 保存在线事实”的边界。把 endpoint 硬编码到 C#、从 Host header/响应 payload 猜测、使用 Resources 全局加载或把 token 写入资产都被拒绝。未来 build pipeline 可以选择不同 profile，但 profile 仍不能保存 secret。

### 4. Operation catalog 是受契约测试约束的手写运行投影

每个纳入 operation 使用一个不可变 descriptor 固定 method、相对 path、认证要求、成功 status、deadline、请求 body policy 和客户端 response hard cap。Catalog 只包含本 change 的 8 个 operation。不可变 contract projection 与 descriptor 是手写运行投影，原因是当前 OpenAPI 类型数量有限，且不值得引入第二条 generated code 工具链。

漂移由两层测试阻止：

- contract tests 使用 `shared/contracts/fixtures/http/cases.json` 中属于本阶段的 cases 验证真实 request/response codec；
- catalog tests 冻结本阶段从 OpenAPI 取得的 method、path、认证、status、deadline 与 body policy，并对照 error registry 验证已知错误映射；既有 contract verify 继续验证 OpenAPI 与服务端 registry/fixtures 本身。

后续 accept/admission change 必须显式扩展 catalog、fixture coverage 和 spec，不能通过通用 escape hatch 抢跑。若 operation 数量或 schema 复杂度显著增长，再用独立 change 评估 OpenAPI C# generation。

### 5. Session 以 generation 防止异步旧结果覆盖

`SessionCoordinator` 保存不可变 session snapshot 和递增 generation。Register/login 在完整验证后一次替换；refresh 捕获当前 generation 并保证 single-flight，只有 lineage 未变化时才提交新 token pair。首个 refresh 调用拥有底层请求，后续调用共享该 flight，但可以独立取消自己的等待。Refresh 响应丢失可能表示服务端已经轮换 token，logout 响应丢失也可能表示远端已经提交；两类 commit-unknown 都进入 `Unresolved` 并撤销旧 snapshot。Logout 成功或权威未认证清除 snapshot；`Unresolved` 禁止继续签发 ticket 或发送 authenticated operation，直到新的 register/login 或显式 forget 解决。

Ticket 记录来源 generation 与绝对 expiry，只能显式交付给对应 channel；过期或 generation 变化后拒绝使用。本 change 不建立 channel，因此不声称 ticket 已消费。Password 不进入 snapshot，token/ticket 类型不提供包含值的 `ToString()`，日志与异常只能携带 operationId、failure kind、HTTP status class 和 requestId。

使用 generation 而不是取消本身作为最终 guard，因为底层取消与远端提交存在竞态，迟到 completion 仍必须在状态 owner 处被拒绝。把 token 放进全局静态字段、多个 service 各存一份或用 event bus 广播 mutable session 均被拒绝。

### 6. 不进行隐式重试，错误使用封闭结果模型

每次请求建立 caller token、operation deadline 与 AppLifetime stop token 的 linked cancellation。取消原因按先发生的 owner 归类。Transport 不自动重试，即便 GET 是 SAFE；上层未来可以在明确预算内重试 SAFE operation，但每次 attempt 都必须可观察。Register/login/refresh/ticket 在 response 丢失时不猜测未提交。

结果模型区分：

- 成功的强类型 value；
- 已验证的 server error（known 或 unknown code）；
- caller cancelled；
- operation timeout；
- DNS/connect/TLS/HTTP transport failure；
- malformed/oversized response；
- local configuration 或 compatibility failure。

Server error 不通过 exception 表示，程序员错误和生命周期非法调用仍可抛出。`retryable` 与 retry-after 只作为事实返回，不触发 sleep。Malformed body、原始 response、credential 和内部 exception message 不向 UI 传播。

### 7. HTTP 资源加入 AppLifetime，但网络 bootstrap 保持显式

Composition 按“本地配置与 codec -> transport -> session/bootstrap services”的顺序构造和登记；逆序停止先阻止新业务调用、取消 in-flight，再释放 transport。初始化不自动访问服务端，使现有空 BootstrapScene、PlayMode 和 Windows build 在没有服务端时仍能启动。未来 UI/application flow 在 AppRoot Running 后显式调用 bootstrap。

测试通过注入 handler、clock 和 build identity 覆盖 contract fixtures、超限流、deadline、caller cancellation、refresh race、logout unknown、ticket expiry、停止竞态和脱敏。真实 Unity 验收继续运行全部 EditMode、PlayMode、协议 parity 与 Windows Development build；本 change 不依赖 Unity Services 或真实 WSS/TCP。

## Risks / Trade-offs

- [手写 DTO/catalog 可能与 OpenAPI 漂移] → 以 fixture parity、catalog metadata tests、既有 contract verify 和无通用发送入口共同阻断；规模增长后再评估生成。
- [System.Text.Json/HttpClient 在未来移动 IL2CPP 平台存在差异] → 当前只承诺 Windows PC；保持 transport/codec 接口与 platform-neutral tests，移动目标启用时单独执行 AOT/linker/TLS 验收。
- [Refresh/logout timeout 后 token lineage 不确定] → 使用 `Unresolved` fail closed，不继续使用可能已轮换或撤销的 credential，也不谎称服务端一定完成。
- [内存 token 不能主动清零托管字符串] → 最小化复制和生命周期，禁止持久化与输出；需要更强 secret container 或 OS secure storage 时独立设计。
- [环境 profile 可能被错误打进 Production build] → 启动时再次执行 scheme/loopback/environment 交叉验证，构建验收检查 Production 不引用 local profile。

## Migration Plan

1. 增加环境定义、HTTP contract/transport、结果模型和纯 C# tests，不改变现有 Scene 行为。
2. 将 profile 直接引用接入 BootstrapScene，并扩展 Composition/AppLifetime；保存 Unity 生成的必要 `.meta` 与场景引用。
3. 运行 HTTP contract/metadata verify、全部客户端 EditMode/PlayMode、协议 parity 和 Windows Development build。
4. 回滚时移除本 change 的 HTTP participant、profile 引用和新增代码即可恢复 C0；服务端契约与已归档协议基线不受影响。

## Open Questions

无。Refresh token 的持久化、invite/admission operation 和自动 SAFE retry 均已明确留给后续独立 change，而不是本 change 的未决实现选择。
