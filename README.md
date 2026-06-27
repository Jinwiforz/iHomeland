# iHomeland

iHomeland 是一个计划使用 Go 服务端搭配 Unity 或 Godot 客户端开发的在线游戏项目。

项目第一阶段聚焦“自定义房间大厅”：先建立外围后台、实时网关、自研房间逻辑和小中规模房间服能力。后续如果进入 MOBA/RTS 核心战斗服，需要单独设计高频权威战斗服务器架构，不直接把当前房间服扩展成《英雄联盟》级别的核心战斗服。

## 项目边界

当前阶段允许推进：

- 服务端基础设施、配置、日志、健康检查和版本接口
- WebSocket / TCP 实时连接能力
- Protobuf 协议 envelope 和协议兼容规则
- 自定义房间的创建、加入、准备、退出、房主转移和断线重连
- Redis、MySQL、gRPC、Docker 等后续基础能力

当前阶段暂不实现：

- 匹配系统
- MOBA/RTS 高频权威战斗模拟
- 独立 battle server
- 跨服、观战、回放和完整经济系统

## 顶层目录

```text
client/      客户端工程或客户端版本信息
docs/        项目文档、架构、路线图、规范
openspec/    需求、设计、规格和变更任务
server/      Go 服务端模块
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
- `docs/protocol-compatibility.md`：协议兼容规则
- `docs/redis-keys.md`：Redis key 规则
- `openspec/specs/`：长期行为契约

## 模块入口

- 服务端开发、运行、测试和配置说明见 `server/README.md`
- 架构或跨模块变更必须先通过 OpenSpec change 描述清楚，再进入实现

## OpenSpec

当前主要长期规格位于：

- `openspec/specs/protocol/spec.md`
- `openspec/specs/gateway/spec.md`
- `openspec/specs/room/spec.md`
- `openspec/specs/storage/spec.md`
