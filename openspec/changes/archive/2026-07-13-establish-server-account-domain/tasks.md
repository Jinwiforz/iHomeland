## 1. Account 值模型与输入安全

- [x] 1.1 在 `server/internal/account` 建立 account/player ID、Account、Status、AccountSummary 与 created time 值模型，使用安全 ASCII 前缀和私有字段，确保客户端不能选择身份或状态
- [x] 1.2 实现 3-64 bytes username 校验与 ASCII lowercase canonicalization，固定允许字符、首尾边界和 case-insensitive 唯一语义，并增加 table-driven/fuzz tests
- [x] 1.3 使用项目锁定的 `golang.org/x/text` 实现 display name UTF-8/NFC/空白规范化及 control/format/bidi 拒绝，按 1-32 Unicode code points 验证并覆盖 Unicode fuzz tests
- [x] 1.4 实现 RegisterPassword、LoginPassword 与 CredentialHash 值类型，分别执行 12-128/1-128 bytes 边界且不修改原始 password；封闭 register/login command 字段，并为 command、credential 的默认格式化、slog、error 和零值增加脱敏测试
- [x] 1.5 定义稳定 account error kind、operation 与安全 commit phase，区分 validation、username conflict、invalid credentials 和 dependency unavailable，但不暴露 username、password、hash 或 repository 细节

## 2. Credential、Repository 与 Session 边界

- [x] 2.1 定义 `CredentialHasher` 的 Hash/Verify 契约和构造时必需的有效 dummy hash，确保 unknown username 通过同一 Verify 路径；不实现或接线具体 production password algorithm
- [x] 2.2 定义窄 `AccountRepository`、authentication record、create/find outcome，要求 account/player/credential 原子创建和 canonical username 唯一，不暴露通用 CRUD、SQL 或 plaintext password
- [x] 2.3 定义只包含 `CreateSession` 的 `SessionIssuer` 消费接口，复用 `session.Principal/SessionResult` 与 Composition Root clock/ID generator，并增加编译期兼容断言防止第二套基础实现
- [x] 2.4 创建只存在于 `_test.go` 的并发安全 reference repository、fake hasher/session issuer/clock/ID，模拟唯一冲突、not found、inactive account、依赖失败和 session 签发失败
- [x] 2.5 为接口原子性、context 取消后的提交不确定性、dummy hash 零值、repository malformed record 和敏感值所有权增加 contract/guardrail tests

## 3. Register 用例

- [x] 3.1 实现 validate/normalize -> hash -> generate IDs/time -> repository Create -> SessionIssuer.CreateSession 的固定注册顺序，并返回不含 username/player ID/hash 的安全 account/session result
- [x] 3.2 覆盖 canonical case variants、重复请求与并发注册，证明同一 username 最多创建一个完整 account/player/credential，NON_IDEMPOTENT register 不会隐式转为 login
- [x] 3.3 实现 repository 已提交但 session 签发失败的 `account_created` dependency phase，确认账号保持可登录、不执行跨存储删除、不返回伪 token且重试 register 返回 username conflict
- [x] 3.4 覆盖 hash/ID/repository/session 各阶段依赖失败与 context cancellation，确认已知提交前失败无 account、repository 模糊结果返回 `account_commit_unknown`、已确认账号后的 session 失败不谎报回滚或成功

## 4. Login 与枚举防护

- [x] 4.1 实现 canonical username lookup、原始 password Verify、active status 检查、Principal 构造与新 session 签发的 login 流程
- [x] 4.2 对 unknown username 使用注入 dummy hash 调用同一 Verify，并让 unknown、wrong password、inactive status 返回相同 invalid credentials kind、消息形态和无 session 结果
- [x] 4.3 覆盖 malformed repository record、hasher/repository/session dependency failure、并发 login 和多 session 行为，确认每次成功 login 创建独立 session，失败不会修改账号状态或泄漏 credential/account existence
- [x] 4.4 审计 account API 不包含 Refresh/Logout/Ticket/raw-token passthrough，不构造 AuthContext、不解析 token，也不复制 Session Core error/epoch 状态

## 5. Contract、文档与门禁

- [x] 5.1 收紧 OpenAPI register/login username pattern并补充 displayName normalization 说明，在 error registry 新增 code 104 `ACCOUNT_USERNAME_TAKEN`，更新 HTTP fixtures/validator 并确认 breaking migration 说明完整
- [x] 5.2 更新 `server/README.md`、`docs/architecture.md`、`docs/file-structure.md`、`docs/roadmap.md` 与必要安全说明，明确 account core 已具备的行为、refresh/logout owner 和仍未接线的 hasher/repository/listener
- [x] 5.3 验证正式 Composition Root 未注入 fake account service、未新增配置/listener/migration、未连接 MySQL/Redis、未提交 generated code，且没有第二套 session/token/clock/ID 实现
- [x] 5.4 执行 protocol verify、Go format/vet/unit/fuzz/race、依赖与 credential/secret scan、`git diff --check` 和 OpenSpec strict，并复核全部手写注释符合 `docs/code-comment-convention.md`
- [x] 5.5 同步新增 `server-account` 与修改后的 `server-contracts` 主 specs，确认 artifacts、tasks、文档与实际行为一致后再归档
