## Context

iHomeland 第一里程碑需要先完成可运行的服务端基础、实时连接和自定义房间大厅。当前仓库已经规划 MySQL 作为持久事实来源、Redis 作为短期运行态缓存，但本地开发环境尚未有统一的依赖启动、示例配置和依赖健康验证入口。

本 change 只解决本地基础设施可复现问题，为后续 `add-websocket-gateway`、`add-room-lobby` 和 `add-persistence-boundaries` 降低环境摩擦。它不把业务状态迁移到 Redis 或 MySQL，也不引入独立 battle server、匹配系统或跨服能力。

## Goals / Non-Goals

**Goals:**

- 提供 Docker Compose 管理的本地 MySQL、Redis 依赖。
- 提供不含真实密钥的 `.env.example` 和本地配置示例。
- 让服务端配置能够表达 MySQL、Redis 连接参数，并在健康/就绪检查中输出依赖状态。
- 提供固定的本地启动、停止、验证入口，便于开发者和 CI 风格的本地检查复用。

**Non-Goals:**

- 不实现房间、玩家或对局数据的 MySQL 持久化读写。
- 不定义新的业务 Redis key；涉及业务 key 的 owner、TTL、恢复路径由后续持久化边界 change 处理。
- 不引入 Kubernetes、生产部署拓扑、云数据库或 secret 管理系统。
- 不改变客户端 Protobuf 协议和实时通信 envelope。

## Decisions

1. 使用 `docker compose` 作为本地依赖编排入口。

   理由：项目第一阶段只需要单机可复现的 MySQL、Redis 环境，Compose 足以表达镜像、端口、数据卷和健康检查。替代方案是手写本机安装文档或引入 Kubernetes，本机安装不可复现，Kubernetes 对第一里程碑过重。

2. Compose 只管理基础设施依赖，Go 服务端仍通过本机命令或脚本运行。

   理由：早期调试需要快速运行 `go test`、热启动和查看本地日志，把服务端放入容器会增加构建与调试成本。替代方案是将服务端一并容器化，但当前没有生产镜像发布目标，收益不足。

3. 本地配置采用示例文件加环境变量覆盖。

   理由：`server-foundation` 已要求配置支持本地文件、环境变量覆盖和校验。本 change 在此基础上补充 MySQL、Redis 示例字段，真实本地覆盖值应通过未提交的 `.env` 或环境变量提供。替代方案是只使用硬编码默认值，但会违反配置规则并削弱端口、账号调整能力。

4. `/healthz` 继续表达进程存活，`/readyz` 表达基础依赖是否可用。

   理由：存活检查不应因为 MySQL 或 Redis 短暂不可用而误判进程死亡；就绪检查更适合阻止依赖不可用时接收请求。替代方案是让 `/healthz` 同时检查依赖，但会混淆进程存活和服务可用性。

5. 依赖探测通过小接口封装，并保留无依赖或禁用探测的本地测试路径。

   理由：服务端基础测试不应强依赖真实 Docker 环境；配置、handler 和探测逻辑需要能通过 fake checker 做单元测试。替代方案是在 handler 中直接连接 MySQL、Redis，会让测试变慢且边界不清晰。

## Risks / Trade-offs

- [Risk] 本地端口与开发者机器已有服务冲突。 → 在 `.env.example` 和本地配置中集中列出端口，允许环境变量覆盖。
- [Risk] 就绪检查依赖真实外部服务后导致单元测试不稳定。 → 通过 checker interface 注入 fake 实现，真实连接只在集成验证或手动脚本中执行。
- [Risk] Redis 被误用为持久事实来源。 → 本 change 不新增业务 Redis key；文档和配置只声明 Redis 是短期运行态依赖。
- [Risk] MySQL 初始化脚本过早绑定业务 schema。 → 只创建本地数据库和必要用户，不创建房间或对局业务表；业务 schema 留给持久化边界 change。
