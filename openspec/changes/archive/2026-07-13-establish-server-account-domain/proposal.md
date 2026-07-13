## Why

Session Core 已能为经过验证的 principal 创建和撤销统一身份，但服务端尚无账号最终事实、凭据验证或并发注册规则。若直接进入 MySQL/HTTPS adapter，username 规范化、密码处理、账号状态和错误语义会散落到 handler 与 repository，形成无法独立测试的第二套认证逻辑。

## What Changes

- 建立 account id、player id、canonical username、display name、status、created time 与安全投影的纯 Go 值模型。
- 固定 username 的 ASCII case-insensitive 规范化、display name 的 Unicode/空白规则，以及注册密码与登录密码不同的长度验证边界。
- 定义 password hasher/verifier 消费接口和不可默认格式化的 password/hash 值；domain/application 不选择具体算法、参数或存储格式。
- 定义 account repository 消费接口，以原子 username uniqueness 表达创建、读取和依赖失败，不泄漏 SQL 或通用 CRUD。
- 实现注册与登录 application use cases：注册先提交持久账号再创建 session；session 创建失败不回滚账号，客户端随后可以登录恢复。
- 登录统一 unknown username、wrong password 与不可认证状态的外部错误，且 unknown username 仍执行 dummy verification，降低账号枚举与明显 timing 差异。
- 明确 refresh/logout 继续由 `internal/session` 拥有，account package 不增加无业务价值的 raw-token 转发 facade。
- **BREAKING** 收紧 OpenAPI username 接受集合为安全 ASCII pattern；为 username 冲突增加稳定 account error code，并让 username/displayName 说明与可执行领域规则一致。
- 增加 normalization、credential redaction、并发注册、登录枚举防护、账号状态和 session 依赖部分失败测试；不启动 listener 或连接 MySQL/Redis。

## Capabilities

### New Capabilities

- `server-account`：定义账号身份、输入规范化、凭据哈希边界、repository 契约、注册/登录用例、账号状态和安全失败行为。

### Modified Capabilities

- `server-contracts`：补充可执行的 username/displayName 账号字段约束与稳定 username conflict 错误，使未来 HTTPS adapter 能无损映射 account 结果。

## Impact

- 新增 `server/internal/account` 纯 Go domain/application package 及仅测试 fake repository、hasher 和 session issuer。
- 复用 `internal/session` 的 `Principal`、`SessionResult` 与 Composition Root 的生产 clock/ID，不创建第二套 token、epoch、clock 或 ID generator。
- 更新 OpenAPI、error registry、HTTP fixtures、长期 specs、路线图和服务端目录说明；使用非 ASCII username 的调用方必须改用 ASCII login username，并把 Unicode 文本放入 display name；generated code 不提交。
- 后续 storage change 实现 MySQL account repository 与具体 password hasher，HTTP change 只负责 decode、rate limit、调用 use case 和映射稳定错误。
- 不实现真实 password hash 算法、MySQL migration、Redis、HTTP handler、邮件/手机、找回密码、MFA、OAuth、角色权限、完整 profile 或 Unity 代码。
