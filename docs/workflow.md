# 项目流程规范

## 文档职责

项目文档按职责分层维护，避免同一规则在多个文件重复展开。

- `README.md`：项目入口，只写项目定位、当前阶段、关键文档入口和下一步。
- `AGENTS.md`：协作代理硬规则，只写必须遵守的约束和文档索引。
- `docs/architecture.md`：系统架构、边界和第一阶段职责。
- `docs/engineering-standards.md`：工程质量、Go 命名、注释、测试、日志、配置和错误处理标准。
- `docs/file-structure.md`：目录结构和文件归属。
- `docs/roadmap.md`：阶段路线和后续 OpenSpec change 拆分。
- `docs/protocol-compatibility.md`：协议兼容和版本策略。
- `docs/redis-keys.md`：Redis key、TTL、owner 和恢复规则。
- `docs/workflow.md`：项目流程、变更流转、验收和归档规则。

## OpenSpec 流程

所有跨模块、架构、协议、房间、存储或流程变更必须先进入 OpenSpec。

标准流程：

1. 提案：创建 change，说明为什么做、做什么、不做什么。
2. 设计：记录关键技术决策、边界、风险和替代方案。
3. 规格：把长期系统行为写入 `specs/`，每个 requirement 必须有 scenario。
4. 任务：把实现拆成可执行、可验收的小任务。
5. 实现：逐项执行任务，每完成一项立即勾选。
6. 同步：确认 delta spec 已同步到主 `openspec/specs/`。
7. 归档：所有 artifacts 和 tasks 完成后，将 change 移入 `openspec/changes/archive/`。

## Change 拆分规则

一个 OpenSpec change 只解决一个明确问题。

应该拆分的信号：

- 同时涉及协议、网关、房间、存储、部署中的两个以上方向。
- 一个任务会生成大量文件。
- 任务无法在一次集中评审中说清验收标准。
- 实现需要先做多个未确定技术决策。
- 回滚时无法清楚判断影响范围。

第一阶段推荐 change 顺序：

1. `add-server-foundation`
2. `add-protocol-envelope`
3. `add-local-infra`
4. `add-websocket-gateway`
5. `add-room-lobby`
6. `add-persistence-boundaries`
7. `document-client-integration`
8. `add-internal-service-boundaries`

## 任务编写规则

`tasks.md` 只写动作，不写空泛目标。

推荐写法：

- 创建 `server/cmd/server/main.go`
- 实现 `/healthz`
- 添加协议版本拒绝测试
- 更新 `docs/protocol-compatibility.md`

避免写法：

- 决定是否使用某技术
- 完善整体架构
- 优化代码质量
- 支持完整房间系统

设计决策写入 `design.md`，长期行为写入 `specs/`，路线写入 `docs/roadmap.md`。

## 实现流程

每次实现前必须确认：

- 当前 change 已选定。
- proposal、design、specs、tasks 已读取。
- 任务范围足够小。
- 相关文档和主 specs 不冲突。

实现时必须做到：

- 保持改动聚焦。
- 每完成一个任务立即更新 checkbox。
- 同步添加或更新测试。
- 遇到设计问题时暂停，先更新 artifacts。
- 不把临时代码伪装成正式实现。

## 验收规则

任务完成必须满足：

- 对应文件存在或代码行为已实现。
- 文档说明与代码或规格一致。
- 关键路径有测试或明确说明暂未测试原因。
- 没有新增无 owner 的表、Redis key、协议消息或服务接口。
- 没有留下无说明的 TODO、魔法值、静默失败或吞错逻辑。

归档前必须满足：

- `openspec status --change <name>` 显示 artifacts 完整。
- `tasks.md` 没有未完成项。
- delta specs 已同步到主 specs，或明确说明跳过原因。
- README 和 docs 不指向已失效路径。

## 第一里程碑控制

第一里程碑只做“自定义房间大厅”。

允许：

- 服务端启动
- 健康检查和版本接口
- WebSocket 连接
- Protobuf envelope
- 创建、加入、退出房间
- 准备和取消准备
- 房主转移
- 断线重连恢复身份

不允许混入：

- 匹配系统
- MOBA/RTS 高频战斗模拟
- 独立 battle server
- 跨服
- 观战和回放
- 完整经济或背包系统
