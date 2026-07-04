## Why

当前本地基础设施默认暴露 Redis 到宿主机 `6379`，在 Windows、WSL、Hyper-V 或安全软件管理的端口排除范围内可能无法绑定，导致新电脑即使没有安装 Redis 也无法启动 Docker Compose。项目需要把本地端口选择、配置覆盖和诊断流程固化，避免开发者手动修改系统端口或临时记忆环境变量。

## What Changes

- 将本地 Docker Compose 的宿主机端口治理升级为项目约定，默认使用项目专用端口，降低与系统保留端口和本机服务冲突的概率。
- 新增本机私有环境配置入口，确保 Docker Compose、服务端启动脚本和本地验证脚本读取同一份本地配置。
- 新增本地环境初始化/诊断脚本，用于检查 Docker、Go、OpenSpec、端口占用和 Windows TCP excluded port range，并生成安全的本机配置。
- 更新本地基础设施文档，明确推荐入口、端口覆盖方式和不建议修改系统端口的处理原则。
- 不改变业务协议、不新增业务 Redis key、不接入真实 MySQL/Redis 业务读写、不实现匹配系统或 battle server。

## Capabilities

### New Capabilities

- 无。

### Modified Capabilities

- `local-infra`: 本地基础设施必须具备端口冲突诊断、本机私有配置生成，以及脚本间一致读取本地配置的能力。

## Impact

- 影响 `server/compose.yaml`、`server/.env.example`、`server/scripts` 下本地启动/运行/验证脚本和新增本地诊断脚本。
- 影响 `server/README.md`、`docs/file-structure.md` 和 `openspec/specs/local-infra/spec.md` 中对本地基础设施入口的说明。
- 不影响客户端协议、服务端业务 API、房间状态机、MySQL schema 或 Redis key 规则。
