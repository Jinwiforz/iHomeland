# Account Session 规格

## Purpose

定义第一阶段账号注册、登录、登出、会话恢复和客户端账号状态的长期行为契约，确保房间大厅使用服务端确认的玩家身份。

## Requirements

### Requirement: 服务端必须提供第一阶段账号会话能力
MUST:服务端必须提供注册、登录、登出、会话恢复和当前身份查询能力，并将成功注册或登录的账号映射为稳定 `player_id`。

#### Scenario: 玩家注册成功
- **WHEN** Unity 客户端提交合法且未被占用的账号凭据
- **THEN** 服务端创建玩家基础资料，返回玩家资料、session token 和过期时间，并将连接 session 绑定到该玩家身份

#### Scenario: 玩家重复注册
- **WHEN** Unity 客户端提交已存在的账号名进行注册
- **THEN** 服务端拒绝注册并返回结构化错误，不覆盖已有玩家资料

#### Scenario: 玩家登录成功
- **WHEN** Unity 客户端提交合法账号凭据
- **THEN** 服务端返回玩家资料、session token 和过期时间，并将连接 session 绑定到该玩家身份

#### Scenario: 玩家凭据非法
- **WHEN** Unity 客户端提交空账号、空密码或错误凭据
- **THEN** 服务端拒绝登录并返回结构化错误，不创建有效 session

### Requirement: Session token 必须是不透明客户端凭证
MUST:客户端必须把 session token 作为不透明字符串保存和提交，不得依赖 token 内部结构表达业务语义。

#### Scenario: 客户端保存登录状态
- **WHEN** 登录响应包含 session token
- **THEN** Unity 客户端只保存 token、过期时间和玩家资料，不解析 token 内容

#### Scenario: token 过期或失效
- **WHEN** 客户端使用过期或已登出的 session token 恢复会话
- **THEN** 服务端拒绝恢复，客户端必须回到未登录状态

### Requirement: 登出必须失效当前账号会话
MUST:登出必须让当前 session token 失效，并清理 gateway connection 上绑定的玩家身份。

#### Scenario: 玩家主动登出
- **WHEN** 已登录玩家发送登出请求
- **THEN** 服务端删除或标记当前 session token 失效，并让连接 session 不再持有 `player_id`

#### Scenario: 登出后继续发送账号态请求
- **WHEN** 客户端登出后继续发送需要登录身份的请求
- **THEN** 服务端必须拒绝请求并返回结构化未登录或会话无效错误

### Requirement: Unity AccountSystem 必须以服务端身份为准
MUST:Unity `AccountSystem` 必须以服务端注册、登录、恢复和登出响应作为账号状态来源，不得把本地表单校验成功当作已登录。

#### Scenario: 登录页提交注册
- **WHEN** 玩家在 `LoginPage` 点击注册
- **THEN** 客户端必须发送注册请求并等待服务端成功响应后才能进入 `HomePage`

#### Scenario: 登录页提交账号密码
- **WHEN** 玩家在 `LoginPage` 点击登录
- **THEN** 客户端必须发送登录请求并等待服务端成功响应后才能进入 `HomePage`

#### Scenario: 服务端登录失败
- **WHEN** 服务端返回登录失败错误
- **THEN** `AccountSystem` 必须保持未登录状态，`LoginPage` 显示或记录失败原因

### Requirement: 账号会话不得扩大第一里程碑范围
MUST:账号会话只为自定义房间大厅提供身份基础，不得实现密码找回、第三方登录、复杂权限、匹配、经济、战绩、好友、观战、回放或 battle server。

#### Scenario: 需求需要完整用户系统
- **WHEN** 新需求要求密码找回、第三方登录、好友或完整权限体系
- **THEN** 项目必须创建单独 OpenSpec change，不得混入账号会话边界

### Requirement: 账号会话身份必须作为后续房间请求身份来源
MUST:注册、登录或会话恢复成功后，服务端绑定到 gateway connection 的玩家身份必须成为该 connection 后续房间大厅请求的身份来源；客户端声明的 `player_id` 不得覆盖服务端绑定身份。

#### Scenario: 登录成功后进入房间大厅
- **WHEN** 玩家通过注册、登录或会话恢复成功绑定 connection 身份
- **THEN** 后续房间大厅请求必须以该绑定玩家身份作为服务端授权依据

#### Scenario: 登出后发送房间请求
- **WHEN** 玩家登出导致 connection session 清理玩家身份后继续发送房间大厅请求
- **THEN** 服务端必须拒绝请求并返回结构化 `UNAUTHENTICATED` 错误
