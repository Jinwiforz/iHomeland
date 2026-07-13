## Why

账号与统一会话已经提供稳定 `PlayerID` 和可信 principal，但服务端尚未拥有个人持久世界的独立领域边界。现在需要先固定 PersonalWorld identity、immutable owner、revision、生命周期与 mutation 契约，避免后续 placement、MySQL/Redis、协议或 Unity 反向决定核心世界模型。

## What Changes

- 新增 transport-independent `personalworld` domain/application package，建立 `PersonalWorldID`、`WorldOwnerID = account.PlayerID`、primary world、生命周期和持久 revision 不变量。
- 定义 primary PersonalWorld 的幂等创建与严格 hydration，拒绝零值、错误 owner、非法 lifecycle、无效 revision 和不一致持久快照。
- 定义带 `expected revision` 与 Owner-scoped `idempotency key` 的世界 mutation 编排、稳定错误分类、提交结果和 commit-unknown 语义。
- 定义消费侧 `PersonalWorldRepository`、clock 与 ID generator 契约，并使用并发 reference repository 和 deterministic fakes 完成纯 Go 验收。
- 明确 PlayerState、PersonalWorldState 与未来 ActivityInstanceState 的所有权边界；本 change 不实现地图模拟、任务、奖励、Visitor、WorldInstance、placement、MySQL/Redis adapter、协议或 listener。

## Capabilities

### New Capabilities

- `server-personal-world`: 定义 PersonalWorld 领域身份、primary world 唯一性、生命周期、revision、幂等 mutation、repository 结果和独立测试要求。

### Modified Capabilities

无。

## Impact

- 新增 `server/internal/personalworld` 手写 Go 代码与测试。
- 复用 `server/internal/account.PlayerID`、Composition Root 的 clock/ID 生产边界和既有日志/错误安全规范，但不接入正式 Composition Root。
- 不新增或修改 Protobuf、OpenAPI、registry、listener、配置、端口、数据库 schema、Redis key、Unity 文件或生成代码。
- 为后续 `establish-server-world-instance-placement` 和 `establish-server-personal-world-storage` 提供稳定消费侧契约与领域不变量。
