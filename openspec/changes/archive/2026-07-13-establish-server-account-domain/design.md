## Context

服务端已经具备统一 Session Core 和跨端 register/login/refresh/logout 契约，但尚无账号领域或持久化接口。Session Core 明确要求 `CreateSession` 只能接收上游已验证的 `Principal`，因此下一步必须先建立 username、credential、account/player identity 和状态的唯一 owner，才能安全进入 MySQL 与 HTTPS adapter。

本 change 仍处于 domain/application 阶段：依靠 fake repository、credential hasher、clock、ID generator 和 session issuer 独立验收，不连接 MySQL/Redis，不启动 listener，也不把 OpenAPI/generated type 引入业务 package。具体密码算法与数据库 schema 属于后续基础设施接线，但本 change 必须把它们需要满足的安全契约定义完整。

## Goals / Non-Goals

**Goals:**

- 建立账号、玩家身份、canonical username、display name、status、created time 和安全 summary 的唯一语义。
- 建立注册密码、登录密码、credential hash 的不可混用值类型与默认脱敏行为。
- 定义能由具体 memory-hard password adapter 实现的 hash/verify 契约，并要求注入同算法、同成本的有效 dummy hash。
- 定义原子 username uniqueness repository 接口和稳定 outcome，不暴露 SQL 或通用 CRUD。
- 实现注册与登录 application service，并与 Session Core 的 principal/session 语义连接。
- 防止 username case 绕过、并发重复注册、账号枚举、密码日志泄漏和 payload 身份注入。
- 兼容新增 username conflict error，并显式记录 username 接受集合收紧这一 breaking HTTP 字段变更。

**Non-Goals:**

- 不实现具体 Argon2id/bcrypt/scrypt 参数、pepper/KMS、hash 升级或 credential migration。
- 不连接 MySQL/Redis，不创建 migration，不向正式 Composition Root 注入 fake repository/hasher。
- 不实现 HTTP handler、TLS、rate limit、CAPTCHA、邮件/手机验证、找回密码、MFA、OAuth/OIDC。
- 不实现账号管理后台、ban/disable command 来源、角色权限、好友、资产或完整 profile。
- 不包装 Session Core 已拥有的 refresh/logout/ticket use cases，不创建第二套 token 或 epoch。
- 不实现 Unity 客户端。

## Decisions

### 1. Username 使用受限 ASCII canonical key，display name 独立支持 Unicode

Username 是登录和唯一索引，不是展示文本。输入必须为 3-64 bytes，仅允许 ASCII letter、digit、`.`、`_`、`-`，首尾必须为 letter/digit；不接受首尾空白或 Unicode look-alike。Canonical form 使用 ASCII lowercase，因此 `Player.One` 与 `player.one` 是同一 username，repository 只按 canonical value 建唯一约束。

Display name 是展示值：输入必须为有效 UTF-8，先执行 Unicode NFC、拒绝 control、format 和 bidi override 字符，再裁剪首尾非 control Unicode whitespace 并把连续空白规范为单个 ASCII space，最后限制为 1-32 Unicode code points。Tab、换行等 control whitespace 必须拒绝而不是折叠；display name 不参与登录、唯一性或权限判断。

选择受限 username 而不是 Unicode case folding，是为了避免 confusable、数据库 collation 和跨语言 normalization 差异；中文等 Unicode 展示需求由 display name 满足。

### 2. Password 保持原始输入，hashing 通过消费侧安全接口完成

注册密码必须为 12-128 bytes，登录密码允许 1-128 bytes 以便未来兼容旧策略；两者都不 trim、不 lowercase、不 Unicode normalize。Password、`CredentialHash` 以及承载认证输入的 register/login command 使用私有字段，必须通过 constructor 创建；它们的 `Stringer`、`GoStringer` 和 `slog.LogValuer` 默认只返回固定占位符，避免调用方在完成值类型转换前通过结构体格式化泄露 username 或 plaintext。错误不得包含长度之外的输入信息。

`CredentialHasher` 由 account application 消费侧定义：`Hash` 只接收注册 password 并返回自描述 hash，`Verify` 接收 hash/password 并返回 match。Service 构造时必须注入使用相同算法和成本参数生成的有效 dummy hash；unknown username 调用同一个 `Verify(dummyHash, password)`，不能提供容易走不同实现路径的 `VerifyUnknown` 捷径。Repository 只接收 `CredentialHash`，永远不接触 plaintext password。

具体算法、参数、salt source、可选 pepper 和 rehash 策略需要结合部署资源 benchmark 后由基础设施 change 锁定；引入的 library/version 必须进入 `versions.yaml`，运行参数则由受校验配置管理。当前不引入一个尚未接线的生产实现。替代方案是在 repository 内 hash，会把凭据安全与 SQL transaction 混合并难以独立测试，因此拒绝。

### 3. Account 与 Player identity 同时创建，客户端 summary 不暴露内部 credential

注册使用 Composition Root 的通用 CSPRNG ID generator 分别生成 `acc_` 与 `ply_` 前缀的安全 ASCII IDs。`Account` 保存 account id、player id、canonical username、display name、status 和 created time；session principal 由 account/player IDs 构造。公开 summary 只包含既有协议要求的 account id、display name 和 created time，不包含 username、player id、status 或 credential hash。

账号初始状态固定为 active。Repository 可以返回 inactive 状态供登录拒绝，但 ban/disable 状态迁移命令不在本 change 创建，避免没有 owner 的管理 API。

### 4. Repository 直接表达唯一性和认证读取，不暴露通用 CRUD

`AccountRepository` 提供原子 `Create` 和按 canonical username 的 `FindForAuthentication`。Create 必须在一个事务边界写入 account/player/credential 并以显式 `created`、`username_conflict` outcome 表达唯一索引竞争；并发注册同一 canonical username 最多一个成功。Context deadline 或连接错误只限制等待，不能证明事务没有提交；未知结果必须表达为 `account_commit_unknown` dependency phase，重试允许收敛为 username conflict，但不能留下部分 account/player/credential。

Find 返回认证所需 record 或统一 not-found outcome。Repository error 表示依赖失败，不能伪装成 username 不存在；接口不提供按客户端 account id 直接取得 credential 的捷径。MySQL table、index、transaction isolation 与 error translation 由 storage change 实现 contract tests。

### 5. 注册先提交持久账号，再创建可失效 session

注册顺序固定为 validate/normalize -> hash -> generate IDs/time -> repository Create -> SessionIssuer.CreateSession。账号是 MySQL 持久事实，session 是 Redis 可失效运行态，两者不使用分布式事务。

Repository 成功而 session 创建失败时，账号保持已创建，application 返回带稳定 `account_created` phase 的 dependency error，不能删除账号或伪造成功 token。客户端随后使用 login 恢复；重复 register 按 username conflict 处理。先创建 session 再写账号会产生可认证的孤儿 principal，因此拒绝。

### 6. Login 对可枚举失败保持统一外部语义

Login 规范化 username 后读取 authentication record。Username 不存在时仍使用注入 dummy hash 调用同一 `Verify`；wrong password、unknown username 与 inactive account 都返回同一 `invalid_credentials` kind，且不创建 session。只有 password match 且 account active 才构造 Principal 并调用 SessionIssuer。没有明确 session-limit policy 时，每次成功 login 创建独立 session，不静默踢掉其他设备。

Dummy verification 只能降低明显 timing 差异，不能替代 HTTP adapter 的 IP/identity rate limit、连接预算和监控。Application 不记录 username、password、hash 或完整 principal；adapter 只映射稳定错误。

### 7. Refresh/logout 保持 Session Core owner，account service 不做无逻辑转发

Account service 只实现需要账号事实的 register/login。`/v1/auth/refresh` 与 `/v1/auth/logout` 在后续 HTTP change 分别调用 Session Core 的 `Refresh` 与 `Logout`；为追求单一 facade 而增加 passthrough 会扩大 raw token 生命周期并模糊 owner，因此拒绝。

### 8. Username conflict 需要独立稳定协议错误

登录继续使用既有 `AUTH_INVALID_CREDENTIALS`。注册 username 唯一冲突新增 code 104 `ACCOUNT_USERNAME_TAKEN`，owner 为 account、category 为 conflict、HTTP status 为 409 且不可直接重试。OpenAPI 增加 username pattern 和规范化说明，HTTP fixtures 覆盖冲突映射，contract validator tests 覆盖字段拒绝；error code 是兼容新增，但 username 接受集合收紧属于 breaking contract change，必须在提交与迁移说明中明确。

## Risks / Trade-offs

- [具体 password algorithm 延后] -> 接口要求 memory-hard、自描述 hash、随机 salt、dummy verify 和 benchmark；storage/runtime change 未提供合格实现前不得接线账号服务。
- [ASCII username 降低可表达性] -> display name 完整支持受控 Unicode，登录 key 保持跨端和数据库一致。
- [注册存在 account-created/session-failed 部分成功] -> 不做危险跨存储回滚；返回稳定 phase、记录低基数指标，并允许用户随后 login。
- [Dummy verify 不能完全消除 timing side channel] -> 使用同算法/参数 dummy hash，并由 HTTP adapter 执行统一响应、rate limit 和并发预算。
- [Concurrent registration 依赖数据库唯一约束] -> 测试 fake 提供线性化参考语义，MySQL adapter 必须以 unique index 和 contract test 证明。
- [Repository timeout 后 commit 状态不明确] -> 返回 `account_commit_unknown`，不声称回滚；重试由 canonical unique key 收敛，storage contract test 覆盖 commit-before-error。
- [Display name Unicode 规则需要 `x/text` normalization] -> 使用已锁定 Go dependency，table/fuzz tests 覆盖 invalid UTF-8、control、bidi 和边界长度。
- [Username contract 收紧拒绝既有非 ASCII 输入] -> 当前没有公开账号 listener 或生产数据；迁移规则要求 login username 使用安全 ASCII，Unicode 用户可见文本只进入 display name。

## Migration Plan

1. 建立 account 值类型、normalization、status、password/hash redaction 与 table/fuzz tests。
2. 定义 repository、credential hasher、session issuer 消费接口和稳定错误/outcome。
3. 实现 register/login，并覆盖并发注册、dummy verify、inactive account 与 session 部分失败。
4. 更新 OpenAPI/error registry/fixtures，显式记录 username breaking 收紧，并执行 protocol verify 与 OpenSpec strict。
5. 更新 owner 文档，确认正式 Composition Root 未接入 fake 或开放 listener。

当前没有账号生产数据或公开账号 listener，失败时可整体回退本 change。后续 storage change 实现持久化与具体 hasher，HTTP change 完成 rate limit、错误映射和端到端验收。

## Open Questions

无。具体 password algorithm/参数不是未决业务语义，必须由后续基础设施 change 基于目标硬件 benchmark 后锁定；在此之前账号运行时保持未接线。
