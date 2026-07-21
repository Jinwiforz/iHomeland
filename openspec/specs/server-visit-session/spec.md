# Server VisitSession 规格

## Purpose

定义服务端个人世界临时访客资格、邀请与 membership 生命周期、Owner/Visitor 权限、断线恢复、幂等并发和 safe-return 行为。

## Requirements

### Requirement: VisitSession identity 与 owner binding 必须稳定且不可转移
服务端 MUST 使用独立、受校验且由服务端生成的 VisitSessionID 标识一次临时访问 aggregate，并 MUST 不可变绑定 `account.PlayerID` Owner、Owner 的 active PersonalWorldID 与创建时完整 current AssignmentStamp。VisitSession MUST NOT 把 Owner client 视为网络主机，不得把 VisitSessionID、PlayerID、PersonalWorldID、WorldInstanceID、connection binding、PartyID、RoomID 或 ActivityInstanceID 相互转换。Owner MUST NOT 因 disconnect、join order、latency、capacity 或 Visitor 状态而转移。Owner 对 own-world gameplay connection 显式 Open 时，若 world active index 指向绑定旧 AssignmentStamp 的 VisitSession，application MUST 先通过已有 system invalidation 取得并提交权威终态结果、发布完整 safe-return，再以同一 Open CommandID 重新解析 current active session；不得就地改写旧 assignment、迁移旧资格或依赖客户端重复点击。

#### Scenario: Owner 为 current world 开启访问
- **WHEN** 受信 Owner AuthContext 对自己的 active PersonalWorld 请求开启 VisitSession，且 placement 返回 current active、lease 有效的完整 assignment
- **THEN** 服务端创建或解析该 world 唯一 active VisitSession，固定 Owner/world/assignment binding，Visitor 或客户端 payload 不能改写这些身份

#### Scenario: Assignment 已经改变
- **WHEN** 旧 VisitSession 绑定的完整 AssignmentStamp 与 PersonalWorld 当前 assignment 不同
- **THEN** 旧 VisitSession 不得迁移或复活到 successor，任何新 accept/join/reconnect 均被拒绝；独立 system invalidation 只有重新取得 missing、expired、非 active 或不同完整 stamp 的权威证据后才能关闭并产生安全返回结果

#### Scenario: 重启后 Owner 显式重新开放访问
- **WHEN** 进程退出前未执行 assignment loss callback，重启后的 world active index 仍指向旧 VisitSession，且 placement 已提供更高 generation/fence 的 current assignment
- **THEN** 首次 Owner Open 先以稳定 system CommandID 原子关闭旧 VisitSession并发布其完整 terminal result，再以原始 Open CommandID 创建或解析只绑定 current assignment 的唯一 active VisitSession；旧 Owner gameplay connection 已不存在时不得向 successor Owner 投递 predecessor assignment changed，客户端无需清理 Redis 或重复点击

#### Scenario: 恢复流程保持可观测且不会中断业务响应
- **WHEN** stale Open 恢复提交或解析旧 VisitSession 终态
- **THEN** 服务端以固定 `stale_open_reconcile` operation 记录 applied、ignored、stale 或 failed，metrics 封闭标签必须接受该 operation，且观测不得 panic、改变提交结论或阻止随后一次 Open

#### Scenario: 恢复提交结果不确定
- **WHEN** 旧 VisitSession invalidation 返回 dependency、dependency defect 或 commit-unknown
- **THEN** application 返回低敏依赖失败且不得继续创建新 VisitSession、替换 CommandID或猜测终态已提交；相同 system command 的后续权威 replay仍能返回首次完整结果

#### Scenario: 并发 Open 恢复同一旧 session
- **WHEN** 多个 Owner gameplay command 同时发现同一旧 AssignmentStamp VisitSession
- **THEN** revision CAS、稳定 invalidation identity与active-world唯一索引保证旧终态最多提交一次、safe-return副作用按首次结果去重且最多存在一个绑定current assignment的active VisitSession

#### Scenario: Visitor 被当作新 Owner
- **WHEN** Owner 断线而某个 Visitor 最早加入、延迟最低或是唯一在线成员
- **THEN** WorldOwnerID 与 VisitSession Owner 保持不变，Visitor 不获得 invite、kick、close 或 Owner world mutation 权限

### Requirement: VisitSession snapshot、revision、capacity 与时间必须严格有界
VisitSession MUST 使用封闭的 `open`、`owner_grace`、`closed` lifecycle、从 1 开始且每次首次 mutation 精确增加一的 revision、1-32 Visitor capacity、最多 64 个 pending invite，以及 UTC 微秒 created/session/invite/reservation/grace/reconnect deadlines。Policy MUST 将 session lifetime 限制为 1 分钟至 24 小时、invite lifetime 限制为 1 秒至 1 小时、join reservation 限制为 1 秒至 2 分钟、Owner grace 限制为 1 秒至 5 分钟、Visitor reconnect grace 限制为 1 秒至 2 分钟；后四项配置值 MUST 分别作为 application 首次生成 target 时的 deadline 上限。Capacity MUST 只计算 `reserved`、`joined` 与 `reconnecting` membership，不计算 Owner 或 pending invite。Hydration MUST 将合法非零时间移除单调分量并规范为 UTC 微秒，同时拒绝 revision 0、unknown state、重复 identity、越界集合、无效 assignment、Owner 同时作为 Visitor、缺失的必需时间与互相矛盾的 deadline；返回集合 MUST 使用稳定顺序和不可变副本。

#### Scenario: Hydrate 合法 session snapshot
- **WHEN** store 返回完整 immutable binding、正 revision、有界 invite/membership 集合和相互一致的 UTC 微秒 deadline
- **THEN** domain 恢复等价 VisitSession，所有 accessor/projection 返回值副本且不会暴露可修改内部集合

#### Scenario: 两个 Visitor 竞争最后容量
- **WHEN** 两个不同 Visitor 以同一 expected revision 并发 accept，当前只剩一个 capacity slot
- **THEN** 最多一个 accept 提交 reserved membership，另一个返回 revision/capacity conflict，active membership 数量永不超过 capacity

#### Scenario: observedAt 到达 deadline
- **WHEN** 受信 observedAt 等于 invite、reservation、grace、reconnect 或 session absolute deadline
- **THEN** 对应资格被视为已过期，不能因为 timer 尚未执行或底层 key 尚存在而继续 accept、join 或 reconnect

#### Scenario: Deadline 超过已配置 policy 上限
- **WHEN** 首次 mutation 提供的 invite、reservation、Owner grace 或 Visitor reconnect deadline 超过对应 policy 配置值
- **THEN** application 拒绝该 target 且 revision 不变；已经提交的相同 command replay 不得用推进后的 observedAt 重新解释首次 deadline

### Requirement: Invite 必须有界、定向、绑定有效目标且永远不是 gameplay credential
只有 VisitSession Owner MAY 为非 Owner、由 Account owner 在首次提交前证明当前 active 且可邀请的目标 PlayerID 创建或撤销 pending invite。Application MUST 在确认受信 actor 拥有 active PersonalWorld 与 active VisitSession 后才解析目标可用性，并 MUST 将 self、missing 与 inactive 统一拒绝为低敏 validation failure，不能推进 revision、创建 InviteID、保存 invite 或发布副作用。Account 读取依赖失败 MUST fail closed，不能伪装为目标不存在。相同 CommandID/fingerprint 已经提交时，store MUST 在重新读取目标可用性之前重放首次完整结果。Invite MUST 绑定 VisitSessionID、目标 Visitor、创建 revision 与绝对 expiry，使用独立 InviteID，并受 session lifecycle/expiry、pending invite 上限和稳定 command identity 约束。Invite MUST NOT 包含 endpoint、socket、通用 gameplay ticket、admission bearer secret 或 Owner/Visitor 可转移权限；持有 InviteID 只允许目标 Visitor 请求 accept，不能直接 join 或执行 world command。

#### Scenario: Owner 创建目标邀请
- **WHEN** session open、未过期且 Owner 使用受信 AuthContext 邀请另一个由 Account owner 证明 active 的 Player
- **THEN** application 原子保存有界 pending invite，返回不具备 gameplay scope 或 admission 权限的 invite projection

#### Scenario: Owner 邀请自身
- **WHEN** target PlayerID 与受信 Owner actor 相同
- **THEN** application 返回统一 validation failure，revision、invite、membership 和网络副作用全部不变，且不需要查询 Account 目录

#### Scenario: 目标 Player 不存在或 inactive
- **WHEN** Account owner 对格式有效 target PlayerID 返回统一 unavailable
- **THEN** application 返回与 self 相同的低敏 validation failure，不创建伪邀请且不泄漏目标是否存在或停用

#### Scenario: 目标 Player 读取依赖失败
- **WHEN** MySQL 或 Account reader 无法权威证明 target available 或 unavailable
- **THEN** application 返回 dependency unavailable，VisitSession revision 与集合不变且不发布邀请

#### Scenario: 已提交邀请在目标状态改变后 replay
- **WHEN** 首次 CreateInvite 已提交，目标随后变为 inactive，并以相同 CommandID/fingerprint 重试
- **THEN** store 在重新读取目标可用性前重放首次 invite、snapshot 与 revision，不生成新 InviteID或第二次 mutation

#### Scenario: Visitor 邀请第三方
- **WHEN** Visitor 或非 Owner actor 尝试创建 invite、改变 capacity 或把 invite 转发给其他 Player accept
- **THEN** application 返回 forbidden/target mismatch，session revision、invite 与 membership 全部不变

#### Scenario: Pending invite 被撤销或到期
- **WHEN** Owner 撤销 matching pending invite，或 system expire command 精确匹配其 identity/deadline 且 observedAt 已到期
- **THEN** application 只移除该 pending invite 并推进一次 revision，不创建 membership 或 safe-return；已 accepted、错误 identity 或旧 deadline 不能删除当前资格

#### Scenario: 使用 invite 直接 join
- **WHEN** 调用方只提交 InviteID 而没有后续 admission owner 验证的 join qualification
- **THEN** join 被拒绝，invite 不被解释为 bearer credential且不创建 connection membership

### Requirement: Accept 必须原子校验资格、容量并创建 reservation
目标 Visitor accept pending invite 时，application MUST 从受信 AuthContext 取得 actor PlayerID、SessionID 与 epoch，并在同一 store transition 中验证 invite target/expiry、session open/expiry、Owner availability、current assignment 完整匹配、Visitor 尚无 active membership和 capacity。成功 accept MUST 把 invite 标记为 accepted、创建唯一 `reserved` membership、增加 revision，并返回绑定 VisitSession/Visitor/session lineage/assignment 与有界 expiry 的 AdmissionIntent。AdmissionIntent MUST NOT 自身授予连接权限，其 expiry MUST 不晚于 invite、session 与 current assignment lease。

#### Scenario: 目标 Visitor 接受合法 invite
- **WHEN** 目标 Visitor 在 invite/session/assignment 均有效且 capacity 可用时使用当前 AuthContext accept
- **THEN** store 只提交一个 reserved membership并返回非凭据 AdmissionIntent，重复相同 command 返回首次 replay 且不重复占用 capacity

#### Scenario: 非目标 Player 接受 invite
- **WHEN** AuthContext actor 与 invite target 不同，即使 payload 声明目标 Player、Owner、world 或 instance
- **THEN** application 只信任 AuthContext 与 store binding并返回 forbidden，invite 保持 pending且不泄漏 session membership

#### Scenario: Owner grace 期间接受 invite
- **WHEN** session 已进入 owner_grace 或 Owner grace deadline 已到达
- **THEN** 新 accept 被拒绝且不预占 capacity；既有 joined Visitor 是否继续只由当前 grace 状态决定

#### Scenario: Join reservation 到期
- **WHEN** reserved membership 到达 reservation deadline 且尚未完成受信 join
- **THEN** matching expire command移除该reservation并释放capacity，不生成safe-return directive，也不能用旧AdmissionIntent恢复资格

### Requirement: Join 必须依赖受信 admission qualification 并重新校验 epoch 与 assignment
Join MUST 只接受后续 admission verifier 已验证的 qualification，且 MUST 再次比较 VisitSessionID、Visitor PlayerID、Visitor SessionID/epoch、reserved membership、reservation/session expiry、session open/Owner available、完整 current AssignmentStamp 与新 ConnectionBindingID。成功 join MUST 原子把同一 membership 从 `reserved` 转为 `joined` 并保存 binding；invite、AdmissionIntent、通用 gameplay scope、客户端 world/instance/endpoint 字段均 MUST NOT 单独满足 join。

#### Scenario: 合法 qualification 完成 join
- **WHEN** verifier 确认 qualification且 reserved membership、session epoch、current assignment和所有 deadline仍匹配
- **THEN** membership 只转换一次为 joined，返回 Visitor role projection；同一 command/binding 重试 replay且不增加 capacity

#### Scenario: Session epoch 已变化
- **WHEN** Visitor token lineage 已失效或当前 epoch 高于 AdmissionIntent绑定 epoch
- **THEN** 旧 qualification 被拒绝且不能恢复 reserved/joined membership，也不能降级为普通 gameplay scope

#### Scenario: 客户端提交旧 InstanceID
- **WHEN** current assignment 已替换而 qualification或 payload仍引用旧 WorldInstanceID/endpoint
- **THEN** application 以 placement current 完整 stamp 拒绝 join 且不连接旧实例；后续带权威 assignment 变化证据的 system invalidation 关闭旧访问资格

### Requirement: Visitor leave、kick 与 reconnect 必须作用于精确 membership binding
Visitor MAY 只以自身受信身份 leave；Owner MAY kick 指定 Visitor；其他 actor MUST NOT 删除 membership。Joined Visitor disconnect MUST 仅在旧 SessionID/epoch/ConnectionBindingID 完整匹配时进入 `reconnecting` 并设置不晚于 session expiry 的绝对 deadline。Reconnect MUST 由同一 Visitor、匹配 session lineage/epoch、current assignment 与新 binding在 deadline前完成。Leave、kick 或 reconnect expiry MUST 删除一个 active membership、精确增加 revision，并为 joined/reconnecting Visitor产生封闭原因的 safe-return directive。

#### Scenario: Visitor 主动离开
- **WHEN** joined/reconnecting Visitor 以自身 AuthContext和匹配 binding请求 leave
- **THEN** application只删除该 Visitor membership、释放一个 capacity slot并返回 voluntary-leave safe-return directive，不影响其他成员

#### Scenario: Owner kick Visitor
- **WHEN** immutable Owner 对存在的 joined/reconnecting Visitor执行 kick
- **THEN** target membership被删除并返回 kicked directive；Visitor不能 kick Owner或其他 Visitor

#### Scenario: 旧 disconnect callback 晚到
- **WHEN** Visitor 已用新 binding reconnect，而旧 binding 的 disconnect/leave callback随后到达
- **THEN** command 返回 stale/replay且不能删除新 binding或改变 session revision

#### Scenario: Visitor reconnect grace 到期
- **WHEN** matching reconnect generation/binding/deadline到期且 Visitor仍为 reconnecting
- **THEN** 只移除该 Visitor、释放 capacity并返回 visitor-reconnect-expired directive，其他 membership和Owner状态保持不变

### Requirement: Owner disconnect grace 必须有绝对 deadline且不可发生 host succession
匹配当前 Owner SessionID/epoch/ConnectionBindingID 的 disconnect MUST 使 open VisitSession进入 `owner_grace`，保存唯一 grace generation与绝对 deadline；既有 joined/reconnecting membership MAY 暂时保留，但新 invite/accept/join MUST 停止。Owner在 deadline前使用actor仍为immutable Owner的当前有效AuthContext和新binding reconnect时 MUST恢复同一session为open，并以新的SessionID/epoch替换旧Owner auth binding。Expire command MUST 同时比较 expected revision、grace generation、旧 binding与deadline；Owner主动 close或matching grace到期 MUST terminal close session且 Visitor永不继承Owner。

#### Scenario: Owner 在 grace 内重连
- **WHEN** Owner在absolute deadline前携带有效session lineage/epoch与新binding恢复
- **THEN** VisitSession回到open，既有membership可以继续，旧timer/disconnect不能再次关闭session

#### Scenario: Owner grace 到期
- **WHEN** matching grace generation在deadline到达时仍有效且Owner未恢复
- **THEN** VisitSession以owner-unavailable关闭，全部joined/reconnecting Visitor获得safe-return directive，reserved membership被取消且Owner身份不转移

#### Scenario: 旧 grace timer 晚到
- **WHEN** Owner已reconnect或进入更新的grace generation后，旧generation timer触发
- **THEN** store返回stale/replay，session lifecycle、revision、membership与新binding保持不变

### Requirement: Close、expiry 与 dependency loss 必须产生确定性 safe-return 结果
Owner close、Owner grace expiry、session expiry、assignment change与无法安全恢复的运行态 dependency loss MUST terminal close VisitSession、取消全部 pending invite/reserved membership并移除 active index。对每个 joined/reconnecting Visitor，结果 MUST 生成按VisitorID稳定排序的SafeReturnDirective，绑定VisitSession与封闭reason，目标仅表达优先返回Visitor own PersonalWorld、不可用时safe entry；directive MUST NOT包含客户端可选world、endpoint、ticket或credential。相同command replay MUST返回首次完整directives，不重复增加revision或改变reason。

#### Scenario: Session absolute expiry
- **WHEN** observedAt达到session expiry且session尚未closed
- **THEN** 单一原子transition关闭session，所有可能仍在instance中的Visitor获得session-expired directive，持久PersonalWorld事实不被删除或回滚

#### Scenario: Current assignment 丢失或改变
- **WHEN** placement返回missing、expired或不同完整stamp，旧VisitSession无法证明仍绑定current writable instance
- **THEN** application 只在权威 missing、expired、非 active 或不同完整 stamp 证据下提交 assignment-changed close；placement 依赖报错返回 dependency 而不伪造 invalidation，旧访问也不把 Visitor 静默迁移到新 instance

#### Scenario: Safe-return 通知尚未发送
- **WHEN** core已经提交close与完整directives但transport通知或own-world启动尚未执行
- **THEN** store仍可按相同command replay确定性directives；core不声称网络side effect已原子完成

### Requirement: Owner 与 Visitor 权限必须显式且默认拒绝 gameplay mutation
VisitSession MUST只授权其控制面operation：Owner可以创建/撤销 pending invite、kick与close，Visitor只能对自身accept/join/leave/disconnect/reconnect，system只能执行带完整 deadline/generation/binding 或权威 current-assignment 查询条件的expire/assignment invalidation。Role projection MAY供后续interaction policy消费，但只有匹配当前 auth lineage 的 immutable Owner 可获得 Owner role，只有 `joined`/`reconnecting` membership 可获得 Visitor role，terminal session、pending invite 与 `reserved` membership MUST 返回 unspecified。Role projection MUST NOT授予Visitor转让世界、邀请第三方、修改世界配置、推进Owner关键任务、消费不可恢复唯一资源、提交奖励/结算事实或绕过PersonalWorld revision/fence。任何未由后续spec显式登记actor role、mutation owner、settlement owner、idempotency与transaction边界的gameplay command MUST默认拒绝。

#### Scenario: Visitor 执行 Owner world command
- **WHEN** Visitor membership有效但尝试归档世界、修改配置或推进仅Owner允许的关键事实
- **THEN** application返回forbidden，VisitSession不能覆盖PersonalWorld Owner/revision/fence且任何持久事实不改变

#### Scenario: Reserved membership 请求角色投影
- **WHEN** 目标 Visitor 已 accept invite 但尚未完成受信 join
- **THEN** role projection 返回 unspecified；只有 join 后的 matching session lineage 才返回 Visitor，旧 epoch 或不同 actor 默认拒绝

#### Scenario: Visitor 交互要求奖励和world mutation
- **WHEN** 未有独立interaction spec的请求同时要求修改Visitor player ledger与Owner PersonalWorld
- **THEN** 本capability拒绝顺序双写或伪原子成功，要求后续change明确两个owner、幂等与settlement边界

#### Scenario: 好友直接访问个人世界
- **WHEN** Owner与Visitor没有Party或Room但满足VisitSession条件
- **THEN** 系统只使用VisitSession membership，不创建Party、Room或ActivityInstance，也不让这些未来aggregate接管访问事实

### Requirement: Store 必须原子决议revision、稳定command identity与提交不确定性
VisitSessionStore MUST 对 active session create/resolve 与每个 state-changing transition 提供单一线性化点。Mutation MUST 携带正 expected revision、有界 CommandID 与规范 SHA-256 fingerprint；fingerprint MUST 包含 operation、existing VisitSessionID、可信 actor、目标 identity、binding，以及由调用方或业务命令明确提交的真实目标 deadline 等稳定 command 字段。Open create MUST 绑定目标 PersonalWorld、assignment、capacity 与 session lifetime，且 MUST NOT 包含重试时重新生成的 candidate VisitSessionID 或重新读取的 observedAt。公开 HTTP accept 的 reservation deadline 完全由服务端 observedAt、policy 与权威 invite/session/assignment/auth deadlines 派生时，该 candidate deadline MUST NOT 进入客户端 command fingerprint；相同 command 必须在领域 precondition 前由 store 决议 replay/conflict，并重放首次保存的完整 result 与较短 deadline，不能借重试时钟延长资格。Store MUST 先决议同 CommandID replay/conflict，再比较 active index/revision/current snapshot 并原子保存 target snapshot 与完整 result/directives。Outcome MUST 区分 created/existing、applied/replay、not-found、revision/idempotency/capacity/stale/invalid-state conflict、not-committed 与 commit-unknown；application MUST 拒绝矛盾 outcome/result 且 MUST NOT 自动重放 callback。

#### Scenario: Mutation response 丢失后重试
- **WHEN** 首次transition已提交但调用方未收到响应，并以相同CommandID/fingerprint重试且observedAt已经推进
- **THEN** store返回首次完整result/directives与replay，不再次推进revision、重复占capacity或改变deadline/reason

#### Scenario: HTTP accept 重试只推进服务端时钟
- **WHEN** 公开 HTTP accept 首次提交后以相同 actor lineage、VisitSession、invite、expected revision 与 CommandID 重试，唯一变化是 observedAt 推进并产生更晚 candidate reservation deadline
- **THEN** store 在当前领域 precondition 前重放首次 reservation、revision 与较短 deadline，不把时钟推进误判为客户端语义冲突

#### Scenario: 相同 CommandID 用于不同目标
- **WHEN** 同一 actor scope 内复用 CommandID 但 operation、target Visitor、binding、调用方提交的目标 deadline 或 current assignment 等稳定授权事实不同
- **THEN** store返回idempotency conflict且任何VisitSession事实不改变

#### Scenario: Commit 结果无法确认
- **WHEN** store无法证明transition是否提交
- **THEN** application返回commit-unknown与严格零伪成功结果，调用方只能复用相同command identity解析，不能换新CommandID盲目补写

#### Scenario: Store 返回矛盾 snapshot
- **WHEN** store声称applied/replay但返回错误Owner、world、assignment、revision、fingerprint或safe-return directives
- **THEN** application将其视为dependency defect fail closed，不向上游暴露部分有效membership或资格

### Requirement: VisitSession core 必须无基础设施独立验收且不得提前接线
VisitSession domain/application MUST 只依赖消费侧定义的 store、OwnedWorldReader、CurrentAssignmentReader、clock、ID generator 与受信 AuthContext/admission qualification，不得依赖 Gin、WebSocket、TCP socket、generated protocol、MySQL、Redis、Docker、mutable global、timer/goroutine 或 memory fallback。Production MAY 保留无法由包外直接构造的 `JoinQualification` 类型作为未来 verifier 的消费边界，但 public constructor MUST 等待独立 admission change；reference store、fake readers/clock/IDs 与 qualification 构造 fixture MUST 只存在于测试。Production Composition Root 在 VisitSession storage、world/visit protocol、admission 与 transport changes 完成前 MUST NOT 构造 VisitSession service、注册 cleanup task 或开放 world/visit API。

#### Scenario: 独立运行领域测试
- **WHEN** 测试identity/hydration、capacity race、invite/accept/join、leave/kick、Owner/Visitor reconnect、expiry、replay/conflict/commit-unknown与safe-return
- **THEN** 测试只构造纯Go对象，并能在unit、table、fuzz与race detector下验证全部路径而不启动listener或backend

#### Scenario: 正式服务端启动
- **WHEN** VisitSession core已存在但production store、admission credential与公开adapter尚未交付
- **THEN** 当前进程继续只运行既有已接线组件，不注入test store、不创建VisitSession Redis key或后台timer，也不宣称visit-world可用

#### Scenario: Redis 未来被清空
- **WHEN** 后续adapter的VisitSession/invite/membership运行态因Redis flush丢失
- **THEN** core契约要求访问安全结束并重新admission，PersonalWorld、PlayerState、资产与奖励持久事实不被伪造、删除或回滚

### Requirement: VisitSession mutation 必须原子保存邀请退役事实

VisitSession application service MUST 在首次成功 mutation 的 source 与 target snapshot 之间计算 pending invite retirement：source 中为 Pending、target 中不存在或不再 Pending 的 identity MUST 作为稳定排序、唯一且有界的 `retired_invites` 与 target snapshot、CommandID、fingerprint、operation payload 和 safe-return directives 原子保存。Store replay MUST 返回首次完整 retirement 集合；not-committed 与 commit-unknown MUST 不返回或发布部分集合。Result validation MUST 拒绝无效、重复、乱序或仍在 target 中保持 Pending 的 retirement。

#### Scenario: Owner 撤销 pending invite
- **WHEN** revoke command 首次提交并从 target snapshot 删除 matching pending invite
- **THEN** mutation result 原子保存该 invite 的完整非凭据 projection，application coordinator 向其 TargetVisitorID 发布 Retired push，重复相同 command replay 不推进 revision且只产生幂等同 identity tombstone

#### Scenario: Accept 或 deadline 退役 invite
- **WHEN** HTTP accept 把 pending invite 转为 Accepted，或 system deadline 精确删除到期 invite
- **THEN** 首次 result 保存对应 retirement，目标客户端立即失去 accept 能力；commit-unknown 不允许服务端伪造已发布结论

#### Scenario: Terminal close 清理多个 pending invite
- **WHEN** Owner close、Owner grace/session expiry、assignment invalidation 或 dependency loss terminal close VisitSession 并清除全部 pending invite
- **THEN** result 按 VisitSessionID/InviteID 稳定保存全部 retirement，coordinator 分别向各精确目标发布 tombstone，不从已经清空的 target snapshot 猜测接收者

#### Scenario: 解码历史 mutation result
- **WHEN** Redis 中已存在本 change 之前不含 `retired_invites` 字段的合法 replay result
- **THEN** codec 将缺失字段解释为空集合并保持旧 result 语义；新 result 的 retirement 集合必须确定性编码、解码和等价比较
