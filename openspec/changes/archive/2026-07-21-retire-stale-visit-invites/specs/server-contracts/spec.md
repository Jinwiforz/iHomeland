## ADDED Requirements

### Requirement: 邀请创建与退役必须使用同一兼容投影

公开 `VisitInviteState` MUST 登记 `PENDING`、`ACCEPTED` 与 `RETIRED` 封闭值；message 2100 `VisitInvitePush` MUST 继续使用唯一 WSS/CONTROL/reliable-ordered 路由，并以完整 VisitSessionID、InviteID、OwnerPlayerID、TargetVisitorID、created revision 与 absolute expiry 表达精确邀请 identity。`RETIRED` MUST 只表示该 identity 已不可接受，不得包含 credential、endpoint、membership、内部关闭原因或新的授权能力。Proto enum 增量、generated code、descriptor、registry projection、fixtures 与 Go/C# parity MUST 由统一协议入口验证。

#### Scenario: 发布邀请退役 tombstone

- **WHEN** 已提交 VisitSession mutation 证明某个 pending invite 已被撤销、到期、接受或随 terminal session 关闭
- **THEN** 服务端通过既有 message 2100 向精确 TargetVisitorID 发送 state 为 `RETIRED` 的同 identity projection，不登记第二个消息 ID、channel 或路由

#### Scenario: 协议生成与兼容性验证

- **WHEN** 统一协议入口基于新增 enum value 重建 Go 与 Unity C# code 并执行 compatibility/fixture parity
- **THEN** 现有字段编号、message ID、route 和既有 golden 语义保持兼容，未知或 unspecified state 仍被应用层拒绝
