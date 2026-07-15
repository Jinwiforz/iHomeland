## 1. 依赖、配置与 secret 准备

- [x] 1.1 按 `versions.yaml` latest-compatible 规则选择并锁定 Gin，更新 `versions.yaml`、`server/go.mod`、`server/go.sum` 与依赖验证测试，确认只由 HTTP transport 导入。
- [x] 1.2 为 public API bind/TLS、advertised endpoints、公开 limits、operation rate policy、Session/VisitSession、placement replay与WorldAdmission policy 增加类型化配置、白名单环境覆盖和安全本地默认值。
- [x] 1.3 增加配置交叉校验测试，覆盖 production TLS 1.3、loopback plaintext 例外、地址冲突、非零端口、完整 operation policy、TTL 顺序、endpoint channel 和未知字段拒绝。
- [x] 1.4 在产生 listener/client 副作用前解析 public TLS private key与world admission derivation key，构造TLS policy并验证secret销毁、错误脱敏和失败不产生网络副作用。
- [x] 1.5 更新可提交配置示例与测试配置构造器，使新增必填项使用无密钥reference且不破坏既有storage/diagnostic启动测试。

## 2. Session 与 VisitSession 应用桥接

- [x] 2.1 扩展SessionStore access解析结果和production Redis Lua/codec，使认证原子返回有效access/session绝对deadline并拒绝缺失、过期或矛盾记录。
- [x] 2.2 增加默认脱敏的 `AuthenticatedSession` 结果，让Session service在HTTPS认证时返回AuthContext与session deadline，同时保持refresh/logout/ticket既有语义和测试覆盖。
- [x] 2.3 实现受验证配置驱动的EndpointProvider及只适用于“无realtime component”构成的NoActiveRealtimeConnections invalidator，并增加防止两类invalidator并存的结构/组合测试。
- [x] 2.4 为VisitSession service增加HTTP accept用例桥接，由领域owner按session、invite、aggregate、assignment lease和policy最早deadline计算reservation并保持既有原子replay结果。
- [x] 2.5 为VisitSession service增加只读admission资格解析，严格区分当前actor的reserved/JOIN与reconnecting/RECONNECT，返回受信intent/deadline且不创建credential或membership。
- [x] 2.6 补充Session与VisitSession单元、并发和production storage integration测试，覆盖deadline等号、response-loss、corruption、stale epoch/assignment及敏感值格式化。

## 3. World Entry 应用编排

- [x] 3.1 创建transport-independent `internal/worldentry` 的窄ports、commands/results与脱敏值类型，限制为bootstrap、invite accept和admission issue三个用例。
- [x] 3.2 实现HTTP Idempotency-Key的actor/session/operation分域SHA-256派生，分别生成合法VisitSession CommandID和WorldAdmission IssueID且不暴露原始key。
- [x] 3.3 实现own-world bootstrap：幂等确保primary PersonalWorld、读取current active placement并只构造client-safe projection；没有assignment时返回world-only结果。
- [x] 3.4 实现invite accept编排，将受信AuthenticatedSession、path identity、expected revision和派生command identity交给VisitSession owner并验证返回reservation投影。
- [x] 3.5 实现own/visit admission编排，从认证lineage、权威world/membership、完整current assignment、TLS_TCP endpoint与最早deadline构造既有binding并调用WorldAdmission issuer。
- [x] 3.6 增加worldentry table-driven与并发测试，覆盖首次/重复bootstrap、world-not-ready、JOIN/RECONNECT、missing membership、stale assignment、幂等重放/冲突、commit-unknown和dependency defect。

## 4. HTTP operation、codec 与错误边界

- [x] 4.1 创建 `internal/transport/httpapi` 集中operation table，登记10个OpenAPI operation的method/path/auth/body limit/timeout/idempotency并拒绝未登记route。
- [x] 4.2 实现versioned request decoder，强制media type、body/header上限、closed object、必填字段、单一JSON value以及path/header/enum/identity解析，保证失败前不调用application。
- [x] 4.3 实现集中response codec，将account/session/ticket/world/visit/admission纯Go结果映射为冻结OpenAPI字段、enum和Unix毫秒，不公开player/session内部事实、node/fence或credential binding。
- [x] 4.4 实现基于 `errors.json` 的集中稳定错误映射与correlation response，覆盖account/session/personalworld/placement/visitsession/worldadmission error及commit phase，不输出backend cause。
- [x] 4.5 增加OpenAPI metadata/closed schema/fixture交叉测试，要求runtime operation table、codec与全部HTTP fixture精确一致且不存在transport-local同义错误。

## 5. Middleware 与资源治理

- [x] 5.1 使用 `gin.New` 组装固定middleware顺序，实现安全panic recovery、CSPRNG request ID、安全响应头、shared readiness gate和不记录credential的access log。
- [x] 5.2 实现OpenAPI operation deadline、客户端取消传播、bodyless请求拒绝和server header/read/write/idle timeout，验证mutation取消不被解释为确定未提交。
- [x] 5.3 实现Bearer middleware，通过Session service取得AuthenticatedSession并只在request context中传递受信值；公开/受保护route认证矩阵必须与OpenAPI一致。
- [x] 5.4 实现容量和idle lifetime有硬上限的进程内limiter，分别执行pre-auth IP与post-auth SessionID/epoch策略并映射 `RATE_LIMITED`/有界retry提示。
- [x] 5.5 扩展低基数HTTP metrics和日志测试，只允许operationId、status class、stable outcome、duration/bytes等字段，禁止URL identity、IP、principal、token/ticket/admission、Idempotency-Key和错误文本。

## 6. Handler 与公开 HTTP component

- [x] 6.1 实现version/config/register/login handler，仅执行decode/validate/authorize/call/encode并返回冻结status与安全endpoint/limit投影。
- [x] 6.2 实现refresh/logout/ticket handler，保持refresh replay、epoch失效、channel-only ticket选择和EndpointProvider授权边界。
- [x] 6.3 实现world bootstrap、invite accept与world admission handler，只调用worldentry用例并验证payload不能覆盖actor/world/role/endpoint。
- [x] 6.4 实现拥有listener、TLS和in-flight request的public HTTP lifecycle component，确保Start失败局部释放、Stop幂等且遵守共享shutdown deadline。
- [x] 6.5 增加handler/middleware/component的无listener或 `httptest` 测试，覆盖success、validation、auth、rate limit、panic、timeout、unknown route、TLS handshake和graceful shutdown。

## 7. Composition Root 与真实适配器接线

- [x] 7.1 在唯一Composition Root中从共享MySQL DB构造Account/PersonalWorld repositories、Argon2id hasher及dummy hash，禁止新pool和memory adapter。
- [x] 7.2 从共享Redis client/keyspace构造Session/Placement/VisitSession/WorldAdmission stores及全部policy/service/worldentry依赖，禁止新client、credential owner或后台timer。
- [x] 7.3 把public HTTP component置于storage之后启动、之前关闭，并调整readiness、启动回滚、task ownership和最终运行日志以准确表达HTTP ready而非realtime connectivity。
- [x] 7.4 增加Composition Root依赖、结构、启动失败和关闭顺序测试，证明公开route只在完整production graph成功后可达且no-active invalidator只存在于当前无realtime阶段。

## 8. 集成验收、文档与质量门

- [x] 8.1 扩展隔离storage harness或新增统一HTTP verify入口，使用真实MySQL/Redis、production adapters、临时TLS证书和动态loopback端口运行10个operation。
- [x] 8.2 增加端到端HTTP integration场景，覆盖register/login/refresh/logout/ticket/bootstrap/accept/admission、幂等重放/冲突、Redis flush/corruption、stale身份/assignment、依赖失败和有界清理。
- [x] 8.3 运行HTTP fixtures、admission相关组合测试、全量Go测试、race、vet、module verify、strict OpenSpec与diff检查，并修复所有非预期结果。
- [x] 8.4 按代码注释规范复核全部新增Go/PowerShell注释和导出契约，清理重复映射、魔法值、吞错、敏感格式化与过度interface。
- [x] 8.5 更新 `docs/architecture.md`、`docs/file-structure.md`、`docs/network-transport-architecture.md`、`server/README.md`、配置/运行/验收说明，明确HTTP已接线而WSS/TLS-TCP仍未实现。
