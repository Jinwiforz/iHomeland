# iHomeland

iHomeland 是一个使用 Go 服务端搭配 Unity 客户端开发的在线游戏项目。

项目第一阶段聚焦“自定义房间大厅”：先建立外围后台、实时网关、自研房间逻辑和小中规模房间服能力。后续如果进入 MOBA/RTS 核心战斗服，需要单独设计高频权威战斗服务器架构，不直接把当前房间服扩展成《英雄联盟》级别的核心战斗服。

当前服务端已经具备第一阶段房间大厅的基础闭环：WebSocket 实时入口、Protobuf envelope、注册、登录、登出、会话恢复、gateway 身份绑定、房间请求身份校验、创建/加入房间、准备、退出、房主转移、断线保留和重连恢复。Unity 客户端已经具备基础应用链路、真实账号会话、Unity Editor WebSocket/account smoke test、房间请求、`RoomSystem`、`RoomPage` 和 `RoomPage.prefab`，可完成房间大厅端到端联调。

下一步优先推进 `add-room-start-gate`，在房间大厅内定义进入后续占位场景的开始闸门。第一里程碑完成前暂不推进正式战斗模块。

## 项目边界

当前阶段允许推进：

- 服务端基础设施、配置、日志、健康检查和版本接口
- WebSocket 实时连接基础能力，TCP 传输由后续 change 评估
- Protobuf 协议 envelope 和协议兼容规则
- 自定义房间的创建、加入、准备、退出、房主转移和断线重连
- 第一阶段账号会话、注册、登录登出和房间大厅所需玩家身份
- 房间大厅请求必须使用服务端确认的 gateway session 玩家身份，客户端 payload `player_id` 只作为声明并必须与 session 身份一致
- Redis、MySQL、gRPC、Docker 等后续基础能力

当前阶段暂不实现：

- 匹配系统
- MOBA/RTS 高频权威战斗模拟
- 独立 battle server
- 从 HomePage 直接进入正式战斗玩法
- 跨服、观战、回放和完整经济系统

## 顶层目录

```text
client/      Unity 客户端工程或客户端版本信息
docs/        项目文档、架构、路线图、规范
openspec/    需求、设计、规格和变更任务
server/      Go 服务端模块和本地服务端基础设施配置
shared/      跨端共享协议和生成配置
tools/       开发、生成、构建和运维辅助工具
```

## 必读文档

- `AGENTS.md`：项目协作、语言、代码质量和流程硬规则
- `docs/architecture.md`：总体架构
- `docs/engineering-standards.md`：工程标准
- `docs/workflow.md`：项目流程规范
- `docs/roadmap.md`：路线图和 change 拆分
- `docs/file-structure.md`：文件结构规划
- `docs/client-integration.md`：Unity 客户端接入说明
- `docs/protocol-compatibility.md`：协议兼容规则
- `docs/redis-keys.md`：Redis key 规则
- `openspec/specs/`：长期行为契约

## 模块入口

- 服务端开发、运行、测试和配置说明见 `server/README.md`
- Unity 客户端接入、协议生成和最小联调说明见 `docs/client-integration.md`
- 本地 MySQL/Redis 推荐先运行 `server/scripts/setup-local-env.bat` 生成本机配置；脚本支持自动选择 Docker 或本机安装服务，验证以服务端实际配置地址是否可连接为准
- 服务端测试入口会自动准备项目本地 Protobuf 生成工具链，并在测试前重新生成协议代码
- 架构或跨模块变更必须先通过 OpenSpec change 描述清楚，再进入实现

## OpenSpec

当前主要长期规格位于：

- `openspec/specs/server-foundation/spec.md`
- `openspec/specs/local-infra/spec.md`
- `openspec/specs/protocol/spec.md`
- `openspec/specs/gateway/spec.md`
- `openspec/specs/account-session/spec.md`
- `openspec/specs/room/spec.md`
- `openspec/specs/storage/spec.md`
- `openspec/specs/client-integration/spec.md`

当前 active change 以 `openspec list` 为准；已完成 change 归档在 `openspec/changes/archive/`。
