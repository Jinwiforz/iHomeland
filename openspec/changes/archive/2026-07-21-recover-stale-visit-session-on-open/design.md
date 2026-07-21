## Context

VisitSession active index 和完整 snapshot 是 Redis 中可恢复运行态。正常 assignment loss 会由 `worldAssignmentCoordinator` 回调 `personalWorldVisitCoordinator.InvalidateAssignment`，但进程在回调前退出时，旧 active VisitSession 会合法地保留到 Redis TTL。重启后 placement 为同一 PersonalWorld 创建更高 generation/fence 的 current assignment；`VisitSessionService.Open` 能识别旧 snapshot 的 AssignmentStamp 不匹配，却只返回 `stale`，因此后续每次 Open 都命中同一个旧 active index。

已有领域边界已经具备正确的终态能力：`InvalidateAssignment` 会重新读取 placement 权威证据、以 expected revision 原子关闭旧 aggregate、移除 active index并保存可重放 SafeReturnDirective。缺失的是 Owner 显式 Open 与该能力之间的 application 编排，以及首次终态结果的跨通道发布。

## Goals / Non-Goals

**Goals:**

- 让服务器进程恢复后的显式 Owner Open 从 Redis 遗留旧 VisitSession 确定性收敛到 current assignment。
- 保证旧 VisitSession 的关闭、safe-return、deadline 收敛和新 Open 各自使用现有权威 owner 与线性化结果。
- 对并发、提交不确定性和依赖故障 fail closed，不使用延时、tick、轮询或无界重试。
- 用纯 Go、真实 Redis 和真实 Windows 客户端验证恢复行为。

**Non-Goals:**

- 不把旧 VisitSession 的 assignment 就地改写为新 assignment，不迁移旧 invite、reservation 或 membership。
- 不修改 Redis schema、Lua transaction、公开协议或客户端 View State。
- 不实现服务器自动重连、客户端隐式重试或后台周期清理。

## Decisions

### 1. Open 只在明确 stale 结果后进入专用恢复编排

`tcpGameplayApplication.VisitOpen` 首先照常调用领域 `Open`。只有该调用返回 `OperationOpen/ErrorCodeStale` 时，application 才要求 `visitResultCoordinator` 对该 PersonalWorld 的 active VisitSession 执行一次 `ReconcileStaleOpen`，随后使用相同外部 CommandID 再调用一次 `Open`。其他错误保持原有映射，不触发清理。

这不是按时间或失败次数进行的重试，而是由第一次权威结果开启、由一次终态迁移分隔的两阶段 application command。重新使用原始 Open CommandID 保留 create replay/idempotency 语义；第二次结果仍由 store 的 active index 和 current assignment 决定。

替代方案是在客户端收到错误后再次点击，无法保证产品收敛；在 `VisitSessionService.Open` 内部隐式关闭并创建，则会在第二阶段失败时丢失已经提交的 safe-return 副作用，均不采用。

### 2. Coordinator 负责终态命令和副作用，领域 Service 继续负责权威证据

`ReconcileStaleOpen` 只读取 world 当前 active snapshot，并使用完整旧 AssignmentStamp、VisitSessionID 和 revision 派生稳定 system CommandID，再调用既有 `VisitSessionService.InvalidateAssignment`。Service 必须重新读取 placement；只有 missing、expired、non-active 或完整 stamp 不同才允许提交。成功或 replay 的完整 `MutationResult` 立即进入 `MutationCommitted`，统一取消 deadline、发布 closed snapshot 和 SafeReturnDirective。Assignment changed 通知只投递给仍持有旧 Owner gameplay connection 的 Owner及受旧会话影响的 Visitor；重启后旧 connection 已不存在时，不得用 predecessor 事件扰动已经进入 successor assignment 的 Owner。

恢复生命周期使用固定 `stale_open_reconcile` 观测 operation。该值必须与 metrics 的封闭标签词汇表、测试和接口注释同时登记；观测不得在已提交终态之后因标签缺失触发 panic，也不得把动态 identity 放入 label。

如果 active index 已被其他并发恢复移除，或已被 current assignment 的新 session 替换，not-found、invalid-state、stale、revision-conflict 视为“由较新事实取代”，交给第二次 Open 最终判定。Dependency、dependency defect 和 commit-unknown 不得被吞掉，也不得执行第二次 Open。

替代方案是让 coordinator 自己比较 assignment 后直接删 key，会复制领域 policy、绕过 replay/directive，并破坏 store owner，因此不采用。

### 3. 恢复严格有界且不承诺跨两个线性化点原子

旧 session terminal commit 与新 session create 是两个既有 store 线性化点，中间允许短暂不存在 active VisitSession。旧终态一旦提交就必须发布，即使随后 create 因依赖故障失败；这时 Owner 看到可重试的服务错误，但服务器状态仍真实一致，下一次新的显式 Open 可以创建新 session。

不新增“替换旧会话并创建新会话”的大型 Lua transaction，因为它会耦合两个 aggregate identity、两种 replay result 与 transport side effect，且不能让网络投递与 Redis 原子化。现有终态后创建的组合更易恢复、观测和重放。

## Risks / Trade-offs

- [终态提交后新 Create 可能失败] → 旧访问已安全关闭并完成副作用；返回依赖错误，不伪造新会话，后续显式新命令可创建。
- [多个 Owner Open 并发恢复同一旧 session] → 稳定 invalidation CommandID、revision CAS 和第二次 Open 的 unique active index保证最多一个新 active session；较新事实竞争由第二次 Open 决议。
- [并发 deadline mutation 先推进旧 revision] → 当前恢复尝试不循环追赶；revision conflict 交给第二次 Open，如果旧 session 仍 stale 则返回稳定冲突，避免无界自动 mutation。
- [safe-return 发布失败] → 与既有 mutation 契约一致，已提交 result 可按相同 system CommandID replay；发布失败不回滚 Redis 事实。
- [旧 assignment 通知误伤 successor Owner] → 只有旧 Owner connection 仍由当前进程解析时才向 Owner 发送 assignment changed；terminal VisitSession closed 与 Visitor safe-return 仍按完整结果投递。
- [观测标签与新流程漂移] → 在封闭 metrics vocabulary 中登记 `stale_open_reconcile`，并让真实 coordinator 测试绑定 production metrics observer，缺项会直接使测试失败。

## Migration Plan

1. 增加 coordinator 恢复端口、VisitOpen 两阶段编排和单元/并发测试。
2. 在真实 Redis adapter tests 与全量 Go/protocol/OpenSpec 验证中确认 schema 和原子契约不变。
3. 保留现场旧 active VisitSession，替换服务端二进制并重启；用 Owner Windows Player 首次点击 Open 验证旧 session closed、新 session 绑定 current assignment，第二次点击保持幂等。
4. 回滚只需恢复 application/coordinator 代码；Redis 无 schema 迁移，已关闭旧 session 与新 session 均保持合法事实。

## Open Questions

无。恢复触发、失败分类、命令 identity 与副作用 owner 均复用现有封闭契约。
