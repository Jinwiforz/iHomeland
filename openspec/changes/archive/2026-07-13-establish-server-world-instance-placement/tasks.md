## 1. Placement 值对象与不变量

- [x] 1.1 创建 `server/internal/placement` package、稳定错误分类，以及严格校验的 `WorldInstanceID`、`RuntimeNodeID`、`AssignmentGeneration` 和 `FencingToken` 值类型，并按注释规范补齐中文 Go doc
- [x] 1.2 实现 immutable assignment stamp、lease、`starting`/`active` snapshot 构造与 hydration，拒绝零值、未知 phase 和矛盾时间，并区分 expired hydration 与调用期资格
- [x] 1.3 为 ID、generation/fence、lease boundary、snapshot round-trip、malformed hydration 和安全格式化补齐 table/fuzz tests

## 2. Store 与 runtime 消费侧契约

- [x] 2.1 定义 PlacementStore 的 resolve/acquire/activate/renew/revoke/replace/qualify 输入、原子条件、outcome 与 commit phase，确保所有条件操作携带完整 stamp
- [x] 2.2 定义幂等 RuntimeController、clock、WorldInstance ID generator 及 service result 契约，区分 placement 已提交、runtime cleanup 失败与提交未知
- [x] 2.3 在 `_test.go` 实现并发 reference store、deterministic clock/ID、fake runtime controller 与 fault injection，覆盖单调 high-watermark、replay/conflict 和 malformed outcome

## 3. 按需启动、续租与写资格

- [x] 3.1 实现 `EnsureActive` 的 resolve/acquire-start-activate 编排，使并发调用只启动一个 candidate，并对 existing active 与 starting in-progress 返回稳定结果
- [x] 3.2 实现 runtime start/cancel/activate commit-unknown 的条件清理与精确 resolve 收敛，禁止 runtime 自行宣布 active
- [x] 3.3 实现完整 stamp 的 lease renew 与 `WriteFence` qualification，拒绝 expiry 边界、stale token、部分 identity 和已失效 qualification
- [x] 3.4 添加并发 ensure、ready/replacement race、start failure、renew expiry、lease replacement 和 stale writer 的 unit/fuzz/race tests

## 4. 休眠、重建与迁移

- [x] 4.1 实现 expected-stamp sleep 的 revoke-before-stop 编排，并显式返回 revoke 已提交但 runtime stop/cleanup 失败的结果
- [x] 4.2 实现预生成 successor identity 的 rebuild/migration replace，确保 predecessor 先失效、successor 从 starting 重新 ready/activate，且不确定重试收敛到同一 candidate
- [x] 4.3 添加重复 sleep、stale sleep、同节点 rebuild、跨节点 migration、replace commit-unknown、旧 renew/ready/write 延迟到达和最多一个 writer 的并发测试

## 5. 边界与质量门

- [x] 5.1 审核 package 依赖、导出声明与日志/错误安全，确认 placement 不持有 PersonalWorld 内容、endpoint、socket 或 production memory fallback，且所有手写注释符合 `docs/code-comment-convention.md`
- [x] 5.2 使用项目 Go 工具入口执行 format、unit、targeted fuzz、race 与 vet，并检查正式 Composition Root 未接线 placement service 或后台任务
- [x] 5.3 执行 OpenSpec strict、`git diff --check` 与仓库卫生检查，确认未生成或提交协议产物、本地缓存、配置密钥、MySQL/Redis/Unity 变更
- [x] 5.4 修复归档前复盘发现的 expired snapshot hydration、Replace 外部 replay 与 activate 失败清理缺陷，并补齐对应回归和部分成功测试
- [x] 5.5 统一 proposal/design/spec/README 与实现术语，移除冗余或失实说明，同步主 specs 后重新执行 strict 与仓库卫生检查
