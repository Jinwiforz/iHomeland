## 1. Unity 客户端接入文档

- [x] 1.1 新增 `docs/client-integration.md`，说明 Unity 客户端接入 owner、范围和非目标
- [x] 1.2 文档化 Unity Protobuf 生成输入、输出归属和禁止手工修改生成代码规则
- [x] 1.3 文档化 Unity WebSocket `/ws`、二进制 envelope、协议版本、request id、sequence 和 payload 处理
- [x] 1.4 文档化心跳、结构化错误响应和版本不兼容处理
- [x] 1.5 文档化房间大厅最小消息流程和 `RoomSnapshot` UI 刷新规则
- [x] 1.6 文档化本地联调验收步骤

## 2. Unity 技术方向收敛

- [x] 2.1 更新根 `README.md`，将客户端技术方向收敛为 Unity
- [x] 2.2 更新 `AGENTS.md`，将项目边界中的客户端表述收敛为 Unity
- [x] 2.3 更新 `docs/architecture.md`，将客户端架构和第一阶段链路改为 Unity
- [x] 2.4 更新 `docs/file-structure.md`，说明 `client/` 归属 Unity 工程
- [x] 2.5 更新 `docs/roadmap.md` 和 `docs/workflow.md`，反映 `document-client-integration` 当前 change 和 Unity 方向

## 3. 协议说明同步

- [x] 3.1 更新 `shared/proto/README.md`，说明 Unity C# 生成由客户端接入文档约束
- [x] 3.2 更新 `docs/protocol-compatibility.md`，补充 Unity 客户端 envelope、错误和版本处理说明
- [x] 3.3 确认文档中不再保留第一阶段客户端 Unity/Godot 二选一表述

## 4. 验证

- [x] 4.1 运行 OpenSpec 状态检查，确认 proposal、design、specs、tasks 完整
- [x] 4.2 运行文档搜索，确认 Unity 方向和客户端接入文档一致
- [x] 4.3 运行服务端 Go 测试或说明本 change 不影响运行时代码
