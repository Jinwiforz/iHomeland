## 1. 配置与 WorldInstance runtime 基础

- [x] 1.1 为 Placement lease、进程内 runtime 容量与 semantic deadline 容量增加严格配置、默认值、YAML 示例及边界测试，续约分数等可推导值不暴露为冗余配置
- [x] 1.2 实现有硬上限、按完整 AssignmentStamp 幂等 Start/Stop 的进程内 RuntimeController，并覆盖并发启动、capacity、stale stop、重复 stop 与关闭清理测试
- [x] 1.3 在 production 启动时生成每进程 RuntimeNodeID，构造 placement.Service，保证 identity/assignment 日志脱敏且 reference/memory store 不进入正式 graph

## 2. 有界 semantic deadline owner

- [x] 2.1 实现单 worker、有界 priority queue、稳定 task key 与原位替换/取消，不为每个 entry 创建 goroutine/timer
- [x] 2.2 为 assignment renew/expiry 与 VisitSession session/invite/reservation/Owner grace/Visitor grace 定义严格 task payload、稳定 CommandID 派生和容量不变量
- [x] 2.3 使用 fake clock 验证排序、唤醒、重复登记、旧 revision/generation、取消、queue boundary、panic/error监督与 bounded shutdown

## 3. Own-world activation 与 placement reconciliation

- [x] 3.1 将 worldentry bootstrap 的只读 assignment 端口改为窄 activation 端口，确保成功响应始终包含 runtime-ready且store-confirmed的current active assignment
- [x] 3.2 实现单进程 bootstrap 对 missing、local current、foreign/orphan predecessor、starting、expired、commit-unknown 与并发重复调用的 Ensure/Replace/reconcile 策略
- [x] 3.3 登记并执行本地 assignment lease renew；stale/missing/replaced/expired 时精确停止 runtime、撤销旧 target 资格并把权威 invalidation 交给 VisitSession coordinator
- [x] 3.4 覆盖 bootstrap activation、response loss replay、runtime failure/capacity、旧进程 assignment replacement、renew failure和stale writer的unit/table/race测试

## 4. TCP 连接生命周期与精确 target

- [x] 4.1 为认证完成的 TCP connection 增加不可变 lifecycle view、窄 lifecycle sink 与低基数 close class，确保 payload/backend文本不进入事件
- [x] 4.2 扩展 registry/publisher 以按 VisitSessionID + VisitorID 或精确 ConnectionID 投递，并在 safe-return 入队前原子进入 returning/closing、拒绝旧 target mutation
- [x] 4.3 实现 Owner/Visitor disconnect coordinator，处理 current binding、active旧连接防抢占、Owner grace自动恢复、Visitor显式reconnect及safe-return/draining callback抑制
- [x] 4.4 覆盖 disconnect/reconnect/close/invalidation/draining race、晚到callback、wrong binding、旧session epoch与slow consumer的table/race测试

## 5. VisitSession 结果编排与跨通道副作用

- [x] 5.1 抽取由 worldentry 与 TCP application 共同消费的窄 VisitSession coordinator 接口，所有 mutation/replay 走统一 result validation、deadline登记和effect路径
- [x] 5.2 将 invite、Owner availability、terminal close 与 assignment change 映射为既有 WSS push，只按受信 Player/session target 投递
- [x] 5.3 将 committed VisitSession revision 映射为当前 gameplay binding 的完整 snapshot push，并把每条 SafeReturnDirective 映射为目标 Visitor 的精确 TCP safe-return
- [x] 5.4 处理 not-committed、commit-unknown、replay、离线目标、queue full与投递竞态，保证网络失败不回滚事实、不改报command失败且有界去重
- [x] 5.5 覆盖每种result→effect映射、wrong channel/type/target、重复replay、多Visitor稳定顺序和response/push serialized writer测试

## 6. Deadline、恢复与 dependency-loss 闭环

- [x] 6.1 从每个 committed/replayed/resolved VisitSession snapshot 幂等登记或替换全部有效 semantic deadlines，并在到期时调用对应领域 command
- [x] 6.2 在 bootstrap、HTTP accept/admission、TCP connect、Visit command与snapshot read边界执行lazy reconciliation，不增加Redis global index或生产SCAN
- [x] 6.3 对assignment change提交VisitSession invalidation并发送既有notice/safe-return；对Redis key missing或dependency error分别执行fail-closed与禁止伪造事实的分支
- [x] 6.4 覆盖invite/reservation/session/grace精确边界、timer replay、进程重建、Redis flush、缺失snapshot和assignment replacement恢复测试

## 7. Composition Root、生命周期与观测

- [x] 7.1 按endpoint/codec/inert registry→storage-backed domain→runtime/coordinator/publisher→handler/listener顺序接入production graph，并让任一构造、supervise或bind失败完整逆序回滚
- [x] 7.2 调整readiness与draining顺序，有界停止新入口、deadline/effect生产、本地runtime、WSS/TCP、HTTP和storage，不残留task、connection或runtime
- [x] 7.3 为runtime、lease、deadline、lifecycle与delivery增加低敏低基数metrics/log outcome，并验证不记录credential、payload、IP、invite或full AssignmentStamp
- [x] 7.4 按项目注释规范补齐所有新增/修改 package、类型、字段、接口与生命周期不变量注释，并更新structure tests防止transport/domain反向依赖

## 8. 竖切验收与长期文档

- [x] 8.1 扩展production Composition Root +真实MySQL/Redis+临时TLS的Go wire integration harness，覆盖own-world bootstrap/admission/snapshot和完整visit invite/accept/join/leave/kick/close
- [x] 8.2 在integration harness覆盖Owner/Visitor断线恢复、deadline safe-return、stale admission、assignment replacement、Redis flush、process reconstruction与资源清理
- [x] 8.3 运行受影响package unit/table/fuzz/race、`go test -count=1 ./...`、`go vet ./...`、storage verify与`git diff --check`，修复全部失败
- [x] 8.4 更新architecture、file structure、roadmap、server README及相关验收说明，只声明本竖切完成并保持`qualify-server-v1`与Unity gate未完成
- [x] 8.5 执行`openspec validate complete-server-personal-world-slice --strict`与`openspec validate --all --strict`并修复全部文档/规格问题
