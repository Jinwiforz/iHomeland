# Server VisitSession Storage 规格

## Purpose

定义VisitSession可失效Redis运行态的schema owner、完整投影、原子create/CAS、命令重放、TTL、恢复、观测与production integration边界，并确保公开world/visit transport启用前可以在真实Redis故障与并发条件下独立验收。

## Requirements

### Requirement: VisitSession Redis schema 必须有明确 owner、边界与字段字典
系统 MUST 由 `visitsession` storage adapter 独占维护 VisitSession active-world index、完整 session snapshot 与 command replay/result 三类 versioned Redis key；每类 key MUST 通过共享 registry 登记稳定 name/pattern、owner、用途、TTL policy、最大 encoded bytes、恢复、清理、故障与低基数 metrics。Value MUST 使用稳定英文 field name 和确定性编码，完整字段、类型、中文短注释、必填规则以及 UTC 微秒/字节等单位 MUST 记录在 `docs/redis-keys.md`。Adapter MUST NOT 创建未被 `VisitSessionStore` 消费的 player membership index、通用 map cache、第二个 Redis client 或未登记 key。

#### Scenario: 共享 keyspace 构造 VisitSession key
- **WHEN** shared storage registry 合并 VisitSession definitions 并为合法 VisitSessionID、PersonalWorldID 或 CommandID 构造 key
- **THEN** key 使用登记的 `visitsession` owner namespace，默认日志只显示 pattern，value schema 与 encoded size 均受 registry 和 codec 验证

#### Scenario: Value schema 未知或超出预算
- **WHEN** adapter 读取 unknown schema version、重复/缺失 field、非法 enum/时间/identity 或超过登记大小的 value
- **THEN** 本次读取或 mutation 以 dependency defect fail closed，不返回部分 snapshot/result，也不把原始 key、value 或 command fingerprint 写入日志与 metrics

### Requirement: Production adapter 必须忠实实现 VisitSessionStore 完整投影
Adapter MUST 实现既有 `VisitSessionStore` 的 `Create`、`ResolveActive`、`FindByID` 与 `Commit`，并 MUST 使用领域公开 hydration/constructor 恢复完整 immutable owner/world/assignment、revision/lifecycle、invite、membership、auth/binding、deadline、AdmissionIntent 与 SafeReturnDirective。领域 MAY 增加只用于恢复完整 `AuthBinding` 与非凭据 `AdmissionIntent` 的严格 hydration constructor，但 MUST NOT 因此开放 AuthContext、credential 或 `JoinQualification` 构造。成功、existing、replay 和 found MUST 返回与首次提交完全等价且通过领域验证的完整结果；not-found、conflict、not-committed 与 commit-unknown MUST 返回严格零值结果。Infrastructure MUST NOT 重写领域 policy、放宽 unknown enum/identity、把 AdmissionIntent 变为 credential或依赖 generated protocol type。

#### Scenario: 重启后读取完整 VisitSession
- **WHEN** Redis 中保存一个包含 pending invite、reserved/joined/reconnecting membership、Owner grace 与完整 assignment stamp 的合法 snapshot，进程重建 adapter 后按 ID 或 active world 读取
- **THEN** adapter 返回可由领域成功 hydrate 的等价 snapshot，集合顺序稳定、时间保持 UTC 微秒且不会丢失 binding、deadline 或 immutable owner facts

#### Scenario: Redis projection 与索引互相矛盾
- **WHEN** active index 指向缺失/closed session，或 replay 的 command/fingerprint/operation/snapshot/revision/directives 与目标 session 不一致
- **THEN** adapter 报告 dependency defect，不修补、不删除、不猜测成功，也不向 application 暴露局部资格

### Requirement: Create 必须原子决议 command replay 与 world active 唯一性
`Create` MUST 在一个 owner Lua script 线性化点内先按全局 CommandID 决议相同 fingerprint 的首次完整 replay 或不同语义的 idempotency conflict，再检查 PersonalWorld active index，并原子写入 candidate snapshot、active index 与 create result。一个 PersonalWorld MUST 至多映射一个未关闭 VisitSession；并发 candidate、重复请求、进程重试或 response loss MUST NOT 创建第二个 active aggregate。已有 active snapshot MUST 返回 `existing` 且不得把当前 CommandID 伪记为已创建；矛盾或部分 key 状态 MUST 作为 dependency defect。

#### Scenario: 两个进程并发 Open 同一 PersonalWorld
- **WHEN** 两个 adapter 实例以不同 CommandID/candidate 同时为同一 PersonalWorld 调用 `Create`
- **THEN** 最多一个得到 `created`，另一个得到同一完整 active snapshot 的 `existing`，Redis 中只有一个 active index和一个被选中的 candidate

#### Scenario: Create 已提交但响应丢失
- **WHEN** Lua 已原子写入 candidate、index 与 result，但调用方只收到连接错误并以相同 CommandID/fingerprint 重试
- **THEN** 首次调用返回 commit-unknown，重试返回首次完整 `replay`；更换 fingerprint 返回 idempotency conflict且不会创建或替换 session

### Requirement: Commit 必须原子决议 replay、revision 与完整 target
`Commit` MUST 在一个 owner Lua script 线性化点内先决议 CommandID replay/conflict，再验证 session 存在、expected revision、不可变 facts、完整 result metadata 与允许的 target revision，并原子保存 target snapshot 和完整 mutation result。Terminal target MUST 在同一线性化点删除仅匹配该 VisitSessionID 的 active-world index；无 target probe MUST 只解析 replay、idempotency conflict、not-found、revision conflict 或 invalid-state 且不得写入 replay success。Capacity、stale 等领域 policy 结果 MUST 由 application 在生成 record 前决议，adapter 不得从 payload 重复实现领域状态机。Adapter MUST NOT 自动重试 mutation script、重新执行 application callback、替换 CommandID 或用客户端 cancel 推断未提交。

#### Scenario: 相同 expected revision 并发提交
- **WHEN** 两个不同 command 对同一 VisitSession revision 同时提交各自完整 target
- **THEN** 最多一个得到applied并把revision精确推进一次，另一个得到确定性revision conflict，snapshot、active index和replay不会出现部分写入或容量超卖

#### Scenario: Close 保存完整 safe-return replay
- **WHEN** terminal mutation 包含按 VisitorID 稳定排序的多个 SafeReturnDirective
- **THEN** script 原子保存 closed snapshot与完整result、删除matching active index；相同command重试返回首次全部directives且不改变reason、顺序或revision

#### Scenario: Mutation 结果无法确认
- **WHEN** script 请求可能已送达而客户端未收到合法 reply
- **THEN** adapter 返回 commit-unknown与严格零值结果，只有复用相同CommandID/fingerprint的后续调用可以通过owner replay record解析是否已提交

### Requirement: Redis TTL 必须晚于领域 expiry 且不能代替语义清理
Session snapshot、active index 与该 session 的全部 command replay MUST 使用由可信 clock 计算的 absolute physical expiry，且不得早于 VisitSession absolute expiry 加配置的有界 replay retention；微秒领域时间转换为 Redis 毫秒 expiry 时 MUST向后取整，禁止提前失效。Replay retention MUST 在启动时验证为 1 分钟至 24 小时。领域 invite/reservation/grace/reconnect/session deadline MUST 始终由 application command 和 snapshot 校验，key 尚存在不表示资格有效，key 自然过期也不得被报告为已经产生 safe-return side effect。Terminal transition MUST 主动移除 active index，但保留 terminal snapshot/result 至 physical expiry。

#### Scenario: Deadline 已到但 Redis key 尚存在
- **WHEN** invite、reservation、grace、reconnect或session领域deadline已经到达，而对应session key仍处于replay retention窗口
- **THEN** adapter只返回原始完整snapshot供application做显式expire/close决策，不因正TTL延长任何资格

#### Scenario: Session close 后调用方重试
- **WHEN** VisitSession在absolute expiry附近close且首次response丢失
- **THEN** terminal snapshot与此前全部command replay至少保留至session expiry后的配置retention，相同command仍能解析首次结果，active index已不可用于新访问

#### Scenario: 没有语义 cleanup 时 key 自然过期
- **WHEN** Redis物理TTL到达前没有后续Composition Root owner执行显式expiry command
- **THEN** key可以自然删除且所有旧访问资格fail closed，但系统不得声称已发送safe-return通知；公开transport启用前必须另行接入可取消、可等待的语义cleanup owner

### Requirement: Dependency failure、恢复与观测必须 fail closed
Adapter MUST 复用共享 Redis failure classifier，并由 owner-specific reply parser 区分可证明未写入的 not-committed 与无法确定写入阶段的 commit-unknown。发送前校验/取消以及固定 Lua `defect` 写入前分支 MUST 返回 not-committed；timeout、cancel、EOF、runtime script error、unknown 或矛盾 reply MUST 返回 commit-unknown。Redis unavailable、restart、flush、missing TTL、corrupt value、unknown script reply 或违反领域不变量时 MUST NOT 返回伪成功；flush 后不得从 MySQL、日志或 memory fallback 恢复旧 VisitSession、active index 或 replay，Redis 进程重启则只能读取 Redis 自身仍保留的合法运行态。日志只使用稳定 component/operation/outcome 和脱敏 key pattern，metrics label MUST 保持低基数且不得包含 PlayerID、VisitSessionID、CommandID、fingerprint、backend error text 或 value。

#### Scenario: Redis flush 后查询旧 session
- **WHEN** Redis flush删除全部VisitSession运行态后，进程继续运行或重启
- **THEN** Resolve/Find返回not-found或明确dependency结果，旧invite、membership、binding与admission intent不能复活，PersonalWorld持久事实不受影响

#### Scenario: Lua 返回未知形状
- **WHEN** Redis/script返回unknown code、非字符串field、重复field或与outcome矛盾的payload
- **THEN** owner parser将其视为dependency defect并增加固定operation/outcome指标，不记录无界backend文本或任何敏感value

### Requirement: Storage 集成不得提前开放 VisitSession runtime
Production adapter MUST 接受调用方注入的 Redis client、shared keyspace、clock 与 observer，并在未来接线时复用 Composition Root 持有的对应共享实例和 storage verification harness。Adapter MUST 在初始化阶段验证 definitions/config；它不得创建独立 client、listener、timer/goroutine、global mutable、memory fallback 或公开 constructor 绕过领域资格。当前 change MAY 把 definitions 纳入正式 storage runtime 并把 adapter 纳入 storage integration verification，但在独立 world admission、HTTP/WSS/TLS-TCP 与完整 service graph change 完成前，Composition Root MUST NOT 构造 VisitSession service、注册语义 cleanup task 或开放 world/visit route。

#### Scenario: 仅完成 storage change 后启动服务
- **WHEN** shared storage registry与verification已经包含VisitSession definitions，但后续admission/transport changes尚未完成
- **THEN**进程仍只开放既有诊断面，不注册world/visit handler/listener，不签发admission，也不宣称visit-world可用

#### Scenario: Docker contract 与并发验收
- **WHEN** storage verification 在真实 Redis 上执行 unit/contract/integration、multi-adapter concurrency、response-loss 与 restart/flush/corruption，并独立执行 race 验收
- **THEN** 所有 `VisitSessionStore` outcome、完整 replay、TTL、索引一致性与 fail-closed 边界可重复通过，测试不依赖固定端口或遗留 key
