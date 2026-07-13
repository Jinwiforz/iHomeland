# 服务端契约规格

## Purpose

定义 HTTPS 与实时消息的唯一机器可读契约、编号和路由治理、统一可靠 envelope、身份边界、版本目录以及跨端可重复生成与 fixtures 验收行为。

## Requirements

### Requirement: 契约源必须具有唯一所有权
系统 MUST 使用 `versions.yaml` 选定的 Protobuf edition 定义实时二进制 payload、使用该目录选定的 OpenAPI 规范定义 HTTPS JSON API，并使用机器可读 registries 保存编号、错误与路由元数据；registry 只能引用 schema symbol，不能复制字段结构。每个 HTTP operation MUST 声明稳定 operation id、body limit、timeout 与幂等语义。

#### Scenario: 同一 payload 被重复定义
- **WHEN** 一个 HTTP 或实时 payload 同时在两个契约源中维护独立字段定义
- **THEN** 契约验证失败并指出重复 owner，生成流程不得继续

### Requirement: v1 契约必须保持第一里程碑范围
基础 contract MUST 覆盖 version/config、账号注册登录与 session、WSS control、通用 gameplay connection ticket 和 endpoint manifest，并 MUST NOT 提前创建 PersonalWorld、VisitSession、ActivityInstance、Room、Party、battle、UDP/KCP、资产、奖励或结算消息。业务领域协议 MUST 在对应 domain/application 语义通过独立测试后由专属 change 增量登记。

#### Scenario: 基础契约引入业务消息
- **WHEN** 基础 schema 或 registry 出现 world、visit、room、party、activity 或 battle message
- **THEN** 范围验证失败，要求由具有明确 domain owner、授权和验收场景的业务协议 change 引入

### Requirement: 编号与错误目录必须稳定且可验证
每个实时 message 和稳定 error MUST 具有全局唯一数值编号、symbolic name 与 owner；已分配或删除的编号 MUST NOT 被复用，error entry 必须定义 category、安全 message key、默认 retryable 和适用的 HTTP status。

#### Scenario: 编号重复或复用
- **WHEN** 两个 symbol 使用同一编号，或新 symbol 使用 reserved 编号
- **THEN** registry 验证失败并报告冲突项

### Requirement: 实时消息必须登记唯一路由
每个实时 message MUST 登记 direction、唯一 allowed channel、auth scope、QoS、完整 encoded envelope max size、rate limit policy、idempotency 与 timeout/expiry，并引用存在的 Protobuf full name。

#### Scenario: Route 引用错误通道或未知消息
- **WHEN** route 使用与消息职责不符的 channel，或引用不存在的 message symbol
- **THEN** validator 拒绝 registry，不能构建 dispatcher 内存 projection

### Requirement: 可靠消息必须使用统一 envelope 与明确 framing
WSS 与 TLS/TCP payload MUST 使用统一可靠 envelope，表达 protocol version、message id、kind、必要 request/command id、sequence、timestamp 与 payload；TLS/TCP MUST 使用 4-byte unsigned big-endian length prefix，并拒绝零长度、截断和超过 1 MiB 的 frame。

#### Scenario: TCP 输入包含半帧与连续帧
- **WHEN** codec 分多次读取一个 frame，随后单次读取包含多个完整 frame
- **THEN** codec 按长度前缀准确重组全部 envelope，且不把 socket read 边界视为消息边界

### Requirement: 身份与 ticket 契约必须阻止 payload 越权
Connection ticket MUST 绑定 session id、session epoch、target channel、endpoint、auth scopes、one-time nonce 与 expiry；WSS MUST 只使用 control scope，TLS/TCP MUST 只使用通用 gameplay scope。Gameplay scope MUST NOT 代表 PersonalWorld、VisitSession、ActivityInstance、Room 或奖励 mutation 授权；实时 command MUST NOT 携带可覆盖 AuthContext 或 admission 的操作者身份字段。

#### Scenario: Gameplay command 声明操作者 player id
- **WHEN** command schema 新增可作为授权依据的 actor/player id
- **THEN** 身份边界验证失败，要求服务端从 AuthContext、admission 与 application policy 取得操作者和目标资格

### Requirement: 协议生成必须可重复且按阶段交付
仓库 MUST 以根目录 `versions.yaml` 统一治理技术版本，并与产品发布版本分离。工具链 MUST 使用目录锁定的 Buf、Protobuf runtime/generators、OpenAPI validator 与 Go dependencies，且 Buf MUST 是唯一公开 schema 治理与 generation orchestration 入口；受管 `protoc` 只能作为 C# 后端。项目自有 `.proto` MUST 使用目录选定的 edition。服务端编译前 MUST 从已提交 schema 重建 Go code；客户端协议阶段 MUST 在 Unity 编译前重建 C# code。Generated code、descriptor 与可推导 projection MUST NOT 进入 Git；registry 与 fixtures/golden MUST 作为协议源和兼容性基线进入 Git。S0 MUST 编译 Go protocol code，但只能在临时目录验证 C# template。

#### Scenario: Clean checkout 重复生成
- **WHEN** 从不包含 generated code 的 clean checkout 连续执行两次 protocol generation，并验证版本化 fixtures
- **THEN** 两次生成摘要一致，Go generated code 编译，C# 临时输出被清理，且 tracked files 不产生未确认漂移

#### Scenario: 生态配置偏离版本目录
- **WHEN** 工具、schema、`go.mod`、Unity 配置或容器镜像声明的版本与 `versions.yaml` 不一致
- **THEN** 验证流程失败并指出偏离项，不得提交不完整升级或静默降级目标规范

#### Scenario: 主动升级技术版本
- **WHEN** 项目采用新的正式版本
- **THEN** 同一变更更新版本目录、所有必要生态声明、生成配置、fixtures 和兼容性验证，并保持旧提交仍可按其锁定目录重复构建

### Requirement: Fixtures 必须验证正向与拒绝行为
Contract fixtures MUST 覆盖 HTTPS request/response/error、control push、gameplay ticket 与可靠 envelope deterministic packets，并使用 negative fixture manifest 固定未知 message、错误 channel、无效 kind/id、超长/截断 frame 和未知 enum 等拒绝原因；对应 contract/codec tests MUST 执行真实拒绝行为。业务 request/response/error/push golden MUST 由对应业务协议 change 添加。Fixtures MUST NOT 包含真实凭据或密钥。

#### Scenario: Go 验证全部基础 golden packets
- **WHEN** Go contract test 读取版本化基础 golden packet 清单
- **THEN** 每个 packet 都能完成 registry lookup 或明确的通用 envelope decode、deterministic re-encode 与摘要校验，negative manifest 中每个原因都有对应的真实拒绝测试

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
