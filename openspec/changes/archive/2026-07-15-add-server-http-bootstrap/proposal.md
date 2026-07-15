## Why

账号、会话、个人世界、placement、VisitSession 与 world admission 已分别具备生产存储和应用语义，但当前进程仍只开放诊断面，公开 OpenAPI 无法被真实客户端调用。按照 N0 交付顺序，现在需要建立独立、可关闭、默认 fail closed 的 HTTPS 启动与账号面，为后续 WSS/TLS-TCP 接入提供唯一认证和准入入口。

## What Changes

- 新增与诊断 listener 隔离的公开 HTTPS listener，严格实现现有 OpenAPI 中的 version/config、register/login/refresh/logout、connection ticket、world bootstrap、invite accept 与 world admission issue operation。
- 在唯一 Composition Root 中使用现有 MySQL/Redis production adapters 构造 Account、Session、PersonalWorld、Placement、VisitSession 与 WorldAdmission service graph；禁止 memory fallback、transport-local credential 或重复状态机。
- 建立统一 HTTP adapter 边界，执行 decode、schema/字段校验、bearer authorize、幂等键与 request deadline/body limit/rate policy，并将稳定 domain outcome 映射为既有公共 response/error；handler 不持有业务规则。
- 将公开 TLS、advertised endpoint、限流、资源上限与 admission derivation secret 纳入启动前完整配置验证，并把 HTTP listener 接入 readiness、结构化日志、低基数 metrics、启动回滚和有界 graceful shutdown。
- 增加 handler/application 单元测试、公开 HTTPS integration tests 与既有 HTTP/admission fixtures 验证，覆盖成功、重复请求、陈旧身份/assignment、依赖失败、超限、超时、敏感信息脱敏和关闭场景。
- 本 change 不实现 WSS control、TLS/TCP framing/dispatcher、world mutation、admission consume 或 Unity/Go 业务客户端；尚未交付的 realtime listener 只可作为经过验证的 advertised endpoint 配置，不得被 HTTP readiness 冒充为已可连接能力。

## Capabilities

### New Capabilities

- `server-http-bootstrap`: 定义生产 HTTPS 启动与账号 API、真实 service graph 接线、统一 HTTP 安全边界、生命周期、可观测及 integration 验收。

### Modified Capabilities

- `server-visit-session`：明确公开 HTTP accept 中由服务端时钟派生的 reservation candidate deadline 不属于客户端稳定命令语义，同一 command 仍重放首次已提交 deadline。
- `server-world-admission-runtime`：明确签发重试时服务端时钟推进不得改变幂等 identity 或延长首次 credential，权威 deadline 收紧仍必须冲突。
- `server-contracts`：为 register/login password 补充既有 Account 领域 128-byte 上限的机器可读扩展，消除 OpenAPI character length 与领域 byte budget 的歧义。

## 进入条件、Owner 与完成门槛

- **上游与进入条件：**依赖已归档的 Account/Session storage、PersonalWorld/Placement core 与 storage、VisitSession core 与 storage、WorldAdmission runtime，以及冻结 HTTP/world/visit contracts；本 change 不改写这些 owner 的持久或运行事实。
- **业务与状态 Owner：**Account、Session、PersonalWorld、Placement、VisitSession、WorldAdmission 继续分别拥有自身状态；`worldentry` 只协调三个公开用例，`httpapi` 只拥有 transport policy 与 listener lifecycle。Owner/Visitor 身份只来自 AuthContext 与领域事实，payload 不能选择 actor role。
- **Mutation、settlement 与 safe-return：**本 change 不开放 world mutation、奖励或结算，也不执行 realtime safe-return side effect；HTTP 只返回已有 owner 的投影或签发结果，VisitSession close/directive 仍由既有 owner 管理。
- **自动化与完成条件：**10 个 operation 必须通过 unit、contract、race、真实 MySQL/Redis、HTTPS lifecycle、strict OpenSpec 与文档一致性验收；公开 readiness 不能冒充 WSS/TLS-TCP 可连接。
- **回滚点：**回滚到本 change 实现提交的直接父提交并移除 public listener/config；本 change 不新增 MySQL migration，Redis 短期资格按既有 TTL 失效，无需伪造反向数据迁移。

## Impact

- 服务端：`server/internal/config`、`server/internal/app`、新增公开 HTTP transport adapter，以及现有 account/session/personalworld/placement/visitsession/worldadmission production adapters 的组合接线。
- 契约：消费 `shared/contracts/http/v1/openapi.yaml`、稳定错误目录和 HTTP/admission fixtures；只补充既有 password byte budget 的机器可读约束，不重新定义 operation、DTO、错误编号或 credential 语义。
- 运行配置：新增公开 HTTPS listener、证书/私钥 secret、advertised WSS/TLS-TCP endpoints、operation policy 与 world admission derivation key 配置；示例配置和运维说明同步更新。
- 验证：扩展 Go 单元、contract 与 `storage_integration`/HTTPS integration 验收；不引入 Unity 运行时代码或新的外部协议依赖。
