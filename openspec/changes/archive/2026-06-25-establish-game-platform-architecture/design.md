## 背景

iHomeland 当前仍处于项目骨架阶段。早期目标是用 Go 构建在线游戏平台基础能力，并在未来对接 Unity 或 Godot 客户端。项目最终可能走向 MOBA/RTS 类玩法，但第一阶段必须避免把外围后台、房间大厅、实时网关和高频核心战斗服混在一个 change 中实现。

本 change 的定位是“架构基线”：统一项目认知、写清职责边界、建立长期文档和 OpenSpec 规格入口。代码实现型工作由后续更小的 changes 承担。

## 目标 / 非目标

**目标：**

- 建立 `docs/architecture.md`、`docs/engineering-standards.md`、`docs/roadmap.md` 等基础文档。
- 明确第一里程碑为“自定义房间大厅”。
- 明确第一里程碑暂不做匹配系统、高频战斗模拟、独立 battle server、跨服、观战、回放和完整持久化业务。
- 建立 `protocol`、`gateway`、`room`、`storage` 四类长期规格入口。
- 将后续实现拆成小而可验收的 OpenSpec change。

**非目标：**

- 本 change 不初始化 Go module。
- 本 change 不实现 Gin server。
- 本 change 不实现 WebSocket 网关。
- 本 change 不创建 Protobuf envelope。
- 本 change 不实现房间业务。
- 本 change 不接入 Redis 或 MySQL。
- 本 change 不拆分 gRPC 服务。

## 技术决策

### 当前 change 只做项目基线

本 change 不承担实现任务，只建立文档、长期规格和里程碑边界。这样可以避免 AI apply 时一次生成过多半成品文件。

备选方案是继续把服务端、协议、网关、房间和持久化放在一个 tasks.md 中。该方案看起来完整，但范围太大，容易产生不可运行或难维护的骨架代码。

### 第一里程碑固定为自定义房间大厅

第一里程碑只验证在线游戏最基础的闭环：启动、健康检查、版本接口、实时连接、Protobuf envelope、创建房间、加入房间、准备、退出、房主转移和断线重连。

备选方案是在第一里程碑尝试 MOBA/RTS 战斗同步。该方案会过早引入 tick、确定性、校正、反作弊和带宽预算问题，不适合项目启动阶段。

### 第一阶段不急于使用 gRPC

gateway、room、match 等模块第一阶段优先通过 Go interface 和 in-process adapter 解耦。等确实需要独立部署时，再通过单独 change 将边界升级为 gRPC。

备选方案是从第一天就拆成多个 gRPC 服务。该方案增加部署、配置、调试和测试复杂度，但早期收益有限。

### 长期路线放入 docs，系统行为放入 specs

`docs/roadmap.md` 记录项目阶段和 change 拆分；`openspec/specs/` 记录长期系统行为；`tasks.md` 只保留当前 change 可执行、可验收的动作。

## 风险 / 取舍

- 文档基线不直接产出功能 -> 缓解：后续 changes 已明确拆分，可从 `add-server-foundation` 开始快速实现。
- 主 specs 与 change specs 可能重复 -> 缓解：当前重复是为了建立长期 source of truth；归档后以主 specs 为准。
- 第一里程碑范围看起来偏小 -> 缓解：自定义房间大厅足以验证网关、协议、房间状态机和重连路径，是更稳的起点。

## 迁移计划

1. 完成并提交本架构基线 change。
2. 从 `add-server-foundation` 开始创建实现型 change。
3. 每个后续 change 只解决一个明确问题，完成后再归档并同步主 specs。
4. 在第一里程碑完成前，不创建 battle server 实现 change。

## 待定问题

- 第一个客户端原型选择 Unity 还是 Godot？
- `add-server-foundation` 是否先使用 `slog`，后续高吞吐场景再评估 `zap`？
- `add-protocol-envelope` 中 message id 使用枚举、注册表还是生成映射？
