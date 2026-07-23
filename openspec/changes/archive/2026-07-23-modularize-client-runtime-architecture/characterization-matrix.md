# 客户端运行时行为刻画矩阵

## 1. 用途

本矩阵把大协调器拆分前必须保持的外部语义绑定到现有 EditMode/PlayMode fixture。测试名称是行为契约入口，不表示拆分后必须保留原类型内部结构；迁移时允许移动测试到新 owner，但不得删除对应行为轴。

`N/A` 只用于没有异步命令的同步 projection reducer；这类 owner 的 cancellation 由上层 command/transaction fixture 覆盖。

## 2. 覆盖矩阵

| Owner / facade | generation / revision | single-flight / 并发 | failure / stale | cancellation | shutdown | 资源计数 / 唯一 owner |
| --- | --- | --- | --- | --- | --- | --- |
| `SessionCoordinator` | `LatestLoginIntentWinsOutOfOrderCompletion`、ticket/admission generation tests | `RefreshIsSingleFlightAndLateCompletionCannotRestoreSession` | commit-unknown、secure-store replacement/delete failure matrix | `RefreshWaiterCancellationDoesNotCancelSharedRequest` | `Stop_RejectsLateRefreshAndPublishesSingleInvalidation` | 单次 invalidation；由 `AppLifetimeTests` 验证唯一生命周期调用 |
| `ClientControlChannel` | `Recovery_DropsQueuedPushFromRetiredConnectionAttempt`、`Run_TerminalNotification_AllowsImmediateNextGeneration` | `Run_ConcurrentStartRejectsSecondOwner` | protocol、retry budget、backpressure、forced logout matrix | `Stop_PendingReceive_CancelsAndDisposesSocket` | `Stop_QueuedPush_DoesNotInvokeSubscriber` | `QualificationFault_CancelsAttemptAndRecoversSameRun` 与 concurrent run fixture 断言 run owner |
| `ClientGameplayChannel` | heartbeat generation、session invalidation、late response correlation tests | connection state gate 与 bounded writer/pending registry tests | kind mismatch、backpressure、heartbeat failure matrix | `CallerCancellationKeepsLateResponseCorrelated` | `ShutdownCompletesPendingAndDisposesConnection` | socket、heartbeat、pending 在 shutdown/recovery fixtures 中回零 |
| `PersonalWorldService` | world revision、assignment generation、lease renewal、tombstone matrix | 同 revision 幂等/冲突 gate | malformed identity、stale revision、control invalidation | N/A；同步 reducer 不拥有异步调用 | `ServicesStopRejectsLateProjection` | 唯一 projection snapshot 与 subscriber isolation |
| `VisitSessionService` | visit revision、invite replacement/retirement、membership revision matrix | command 前置条件与同 revision 冲突 gate | capacity、expiry、safe-return、malformed identity | command cancellation 由 admission/experience fixtures 覆盖 | `ServicesStopRejectsLateProjection` | inbox capacity 与唯一 visit projection |
| `WorldAdmissionCoordinator` | target/session/assignment generation recovery matrix | `ConcurrentEnterOwnWorldKeepsSingleIntent` | accept rejection、return failure、disconnect、stale admission matrix | flow cancellation 由 Experience route-cancellation fixture 覆盖 | `ShutdownRejectsLateAndNewFlow` | 单一 intent 与 current target owner |
| `ClientConnectionRecoveryCoordinator` | frozen target、scene gate、session refresh generation matrix | automatic/manual/duplicate control single-flight matrix | deadline、terminal、simultaneous channel failures | caller cancellation 由 Experience recovery fixture覆盖 | `ShutdownRejectsLateRecoveryCompletion` | `ThreeRecoveryRoundsReturnToSingleIdleOwner` |
| `ClientUiRouter` | navigation/scene/host generation matrix | bounded transition queue、modal ordering | show failure rollback、scene invalidation、subscriber failure | caller cancellation before/after commit matrix | `CancelledStopCanBeRetriedToFinishCleanup` | active/cached/modal/input owner 由 Router EditMode 与双 Host PlayMode 覆盖 |
| `ClientPersonalWorldExperience` | presentation/session/route generation invalidation matrix | authentication 与 recovery single-flight | bootstrap/control/world/logout failure matrix | auth、recovery 与 committed route cancellation fixtures | `StopClosesRoutesAndRejectsLateAction` | subscriber isolation；qualification soak 比较 dispatcher/subscription/scene/channel owner 基线 |

## 3. 拆分期间的使用规则

1. 每提取一个 reducer、flow、transaction 或 pump，先把对应行的 fixture 移到新 owner 或增加更窄的测试，再删除旧实现。
2. 测试必须继续观察公开 facade、typed port 或只读低敏诊断，不得读取 credential、generated payload 或新组件私有可变字段。
3. resource-count 断言只验证唯一 owner、零泄漏和稳定基线，不把计数 API 扩大为生产 service locator。
4. 任一矩阵行为失败时，该迁移 task 不得勾选；不得通过保留新旧双写来让测试偶然通过。
