## Context

PersonalWorld、current WorldInstance assignment、PersonalWorld MySQL storage 与 VisitSession domain/application 已分别建立长期 owner；通用存储 runtime 与 Redis key registry 已就绪，但 VisitSession Redis adapter 尚未实现。现有跨端合同只覆盖 common/account/session/control，客户端目前无法从一个可生成、可登记、可校验的源确定 own-world bootstrap、邀请接受、VisitSession 控制、世界快照、safe-return 与 admission 的字段、编号和唯一通道。

本 change 位于 P0 领域/存储阶段与 N0 transport vertical slice 之间。它只冻结合同，不开放生产入口。设计必须同时满足：domain 不依赖 generated type；HTTP/WSS/TLS-TCP 不出现同一业务双入口；actor 只能来自受信连接上下文；Redis 运行态丢失时 fail closed；客户端不能看到或回传内部 fencing、runtime node、session epoch、connection binding 或 nonce；所有可重新生成的 Go/C# projection 继续忽略提交。

## Goals / Non-Goals

**Goals:**

- 为已有 PersonalWorld、assignment 与 VisitSession 行为建立唯一 `world.v1`/`visit.v1` wire projection。
- 冻结 own-world bootstrap、invite accept 与 world/visit admission 的 HTTPS 合同。
- 分配互不重叠的 world/visit realtime owner range，并为每条消息登记唯一 channel、direction、auth scope、QoS、大小、限流、timeout 与幂等语义。
- 把 session bearer、gameplay `ConnectionTicket` 与一次性 world admission 的权限边界写成可验收合同。
- 提供 deterministic HTTP/realtime fixtures、negative cases 与 registry/descriptor 校验，为后续三条 transport change 提供稳定输入。

**Non-Goals:**

- 不实现 Gin/WSS/TCP handler、listener、dispatcher、connection registry、admission issuer/verifier/nonce store 或 production wiring。
- 不修改 PersonalWorld、placement 或 VisitSession 状态机，不新增 table、Redis key、cleanup worker 或跨 owner transaction。
- 不实现 Go 协议测试客户端、Unity runtime、地图、任务、资产、奖励、Party、Room、ActivityInstance 或 battle transport。
- 不创建 generic world interaction、通用 mutation envelope 或“Visitor 可以执行任意 gameplay command”的 ACL。
- 不提交由 Protobuf 源可重新生成的 Go/C# 文件。

## Decisions

### 1. 协议源按职责分层，领域模型保持独立

`shared/proto/ihomeland/world/v1/` 与 `shared/proto/ihomeland/visit/v1/` 是 realtime payload 的唯一 schema source；`shared/contracts/http/v1/openapi.yaml` 是 HTTPS 的唯一 schema source；`messages.json`、`routes.json` 与 `errors.json` 分别拥有编号、路由和稳定错误。Fixtures 只保存这些源的版本化示例，不成为第四份 schema。

`server/internal/contract` 负责 OpenAPI/registry/fixture 结构和交叉引用，`server/internal/protocol` 负责 descriptor、envelope 与 payload compatibility。PersonalWorld、placement、VisitSession domain/application 不 import generated protobuf；未来 adapter 显式完成 domain snapshot 与 public projection 的双向映射。

相比直接复用 domain struct，这能防止内部字段和微秒精度成为意外公网 ABI；相比每个 handler 自定义 DTO，这能让 HTTP、WSS、TCP 与 Go 测试客户端共享同一 compatibility gate。

### 2. Public projection 只暴露客户端完成流程所需事实

World projection 包含 PersonalWorldID、OwnerPlayerID、lifecycle、revision、创建时间和 client-safe assignment view。Assignment view 只包含 PersonalWorldID、WorldInstanceID、公开 endpoint、assignment generation 与 lease expiry；runtime NodeID、FencingToken、完整 AssignmentStamp 和内部 lease owner 不对客户端公开，也不得由客户端回传作为授权依据。

Visit projection 包含 VisitSessionID、OwnerPlayerID、client-safe assignment、lifecycle、revision、capacity、Visitor 摘要与公开 deadline。Visitor 摘要只表达 PlayerID 与公开 presence state，不重复表达已由列表语义确定的 role，也不把 Owner 重复计入 capacity；SessionID/epoch、ConnectionBindingID、grace generation、command fingerprint、pending invite 集合和 AdmissionIntent 不进入通用 snapshot。定向 invite push 只发送给目标 Visitor；Owner 创建 invite 的 response 可以返回该 invite 的公开摘要。Safe-return 列表必须携带目标 VisitorID，确保批量 close 结果可以稳定关联到每个受影响 Visitor。

所有 ID 使用不超过 128 bytes 的安全 ASCII string，revision/generation 使用无符号 64-bit。enum 的 `0` 值固定为 `UNSPECIFIED`，接收 unknown enum 必须拒绝。跨 wire 的绝对时间统一为 Unix epoch milliseconds；domain UTC 微秒向 wire 转换时向下截断到毫秒，wire 时间只用于展示、deadline 与请求前置条件，绝不能反向覆盖 MySQL/Redis/domain 的微秒事实。

### 3. HTTPS 只承载 bootstrap、invite accept 与 admission issuance

OpenAPI 增加三个受 bearer 认证的 operation：

| Method/path | 职责 | body limit | timeout | 幂等 |
|---|---|---:|---:|---|
| `GET /v1/world/bootstrap` | 查询 actor 自己的 PersonalWorld 与当前 client-safe assignment | 0 | 5000 ms | `SAFE` |
| `POST /v1/visits/{visitSessionId}/invites/{inviteId}/accept` | 目标 Visitor 接受定向 invite 并创建 reservation | 4096 bytes | 5000 ms | `IDEMPOTENCY_KEY_REQUIRED` |
| `POST /v1/world/admissions` | 为 own-world 或已 reservation 的 visit-world 签发一次性 admission | 4096 bytes | 5000 ms | `IDEMPOTENCY_KEY_REQUIRED` |

Accept body 只包含正 `expectedRevision`；稳定 command identity 来自必填 `Idempotency-Key`，actor、SessionID/epoch 与目标 invite 由 bearer/path 解析。Admission body 使用封闭 one-of target：`ownWorld` 不含客户端 world/instance/role，`visitWorld` 只含 VisitSessionID；server 必须从当前 auth、membership 和 current assignment 派生其余 binding。Admission response 只公开 opaque credential、`TLS_TCP` endpoint、角色、`OWN_WORLD`/`JOIN`/`RECONNECT` purpose 与 expiry，不公开 nonce、session epoch、node 或 fence。

Admission issuance 的同 key 重试必须解析为首次结果；相同 key 改变 target 返回 idempotency conflict。Credential 已消费时不得因 HTTP 重试恢复其资格。相比让 invite accept 直接返回 credential，独立 issuance 能在连接前再次确认 current assignment、membership、session epoch 与 endpoint；相比让客户端提交完整 target，它消除了 identity substitution。

### 4. World 与 VisitSession 使用固定、互不重叠的 Message ID 范围

`messages.json` 新增 `world=2000-2099` 与 `visit=2100-2299` owner range。首批编号固定如下；删除后进入 `reserved`，不得复用：

| ID | Message | Channel | Kind/direction |
|---:|---|---|---|
| 2000 | `WORLD_SNAPSHOT_REQUEST` | TLS/TCP | REQUEST C2S |
| 2001 | `WORLD_SNAPSHOT_RESPONSE` | TLS/TCP | RESPONSE S2C |
| 2002 | `WORLD_SNAPSHOT_PUSH` | TLS/TCP | PUSH S2C |
| 2003 | `WORLD_ASSIGNMENT_CHANGED_PUSH` | WSS | PUSH S2C |
| 2100 | `VISIT_INVITE_PUSH` | WSS | PUSH S2C |
| 2101 | `VISIT_OWNER_AVAILABILITY_PUSH` | WSS | PUSH S2C |
| 2102 | `VISIT_CLOSED_NOTICE_PUSH` | WSS | PUSH S2C |
| 2103/2104 | `VISIT_OPEN_COMMAND/RESPONSE` | TLS/TCP | COMMAND C2S / RESPONSE S2C |
| 2105/2106 | `VISIT_CREATE_INVITE_COMMAND/RESPONSE` | TLS/TCP | COMMAND C2S / RESPONSE S2C |
| 2107/2108 | `VISIT_REVOKE_INVITE_COMMAND/RESPONSE` | TLS/TCP | COMMAND C2S / RESPONSE S2C |
| 2109/2110 | `VISIT_JOIN_COMMAND/RESPONSE` | TLS/TCP | COMMAND C2S / RESPONSE S2C |
| 2111/2112 | `VISIT_LEAVE_COMMAND/RESPONSE` | TLS/TCP | COMMAND C2S / RESPONSE S2C |
| 2113/2114 | `VISIT_KICK_COMMAND/RESPONSE` | TLS/TCP | COMMAND C2S / RESPONSE S2C |
| 2115/2116 | `VISIT_RECONNECT_COMMAND/RESPONSE` | TLS/TCP | COMMAND C2S / RESPONSE S2C |
| 2117/2118 | `VISIT_CLOSE_COMMAND/RESPONSE` | TLS/TCP | COMMAND C2S / RESPONSE S2C |
| 2119/2120 | `VISIT_SNAPSHOT_REQUEST/RESPONSE` | TLS/TCP | REQUEST C2S / RESPONSE S2C |
| 2121 | `VISIT_SNAPSHOT_PUSH` | TLS/TCP | PUSH S2C |
| 2122 | `VISIT_SAFE_RETURN_PUSH` | TLS/TCP | PUSH S2C |

WSS route 使用 `CONTROL` scope，只表达邀请、Owner availability、assignment changed 与 VisitSession closed 的控制面事实；它不能完成 join、改变 world state 或确认 safe-return。TLS/TCP route 使用 `GAMEPLAY` scope，并在 domain authorization 前额外要求有效 world admission。`VISIT_CLOSED_NOTICE_PUSH` 只供控制面 UI 收敛，`VISIT_SAFE_RETURN_PUSH` 才是当前 gameplay connection 上的权威返回指令；两者 payload、consumer 与副作用不同，不是同一消息的双通道入口。

所有 route 使用 `RELIABLE_ORDERED`。请求/命令完整 envelope 上限为 4 KiB，普通 response/push 为 16 KiB，world/visit snapshot response/push 为 64 KiB；C2S command timeout 为 5 秒，snapshot request 为 10 秒，S2C push timeout 为 0。WSS push 复用 `server_control`，world read 使用 `world_read`，VisitSession mutation 使用 `visit_command`，server gameplay push 使用 `server_world` 稳定 rate-policy reference；具体 token-bucket 参数由启用正式 listener 的 N0 change 以容量证据配置。

### 5. 三层 credential 不得相互替代

HTTPS bearer 只证明 account/session lineage；`ConnectionTicket` 只允许为单一 endpoint/channel 建立一条短期 TLS/TCP connection；world admission 才允许该 connection 进入一个 current PersonalWorld/VisitSession target。三者缺一不可，且 gameplay scope 不是 domain role。

Admission 必须是短期、一次性、opaque credential，并由 issuer/verifier 绑定 PlayerID、SessionID/epoch、Owner/Visitor role、PersonalWorldID、可选 VisitSessionID、`OWN_WORLD`/`JOIN`/`RECONNECT` purpose、完整 current AssignmentStamp、endpoint、`TLS_TCP` channel、随机 nonce、issued-at 与 expiry。首次 Visitor join admission 必须绑定 active `reserved` membership 与 reservation deadline；Visitor reconnect admission 必须绑定 active `reconnecting` membership 与 reconnect deadline，二者不得互换。Expiry 不得晚于 session、对应 membership deadline、assignment lease 或配置上限中的最早值。Verifier 必须原子 consume nonce，并在 join/reconnect 前重新确认 current session epoch、membership state、purpose、assignment 与 deadline；replay、expiry、错误 purpose/endpoint/channel、旧 epoch 或旧 assignment 全部 fail closed。

OpenAPI 的 `WorldAdmissionResponse` 是 admission issuance 的唯一公开 schema；realtime join/reconnect payload 原样携带该响应产生的安全 ASCII opaque credential string，避免跨语言 Base64/UTF-8 二次编码歧义。合同不定义可伪造的 claims JSON，也不要求暴露签名/加密布局。Admission implementation 可以选择 signed-and-encrypted self-contained token 或 opaque server-side handle，但必须满足同一 binding 与 consume 契约。相比现在提前选定密码学格式，这保留 key rotation/nonce store 设计空间，同时不削弱 transport 的验证输入。

### 6. Realtime command 只携带业务目标，不携带 actor

所有 C2S request/command 从连接 `AuthContext` 与已验证 admission 取得 actor、SessionID/epoch、role、world、assignment 和 connection binding。Payload 禁止 `actor_id`、`account_id`、`player_id`、`session_id`、`session_epoch`、`world_id`、`world_instance_id`、`role`、`endpoint`、`fencing_token`；Owner kick/create invite 可以携带明确命名的 `target_visitor_id`，但它只表示操作目标。

Envelope 为 mutation command 携带有界 `command_id`，针对已有 VisitSession 的 command payload 携带正 `expected_revision`；`VisitOpenCommand` 尚无 aggregate revision，因此不伪造前置版本，其 operation/target/policy 由 command fingerprint 约束。Response 回显 correlation 并返回首次提交后的 revision/projection；request 使用 `request_id`，response 使用相同 correlation；push 无 correlation。Command registry 使用 `COMMAND_ID`，request 使用 `REQUEST_ID`，response 使用 `CORRELATION_ID`，push 使用 `NONE`。相同 command identity 的重试必须得到首次结果，相同 identity 改变 operation/target/expected revision 必须冲突。

Open/create/revoke/join/leave/kick/reconnect/close 只映射已有 VisitSession application command。Disconnect、Owner grace、expiry、assignment invalidation 与 dependency loss 是 server-side lifecycle callback，不新增客户端伪装的 system command。Join payload只提交 admission credential；invite、AdmissionIntent、ConnectionTicket 或客户端 role 声明均不能替代 verified qualification。

### 7. Stable error 既可映射 HTTP，也可进入 realtime envelope

`errors.json` 增加 world `2000-2099` 与 visit `2100-2199` 稳定错误空间，首批编号固定如下：

| Code | Name | HTTP | Retryable |
|---:|---|---:|---|
| 2000 | `WORLD_NOT_FOUND` | 404 | false |
| 2001 | `WORLD_NOT_READY` | 409 | true |
| 2002 | `WORLD_ASSIGNMENT_STALE` | 409 | false |
| 2003 | `WORLD_ADMISSION_INVALID` | 401 | false |
| 2004 | `WORLD_ADMISSION_EXPIRED` | 401 | false |
| 2005 | `WORLD_ADMISSION_REPLAYED` | 409 | false |
| 2006 | `WORLD_IDEMPOTENCY_CONFLICT` | 409 | false |
| 2100 | `VISIT_NOT_FOUND` | 404 | false |
| 2101 | `VISIT_INVITE_NOT_FOUND` | 404 | false |
| 2102 | `VISIT_INVITE_EXPIRED` | 410 | false |
| 2103 | `VISIT_CAPACITY_EXCEEDED` | 409 | false |
| 2104 | `VISIT_STATE_CONFLICT` | 409 | false |
| 2105 | `VISIT_REVISION_CONFLICT` | 409 | false |
| 2106 | `VISIT_IDEMPOTENCY_CONFLICT` | 409 | false |
| 2107 | `VISIT_MEMBERSHIP_REQUIRED` | 403 | false |
| 2108 | `VISIT_OWNER_UNAVAILABLE` | 409 | false |
| 2109 | `VISIT_RECONNECT_EXPIRED` | 410 | false |

权限错误继续复用 `AUTH_FORBIDDEN`，畸形输入复用 `VALIDATION_FAILED`，依赖故障复用 `DEPENDENCY_UNAVAILABLE`。

Public error 只包含稳定 code/name、safe message key、retryable 与 HTTP status/correlation；不得泄漏 token、nonce、完整 assignment、internal node/fence、session epoch、binding、command fingerprint 或后端错误。HTTP 和 realtime 对同一领域 outcome 使用同一稳定 error，不各自创建同义编号。

### 8. Fixtures 验证 wire compatibility，不伪装 production security implementation

HTTP cases 增加 bootstrap、accept 与 admission 成功/失败样例；realtime golden 覆盖每个新增 message 的 deterministic envelope/payload；negative fixtures 覆盖当前 codec/registry 可实际拒绝的错误 channel、kind/direction/correlation、unknown envelope enum、identity/frame boundary 与未登记 interaction。Admission semantic fixtures 另行描述 stale assignment/epoch、admission expiry/replay 的输入与预期 stable error；业务 payload 的 enum/revision/time 校验由对应 transport adapter 接入时实现并测试。

`verify` 必须证明：所有 proto 被 descriptor 收录；message full name 存在且 ID/owner/range 唯一；每条 message 恰有一条 route；request/command/response/push 的 correlation 与幂等组合合法；OpenAPI operation 均有 body limit/timeout/idempotency/error；golden 可 decode/re-encode为相同 canonical bytes；negative case 被预期 gate 拒绝；fixture 引用的 stable error 存在。

Admission semantic fixture 是后续 issuer/verifier 的强制验收 corpus；本 change 只校验其结构、binding 组合与 expected error，不声称已经完成签名、nonce 原子消费或 runtime replay 防护。

### 9. 未登记 interaction 默认拒绝

本 change 不分配 interaction message owner/range，不创建 `WORLD_COMMAND`、`INTERACT`、`ACTION`、任意 payload 或通用 mutation RPC。World realtime 仅有 snapshot/read 与 assignment notification；VisitSession realtime 仅有已存在的访问控制生命周期。

未来 interaction change 必须逐条登记 actor role、mutation owner、settlement owner、allowed channel、message ID、payload limit、rate policy、idempotency、revision/fencing 与跨 owner transaction，并为 Visitor 权限给出明确产品验收。没有该登记时 dispatcher/application 必须默认拒绝，而不是把 unknown payload转交脚本或 domain。

## Risks / Trade-offs

- [首批 VisitSession 消息数量较多，registry review 成本上升] → 每个编号只映射一个已经存在的 application operation；system callback 与未来 interaction 不进入公网合同，避免后续兼容性拆分。
- [WSS close notice 与 TCP safe-return 容易被误认为双写] → 固定不同 payload 和职责；WSS 只更新控制面可见性，TCP 才驱动当前 gameplay connection 返回，任何一方都不能代替另一方授权。
- [Public assignment 不含 fence/node，客户端无法调试内部 placement] → 通过 correlation 与结构化服务端日志关联；不为可观测便利泄漏写授权或拓扑 identity。
- [Admission 密码学与 nonce store 尚未实现] → production graph 保持无 world/visit endpoint；fixtures 作为后续 change 的强制输入，不把合同测试宣称为 runtime security。
- [微秒事实映射到毫秒会损失精度] → wire 值只做展示和 fail-closed deadline；服务端授权始终使用原始 domain/store 微秒事实，mapping tests 固定截断语义。
- [Exact rate 数值尚无容量证据] → 当前 route 冻结稳定 policy reference；正式 listener change 必须在启用前提供可机读参数、维度与 overload 行为。
- [OpenAPI idempotency key 需要扩展现有 validator] → 将其作为显式合同枚举和 fixture header处理，不把 credential issuance标为可安全盲重试。

## Migration Plan

1. 新增 world/visit proto 源及公开 projection，扩展 proto lint/descriptor 注释与 compatibility baseline。
2. 扩展 OpenAPI 与 HTTP fixtures，登记 bootstrap、invite accept、admission issuance 和 stable errors。
3. 分配 owner range，登记全部 realtime message/route，并增加 golden/negative/semantic fixtures。
4. 扩展 contract/protocol validators 与测试，更新 registry、协议治理、网络、文件结构、路线图和服务端 README。
5. 运行统一 proto verify、Go unit/fuzz/race/vet/mod verify、OpenSpec strict 和仓库卫生检查；保持 production Composition Root 不变。

该变更完全 additive，当前没有已部署的 world/visit endpoint 或历史客户端需要迁移。回滚时删除新增源、registry 条目、fixtures 与 validator 分支即可；已分配过并进入共享历史的 message/error ID 必须转入 `reserved`，不能重新解释或复用。

## Open Questions

无。Admission 的签名/加密格式、nonce store、正式 rate-policy 数值、listener/dispatcher 与 connection binding 由后续独立 change 决定，但其必须满足本设计冻结的 public contract 与安全不变量。
