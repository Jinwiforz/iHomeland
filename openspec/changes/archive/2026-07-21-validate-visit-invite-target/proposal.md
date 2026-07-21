## Why

当前 CreateInvite 只验证 `PlayerID` 的语法和 Owner 权限，格式正确但不存在的身份仍会被写入 pending invite；手动验收还发现用户可能误把非当前 Player 身份当作自己并产生无效列表项。商业客户端不能展示服务端从未证明可邀请的目标，因此服务端必须在提交前以 Account 持久事实校验目标。

## What Changes

- Account owner 增加按 `PlayerID` 判断目标是否为 active、可邀请玩家的最小只读端口与 MySQL 实现。
- VisitSession CreateInvite 在确认当前 actor 为 active VisitSession Owner 后，拒绝 Owner 自身以及不存在、inactive 或矛盾的目标，并保持 revision、invite 集合和副作用不变。
- 相同 CommandID/fingerprint 已提交时先重放首次结果，不因目标账号随后变为 inactive 而破坏幂等结果。
- 对 self、missing 与 inactive 统一使用现有低敏 validation 失败，不新增可用于枚举账号状态的公开错误码。
- 增加 account repository、VisitSession service、application/真实存储测试。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `server-account-session-storage`: 增加按 PlayerID 提供 active 可邀请性决议的 Account owner 只读合同。
- `server-visit-session`: 创建定向邀请前必须验证目标是非 Owner 的 active Player，并保持 replay、依赖故障与拒绝零写入语义。

## Impact

- 影响 `internal/account` 读取合同、MySQL account repository、VisitSession application service、Composition Root 和相关 fake/integration tests。
- 不修改 protobuf、message ID、HTTP/OpenAPI、Redis schema、Unity 资产或 `.meta` 文件。
- 客户端继续使用既有 `VALIDATION_FAILED` 中文映射，不依赖本地 PlayerID 存在性猜测。
