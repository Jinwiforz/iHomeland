## 1. 本机配置与端口默认值

- [x] 1.1 创建或更新 `server/.env.example`，声明项目专用本地端口、服务端连接地址和安全示例值。
- [x] 1.2 更新 `server/compose.yaml` 默认宿主机端口为项目专用端口，并保留环境变量覆盖能力。
- [x] 1.3 更新 `server/config/local.yaml` 中 MySQL 和 Redis 本地连接地址，使其匹配项目专用端口。

## 2. 本地脚本配置加载

- [x] 2.1 更新 `start-local-infra.bat`，在执行 Docker Compose 前加载 `server/.env.local`。
- [x] 2.2 更新 `run.bat`，在启动 Go 服务端前加载 `server/.env.local`。
- [x] 2.3 更新 `verify-local.bat`，在检查依赖和 HTTP 接口前加载 `server/.env.local`。

## 3. 本地环境初始化与诊断

- [x] 3.1 新增 `setup-local-env.bat`，在 `server/.env.local` 不存在时根据 `server/.env.example` 生成本机配置。
- [x] 3.2 在 `setup-local-env.bat` 中检查 Docker、Go 和 OpenSpec CLI 可用性。
- [x] 3.3 在 `setup-local-env.bat` 中诊断配置端口是否被本机进程占用或落入 Windows TCP excluded port range。

## 4. 文档与长期规格

- [x] 4.1 更新 `server/README.md`，说明推荐的本地初始化、启动、运行和验证流程。
- [x] 4.2 更新 `docs/file-structure.md`，记录 `server/.env.local` 和 `setup-local-env.bat` 的职责。

## 5. 验证

- [x] 5.1 运行 `openspec.cmd status --change harden-local-infra-port-config`，确认 artifacts 完整。
- [x] 5.2 运行 `server/scripts/setup-local-env.bat`，确认本机配置生成和诊断流程可执行。
- [ ] 5.3 运行服务端相关测试，确认脚本改动未破坏现有 Go 测试入口。

验证记录：当前机器 `where.exe go` 找不到 Go，`server/scripts/test.bat` 因缺少 Go 工具链停止，待安装 Go 或修复 PATH 后补跑。
