## Why

当前项目已有服务端基础和协议边界规划，但缺少统一、可复现的本地基础设施启动方式。为后续 WebSocket gateway、房间大厅和持久化边界接入 Redis、MySQL 做准备，需要先把本地依赖、配置示例和启动验证固化下来。

## What Changes

- 新增本地开发基础设施约定，覆盖 Docker Compose 管理的 MySQL、Redis 和必要的服务健康检查。
- 新增本地配置示例，明确端口、数据库、Redis、日志等示例值来源，避免真实密钥进入仓库。
- 新增本地启动和验证脚本或命令入口，使开发者可以用固定流程启动依赖、运行服务并验证 `/healthz`、`/version`。
- 明确本 change 不接入房间持久化、不引入 Redis 作为持久事实来源、不实现匹配系统或 battle server。

## Capabilities

### New Capabilities

- `local-infra`: 本地开发基础设施的启动、配置示例、依赖健康检查和验证约定。

### Modified Capabilities

- `server-foundation`: 服务端启动和健康检查需要能够表达本地基础设施依赖状态。

## Impact

- 影响本地开发和测试环境，包括 Docker Compose、`.env.example`、本地配置文件和脚本入口。
- 影响服务端配置加载与健康检查输出，但不改变客户端协议、不新增业务 API。
- 引入或固化 MySQL、Redis 本地容器依赖，后续 `add-persistence-boundaries` 才负责业务读写接入。
