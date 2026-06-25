# iHomeland

iHomeland 是一个计划使用 Go 后端搭配 Unity 或 Godot 客户端开发的在线游戏项目。

早期目标是搭建外围后台、实时网关、自研房间逻辑和小中规模房间服能力。后续如果进入 MOBA/RTS 核心战斗服，需要单独设计高频权威战斗服务器架构。

当前前期企划和架构标准：

- `AGENTS.md`
- `docs/architecture.md`
- `docs/engineering-standards.md`
- `docs/workflow.md`
- `docs/roadmap.md`
- `docs/file-structure.md`
- `docs/protocol-compatibility.md`
- `docs/redis-keys.md`
- `openspec/specs/`
- `openspec/changes/archive/2026-06-25-establish-game-platform-architecture/`

第一里程碑固定为“自定义房间大厅”。当前阶段暂不实现匹配系统、MOBA/RTS 高频战斗模拟、独立 battle server、跨服、观战和回放。

## 本地开发入口

当前仓库处于架构基线阶段，尚未创建可运行的 Go 服务端。下一步实现型 change 是 `add-server-foundation`，它会添加 Go module、服务端入口、配置、日志、Gin HTTP server、`/healthz`、`/readyz` 和 `/version`。

在 `add-server-foundation` 完成前，本地开发请先阅读：

- `docs/roadmap.md`
- `docs/file-structure.md`
- `openspec/specs/protocol/spec.md`
- `openspec/specs/gateway/spec.md`
- `openspec/specs/room/spec.md`
- `openspec/specs/storage/spec.md`
