## MODIFIED Requirements

### Requirement: Store 必须原子决议revision、稳定command identity与提交不确定性
VisitSessionStore MUST 对 active session create/resolve 与每个 state-changing transition 提供单一线性化点。Mutation MUST 携带正 expected revision、有界 CommandID 与规范 SHA-256 fingerprint；fingerprint MUST 包含 operation、existing VisitSessionID、可信 actor、目标 identity、binding，以及由调用方或业务命令明确提交的真实目标 deadline 等稳定 command 字段。Open create MUST绑定目标 PersonalWorld、assignment、capacity 与 session lifetime，且 MUST NOT包含重试时重新生成的 candidate VisitSessionID或重新读取的 observedAt。公开 HTTP accept 的 reservation deadline完全由服务端 observedAt、policy与权威invite/session/assignment/auth deadlines派生时，该 candidate deadline MUST NOT进入客户端 command fingerprint；相同command必须在领域precondition前由store决议replay/conflict，并重放首次保存的完整result与较短deadline，不能借重试时钟延长资格。Store MUST 先决议同 CommandID replay/conflict，再比较 active index/revision/current snapshot并原子保存target snapshot与完整result/directives。Outcome MUST 区分 created/existing、applied/replay、not-found、revision/idempotency/capacity/stale/invalid-state conflict、not-committed 与 commit-unknown；application MUST拒绝矛盾outcome/result且 MUST NOT自动重放callback。

#### Scenario: Mutation response 丢失后重试
- **WHEN**首次transition已提交但调用方未收到响应，并以相同CommandID/fingerprint重试且observedAt已经推进
- **THEN**store返回首次完整result/directives与replay，不再次推进revision、重复占capacity或改变deadline/reason

#### Scenario: HTTP accept 重试只推进服务端时钟
- **WHEN**公开HTTP accept首次提交后以相同actor lineage、VisitSession、invite、expected revision与CommandID重试，唯一变化是observedAt推进并产生更晚candidate reservation deadline
- **THEN**store在当前领域precondition前重放首次reservation、revision与较短deadline，不把时钟推进误判为客户端语义冲突

#### Scenario: 相同 CommandID 用于不同目标
- **WHEN**同一actor scope内复用CommandID但operation、target Visitor、binding、调用方提交的目标deadline或current assignment等稳定授权事实不同
- **THEN**store返回idempotency conflict且任何VisitSession事实不改变

#### Scenario: Commit 结果无法确认
- **WHEN**store无法证明transition是否提交
- **THEN**application返回commit-unknown与严格零伪成功结果，调用方只能复用相同command identity解析，不能换新CommandID盲目补写

#### Scenario: Store 返回矛盾 snapshot
- **WHEN**store声称applied/replay但返回错误Owner、world、assignment、revision、fingerprint或safe-return directives
- **THEN**application将其视为dependency defect fail closed，不向上游暴露部分有效membership或资格
