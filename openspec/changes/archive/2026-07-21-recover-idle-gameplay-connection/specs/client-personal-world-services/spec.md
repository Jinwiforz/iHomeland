## ADDED Requirements

### Requirement: 显式连接恢复必须在统一 deadline 内提交 terminal snapshot

PersonalWorld presentation owner MUST 把 control ready、own-world bootstrap/admission、gameplay connect、world snapshot、scene synchronization 与 route commit 作为同一 generation 的显式恢复事务，并设置冻结的总 deadline。事务 MUST 恰好提交成功或稳定失败 snapshot；超时、caller cancellation、dependency failure、session invalidation 与 shutdown MUST 撤销未提交 target 和 transport，UI MUST NOT 无限停留在 `EnteringOwnWorld`、`ResolvingOwnWorld` 或 busy 状态。恢复事务新建的 control run MUST 在首次进入 `Connected` 前可由该事务撤销，进入 `Connected` 后 MUST 转交 App Scope owner，关闭 `ConnectionLost` route MUST NOT 取消已提交的 control run。系统 MUST NOT 使用延时重试、frame tick 或本地猜测修正权威 flow。

#### Scenario: 重连中 gameplay response 永不返回

- **WHEN** 用户在 ConnectionLost 页面点击重试，control 已连接但 gameplay connect 或 world snapshot 在总 deadline 前未完成
- **THEN** 当次 intent 被撤销，可能建立的 gameplay generation 被关闭，flow 与 UI 提交稳定可重试失败，重试按钮重新可操作

#### Scenario: 用户连续点击重试

- **WHEN** 第一笔恢复 intent 仍在执行时再次提交重试
- **THEN** presentation intent gate 稳定拒绝第二笔请求，不签发第二组 ticket/admission、不创建并行 socket，也不覆盖第一笔 terminal snapshot

#### Scenario: 恢复成功后旧失败回调迟到

- **WHEN** 新 generation 已完成 own-world snapshot 与 scene/route commit，旧 generation 的 timeout、disconnect 或 UI cancellation 随后到达
- **THEN** generation gate 丢弃旧结果，UI 继续显示已提交 OwnWorld，不回退到 ConnectionLost 或 EnteringOwnWorld

#### Scenario: 重连成功后关闭 ConnectionLost 页面

- **WHEN** control run 已进入 `Connected`，own-world Scene/HUD 已提交，presentation 关闭 `ConnectionLost` route 并由 route lifecycle 取消页面 token
- **THEN** 已提交的 control run 保持存活，Router 只保留当前 WorldHUD，旧 modal 不得在下一帧因页面取消而重新打开
