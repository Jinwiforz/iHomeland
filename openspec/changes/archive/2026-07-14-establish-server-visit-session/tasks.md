## 1. Identity、policy 与 snapshot

- [x] 1.1 创建 `internal/visitsession` package 与中文 package doc，实现独立 `VisitSessionID`、`InviteID`、`CommandID`、`ConnectionBindingID` namespace、128-byte 安全 ASCII 校验、不可混用值语义和默认脱敏格式化
- [x] 1.2 实现 `Capacity`、`Revision`、session/invite/reservation/Owner grace/Visitor reconnect policy，固定 1-32 capacity、64 pending invite 上限、UTC 微秒规范化、全局安全范围、application 配置上限与等于即失效语义
- [x] 1.3 实现封闭 session/invite/membership lifecycle、基于 current auth lineage 与 joined/reconnecting membership 的 Owner/Visitor role、auth/binding projection、immutable Owner/world/full assignment binding 和严格 accessor
- [x] 1.4 实现稳定排序且返回副本的 `Snapshot`/Invite/Membership projection 与 hydration，拒绝 revision 0、重复 identity、Owner-as-Visitor、unknown state、越界集合和矛盾 deadline
- [x] 1.5 实现 `SafeReturnDirective`/reason/destination、`AdmissionIntent` 与构造入口封闭的 trusted join qualification 值，确保不包含 endpoint、credential 或客户端可选 world identity且默认格式化脱敏
- [x] 1.6 添加 identity/policy/hydration/table/fuzz tests，覆盖 ID namespace、capacity/time 边界、集合排序/副本、malformed snapshot、亚微秒规范化和默认格式化泄漏

## 2. Aggregate 状态迁移

- [x] 2.1 实现 VisitSession create/open 与 active assignment immutable binding，初始 revision/lifecycle/Owner auth binding正确且不把Owner计入capacity
- [x] 2.2 实现 Owner-only create/revoke/expire invite、pending invite上限/expiry/target校验与不预占capacity语义，拒绝Owner自邀、Visitor邀请和invite-as-credential
- [x] 2.3 实现目标Visitor accept：原子校验target/session/Owner/assignment/capacity/epoch，消费invite、创建唯一reserved membership并返回expiry受限的AdmissionIntent
- [x] 2.4 实现 trusted qualification join：重新校验 reserved membership、session lineage/epoch、current full assignment、Owner availability、deadline 与新 binding，并只转换一次为 joined；reserved 不授予 Visitor role
- [x] 2.5 实现reserved membership expire，精确比较reservation identity/deadline、释放capacity且不生成safe-return，旧AdmissionIntent不能恢复资格
- [x] 2.6 实现Visitor self leave、Owner kick与精确binding authorization，只删除目标membership、释放capacity并返回确定性safe-return directive
- [x] 2.7 实现Visitor disconnect/reconnect/reconnect-expire，比较SessionID/epoch/binding/generation/deadline，确保旧callback不能删除新binding或影响其他membership
- [x] 2.8 实现Owner disconnect/reconnect/close/grace-expire，暂停新invite/accept/join、以当前有效Owner AuthContext更新auth binding、禁止host succession，并让旧grace timer保持stale
- [x] 2.9 实现session expiry、assignment change与dependency-loss close，单次revision推进、清空invite/reserved membership，并按VisitorID稳定生成joined/reconnecting成员的完整safe-return directives
- [x] 2.10 添加 aggregate table tests，覆盖每条合法/非法状态边、deadline 等号、Owner grace 期间限制、批量 close、assignment replacement、epoch 变化和控制面权限

## 3. Store 契约与 application service

- [x] 3.1 定义最小消费侧 `OwnedWorldReader`、`CurrentAssignmentReader`、Clock、IDGenerator 与 `VisitSessionStore`，只复用 account/personalworld/placement/session owner值且不依赖backend、socket或generated type
- [x] 3.2 实现规范 `CommandFingerprint`、create/transition record 与完整 result；existing session mutation 绑定 VisitSessionID，open 绑定 world/assignment/capacity/session lifetime，排除重试 observedAt 与重新生成的 candidate ID；deadline、target、binding 和 actor 变化必须产生不同 fingerprint
- [x] 3.3 定义created/existing、applied/replay、not-found、revision/idempotency/capacity/stale/invalid-state、not-committed/commit-unknown outcomes与稳定operation/error/commit phase，默认错误不泄漏identity、assignment、command或binding
- [x] 3.4 实现Service构造、open/resolve/create-invite编排，从受信AuthContext解析actor并严格验证OwnedWorldReader、CurrentAssignmentReader与store返回组合
- [x] 3.5 实现accept/join编排，确保每次调用重新确认current active assignment与lease，AdmissionIntent不被解释为credential，trusted qualification违约fail closed
- [x] 3.6 实现 leave/kick、Owner/Visitor disconnect/reconnect、expire/close/invalidate 编排，assignment invalidation 必须重新取得权威 missing/expired/replaced 证据，system command 不伪造 Owner 且所有 safe-return result 可由 store 完整 replay
- [x] 3.7 对store snapshot/result做严格hydration、fingerprint/revision/Owner/world/assignment/directive一致性校验，任何矛盾outcome或部分结果映射为dependency defect
- [x] 3.8 添加 service fake dependency/error tests，覆盖 invalid AuthContext、world owner mismatch、placement missing/stale/expired、configured deadline、assignment invalidation evidence、reader failure、malformed store result、not-committed 与 commit-unknown

## 4. 并发、重试与安全验收

- [x] 4.1 在 `_test.go` 实现并发reference store，原子维护world active index、snapshot、CommandID replay record与故障注入；禁止production memory fallback
- [x] 4.2 添加并发open/invite/accept tests，证明每world至多一个active session、最后capacity slot只被一个Visitor取得、同Visitor membership唯一且revision单调
- [x] 4.3 添加response-loss与idempotency tests，覆盖相同command在observedAt推进后replay、同CommandID异义conflict、完整directives重放、not-committed和commit-unknown不自动补写
- [x] 4.4 添加Owner/Visitor reconnect race tests，覆盖旧disconnect、旧leave、旧grace/reconnect timer与新binding竞争，并在race detector下证明不会ABA删除或host succession
- [x] 4.5 添加权限与结构扫描，证明 Visitor 不能 invite/kick/close/Owner mutation，invite/AdmissionIntent 不能 join，package 不暴露 generic gameplay authorizer，且不包含 Gin/WebSocket/TCP/Redis/MySQL/generated imports、goroutine、timer、global mutable store 或 Room/Party/Activity 依赖

## 5. 文档与质量门

- [x] 5.1 更新 `docs/file-structure.md`、`docs/roadmap.md` 与 `server/README.md`，记录VisitSession owner、状态/恢复/safe-return边界、仍未实现的storage/protocol/transport与正式Composition Root未接线事实
- [x] 5.2 复核全部手写Go注释与错误格式化，确保导出/业务声明、struct字段、interface方法、时间单位、角色授权、并发、失败、生命周期和敏感identity符合注释规范且无无上下文TODO
- [x] 5.3 使用项目Go入口执行format、全量unit/table、targeted fuzz、race、vet、mod verify、`git diff --check`、依赖/secret/generated/cache hygiene与全量OpenSpec strict
- [x] 5.4 将 `server-visit-session` delta同步到主spec，复核tasks与实际实现一致并再次strict，满足归档条件但不提前实现Redis adapter、protocol、admission credential、transport、Go协议客户端或Unity
