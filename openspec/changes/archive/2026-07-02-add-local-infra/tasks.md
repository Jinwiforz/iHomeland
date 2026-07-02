## 1. 本地基础设施文件

- [x] 1.1 创建根目录 `compose.yaml`，定义 MySQL、Redis 服务、固定开发端口、命名 volume 和健康检查。
- [x] 1.2 创建根目录 `.env.example`，列出 HTTP、MySQL、Redis、日志等级等本地示例配置，且不包含真实密钥。
- [x] 1.3 更新 `server/config/local.yaml`，补充 MySQL、Redis 本地连接示例值。

## 2. 服务端配置与依赖探测

- [x] 2.1 扩展 `server/internal/config` 配置结构，加入 MySQL、Redis 字段、环境变量覆盖和非法地址校验。
- [x] 2.2 为 MySQL、Redis 配置默认值、环境变量覆盖和非法值补充单元测试。
- [x] 2.3 新增基础设施依赖 checker 接口和实现，用于探测 MySQL、Redis 连接状态。
- [x] 2.4 为依赖 checker 增加 fake 实现或测试替身，保证基础接口单元测试不依赖真实 Docker 环境。

## 3. 健康与就绪接口

- [x] 3.1 保持 `/healthz` 只报告进程存活，不因 MySQL 或 Redis 不可用而失败。
- [x] 3.2 扩展 `/readyz` 响应，包含 MySQL、Redis 依赖状态和失败依赖名称。
- [x] 3.3 更新 `server/internal/ops` 测试，覆盖依赖全部可用、MySQL 不可用、Redis 不可用和 `/healthz` 独立存活场景。

## 4. 本地脚本与验证

- [x] 4.1 新增或更新 `server/scripts` 本地基础设施启动脚本，调用 `docker compose up -d` 并等待依赖 healthy。
- [x] 4.2 新增或更新 `server/scripts` 本地基础设施停止脚本，调用 `docker compose down` 且默认保留开发数据卷。
- [x] 4.3 新增本地验证脚本，检查服务端实际配置的 MySQL、Redis 地址，并验证 `/healthz`、`/readyz`、`/version` 响应。
- [x] 4.4 更新 `server/README.md` 或项目入口文档中的本地启动步骤，指向新的 Compose、配置示例和验证脚本。

## 5. 验收

- [x] 5.1 运行 `go test ./...`，确认服务端配置、ops 接口和依赖状态测试通过。
- [x] 5.2 运行本地基础设施启动脚本，确认 MySQL、Redis 容器进入 healthy 状态。
- [x] 5.3 在本地依赖正常运行时启动服务端并运行验证脚本，确认 `/healthz`、`/readyz`、`/version` 验证通过。
- [x] 5.4 停止 Redis 后再次运行验证脚本，确认 `/readyz` 和验证脚本能报告 Redis 不可用。
