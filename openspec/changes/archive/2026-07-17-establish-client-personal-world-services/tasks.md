## 1. Invite accept HTTP capability

- [x] 1.1 增加 `acceptVisitInvite` 的冻结 operation metadata、不可变 request/reservation model 与严格 path/body/response codec
- [x] 1.2 扩展 `IClientHttpApi`/`ClientHttpApi`，只暴露 VisitSessionID、InviteID、expected revision 与 idempotency key 的强类型 accept 方法
- [x] 1.3 在 `SessionCoordinator` 中实现 current access token、session generation、expiry 与迟到结果保护的 accept wrapper
- [x] 1.4 扩展 HTTP fixtures、baseline 与 contract tests，覆盖第十个 operation、非法 path identity、response mismatch、commit-unknown 和敏感信息边界

## 2. 权威投影 Services

- [x] 2.1 定义 PersonalWorld、WorldAssignment、VisitSession、invite、control hint、safe-return 与 flow 的不可变 application models 和稳定 apply/failure results
- [x] 2.2 实现 `PersonalWorldService` 的 identity/enum/assignment 校验、最高 revision、同 revision 幂等/冲突和完整 assignment 清除
- [x] 2.3 实现 `VisitSessionService` 的完整 snapshot gate、role/membership 投影、按 expiry 清理且有硬上限的 invite inbox 与 control hint 收敛
- [x] 2.4 将 WSS assignment/invite/availability/closed PUSH 与 TLS/TCP world/visit/safe-return PUSH 接入对应 Service，并保持 control hint 不冒充完整 gameplay snapshot
- [x] 2.5 增加 malformed identity、低/同 revision、内容冲突、assignment generation、invite expiry/overflow 与乱序跨通道 EditMode tests

## 3. Visit command 与 world target 编排

- [x] 3.1 为 gameplay channel 增加不泄漏 admission credential 的 pending JOIN/RECONNECT 窄入口，并验证 purpose、expected revision 与首帧约束
- [x] 3.2 在 `VisitSessionService` 实现 Owner open/create/revoke/kick/close 与 Visitor leave 的强类型 command，用 current role/revision 在写入前 fail closed
- [x] 3.3 实现 `WorldAdmissionCoordinator` 的状态、单一 intent、`targetGeneration`、session generation 与并发转换 gate
- [x] 3.4 实现 bootstrap、own-world admission/connect、world snapshot 与 `OwnWorld` 提交流程
- [x] 3.5 实现 invite accept、稳定双 idempotency identity、visit admission/connect/join、visit/world snapshot 与 `Visiting` 提交流程
- [x] 3.6 实现 leave、terminal snapshot、assignment loss 与 safe-return 驱动的 mutation 关闭、旧连接清理和 `ReturningOwnWorld` 流程；owner unavailable control hint 只请求收敛
- [x] 3.7 增加 role policy、revision conflict、并发 intent、caller cancel、迟到 response/PUSH、session invalidation、return 失败和 shutdown 竞态 tests

## 4. App Scope、文档与验收

- [x] 4.1 在 `AppComposition`/`AppCompositionResult` 中显式接线 Services 与 coordinator，登记 subscriber 撤销和逆序停止且保持默认初始化零网络副作用
- [x] 4.2 按项目规范复核全部手写 C# XML 注释、credential/identity 脱敏、不可变快照、并发所有权和无 service locator/event bus 边界
- [x] 4.3 更新客户端架构、接入、文件结构与路线图文档，记录已落地 Services、十个 HTTP operation 及仍留给 UI/Scene/恢复 change 的边界
- [x] 4.4 运行静态客户端编译、协议 verify、OpenSpec strict、`git diff --check` 与 generated/meta 跟踪边界检查
- [x] 4.5 运行 Unity EditMode、PlayMode 与 Windows Development build，确认状态机 tests 通过、空 BootstrapScene 可离线启动关闭且 Player.log 无 credential/未观察异常
