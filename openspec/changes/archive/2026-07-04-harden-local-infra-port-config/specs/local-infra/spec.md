## ADDED Requirements

### Requirement: 本地基础设施必须使用项目级端口治理

本地基础设施 MUST 支持通过项目配置声明 MySQL 和 Redis 的宿主机端口，并 MUST 提供避开常见系统保留端口和本机服务冲突的默认开发端口。

#### Scenario: 使用项目默认宿主机端口

- **WHEN** 开发者未提供本机端口覆盖配置并启动本地基础设施
- **THEN** Docker Compose MUST 将 MySQL 暴露到项目默认 MySQL 宿主机端口，并将 Redis 暴露到项目默认 Redis 宿主机端口

#### Scenario: 使用本机端口覆盖配置

- **WHEN** 开发者在本机配置中声明 MySQL 或 Redis 宿主机端口
- **THEN** Docker Compose MUST 使用本机配置声明的宿主机端口暴露对应依赖

### Requirement: 本地脚本必须共享同一份本机配置

本地基础设施启动脚本、服务端启动脚本和本地验证脚本 MUST 在执行前加载同一份本机私有配置文件，并 MUST 允许当前 shell 中已存在的环境变量覆盖本机配置文件中的值。

#### Scenario: 脚本读取一致配置

- **WHEN** 开发者在本机配置中声明 Redis 宿主机端口和服务端 Redis 地址
- **THEN** 基础设施启动脚本 MUST 使用该端口暴露 Redis，服务端启动脚本和验证脚本 MUST 连接该 Redis 地址

#### Scenario: 临时环境变量覆盖本机配置

- **WHEN** 当前 shell 已经声明与本机配置同名的环境变量
- **THEN** 本地脚本 MUST 保留当前 shell 的环境变量值，不得用本机配置覆盖

### Requirement: 本地环境初始化必须诊断端口不可用原因

项目 MUST 提供本地环境初始化或诊断入口，用于生成本机私有配置，并检查 Docker、Go、OpenSpec、端口占用和 Windows TCP excluded port range。

#### Scenario: 生成本机私有配置

- **WHEN** 开发者执行本地环境初始化入口且本机私有配置不存在
- **THEN** 初始化入口 MUST 生成不包含真实生产密钥的本机私有配置文件

#### Scenario: 端口被系统排除范围覆盖

- **WHEN** 本机配置声明的宿主机端口落入 Windows TCP excluded port range
- **THEN** 诊断入口 MUST 报告该端口不可用于 Docker 绑定，并提示开发者修改项目本机配置而不是修改系统端口保留表

#### Scenario: 端口被本机进程占用

- **WHEN** 本机配置声明的宿主机端口已经被本机进程监听
- **THEN** 诊断入口 MUST 报告端口占用，并提示开发者修改项目本机配置或停止占用端口的本机服务
