## ADDED Requirements

### Requirement: PersonalWorld 与 VisitSession 协议必须有唯一且可生成的源
系统 MUST 使用 `ihomeland.world.v1` 与 `ihomeland.visit.v1` Protobuf 源定义 realtime payload，使用 versioned OpenAPI 定义 HTTPS payload，并使用 message、route 与 error registry 分别登记编号、唯一通道和稳定错误。Domain/application MUST NOT 依赖 generated Protobuf type，Go/C# generated projection MUST NOT 提交；proto、OpenAPI、registry、fixtures、descriptor/generation summary 与 compatibility baseline MUST 由统一入口交叉校验。

#### Scenario: 统一入口验证合同
- **WHEN** 开发者运行协议 `verify`
- **THEN** 每个新增 proto full name、HTTP operation、message ID、route、stable error 与 fixture 都能从唯一源解析并通过交叉引用，未提交 generated Go/C# projection

#### Scenario: Handler 自行定义同义 DTO
- **WHEN** transport implementation 尝试为已有 world/visit operation 引入不同字段、enum、时间单位或 error 的本地 DTO
- **THEN** review/compatibility gate 拒绝该双重 source，要求 adapter 映射已冻结合同

### Requirement: Wire projection 必须有界、可兼容且默认不暴露内部授权事实
World/visit wire ID MUST 是不超过 128 bytes 的受校验安全 ASCII，revision/generation MUST 是正无符号 64-bit，enum `0` MUST 为 `UNSPECIFIED` 且 unknown value MUST fail closed。绝对时间 MUST 使用 Unix epoch milliseconds，并 MUST NOT 反向覆盖 domain/store 的 UTC 微秒事实。Public assignment MAY包含 PersonalWorldID、WorldInstanceID、公开 endpoint、generation 与 lease expiry，但 MUST NOT 包含 runtime NodeID、FencingToken 或完整 AssignmentStamp；VisitSession snapshot 的 Visitor 列表 MUST只包含计入 capacity 的 Visitor 且不得重复 Owner，批量 safe-return directive MUST携带目标 VisitorID。通用 VisitSession projection MUST NOT 包含 SessionID/epoch、ConnectionBindingID、nonce、command fingerprint、grace generation 或完整 pending invite 集合。

#### Scenario: Domain 微秒事实映射到 wire
- **WHEN** adapter 将带亚毫秒部分的合法 UTC 微秒 snapshot 编码为 world/visit payload
- **THEN** wire 时间确定性向下截断为 Unix milliseconds，服务端授权、expiry 与持久事实仍使用原始微秒值

#### Scenario: 客户端提交内部 assignment 字段
- **WHEN** request/command payload包含 runtime node、fencing token、完整 assignment 或 session binding字段
- **THEN** schema/validator拒绝该payload且服务端不把它作为current assignment或授权证据

#### Scenario: Unknown enum 或越界 identity
- **WHEN** payload包含未知 lifecycle/role/state 或超过限制、含控制字符的identity
- **THEN** decoder/validation gate返回稳定 validation/protocol error且不调用domain mutation

### Requirement: HTTPS 必须只承担 bootstrap、invite accept 与一次性 admission issuance
系统 MUST 定义受 bearer 认证的 `GET /v1/world/bootstrap`、`POST /v1/visits/{visitSessionId}/invites/{inviteId}/accept` 与 `POST /v1/world/admissions`。每个 operation MUST 登记 body limit、timeout、idempotency 与 stable error；bootstrap MUST 为 `SAFE`，accept/admission MUST 要求稳定 idempotency key。Actor、SessionID/epoch、world、instance 与 role MUST由受信认证/当前领域事实派生；accept body只可携带 expected revision，admission target只可选择 own-world或提供VisitSessionID，不得由payload替换actor/world/instance/role/endpoint。

#### Scenario: 查询 own-world bootstrap
- **WHEN** 有效 bearer 的 Player 查询 bootstrap
- **THEN** response返回该actor自己的PersonalWorld与client-safe current assignment，不能查询另一个Player的世界或泄漏node/fence

#### Scenario: 目标 Visitor 接受 invite
- **WHEN** invite path、bearer actor、expected revision与idempotency key匹配一个pending定向invite
- **THEN** operation映射到已有accept command并返回reservation/visit projection；重试解析为首次结果且不重复占用capacity

#### Scenario: Payload 尝试代替 actor
- **WHEN** accept或admission payload声明PlayerID、SessionID/epoch、WorldID、WorldInstanceID、role、endpoint或fencing token
- **THEN** schema/handler边界拒绝请求，服务端只使用bearer与权威world/membership/assignment

#### Scenario: Admission target 改变但复用 idempotency key
- **WHEN** 调用方用相同idempotency key从own-world target改为另一个VisitSession或反向修改
- **THEN** operation返回稳定idempotency conflict，不签发第二个不同语义credential

### Requirement: World 与 VisitSession realtime message 必须拥有唯一编号和通道
Registry MUST 分配 `world=2000-2099`、`visit=2100-2299` 非重叠owner range，并 MUST 保持 `messages.json` 已发布的2000-2003与2100-2122首批message mapping。每条message MUST恰有一个route，登记owner、Protobuf full name、kind、direction、channel、auth scope、`RELIABLE_ORDERED` QoS、完整envelope max size、rate-policy reference、idempotency与timeout；删除后的编号 MUST进入reserved且不得复用。

#### Scenario: World snapshot 经 TLS/TCP 读取
- **WHEN** 已通过GAMEPLAY connection与有效world admission的client发送`WORLD_SNAPSHOT_REQUEST`
- **THEN** dispatcher只接受message 2000的TLS/TCP REQUEST并以correlated 2001 response返回client-safe snapshot，WSS上的相同ID被拒绝

#### Scenario: Visit invite 经 WSS 通知
- **WHEN** 服务端向目标Visitor发送`VISIT_INVITE_PUSH`
- **THEN** message 2100只能通过CONTROL WSS可靠有序发送，不能在TLS/TCP形成第二个invite入口，也不能直接建立membership或join

#### Scenario: VisitSession mutation 经 TLS/TCP 执行
- **WHEN** client发送open/create-invite/revoke/join/leave/kick/reconnect/close command之一
- **THEN** 只允许registry登记的GAMEPLAY TLS/TCP message ID、direction、size、rate、timeout与幂等组合，错误channel/kind/direction在调用application前被拒绝

#### Scenario: Close notice 与 safe-return 同时发生
- **WHEN** VisitSession关闭且Visitor仍连接gameplay channel
- **THEN** WSS close notice只收敛控制面可见性，TLS/TCP safe-return才表达当前连接的权威返回动作；任一消息均不能代替另一条message的channel或授权语义

### Requirement: Session bearer、ConnectionTicket 与 world admission 必须分层且不可互换
HTTPS bearer MUST只证明account/session lineage；`ConnectionTicket` MUST只授权为单一endpoint/channel建立短期实时连接；world admission MUST单独授权该连接进入一个current PersonalWorld/VisitSession target。Admission MUST短期、一次性且opaque，并 MUST绑定PlayerID、SessionID/epoch、Owner/Visitor role、PersonalWorldID、可选VisitSessionID、`OWN_WORLD`/`JOIN`/`RECONNECT` purpose、完整current AssignmentStamp、endpoint、`TLS_TCP` channel、nonce、issued-at与expiry。`OWN_WORLD` MUST只用于Owner进入自己的current PersonalWorld；首次Visitor join admission MUST绑定active `reserved` membership与reservation deadline，Visitor reconnect admission MUST绑定active `reconnecting` membership与reconnect deadline，三种purpose不得互换；expiry MUST不晚于session、对应membership deadline、assignment lease与配置上限的最早值。

#### Scenario: 只有 gameplay ConnectionTicket
- **WHEN** client已建立带GAMEPLAY scope的TLS/TCP connection但没有有效world admission
- **THEN** connection不得读取world snapshot、join VisitSession或执行任何world/visit command

#### Scenario: Admission 正常消费
- **WHEN** credential的actor/session epoch/role/target/full assignment/endpoint/channel/deadline均匹配当前权威事实且nonce未使用
- **THEN** admission verifier仅允许该credential原子消费一次，并向application提供不可由payload构造的受信qualification

#### Scenario: Join admission 被用于 reconnect
- **WHEN** Visitor把绑定reserved membership与`JOIN` purpose的credential提交给reconnect，或把绑定reconnecting membership与`RECONNECT` purpose的credential提交给首次join
- **THEN** verifier按purpose和current membership state拒绝，不能跨状态复用一次性资格

#### Scenario: Admission replay 或过期
- **WHEN** 同一nonce再次使用、observed time达到expiry，或credential绑定旧session epoch/旧assignment/错误endpoint/channel
- **THEN** verifier必须fail closed并返回对应stable error，不得降级使用bearer、invite、AdmissionIntent或ConnectionTicket继续join

#### Scenario: Credential 布局未冻结
- **WHEN** admission implementation在signed-and-encrypted token与opaque server-side handle之间选择实现
- **THEN** public contract保持opaque且实现仍必须满足全部binding、expiry、atomic consume与安全错误不变量

### Requirement: Realtime command 必须从连接上下文取得 actor 并使用显式 revision/idempotency
C2S request/command MUST从已认证connection与verified admission取得actor、SessionID/epoch、role、world、assignment和ConnectionBindingID。Payload MUST NOT包含actor/account/player/session/epoch/world/instance/role/endpoint/fencing字段；Owner控制目标 MAY使用明确的`target_visitor_id`。每个mutation command的 envelope MUST包含有界command ID；针对已有 VisitSession 的 mutation payload MUST包含正expected revision，`VisitOpenCommand`因尚无aggregate revision而不携带该字段。Request MUST使用request ID，response MUST使用correlation ID，push MUST没有correlation。相同command identity/fingerprint重试 MUST返回首次结果，改变operation/target/revision MUST返回idempotency conflict。

#### Scenario: Owner kick 指定 Visitor
- **WHEN** 当前Owner通过有效admission发送kick command并提供target visitor与expected revision
- **THEN** actor只取自connection，target只作为被操作对象，application执行已有Owner-only kick policy并返回correlated首次结果

#### Scenario: Visitor 伪造 Owner identity
- **WHEN** Visitor在payload中加入Owner PlayerID、role或world字段尝试open、invite、kick或close
- **THEN** schema/dispatcher拒绝伪造字段或application按受信Visitor role返回forbidden，PersonalWorld/VisitSession事实不改变

#### Scenario: System lifecycle 被伪装为客户端 command
- **WHEN** client尝试发送disconnect、grace expiry、session expiry、assignment invalidation或dependency-loss operation
- **THEN** registry没有对应C2S route，只有server-side lifecycle owner可以调用带权威条件的application command

### Requirement: VisitSession wire operation 必须忠实映射已有状态机
协议 MUST只公开已有open、create/revoke invite、HTTP accept、join、leave、kick、reconnect、close与snapshot行为。Join MUST要求已验证admission并重新比较reserved membership、SessionID/epoch、current full assignment与deadline；pending invite、AdmissionIntent或generic gameplay scope MUST不能join。Owner availability、close与safe-return projection MUST保持Owner不可转移、capacity、revision、deadline和deterministic safe-return语义；协议不得创造domain不存在的部分成功或silent recovery。

#### Scenario: Accept 后直接使用 invite join
- **WHEN** Visitor只有invite或accept产生的reservation信息而没有verified admission
- **THEN** `VISIT_JOIN_COMMAND`被拒绝且reserved membership不转换为joined

#### Scenario: Assignment 在 admission 后被替换
- **WHEN** credential或client projection仍引用旧instance而current完整AssignmentStamp已经改变
- **THEN** join/reconnect被拒绝，旧VisitSession按现有system invalidation规则关闭并产生safe-return，不静默迁移到successor

#### Scenario: Owner grace 期间新增成员
- **WHEN** Owner处于unavailable grace且client尝试create invite、accept或join
- **THEN** wire operation映射稳定Owner-unavailable/state-conflict error，不能绕过domain状态机

#### Scenario: Safe-return projection 重放
- **WHEN** close/expiry结果已提交但response或push丢失并以相同command重试
- **THEN** 协议返回首次确定性directive/revision，不改变reason、不重复推进revision且不声称网络迁移已原子完成

### Requirement: World/visit stable error 必须跨 HTTP 与 realtime 一致且默认脱敏
Error registry MUST为world与visit分配独立稳定空间，并 MUST保持 `errors.json` 已发布的2000-2006与2100-2109 code/name/HTTP/retryability mapping，覆盖not-found/not-ready、assignment stale、admission invalid/expired/replayed、world/visit idempotency conflict、invite missing/expired、capacity、state/revision conflict、membership required、Owner unavailable与reconnect expired；权限、通用validation和dependency failure MUST复用既有shared error。HTTP与realtime对同一outcome MUST使用同一code/name/retryability，不得创建同义transport-local error。Public error MUST NOT包含credential、nonce、full assignment、node/fence、session epoch、binding、fingerprint或backend detail。

#### Scenario: 相同 revision conflict 发生于不同 transport
- **WHEN** HTTP invite accept或TLS/TCP VisitSession command提交stale expected revision
- **THEN** 两者返回同一visit revision-conflict stable error与安全correlation，不暴露current内部snapshot或store错误

#### Scenario: Dependency 不可用
- **WHEN** PersonalWorld、placement、VisitSession store或admission nonce store无法证明权威结果
- **THEN** adapter返回共享dependency-unavailable并fail closed，不伪装为not-found、assignment changed、credential consumed或成功

### Requirement: Deterministic fixtures 与 validators 必须覆盖兼容性和负向边界
协议交付 MUST包含每个新增message的deterministic golden envelope/payload、三个HTTP operation的success/error cases，以及当前 codec/registry 可实际拒绝的错误channel/kind/direction/correlation、unknown envelope enum、payload actor字段、越界identity/frame与未登记interaction negative cases；stale assignment/epoch和admission replay/expiry MUST由独立semantic cases描述。Verify MUST验证descriptor full name、owner range、ID/route唯一性、route组合、OpenAPI metadata、canonical decode/re-encode、expected rejection与stable error引用。Admission semantic fixture MUST明确只是issuer/verifier的验收corpus，不得被报告为已实现production签名或nonce消费。

#### Scenario: Golden packet 漂移
- **WHEN** schema或registry变更导致已登记message的canonical bytes、field number、kind或route发生未声明变化
- **THEN** verify失败并要求compatibility decision与fixture更新，不能静默重写baseline

#### Scenario: Negative fixture 被错误接受
- **WHEN** decoder/registry validator接受错误channel、actor字段、unknown enum或越界frame
- **THEN** test失败且变更不能归档

#### Scenario: Semantic admission fixture 被误称为runtime验收
- **WHEN** issuer/verifier/nonce store尚未接入production但文档或测试声称replay protection已经上线
- **THEN** review拒绝该结论并保持production world/visit入口未接线

### Requirement: 未登记 world interaction 必须默认拒绝且不得提前接线
本 capability MUST NOT定义generic interaction/action/mutation message、任意payload、PlayerState/PersonalWorld双写或通用Visitor ACL。每条interaction MUST通过独立OpenSpec登记actor role、mutation owner、settlement owner、唯一channel、message ID、size/rate/idempotency、revision/fencing与transaction边界；未登记行为 MUST默认拒绝。在对应 N0 transport/admission capability 完成前，production Composition Root MUST不注册world/visit HTTP/WSS/TCP入口、admission组件或客户端runtime。

#### Scenario: 客户端发送未知 world action
- **WHEN** client尝试发送未在registry登记的移动、交互、任务、资产、奖励或任意script payload
- **THEN** protocol gate拒绝unknown message且不把payload转交domain、脚本或存储

#### Scenario: 协议合同存在但 transport 尚未通过 N0 验收
- **WHEN** server以production配置启动
- **THEN** 进程继续只开放既有入口，不构造world/visit handler、listener、dispatcher、admission verifier或Unity/Go业务客户端，也不宣称own-world/visit-world可联机

#### Scenario: N0 transport capability 消费合同
- **WHEN** `add-server-http-bootstrap`、`add-server-websocket-control`或`add-server-tcp-gameplay`开始实现
- **THEN** 它们必须映射本capability的operation/message/error/route与credential边界，不得重新分配编号、建立第二通道或放宽actor/admission规则
