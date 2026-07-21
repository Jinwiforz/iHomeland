## 1. 协议与领域结果

- [x] 1.1 为 `VisitInviteState` 增加公开 `RETIRED` 值，更新协议 fixture/生成一致性测试且不新增 message ID 或 route。
- [x] 1.2 扩展 VisitSession `MutationResult` 与 Redis codec，原子保存、验证、确定性编码并 replay 稳定排序的 retired invite 集合，同时兼容缺失该字段的历史 result。
- [x] 1.3 在统一 commit 边界从 source/target snapshot 计算 Pending retirement 差集，覆盖 revoke、expire、accept 与全部 terminal close 操作。

## 2. 服务端定向发布

- [x] 2.1 让 result coordinator 从 committed/replayed retirement 集合构造 state 为 `RETIRED` 的 message 2100，并只向各 TargetVisitorID 发布。
- [x] 2.2 增加 domain/store/codec/application tests，覆盖单项撤销、接受、到期、多邀请关闭、重复 replay、commit-unknown 与定向接收者。

## 3. Unity 客户端收敛

- [x] 3.1 扩展 mapper 与 `VisitSessionService` 应用 Retired tombstone，允许过期 tombstone 删除 exact identity，并同步清除不可变 inbox/selection/capability。
- [x] 3.2 让 `WorldAdmissionCoordinator` 对白名单明确 accept 拒绝退役 exact 残留邀请、保持 OwnWorld 且发布 `InviteUnavailable`；暂时性失败与 commit-unknown 保留邀请。
- [x] 3.3 将 `InviteUnavailable` 映射为明确中文低敏说明，增加 Service/Coordinator/Experience/UI 回归测试，证明失败不触发 own-world admission 或 Scene 重载。

## 4. 全量验收

- [x] 4.1a 运行协议 verify、服务端 targeted/full Go tests、OpenSpec strict 与 `git diff --check`，结果记录于 `manual-acceptance.md`。
- [x] 4.1b 使用包含本 change 最终源码的 Unity 工程运行全量 EditMode/PlayMode tests，并确认 Console 无非预期错误。
- [x] 4.2 构建新的服务端与 Windows Development Player，使用两个真实客户端验证 create/revoke、close、多残留项和点击失效邀请的权威收敛，并将日志与版本路径记录于 `manual-acceptance.md`。
