## ADDED Requirements

### Requirement: 服务端必须可启动
服务端必须提供一个 Go 应用入口，能够加载配置、初始化日志、注册 HTTP 基础接口并启动 HTTP server。

#### Scenario: 启动服务端
- **WHEN** 开发者运行服务端入口
- **THEN** 服务端启动 HTTP server 并监听配置指定地址

### Requirement: 配置必须支持本地文件、环境变量覆盖和校验
服务端配置必须提供默认值、本地配置文件、环境变量覆盖和必要字段校验。

#### Scenario: 未提供环境变量
- **WHEN** 服务端在未设置环境变量的本地环境启动
- **THEN** 服务端读取默认本地配置文件或使用内置默认值

#### Scenario: 环境变量覆盖配置文件
- **WHEN** 本地配置文件和环境变量同时提供 HTTP 地址
- **THEN** 服务端优先使用环境变量中的 HTTP 地址

#### Scenario: 配置值非法
- **WHEN** 配置包含非法 HTTP 地址或不支持的日志等级
- **THEN** 服务端返回配置校验错误并拒绝启动

### Requirement: 日志必须通过项目封装初始化
服务端必须通过项目级 logger 包初始化结构化日志，业务代码不得直接散落日志实现细节。

#### Scenario: 初始化日志
- **WHEN** 服务端启动
- **THEN** logger 包根据配置创建结构化 logger

### Requirement: 服务端必须提供健康检查
服务端必须提供 `/healthz` 接口，用于报告进程存活状态。

#### Scenario: 调用 healthz
- **WHEN** 客户端请求 `/healthz`
- **THEN** 服务端返回成功状态和机器可读响应

### Requirement: 服务端必须提供就绪检查
服务端必须提供 `/readyz` 接口，用于报告当前进程是否具备处理基础 HTTP 请求的能力。

#### Scenario: 调用 readyz
- **WHEN** 客户端请求 `/readyz`
- **THEN** 服务端返回当前就绪状态和机器可读响应

### Requirement: 服务端必须提供版本接口
服务端必须提供 `/version` 接口，返回 release、server、client 和 protocol 版本元数据。

#### Scenario: 调用 version
- **WHEN** 客户端请求 `/version`
- **THEN** 服务端返回 release、server、client、protocol、build number、commit 和 build time 等版本信息

### Requirement: 基础接口必须可测试
配置加载、健康检查、就绪检查和版本接口必须有自动化测试覆盖。

#### Scenario: 运行服务端基础测试
- **WHEN** 开发者运行服务端测试
- **THEN** 配置默认值、健康检查、就绪检查和版本响应测试通过
