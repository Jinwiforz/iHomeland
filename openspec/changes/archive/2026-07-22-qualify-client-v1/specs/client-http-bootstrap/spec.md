## MODIFIED Requirements

### Requirement: Session owner 必须原子管理 token lineage 与短期凭据

App Scope MUST 只有一个 Session owner 保存 account/session/token snapshot 与单调本地 generation。Register/login 成功 MUST 在完整响应验证后原子替换 snapshot；refresh MUST single-flight，并且只有发起时的 generation 仍为 current 才能提交轮换结果。Password MUST 只存在于当前调用参数，access token、ticket 与 admission MUST NOT 持久化；refresh token MUST 只通过登记的 secure session store 以当前 Windows 用户范围的 OS 保护持久化，且 MUST NOT 写入 Unity 序列化资产、PlayerPrefs、日志、exception、metrics 或普通 `ToString()`。Store record MUST 只包含 schema version、environment/build binding、refresh lineage 与验证恢复所需的最小低敏 metadata；写入 MUST 使用同目录原子 replacement，读取、unprotect、schema/binding 验证或 replacement 任一步失败 MUST fail closed。同一 production local profile MUST 只有一个跨进程 writer owner；第二个进程不能并发读取、轮换或覆盖同一 refresh lineage。成功 register/login/refresh MUST 先安全提交新 record 再发布可继续认证的 current snapshot；logout、forget、forced invalidation、明确 unauthenticated、refresh commit-unknown 与安全存储损坏 MUST 删除旧 record。未提供受支持平台 adapter 时，进程重启 MUST 回到未认证状态且不得降级为明文或自制加密。

#### Scenario: 旧 refresh 在新 login 后返回

- **WHEN** refresh 尚未完成时新的 register/login 已提交更高 generation
- **THEN** 旧 refresh 结果被丢弃、旧持久 lineage 被删除，且不能覆盖新 session、token、epoch 或 secure record

#### Scenario: 并发 refresh 的后续等待方取消

- **WHEN** 已存在 single-flight refresh，后续调用方在共享请求完成前取消自己的等待
- **THEN** 后续调用方收到 caller-cancelled，首个调用拥有的请求继续执行且服务端只收到一次 refresh

#### Scenario: 当前 session 被判定未认证

- **WHEN** authenticated operation 返回有效 `AUTH_UNAUTHENTICATED`，或 logout 得到有效 204
- **THEN** Session owner 原子清除 token、secure record 与尚未交付的 ticket，递增 generation 并拒绝继续使用旧 snapshot

#### Scenario: Logout 提交结果未知

- **WHEN** logout 因 timeout、取消或 transport failure 未获得可判定响应
- **THEN** Session owner 进入 unresolved 状态、清除当前 snapshot、secure record 与短期 ticket，并阻止新的 authenticated operation，直到新的 register/login 或本地 forget 解决 lineage

#### Scenario: Refresh 提交结果未知

- **WHEN** refresh 因 timeout、取消、transport failure、停止或不可判定响应而无法确认服务端是否已轮换 token
- **THEN** Session owner 进入 unresolved 状态、撤销旧 snapshot 并删除 secure record，阻止旧 access/refresh token 继续使用，直到新的 register/login 或本地 forget 解决 lineage

#### Scenario: Connection ticket 已过期或 session 已轮换

- **WHEN** ticket 超过 `expiresAtMs`，或其来源 generation 不再 current
- **THEN** ticket 不能交给后续 channel 使用且不得自动申请、持久化或复用替代 ticket

#### Scenario: 新进程恢复有效 refresh lineage

- **WHEN** Windows Player 在匹配 environment binding 下读取并解封合法 current secure record
- **THEN** Session owner 在发布 authenticated snapshot 前只执行一次 refresh，以轮换后的 access/refresh 建立新 generation 并原子替换 record；旧 refresh 不再留在磁盘或内存 owner 中

#### Scenario: 安全记录损坏或来自其他环境

- **WHEN** record 无法解封、schema 不支持、完整性失败或 environment binding 不匹配
- **THEN** 客户端删除或隔离该精确 record、保持未认证并返回稳定低敏 restore failure，不发送 refresh、不猜测账号且不回退明文存储

#### Scenario: 第二个进程打开同一 production profile

- **WHEN** 一个 Windows Player 已拥有 default secure profile 的 writer lifetime，第二个进程尝试初始化同一 profile
- **THEN** 第二个进程得到稳定 profile-in-use 结果且不读取、刷新、删除或覆盖现有 record；UI 提供退出而不伪装为普通账号密码错误
