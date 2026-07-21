## 1. 权威恢复编排

- [x] 1.1 扩展 VisitSession result coordinator 窄端口，以稳定 system CommandID 解析并终态退役 world 当前的旧 assignment VisitSession，成功后统一消费完整 mutation result。
- [x] 1.2 修改 `VisitOpen`，只在首次 `OperationOpen/ErrorCodeStale` 后执行一次恢复，并以原始 Open CommandID 再解析；dependency、dependency defect 与 commit-unknown 不继续。

## 2. 自动化验证

- [x] 2.1 增加纯 Go tests，覆盖 current session 幂等、旧 assignment 退役、新 session current binding、safe-return、副作用去重、并发 Open 与提交不确定性。
- [x] 2.2 运行 VisitSession/application targeted tests、服务端全量 Go tests、协议 verify、OpenSpec strict 与 `git diff --check`。
- [x] 2.3 登记 `stale_open_reconcile` 封闭 metrics operation，并以 production observer 测试防止终态提交后观测 panic。
- [x] 2.4 仅在旧 Owner gameplay connection 仍存活时投递 assignment changed，保留旧会话 closed 与 Visitor safe-return 副作用。

## 3. 真实恢复验收

- [x] 3.1 保留现场 Redis 遗留旧 VisitSession，重启修复后的服务端并使用 Windows Development Player 验证首次 Open 成功、旧 session closed、新 session 绑定 current assignment、Open 后 UI 按权威 capability 禁用重复提交且无红字；重复 Open 幂等由 application tests 验证。

## 实现验收记录

- 2026-07-20 使用 Windows Development Player 与保留的 Redis 运行态执行恢复验收：旧会话 `vses_d710859336c193de7497e9cd06fdd312` 从 Open revision 1 收敛为 Closed revision 2，新会话 `vses_f486c4ec1c844f3e1ec13ed354efd77c` 以 revision 1 绑定 current assignment generation/fence 3。
- 修复后的服务端首次 Open 即成功；客户端呈现新会话且无红字，随后“开放访问”按服务端权威 action capability 禁用。Metrics 记录 `stale_open_reconcile/applied=1`、Open dispatch `ok=1`，且无 panic。
