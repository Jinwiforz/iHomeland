## 1. 安全会话存储契约

- [x] 1.1 在 Application/Session 定义 secure record、schema/environment binding、稳定 storage outcome 和窄 `IClientSecureSessionStore`，覆盖 validity、size、redaction 与默认不可格式化测试
- [x] 1.2 在 Infrastructure 增加 Windows DPAPI CurrentUser adapter，严格封送 `CryptProtectData`/`CryptUnprotectData`、归零 native/managed secret buffer，并覆盖 native failure、corruption、oversize 与 dispose
- [x] 1.3 实现 owner-specific 路径、同目录 temp/flush/atomic replace、受控 ACL、精确 temp cleanup 和 production profile named mutex，拒绝跨卷写入、目录枚举删除与第二个 writer
- [x] 1.4 增加非 Windows Unsupported adapter与仅 Debug/Local 可用的资格 data-profile隔离，验证Release不解析profile参数且不回退PlayerPrefs/明文/自制加密

## 2. Session 原子持久化与启动恢复

- [x] 2.1 重构 `ClientSessionCoordinator` 候选提交，使 register/login/refresh 先安全 replace refresh record、再发布 current snapshot，并在持久化失败时按 login/refresh 不同提交结论 fail closed
- [x] 2.2 将 logout、forget、forced invalidation、unauthenticated、refresh replay/commit-unknown、record corruption 与 environment mismatch 接入精确 record 删除和 session generation 失效
- [x] 2.3 以可控 fake store/HTTP/clock覆盖旧 refresh 晚到、新 login竞争、single-flight waiter取消、replace/delete失败、进程中断点和 credential/exception/log脱敏
- [x] 2.4 实现一次性 `ClientSessionRestoreCoordinator` 与有界 `Read -> bootstrap -> refresh -> replace -> commit` 状态机，区分 NotAvailable、Restored、Rejected、Unresolved、StorageFailure和Stopped
- [x] 2.5 将 restore coordinator 接入 AppComposition/AppLifetime，保证启动顺序、失败回滚、逆序停止、重复bootstrap和迟到callback都不能创建第二个Session/control/target

## 3. 通道健康与恢复状态机

- [x] 3.1 定义 WSS/gameplay generation-bound health、`RecoveryTargetDescriptor`、恢复阶段/terminal result和不可变snapshot，确保不包含credential、endpoint或第二份业务事实
- [x] 3.2 实现唯一 `ClientConnectionRecoveryCoordinator` 的single-flight intent、总deadline、classified有限attempt、取消与四重session/recovery/target/scene generation提交gate
- [x] 3.3 接入control/gameplay typed lifecycle事件，区分unexpected transport、protocol、auth、safe-return、session invalidation、explicit close与shutdown，确保每代只发布一次terminal事实
- [x] 3.4 用纯C#竞态矩阵覆盖automatic/manual并发、terminal后迟到completion、deadline、shutdown、重复事件和健康channel不被重建

## 4. WSS 独立恢复与断线窗口收敛

- [x] 4.1 扩展 control channel只读generation/health通知，不改变其一次性ticket、有限backoff、sequence与App Scope ownership
- [x] 4.2 control进入Recovering时冻结依赖inbox/hint完整性的动作；新generation提交前原子失效旧control-only projection、selection和callback
- [x] 4.3 control Connected后通过健康gameplay请求并提交current world/VisitSession完整snapshot，验证revision/assignment/member收敛且不重建健康gameplay/Scene
- [x] 4.4 覆盖断线期间invite create/revoke PUSH缺失、旧Retired迟到、旧generation invalidation、snapshot conflict和再次断开，证明客户端不保留stale可点邀请也不伪造未收到邀请

## 5. Gameplay OwnWorld 与 Visitor 权威恢复

- [x] 5.1 在稳定OwnWorld/Visiting提交时维护无credential recovery descriptor，并在unexpected gameplay disconnect时先关闭旧mutation、input、HUD/Scene binding
- [x] 5.2 实现OwnWorld恢复计划：重新bootstrap、签发新ticket/admission、connect、snapshot和Scene commit，接受同world revision下更高assignment generation并拒绝stale identity
- [x] 5.3 实现Visitor恢复计划：校验VisitSession/membership/deadline、签发`RECONNECT` admission、建立pending gameplay并以现有typed `VisitReconnectCommand`作为唯一首帧
- [x] 5.4 对RECONNECT response/snapshot执行identity、role、revision、assignment和deadline验证；not-found/expired/closed/safe-return明确转入ReturningOwnWorld而非JOIN或OwnWorld假成功
- [x] 5.5 修复Visitor断线后公开revision缺口：WorldAdmission幂等冻结并返回权威`visitRevision`，JOIN/RECONNECT首帧只消费该值，禁止客户端算术猜测
- [x] 5.6 覆盖Owner/Visitor断开竞态、grace边界、control与gameplay同时失败、heartbeat timeout、safe-return/forced logout优先级和三轮重复恢复的socket/task/pending守恒

## 6. 产品恢复状态与 UI/Scene 生命周期

- [x] 6.1 扩展Experience不可变View State与产品route，表达RestoringSession、RecoveringControl、RecoveringWorld和terminal ConnectionLost，不让页面保存恢复策略或channel引用
- [x] 6.2 接线startup restore：无record/Unsupported进入Login，合法lineage无密码进入OwnWorld，rejected/corrupt/unknown清理候选状态并显示稳定低敏结果
- [x] 6.3 接线control degraded能力：保持健康gameplay显示，禁用依赖control完整性的invite动作，并在snapshot收敛后按current collections恢复
- [x] 6.4 接线gameplay recovery：立即撤销旧Scene/input，成功提交新Scene/HUD后关闭modal；automatic terminal后才开放manual retry且两者复用同一intent owner
- [x] 6.5 以EditMode覆盖View State/action matrix、重复按钮、route cancellation、UI reload、stale callback和错误message key，以PlayMode覆盖真实UI Toolkit/uGUI、focus/cursor、Scene replacement与teardown

## 7. 客户端资格 manifest 与工具链

- [x] 7.1 在 `shared/contracts/fixtures/client-qualification` 定义schema-versioned manifest与evidence schema，登记稳定ID、group、execution、mandatory、budget、build profile、outcome和owner，并增加严格loader/closed-schema测试
- [x] 7.2 建立manifest、自动runner和人工evidence双向completeness，拒绝重复/未知/缺失/hidden/skipped场景及contract/build digest不匹配的stale证据
- [x] 7.3 将现有Development builder收敛为统一Windows build owner，显式支持Development/Release、共用scene/identity/baseline验证，并扫描Release不存在资格profile、测试账号、故障注入或Development bypass
- [x] 7.4 创建 `tools/client-qualification/client-qualification.ps1`，实现锁定Editor解析、严格参数、run-id目录、阶段/全局deadline、稳定输出和ignored `.local/client-qualification` ownership
- [x] 7.5 编排proto clean generation、静态编译、EditMode、PlayMode、Development/Release build、Player smoke、soak、evidence、OpenSpec/docs/governance与cleanup，任何mandatory missing/skipped均失败
- [x] 7.6 生成低敏schema-versioned report，记录manifest/contract/build digest、工具版本、阶段/outcome/耗时/evidence/cleanup，拒绝credential、账号、runtime identity、endpoint、PID、绝对路径和原始日志
- [x] 7.7 覆盖工具success、Unity test/build failure、Player crash、timeout、Ctrl+C、stale evidence和cleanup failure，精确终止自有PID并只通过storage owner清理当前run
- [x] 7.8 增加闭合 `diagnose -Scenario` 开发反馈入口，复用automatic registry定向运行Unity fixtures、只构建Development Player并生成非证据双Player清单，覆盖未知场景、selector完整性与正式evidence隔离
- [x] 7.9 增加真实 Development Player 服务端进程替换诊断，以精确 listener PID、同一storage run和产品重连入口自动验证断线提交、权威OwnWorld收敛、资源基线与进程清理，不产生资格证据

## 8. 自动资格矩阵与五分钟 soak

- [x] 8.1 把secure store、session restore、WSS/TCP独立恢复、低/重复/冲突revision、assignment replacement、stale callback、backpressure、Scene/UI lifecycle与redaction测试映射到manifest自动场景
- [x] 8.2 由用户使用锁定Unity Editor刷新新脚本资产并审阅必要 `.meta`，确认本实现过程不手工生成、覆盖或批量修改 `.meta`
- [x] 8.3 实现Development-only低敏资格诊断计数，固定AppRoot、channel generation、heartbeat/recovery intent、pending、dispatcher、subscription与Scene owner词汇，Release完全移除入口
- [x] 8.4 实现至少五分钟自动soak，跨多个heartbeat周期执行三轮channel disconnect/recovery、route open/close和Scene replacement，并断言每轮资源回到manifest基线
- [x] 8.5 从无generated/cache/build输入运行protocol parity、全部EditMode/PlayMode、Development与Release build/smoke，检查第二次generation无差异、Player.log无未观察异常或敏感信息

## 9. 真实双客户端与故障验收

- [x] 9.1 由资格入口准备隔离local storage/server/build run和两个Development data profile，输出不含账号密码/identity的版本化人工场景清单与evidence模板
- [x] 9.2 使用两个真实账号验收login/restore、OwnWorld、invite/accept/JOIN、leave、kick、close、重新邀请和Owner/Visitor权限，逐步核对role/target/revision/member/invite/route/Scene/action
- [x] 9.3 分别验收WSS单断、OwnWorld gameplay单断、Visitor grace内RECONNECT、Player退出重启、服务端停止/重启和session失效，确认无旧socket/member/invite/modal/Scene callback残留
- [x] 9.4 在同一contract/build digest上登记全部人工结果，运行完整资格入口并确认mandatory gate、report、cleanup和连续重复执行的稳定outcome一致

## 10. 文档、审计与归档门

- [x] 10.1 新增 `docs/client-v1-qualification.md`，记录manifest版本、contract/build digest、执行命令、自动/人工证据、Development/Release、soak、cleanup、非目标与明确qualified结论
- [x] 10.2 更新roadmap、client architecture/integration/UI、file structure、workflow、technology versions、client README和必要顶层文档，说明secure store owner、恢复状态机、control gap fail-closed和资格回归规则
- [x] 10.3 按注释规范审计全部新增C#、PowerShell、manifest/report字段，补齐安全、线程、单位、deadline、generation、native资源和失败语义注释，移除无owner TODO、魔法值与吞错
- [x] 10.4 运行OpenSpec strict、protocol verify、静态编译、Unity全量测试、两种Windows build、资格completeness、redaction与Git generated/cache/secret审计，修复所有失败
- [x] 10.5 在全部mandatory证据属于同一冻结输入、tasks/specs/docs与report一致且工作区无未确认contract漂移后同步长期specs，并验证全部delta requirement与主spec逐段一致
- [x] 10.6 仅在最终工作区审计无未确认漂移后归档 `qualify-client-v1`
