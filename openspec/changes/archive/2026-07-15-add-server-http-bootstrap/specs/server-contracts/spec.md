## MODIFIED Requirements

### Requirement: Account HTTP 契约必须与领域验证和唯一冲突一致
OpenAPI register/login username MUST声明3-64 characters、ASCII letter/digit/`.`/`_`/`-`、首尾letter/digit的可执行pattern，并说明服务端使用ASCII lowercase canonical key。DisplayName MUST说明Unicode normalization与安全字符边界。Register/login password MUST保持原始UTF-8 bytes、不执行trim或Unicode normalization，并在标准character length之外以机器可读扩展声明Account领域共同的128-byte上限。Error registry MUST新增唯一、稳定、owner为account的code 104 `ACCOUNT_USERNAME_TAKEN` conflict error，HTTP status为409且retryable为false；已有编号、响应结构和 `AUTH_INVALID_CREDENTIALS` 语义不得改变。

#### Scenario: OpenAPI 拒绝非法 username
- **WHEN**contract validation输入包含空白、非ASCII、首尾标点或超出长度的register/login username
- **THEN**schema validation在进入application前拒绝输入，并与account normalization接受集合一致

#### Scenario: Password 超过领域 byte budget
- **WHEN**register/login password虽未超过OpenAPI character maxLength但其原始UTF-8编码超过128 bytes
- **THEN**HTTP adapter在进入Account application与password hasher前拒绝输入，且contract validator要求schema保留机器可读128-byte约束

#### Scenario: 注册 username 冲突映射
- **WHEN**account application返回username conflict
- **THEN**HTTPS adapter映射为 `ACCOUNT_USERNAME_TAKEN` 和HTTP 409，不误用validation、invalid credentials或dependency error

#### Scenario: 合同兼容性验证
- **WHEN**新增account error与字段约束后执行协议验证
- **THEN**error code保持唯一、fixtures与OpenAPI schema一致，既有编号和成功响应字段不发生breaking change
