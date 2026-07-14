## Why

PersonalWorld identity、current WorldInstance placement 与 production storage adapters 已可独立查询和验收，但服务端仍缺少临时访客资格的唯一领域 owner。若不先冻结 VisitSession 的邀请、membership、断线恢复和安全返回语义，后续协议与 transport 很容易把 invite、socket presence 或客户端 payload 误当成 world admission 与角色授权。

## What Changes

- 新增 transport-independent VisitSession domain/application core，建立独立 `VisitSessionID`、immutable Owner/PersonalWorld/current assignment 绑定、正 revision、容量与绝对 expiry。
- 建立有界 invite 与 membership 状态机，覆盖 create invite、accept、join、Visitor leave/kick、Owner/Visitor disconnect/reconnect、session/邀请/grace 到期与显式 close；所有调用使用受信绝对时间，不在 domain 内启动 timer/goroutine。
- 固定 Owner 与 Visitor 角色权限：Visitor 不能邀请、踢人、关闭访问、继承 Owner、修改 Owner 世界关键事实或提交奖励/结算事实；未知 gameplay interaction 默认没有授权。
- 定义 connection-binding、session epoch、完整 placement assignment stamp 与 stale callback/timer 防护；assignment 变化、lease 失效、epoch 变化或 Redis 运行态丢失只能关闭/重建访问，不能恢复旧资格。
- 定义 accept 后的 admission intent 与 join qualification，但不创建 bearer credential、协议字段或 transport channel；invite 本身永远不是 gameplay credential。
- 定义明确的 safe-return directive/reason，使 leave、kick、Owner unavailable、session expiry、assignment change 与 dependency loss 能返回到 Visitor 自己 PersonalWorld 或安全入口，而不在本 change 启动 world、发送网络消息或修改持久世界事实。
- 定义消费侧 `VisitSessionStore`、PersonalWorld/placement 查询接口与低基数 operation/outcome 分类，以及严格 snapshot hydration、expected revision、稳定 command identity、replay/conflict/commit-unknown 结果；并发 reference store 与故障注入只存在于测试。
- 增加 unit、table、fuzz 与 race 验收，覆盖容量竞争、重复 accept/join、旧 binding 回调、Owner grace race、stale assignment、权限越界、响应丢失和安全返回。
- 本 change 不实现 MySQL/Redis adapter、migration、Redis key、公开 Protobuf/OpenAPI、admission token、HTTP/WSS/TCP handler/listener、connection registry、RuntimeController、Go 协议测试客户端或 Unity，也不引入 Room、Party、ActivityInstance、地图、任务、资产、奖励和结算。

## Capabilities

### New Capabilities

- `server-visit-session`: 定义 VisitSession identity、invite/membership 生命周期、Owner/Visitor 权限、断线 grace、assignment/epoch 绑定、幂等并发结果与 safe-return 边界。

### Modified Capabilities

无。`personal-world-coop`、`server-personal-world`、`server-world-instance-placement`、`server-session` 与 `server-personal-world-storage` 已冻结上游边界；本 change 通过新的 VisitSession capability 细化并实现这些既有约束，不改变其 requirement。

## 进入、验收与回滚

- 上游基线：`f7728ae` 已交付 PersonalWorld production repository、current placement adapter 与相应主 specs；session core 已提供不可伪造 `AuthContext`、session ID/epoch 和通用 gameplay scope。
- 状态 owner：VisitSession 只拥有 invite、Visitor membership、临时 connection binding、grace、expiry 与 safe-return 结果；PersonalWorld、placement assignment、session authentication、未来 admission credential、transport connection、PlayerState 和 settlement 继续由各自 owner 管理。
- 协议与安全：不新增跨端 schema 或 listener；actor 必须来自受信 `AuthContext`，world/instance/epoch/binding 必须来自 owner provider，客户端声明不能覆盖。Invite/command identity、完整 assignment、connection identity 与潜在 admission material 不进入普通日志或 metrics label。
- 验收：项目 Go 入口的 unit/table/fuzz/race/vet/mod verify、`git diff --check`、依赖/secret/generated/cache hygiene 与全量 OpenSpec strict 全部通过；正式 Composition Root 仍不接线 VisitSession service 或公开 world/visit API。
- 回滚：代码回滚到可运行提交 `f7728ae`；本 change 不创建 production schema/key、后台任务或外部协议，因此无需数据 down、credential 撤销或客户端兼容窗口。

## Impact

- 新增 `server/internal/visitsession` 纯 Go package、领域值、application service、消费侧 ports、错误/outcome 与仅测试 reference store/fakes。
- 复用 `account.PlayerID`、`personalworld.PersonalWorldID`、`placement.AssignmentStamp`、`session.AuthContext`/epoch 和项目 Clock/ID 风格，不复制账号、世界、placement 或认证 owner。
- 更新 `docs/file-structure.md`、`docs/roadmap.md` 与 `server/README.md` 的 VisitSession owner、当前完成边界和后续 protocol/storage adapter 进入条件。
- 为后续 `establish-server-personal-world-protocol` 提供稳定 projection 与 admission intent，但不提前决定 message ID、allowed channel、endpoint、wire encoding 或 bearer credential 格式。
