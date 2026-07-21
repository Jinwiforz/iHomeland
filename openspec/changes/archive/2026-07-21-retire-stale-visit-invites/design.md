## Context

当前服务端只在 `OperationCreateInvite` 后向目标玩家发送 message 2100 `VisitInvitePush`。撤销、deadline 到期、HTTP accept 和 terminal close 都会改变或移除 pending invite，但提交后的 snapshot 已经不再包含原始目标，result 也没有保存被退役的 invite，因此 application coordinator 无法可靠构造定向撤销通知。客户端 `VisitSessionService.ApplyInviteProjection` 已有“非 Pending 状态删除 exact identity”的雏形，但 generated enum 只有 Pending/Accepted，且明确 accept 拒绝只映射成宽泛 validation failure。

这是一条跨 domain result、Redis replay、WSS contract、Unity projection 与 presentation 的权威事实链。修复不能依赖客户端定时清理、页面打开时猜测、按钮点击后的延时刷新或把 accept 失败改写为成功。

## Goals / Non-Goals

**Goals:**

- 每个 pending invite 从可接受变为不可接受时，产生可持久、可 replay、可定向发布的权威退役事实。
- 复用 message 2100 和 WSS reliable-ordered channel，让 Visitor inbox 与服务器当前资格收敛。
- 明确区分“邀请不可用”和“进入 OwnWorld 成功”，失败时保持原 target 并显示准确中文说明。
- 保持 mutation result、side effect、client projection 的幂等、乱序防护和 commit-unknown 语义。

**Non-Goals:**

- 不增加 inbox 查询 API、轮询、定时器、tick 修正或自动 accept 重试。
- 不改变 invite/accept/admission/JOIN 的权限、credential、capacity 或 idempotency 边界。
- 不新增 message ID、数据库表、Redis key 或第二条通知通道。

## Decisions

### 1. 复用 `VisitInvitePush` 并增加公开 `RETIRED` 状态

`VisitInviteState` 增加 `VISIT_INVITE_STATE_RETIRED`。Pending push 表示 exact identity 当前可接受；Retired push 表示该 identity 已被权威退役。Retired 不公开内部原因，也不授予任何能力。客户端只按完整 VisitSessionID + InviteID 删除，不按 Owner、target 或列表位置模糊匹配。

替代方案是新增 `VisitInviteRetiredPush` 和 message ID。它会为同一 projection 建立第二条路由和重复 codec，且现有客户端 apply 边界已经按 state 设计，因此不采用。

### 2. Mutation result 原子保存 `retired_invites`

VisitSession application service 在 source snapshot 与首次成功 target snapshot 之间计算 pending invite 差集：source 中 Pending、target 中已不存在或已非 Pending的 identity 进入稳定排序、去重的 `retired_invites`。该集合与 target snapshot、CommandID、fingerprint、operation-specific payload 和 safe-return directives 一起写入 transition result，并由 Redis codec 完整 replay。

这使 revoke、expire、accept、close、owner grace expiry、session expiry、assignment invalidation 和 dependency loss 自动遵循同一规则，不需要每个 handler 重读提交前状态。Result validation 要求 retired identity 完整、唯一、排序，且 target snapshot 不再包含同一 Pending identity。Commit-unknown 不发布；applied/replay 可以重复发布同一 tombstone，客户端幂等处理。

替代方案是在 TCP/HTTP handler 提交前临时保存目标玩家。该信息无法随 store replay 恢复，进程故障或响应丢失后会产生不同副作用，因此不采用。

### 3. Application coordinator 只从 committed result 发布退役通知

`personalWorldVisitCoordinator` 对每个 `RetiredInvites()` 项向其 TargetVisitorID 发布 message 2100，并把 summary state 投影为 Retired；create 仍投影 Pending。Terminal close 不再尝试从已经清空邀请的 target snapshot 推测邀请接收者。WSS 的同连接可靠有序队列保证 create/revoke 的提交顺序，重复 tombstone 只删除 exact identity。

### 4. 客户端明确拒绝时退役残留项并保持 OwnWorld

客户端收到 Retired push 时，即使原 invite 已过期，也必须应用删除；expiry gate 只拒绝新增 Pending。若玩家点击残留项，HTTP accept 对 visit/invite not-found、invite expired、state/revision conflict 或当前 actor 明确不可接受给出确定拒绝时，Coordinator 调用 Service 的权威拒绝退役入口删除 exact identity，并提交 `InviteUnavailable` failure。Capacity、Owner unavailable、rate limit、transport、timeout 与 commit-unknown 不删除，因为资格仍可能有效或提交结论未知。

`JoinVisitAsync` 在 accept 成功前不会关闭 OwnWorld gameplay target；上述失败只保持原 OwnWorld target/Scene，不调用 own-world admission，也不把失败伪装为导航成功。Presentation 将其显示为“邀请已撤销或失效，请选择最新邀请”。

## Risks / Trade-offs

- [Mutation result JSON 增加可选集合] → Codec 使用向后兼容的 `retired_invites` 可选字段；旧记录缺失时解码为空，新记录由 strict validation 验证。
- [旧客户端不识别 Retired enum] → Protobuf wire 兼容但旧 application mapper 会拒绝该 push；本地服务端与客户端作为同一里程碑整体升级，并重新执行版本/fixture/双客户端验收。
- [重复 replay 再次发送 tombstone] → 客户端 exact-identity 删除幂等，重复通知不产生错误或 UI 抖动。
- [错误地把暂时失败当成失效] → 只对白名单确定性 server error 退役；不对 transport、timeout、commit-unknown、capacity 或 Owner unavailable 猜测。

## Migration Plan

1. 先扩展 proto enum、domain result/codec 与测试，验证旧 Redis result 仍能解码。
2. 再接入 server result coordinator 和 Unity mapper/Service/Coordinator/presentation。
3. 统一重新生成 Go/C# protocol，运行 compatibility、fixtures、Go/Unity 全量测试。
4. 构建并同时升级本地 server 与 Windows Development Player，执行 create/revoke、expire、close、残留 accept 双客户端矩阵。
5. 回滚时 server/client 同时回退；新 reader 兼容缺失 `retired_invites` 的历史结果。若必须回退到不认识该字段的旧 server，应先清理可恢复的 VisitSession Redis 运行态再启动旧版本，MySQL 持久事实不受影响。

## Open Questions

无。首期复用 message 2100、Retired 不公开内部原因、明确拒绝使用客户端本地低敏说明的边界在本 change 内冻结。
