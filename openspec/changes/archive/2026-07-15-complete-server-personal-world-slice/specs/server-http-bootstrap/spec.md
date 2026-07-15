## MODIFIED Requirements

### Requirement: World bootstrap 与准入签发必须由窄应用编排派生权威事实
Transport-independent world-entry application MUST 拥有 `BootstrapOwnWorld`、`AcceptVisitInvite` 与 `IssueWorldAdmission` 三个公开编排用例。Bootstrap MUST 确保认证 Player 唯一 primary PersonalWorld 存在，并通过 production Placement owner 幂等确保本进程可承载的 current active assignment；只有 runtime ready 且 assignment 已原子发布 active 后才返回 world 与 assignment，不得返回缺少 assignment 的成功投影。Invite accept MUST 由 VisitSession owner 验证目标 actor、invite、revision、capacity、owner availability 和 current assignment，并把 reservation deadline 限制为 session、invite、VisitSession、assignment 与配置上限的最早值。Admission issuance MUST 从认证 session、own-world 或 current Visitor membership、完整 current AssignmentStamp 及受信 TLS_TCP endpoint 派生既有 WorldAdmission binding；payload MUST 只能选择 own-world 或 VisitSessionID。

#### Scenario: 首次查询 own-world bootstrap
- **WHEN** 有效 HTTPS actor 尚无 primary PersonalWorld 且请求 bootstrap
- **THEN** application 幂等创建唯一 primary world、启动并发布唯一 current active assignment，再返回该 actor 的安全投影；不接受客户端指定 owner/world

#### Scenario: Bootstrap 启动 runtime 失败
- **WHEN** primary PersonalWorld 已存在但 runtime capacity、placement dependency、commit状态或ready条件无法证明active assignment
- **THEN** bootstrap返回既有稳定dependency/capacity error并省略伪成功结果，不签发credential、不留下可写starting assignment或memory fallback

#### Scenario: 并发重复 bootstrap
- **WHEN** 同一 Player 对相同 primary PersonalWorld并发或在response丢失后重复请求bootstrap
- **THEN** Placement原子决议并返回同一current active assignment，不启动第二个WorldInstance或改变已提交generation/fence

#### Scenario: 接受临近到期 invite
- **WHEN** 目标 Visitor 以正确 expected revision 接受 pending invite 且 invite、session 或 assignment 的剩余寿命短于默认 reservation lifetime
- **THEN** VisitSession owner 使用所有权威 deadline 的最早值创建一次 reservation，不由 handler 自行延长或猜测 expiry

#### Scenario: Visitor 请求 world admission
- **WHEN** bearer actor 在指定 VisitSession 中具有 current `reserved` 或 `reconnecting` membership
- **THEN** application 分别派生 `VISITOR/JOIN` 或 `VISITOR/RECONNECT` binding，并把 expiry 限制到 session、membership、assignment lease 和 admission policy 最早 deadline

#### Scenario: Visitor 缺少 membership
- **WHEN** actor 只有 invite、已过期 reservation 或不属于指定 VisitSession
- **THEN** operation 返回既有 `VISIT_MEMBERSHIP_REQUIRED` 或更具体稳定错误，不把 invite、bearer 或 GAMEPLAY scope 提升为 world admission
