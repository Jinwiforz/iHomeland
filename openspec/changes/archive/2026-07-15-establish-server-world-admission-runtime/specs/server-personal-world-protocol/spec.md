## MODIFIED Requirements

### Requirement: Session bearer、ConnectionTicket 与 world admission 必须分层且不可互换
HTTPS bearer MUST只证明account/session lineage；`ConnectionTicket` MUST只授权为单一endpoint/channel建立短期实时连接；world admission MUST单独授权该连接进入一个current PersonalWorld/VisitSession target。Admission MUST短期、一次性且opaque，并 MUST绑定PlayerID、SessionID/epoch、Owner/Visitor role、PersonalWorldID、可选VisitSessionID、`OWN_WORLD`/`JOIN`/`RECONNECT` purpose、完整current AssignmentStamp、endpoint、`TLS_TCP` channel、issued-at与expiry；原子消费 MUST使用credential digest与稳定consume identity，不复用ConnectionTicket nonce。`OWN_WORLD` MUST只用于Owner进入自己的current PersonalWorld；首次Visitor join admission MUST绑定active `reserved` membership与reservation deadline，Visitor reconnect admission MUST绑定active `reconnecting` membership与reconnect deadline，三种purpose不得互换；expiry MUST不晚于session、对应membership deadline、assignment lease与配置上限的最早值。

#### Scenario: 只有 gameplay ConnectionTicket
- **WHEN** client已建立带GAMEPLAY scope的TLS/TCP connection但没有有效world admission
- **THEN** connection不得读取world snapshot、join VisitSession或执行任何world/visit command

#### Scenario: Admission 正常消费
- **WHEN** credential的actor/session epoch/role/target/full assignment/endpoint/channel/deadline均匹配当前权威事实且credential未消费
- **THEN** admission verifier仅允许该credential原子消费一次，并向application提供不可由payload构造的受信qualification

#### Scenario: Join admission 被用于 reconnect
- **WHEN** Visitor把绑定reserved membership与`JOIN` purpose的credential提交给reconnect，或把绑定reconnecting membership与`RECONNECT` purpose的credential提交给首次join
- **THEN** verifier按purpose和current membership state拒绝，不能跨状态复用一次性资格

#### Scenario: Admission replay 或过期
- **WHEN** 同一credential由不同consume identity再次使用、observed time达到expiry，或credential绑定旧session epoch/旧assignment/错误endpoint/channel
- **THEN** verifier必须fail closed并返回对应stable error，不得降级使用bearer、invite、AdmissionIntent或ConnectionTicket继续join

#### Scenario: Credential 布局未冻结
- **WHEN** admission implementation在signed-and-encrypted token与opaque server-side handle之间选择实现
- **THEN** public contract保持opaque且实现仍必须满足全部binding、expiry、atomic consume与安全错误不变量

### Requirement: World/visit stable error 必须跨 HTTP 与 realtime 一致且默认脱敏
Error registry MUST为world与visit分配独立稳定空间，并 MUST保持 `errors.json` 已发布的2000-2006与2100-2109 code/name/HTTP/retryability mapping，覆盖not-found/not-ready、assignment stale、admission invalid/expired/replayed、world/visit idempotency conflict、invite missing/expired、capacity、state/revision conflict、membership required、Owner unavailable与reconnect expired；权限、通用validation和dependency failure MUST复用既有shared error。HTTP与realtime对同一outcome MUST使用同一code/name/retryability，不得创建同义transport-local error。Public error MUST NOT包含credential、consume identity、full assignment、node/fence、session epoch、binding、fingerprint或backend detail。

#### Scenario: 相同 revision conflict 发生于不同 transport
- **WHEN** HTTP invite accept或TLS/TCP VisitSession command提交stale expected revision
- **THEN** 两者返回同一visit revision-conflict stable error与安全correlation，不暴露current内部snapshot或store错误

#### Scenario: Dependency 不可用
- **WHEN** PersonalWorld、placement、VisitSession store或world admission store无法证明权威结果
- **THEN** adapter返回共享dependency-unavailable并fail closed，不伪装为not-found、assignment changed、credential consumed或成功

### Requirement: Deterministic fixtures 与 validators 必须覆盖兼容性和负向边界
协议交付 MUST包含每个新增message的deterministic golden envelope/payload、三个HTTP operation的success/error cases，以及当前 codec/registry 可实际拒绝的错误channel/kind/direction/correlation、unknown envelope enum、payload actor字段、越界identity/frame与未登记interaction negative cases；stale assignment/epoch和admission replay/expiry MUST由独立semantic cases描述。Verify MUST验证descriptor full name、owner range、ID/route唯一性、route组合、OpenAPI metadata、canonical decode/re-encode、expected rejection与stable error引用。Admission semantic fixture MUST明确是issuer/verifier与VisitSession二次校验的验收corpus，不冻结raw credential、consume identity或claims布局。

#### Scenario: Golden packet 漂移
- **WHEN** schema或registry变更导致已登记message的canonical bytes、field number、kind或route发生未声明变化
- **THEN** verify失败并要求compatibility decision与fixture更新，不能静默重写baseline

#### Scenario: Negative fixture 被错误接受
- **WHEN** decoder/registry validator接受错误channel、actor字段、unknown enum或越界frame
- **THEN** test失败且变更不能归档

#### Scenario: Semantic admission fixture 被误称为runtime验收
- **WHEN** semantic corpus未由production issuer/verifier、credential store与VisitSession二次校验逐项执行，但文档或测试声称replay protection已经验收
- **THEN** review拒绝该结论并保持production world/visit入口未接线

### Requirement: 未登记 world interaction 必须默认拒绝且不得提前接线
本 capability MUST NOT定义generic interaction/action/mutation message、任意payload、PlayerState/PersonalWorld双写或通用Visitor ACL。每条interaction MUST通过独立OpenSpec登记actor role、mutation owner、settlement owner、唯一channel、message ID、size/rate/idempotency、revision/fencing与transaction边界；未登记行为 MUST默认拒绝。world admission源码组件与Redis adapter MAY在本change独立实现和验收，但在对应 N0 transport capability 完成前，production Composition Root MUST不注册world/visit HTTP/WSS/TCP入口、不构造admission service graph且不接入客户端runtime。

#### Scenario: 客户端发送未知 world action
- **WHEN** client尝试发送未在registry登记的移动、交互、任务、资产、奖励或任意script payload
- **THEN** protocol gate拒绝unknown message且不把payload转交domain、脚本或存储

#### Scenario: 协议合同与 admission 源码存在但 transport 尚未通过 N0 验收
- **WHEN** server以production配置启动
- **THEN** 进程继续只开放既有入口，不构造world/visit handler、listener、dispatcher、admission service graph或Unity/Go业务客户端，也不宣称own-world/visit-world可联机

#### Scenario: N0 transport capability 消费合同
- **WHEN** `add-server-http-bootstrap`、`add-server-websocket-control`或`add-server-tcp-gameplay`开始实现
- **THEN** 它们必须映射本capability的operation/message/error/route与credential边界，不得重新分配编号、建立第二通道或放宽actor/admission规则
