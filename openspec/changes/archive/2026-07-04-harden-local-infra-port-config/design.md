## Context

本地基础设施已经由 `server/compose.yaml` 管理 MySQL 和 Redis，并由 `start-local-infra.bat` 等脚本启动和验证。现有默认宿主机端口使用 `3306` 和 `6379`，其中 `6379` 在部分 Windows 新机器上会落入 TCP excluded port range，Docker 无法绑定，错误表现为权限禁止而不是端口占用。

这个问题属于本地开发流程和基础设施边界，不应要求开发者修改 Windows 系统端口保留表，也不应依赖每次手动设置临时环境变量。项目需要一个可复现、可诊断、可覆盖的本机配置入口，让 Docker Compose、服务端启动和验证脚本读取同一份配置。

## Goals / Non-Goals

**Goals:**

- 使用项目专用宿主机端口作为本地默认值，降低与系统保留端口和本机安装服务冲突的概率。
- 提供 `server/.env.local` 作为本机私有配置文件，并继续通过 `.gitignore` 排除真实本机配置。
- 提供 `setup-local-env.bat`，自动生成安全默认值并诊断 Docker、Go、OpenSpec 和端口可用性。
- 让 `start-local-infra.bat`、`run.bat`、`verify-local.bat` 统一加载 `server/.env.local`。
- 在诊断输出中明确区分端口被进程占用和端口落入 Windows excluded port range。

**Non-Goals:**

- 不修改 Windows 系统端口保留范围，不执行需要管理员权限的网络配置命令。
- 不改变容器内部端口；MySQL 容器内仍为 `3306`，Redis 容器内仍为 `6379`。
- 不新增业务 Redis key、MySQL 表、协议消息或服务接口。
- 不把 Go 服务端容器化，不引入 Kubernetes、secret 管理或生产部署流程。

## Decisions

1. 默认宿主机端口改为 `3306` 和 `6379`。

   理由：容器内部继续使用标准端口，宿主机使用项目专用端口，能避开开发者本机已有 MySQL/Redis 和常见系统保留段。替代方案是继续使用 `3306/6379` 并让开发者手动处理冲突，但这会把团队流程问题转嫁到个人机器。

2. 使用 `server/.env.local` 作为脚本间共享的本机配置来源。

   理由：Docker Compose 需要 `IHOMELAND_MYSQL_PORT` 和 `IHOMELAND_REDIS_PORT`，服务端和验证脚本需要 `IHOMELAND_MYSQL_ADDR` 和 `IHOMELAND_REDIS_ADDR`。把这些值放在同一个未提交文件中，可以保证端口映射和连接地址一致。替代方案是写入用户级环境变量，但这会污染全局环境，且不利于多个项目并存。

3. `setup-local-env.bat` 只生成和诊断，不启动服务。

   理由：初始化配置和启动依赖是两个不同动作。拆开后，开发者可以先修复端口问题，再启动容器；CI 或未来自动化也能复用诊断逻辑。替代方案是在 `start-local-infra.bat` 中隐式生成所有配置，但隐式副作用更难排查。

4. 诊断脚本检测 Windows excluded port range，但不尝试修复系统范围。

   理由：修改 excluded port range 往往需要管理员权限，并可能影响 Hyper-V、WSL、Docker Desktop 或企业安全策略。项目脚本应给出清楚结论和项目内修复方式，而不是越权修改系统。替代方案是执行 `netsh` 删除或预留端口，但风险和权限成本都高。

5. 保持环境变量最高优先级。

   理由：开发者或 CI 可以临时覆盖端口和地址；`.env.local` 是日常默认，本地 shell 环境仍可在需要时覆盖。替代方案是 `.env.local` 覆盖一切，但会削弱临时调试能力。

## Risks / Trade-offs

- [Risk] `3306` 或 `6379` 仍可能在个别机器上被占用或被系统保留。-> Mitigation：`setup-local-env.bat` 会诊断端口占用和 excluded range，并提示修改 `server/.env.local` 到其它端口。
- [Risk] Windows 批处理解析 `.env.local` 时遇到复杂字符可能行为不一致。-> Mitigation：`.env.local` 仅支持简单 `KEY=value` 本地配置，不用于保存复杂 secret。
- [Risk] 已经依赖 `127.0.0.1:6379` 的开发者需要更新本机配置。-> Mitigation：默认配置和文档统一改为 `6379`，环境变量仍允许临时兼容旧端口。
- [Risk] OpenSpec CLI 在 PowerShell 下可能被执行策略拦截 `.ps1`。-> Mitigation：文档和诊断建议使用 `openspec.cmd` 进行 Windows 本地检查，不要求修改执行策略。

## Migration Plan

1. 新增 `server/.env.example` 和 `setup-local-env.bat`，生成默认 `server/.env.local`。
2. 更新本地脚本，使其在启动、运行和验证前加载 `server/.env.local`。
3. 更新 Compose 默认宿主机端口为 `3306/6379`。
4. 更新服务端本地配置示例和文档，指向项目专用端口。
5. 对已有本地容器执行 `docker compose down` 后重新 `start-local-infra.bat`，使新端口映射生效。

## Open Questions

无。
