## ADDED Requirements

### Requirement: Account HTTP 契约必须与领域验证和唯一冲突一致
OpenAPI register/login username MUST 声明 3-64 characters、ASCII letter/digit/`.`/`_`/`-`、首尾 letter/digit 的可执行 pattern，并说明服务端使用 ASCII lowercase canonical key。DisplayName MUST 说明 Unicode normalization 与安全字符边界。Error registry MUST 新增唯一、稳定、owner 为 account 的 code 104 `ACCOUNT_USERNAME_TAKEN` conflict error，HTTP status 为 409 且 retryable 为 false；已有编号、响应结构和 `AUTH_INVALID_CREDENTIALS` 语义不得改变。

#### Scenario: OpenAPI 拒绝非法 username
- **WHEN** contract validation 输入包含空白、非 ASCII、首尾标点或超出长度的 register/login username
- **THEN** schema validation 在进入 application 前拒绝输入，并与 account normalization 接受集合一致

#### Scenario: 注册 username 冲突映射
- **WHEN** account application 返回 username conflict
- **THEN** HTTPS adapter 映射为 `ACCOUNT_USERNAME_TAKEN` 和 HTTP 409，不误用 validation、invalid credentials 或 dependency error

#### Scenario: 合同兼容性验证
- **WHEN** 新增 account error 与字段约束后执行协议验证
- **THEN** error code 保持唯一、fixtures 与 OpenAPI schema 一致，既有编号和成功响应字段不发生 breaking change
