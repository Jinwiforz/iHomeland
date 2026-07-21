## Why

目标 Visitor 收到 `VisitInvitePush` 后，Owner 撤销邀请、邀请到期或 VisitSession 关闭时，服务端没有发送对应的权威退役事实，导致客户端 inbox 长期保留已经无效的邀请。玩家点击残留邀请后虽然服务端拒绝 accept，客户端却只回到原有 OwnWorld 状态并显示宽泛错误，容易被误解为接受成功进入了自己的世界。

## What Changes

- 扩展公开 invite projection 的封闭状态，使用既有 `VisitInvitePush` 向精确目标 Visitor 发送 `RETIRED` tombstone，不新增第二条邀请通知通道。
- 让 VisitSession mutation result 原子保存并可重放本次退役的 invite identity；撤销、到期、接受和 terminal close 的 WSS 副作用都从已提交结果派生，不从提交后的 snapshot 猜测已经被删除的目标。
- 客户端按完整 VisitSessionID、InviteID、目标玩家与 revision 应用 tombstone，立即删除 inbox 条目和页面 selection；重复、迟到和乱序通知保持幂等或被拒绝。
- 当客户端仍持有残留邀请且 accept 被服务端明确判定为不存在、已过期或状态冲突时，线性化退役该 identity，保持 OwnWorld target 不变，并显示“邀请已撤销或失效”的明确低敏说明；timeout、transport 与 commit-unknown 不猜测提交结果。
- 增加服务端 domain/store/codec/WSS、客户端 Service/Coordinator/Presentation 与双客户端回归测试，并重新生成协议、验证兼容性、构建 Windows Development Player。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `server-visit-session`: VisitSession 首次提交与 replay 必须携带完整 invite retirement 事实，并向精确目标发布权威 tombstone。
- `server-contracts`: `VisitInviteState` 增加兼容的公开 `RETIRED` 状态，既有 `VisitInvitePush` 承担创建与退役两类定向投影。
- `client-personal-world-services`: Invite inbox 必须应用退役 tombstone，并在明确 accept 拒绝时删除被服务器否定的残留 identity、保持 OwnWorld 且呈现准确错误。

## Impact

- 影响 `shared/proto/ihomeland/visit/v1/visit.proto`、协议生成/fixtures/compatibility baseline。
- 影响 Go VisitSession mutation result、Redis codec、application result coordinator 与 WSS contract tests。
- 影响 Unity `VisitSessionService`、`WorldAdmissionCoordinator`、个人世界 presentation failure 映射及 EditMode/PlayMode tests。
- 不改变 message ID、allowed channel、认证 scope、invite bearer 语义、HTTP accept 路径、存储 schema 或客户端 target/Scene owner。
