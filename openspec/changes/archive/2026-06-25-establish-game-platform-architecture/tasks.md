## 1. 项目架构文档

- [x] 1.1 创建 `docs/architecture.md`，说明客户端、网关、房间服、未来战斗服、Redis、MySQL 的职责边界
- [x] 1.2 创建 `docs/engineering-standards.md`，说明目录结构、Go 命名、日志、配置、错误处理、测试和协议规范
- [x] 1.3 创建 `docs/roadmap.md`，说明第一阶段到未来战斗服的演进路线
- [x] 1.4 更新 `README.md`，说明项目目标、当前范围和本地开发入口

## 2. OpenSpec 基线规格

- [x] 2.1 创建或更新 `openspec/specs/protocol/spec.md`
- [x] 2.2 创建或更新 `openspec/specs/gateway/spec.md`
- [x] 2.3 创建或更新 `openspec/specs/room/spec.md`
- [x] 2.4 创建或更新 `openspec/specs/storage/spec.md`

## 3. 第一里程碑决策

- [x] 3.1 明确第一里程碑为“自定义房间大厅”
- [x] 3.2 明确第一里程碑暂不实现匹配系统
- [x] 3.3 明确第一里程碑暂不实现 MOBA/RTS 高频战斗模拟
- [x] 3.4 明确第一里程碑暂不拆分独立 battle server

## 4. 后续变更拆分

- [x] 4.1 创建后续 change 清单：`add-server-foundation`、`add-protocol-envelope`、`add-local-infra`、`add-websocket-gateway`、`add-room-lobby`
- [x] 4.2 在 `docs/roadmap.md` 中记录每个后续 change 的目标、不做事项和验收边界
