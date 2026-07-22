## MODIFIED Requirements

### Requirement: 产品入口必须从安全会话恢复或本地登录显式启动

客户端 MUST 在 App Scope 全部初始化成功后先进入唯一 `RestoringSession` route/state，并对当前环境的 secure session store 执行一次有界恢复；没有 record、平台不支持、record 无效或服务端明确拒绝 lineage 时 MUST 清理恢复状态并打开唯一 `Login` route。合法 record MUST 先完成冻结 version/config bootstrap 与一次 refresh 轮换，再启动唯一 control run，并在该 run 首次进入 `Connected` 后显式请求进入自己的 PersonalWorld。Register/login intent 仍 MUST 由玩家在 Login 显式提交，且先完成同一 version/config bootstrap 再调用唯一 Session owner。若 control 在 `Connected` 前稳定失败，客户端 MUST 保留已安全提交的 Session、停止进入世界并呈现可重试的低敏连接失败，不能在无法接收定向 invite push 时伪装产品流程已就绪。Password MUST 只存在于当前输入与调用参数，页面隐藏、失败、停止或提交完成后 MUST 清除，不得进入 View State、日志、Scene、Prefab 或异常文本。

#### Scenario: 新进程没有可恢复 session

- **WHEN** Windows Player 完成 App Scope 初始化且 secure session store 不存在 current record
- **THEN** 恢复 owner 不发送 refresh、ticket、world 或 channel 请求，唯一 active screen 从 RestoringSession 收敛到 Login

#### Scenario: 新进程恢复 session 并进入自己的世界

- **WHEN** secure record、version/config、refresh、control start 和 own-world flow 依次成功
- **THEN** Session owner 以轮换后的唯一 lineage 提交新 generation，Experience 无需密码离开恢复状态并进入 OwnWorld，View 与 Scene 不取得 token、ticket 或 admission

#### Scenario: 恢复 lineage 被拒绝

- **WHEN** refresh 明确返回 unauthenticated、record 损坏/不匹配，或恢复提交结果未知
- **THEN** 客户端 fail closed 删除不可继续使用的 record、关闭候选 route/channel/target并显示 Login 或稳定低敏恢复失败，不自动循环 refresh

#### Scenario: 登录并进入自己的世界

- **WHEN** 玩家提交合法登录信息且 bootstrap、login、control start 和 own-world admission 依次成功
- **THEN** Session owner 保存唯一认证事实与安全 refresh lineage，Experience 离开 Login 并进入 own-world loading/scene 流程，View 与 Scene 不取得 token、ticket 或 admission

#### Scenario: 登录失败

- **WHEN** bootstrap 不兼容、凭据被拒绝、限流、timeout 或 transport failure
- **THEN** Login 保持唯一 active screen、恢复可重试输入并显示稳定低敏错误，password 被清除且客户端不自动重复认证

### Requirement: Session、连接与业务失败必须映射为稳定产品状态

Experience MUST 将 protocol incompatibility、unauthenticated/session invalidation、permission、validation、revision conflict、not found、rate limit、dependency unavailable、transport/timeout 和内部失败映射为封闭低敏 UI failure。Session invalidation MUST 取消当前 presentation/recovery/scene intent、关闭产品 routes 和 Scene Scope、删除不可继续使用的 secure lineage 并回到 Login。Session 仍有效的 channel 中断 MUST 从唯一 recovery snapshot 映射为 `RecoveringControl`、`RecoveringWorld` 或稳定 `ConnectionLost`；UI MUST NOT 根据按钮、延时、frame tick 或单个 socket callback伪造恢复成功。

#### Scenario: Session 在 Visiting 时失效

- **WHEN** forced logout 或 session invalidation 使唯一 Session owner 清除当前 lineage
- **THEN** Experience 取消 target/recovery/scene/page intent、关闭旧 HUD/overlay、卸载内容 Scene并回到 Login，任何旧 generation completion 都不能重新认证或打开 world route

#### Scenario: Revision conflict

- **WHEN** Owner 或 Visitor mutation 返回 revision conflict
- **THEN** UI 不重发 mutation，只显示刷新中的只读状态，并允许既有 Service 按契约执行一次权威 snapshot 收敛

#### Scenario: 未知内部错误

- **WHEN** Host、Scene 或 application action 返回未登记异常
- **THEN** 玩家只看到通用可恢复错误和允许的 retry/logout action，Console/Player.log 不包含 password、token、ticket、admission、endpoint 或完整 payload

#### Scenario: Gameplay server 非预期断开

- **WHEN** OwnWorld 或 Visiting 的 TLS/TCP connection 因 transport、protocol 或 backpressure terminal failure 关闭，而 Session 仍有效
- **THEN** channel 事件立即使旧 world mutation 与 Scene/HUD 失效，Experience 显示唯一 RecoveringWorld 状态并由应用层恢复协调者尝试一次有界权威重建；显式 target 切换、safe-return、logout 或 shutdown 的关闭不得误报为 server failure

#### Scenario: 自动恢复耗尽后显式重新连接

- **WHEN** automatic recovery 已稳定失败为 ConnectionLost，服务端恢复后玩家提交一次 reconnect
- **THEN** 客户端复用同一恢复规则等待 control readiness，再以新 generation 重建权威 target、Scene 与 HUD，全部提交后才关闭 modal；不得创建并行 recovery、轮询 channel state、使用固定延时或保留失效 gameplay/Scene

#### Scenario: Control 单独恢复

- **WHEN** WSS 瞬时中断而 current gameplay 与 Session 仍健康
- **THEN** Experience 只呈现非阻断或明确受限的 RecoveringControl 状态，禁用依赖 control 完整性的邀请动作；WSS Connected 且完整 snapshot 收敛后恢复动作能力，不重建健康 Scene/gameplay

#### Scenario: 重连时 access 已到期但 refresh 仍有效

- **WHEN** automatic 或 manual recovery 需要授权 HTTP operation，唯一 Session owner 的 access 已越过绝对 expiry而 refresh lineage 仍有效
- **THEN** 首个授权 HTTP operation先共享一次 single-flight refresh，再以新 session generation 和新 access 继续 control ticket、world admission 与 Scene/HUD 恢复；不得先发送已知过期 access、显示内部错误 key、要求玩家重复登录或循环重试

#### Scenario: 到期刷新无法确认提交结论

- **WHEN** access 到期触发的 refresh 返回 timeout、transport 或 commit-unknown
- **THEN** Session owner 按既有 refresh 契约使旧 token lineage 与 secure record fail closed，停止原始授权 operation并显示低敏恢复失败；不得继续使用旧 access 或猜测 refresh 未提交

#### Scenario: World revision 不变但 assignment 升代

- **WHEN** 服务端恢复后返回相同 PersonalWorld revision、相同持久世界事实，以及更高 assignment generation 和新的 WorldInstance
- **THEN** 客户端接纳新的 runtime assignment 并完成 admission；同 generation 的 instance/endpoint 漂移、generation 回退或 lease deadline 回退仍必须 fail closed
