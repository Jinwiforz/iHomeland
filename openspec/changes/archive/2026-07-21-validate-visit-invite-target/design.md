## Context

`VisitSession.CreateInvite` 当前从 TCP application 收到格式有效的 `account.PlayerID` 后，直接进入 aggregate mutation。Aggregate 已拒绝 Owner 与 target 完全相等，但 application 没有读取 Account owner 的持久事实，因此任意形如 `ply_*` 的不存在身份都能形成 pending invite、推进 revision 并出现在 Owner UI。Account repository 目前只支持按 username 认证读取，没有面向其他领域的最小 Player 可用性端口。

该校验跨 MySQL Account owner 与 Redis VisitSession owner，不能形成跨存储事务。本设计只要求在首次 CreateInvite 提交前取得目标当前为 active Player 的权威证据；之后账号状态变化由未来独立资格策略处理。

## Goals / Non-Goals

**Goals:**

- 只有非 Owner、当前存在且 active 的 PlayerID 才能首次创建邀请。
- self、missing 与 inactive 使用同一公开 validation 语义，避免账号状态枚举。
- Account 读取故障 fail closed，且不能推进 VisitSession revision 或产生推送。
- 已提交 command 的 replay 不受目标后续状态变化影响。

**Non-Goals:**

- 不新增好友关系、搜索玩家、昵称解析或 player profile。
- 不新增协议错误码、客户端本地存在性缓存或 HTTP API。
- 不尝试跨 MySQL 与 Redis 建立分布式事务，也不自动撤销目标随后 inactive 的既有邀请。

## Decisions

### 1. Account owner 提供最小 `InvitablePlayerReader`

在 `internal/account` 定义只返回 `Available`、`Unavailable` 或 dependency error 的读取合同。MySQL adapter 使用唯一 `player_id` 索引读取封闭 account status；active 返回 Available，missing/inactive 合并为 Unavailable，unknown/corrupt status 返回依赖错误。VisitSession 不读取 AccountID、username、credential 或其他账号事实。

选择该窄端口而不扩展认证读取，是为了让 Account owner 保持数据解释权，同时避免 VisitSession 依赖完整 `AuthenticationRecord`。也不在 TCP handler 直接查询 repository，防止业务授权扩散到 transport。

### 2. 首次校验位于 Owner/active-session 授权之后、mutation 之前

Service 先从受信 AuthContext 解析 actor、确认 owned active PersonalWorld 与 active VisitSession，再构造包含 target 的 command fingerprint。它先用 conflict probe 决议已提交 replay/idempotency conflict；只有首次命令才拒绝 self 并调用 `InvitablePlayerReader`。Available 后才生成 InviteID 和提交 Redis transition。

该顺序避免未授权调用者借接口枚举 PlayerID，也保证首次结果已经提交后，即使目标账号随后 inactive，相同 command 仍重放首次结果。目录读取和 Redis commit 之间存在状态变化窗口，但目标在校验时有权威 active 证据；本 change 不引入跨存储事务。

### 3. 公开失败复用现有 validation 语义

self、missing 和 inactive 都映射 `ErrorCodeInvalidArgument`，经现有 gameplay error 转为 `VALIDATION_FAILED`。客户端继续显示“输入内容无效，请检查后重试”，不会知道目标究竟不存在、inactive 还是不能邀请自身。依赖读取失败映射 dependency unavailable，不伪装成无效输入。

不新增 `VISIT_TARGET_NOT_FOUND`，因为精确区分会扩大账号枚举面并带来不必要的协议、registry 与客户端版本耦合。

## Risks / Trade-offs

- [Account 在校验后、Redis 提交前变为 inactive] → 接受短暂竞态；校验使用当时权威事实，后续 accept 仍受有效 AuthContext 和 VisitSession 条件约束，未来如需强制撤销由独立 account-status 事件设计承担。
- [每次首次邀请增加一次 MySQL point lookup] → 使用唯一 `player_id` 索引与只读单列查询；replay 在查询前由 store probe 返回，不重复消耗 MySQL。
- [通用 validation 文案不够具体] → 保护账号枚举边界；产品未来可使用同一低敏“目标玩家不存在或不可邀请”本地文案，但不依赖服务端透露具体原因。

## Migration Plan

1. 先发布 Account reader、repository test 与 VisitSession service 接线。
2. 运行 MySQL integration、VisitSession targeted/full Go tests 和 OpenSpec strict。
3. 构建新 server 并重启本地进程；不需要 MySQL/Redis schema 迁移。
4. 回滚只需回退 server binary；已经存在的伪目标 pending invite 可由 Owner 撤销、关闭 VisitSession 或既有 expiry 自动清理。

## Open Questions

无。
