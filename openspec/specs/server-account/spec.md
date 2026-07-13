# 服务端账号规格

## Purpose

定义账号身份、输入规范化、credential 与 repository 边界、register/login 应用用例、账号枚举防护以及与 Session Core 的所有权分离。

## Requirements

### Requirement: Username 必须具有唯一 canonical form
服务端 MUST 将 username 限制为 3-64 bytes 的 ASCII identifier，仅允许 letter、digit、`.`、`_`、`-` 且首尾为 letter/digit，并使用 ASCII lowercase 作为唯一 canonical key。Username MUST NOT 接受首尾空白、Unicode look-alike 或控制字符；repository MUST 对 canonical username 执行原子唯一约束。

#### Scenario: Case variant 注册同一 username
- **WHEN** 已存在 canonical username `player.one`，另一个注册请求提交 `Player.One`
- **THEN** 两者规范化为同一 key，第二次创建返回 username conflict 且不新增 account、player 或 credential

#### Scenario: Username 包含 Unicode look-alike
- **WHEN** 注册或登录 username 包含非 ASCII 字符、首尾标点或空白
- **THEN** application 在调用 hasher、repository 或 session 前返回稳定 validation error

### Requirement: Display name 必须规范化且不能参与身份授权
服务端 MUST 对 display name 执行有效 UTF-8、Unicode NFC、首尾非 control Unicode whitespace 裁剪和连续空白折叠，并在规范化后限制为 1-32 Unicode code points。Control（包括 tab 与换行）、format 与 bidi override 字符 MUST 被拒绝而不是折叠。Display name MUST NOT 用于登录唯一性、principal、repository key 或权限判断。

#### Scenario: Display name 包含组合字符和重复空白
- **WHEN** 注册请求提交可规范化的组合字符以及连续 Unicode whitespace
- **THEN** account 保存 NFC 且空白稳定的展示值，并在 summary 中返回同一规范化结果

#### Scenario: Display name 包含 bidi override
- **WHEN** display name 包含 Unicode bidi override 或 control 字符
- **THEN** 注册返回 validation error，危险文本不会进入 repository、日志或客户端 summary

### Requirement: Plaintext password 必须保持原样且只能进入 credential boundary
注册 password MUST 为 12-128 bytes，登录 password MUST 为 1-128 bytes；服务端 MUST NOT trim、case-fold 或 Unicode normalize password。承载 plaintext 的 register/login command、password value、credential hash 与 dummy hash MUST 默认脱敏，不得进入日志、错误、metrics label、repository key 或 account summary。Repository MUST 只接收自描述 `CredentialHash`。

#### Scenario: Password 仅空白或大小写不同
- **WHEN** password 包含首尾空白或与已注册 password 仅大小写不同
- **THEN** hasher 按原始 bytes 处理，application 不修改输入且验证结果由 credential boundary 决定

#### Scenario: Credential 被格式化或记录
- **WHEN** register/login command 或 password/hash 被作为 `%v`、`%#v`、error 参数或 slog attribute 传入
- **THEN** 输出只包含固定占位符，不包含 username、plaintext、完整 hash、salt 或 pepper

### Requirement: Account 必须同时拥有稳定 account/player identity 与认证状态
新账号 MUST 使用服务端 CSPRNG ID generator 分别创建不可由客户端选择的 account id 与 player id，初始状态 MUST 为 active，并保存 canonical username、normalized display name 和绝对 created time。公开 account summary MUST 只包含 account id、display name 与 created time；session principal MUST 只由 repository 返回的 account/player IDs 构造。

#### Scenario: 客户端提交 account 或 player id
- **WHEN** register/login payload 或 adapter 尝试指定 account id、player id、status 或 created time
- **THEN** application 忽略或拒绝这些字段，最终身份只来自服务端创建和 repository 当前事实

#### Scenario: Inactive account 登录
- **WHEN** credential 验证匹配但 repository account status 不是 active
- **THEN** application 不创建 session，并与 unknown username/wrong password 使用相同 invalid credentials 外部语义

### Requirement: AccountRepository 必须原子表达创建与认证读取
Account application MUST 通过消费侧 `AccountRepository` 执行原子 Create 和按 canonical username 的 FindForAuthentication。Create MUST 在同一持久事务中写入 account、player 与 credential hash，并显式返回 created 或 username conflict；接口 MUST NOT 暴露 SQL、通用 CRUD、plaintext password 或 generated protocol type。

#### Scenario: 并发注册同一 canonical username
- **WHEN** 两个注册操作并发创建同一 canonical username
- **THEN** 最多一个原子 Create 返回 created，另一个返回 username conflict，且不存在重复或部分 account/player/credential

#### Scenario: Repository 依赖失败
- **WHEN** Create 或 FindForAuthentication 的持久依赖不可用
- **THEN** application 返回 dependency unavailable，不把失败伪装成 username conflict、not found 或 invalid credentials

#### Scenario: Create 提交后响应丢失
- **WHEN** repository 已原子提交 account/player/credential，但调用方因 timeout 或连接错误无法确认结果
- **THEN** application 返回 `account_commit_unknown` dependency phase，不声称账号未创建或执行补偿删除，后续重试可以由 canonical unique key 收敛为 username conflict

### Requirement: 注册必须先提交持久账号再签发 session
Register use case MUST 按 validate/normalize、hash、generate identity/time、repository Create、SessionIssuer.CreateSession 的顺序执行。Repository 成功后 session 签发失败 MUST NOT 删除或回滚账号，也不得返回 token；错误 MUST 保留安全 `account_created` phase，使后续 login 可以恢复。

#### Scenario: 注册成功
- **WHEN** 输入有效、username 未占用、repository 创建成功且 session 签发成功
- **THEN** application 返回安全 account summary 与 Session Core 产生的 session/token 结果，不返回 credential hash 或内部 record

#### Scenario: Account 已创建但 session 依赖失败
- **WHEN** repository 已提交新账号而 SessionIssuer 返回依赖错误
- **THEN** 注册返回 account-created phase 的 dependency failure，账号保持可登录且不会创建伪 token 或执行跨存储删除

#### Scenario: 重复提交已成功注册请求
- **WHEN** 客户端在未使用幂等键的情况下再次提交相同 register 输入
- **THEN** application 按 username conflict 处理，不把 NON_IDEMPOTENT register 偷换为隐式 login

### Requirement: Login 必须防止账号枚举并只为有效账号创建 session
Login use case MUST 规范化 username、读取 authentication record、通过 credential boundary 验证原始 password，并只在 password match 且 account active 时创建 session。Service MUST 注入使用相同算法/成本参数生成的有效 dummy hash；unknown username MUST 调用与正常账号相同的 Verify 方法验证该 dummy hash。Unknown username、wrong password 与 inactive account MUST 返回相同 invalid credentials kind 和安全消息。

#### Scenario: Username 不存在
- **WHEN** FindForAuthentication 返回 not found
- **THEN** application 使用注入 dummy hash 调用同一 Verify、不创建 session，并返回与 wrong password 相同的 invalid credentials

#### Scenario: Password 错误
- **WHEN** account 存在但 password verification 不匹配
- **THEN** application 不创建 session，不暴露 account/status/hash 是否存在，并返回 invalid credentials

#### Scenario: Login 成功
- **WHEN** account active 且 password verification 匹配
- **THEN** application 从 repository account/player IDs 构造 Principal，创建新的 session，并返回安全 account/session/token 结果

#### Scenario: 同一账号重复成功登录
- **WHEN** active account 在没有 session-limit policy 时多次通过 credential verification
- **THEN** 每次 login 创建彼此独立的 session，不复用 token、不递增其他 session epoch，也不静默踢掉已有连接

### Requirement: Account 与 Session use case ownership 必须保持分离
Account package MUST 只拥有需要账号事实的 register/login。Token refresh、logout、ticket 和 epoch invalidation MUST 继续由 Session Core 拥有；account application MUST NOT 增加 raw-token passthrough、复制 token parsing 或自行构造 AuthContext。

#### Scenario: HTTP adapter 处理 refresh/logout
- **WHEN** 后续 HTTPS adapter 实现 `/v1/auth/refresh` 或 `/v1/auth/logout`
- **THEN** adapter 分别调用 Session Core 的 Refresh/Logout，不经过 account facade 且不产生第二套认证状态

### Requirement: Account core 必须可在无 listener 与真实存储环境验收
Account domain/application MUST 只依赖消费侧 repository、credential hasher、session issuer、clock 和 ID generator。实现与测试 MUST NOT 启动 listener、连接 MySQL/Redis、依赖 generated protocol type 或把 fake repository/hasher 注册到正式 Composition Root。

#### Scenario: 独立运行账号安全测试
- **WHEN** 测试 normalization、并发注册、dummy verification、inactive login 和 session 部分失败
- **THEN** 测试只使用 deterministic fake 与并发 reference repository，并能通过 race detector 验证

#### Scenario: 正式服务端启动
- **WHEN** production MySQL repository、password hasher、session adapter 或 HTTPS adapter 尚未全部接线并通过对应验收
- **THEN** Composition Root 不接线账号服务，不新增未被消费的账号配置，也不宣称 register/login 已可用
