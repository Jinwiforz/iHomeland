## Context

`internal/visitsession` 已经用纯 Go 定义完整 aggregate、snapshot hydration、`VisitSessionStore` outcome/result contract 与并发 reference store。Production storage 目前只有 Account、Session、PersonalWorld 和 Placement adapters；`server/README.md` 与 `docs/file-structure.md` 也明确说明 VisitSession Redis adapter、cleanup owner 和 admission credential 尚不存在。现有 snapshot constructor 可以恢复 invite 与 membership，但 `AuthBinding` 和非凭据 `AdmissionIntent` 缺少 store hydration 入口，因此 production codec 不能绕过私有字段完成合法恢复。

这不是简单的 cache：一次 create/transition 必须同时决议全局 CommandID replay、world active 唯一索引、expected revision、完整 snapshot/result 和 terminal index removal。响应丢失后，首次完整 AdmissionIntent 或 SafeReturnDirective 仍必须可重放；另一方面 Redis 只保存可失效运行态，flush 后旧访问资格必须整体失效，不能从 MySQL 或进程内 memory 补回。

当前共享 Redis runtime 是 standalone 拓扑，已有安全 keyspace、definition registry、owner Lua、TTL helper、failure classifier、真实 Docker harness 与 response-loss 测试模式。本 change 延续这些模式，不引入 Redis Cluster、第二套 client 或新的第三方编码库。

## Goals / Non-Goals

**Goals:**

- 实现既有 `VisitSessionStore` 的 production Redis adapter，逐项保持领域 outcome、完整 result 与 fail-closed 契约。
- 让 active index、snapshot 和 replay 在单个 Lua 线性化点原子变化，并验证多 adapter 并发与响应丢失。
- 为 schema、大小、TTL、恢复、清理、观测和字段中文短注释建立可维护的长期基线。
- 复用正式 storage runtime 与 verification，但保持公开业务面关闭。
- 修正路线图缺失的进入门，使 world admission runtime 在本 change 之后、HTTP 之前独立交付。

**Non-Goals:**

- 不实现 admission credential 的签名/加密、nonce store、issuer/verifier或`JoinQualification`公开构造。
- 不实现 HTTP、WSS、TLS/TCP、connection registry、safe-return网络side effect或Go协议客户端。
- 不构造正式VisitSession service graph，不启动语义expiry/cleanup goroutine。
- 不新增MySQL table/migration，不把Redis运行态提升为持久事实。
- 不建立尚无production consumer的player membership/presence index，不支持Redis Cluster。
- 不修改VisitSession领域状态机、OpenAPI、Protobuf、route/error/message registry或fixtures；只补充严格校验的`AuthBinding`与非凭据`AdmissionIntent` hydration constructor，且不开放`JoinQualification`构造。

## Decisions

### 1. 使用三个 owner-specific key family，而不是一个万能 aggregate blob

实现登记以下逻辑 schema：

- `ih:<env>:visitsession:active:<personalWorldID>`：PersonalWorld 到唯一 active VisitSessionID 的索引。
- `ih:<env>:visitsession:session:<visitSessionID>`：完整 current/terminal snapshot与Lua比较所需metadata。
- `ih:<env>:visitsession:command:<commandID>`：首次create/mutation fingerprint、kind与完整result。

Snapshot/result 使用确定性、versioned codec编码；Lua需要比较的 schema version、identity、world、revision、lifecycle、fingerprint和expiry保留为明确Hash fields，复杂projection保留为有大小上限的canonical payload。Session Hash另存覆盖ID、Owner、World、完整assignment、capacity及创建/到期时间的`facts` SHA-256，Commit必须与current值原子比较，从而不能用另一个自洽payload替换不可变事实。Go codec必须在调用script前后用领域constructor/hydration验证完整形状，Lua只承担线性化所需的交叉绑定与CAS，不复制领域状态机。

选择独立command key，是为了让旧command在后续revision变化后仍能返回首次完整结果，同时避免把数量不定且单项较大的replay塞入一个无限增长Hash。CommandID由现有领域契约定义为全局store索引；key/value仍默认脱敏，fingerprint只用于精确比较。

未选择：

- 单一session JSON key：无法独立保留全部历史command结果，后续mutation会覆盖旧replay。
- Redis Stream/audit log：引入无consumer的持久日志语义和额外trim/offset ownership。
- player membership index：当前`VisitSessionStore`没有该查询，预建会产生双写和cleanup责任。

### 2. Create、ResolveActive 与 Commit 分别使用固定 owner Lua script

`Create` script按以下顺序执行：读取command并决议replay/conflict；验证active index和对应session一致；没有active时同时写candidate snapshot、active index和create result。已有合法active返回`existing`，不写当前command replay。

`ResolveActive` script在同一Redis执行点读取index与session并校验world、identity、lifecycle；`FindByID`读取单session但仍执行完整codec/hydration。这样close与resolve不会通过pipeline窗口返回“旧index+新terminal snapshot”的伪一致结果。

`Commit` script按以下顺序执行：读取command并决议replay/conflict；读取session并比较identity/revision；验证target metadata；写target与command result；若target closed，仅在active index仍指向该session时删除index。无target probe只用于解析replay、idempotency conflict、not-found或revision conflict；若revision仍匹配则返回invalid-state且不写success replay。Capacity/stale等policy结果由application在生成record前决议，不由storage从JSON重复推导。

脚本和parser使用固定字符串code，不把uint64 revision/generation/fence经Lua `tonumber` 转为IEEE-754 double；Go与Lua共享canonical十进制和UTC Unix微秒字段约束。当前多key script依赖项目既有standalone Redis，未来若引入Redis Cluster必须由独立change设计hash tag/slot和迁移。

未选择WATCH/MULTI客户端循环，因为冲突重试会扩大commit-unknown窗口，并容易在application callback与Redis事务之间形成隐式重放。

### 3. 物理TTL统一锚定`session_expires_at + replay_retention`

每个session、active index和归属该session的command key都使用同一absolute physical expiry：领域`session_expires_at`加配置的`replay_retention`。Retention范围为1分钟至24小时；转换为Redis毫秒时向后取整，保证物理key不会早于微秒领域deadline消失。每次成功mutation可刷新相关current key的同一absolute expiry，但不能滑动延长session资格。

这使早期command和临近expiry的terminal command都至少保留到session自然到期后的相同有界窗口，响应丢失可确定性解析。Closed transition立即删除active index，terminal snapshot与全部command留到TTL；未closed但已过领域deadline的key可能仍存在，application必须显式执行expiry command，不能用TTL判断资格。

本change不启动deadline scheduler。正式service graph接线时必须注册可取消、可等待、无`KEYS`扫描的语义cleanup owner；在那之前没有public route会创建VisitSession。若未来cleanup未执行而物理TTL到达，运行态整体丢失并fail closed，但不能声称safe-return side effect已完成。

未选择“deadline到点立即TTL删除”，因为这会绕过领域close mutation并丢失safe-return/replay结果；也未选择永久保存terminal result，因为Redis不是审计数据库。

### 4. Codec先验证完整领域值，Lua只接受有界canonical输入

Adapter在任何Redis调用前验证record、snapshot、result、排序、cross-binding与encoded size；读取时拒绝unknown version、unknown enum、重复/缺失field、非canonical整数/时间、超限集合、错误payload kind和矛盾result。Payload采用项目内手写稳定DTO/codec，不序列化带私有字段的领域struct，不依赖generated protocol，也不使用`map[string]any`。

领域包新增两个仅表达存储恢复的严格constructor：`HydrateAuthBinding`从PlayerID、SessionID、epoch与ConnectionBindingID恢复snapshot binding；`HydrateAdmissionIntent`从完整reservation lineage、assignment与expiry恢复非凭据结果。二者执行与原始aggregate相同的不变量校验，但不会创建AuthContext、credential或`JoinQualification`，因此不能把Redis/payload数据提升为连接资格。

Redis只提供毫秒TTL，领域所有时间仍保存为UTC Unix微秒字符串；TTL向后取整不改变snapshot时间。ID作为高熵/受校验ASCII segment构造key，默认`String`/`GoString`/`LogValue`只暴露pattern。

### 5. 依赖错误只在 owner 已证明未写入时收窄为 not-committed

执行 Lua 前的本地校验失败以及 preflight 已经观察到的 context 取消可映射 not-committed。Owner Lua 的固定 `defect` 分支全部位于首次写入之前，完整 reply 因此也能证明本次未提交；该结果返回 not-committed 和 dependency defect。Timeout/cancel/EOF、runtime script error、未知 reply 或响应解析失败不能证明写入阶段，必须保守返回 commit-unknown。成功/冲突 reply 必须是固定形状且由 owner parser 重新验证，generic client retry 保持关闭。

Read 操作可返回 not-found 或 dependency error；任何索引缺失一半、missing TTL、corrupt/unknown value 都作为 dependency defect，adapter 不自动删除“修复”。Redis 进程重启后可以读取 Redis 自身仍保留的合法运行态；`flush` 或 key 丢失后不从 MySQL、日志或 memory 补回，旧连接/admission 资格一律失效。

### 6. 只接入storage definitions与verification，不接入业务service graph

`Definitions()`与`New(...)`沿用现有storage package风格，复用调用方提供的共享client/keyspace/clock/observer。根storage registry合并definitions，Docker storage verification直接构造adapter并验证retention；正式Composition Root保持不变，不注册VisitSession service、timer、listener或route。这样本change可在没有网络和admission的条件下独立验收，也不会用未完成组件伪装visit-world可用。

Roadmap在D1与HTTP之间增加：VisitSession storage（本change）和world admission runtime（下一独立change）。Admission change消费本adapter与既有semantic fixtures，但不能反向修改本change的snapshot/replay owner。

## Risks / Trade-offs

- [每个command保存完整result，Redis占用高于只存当前snapshot] → 对单value设置严格预算，全部key有统一absolute TTL；未来用实际profile决定是否做兼容压缩，不能牺牲完整replay。
- [standalone多key Lua不能直接迁移到Redis Cluster] → 当前明确锁定既有拓扑；集群化必须独立设计slot、迁移和故障验收。
- [物理TTL晚于领域deadline，陈旧key短期仍可见] → 所有qualification由application重新校验deadline/assignment；存在key从不等于有效资格。
- [语义 cleanup 未执行时，已过期 active index 会在物理 retention 窗口内拒绝同 world 新建 session] → Create 不能跳过 close/safe-return 语义直接覆盖；当前 public transport 保持关闭，完整 service graph 接线前必须交付可取消、可等待的 deadline cleanup owner。
- [本change不运行semantic cleanup] → public transport继续关闭；完整slice接线前必须提供deadline owner和safe-return side effect流程。
- [corrupt active index会阻断该world新建session] → fail closed并提供固定指标，不自动覆盖可能仍有效的资格；运维可清理可失效namespace。
- [全局CommandID碰撞会拒绝不同请求] → 保持既有domain contract；ID由受信边界生成高熵namespaced值，不在storage层改变scope。

## Migration Plan

1. 增加VisitSession definitions、codec、scripts、store和observer，不改变现有runtime行为。
2. 更新Redis字段字典、文件结构、README与roadmap，明确新增进入门和未开放边界。
3. 将definitions合并进共享registry，并让storage verification在隔离namespace构造adapter。
4. 运行unit/contract/fuzz/race与真实Docker Redis的并发、response-loss、restart/flush/corruption测试。
5. 保持正式Composition Root不构造VisitSession service；下一change实现world admission runtime后再进入HTTP。

回滚时删除adapter与definition合并即可恢复本change前进程；没有公开writer和持久migration。测试或未来环境遗留key均带TTL，也可由既有隔离run cleanup按namespace清除。

## Open Questions

无。Redis Cluster、player membership index、semantic deadline scheduler与admission credential均有意留给存在明确consumer和独立验收边界的后续change。
