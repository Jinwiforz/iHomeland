## Why

服务端进程在 assignment loss callback 执行前退出时，Redis 可能仍保留绑定旧 AssignmentStamp 的 active VisitSession；进程恢复并创建新 assignment 后，Owner 的显式 Open 只会持续返回 stale，导致客户端界面无法与服务器当前状态收敛。该状态必须通过已有权威 placement 证据和显式终态命令恢复，不能依赖清理 Redis、延时、tick 或客户端重复点击。

## What Changes

- 为 Owner Open 增加旧 assignment VisitSession 的确定性恢复编排：先提交并发布 `assignment-changed` 终态结果，再以原始 Open CommandID 解析或创建绑定 current assignment 的唯一 active VisitSession。
- 将恢复限制为一次有因果依据的重新解析；dependency、dependency defect 或 commit-unknown 立即 fail closed，不自动换 CommandID、循环重试或猜测提交结果。
- 覆盖并发 Open、current session 幂等、safe-return 投递和服务端重启后 Redis 遗留状态的测试与真实客户端验收。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `server-visit-session`: 补充 Owner 显式 Open 在 current assignment 已替换旧 active VisitSession 时的权威退役、重新解析和失败边界。

## Impact

- 影响 `server/internal/app` 的 VisitSession application/coordinator 编排及其测试。
- 复用现有 `VisitSessionService.InvalidateAssignment`、Redis `Commit`/`Create`、safe-return 与 WSS/TCP 发布契约，不修改协议、Redis schema、客户端状态机或持久化数据。
- 服务端重启后无需人工删除 Redis key；同一份遗留运行态可由 Owner 的显式 Open 收敛。
